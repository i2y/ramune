package ramune

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// jsEscaper performs single-pass JS string literal escaping.
// Null bytes are escaped to prevent JSC's UTF8CString from truncating the string.
var jsEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\x00", `\x00`)

// installBunCompat registers Bun-compatible APIs.
// Must be called with rt.mu held.
func (r *Runtime) installBunCompat() error {
	r.bunSrv = &bunServerState{rt: r}

	if err := r.registerFuncLocked("__go_bun_file_read", goBunFileRead); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_bun_file_write", goBunFileWrite); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_bun_file_size", goBunFileSize); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_bun_password_hash", goBunPasswordHash); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_bun_password_verify", goBunPasswordVerify); err != nil {
		return err
	}

	// Bun.serve() start: JS passes handler code, Go manages HTTP.
	// Requests are queued and processed inside the event loop tick
	// to avoid concurrent JSC access.
	if err := r.registerFuncLocked("__go_bun_serve_start", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("serve: port required")
		}
		port, _ := args[0].(float64)
		// Second arg: whether WebSocket handlers are configured.
		if len(args) >= 2 {
			if ws, ok := args[1].(bool); ok && ws {
				r.bunSrv.wsEnabled = true
			}
		}
		actualPort, err := r.bunSrv.start(int(port))
		if err != nil {
			return nil, err
		}
		return float64(actualPort), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_bun_serve_stop", func(args []any) (any, error) {
		r.bunSrv.stop()
		return nil, nil
	}); err != nil {
		return err
	}

	// No drain/respond callbacks needed — HTTP requests are processed
	// directly in the event loop tick via processHTTPRequests().

	// Install WebSocket support.
	if err := r.installWebSocket(); err != nil {
		return err
	}

	// __go_ws_upgrade(reqId) — upgrade an HTTP request to WebSocket.
	if err := r.registerFuncLocked("__go_ws_upgrade", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("ws_upgrade: reqId required")
		}
		reqID, _ := args[0].(float64)
		id := int(reqID)

		r.bunSrv.upgradeMu.Lock()
		pu, ok := r.bunSrv.upgradeRequests[id]
		r.bunSrv.upgradeMu.Unlock()
		if !ok {
			return false, nil
		}

		connID, err := r.bunSrv.wsMgr.upgrade(pu.w, pu.r)
		if err != nil {
			pu.done <- 0
			return false, nil
		}

		pu.done <- connID
		return float64(connID), nil
	}); err != nil {
		return err
	}

	return r.execLocked(bunCompatJSSource())
}

// --- HTTP Server ---

type bunServerState struct {
	rt        *Runtime
	mu        sync.Mutex
	listener  net.Listener
	server    *http.Server
	requests  chan pendingHTTPReq
	pending   map[int]chan httpResponse
	nextID    int
	gcCounter int
	wsEnabled bool       // true when Bun.serve() was called with websocket handlers
	wsMgr     *wsManager // WebSocket connection manager

	// Cached JSC references for direct function calls (avoids JS parsing per request).
	handleFastFn uintptr // JSObjectRef for __bunHandleFast
	handleFn     uintptr // JSObjectRef for __bunHandle

	// upgradeRequests maps request IDs to pending upgrade state.
	upgradeMu       sync.Mutex
	upgradeRequests map[int]*pendingUpgrade
}

// pendingUpgrade holds the HTTP writer/request for a pending WebSocket upgrade.
type pendingUpgrade struct {
	w    http.ResponseWriter
	r    *http.Request
	done chan int // receives connID (or 0 on error)
}

type httpResponse struct {
	Status  int
	Headers map[string]string
	Body    string
}

type pendingHTTPReq struct {
	ID          int
	Method      string
	URL         string
	HeadersJSON string // pre-marshaled off the JSC thread
	Body        string
}

