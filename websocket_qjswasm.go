//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"strconv"

	"github.com/fastschema/qjs"
)

// processWSEvents drains pending WebSocket events and dispatches them
// to the JS handler registered via __wsHandleEvent.
func (s *bunServerState) processWSEvents(r *Runtime) {
	if s == nil || s.wsMgr == nil {
		return
	}
	events := s.wsMgr.drain()
	if len(events) == 0 {
		return
	}
	for _, ev := range events {
		var kind string
		switch ev.Kind {
		case "open", "message", "close", "error":
			kind = ev.Kind
		default:
			continue
		}
		code := `__wsHandleEvent('` + kind + `',` + strconv.Itoa(ev.ConnID) + `,"` + escJS(ev.Data) + `")`
		if v, err := r.qjsCtx.Eval("<ws>", qjs.Code(code)); err == nil && v != nil {
			v.Free()
		}
	}
}
