//go:build quickjs

package ramune

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"modernc.org/quickjs"
)

// JSFunc wraps a JavaScript function reference, allowing it to be called from Go.
// Created automatically when a JS function is passed as an argument to a GoFunc callback.
// Call Close() when done to release the JS reference.
type JSFunc struct {
	noCopy  noCopy
	rt      *Runtime
	refName string // temp global name holding the JS function
	closed  atomic.Bool
}

// Call invokes the JavaScript function with the given arguments.
// Returns the result converted to a Go value.
// Safe to call from any goroutine and from within GoFunc callbacks.
func (f *JSFunc) Call(args ...any) (any, error) {
	if f == nil || f.closed.Load() {
		return nil, ErrNilValue
	}
	var result any
	var err error
	f.rt.dispatch(func() {
		argCode := ""
		for i, arg := range args {
			if i > 0 {
				argCode += ","
			}
			b, e := json.Marshal(arg)
			if e != nil {
				err = fmt.Errorf("ramune: JSFunc.Call arg %d: %w", i, e)
				return
			}
			argCode += string(b)
		}
		callCode := "globalThis[" + jsQuoteName(f.refName) + "](" + argCode + ")"
		r, e := f.rt.vm.Eval(callCode, quickjs.EvalGlobal)
		if e != nil {
			err = &JSError{Context: "JSFunc.Call", Message: e.Error()}
			return
		}
		// Normalize int → float64 to match JSC backend behavior.
		if n, ok := r.(int); ok {
			result = float64(n)
		} else {
			result = r
		}
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
		return nil
	}
	f.rt.dispatch(func() {
		f.rt.execLocked("delete globalThis[" + jsQuoteName(f.refName) + "]")
	})
	return nil
}
