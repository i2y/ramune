package workers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/tsgotranspile"
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
//	var __workers_export = (function(){ var module={exports:{}};
//	                                    var exports=module.exports;
//	                                    <tsgo CJS output>
//	                                    return module.exports; })();
//
// Returned code may be evaluated with Runtime.Exec in any context; it
// does not rely on ESM host support.
func TranspileModule(filename string, code string) (string, error) {
	r, err := tsgotranspile.Transpile(code, tsgotranspile.Options{
		FileName: filename,
		Target:   core.ScriptTargetESNext,
		Module:   core.ModuleKindCommonJS,
	})
	if err != nil {
		return "", fmt.Errorf("workers: tsgo %s: %w", filename, err)
	}
	if e := tsgotranspile.FirstError(r.Diagnostics); e != nil {
		return "", fmt.Errorf("workers: tsgo %s: %w", filename, e)
	}
	var b strings.Builder
	b.Grow(len(r.JS) + 128)
	b.WriteString("var __workers_export = (function(){\n")
	b.WriteString("var module = { exports: {} };\n")
	b.WriteString("var exports = module.exports;\n")
	b.WriteString(r.JS)
	b.WriteString("\nreturn module.exports;\n")
	b.WriteString("})();\n")
	return b.String(), nil
}

// ModuleConfig is the subset of a Workers-style module's default export
// that callers read once at Register time. Framework methods (Hono's
// route() / etc.) are filtered out so only primitive strings land here.
type ModuleConfig struct {
	Route        string
	Cron         string
	HasFetch     bool
	HasScheduled bool
}

// ExtractModuleConfig reads __workers_export.default from rt, which
// must have just evaluated the IIFE emitted by TranspileModule.
// Exposed so embedders that run their own dispatch (e.g. Soda) can
// reuse the Ramune inspection instead of duplicating it.
func ExtractModuleConfig(rt *ramune.Runtime) (*ModuleConfig, error) {
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

	out := &ModuleConfig{}
	out.Route = readStringAttr(val, "route")
	out.Cron = readStringAttr(val, "cron")
	out.HasFetch = hasFunctionAttr(val, "fetch")
	out.HasScheduled = hasFunctionAttr(val, "scheduled")
	return out, nil
}

// readStringAttr returns the named attr's string value, or "" when the
// attr is missing, null, undefined, or a function. The function guard
// matters because frameworks like Hono expose a .route() method on
// their app instance — without the check it would be coerced to its
// source via toString().
func readStringAttr(v *ramune.Value, name string) string {
	a := v.Attr(name)
	if a == nil {
		return ""
	}
	defer a.Close()
	if a.IsNull() || a.IsUndefined() || a.IsFunction() {
		return ""
	}
	s, _ := a.GoString()
	return s
}

func hasFunctionAttr(v *ramune.Value, name string) bool {
	a := v.Attr(name)
	if a == nil {
		return false
	}
	defer a.Close()
	return a.IsFunction()
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

// writeHeader emits status+headers once. Subsequent calls are no-ops —
// matching net/http's contract that WriteHeader is invoked at most once.
func (s *requestState) writeHeader(status int) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.started {
		return
	}
	s.w.WriteHeader(status)
	s.started = true
}

// writeBody sends a chunk of body text, marking the response as having
// written bytes (so errCh failures don't double-emit an error status).
// Client-disconnect errors are dropped silently.
func (s *requestState) writeBody(text string) {
	if _, err := io.WriteString(s.w, text); err != nil {
		return
	}
	s.statusMu.Lock()
	s.written = true
	s.statusMu.Unlock()
}

// flush calls http.Flusher.Flush when the writer supports it. For
// buffered writers (httptest default) this is a no-op.
func (s *requestState) flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// fetchDispatcher is the http.Handler returned by newFetchDispatcher.
// It tracks executor goroutines so that AttachPrepared's close hook
// can wait for in-flight requests AND their ctx.waitUntil background
// promises before releasing the worker.
type fetchDispatcher struct {
	rt          *ramune.Runtime
	cacheKey    string
	cfg         Config
	waitUntilMs int64
	executors   sync.WaitGroup
}

