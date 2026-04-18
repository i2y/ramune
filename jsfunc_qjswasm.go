//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// JSFunc wraps a JS function as a Go-callable handle. Like the QuickJS
// (modernc) backend we keep the JS function alive by stashing it in
// globalThis[refName], which lets us reuse the same __jsfunc_ref JSON
// protocol end-to-end.
type JSFunc struct {
	noCopy  noCopy
	rt      *Runtime
	refName string
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
		// Pull the function value from globalThis[refName].
		code := fmt.Sprintf("globalThis[%q]", f.refName)
		fnH, e := f.rt.rawEvalLocked(code, "<jsfunc.call>", 0)
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(fnH) {
			err = f.rt.pullExceptionLocked()
			return
		}
		defer f.rt.freeValueLocked(fnH)

		argsJSON, e := json.Marshal(args)
		if e != nil {
			err = e
			return
		}
		argPtr, argLen, e := f.rt.writeStringLocked(string(argsJSON))
		if e != nil {
			err = e
			return
		}
		defer f.rt.wasmFreeLocked(argPtr)
		res, e := f.rt.wzExp.valCall.Call(f.rt.wzCtx,
			uint64(f.rt.qjsCtx), fnH, 0,
			uint64(argPtr), uint64(argLen))
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(res[0]) {
			err = f.rt.pullExceptionLocked()
			return
		}
		out, err = f.rt.jsToGoLocked(res[0])
		f.rt.freeValueLocked(res[0])
	})
	return out, err
}

// Close removes the globalThis reference that keeps the JS function
// alive.
func (f *JSFunc) Close() error {
	if f == nil || f.closed.Swap(true) {
		return nil
	}
	if f.rt == nil || f.rt.closed.Load() {
		return nil
	}
	f.rt.dispatch(func() {
		_ = f.rt.execLocked(fmt.Sprintf("delete globalThis[%q]", f.refName))
	})
	return nil
}