func (s *bunServerState) start(port int) (int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return 0, err
	}

	// Disable Go's automatic GC while the HTTP server is running if configured.
	// Go's concurrent GC corrupts JSC's internal property tables under load.
	if s.rt.gcConfig.DisableAutoGC {
		debug.SetGCPercent(-1)
	} else if s.rt.gcConfig.GCPercent != 0 {
		debug.SetGCPercent(s.rt.gcConfig.GCPercent)
	}

	s.mu.Lock()
	s.listener = ln
	s.requests = make(chan pendingHTTPReq, 1024)
	s.pending = make(map[int]chan httpResponse)
	s.nextID = 0
	s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		// Pre-marshal headers to JSON off the JSC thread.
		headers := make(map[string]string, len(r.Header))
		for k := range r.Header {
			headers[strings.ToLower(k)] = r.Header.Get(k)
		}
		hdJSON, _ := json.Marshal(headers)

		s.mu.Lock()
		s.nextID++
		id := s.nextID
		respCh := make(chan httpResponse, 1)
		s.pending[id] = respCh
		s.mu.Unlock()

		// If this is a WebSocket upgrade request, store the writer/request
		// so the JS upgrade callback can perform the actual handshake.
		isUpgrade := s.wsEnabled && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		if isUpgrade {
			s.upgradeMu.Lock()
			if s.upgradeRequests == nil {
				s.upgradeRequests = make(map[int]*pendingUpgrade)
			}
			pu := &pendingUpgrade{w: w, r: r, done: make(chan int, 1)}
			s.upgradeRequests[id] = pu
			s.upgradeMu.Unlock()

			s.requests <- pendingHTTPReq{
				ID:          id,
				Method:      r.Method,
				URL:         r.URL.String(),
				HeadersJSON: string(hdJSON),
				Body:        string(body),
			}

			// Wait for upgrade to complete (or for a normal response).
			connID := <-pu.done

			s.upgradeMu.Lock()
			delete(s.upgradeRequests, id)
			s.upgradeMu.Unlock()

			if connID > 0 {
				// Upgrade succeeded — connection is now managed by wsManager.
				// Don't write anything to the ResponseWriter; it's been hijacked.
				s.mu.Lock()
				delete(s.pending, id)
				s.mu.Unlock()
				return
			}

			// Upgrade was not called or failed; fall through to normal response.
			resp := <-respCh

			s.mu.Lock()
			delete(s.pending, id)
			s.mu.Unlock()

			for k, v := range resp.Headers {
				w.Header().Set(k, v)
			}
			if resp.Status > 0 {
				w.WriteHeader(resp.Status)
			}
			w.Write([]byte(resp.Body))
			return
		}

		s.requests <- pendingHTTPReq{
			ID:          id,
			Method:      r.Method,
			URL:         r.URL.String(),
			HeadersJSON: string(hdJSON),
			Body:        string(body),
		}

		resp := <-respCh

		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()

		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		if resp.Status > 0 {
			w.WriteHeader(resp.Status)
		}
		w.Write([]byte(resp.Body))
	})

	s.server = &http.Server{Handler: mux}
	go s.server.Serve(ln)

	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (s *bunServerState) stop() {
	// Close all WebSocket connections first.
	if s.wsMgr != nil {
		s.wsMgr.closeAll()
	}
	// Re-enable Go's automatic GC.
	debug.SetGCPercent(s.rt.gcConfig.GCPercent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
	for id, ch := range s.pending {
		ch <- httpResponse{Status: 503, Body: "server stopped"}
		delete(s.pending, id)
	}
}

func (s *bunServerState) respond(id int, resp httpResponse) {
	s.mu.Lock()
	ch, ok := s.pending[id]
	s.mu.Unlock()
	if ok {
		ch <- resp
	}
}

// processRequests processes pending HTTP requests by calling the JS handler.
// Must be called with rt.mu held (inside event loop tick).
func (s *bunServerState) processRequests(r *Runtime) {
	if s == nil || s.requests == nil {
		return
	}
	r.drainUnprotectQueue()

	if len(s.requests) == 0 {
		return
	}

	interval := s.rt.gcConfig.GCInterval
	if interval <= 0 {
		interval = 2000
	}

	processed := 0
	for i := 0; i < 256; i++ {
		select {
		case req := <-s.requests:
			s.handleSingleRequest(r, req)
			r.drainUnprotectQueue()
			processed++
		default:
			goto done
		}
	}
done:
	// Run JSC GC periodically to collect JS objects created by frameworks.
	s.gcCounter += processed
	if s.gcCounter >= interval {
		s.gcCounter = 0
		r.jsGarbageCollect(r.ctx)
	}
}

// ensureHandlerCached caches JS function references for direct calls.
// Must be called with rt.mu held.
func (s *bunServerState) ensureHandlerCached(r *Runtime) {
	if s.handleFastFn != 0 {
		return
	}
	global := r.jsContextGetGlobalObject(r.ctx)

	fastName := r.jsStringCreateWithUTF8CString("__bunHandleFast")
	s.handleFastFn = r.jsObjectGetProperty(r.ctx, global, fastName, 0)
	if s.handleFastFn != 0 {
		r.jsValueProtect(r.ctx, s.handleFastFn)
	}
	r.jsStringRelease(fastName)

	handleName := r.jsStringCreateWithUTF8CString("__bunHandle")
	s.handleFn = r.jsObjectGetProperty(r.ctx, global, handleName, 0)
	if s.handleFn != 0 {
		r.jsValueProtect(r.ctx, s.handleFn)
	}
	r.jsStringRelease(handleName)
}

func (s *bunServerState) handleSingleRequest(r *Runtime, req pendingHTTPReq) {
	r.drainUnprotectQueue()
	s.ensureHandlerCached(r)

	// Build arguments as JSC values and call the handler function directly.
	// This avoids JS source parsing and compilation per request.
	methodJS, _ := r.goToJS(req.Method)
	urlJS, _ := r.goToJS(req.URL)
	bodyJS, _ := r.goToJS(req.Body)

	// Parse pre-marshaled headers JSON to a JS object.
	hdCode := r.jsStringCreateWithUTF8CString("(" + req.HeadersJSON + ")")
	headersJS := r.jsEvaluateScript(r.ctx, hdCode, 0, 0, 0, 0)
	r.jsStringRelease(hdCode)
	if headersJS == 0 {
		headersJS = r.jsValueMakeNull(r.ctx)
	}

	var result uintptr
	if s.wsEnabled && s.handleFn != 0 {
		reqIDJS := r.jsValueMakeNumber(r.ctx, float64(req.ID))
		fnObj := r.jsValueToObject(r.ctx, s.handleFn, 0)
		args := []uintptr{reqIDJS, methodJS, urlJS, bodyJS, headersJS}
		result = r.jsObjectCallAsFunction(r.ctx, fnObj, 0, uint64(len(args)), args, 0)
	} else if s.handleFastFn != 0 {
		fnObj := r.jsValueToObject(r.ctx, s.handleFastFn, 0)
		args := []uintptr{methodJS, urlJS, bodyJS, headersJS}
		result = r.jsObjectCallAsFunction(r.ctx, fnObj, 0, uint64(len(args)), args, 0)
	} else {
		// Fallback to eval (first request before cache is ready).
		code := `__bunHandleFast("` + escJS(req.Method) + `","` + escJS(req.URL) + `","` + escJS(req.Body) + `",` + req.HeadersJSON + `)`
		jsStr := r.jsStringCreateWithUTF8CString(code)
		result = r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, 0)
		r.jsStringRelease(jsStr)
	}

	if result == 0 {
		s.respond(req.ID, httpResponse{Status: 500, Body: "handler error"})
		return
	}

	raw := r.jsValueToGoString(result)

	// WebSocket upgrade path: if JS called server.upgrade(), the request
	// has been upgraded and we should not send a normal HTTP response.
	if raw == "__upgrade__" {
		// The upgrade was handled by __go_ws_upgrade inside __bunHandle.
		// The pendingUpgrade.done channel was already signaled.
		// Send a dummy response to unblock the HTTP goroutine for non-upgrade case.
		return
	}

	// Async path: __bunHandle stores the Promise in a single global.
	// This is safe because processRequests calls handleSingleRequest
	// sequentially while holding the JSC lock — no concurrent access.
	if raw == "__async__" {
		asyncKey := "__resp" + strconv.Itoa(req.ID)
		setupCode := `globalThis.__bunPendingPromise.then(` +
			`function(v){globalThis['` + asyncKey + `']=__bunExtract(v);},` +
			`function(e){globalThis['` + asyncKey + `']='500\n{}\n'+String(e);});` +
			`delete globalThis.__bunPendingPromise;`
		setupJS := r.jsStringCreateWithUTF8CString(setupCode)
		setupResult := r.jsEvaluateScript(r.ctx, setupJS, 0, 0, 0, 0)
		r.jsStringRelease(setupJS)

		if setupResult == 0 {
			cleanJS := r.jsStringCreateWithUTF8CString("delete globalThis.__bunPendingPromise")
			r.jsEvaluateScript(r.ctx, cleanJS, 0, 0, 0, 0)
			r.jsStringRelease(cleanJS)
			s.respond(req.ID, httpResponse{Status: 500, Body: "async setup failed"})
			return
		}

		// Run event loop ticks until the Promise resolves or timeout.
		// Unlike the old tight loop (1000 iterations), this respects
		// setTimeout delays by sleeping between ticks when timers are pending.
		checkCode := `globalThis['` + asyncKey + `']`
		tickJS := r.jsStringCreateWithUTF8CString("__eventLoop.tick()")
		checkJS := r.jsStringCreateWithUTF8CString(checkCode)
		nextDelayJS := r.jsStringCreateWithUTF8CString("__eventLoop.nextDelay()")

		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			// Drain async I/O managers so their callbacks can fire.
			if r.procMgr != nil {
				r.procMgr.processEvents(r)
			}
			if r.sockMgr != nil {
				r.sockMgr.processEvents(r)
			}

			r.jsEvaluateScript(r.ctx, tickJS, 0, 0, 0, 0)
			checkResult := r.jsEvaluateScript(r.ctx, checkJS, 0, 0, 0, 0)

			if checkResult != 0 && !r.jsValueIsNull(r.ctx, checkResult) && !r.jsValueIsUndefined(r.ctx, checkResult) {
				raw = r.jsValueToGoString(checkResult)
				cleanJS := r.jsStringCreateWithUTF8CString("delete globalThis['" + asyncKey + "']")
				r.jsEvaluateScript(r.ctx, cleanJS, 0, 0, 0, 0)
				r.jsStringRelease(cleanJS)
				break
			}

			// Check next timer delay and sleep if needed.
			delayResult := r.jsEvaluateScript(r.ctx, nextDelayJS, 0, 0, 0, 0)
			if delayResult != 0 {
				ms := r.jsValueToNumber(r.ctx, delayResult, 0)
				if ms > 0 {
					d := time.Duration(ms) * time.Millisecond
					if d > 100*time.Millisecond {
						d = 100 * time.Millisecond
					}
					time.Sleep(d)
				}
			}
		}

		r.jsStringRelease(tickJS)
		r.jsStringRelease(checkJS)
		r.jsStringRelease(nextDelayJS)

		if raw == "__async__" {
			cleanJS := r.jsStringCreateWithUTF8CString("delete globalThis['" + asyncKey + "']")
			r.jsEvaluateScript(r.ctx, cleanJS, 0, 0, 0, 0)
			r.jsStringRelease(cleanJS)
			raw = "500\n{}\nasync handler timeout"
		}
	}

	// Parse "status\nheadersJSON\nbody" format directly into struct.
	parts := strings.SplitN(raw, "\n", 3)
	resp := httpResponse{Status: 200}
	if len(parts) >= 1 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			resp.Status = n
		}
	}
	if len(parts) >= 2 && parts[1] != "" {
		json.Unmarshal([]byte(parts[1]), &resp.Headers)
	}
	if len(parts) >= 3 {
		resp.Body = parts[2]
	}

	s.respond(req.ID, resp)
}

