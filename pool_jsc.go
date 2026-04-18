//go:build !quickjs && !goja

package ramune

import (
	"encoding/json"
	"strconv"
	"strings"
)

// workerLoop processes requests from the worker's dedicated channel.
// Batches up to 256 requests per dispatch to minimize overhead.
func (p *RuntimePool) workerLoop(rt *Runtime, ch <-chan poolHTTPReq) {
	var gcCounter int
	interval := rt.gcConfig.GCInterval
	if interval <= 0 {
		interval = 2000
	}

	batch := make([]poolHTTPReq, 0, 256)
	resps := make([]httpResponse, 0, 256)

	for {
		req, ok := <-ch
		if !ok {
			return
		}
		batch = append(batch[:0], req)

		// Drain more from THIS worker's channel without blocking.
		for i := 0; i < 255; i++ {
			select {
			case r, ok := <-ch:
				if !ok {
					goto process
				}
				batch = append(batch, r)
			default:
				goto process
			}
		}

	process:
		resps = resps[:len(batch)]
		rt.dispatch(func() {
			rt.drainUnprotectQueue()
			for i, r := range batch {
				resps[i] = p.handleRequest(rt, r)
			}
			gcCounter += len(batch)
			if gcCounter >= interval {
				gcCounter = 0
				rt.jsGarbageCollect(rt.ctx)
			}
		})

		for i, r := range batch {
			r.respCh <- resps[i]
		}
	}
}

// handleRequest calls the JS handler on a specific Runtime.
func (p *RuntimePool) handleRequest(r *Runtime, req poolHTTPReq) httpResponse {
	r.drainUnprotectQueue()

	methodJS, _ := r.goToJS(req.method)
	urlJS, _ := r.goToJS(req.url)
	bodyJS, _ := r.goToJS(req.body)

	hdCode := r.jsStringCreateWithUTF8CString("(" + req.headersJSON + ")")
	headersJS := r.jsEvaluateScript(r.ctx, hdCode, 0, 0, 0, 0)
	r.jsStringRelease(hdCode)
	if headersJS == 0 {
		headersJS = r.jsValueMakeNull(r.ctx)
	}

	fnPtr := r.poolHandleFn
	if fnPtr == 0 {
		return httpResponse{Status: 500, Body: "handler not installed"}
	}

	fnObj := r.jsValueToObject(r.ctx, fnPtr, 0)
	args := []uintptr{methodJS, urlJS, bodyJS, headersJS}
	result := r.jsObjectCallAsFunction(r.ctx, fnObj, 0, uint64(len(args)), args, 0)

	if result == 0 {
		return httpResponse{Status: 500, Body: "handler error"}
	}

	raw := r.jsValueToGoString(result)

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

func (p *RuntimePool) cacheHandlerRef(r *Runtime) {
	global := r.jsContextGetGlobalObject(r.ctx)
	name := r.jsStringCreateWithUTF8CString("__poolHandleFast")
	fn := r.jsObjectGetProperty(r.ctx, global, name, 0)
	r.jsStringRelease(name)
	if fn != 0 && !r.jsValueIsUndefined(r.ctx, fn) {
		r.jsValueProtect(r.ctx, fn)
		r.poolHandleFn = fn
	}
}
