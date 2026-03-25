//go:build !quickjs

package ramune

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

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

// releaseCachedRefs unprotects cached JSC function references.
// Must be called on the JSC goroutine during Runtime.Close().
func (s *bunServerState) releaseCachedRefs(r *Runtime) {
	if s.handleFastFn != 0 {
		r.jsValueUnprotect(r.ctx, s.handleFastFn)
		s.handleFastFn = 0
	}
	if s.handleFn != 0 {
		r.jsValueUnprotect(r.ctx, s.handleFn)
		s.handleFn = 0
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
