//go:build goja

package ramune

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/dop251/goja"
)

// goToJS converts a Go value to a goja.Value.
func (r *Runtime) goToJS(v any) (goja.Value, error) {
	if v == nil {
		return goja.Null(), nil
	}
	switch val := v.(type) {
	case bool:
		return r.vm.ToValue(val), nil
	case int:
		return r.vm.ToValue(val), nil
	case int64:
		return r.vm.ToValue(val), nil
	case float64:
		return r.vm.ToValue(val), nil
	case string:
		return r.vm.ToValue(val), nil
	case *Value:
		if val == nil || val.val == nil {
			return goja.Undefined(), nil
		}
		return val.val, nil
	case *JSFunc:
		if val == nil || val.refName == "" {
			return goja.Null(), nil
		}
		v := r.vm.Get(val.refName)
		if v == nil {
			return goja.Null(), nil
		}
		return v, nil
	case map[string]any:
		// Round-trip through JSON so nested structures become native JS objects
		// rather than live Go reflection wrappers (matches JSC/QuickJS semantics).
		data, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("cannot marshal map: %w", err)
		}
		return r.safeRunString("(" + string(data) + ")")
	case []any:
		data, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("cannot marshal slice: %w", err)
		}
		return r.safeRunString(string(data))
	default:
		rv := reflect.ValueOf(v)
		origVal := rv

		// Promise detection (has Await method).
		if rv.Kind() == reflect.Ptr && rv.MethodByName("Await").IsValid() {
			return r.promiseToJSGoja(rv)
		}

		// Slice -> []any -> JS array.
		if rv.Kind() == reflect.Slice {
			elems := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				elems[i] = rv.Index(i).Interface()
			}
			return r.goToJS(elems)
		}
		// Map[string]T -> map[string]any.
		if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
			m := make(map[string]any, rv.Len())
			iter := rv.MapRange()
			for iter.Next() {
				m[iter.Key().String()] = iter.Value().Interface()
			}
			return r.goToJS(m)
		}

		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			return r.structToJSObjectGoja(origVal, rv)
		}
		return nil, fmt.Errorf("unsupported type: %T", v)
	}
}

// structToJSObjectGoja creates a JS object with live getter/setter properties and methods.
// Uses the shared native_instance.go registry for consistency with JSC/QuickJS backends.
func (r *Runtime) structToJSObjectGoja(origVal, rv reflect.Value) (goja.Value, error) {
	methodTarget := origVal
	if methodTarget.Kind() != reflect.Ptr {
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		methodTarget = ptr
	}

	r.ensureNativeReg()
	info := r.nativeReg.ensureTypeRegistered(r, rv.Type())
	instanceID := r.nativeReg.registerInstance(methodTarget)
	jsCode := info.generateJSObject(instanceID)

	result, err := r.safeRunString(jsCode)
	if err != nil {
		r.nativeReg.releaseInstance(instanceID)
		return nil, fmt.Errorf("structToJSObjectGoja: %w", err)
	}
	return result, nil
}

// promiseToJSGoja converts a Go *promise.Promise[T] to a JS Promise for goja.
func (r *Runtime) promiseToJSGoja(rv reflect.Value) (goja.Value, error) {
	r.nativeMethodSeq++
	seq := r.nativeMethodSeq
	resolveName := fmt.Sprintf("__promise_resolve_%d", seq)
	rejectName := fmt.Sprintf("__promise_reject_%d", seq)

	jsCode := fmt.Sprintf(
		`new Promise(function(resolve, reject) {
			globalThis.%s = function(v) { resolve(v); };
			globalThis.%s = function(e) { reject(e); };
		})`,
		resolveName, rejectName,
	)
	jsPromise, err := r.safeRunString(jsCode)
	if err != nil {
		return nil, fmt.Errorf("promiseToJSGoja: %w", err)
	}

	awaitMethod := rv.MethodByName("Await")

	// Mark this bridge as pending so RunEventLoopFor doesn't return before
	// the resolver fires. Decrement after the dispatched resolve/reject runs.
	r.nativePromiseCount.Add(1)
	go func() {
		defer r.nativePromiseCount.Add(-1)
		results := awaitMethod.Call(nil)
		r.dispatch(func() {
			if len(results) >= 2 && !results[1].IsNil() {
				errMsg := results[1].Interface().(error).Error()
				_, _ = r.safeRunString(fmt.Sprintf("globalThis.%s(%q);delete globalThis.%s;delete globalThis.%s;",
					rejectName, errMsg, resolveName, rejectName))
			} else if len(results) >= 1 {
				v := results[0].Interface()
				jsVal, convErr := r.goToJS(v)
				if convErr != nil {
					_, _ = r.safeRunString(fmt.Sprintf("globalThis.%s(%q);delete globalThis.%s;delete globalThis.%s;",
						rejectName, convErr.Error(), resolveName, rejectName))
					return
				}
				tmpName := fmt.Sprintf("__pval_%d", seq)
				r.vm.Set(tmpName, jsVal)
				_, _ = r.safeRunString(fmt.Sprintf("globalThis.%s(globalThis.%s);delete globalThis.%s;delete globalThis.%s;delete globalThis.%s;",
					resolveName, tmpName, resolveName, rejectName, tmpName))
			}
		})
		r.Wake()
	}()

	return jsPromise, nil
}

// jsToGo converts a goja value to a Go value.
// Numbers are normalized to float64 to match JSC/QuickJS backend semantics.
func (r *Runtime) jsToGo(v goja.Value) (any, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	exp := v.Export()
	switch n := exp.(type) {
	case int:
		return float64(n), nil
	case int8:
		return float64(n), nil
	case int16:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint:
		return float64(n), nil
	case uint8:
		return float64(n), nil
	case uint16:
		return float64(n), nil
	case uint32:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case float32:
		return float64(n), nil
	}
	return exp, nil
}
