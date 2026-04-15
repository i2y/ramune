package ramune

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
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

	"github.com/andybalholm/brotli"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/tailscale/hujson"

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

	if err := r.registerFuncLocked("__go_bun_jsonc_parse", goBunJSONCParse); err != nil {
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
		// Third arg: idle timeout in milliseconds.
		if len(args) >= 3 {
			if ms, ok := args[2].(float64); ok && ms > 0 {
				r.bunSrv.idleTimeout = time.Duration(ms) * time.Millisecond
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

	if err := r.registerFuncLocked("__go_bun_build", goBunBuild); err != nil {
		return err
	}

	return r.execLocked(bunCompatJSSource())
}

func goBunBuild(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("build: options required")
	}
	optsJSON, _ := args[0].(string)

	var opts struct {
		Entrypoints []string `json:"entrypoints"`
		Outdir      string   `json:"outdir"`
		Target      string   `json:"target"`
		Format      string   `json:"format"`
		Minify      bool     `json:"minify"`
		Splitting   bool     `json:"splitting"`
		Sourcemap   string   `json:"sourcemap"`
		External    []string `json:"external"`
	}
	if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
		return nil, fmt.Errorf("build: invalid options: %w", err)
	}

	buildOpts := api.BuildOptions{
		EntryPoints: opts.Entrypoints,
		Bundle:      true,
		LogLevel:    api.LogLevelSilent,
	}

	if opts.Outdir != "" {
		buildOpts.Outdir = opts.Outdir
		buildOpts.Write = true
	}

	switch opts.Target {
	case "bun", "node":
		buildOpts.Platform = api.PlatformNode
	default:
		buildOpts.Platform = api.PlatformBrowser
	}

	switch opts.Format {
	case "cjs":
		buildOpts.Format = api.FormatCommonJS
	case "iife":
		buildOpts.Format = api.FormatIIFE
	default:
		buildOpts.Format = api.FormatESModule
	}

	if opts.Minify {
		buildOpts.MinifySyntax = true
		buildOpts.MinifyWhitespace = true
		buildOpts.MinifyIdentifiers = true
	}
	buildOpts.Splitting = opts.Splitting

	switch opts.Sourcemap {
	case "inline":
		buildOpts.Sourcemap = api.SourceMapInline
	case "linked", "external":
		buildOpts.Sourcemap = api.SourceMapLinked
	}

	if len(opts.External) > 0 {
		buildOpts.External = opts.External
	}

	result := api.Build(buildOpts)

	outputs := make([]map[string]any, 0, len(result.OutputFiles))
	for _, f := range result.OutputFiles {
		kind := "entry-point"
		if strings.HasSuffix(f.Path, ".map") {
			kind = "sourcemap"
		} else if strings.Contains(f.Path, "chunk-") {
			kind = "chunk"
		}
		outputs = append(outputs, map[string]any{
			"path": f.Path,
			"kind": kind,
		})
	}

	logs := make([]map[string]any, 0)
	for _, msg := range result.Errors {
		logs = append(logs, map[string]any{"level": "error", "message": msg.Text})
	}
	for _, msg := range result.Warnings {
		logs = append(logs, map[string]any{"level": "warning", "message": msg.Text})
	}

	resp := map[string]any{
		"outputs": outputs,
		"success": len(result.Errors) == 0,
		"logs":    logs,
	}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

// --- HTTP Server ---

type bunServerState struct {
	rt          *Runtime
	mu          sync.Mutex
	listener    net.Listener
	server      *http.Server
	requests    chan pendingHTTPReq
	pending     map[int]chan httpResponse
	nextID      int
	gcCounter   int
	wsEnabled   bool          // true when Bun.serve() was called with websocket handlers
	wsMgr       *wsManager    // WebSocket connection manager
	idleTimeout time.Duration // configurable idle timeout

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
	Status   int
	Headers  map[string]string
	Body     string
	StreamID int    // non-zero: body is a js2go managed stream
	FilePath string // non-empty: serve static file
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

	if s.rt.gcConfig.GCPercent != 0 {
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

			s.writeResponse(w, r, resp)
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

		s.writeResponse(w, r, resp)
	})

	s.server = &http.Server{Handler: mux}
	if s.idleTimeout > 0 {
		s.server.IdleTimeout = s.idleTimeout
		s.server.ReadHeaderTimeout = s.idleTimeout
	}
	go s.server.Serve(ln)

	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (s *bunServerState) stop() {
	// Close all WebSocket connections first.
	if s.wsMgr != nil {
		s.wsMgr.closeAll()
	}
	if s.rt.gcConfig.GCPercent != 0 {
		debug.SetGCPercent(s.rt.gcConfig.GCPercent)
	}
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

func (s *bunServerState) writeResponse(w http.ResponseWriter, r *http.Request, resp httpResponse) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	// Streaming response via js2go stream.
	if resp.StreamID != 0 && s.rt.streamMgr != nil {
		stream := s.rt.streamMgr.getStream(resp.StreamID)
		if stream == nil {
			w.WriteHeader(500)
			w.Write([]byte("stream not found"))
			return
		}
		if resp.Status > 0 {
			w.WriteHeader(resp.Status)
		}
		flusher, _ := w.(http.Flusher)
		for {
			select {
			case chunk, ok := <-stream.dataCh:
				if !ok {
					return
				}
				if _, err := w.Write(chunk); err != nil {
					s.rt.streamMgr.cancelStream(resp.StreamID)
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			case <-stream.doneCh:
				for {
					select {
					case chunk := <-stream.dataCh:
						w.Write(chunk)
					default:
						if flusher != nil {
							flusher.Flush()
						}
						s.rt.streamMgr.removeStream(resp.StreamID)
						return
					}
				}
			}
		}
	}

	// Static file response.
	if resp.FilePath != "" {
		http.ServeFile(w, r, resp.FilePath)
		return
	}

	// Normal buffered response with optional compression.
	body := []byte(resp.Body)
	ct := w.Header().Get("Content-Type")

	// Compress if body is large enough, content type is compressible, and not already encoded.
	if r != nil && len(body) >= 1024 && !strings.HasPrefix(ct, "text/event-stream") && w.Header().Get("Content-Encoding") == "" {
		encoding := negotiateEncoding(r.Header.Get("Accept-Encoding"))
		if encoding != "" {
			compressed, err := compressBody(body, encoding)
			if err == nil && len(compressed) < len(body) {
				w.Header().Set("Content-Encoding", encoding)
				w.Header().Del("Content-Length")
				body = compressed
			}
		}
	}

	if resp.Status > 0 {
		w.WriteHeader(resp.Status)
	}
	w.Write(body)
}

func negotiateEncoding(accept string) string {
	if accept == "" {
		return ""
	}
	// Simple quality-unaware negotiation: prefer br > gzip > deflate.
	a := strings.ToLower(accept)
	if strings.Contains(a, "br") {
		return "br"
	}
	if strings.Contains(a, "gzip") {
		return "gzip"
	}
	if strings.Contains(a, "deflate") {
		return "deflate"
	}
	return ""
}

func compressBody(data []byte, encoding string) ([]byte, error) {
	var buf bytes.Buffer
	switch encoding {
	case "gzip":
		w := gzip.NewWriter(&buf)
		w.Write(data)
		w.Close()
	case "deflate":
		w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
		w.Write(data)
		w.Close()
	case "br":
		w := brotli.NewWriter(&buf)
		w.Write(data)
		w.Close()
	default:
		return nil, fmt.Errorf("unsupported encoding: %s", encoding)
	}
	return buf.Bytes(), nil
}

func escJS(s string) string {
	return jsEscaper.Replace(s)
}

// parseHTTPResponse parses the response string from JS handler.
// Formats:
//   - "__stream__:ID\nstatus\nheadersJSON" — streaming response
//   - "__file__:/path\nstatus\nheadersJSON" — static file response
//   - "status\nheadersJSON\nbody" — normal buffered response
func parseHTTPResponse(raw string) httpResponse {
	// Streaming response.
	if strings.HasPrefix(raw, "__stream__:") {
		parts := strings.SplitN(raw, "\n", 3)
		resp := httpResponse{Status: 200}
		if len(parts) >= 1 {
			idStr := strings.TrimPrefix(parts[0], "__stream__:")
			if n, err := strconv.Atoi(idStr); err == nil {
				resp.StreamID = n
			}
		}
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				resp.Status = n
			}
		}
		if len(parts) >= 3 && parts[2] != "" {
			json.Unmarshal([]byte(parts[2]), &resp.Headers)
		}
		return resp
	}

	// Static file response.
	if strings.HasPrefix(raw, "__file__:") {
		parts := strings.SplitN(raw, "\n", 3)
		resp := httpResponse{Status: 200}
		if len(parts) >= 1 {
			resp.FilePath = strings.TrimPrefix(parts[0], "__file__:")
		}
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				resp.Status = n
			}
		}
		if len(parts) >= 3 && parts[2] != "" {
			json.Unmarshal([]byte(parts[2]), &resp.Headers)
		}
		return resp
	}

	// Normal buffered response.
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
	return resp
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

