//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/fastschema/qjs"
)

// workerLoop drains up to 256 requests from the worker's channel, dispatches
// them on the engine goroutine as a batch, and ships responses back. Same
// structure as pool_goja.go / pool_quickjs.go.
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

// handleRequest calls __poolHandleFast on the worker's Runtime. The
// handler returns "status\nheadersJSON\nbody" which we split and assemble
// into an httpResponse. Mirrors pool_goja.go's approach.
func (p *RuntimePool) handleRequest(r *Runtime, req poolHTTPReq) httpResponse {
	code := `__poolHandleFast("` + escJS(req.method) + `","` + escJS(req.url) + `","` + escJS(req.body) + `",` + req.headersJSON + `)`
	result, err := r.qjsCtx.Eval("<pool>", qjs.Code(code))
	if err != nil {
		return httpResponse{Status: 500, Body: "handler error: " + err.Error()}
	}
	defer result.Free()

	raw := result.String()
	parts := strings.SplitN(raw, "\n", 3)
	resp := httpResponse{Status: 200}
	if len(parts) >= 1 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			resp.Status = n
		}
	}
	if len(parts) >= 2 && parts[1] != "" {
		_ = json.Unmarshal([]byte(parts[1]), &resp.Headers)
	}
	if len(parts) >= 3 {
		resp.Body = parts[2]
	}
	return resp
}

// cacheHandlerRef is a no-op on qjswasm. fastschema/qjs doesn't expose the
// raw JSValue handle slot needed for the JSC-style fast dispatch; every
// request re-resolves __poolHandleFast by name via Eval.
func (p *RuntimePool) cacheHandlerRef(r *Runtime) {}
