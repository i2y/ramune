//go:build !quickjs && !goja && !qjswasm

package ramune

import (
	"encoding/json"
	"unsafe"

	"github.com/ebitengine/purego"
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

// registerFuncLocked registers a Go function. Must be called with rt.mu held.
// Uses a single purego callback (dispatcher) shared across all registered
// functions, avoiding the 2048 callback limit.
func (r *Runtime) registerFuncLocked(name string, fn GoFunc) error {
	if err := r.ensureDispatcher(); err != nil {
		return err
	}

	// Store function by ID.
	id := len(r.goFuncs)
	r.goFuncs = append(r.goFuncs, fn)

	// Create JS wrapper: globalThis.name = function() { return __dispatch(id, arguments); }
	jsName := r.jsStringCreateWithUTF8CString(name)
	defer r.jsStringRelease(jsName)

	// Use Eval to create a function that calls __dispatch with the ID.
	code := r.jsStringCreateWithUTF8CString(
		`(function(id) { return function() { return __dispatch(id, Array.prototype.slice.call(arguments)); }; })(` +
			itoa(id) + `)`)
	defer r.jsStringRelease(code)

	var exc uintptr
	fnObj := r.jsEvaluateScript(r.ctx, code, 0, 0, 0, uintptr(unsafe.Pointer(&exc)))
	if fnObj == 0 {
		return &JSError{Context: "RegisterFunc", Message: "failed to create wrapper"}
	}

	global := r.jsContextGetGlobalObject(r.ctx)
	r.jsObjectSetProperty(r.ctx, global, jsName, fnObj, 0, 0)

	return nil
}

// ensureDispatcher creates the single __dispatch callback (once per Runtime).
func (r *Runtime) ensureDispatcher() error {
	if r.dispatcherReady {
		return nil
	}

	// Create ONE purego callback for the entire Runtime.
	// Serialize via puregoMu — purego.NewCallback uses global state.
	puregoMu.Lock()
	cb := purego.NewCallback(func(ctx, function, thisObject uintptr, argc uint64, argv, exc uintptr) uintptr {
		// First argument is the function ID, rest are actual arguments.
		if argc < 1 {
			return r.jsValueMakeUndefined(r.ctx)
		}

		// Get function ID from first argument.
		idPtr := *(*uintptr)(unsafe.Pointer(argv))
		id := int(r.jsValueToNumber(r.ctx, idPtr, 0))

		if id < 0 || id >= len(r.goFuncs) {
			return r.jsValueMakeUndefined(r.ctx)
		}

		// Get actual arguments (second argument is the args array).
		var goArgs []any
		if argc >= 2 {
			argsPtr := *(*uintptr)(unsafe.Pointer(argv + unsafe.Sizeof(uintptr(0))))
			goArgs = r.jsArrayToGoSlice(argsPtr)
		}

		// Call the Go function.
		result, err := r.goFuncs[id](goArgs)
		if err != nil {
			if exc != 0 {
				errStr := r.jsStringCreateWithUTF8CString(err.Error())
				errVal := r.jsValueMakeString(r.ctx, errStr)
				r.jsStringRelease(errStr)
				*(*uintptr)(unsafe.Pointer(exc)) = errVal
			}
			return r.jsValueMakeUndefined(r.ctx)
		}

		jsVal, convErr := r.goToJS(result)
		if convErr != nil {
			return r.jsValueMakeUndefined(r.ctx)
		}
		return jsVal
	})

	puregoMu.Unlock()

	r.callbacks = append(r.callbacks, cb)

	// Register __dispatch as a global JSC function.
	dispatchName := r.jsStringCreateWithUTF8CString("__dispatch")
	defer r.jsStringRelease(dispatchName)

	fnObj := r.jsObjectMakeFunctionWithCallback(r.ctx, dispatchName, cb)
	global := r.jsContextGetGlobalObject(r.ctx)
	r.jsObjectSetProperty(r.ctx, global, dispatchName, fnObj, 0, 0)

	r.dispatcherReady = true
	return nil
}

// jsArrayToGoSlice converts a JS array to []any for callback arguments.
func (r *Runtime) jsArrayToGoSlice(arrPtr uintptr) []any {
	if arrPtr == 0 {
		return nil
	}

	// Get array length.
	lengthName := r.jsStringCreateWithUTF8CString("length")
	defer r.jsStringRelease(lengthName)

	obj := r.jsValueToObject(r.ctx, arrPtr, 0)
	if obj == 0 {
		return nil
	}

	lengthVal := r.jsObjectGetProperty(r.ctx, obj, lengthName, 0)
	if lengthVal == 0 {
		return nil
	}
	length := int(r.jsValueToNumber(r.ctx, lengthVal, 0))

	result := make([]any, length)
	for i := 0; i < length; i++ {
		elem := r.jsObjectGetPropertyAtIndex(r.ctx, obj, uint32(i), 0)
		result[i] = r.jsToGoCallback(elem)
	}
	return result
}

// itoa converts int to string without importing strconv.
// jsToGoCallback converts a JSValueRef to a Go value without returning *Value.
// This avoids mutex deadlock since callbacks run with the mutex already held.
// Object arguments are converted via JSON.stringify to map[string]any or []any.
func (r *Runtime) jsToGoCallback(ptr uintptr) any {
	if ptr == 0 {
		return nil
	}
	typ := r.jsValueGetType(r.ctx, ptr)
	switch typ {
	case jsTypeUndefined, jsTypeNull:
		return nil
	case jsTypeBoolean:
		return r.jsValueToBoolean(r.ctx, ptr)
	case jsTypeNumber:
		return r.jsValueToNumber(r.ctx, ptr, 0)
	case jsTypeString:
		return r.jsValueToGoString(ptr)
	default:
		// Function: wrap as *JSFunc for callable access from Go.
		obj := r.jsValueToObject(r.ctx, ptr, 0)
		if obj != 0 && r.jsObjectIsFunction(r.ctx, obj) {
			return r.newJSFunc(obj)
		}
		// Object: convert via JSON.stringify → json.Unmarshal.
		return r.jsObjectToGo(ptr)
	}
}

// ensureJSONStringify caches the JSON.stringify function reference.
// Must be called with rt.mu held.
func (r *Runtime) ensureJSONStringify() {
	if r.jsonStringifyFn != 0 {
		return
	}
	global := r.jsContextGetGlobalObject(r.ctx)
	jsonName := r.jsStringCreateWithUTF8CString("JSON")
	defer r.jsStringRelease(jsonName)
	jsonObj := r.jsObjectGetProperty(r.ctx, global, jsonName, 0)

	strName := r.jsStringCreateWithUTF8CString("stringify")
	defer r.jsStringRelease(strName)
	fn := r.jsObjectGetProperty(r.ctx, jsonObj, strName, 0)

	r.jsValueProtect(r.ctx, fn)
	r.jsonStringifyFn = fn
}

// jsObjectToGo converts a JS object to map[string]any or []any via JSON.stringify.
// Falls back to toString on failure. Must be called with rt.mu held.
func (r *Runtime) jsObjectToGo(ptr uintptr) any {
	r.ensureJSONStringify()

	fnObj := r.jsValueToObject(r.ctx, r.jsonStringifyFn, 0)
	if fnObj == 0 {
		return r.jsValueToGoString(ptr)
	}

	args := []uintptr{ptr}
	result := r.jsObjectCallAsFunction(r.ctx, fnObj, 0, 1, args, 0)
	if result == 0 {
		return r.jsValueToGoString(ptr)
	}

	jsonStr := r.jsValueToGoString(result)
	var v any
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return r.jsValueToGoString(ptr)
	}
	return v
}
