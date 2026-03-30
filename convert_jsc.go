//go:build !quickjs

package ramune

import (
	"fmt"
	"reflect"
	"runtime"
	"unsafe"
)

// JSType constants matching JavaScriptCore's JSType enum.
const (
	jsTypeUndefined int32 = 0
	jsTypeNull      int32 = 1
	jsTypeBoolean   int32 = 2
	jsTypeNumber    int32 = 3
	jsTypeString    int32 = 4
	jsTypeObject    int32 = 5
	jsTypeSymbol    int32 = 6
)

// goToJS converts a Go value to a JSValueRef.
// Must be called with rt.mu held.
func (r *Runtime) goToJS(v any) (uintptr, error) {
	switch val := v.(type) {
	case *Value:
		if val == nil || val.ptr == 0 {
			return r.jsValueMakeNull(r.ctx), nil
		}
		runtime.KeepAlive(val)
		return val.ptr, nil
	case *JSFunc:
		if val == nil || val.ptr == 0 {
			return r.jsValueMakeNull(r.ctx), nil
		}
		return val.ptr, nil
	case bool:
		return r.jsValueMakeBoolean(r.ctx, val), nil
	case int:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case int8:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case int16:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case int32:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case int64:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case uint:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case uint8:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case uint16:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case uint32:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case float32:
		return r.jsValueMakeNumber(r.ctx, float64(val)), nil
	case float64:
		return r.jsValueMakeNumber(r.ctx, val), nil
	case string:
		jsStr := r.jsStringCreateWithUTF8CString(val)
		defer r.jsStringRelease(jsStr)
		return r.jsValueMakeString(r.ctx, jsStr), nil
	case nil:
		return r.jsValueMakeNull(r.ctx), nil
	case []byte:
		if r.jsObjectMakeTypedArray == nil {
			return 0, fmt.Errorf("ramune: TypedArray API not available")
		}
		var exc uintptr
		obj := r.jsObjectMakeTypedArray(r.ctx, jsTypedArrayTypeUint8Array, uint64(len(val)), uintptr(unsafe.Pointer(&exc)))
		if exc != 0 {
			msg := r.jsValueToGoString(exc)
			return 0, fmt.Errorf("ramune: JSObjectMakeTypedArray: %s", msg)
		}
		if len(val) > 0 {
			bytesPtr := r.jsObjectGetTypedArrayBytesPtr(r.ctx, obj, 0)
			if bytesPtr != 0 {
				dst := unsafe.Slice((*byte)(unsafe.Pointer(bytesPtr)), len(val))
				copy(dst, val)
			}
		}
		return obj, nil
	case map[string]any:
		obj := r.jsObjectMake(r.ctx, 0, 0)
		if obj == 0 {
			return 0, fmt.Errorf("ramune: JSObjectMake returned NULL")
		}
		for key, elem := range val {
			jsVal, err := r.goToJS(elem)
			if err != nil {
				return 0, fmt.Errorf("ramune: map key %q: %w", key, err)
			}
			jsKey := r.jsStringCreateWithUTF8CString(key)
			r.jsObjectSetProperty(r.ctx, obj, jsKey, jsVal, 0, 0)
			r.jsStringRelease(jsKey)
		}
		return obj, nil
	case []any:
		jsArgs := make([]uintptr, len(val))
		for i, elem := range val {
			jsVal, err := r.goToJS(elem)
			if err != nil {
				return 0, fmt.Errorf("ramune: slice index %d: %w", i, err)
			}
			jsArgs[i] = jsVal
		}
		var exc uintptr
		arr := r.jsObjectMakeArray(r.ctx, uint64(len(jsArgs)), jsArgs, uintptr(unsafe.Pointer(&exc)))
		if exc != 0 {
			msg := r.jsValueToGoString(exc)
			return 0, fmt.Errorf("ramune: JSObjectMakeArray: %s", msg)
		}
		return arr, nil
	default:
		rv := reflect.ValueOf(v)
		origVal := rv

		// Check for Promise type (has Await() method) → convert to JS Promise
		if rv.Kind() == reflect.Ptr && rv.MethodByName("Await").IsValid() {
			return r.promiseToJS(rv)
		}

		// Handle arbitrary slices via reflection (e.g. []int, []string)
		if rv.Kind() == reflect.Slice {
			jsArgs := make([]uintptr, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				jsVal, err := r.goToJS(rv.Index(i).Interface())
				if err != nil {
					return 0, fmt.Errorf("ramune: slice index %d: %w", i, err)
				}
				jsArgs[i] = jsVal
			}
			var exc uintptr
			arr := r.jsObjectMakeArray(r.ctx, uint64(len(jsArgs)), jsArgs, uintptr(unsafe.Pointer(&exc)))
			if exc != 0 {
				msg := r.jsValueToGoString(exc)
				return 0, fmt.Errorf("ramune: JSObjectMakeArray: %s", msg)
			}
			return arr, nil
		}

		// Handle arbitrary maps with string keys via reflection (e.g. map[string]int)
		if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
			obj := r.jsObjectMake(r.ctx, 0, 0)
			if obj == 0 {
				return 0, fmt.Errorf("ramune: JSObjectMake returned NULL")
			}
			iter := rv.MapRange()
			for iter.Next() {
				jsVal, err := r.goToJS(iter.Value().Interface())
				if err != nil {
					return 0, fmt.Errorf("ramune: map key %q: %w", iter.Key().String(), err)
				}
				jsKey := r.jsStringCreateWithUTF8CString(iter.Key().String())
				r.jsObjectSetProperty(r.ctx, obj, jsKey, jsVal, 0, 0)
				r.jsStringRelease(jsKey)
			}
			return obj, nil
		}

		if rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			return r.structToJSObject(origVal, rv)
		}
		return 0, fmt.Errorf("ramune: unsupported Go type %T", v)
	}
}

