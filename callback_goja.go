//go:build goja

package ramune

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/dop251/goja"
)

// RegisterFunc registers a Go function as a global JavaScript function.
func (r *Runtime) RegisterFunc(name string, fn GoFunc) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}
	var err error
	r.dispatch(func() {
		err = r.registerFuncLocked(name, fn)
	})
	return err
}

// registerFuncLocked registers a Go function on the engine goroutine.
// Each registered function is a native goja FunctionCall handler that:
//  1. Converts JS args to Go via Export(), mirroring the JSON-based semantics
//     of the QuickJS backend (so struct/function args look identical to GoFunc).
//  2. Calls fn and converts the result via goToJS.
func (r *Runtime) registerFuncLocked(name string, fn GoFunc) error {
	id := len(r.goFuncs)
	r.goFuncs = append(r.goFuncs, fn)

	wrapper := func(call goja.FunctionCall) goja.Value {
		goArgs := make([]any, len(call.Arguments))
		for i, arg := range call.Arguments {
			if arg == nil || goja.IsUndefined(arg) || goja.IsNull(arg) {
				goArgs[i] = nil
				continue
			}
			// If the arg is a function, wrap it as JSFunc (per QuickJS/JSC behavior).
			if _, ok := goja.AssertFunction(arg); ok {
				// Stash the function under a unique global so JSFunc.Call can dispatch.
				r.nativeMethodSeq++
				refName := fmt.Sprintf("__jsfunc_%d", r.nativeMethodSeq)
				r.vm.Set(refName, arg)
				goArgs[i] = &JSFunc{rt: r, refName: refName}
				continue
			}
			// Normalize to match the JSC/QuickJS backends' argument shapes:
			//   - bool / string / nil pass through directly
			//   - all numeric types become float64
			//   - arrays / objects / structs: JSON round-trip so every nested
			//     number is also float64, matching the other backends exactly
			exp := arg.Export()
			switch v := exp.(type) {
			case bool, string, nil:
				goArgs[i] = v
			case float64:
				goArgs[i] = v
			case float32:
				goArgs[i] = float64(v)
			case int:
				goArgs[i] = float64(v)
			case int8:
				goArgs[i] = float64(v)
			case int16:
				goArgs[i] = float64(v)
			case int32:
				goArgs[i] = float64(v)
			case int64:
				goArgs[i] = float64(v)
			case uint:
				goArgs[i] = float64(v)
			case uint8:
				goArgs[i] = float64(v)
			case uint16:
				goArgs[i] = float64(v)
			case uint32:
				goArgs[i] = float64(v)
			case uint64:
				goArgs[i] = float64(v)
			default:
				// JSON round-trip normalizes shape + recurses the number fix.
				if b, err := json.Marshal(v); err == nil {
					var out any
					if e := json.Unmarshal(b, &out); e == nil {
						goArgs[i] = out
						continue
					}
				}
				goArgs[i] = exp
			}
		}

		result, err := fn(goArgs)
		if err != nil {
			// Throw a plain JS Error: NewGoError stringifies as "GoError: <msg>",
			// but tests expect the raw message (matching JSC/QuickJS semantics).
			panic(r.newJSError(err))
		}

		jv, convErr := r.goToJS(result)
		if convErr != nil {
			panic(r.newJSError(convErr))
		}
		return jv
	}

	r.vm.Set(name, wrapper)
	// Silence unused-variable warning; id is a debug marker mirroring the other backends.
	_ = id
	return nil
}

// newJSError constructs a JS Error with the given Go error's message and throws
// it with a bare string toString, matching the shape of a "throw new Error(...)"
// that the JSC/QuickJS backends produce.
func (r *Runtime) newJSError(err error) goja.Value {
	msg := err.Error()
	// Use RunString to get a native Error constructor without the "GoError:"
	// prefix that NewGoError adds.
	r.vm.Set("__rampErrMsg", msg)
	defer r.vm.Set("__rampErrMsg", goja.Undefined())
	v, _ := r.safeRunString("(function(m){var e = new Error(m); e.toString = function(){return m}; return e;})(__rampErrMsg)")
	if v != nil {
		return v
	}
	return r.vm.NewGoError(err)
}

// Compile-time sanity: the goFuncs slice and GoFunc type must be usable.
var _ reflect.Type = reflect.TypeOf((*GoFunc)(nil)).Elem()
