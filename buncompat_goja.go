//go:build goja

package ramune

import (
	"net/http"
	"time"

	"github.com/dop251/goja"
)

// commonHTTPMethods are pre-baked into bunMethodValCache so the hot dispatch
// path skips vm.ToValue(string) for the overwhelming majority of requests.
var commonHTTPMethods = [...]string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete,
	http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodConnect, http.MethodTrace,
}

// processRequests drains pending HTTP requests and dispatches them to the JS handler.
func (s *bunServerState) processRequests(r *Runtime) {
	if s == nil || s.requests == nil {
		return
	}

	for i := 0; i < 256; i++ {
		select {
		case req := <-s.requests:
			s.handleSingleRequest(r, req)
		default:
			return
		}
	}
}

// ensureHandlerCached captures direct goja.Callable references so subsequent
// requests skip per-request source parse/compile (goja's parser runs on every
// RunString; caching trades ~2.5us/req of parse for a direct function call).
func (s *bunServerState) ensureHandlerCached(r *Runtime) {
	if r.bunHandleFastFn != nil {
		return
	}

	// __bunAsyncSetup must exist in globalThis before the async response path
	// can dispatch to it; install once here rather than rebuilding per request.
	const asyncSetupJS = `globalThis.__bunAsyncSetup = function(asyncKey) {
		var p = globalThis.__bunPendingPromise;
		p.then(function(v){ globalThis[asyncKey] = __bunExtract(v); },
		       function(e){ globalThis[asyncKey] = '500\n{}\n' + String(e); });
		delete globalThis.__bunPendingPromise;
	};`
	if _, err := r.safeRunString(asyncSetupJS); err != nil {
		return
	}

	fn, ok := goja.AssertFunction(r.vm.Get("__bunHandleFast"))
	if !ok {
		return
	}
	r.bunHandleFastFn = fn

	if asyncFn, ok := goja.AssertFunction(r.vm.Get("__bunAsyncSetup")); ok {
		r.bunAsyncSetupFn = asyncFn
	}

	// __eventLoop may not be installed (e.g. withFetch=false), so these lookups
	// are best-effort; handleSingleRequest falls back to RunString when nil.
	if elVal := r.vm.Get("__eventLoop"); elVal != nil {
		if elObj := elVal.ToObject(r.vm); elObj != nil {
			if tickFn, ok := goja.AssertFunction(elObj.Get("tick")); ok {
				r.bunTickFn = tickFn
			}
			if ndFn, ok := goja.AssertFunction(elObj.Get("nextDelay")); ok {
				r.bunNextDelayFn = ndFn
			}
		}
	}

	r.bunMethodValCache = make(map[string]goja.Value, len(commonHTTPMethods))
	for _, m := range commonHTTPMethods {
		r.bunMethodValCache[m] = r.vm.ToValue(m)
	}
	r.bunCallArgs = make([]goja.Value, 4)
}

func (s *bunServerState) handleSingleRequest(r *Runtime, req pendingHTTPReq) {
	s.ensureHandlerCached(r)

	var result goja.Value
	var err error
	if r.bunHandleFastFn != nil {
		methodVal, ok := r.bunMethodValCache[req.Method]
		if !ok {
			methodVal = r.vm.ToValue(req.Method)
		}
		var headersVal goja.Value
		if req.Headers != nil {
			headersVal = r.vm.ToValue(req.Headers)
		} else {
			headersVal = r.vm.NewObject()
		}
		r.bunCallArgs[0] = methodVal
		r.bunCallArgs[1] = r.vm.ToValue(req.URL)
		r.bunCallArgs[2] = r.vm.ToValue(req.Body)
		r.bunCallArgs[3] = headersVal
		result, err = r.safeCallable(r.bunHandleFastFn, goja.Undefined(), r.bunCallArgs...)
	} else {
		// Reached only if ensureHandlerCached found no __bunHandleFast global
		// (e.g. the user has not called Ramune.serve yet but requests arrived).
		code := `__bunHandleFast("` + escJS(req.Method) + `","` + escJS(req.URL) + `","` + escJS(req.Body) + `",` + req.HeadersJSON + `)`
		result, err = r.safeRunString(code)
	}
	if err != nil {
		s.respond(req.ID, httpResponse{Status: 500, Body: "handler error"})
		return
	}
	if result == nil {
		s.respond(req.ID, httpResponse{Status: 500, Body: "handler returned nil"})
		return
	}

	raw := result.String()

	if raw == "__upgrade__" {
		return
	}

	if raw == "__async__" {
		asyncKey := asyncRespKey(req.ID)
		asyncKeyVal := r.vm.ToValue(asyncKey)

		if r.bunAsyncSetupFn != nil {
			if _, setupErr := r.safeCallable(r.bunAsyncSetupFn, goja.Undefined(), asyncKeyVal); setupErr != nil {
				r.vm.GlobalObject().Delete("__bunPendingPromise")
				s.respond(req.ID, httpResponse{Status: 500, Body: "async setup failed"})
				return
			}
		} else {
			setupCode := `globalThis.__bunPendingPromise.then(` +
				`function(v){globalThis['` + asyncKey + `']=__bunExtract(v);},` +
				`function(e){globalThis['` + asyncKey + `']='500\n{}\n'+String(e);});` +
				`delete globalThis.__bunPendingPromise;`
			if _, setupErr := r.safeRunString(setupCode); setupErr != nil {
				r.safeRunString("delete globalThis.__bunPendingPromise")
				s.respond(req.ID, httpResponse{Status: 500, Body: "async setup failed"})
				return
			}
		}

		global := r.vm.GlobalObject()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if r.procMgr != nil {
				r.procMgr.processEvents(r)
			}
			if r.sockMgr != nil {
				r.sockMgr.processEvents(r)
			}

			if r.bunTickFn != nil {
				r.safeCallable(r.bunTickFn, goja.Undefined())
			} else {
				r.safeRunString("__eventLoop.tick()")
			}

			if v := global.Get(asyncKey); v != nil && !goja.IsUndefined(v) {
				if exp := v.Export(); exp != nil {
					if sresp, ok := exp.(string); ok {
						raw = sresp
					}
					global.Delete(asyncKey)
					break
				}
			}

			var ms float64
			if r.bunNextDelayFn != nil {
				if delayVal, err := r.safeCallable(r.bunNextDelayFn, goja.Undefined()); err == nil && delayVal != nil {
					ms = delayVal.ToFloat()
				}
			} else {
				delayVal, _ := r.safeRunString("__eventLoop.nextDelay()")
				if delayVal != nil {
					ms = delayVal.ToFloat()
				}
			}
			if ms > 0 {
				d := time.Duration(ms) * time.Millisecond
				if d > 100*time.Millisecond {
					d = 100 * time.Millisecond
				}
				time.Sleep(d)
			}
		}

		if raw == "__async__" {
			global.Delete(asyncKey)
			raw = "500\n{}\nasync handler timeout"
		}
	}

	resp := parseHTTPResponse(raw)
	s.respond(req.ID, resp)
}