// promiseToJS converts a Go *promise.Promise[T] to a JS Promise.
// It spawns a goroutine that calls Await(), then resolves/rejects the JS Promise.
func (r *Runtime) promiseToJS(rv reflect.Value) (uintptr, error) {
	r.nativeMethodSeq++
	seq := r.nativeMethodSeq
	resolveName := fmt.Sprintf("__promise_resolve_%d", seq)
	rejectName := fmt.Sprintf("__promise_reject_%d", seq)

	// Create JS Promise that stores resolve/reject as globals
	jsCode := fmt.Sprintf(
		`new Promise(function(resolve, reject) {
			globalThis.%s = function(v) { resolve(v); };
			globalThis.%s = function(e) { reject(e); };
		})`,
		resolveName, rejectName,
	)
	jsPromise, err := r.evalScriptLocked(jsCode, "promise-bridge")
	if err != nil {
		return 0, fmt.Errorf("promiseToJS: %w", err)
	}

	awaitMethod := rv.MethodByName("Await")
	go func() {
		results := awaitMethod.Call(nil)
		// results[0] = value, results[1] = error
		r.dispatch(func() {
			if len(results) >= 2 && !results[1].IsNil() {
				errMsg := results[1].Interface().(error).Error()
				r.execLocked(fmt.Sprintf("globalThis.%s(%q);delete globalThis.%s;delete globalThis.%s;",
					rejectName, errMsg, resolveName, rejectName))
			} else if len(results) >= 1 {
				val := results[0].Interface()
				// Convert value to JSON for passing to JS
				jsVal, convErr := r.goToJS(val)
				if convErr != nil {
					r.execLocked(fmt.Sprintf("globalThis.%s(%q);delete globalThis.%s;delete globalThis.%s;",
						rejectName, convErr.Error(), resolveName, rejectName))
					return
				}
				// Store value in temp, resolve, cleanup
				tmpName := fmt.Sprintf("__pval_%d", seq)
				jsKey := r.jsStringCreateWithUTF8CString(tmpName)
				globalObj := r.jsContextGetGlobalObject(r.ctx)
				r.jsObjectSetProperty(r.ctx, globalObj, jsKey, jsVal, 0, 0)
				r.jsStringRelease(jsKey)
				r.execLocked(fmt.Sprintf("globalThis.%s(globalThis.%s);delete globalThis.%s;delete globalThis.%s;delete globalThis.%s;",
					resolveName, tmpName, resolveName, rejectName, tmpName))
			}
		})
		r.Wake()
	}()

	return jsPromise, nil
}

// structToJSObject converts a Go struct to a JS object with live getter/setter
// properties and callable methods. Uses per-type callback registration to avoid
// leaking GoFuncs for every instance.
func (r *Runtime) structToJSObject(origVal, rv reflect.Value) (uintptr, error) {
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
		return 0, fmt.Errorf("structToJSObject: %w", err)
	}
	return jsObj, nil
}

// jsToGo converts a JSValueRef to a Go value.
// Must be called with rt.mu held.
func (r *Runtime) jsToGo(ptr uintptr) (any, error) {
	if ptr == 0 {
		return nil, nil
	}

	typ := r.jsValueGetType(r.ctx, ptr)
	switch typ {
	case jsTypeUndefined, jsTypeNull:
		return nil, nil
	case jsTypeBoolean:
		return r.jsValueToBoolean(r.ctx, ptr), nil
	case jsTypeNumber:
		return r.jsValueToNumber(r.ctx, ptr, 0), nil
	case jsTypeString:
		return r.jsValueToGoString(ptr), nil
	default:
		// Object, Symbol, etc. — return as *Value.
		return r.newValue(ptr), nil
	}
}
