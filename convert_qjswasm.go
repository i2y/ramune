//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"

	"github.com/fastschema/qjs"
)

// goToJSPublic is the exported-to-package entry for Go→JS conversion.
// Wraps goToJSLocked with a dispatch to the engine goroutine.
func (r *Runtime) goToJSPublic(v any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var out *Value
	var err error
	r.dispatch(func() {
		fv, e := r.goToJSLocked(v)
		if e != nil {
			err = e
			return
		}
		out = r.wrapValue(fv)
	})
	return out, err
}

// goToJSLocked converts a Go value to a *qjs.Value on the engine
// goroutine. Most paths delegate to fastschema's ToJsValue which
// auto-detects numeric / string / map / slice / struct shapes.
func (r *Runtime) goToJSLocked(v any) (*qjs.Value, error) {
	switch x := v.(type) {
	case *Value:
		if x == nil || x.rt != r || x.fsv == nil {
			return r.qjsCtx.NewUndefined(), nil
		}
		// Clone so the caller's ownership contract (returned *qjs.Value
		// is freshly-owned) is preserved.
		return x.fsv.Clone(), nil
	case *JSFunc:
		if x == nil || x.fsv == nil {
			return r.qjsCtx.NewUndefined(), nil
		}
		return x.fsv.Clone(), nil
	}
	return qjs.ToJsValue(r.qjsCtx, v)
}

// jsToGoLocked converts a *qjs.Value into a plain Go value. Delegates
// to the helpers in callback_qjswasm.go via jsArgToGo for consistency
// with callback argument marshaling.
func (r *Runtime) jsToGoLocked(v *qjs.Value) (any, error) {
	return jsArgToGo(r, v), nil
}

// jsonUnmarshal is a small wrapper so callback/jsfunc can share a JSON
// decoder without each importing encoding/json.
func jsonUnmarshal(data []byte, out any) error {
	return json.Unmarshal(data, out)
}
