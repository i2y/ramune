//go:build quickjs

package ramune

import (
	"encoding/json"
	"strconv"
	"strings"

	"modernc.org/quickjs"
)

// workerLoop processes requests from the worker's dedicated channel.
func (p *RuntimePool) workerLoop(rt *Runtime, ch <-chan poolHTTPReq) {
	batch := make([]poolHTTPReq, 0, 256)
	resps := make([]httpResponse, 0, 256)

	for {
		req, ok := <-ch
		if !ok {
			return
		}
		batch = append(batch[:0], req)

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
			for i, r := range batch {
				resps[i] = p.handleRequest(rt, r)
			}
		})

		for i, r := range batch {
			r.respCh <- resps[i]
		}
	}
}

// handleRequest calls the JS handler on a specific Runtime.
func (p *RuntimePool) handleRequest(r *Runtime, req poolHTTPReq) httpResponse {
	code := `__poolHandleFast("` + escJS(req.method) + `","` + escJS(req.url) + `","` + escJS(req.body) + `",` + req.headersJSON + `)`
	result, err := r.vm.Eval(code, quickjs.EvalGlobal)
	if err != nil {
		return httpResponse{Status: 500, Body: "handler error"}
	}

	raw, ok := result.(string)
	if !ok {
		return httpResponse{Status: 500, Body: "handler returned non-string"}
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
	return resp
}

func (p *RuntimePool) cacheHandlerRef(r *Runtime) {
	// QuickJS doesn't need to cache function pointers — we call via eval.
}
