//go:build quickjs

package ramune

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"modernc.org/quickjs"
)

// processRequests processes pending HTTP requests by calling the JS handler.
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

// ensureHandlerCached is a no-op for QuickJS (we call via eval).
func (s *bunServerState) ensureHandlerCached(r *Runtime) {}

func (s *bunServerState) handleSingleRequest(r *Runtime, req pendingHTTPReq) {
	code := `__bunHandleFast("` + escJS(req.Method) + `","` + escJS(req.URL) + `","` + escJS(req.Body) + `",` + req.HeadersJSON + `)`
	result, err := r.vm.Eval(code, quickjs.EvalGlobal)
	r.executePendingJobs()
	if err != nil {
		s.respond(req.ID, httpResponse{Status: 500, Body: "handler error"})
		return
	}

	raw, ok := result.(string)
	if !ok {
		s.respond(req.ID, httpResponse{Status: 500, Body: "handler returned non-string"})
		return
	}

	if raw == "__upgrade__" {
		return
	}

	if raw == "__async__" {
		asyncKey := "__resp" + strconv.Itoa(req.ID)
		setupCode := `globalThis.__bunPendingPromise.then(` +
			`function(v){globalThis['` + asyncKey + `']=__bunExtract(v);},` +
			`function(e){globalThis['` + asyncKey + `']='500\n{}\n'+String(e);});` +
			`delete globalThis.__bunPendingPromise;`
		_, setupErr := r.vm.Eval(setupCode, quickjs.EvalGlobal)
		r.executePendingJobs()
		if setupErr != nil {
			r.vm.Eval("delete globalThis.__bunPendingPromise", quickjs.EvalGlobal)
			s.respond(req.ID, httpResponse{Status: 500, Body: "async setup failed"})
			return
		}

		checkCode := `globalThis['` + asyncKey + `']`
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if r.procMgr != nil {
				r.procMgr.processEvents(r)
			}
			if r.sockMgr != nil {
				r.sockMgr.processEvents(r)
			}

			r.vm.Eval("__eventLoop.tick()", quickjs.EvalGlobal)
			r.executePendingJobs()
			checkResult, _ := r.vm.EvalValue(checkCode, quickjs.EvalGlobal)

			if !checkResult.IsUndefined() {
				a, _ := checkResult.Any()
				checkResult.Free()
				if a != nil {
					if s, ok := a.(string); ok {
						raw = s
					}
					r.vm.Eval("delete globalThis['"+asyncKey+"']", quickjs.EvalGlobal)
					break
				}
			} else {
				checkResult.Free()
			}

			delayResult, _ := r.vm.Eval("__eventLoop.nextDelay()", quickjs.EvalGlobal)
			if ms, ok := delayResult.(float64); ok && ms > 0 {
				d := time.Duration(ms) * time.Millisecond
				if d > 100*time.Millisecond {
					d = 100 * time.Millisecond
				}
				time.Sleep(d)
			}
		}

		if raw == "__async__" {
			r.vm.Eval("delete globalThis['"+asyncKey+"']", quickjs.EvalGlobal)
			raw = "500\n{}\nasync handler timeout"
		}
	}

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