func goBunJSONCParse(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("JSONC.parse: input required")
	}
	src, _ := args[0].(string)
	b, err := hujson.Standardize([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("JSONC.parse: %w", err)
	}
	return string(b), nil
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
		// Detect ReadableStream body for streaming response.
		if (r._stream && r._body === null && typeof ReadableStream !== 'undefined' && r._stream instanceof ReadableStream) {
			var streamId = __go_stream_create_js2go();
			__streamPumpToGo(r._stream, streamId);
			return '__stream__:' + streamId + '\n' + s + '\n' + JSON.stringify(hd);
		}
		// Detect Bun.file() object in response body.
		if (r._fileObj && r._fileObj._path) {
			return '__file__:' + r._fileObj._path + '\n' + s + '\n' + JSON.stringify(hd);
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

			var actualPort = __go_bun_serve_start(port, !!opts.websocket, opts.idleTimeout || 0);

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
		},

		build: function(opts) {
			return new Promise(function(resolve, reject) {
				try {
					var result = JSON.parse(__go_bun_build(JSON.stringify(opts || {})));
					resolve(result);
				} catch(e) { reject(e); }
			});
		}
	};

	// --- Bun.plugin() ---
	var _plugins = [];
	globalThis.Ramune.plugin = function(pluginDef) {
		if (typeof pluginDef === 'function') {
			pluginDef = { setup: pluginDef };
		}
		var loaders = [];
		var build = {
			onLoad: function(opts, callback) {
				loaders.push({ filter: opts.filter, namespace: opts.namespace || 'file', loader: opts.loader, callback: callback });
			},
			module: function(name, exports) {
				if (globalThis.require && globalThis.require._modules) {
					globalThis.require._modules[name] = exports;
				}
			}
		};
		if (pluginDef.setup) pluginDef.setup(build);
		_plugins = _plugins.concat(loaders);
	};
	// Hook into require to check plugins.
	globalThis.__bunPluginResolve = function(path) {
		for (var i = 0; i < _plugins.length; i++) {
			var p = _plugins[i];
			if (p.filter && p.filter.test(path)) {
				return p;
			}
		}
		return null;
	};

	// --- Bun.JSONC ---
	globalThis.Ramune.JSONC = {
		parse: function(src) {
			return JSON.parse(__go_bun_jsonc_parse(src));
		}
	};

	// --- Bun.Archive ---
	globalThis.Ramune.Archive = {
		tar: function(opts) {
			var result = __go_bun_archive_tar(JSON.stringify(opts));
			return result;
		},
		untar: function(opts, data) {
			var result = __go_bun_archive_untar(JSON.stringify(opts), data || '');
			return JSON.parse(result);
		}
	};

	// --- Bun.CSRF ---
	globalThis.Ramune.CSRF = {
		generate: function(secret) { return __go_bun_csrf_generate(secret); },
		verify: function(secret, token) { return __go_bun_csrf_verify(secret, token); }
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
			} else if (body && typeof body === 'object' && body._path) {
				// Bun.file() object — preserve for static file serving.
				this._fileObj = body;
				this._body = null;
				this._stream = null;
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
