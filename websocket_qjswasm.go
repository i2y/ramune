//go:build qjswasm && !quickjs && !goja

package ramune

// processWSEvents is the qjswasm WebSocket event pump. Currently a
// no-op — Bun.serve's WebSocket upgrade path is not ported, so the
// server side never produces events for the pump to drain.
func (s *bunServerState) processWSEvents(r *Runtime) {}