// newFetchDispatcher returns an http.Handler that runs the cached
// Workers module's fetch() for every incoming request.
func newFetchDispatcher(rt *ramune.Runtime, cacheKey string, cfg Config) *fetchDispatcher {
	waitMs := int64(0)
	if cfg.WaitUntilTimeout > 0 {
		waitMs = cfg.WaitUntilTimeout.Milliseconds()
	}
	return &fetchDispatcher{
		rt:          rt,
		cacheKey:    cacheKey,
		cfg:         cfg,
		waitUntilMs: waitMs,
	}
}

func (d *fetchDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	d.executors.Add(1)
	go func() {
		defer d.executors.Done()
		defer func() {
			if rec := recover(); rec != nil {
				errCh <- fmt.Errorf("workers fetch panic: %v", rec)
				state.signal() // release the HTTP handler even on panic
			}
		}()
		code := buildFetchCode(reqID, r, d.cacheKey, d.waitUntilMs)
		_, err := d.rt.EvalAsync(code)
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

// wait blocks until every executor goroutine (HTTP request + its
// background ctx.waitUntil drain) has returned, or timeout elapses.
func (d *fetchDispatcher) wait(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		d.executors.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("workers: drain timed out after %v with executor(s) still running", timeout)
	}
}

// fetchDispatchJS installs globalThis.__wkDispatch once per Runtime so
// that per-request dispatch only parses a short call expression. The
// function is idempotent: running it twice on the same Runtime simply
// re-assigns the global.
const fetchDispatchJS = `
(function() {
	globalThis.__wkDispatch = async function(__rid, __method, __url, __cacheKey, __waitMs) {
		var __wk = globalThis[__cacheKey];
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
					while (true) {
						var __read = await __reader.read();
						if (__read.done) break;
						if (typeof __read.value === "string") {
							if (__read.value) __writeWorkerResponseChunk(__rid, __read.value);
						} else if (__read.value && __read.value.length) {
							// Binary chunk: encode as base64 with marker so
							// __writeWorkerResponseChunk can reconstruct the
							// raw bytes (TextDecoder would lossy-convert).
							var __cs = "";
							for (var __ci = 0; __ci < __read.value.length; __ci++) {
								__cs += String.fromCharCode(__read.value[__ci]);
							}
							var __cb64 = (typeof btoa === "function" ? btoa(__cs) : Buffer.from(__cs, "binary").toString("base64"));
							__writeWorkerResponseChunk(__rid, "__bytes_b64__:" + __cb64);
						}
					}
				} else {
					var __rb = "";
					// If Response was constructed with Uint8Array / ArrayBuffer /
					// ArrayBufferView, _bodyBytes carries the raw bytes. Re-encode
					// as base64 with a marker prefix so the Go callback can
					// recover bytes byte-for-byte; without this binary GET
					// responses (images, gzip, ...) lose data through JS string
					// coercion.
					if (__response._bodyBytes != null) {
						var __bs = "";
						for (var __bi = 0; __bi < __response._bodyBytes.length; __bi++) {
							__bs += String.fromCharCode(__response._bodyBytes[__bi]);
						}
						__rb = "__bytes_b64__:" + (typeof btoa === "function" ? btoa(__bs) : Buffer.from(__bs, "binary").toString("base64"));
					} else if (typeof __response.text === "function") {
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
			if (__waitMs > 0) {
				var __timeoutP = new Promise(function(resolve){ setTimeout(resolve, __waitMs); });
				await Promise.race([__wait, __timeoutP]);
			} else {
				await __wait;
			}
		}
	};
})();
`

// buildFetchCode returns the per-request JS that invokes
// __wkDispatch. Only the reqID and small URL/method literals are
// serialized — the heavy template lives in fetchDispatchJS and is
// installed once per Runtime.
func buildFetchCode(reqID int64, r *http.Request, cacheKey string, waitUntilMs int64) string {
	return fmt.Sprintf(
		"__wkDispatch(%d, %s, %s, %s, %d)",
		reqID,
		jsString(r.Method),
		jsString(fullRequestURL(r)),
		jsString(cacheKey),
		waitUntilMs,
	)
}

// jsString produces a double-quoted JS string literal. json.Marshal
// on a Go string always emits a JSON string, which is also a valid
// JS string literal (escapes ", \, control chars, <>&).
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
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
	return rt.Exec(js)
}
