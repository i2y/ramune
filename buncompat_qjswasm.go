//go:build qjswasm && !quickjs && !goja

package ramune

// processRequests drains pending HTTP requests and dispatches them to the
// JS handler. M1-M7: identical channel-drain loop to the goja/quickjs
// backends; M8 adds a cached __bunHandleFast trampoline.
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

// handleSingleRequest M1 stub: returns 500 for every incoming request.
// This lets backend-agnostic eventloop / HTTP plumbing compile and run;
// real request handling wires up with M7.
func (s *bunServerState) handleSingleRequest(r *Runtime, req pendingHTTPReq) {
	s.respond(req.ID, httpResponse{
		Status: 500,
		Body:   "Bun.serve on qjswasm is not yet wired (M7)",
	})
}
