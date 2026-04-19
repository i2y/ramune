//go:build qjswasm && !quickjs && !goja

package ramune

// workerLoop mirrors pool_goja.go structurally: drain up to 256 requests
// off the worker's channel, dispatch as a batch, reply. handleRequest is
// a stub today and returns 500; real JS dispatch is not yet ported.
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

// handleRequest stub: returns 500. The real __poolHandleFast-based JS
// dispatch (matching pool_goja.go / pool_quickjs.go) is not yet ported to
// qjswasm.
func (p *RuntimePool) handleRequest(r *Runtime, req poolHTTPReq) httpResponse {
	return httpResponse{
		Status: 500,
		Body:   "RuntimePool on qjswasm is not implemented",
	}
}

// cacheHandlerRef is a no-op on qjswasm.
func (p *RuntimePool) cacheHandlerRef(r *Runtime) {}
