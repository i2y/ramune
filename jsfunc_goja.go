//go:build goja

package ramune

import (
	"fmt"
	"sync/atomic"

	"github.com/dop251/goja"
)

// JSFunc wraps a goja function reference, allowing it to be called from Go.
type JSFunc struct {
	noCopy  noCopy
	rt      *Runtime
	refName string // global name holding the JS function
	closed  atomic.Bool
}

// Call invokes the JavaScript function with the given arguments.
func (f *JSFunc) Call(args ...any) (any, error) {
	if f == nil || f.closed.Load() {
		return nil, ErrNilValue
	}
	var result any
	var err error
	f.rt.dispatch(func() {
		v := f.rt.vm.Get(f.refName)
		if v == nil {
			err = &JSError{Context: "JSFunc.Call", Message: "function reference missing"}
			return
		}
		fn, ok := goja.AssertFunction(v)
		if !ok {
			err = &JSError{Context: "JSFunc.Call", Message: "not a function"}
			return
		}
		jsArgs := make([]goja.Value, len(args))
		for i, arg := range args {
			jv, e := f.rt.goToJS(arg)
			if e != nil {
				err = fmt.Errorf("ramune: JSFunc.Call arg %d: %w", i, e)
				return
			}
			jsArgs[i] = jv
		}
		ret, e := fn(goja.Undefined(), jsArgs...)
		if e != nil {
			err = &JSError{Context: "JSFunc.Call", Message: e.Error()}
			return
		}
		if ret == nil {
			result = nil
			return
		}
		exp := ret.Export()
		// Normalize numbers to float64 for parity with JSC/QuickJS JSFunc.Call.
		switch n := exp.(type) {
		case int:
			result = float64(n)
		case int64:
			result = float64(n)
		case int32:
			result = float64(n)
		default:
			result = exp
		}
	})
	return result, err
}

// Close releases the JavaScript function reference.
func (f *JSFunc) Close() error {
	if f == nil || f.closed.Swap(true) {
		return nil
	}
	if f.rt.closed.Load() {
		return nil
	}
	f.rt.dispatch(func() {
		f.rt.vm.Set(f.refName, goja.Undefined())
	})
	return nil
}
