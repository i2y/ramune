//go:build qjswasm && !goja

package ramune

import (
	"encoding/json"
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
		defer func() {
			for _, jv := range jsArgs {
				jv.Free()
			}
		}()
		for _, a := range args {
			jv, e := f.rt.goToJSLocked(a)
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
		j, e := res.JSONStringify()
		if e != nil {
			err = e
			return
		}
		if e := json.Unmarshal([]byte(j), &out); e != nil {
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
