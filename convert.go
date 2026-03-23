package ramune

import (
	"fmt"
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
		return 0, fmt.Errorf("ramune: unsupported Go type %T", v)
	}
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
