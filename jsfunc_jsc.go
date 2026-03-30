//go:build !quickjs

package ramune

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// JSFunc wraps a JavaScript function reference, allowing it to be called from Go.
// Created automatically when a JS function is passed as an argument to a GoFunc callback.
// Call Close() when done to release the JS reference.
type JSFunc struct {
	noCopy noCopy
	rt     *Runtime
	ptr    uintptr // JSObjectRef (protected)
	closed atomic.Bool
}

func (r *Runtime) newJSFunc(ptr uintptr) *JSFunc {
	r.jsValueProtect(r.ctx, ptr)
	r.unprotectMu.Lock()
	r.protectedPtrs = append(r.protectedPtrs, ptr)
	r.unprotectMu.Unlock()
	return &JSFunc{rt: r, ptr: ptr}
}

// Call invokes the JavaScript function with the given arguments.
// Returns the result converted to a Go value (bool, float64, string, nil,
// map[string]any, or []any).
// Safe to call from any goroutine and from within GoFunc callbacks.
func (f *JSFunc) Call(args ...any) (any, error) {
	if f == nil || f.closed.Load() {
		return nil, ErrNilValue
	}
	var result any
	var err error
	f.rt.dispatch(func() {
		jsArgs := make([]uintptr, len(args))
		for i, arg := range args {
			jsVal, e := f.rt.goToJS(arg)
			if e != nil {
				err = fmt.Errorf("ramune: JSFunc.Call arg %d: %w", i, e)
				return
			}
			jsArgs[i] = jsVal
		}

		var exc uintptr
		ret := f.rt.jsObjectCallAsFunction(
			f.rt.ctx, f.ptr, 0,
			uint64(len(jsArgs)), jsArgs,
			uintptr(unsafe.Pointer(&exc)),
		)
		if ret == 0 {
			if exc != 0 {
				msg, stack := f.rt.getExceptionInfo(exc)
				err = &JSError{Context: "JSFunc.Call", Message: msg, Stack: stack}
				return
			}
			err = &JSError{Context: "JSFunc.Call", Message: "JavaScript exception occurred"}
			return
		}

		result = f.rt.jsToGoCallback(ret)
	})
	return result, err
}

// Close releases the JavaScript function reference.
// Safe to call multiple times or on nil.
func (f *JSFunc) Close() error {
	if f == nil || f.closed.Swap(true) {
		return nil
	}
	if f.rt.closed.Load() {
		f.ptr = 0
		return nil
	}
	f.rt.unprotectMu.Lock()
	f.rt.unprotectQueue = append(f.rt.unprotectQueue, f.ptr)
	f.rt.unprotectMu.Unlock()
	f.ptr = 0
	return nil
}
