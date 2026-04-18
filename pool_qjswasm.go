//go:build qjswasm && !quickjs && !goja

package ramune

// workerLoop mirrors pool_goja.go structurally: drain up to 256 requests
// off the worker's channel, dispatch as a batch, reply. M1-M7 produces
// 500s via handleRequest (M7 implements real dispatch).
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

// handleRequest M1 stub: returns 500 for every incoming request. M7
// wires up the real JS dispatch through __poolHandleFast.
func (p *RuntimePool) handleRequest(r *Runtime, req poolHTTPReq) httpResponse {
	return httpResponse{
		Status: 500,
		Body:   "RuntimePool on qjswasm is not yet wired (M7)",
	}
}

// cacheHandlerRef is a no-op on qjswasm.
func (p *RuntimePool) cacheHandlerRef(r *Runtime) {}
