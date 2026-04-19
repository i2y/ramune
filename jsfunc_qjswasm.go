//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"
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

// Call invokes the JS function with args. Uses the dedicated
// global_get_prop + val_call shim exports (not the `eval` export) to
// avoid wazero compiler mode's eval-reentry corruption when Call is
// invoked from inside another Go callback.
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
		fnH, e := f.rt.globalGetPropLocked(f.refName)
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
// alive. Uses global_delete_prop rather than the `eval` export to avoid
// the wazero compiler-mode re-entry corruption.
func (f *JSFunc) Close() error {
	if f == nil || f.closed.Swap(true) {
		return nil
	}
	if f.rt == nil || f.rt.closed.Load() {
		return nil
	}
	f.rt.dispatch(func() {
		_ = f.rt.globalDeletePropLocked(f.refName)
	})
	return nil
}