func escJS(s string) string {
	return jsEscaper.Replace(s)
}

// hasActive returns true if the server is running or WebSocket connections are active.
func (s *bunServerState) hasActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	serverRunning := s.server != nil
	s.mu.Unlock()
	if serverRunning {
		return true
	}
	if s.wsMgr != nil && s.wsMgr.hasActive() {
		return true
	}
	return false
}

// --- File APIs ---

func goBunFileRead(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("file: path required")
	}
	path, _ := args[0].(string)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func goBunFileWrite(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("write: path and data required")
	}
	path, _ := args[0].(string)
	data, _ := args[1].(string)
	return float64(len(data)), os.WriteFile(path, []byte(data), 0o644)
}

func goBunFileSize(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("file: path required")
	}
	path, _ := args[0].(string)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return float64(info.Size()), nil
}

func goBunPasswordHash(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("password.hash: password required")
	}
	password, _ := args[0].(string)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return string(hash), nil
}

func goBunPasswordVerify(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("password.verify: password and hash required")
	}
	password, _ := args[0].(string)
	hash, _ := args[1].(string)
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil, nil
}

func bunCompatJSSource() string {
	return strings.TrimSpace(`
(function() {
	// Pre-compiled request handler — avoids re-parsing JS on every request.
	globalThis.__bunExtract = function(r) {
		var s = r.status || 200;
		var hd = {};
		if (r.headers) {
			if (typeof r.headers.forEach === 'function') {
				r.headers.forEach(function(v, k) { hd[k] = v; });
			} else if (typeof r.headers === 'object') {
				hd = r.headers;
			}
		}
		var b = '';
		if (typeof r.body === 'string') b = r.body;
		else if (r._body) b = r._body;
		return s + '\n' + JSON.stringify(hd) + '\n' + b;
	};

	// Fast path for HTTP-only requests (no WebSocket overhead).
	globalThis.__bunHandleFast = function(method, url, body, headers) {
		var h = globalThis.__bunFetchHandler;
		if (!h) return '503\n\n';
		var r = h(new Request('http://localhost' + url, {
			method: method, body: body || undefined, headers: headers
		}));
		if (r && typeof r.then === 'function') {
			globalThis.__bunPendingPromise = r;
			return '__async__';
		}
		return __bunExtract(r);
	};

	// WebSocket-aware path — passes server and reqId for upgrades.
	globalThis.__bunHandle = function(reqId, method, url, body, headers) {
		var h = globalThis.__bunFetchHandler;
		if (!h) return '503\n\n';
		var req = new Request('http://localhost' + url, {
			method: method, body: body || undefined, headers: headers
		});
		req._reqId = reqId;
		var r = h(req, globalThis.__bunServerObj);
		if (r === undefined && globalThis.__bunLastUpgradeOk) {
			globalThis.__bunLastUpgradeOk = false;
			return '__upgrade__';
		}
		if (r && typeof r.then === 'function') {
			globalThis.__bunPendingPromise = r;
			return '__async__';
		}
		return __bunExtract(r);
	};

	// WebSocket connection wrappers tracked by connId.
	var __wsConns = {};

	// WebSocket event handler — called from Go via processWSEvents.
	globalThis.__wsHandleEvent = function(kind, connId, data) {
		var handlers = globalThis.__bunWSHandlers;
		if (!handlers) return;
		if (kind === 'open') {
			var ws = {
				_id: connId,
				data: null,
				readyState: 1,
				send: function(msg) { __go_ws_send(connId, String(msg)); },
				close: function() {
					this.readyState = 3;
					__go_ws_close(connId);
				}
			};
			__wsConns[connId] = ws;
			if (handlers.open) {
				try { handlers.open(ws); } catch(e) {}
			}
		} else if (kind === 'message') {
			var ws = __wsConns[connId];
			if (ws && handlers.message) {
				try { handlers.message(ws, data); } catch(e) {}
			}
		} else if (kind === 'close') {
			var ws = __wsConns[connId];
			if (ws) {
				ws.readyState = 3;
				if (handlers.close) {
					try { handlers.close(ws); } catch(e) {}
				}
				delete __wsConns[connId];
			}
		} else if (kind === 'error') {
			var ws = __wsConns[connId];
			if (ws && handlers.error) {
				try { handlers.error(ws, new Error(data)); } catch(e) {}
			}
		}
	};

	globalThis.Ramune = {
		serve: function(opts) {
			var port = (opts.port !== undefined && opts.port !== null) ? opts.port : 3000;
			// Store handler globally — Go calls it directly during event loop tick.
			globalThis.__bunFetchHandler = opts.fetch;

			// Store WebSocket handlers if provided.
			if (opts.websocket) {
				globalThis.__bunWSHandlers = opts.websocket;
			}

			var actualPort = __go_bun_serve_start(port, !!opts.websocket);

			var server = {
				port: actualPort,
				hostname: 'localhost',
				upgrade: function(req) {
					if (!req._reqId) return false;
					var connId = __go_ws_upgrade(req._reqId);
					if (connId && connId > 0) {
						globalThis.__bunLastUpgradeOk = true;
						return true;
					}
					return false;
				},
				stop: function() {
					__go_bun_serve_stop();
					globalThis.__bunFetchHandler = null;
					globalThis.__bunWSHandlers = null;
					globalThis.__bunServerObj = null;
				}
			};
			globalThis.__bunServerObj = server;
			return server;
		},

		file: function(path) {
			return {
				_path: path,
				text: function() { return Promise.resolve(__go_bun_file_read(path)); },
				json: function() { return Promise.resolve(JSON.parse(__go_bun_file_read(path))); },
				size: (function() { try { return __go_bun_file_size(path); } catch(e) { return 0; } })(),
				name: path.split('/').pop(),
				exists: function() { try { __go_bun_file_size(path); return true; } catch(e) { return false; } }
			};
		},

		write: function(path, data) {
			return Promise.resolve(__go_bun_file_write(path, String(data)));
		},

		env: globalThis.process ? process.env : {},

		spawn: function(cmd) {
			var cp = require('child_process');
			if (typeof cmd === 'string') return cp.spawn('sh', ['-c', cmd]);
			var args = Array.isArray(cmd.cmd) ? cmd.cmd : [cmd.cmd];
			return cp.spawn(args[0], args.slice(1), {cwd: cmd.cwd, env: cmd.env});
		},

		version: '0.1.0-ramune',
		revision: 'ramune',

		sleep: function(ms) {
			return new Promise(function(resolve) { setTimeout(resolve, ms); });
		},

		hash: function(data) {
			var str = String(data);
			var hash = 2166136261;
			for (var i = 0; i < str.length; i++) {
				hash ^= str.charCodeAt(i);
				hash = (hash * 16777619) >>> 0;
			}
			return hash;
		},

		password: {
			hash: function(pw) { return Promise.resolve(__go_bun_password_hash(String(pw))); },
			verify: function(pw, h) { return Promise.resolve(__go_bun_password_verify(String(pw), String(h))); }
		}
	};

	// Bun compatibility alias — existing Bun.serve() code works as-is.
	globalThis.Bun = globalThis.Ramune;

	// Shared body-reading helper for Request/Response.
	function __readStreamAsText(stream) {
		var reader = stream.getReader();
		var chunks = [];
		function pump() {
			return reader.read().then(function(r) {
				if (r.done) return chunks.join('');
				chunks.push(typeof r.value === 'string' ? r.value : new TextDecoder().decode(r.value));
				return pump();
			});
		}
		return pump();
	}
	function __bodyText(obj) {
		if (obj._body !== null) { obj.bodyUsed = true; return Promise.resolve(obj._body); }
		if (obj.bodyUsed) return Promise.reject(new TypeError('Body already consumed'));
		obj.bodyUsed = true;
		return __readStreamAsText(obj._stream);
	}
	function __bodyJSON(obj) {
		if (obj._body !== null) { obj.bodyUsed = true; try { return Promise.resolve(JSON.parse(obj._body)); } catch(e) { return Promise.reject(e); } }
		return __bodyText(obj).then(function(t) { return JSON.parse(t); });
	}
	function __bodyArrayBuffer(obj) {
		if (obj._body !== null) { obj.bodyUsed = true; return Promise.resolve(new TextEncoder().encode(obj._body).buffer); }
		return __bodyText(obj).then(function(t) { return new TextEncoder().encode(t).buffer; });
	}

	// Request/Response Web API polyfills for frameworks like Hono.
	if (typeof Request === 'undefined') {
		globalThis.Request = function(url, opts) {
			opts = opts || {};
			this.url = url;
			this.method = (opts.method || 'GET').toUpperCase();
			this.headers = new (globalThis.Headers || function(){})(opts.headers || {});
			this.bodyUsed = false;
			if (opts.body instanceof ReadableStream) {
				this._stream = opts.body;
				this._body = null;
			} else {
				this._body = opts.body != null ? String(opts.body) : '';
				this._stream = null;
			}
		};
		Object.defineProperty(Request.prototype, 'body', { get: function() {
			if (this._stream) return this._stream;
			var text = this._body || '';
			if (!text) return null;
			this._stream = new ReadableStream({
				start: function(c) { c.enqueue(new TextEncoder().encode(text)); c.close(); }
			});
			return this._stream;
		}});
		Request.prototype.text = function() { return __bodyText(this); };
		Request.prototype.json = function() { return __bodyJSON(this); };
		Request.prototype.arrayBuffer = function() { return __bodyArrayBuffer(this); };
		Request.prototype.clone = function() { return new Request(this.url, {method: this.method, body: this._body, headers: this.headers}); };
	}

	if (typeof Response === 'undefined') {
		globalThis.Response = function(body, opts) {
			opts = opts || {};
			this.status = opts.status || 200;
			this.statusText = opts.statusText || 'OK';
			this.headers = new (globalThis.Headers || function(){})(opts.headers || {});
			this.ok = this.status >= 200 && this.status < 300;
			this.bodyUsed = false;
			if (body instanceof ReadableStream) {
				this._stream = body;
				this._body = null;
			} else {
				this._body = body != null ? String(body) : '';
				this._stream = null;
			}
		};
		Object.defineProperty(Response.prototype, 'body', { get: function() {
			if (this._stream) return this._stream;
			var text = this._body || '';
			this._stream = new ReadableStream({
				start: function(c) { if (text) c.enqueue(new TextEncoder().encode(text)); c.close(); }
			});
			return this._stream;
		}});
		Response.prototype.text = function() { return __bodyText(this); };
		Response.prototype.json = function() { return __bodyJSON(this); };
		Response.prototype.arrayBuffer = function() { return __bodyArrayBuffer(this); };
		Response.prototype.clone = function() { return new Response(this._body, {status: this.status, headers: this.headers}); };
		Response.json = function(data, opts) {
			opts = opts || {};
			return new Response(JSON.stringify(data), {
				status: opts.status || 200,
				headers: Object.assign({'content-type': 'application/json'}, opts.headers || {})
			});
		};
		Response.redirect = function(url, status) {
			return new Response('', { status: status || 302, headers: { location: url } });
		};
	}

	if (typeof Headers === 'undefined') {
		globalThis.Headers = function(init) {
			this._h = {};
			if (init) for (var k in init) this._h[k.toLowerCase()] = init[k];
		};
		Headers.prototype.get = function(k) { return this._h[k.toLowerCase()] || null; };
		Headers.prototype.set = function(k, v) { this._h[k.toLowerCase()] = v; };
		Headers.prototype.has = function(k) { return k.toLowerCase() in this._h; };
		Headers.prototype.delete = function(k) { delete this._h[k.toLowerCase()]; };
		Headers.prototype.forEach = function(cb) { var h = this._h; for (var k in h) cb(h[k], k); };
		Headers.prototype.entries = function() { var h = this._h; var keys = Object.keys(h); var i = 0; return { next: function() { if (i >= keys.length) return {done:true}; var k = keys[i++]; return {value:[k,h[k]],done:false}; } }; };
	}
})();
`)
}
