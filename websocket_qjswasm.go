//go:build qjswasm && !quickjs && !goja

package ramune

// processWSEvents is the qjswasm WebSocket event pump. M1-M7: no-op, and
// the Bun.serve WebSocket upgrade path is disabled (the server side
// rejects the upgrade). M8 ports the goja implementation.
func (s *bunServerState) processWSEvents(r *Runtime) {
	// No-op until M8.
}
