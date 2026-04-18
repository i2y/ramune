package workers

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	esbuildapi "github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune"
)

// IsWorkersStyle reports whether the source uses a default export, the
// signal that distinguishes Workers-style modules from old-style hook
// scripts. Since "export default" is illegal outside a module it has
// zero false positives against scripts.
func IsWorkersStyle(code string) bool {
	return strings.Contains(code, "export default")
}

// TranspileModule wraps a Workers-style source as an IIFE that sets
// globalThis.__workers_export = { default: <exported value> }.
//
//	export default { fetch(r,e){…} }
//
// becomes
//
//	var __workers_export = (() => { … return { default: … }; })();
//
// Returned code may be evaluated with Runtime.Exec in any context; it
// does not rely on ESM host support.
func TranspileModule(filename string, code string) (string, error) {
	loader := esbuildapi.LoaderJS
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".ts", ".mts", ".cts":
		loader = esbuildapi.LoaderTS
	case ".tsx":
		loader = esbuildapi.LoaderTSX
	case ".jsx":
		loader = esbuildapi.LoaderJSX
	}
	result := esbuildapi.Transform(code, esbuildapi.TransformOptions{
		Sourcefile: filepath.Base(filename),
		Loader:     loader,
		Target:     esbuildapi.ESNext,
		Format:     esbuildapi.FormatIIFE,
		GlobalName: "__workers_export",
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("workers: esbuild %s: %s", filename, result.Errors[0].Text)
	}
	return string(result.Code), nil
}

// moduleConfig mirrors the subset of the default export that Register
// inspects once. All other fields are accessed from JS per-request.
type moduleConfig struct {
	Route        string
	Cron         string
	HasFetch     bool
	HasScheduled bool
}

// extractModuleConfig reads the default export from rt (which must
// have just evaluated the IIFE) and returns the relevant fields.
func extractModuleConfig(rt *ramune.Runtime) (*moduleConfig, error) {
	val, err := rt.Eval("__workers_export && __workers_export.default")
	if err != nil {
		return nil, fmt.Errorf("workers: read default export: %w", err)
	}
	if val == nil {
		return nil, fmt.Errorf("workers: default export is missing")
	}
	defer val.Close()

	if val.IsNull() || val.IsUndefined() {
		return nil, fmt.Errorf("workers: default export is null/undefined")
	}

	out := &moduleConfig{}
	if r := val.Attr("route"); r != nil {
		// Hono and other frameworks expose a route() method on their app
		// objects; skip anything that is not a primitive string so the
		// framework's methods don't get mistaken for a path pattern.
		if !r.IsNull() && !r.IsUndefined() && !r.IsFunction() {
			s, _ := r.GoString()
			out.Route = s
		}
		r.Close()
	}
	if c := val.Attr("cron"); c != nil {
		if !c.IsNull() && !c.IsUndefined() && !c.IsFunction() {
			s, _ := c.GoString()
			out.Cron = s
		}
		c.Close()
	}
	if f := val.Attr("fetch"); f != nil {
		out.HasFetch = f.IsFunction()
		f.Close()
	}
	if s := val.Attr("scheduled"); s != nil {
		out.HasScheduled = s.IsFunction()
		s.Close()
	}
	return out, nil
}

// nextRequestID issues monotonic request identifiers used to key the
// requestRegistry. Int64 keeps it cheap on 64-bit archs and avoids
// wrap-around for the life of any realistic process.
var nextRequestID atomic.Int64

// requestRegistry maps request IDs to per-request state. Looked up by
// the __wk* bindings when JS wants to consume the request or write the
// response.
var requestRegistry sync.Map // int64 → *requestState

// requestState is the Go-side handle for an in-flight fetch request.
type requestState struct {
	w       http.ResponseWriter
	r       *http.Request
	flusher http.Flusher // cached; nil when w does not implement it

	signalMu   sync.Mutex
	signalCh   chan struct{}
	signalOnce bool

	statusMu sync.Mutex
	started  bool // headers written via WriteHeader
	written  bool // at least one byte of body written
}

// signal closes signalCh at most once. Safe to call from any goroutine.
func (s *requestState) signal() {
	s.signalMu.Lock()
	defer s.signalMu.Unlock()
	if !s.signalOnce {
		s.signalOnce = true
		close(s.signalCh)
	}
}

