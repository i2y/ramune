//go:build quickjs

package ramune

import (
	"encoding/json"
	"fmt"
	"reflect"

	"modernc.org/quickjs"
)

// goToJS converts a Go value to a QuickJS value.
func (r *Runtime) goToJS(v any) (quickjs.Value, error) {
	if v == nil {
		result, err := r.vm.EvalValue("null", quickjs.EvalGlobal)
		return result, err
	}
	switch val := v.(type) {
	case bool:
		if val {
			r, err := r.vm.EvalValue("true", quickjs.EvalGlobal)
			return r, err
		}
		r, err := r.vm.EvalValue("false", quickjs.EvalGlobal)
		return r, err
	case int:
		return r.vm.NewInt(val), nil
	case int64:
		return r.vm.NewInt(int(val)), nil
	case float64:
		return r.vm.NewFloat64(val), nil
	case string:
		return r.vm.NewString(val)
	case *Value:
		if val == nil {
			r, err := r.vm.EvalValue("undefined", quickjs.EvalGlobal)
			return r, err
		}
		return val.val.Dup(), nil
	case *JSFunc:
		if val == nil || val.refName == "" {
			return r.vm.EvalValue("null", quickjs.EvalGlobal)
		}
		return r.vm.EvalValue("globalThis["+jsQuoteName(val.refName)+"]", quickjs.EvalGlobal)
	case map[string]any:
		data, err := json.Marshal(val)
		if err != nil {
			return quickjs.Value{}, fmt.Errorf("cannot marshal map: %w", err)
		}
		return r.vm.EvalValue(fmt.Sprintf("(%s)", string(data)), quickjs.EvalGlobal)
	case []any:
		data, err := json.Marshal(val)
		if err != nil {
			return quickjs.Value{}, fmt.Errorf("cannot marshal slice: %w", err)
		}
		return r.vm.EvalValue(string(data), quickjs.EvalGlobal)
	default:
		rv := reflect.ValueOf(v)
		origVal := rv

		// Check for Promise type (has Await() method) → convert to JS Promise
		if rv.Kind() == reflect.Ptr && rv.MethodByName("Await").IsValid() {
			return r.promiseToJSQJS(rv)
		}

		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			return r.structToJSObjectQJS(origVal, rv)
		}
		return quickjs.Value{}, fmt.Errorf("unsupported type: %T", v)
	}
}

// structToJSObjectQJS creates a JS object with live getter/setter properties and methods.
// Uses per-type callback registration to avoid leaking GoFuncs.
func (r *Runtime) structToJSObjectQJS(origVal, rv reflect.Value) (quickjs.Value, error) {
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

	jsObj, err := r.evalScriptLocked(jsCode, "native-struct")
	if err != nil {
		r.nativeReg.releaseInstance(instanceID)
		return quickjs.Value{}, fmt.Errorf("structToJSObjectQJS: %w", err)
	}
	return jsObj, nil
}

// promiseToJSQJS converts a Go *promise.Promise[T] to a JS Promise for QuickJS.
func (r *Runtime) promiseToJSQJS(rv reflect.Value) (quickjs.Value, error) {
	r.nativeMethodSeq++
	seq := r.nativeMethodSeq
	resolveName := fmt.Sprintf("__promise_resolve_%d", seq)
	rejectName := fmt.Sprintf("__promise_reject_%d", seq)

	// Create JS Promise and store resolve/reject as globals
	jsCode := fmt.Sprintf(
		`new Promise(function(resolve, reject) {
			globalThis.%s = function(v) { resolve(v); };
			globalThis.%s = function(e) { reject(e); };
		})`,
		resolveName, rejectName,
	)
	jsPromise, err := r.evalScriptLocked(jsCode, "promise-bridge")
	if err != nil {
		return quickjs.Value{}, fmt.Errorf("promiseToJSQJS: %w", err)
	}

	awaitMethod := rv.MethodByName("Await")

	go func() {
		results := awaitMethod.Call(nil)
		r.dispatch(func() {
			if len(results) >= 2 && !results[1].IsNil() {
				errMsg := results[1].Interface().(error).Error()
				r.execLocked(fmt.Sprintf("globalThis.%s(%q);delete globalThis.%s;delete globalThis.%s;",
					rejectName, errMsg, resolveName, rejectName))
			} else if len(results) >= 1 {
				val := results[0].Interface()
				// Use goToJS + temp global to preserve struct methods/live binding
				jsVal, convErr := r.goToJS(val)
				if convErr != nil {
					r.execLocked(fmt.Sprintf("globalThis.%s(%q);delete globalThis.%s;delete globalThis.%s;",
						rejectName, convErr.Error(), resolveName, rejectName))
					return
				}
				tmpVal := fmt.Sprintf("__pval_%d", seq)
				atom, _ := r.vm.NewAtom(tmpVal)
				global := r.vm.GlobalObject()
				global.SetPropertyValue(atom, jsVal)
				global.Free()
				r.execLocked(fmt.Sprintf("globalThis.%s(globalThis.%s);delete globalThis.%s;delete globalThis.%s;delete globalThis.%s;",
					resolveName, tmpVal, resolveName, rejectName, tmpVal))
			}
		})
		r.Wake()
	}()

	return jsPromise, nil
}

// jsToGo converts a QuickJS value to a Go value.
func (r *Runtime) jsToGo(v quickjs.Value) (any, error) {
	return v.Any()
}
