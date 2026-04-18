//go:build !quickjs && !goja

package ramune

import (
	"fmt"
	"strconv"
)

// processWSEvents processes pending WebSocket events by calling JS handlers.
// Must be called with rt.mu held (inside event loop tick).
func (s *bunServerState) processWSEvents(r *Runtime) {
	if s == nil || s.wsMgr == nil {
		return
	}

	events := s.wsMgr.drain()
	if len(events) == 0 {
		return
	}

	for _, ev := range events {
		var code string
		switch ev.Kind {
		case "open":
			code = fmt.Sprintf("__wsHandleEvent('open',%d,'')", ev.ConnID)
		case "message":
			code = `__wsHandleEvent('message',` + strconv.Itoa(ev.ConnID) + `,"` + escJS(ev.Data) + `")`
		case "close":
			code = fmt.Sprintf("__wsHandleEvent('close',%d,'')", ev.ConnID)
		case "error":
			code = `__wsHandleEvent('error',` + strconv.Itoa(ev.ConnID) + `,"` + escJS(ev.Data) + `")`
		default:
			continue
		}

		jsStr := r.jsStringCreateWithUTF8CString(code)
		r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, 0)
		r.jsStringRelease(jsStr)
	}
}
