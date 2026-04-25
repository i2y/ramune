//go:build qjswasm && !goja

package ramune

import (
	"strconv"
	"time"

	"github.com/i2y/ramune/third_party/qjs"
)

// processRequests drains pending HTTP requests and dispatches them to the
// JS handler. Same channel-drain structure as the goja/quickjs backends.
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

// ensureHandlerCached is a no-op for qjswasm (we eval the handler by name
// on each request, same as the modernc quickjs backend).
func (s *bunServerState) ensureHandlerCached(r *Runtime) {}

// handleSingleRequest evaluates the JS HTTP handler. When wsEnabled we must
// route through __bunHandle so the JS side can set req._reqId — server.upgrade(req)
// needs it to identify the pending upgrade; __bunHandleFast omits that arg.
func (s *bunServerState) handleSingleRequest(r *Runtime, req pendingHTTPReq) {
	bodyEnc, bodyB64 := encodeBodyForJS(req.Body)
	b64Flag := "false"
	if bodyB64 {
		b64Flag = "true"
	}
	var code string
	if s.wsEnabled {
		code = `__bunHandle(` + strconv.Itoa(req.ID) + `,"` + escJS(req.Method) + `","` + escJS(req.URL) + `","` + escJS(bodyEnc) + `",` + req.HeadersJSON + `,` + b64Flag + `)`
	} else {
		code = `__bunHandleFast("` + escJS(req.Method) + `","` + escJS(req.URL) + `","` + escJS(bodyEnc) + `",` + req.HeadersJSON + `,` + b64Flag + `)`
	}
	result, err := r.qjsCtx.Eval("<bun>", qjs.Code(code))
	if err != nil {
		s.respond(req.ID, httpResponse{Status: 500, Body: "handler error: " + err.Error()})
		return
	}

	raw := result.String()
	result.Free()

	if raw == "__upgrade__" {
		return
	}

	if raw == "__async__" {
		asyncKey := asyncRespKey(req.ID)
		setupCode := `globalThis.__bunPendingPromise.then(` +
			`function(v){globalThis['` + asyncKey + `']=__bunExtract(v);},` +
			`function(e){globalThis['` + asyncKey + `']='500\n{}\n'+String(e);});` +
			`delete globalThis.__bunPendingPromise;`
		if _, setupErr := r.qjsCtx.Eval("<bun-async>", qjs.Code(setupCode)); setupErr != nil {
			r.qjsCtx.Eval("<bun-cleanup>", qjs.Code("delete globalThis.__bunPendingPromise"))
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

			r.qjsCtx.Eval("<bun-tick>", qjs.Code("__eventLoop.tick()"))
			checkResult, _ := r.qjsCtx.Eval("<bun-check>", qjs.Code(checkCode))
			if checkResult != nil && !checkResult.IsUndefined() {
				if checkResult.IsString() {
					raw = checkResult.String()
				}
				checkResult.Free()
				r.qjsCtx.Eval("<bun-cleanup>", qjs.Code("delete globalThis['"+asyncKey+"']"))
				break
			}
			if checkResult != nil {
				checkResult.Free()
			}

			delayResult, _ := r.qjsCtx.Eval("<bun-delay>", qjs.Code("__eventLoop.nextDelay()"))
			if delayResult != nil {
				if ms := delayResult.Float64(); ms > 0 {
					d := time.Duration(ms) * time.Millisecond
					if d > 100*time.Millisecond {
						d = 100 * time.Millisecond
					}
					time.Sleep(d)
				}
				delayResult.Free()
			}
		}

		if raw == "__async__" {
			r.qjsCtx.Eval("<bun-cleanup>", qjs.Code("delete globalThis['"+asyncKey+"']"))
			raw = "500\n{}\nasync handler timeout"
		}
	}

	resp := parseHTTPResponse(raw)
	s.respond(req.ID, resp)
}