// newFetchDispatcher returns an http.Handler that runs the cached
// Workers module's fetch() for every incoming request.
func newFetchDispatcher(rt *ramune.Runtime, cacheKey string, cfg Config) http.HandlerFunc {
	waitUntilMs := int64(0)
	if cfg.WaitUntilTimeout > 0 {
		waitUntilMs = cfg.WaitUntilTimeout.Milliseconds()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		reqID := nextRequestID.Add(1)
		flusher, _ := w.(http.Flusher)
		state := &requestState{
			w:        w,
			r:        r,
			flusher:  flusher,
			signalCh: make(chan struct{}),
		}
		requestRegistry.Store(reqID, state)
		defer requestRegistry.Delete(reqID)

		errCh := make(chan error, 1)
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					errCh <- fmt.Errorf("workers fetch panic: %v", rec)
					state.signal() // release the HTTP handler even on panic
				}
			}()
			code := buildFetchCode(reqID, r, cacheKey, waitUntilMs)
			_, err := rt.EvalAsync(code)
			errCh <- err
			// Ensure the HTTP handler always returns, even if the JS
			// side forgot to detach (shouldn't happen, but be safe).
			state.signal()
		}()

		select {
		case <-state.signalCh:
			// Response has been written — the JS executor may still be
			// draining ctx.waitUntil promises. Return control to Go
			// so the HTTP handler can unblock the net/http pool.
			return
		case err := <-errCh:
			if err == nil {
				return
			}
			state.statusMu.Lock()
			started := state.started
			state.statusMu.Unlock()
			if !started {
				http.Error(w, "workers: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}
}

// buildFetchCode produces the JS snippet executed for each request.
// The per-request IDs and URL/method are injected via strconv+json so
// no escaping helpers are needed at runtime.
func buildFetchCode(reqID int64, r *http.Request, cacheKey string, waitUntilMs int64) string {
	return fmt.Sprintf(
		`(async function() {
			var __rid = %d;
			var __method = %s;
			var __url = %s;
			var __wk = globalThis[%q];
			if (!__wk || typeof __wk.fetch !== "function") {
				__writeWorkerResponse(__rid, 500, "{}", "workers: fetch handler missing");
				__detachResponse(__rid);
				return;
			}
			var __headers = __getGoRequestHeaders(__rid);
			var __body = (__method !== "GET" && __method !== "HEAD")
				? __readGoRequestBody(__rid) : undefined;
			var __request = new Request(__url, {
				method: __method, headers: __headers, body: __body
			});
			var __env = __buildEnv();
			var __pending = [];
			var __ctx = {
				waitUntil: function(p) {
					if (p && typeof p.then === "function") __pending.push(p);
				},
				passThroughOnException: function() {}
			};
			try {
				var __response = await __wk.fetch(__request, __env, __ctx);
				if (!__response || typeof __response !== "object") {
					__writeWorkerResponse(__rid, 204, "{}", "");
				} else {
					var __rh = {};
					if (__response.headers && typeof __response.headers.forEach === "function") {
						__response.headers.forEach(function(v, k) { __rh[k] = v; });
					}
					var __respBody = __response.body;
					if (__respBody && typeof __respBody.getReader === "function") {
						__writeWorkerResponseStart(__rid, __response.status || 200, JSON.stringify(__rh));
						var __reader = __respBody.getReader();
						var __dec = new TextDecoder();
						while (true) {
							var __read = await __reader.read();
							if (__read.done) break;
							var __text = typeof __read.value === "string"
								? __read.value
								: __dec.decode(__read.value, {stream: true});
							if (__text) __writeWorkerResponseChunk(__rid, __text);
						}
					} else {
						var __rb = "";
						if (typeof __response.text === "function") {
							__rb = await __response.text();
						} else if (__respBody != null) {
							__rb = String(__respBody);
						}
						__writeWorkerResponse(__rid, __response.status || 200, JSON.stringify(__rh), __rb);
					}
				}
			} catch (__e) {
				__writeWorkerResponse(__rid, 500, "{}", "workers: " + (__e && __e.stack ? __e.stack : String(__e)));
			} finally {
				__detachResponse(__rid);
			}
			if (__pending.length) {
				var __wait = Promise.allSettled(__pending);
				var __timeoutMs = %d;
				if (__timeoutMs > 0) {
					var __timeoutP = new Promise(function(resolve){ setTimeout(resolve, __timeoutMs); });
					await Promise.race([__wait, __timeoutP]);
				} else {
					await __wait;
				}
			}
		})()`,
		reqID,
		jsString(r.Method),
		jsString(fullRequestURL(r)),
		cacheKey,
		waitUntilMs,
	)
}

// jsString produces a JS string literal via JSON encoding, which gives
// us correct escaping for free.
func jsString(s string) string {
	// json.Marshal of a string always produces a double-quoted JS
	// literal compatible with both JS and JSON.
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case 0:
			sb.WriteString(`\u0000`)
		default:
			if r < 0x20 {
				sb.WriteString(`\u`)
				sb.WriteString(padHex4(int(r)))
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func padHex4(n int) string {
	s := strconv.FormatInt(int64(n), 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// fullRequestURL reconstructs an absolute URL from an incoming request.
func fullRequestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host + r.RequestURI
}

// registerScheduled installs a Ramune cron handler that runs the
// scheduled export. It relies on Ramune's built-in cron (installed by
// NodeCompat/default). If cron is unavailable the caller gets a clear
// error at Register time.
func registerScheduled(rt *ramune.Runtime, cacheKey, expr string) error {
	id := "workers:" + cacheKey
	js := fmt.Sprintf(
		`(function(){
			if (!globalThis.Ramune || typeof globalThis.Ramune.cron !== "function") {
				throw new Error("workers: Ramune.cron not available; scheduled handler cannot be registered");
			}
			Ramune.cron(%q, %q, async function() {
				var __wk = globalThis[%q];
				if (!__wk || typeof __wk.scheduled !== "function") return;
				var __env = __buildEnv();
				var __pending = [];
				var __ctx = {
					waitUntil: function(p){ if (p && typeof p.then === "function") __pending.push(p); },
					passThroughOnException: function(){}
				};
				var __event = { scheduledTime: Date.now(), cron: %q };
				try {
					await __wk.scheduled(__event, __env, __ctx);
				} catch (e) {
					console.error("workers: scheduled handler threw:", e);
				}
				if (__pending.length) await Promise.allSettled(__pending);
			});
		})();`,
		id, expr, cacheKey, expr,
	)
	if err := rt.Exec(js); err != nil {
		return err
	}
	return nil
}

// logger chooses a slog.Logger from ramune if one is exposed, falling
// back to the default. Used only for diagnostic output from Register.
func logger() *slog.Logger {
	return slog.Default()
}
