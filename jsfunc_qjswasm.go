//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"sync/atomic"

	"github.com/fastschema/qjs"
)

// JSFunc wraps a JS function as a Go-callable handle. We keep the
// fastschema *qjs.Value directly (no globalThis refName indirection);
// Close() frees the underlying QuickJS value.
type JSFunc struct {
	noCopy  noCopy
	rt      *Runtime
	fsv     *qjs.Value
	refName string // kept for cross-backend API parity (unused on qjswasm)
	closed  atomic.Bool
}

// Call invokes the JS function with args.
func (f *JSFunc) Call(args ...any) (any, error) {
	if f == nil || f.closed.Load() {
		return nil, ErrNilValue
	}
	if f.rt == nil || f.rt.closed.Load() {
		return nil, ErrAlreadyClosed
	}

	var out any
	var err error
	f.rt.dispatch(func() {
		jsArgs := make([]*qjs.Value, 0, len(args))
		for _, a := range args {
			jv, e := qjs.ToJsValue(f.rt.qjsCtx, a)
			if e != nil {
				err = e
				return
			}
			jsArgs = append(jsArgs, jv)
		}
		res, e := f.rt.qjsCtx.Invoke(f.fsv, nil, jsArgs...)
		if e != nil {
			err = e
			return
		}
		defer res.Free()
		if res.IsNumber() {
			out = res.Float64()
			return
		}
		if res.IsBool() {
			out = res.Bool()
			return
		}
		if res.IsString() {
			out = res.String()
			return
		}
		if res.IsNull() || res.IsUndefined() {
			out = nil
			return
		}
		// Object / array: JSON round-trip for back-compat with other
		// backends (they decode into map[string]any / []any).
		j, e := res.JSONStringify()
		if e != nil {
			err = e
			return
		}
		if e := jsonUnmarshal([]byte(j), &out); e != nil {
			err = e
		}
	})
	return out, err
}

// Close releases the underlying JS function handle.
func (f *JSFunc) Close() error {
	if f == nil || f.closed.Swap(true) {
		return nil
	}
	if f.rt == nil || f.rt.closed.Load() {
		return nil
	}
	f.rt.dispatch(func() {
		if f.fsv != nil {
			f.fsv.Free()
			f.fsv = nil
		}
	})
	return nil
}
