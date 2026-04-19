//go:build qjswasm && !quickjs && !goja

package ramune

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

// ensureHandlerCached is a no-op for qjswasm (we call via eval).
func (s *bunServerState) ensureHandlerCached(r *Runtime) {}

// handleSingleRequest stub: returns 500 for every incoming request so the
// backend-agnostic eventloop / HTTP plumbing still runs. The real dispatch
// (matching buncompat_quickjs.go) is not yet ported to qjswasm.
func (s *bunServerState) handleSingleRequest(r *Runtime, req pendingHTTPReq) {
	s.respond(req.ID, httpResponse{
		Status: 500,
		Body:   "Bun.serve on qjswasm is not implemented",
	})
}
