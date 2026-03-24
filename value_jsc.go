//go:build !quickjs

package ramune

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"
)

// Value wraps a JavaScriptCore JSValueRef with lifecycle management.
// Call Close() to unprotect the value.
type Value struct {
	noCopy noCopy
	ptr    uintptr
	rt     *Runtime
	closed atomic.Bool
}

// newValue creates a Value that protects the given JSValueRef from GC.
// Returns nil if ptr is 0.
// No Go finalizer is set — Values must be explicitly Close()d or they
// are unprotected when the Runtime is closed. This avoids SIGTRAP crashes
// caused by Go's concurrent GC running finalizers that touch JSC internals.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) newValue(ptr uintptr) *Value {
	if ptr == 0 {
		return nil
	}
	r.jsValueProtect(r.ctx, ptr)
	v := &Value{ptr: ptr, rt: r}
	// Track for cleanup on Runtime.Close() if not explicitly closed.
	r.unprotectMu.Lock()
	r.protectedPtrs = append(r.protectedPtrs, ptr)
	r.unprotectMu.Unlock()
	return v
}

// Close queues the JSValueRef for unprotection at the next safe point.
// Uses the unprotectQueue to avoid deadlock when called from GoFunc callbacks
// (which already run on the dedicated JSC goroutine).
// Safe to call multiple times or on nil.
func (v *Value) Close() error {
	if v == nil || v.closed.Swap(true) {
		return nil
	}
	if v.rt.closed.Load() {
		v.ptr = 0
		return nil
	}
	// Queue for unprotection rather than dispatching directly.
	// This avoids deadlock if Close() is called from within a callback.
	v.rt.unprotectMu.Lock()
	v.rt.unprotectQueue = append(v.rt.unprotectQueue, v.ptr)
	v.rt.unprotectMu.Unlock()
	v.ptr = 0
	return nil
}

// Ptr returns the raw JSValueRef pointer.
func (v *Value) Ptr() uintptr {
	if v == nil {
		return 0
	}
	return v.ptr
}

// String returns the JavaScript string representation.
func (v *Value) String() string {
	if v == nil || v.ptr == 0 {
		return "<nil>"
	}
	var s string
	v.rt.dispatch(func() {
		s = v.rt.jsValueToGoString(v.ptr)
	})
	return s
}

// Float64 returns the value as a float64.
func (v *Value) Float64() (float64, error) {
	if v == nil || v.ptr == 0 {
		return 0, ErrNilValue
	}
	var n float64
	v.rt.dispatch(func() {
		n = v.rt.jsValueToNumber(v.rt.ctx, v.ptr, 0)
	})
	runtime.KeepAlive(v)
	return n, nil
}

// Int64 returns the value as an int64 (truncated from float64, since JS has no integer type).
func (v *Value) Int64() (int64, error) {
	f, err := v.Float64()
	if err != nil {
		return 0, err
	}
	return int64(f), nil
}

// Bool returns the value as a bool.
func (v *Value) Bool() (bool, error) {
	if v == nil || v.ptr == 0 {
		return false, ErrNilValue
	}
	var b bool
	v.rt.dispatch(func() {
		b = v.rt.jsValueToBoolean(v.rt.ctx, v.ptr)
	})
	runtime.KeepAlive(v)
	return b, nil
}

// Bytes returns the contents of a TypedArray or ArrayBuffer as a Go byte slice.
func (v *Value) Bytes() ([]byte, error) {
	if v == nil || v.ptr == 0 {
		return nil, ErrNilValue
	}
	var result []byte
	var err error
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			err = &JSError{Context: "Bytes", Message: "value is not an object"}
			return
		}

		// Try TypedArray first.
		if v.rt.jsObjectGetTypedArrayBytesPtr != nil {
			var exc uintptr
			bytesPtr := v.rt.jsObjectGetTypedArrayBytesPtr(v.rt.ctx, obj, uintptr(unsafe.Pointer(&exc)))
			if bytesPtr != 0 && exc == 0 {
				byteLen := v.rt.jsObjectGetTypedArrayByteLength(v.rt.ctx, obj, 0)
				if byteLen == 0 {
					result = []byte{}
					return
				}
				src := unsafe.Slice((*byte)(unsafe.Pointer(bytesPtr)), byteLen)
				result = make([]byte, byteLen)
				copy(result, src)
				return
			}
		}

		// Try ArrayBuffer.
		if v.rt.jsObjectGetArrayBufferBytesPtr != nil {
			var exc uintptr
			bytesPtr := v.rt.jsObjectGetArrayBufferBytesPtr(v.rt.ctx, obj, uintptr(unsafe.Pointer(&exc)))
			if bytesPtr != 0 && exc == 0 {
				byteLen := v.rt.jsObjectGetArrayBufferByteLength(v.rt.ctx, obj, 0)
				if byteLen == 0 {
					result = []byte{}
					return
				}
				src := unsafe.Slice((*byte)(unsafe.Pointer(bytesPtr)), byteLen)
				result = make([]byte, byteLen)
				copy(result, src)
				return
			}
		}

		err = &JSError{Context: "Bytes", Message: "value is not a TypedArray or ArrayBuffer"}
	})
	return result, err
}

// GoString returns the JavaScript string value as a Go string.
func (v *Value) GoString() (string, error) {
	if v == nil || v.ptr == 0 {
		return "", ErrNilValue
	}
	var s string
	v.rt.dispatch(func() {
		s = v.rt.jsValueToGoString(v.ptr)
	})
	return s, nil
}

// IsNull reports whether this value is JavaScript null.
func (v *Value) IsNull() bool {
	if v == nil || v.ptr == 0 {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		result = v.rt.jsValueIsNull(v.rt.ctx, v.ptr)
	})
	return result
}

// IsUndefined reports whether this value is JavaScript undefined.
func (v *Value) IsUndefined() bool {
	if v == nil || v.ptr == 0 {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		result = v.rt.jsValueIsUndefined(v.rt.ctx, v.ptr)
	})
	return result
}

// IsArray reports whether this value is a JavaScript array.
func (v *Value) IsArray() bool {
	if v == nil || v.ptr == 0 {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		result = v.rt.jsValueIsArray(v.rt.ctx, v.ptr)
	})
	return result
}

// IsFunction reports whether this value is a JavaScript function.
func (v *Value) IsFunction() bool {
	if v == nil || v.ptr == 0 {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			return
		}
		result = v.rt.jsObjectIsFunction(v.rt.ctx, obj)
	})
	return result
}

// Keys returns all enumerable property names of the JS object.
func (v *Value) Keys() ([]string, error) {
	if v == nil || v.ptr == 0 {
		return nil, ErrNilValue
	}
	var result []string
	var err error
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			err = &JSError{Context: "Keys", Message: "value is not an object"}
			return
		}

		names := v.rt.jsObjectCopyPropertyNames(v.rt.ctx, obj)
		if names == 0 {
			return
		}
		defer v.rt.jsPropertyNameArrayRelease(names)

		count := v.rt.jsPropertyNameArrayGetCount(names)
		result = make([]string, count)
		for i := uint64(0); i < count; i++ {
			jsStr := v.rt.jsPropertyNameArrayGetNameAtIndex(names, i)
			result[i] = v.rt.jsStringToGo(jsStr)
		}
	})
	return result, err
}

// Len returns the "length" property as an integer.
// Works for arrays, strings, and any object with a length property.
func (v *Value) Len() (int, error) {
	if v == nil || v.ptr == 0 {
		return 0, ErrNilValue
	}
	var n int
	var err error
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			err = &JSError{Context: "Len", Message: "value is not an object"}
			return
		}

		jsName := v.rt.jsStringCreateWithUTF8CString("length")
		defer v.rt.jsStringRelease(jsName)
		prop := v.rt.jsObjectGetProperty(v.rt.ctx, obj, jsName, 0)
		if prop == 0 || v.rt.jsValueIsUndefined(v.rt.ctx, prop) {
			return
		}
		n = int(v.rt.jsValueToNumber(v.rt.ctx, prop, 0))
	})
	return n, err
}

// Has reports whether the JS object has the named property.
func (v *Value) Has(name string) bool {
	if v == nil || v.ptr == 0 {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			return
		}
		jsName := v.rt.jsStringCreateWithUTF8CString(name)
		defer v.rt.jsStringRelease(jsName)
		prop := v.rt.jsObjectGetProperty(v.rt.ctx, obj, jsName, 0)
		result = prop != 0 && !v.rt.jsValueIsUndefined(v.rt.ctx, prop)
	})
	return result
}

// Delete removes a property from the JS object.
func (v *Value) Delete(name string) error {
	if v == nil || v.ptr == 0 {
		return ErrNilValue
	}
	var err error
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			err = &JSError{Context: "Delete", Message: "value is not an object"}
			return
		}
		jsName := v.rt.jsStringCreateWithUTF8CString(name)
		defer v.rt.jsStringRelease(jsName)
		v.rt.jsObjectDeleteProperty(v.rt.ctx, obj, jsName, 0)
	})
	return err
}

// Index returns the value at the given array index.
func (v *Value) Index(i int) *Value {
	if v == nil || v.ptr == 0 {
		return nil
	}
	var result *Value
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			return
		}
		prop := v.rt.jsObjectGetPropertyAtIndex(v.rt.ctx, obj, uint32(i), 0)
		if prop == 0 || v.rt.jsValueIsUndefined(v.rt.ctx, prop) {
			return
		}
		result = v.rt.newValue(prop)
	})
	return result
}

// ToMap converts a JS object to a Go map via JSON serialization.
// Returns an error if the value is not a JSON-serializable object.
func (v *Value) ToMap() (map[string]any, error) {
	if v == nil || v.ptr == 0 {
		return nil, ErrNilValue
	}
	var m map[string]any
	var err error
	v.rt.dispatch(func() {
		result := v.rt.jsObjectToGo(v.ptr)
		switch r := result.(type) {
		case map[string]any:
			m = r
		default:
			err = &JSError{Context: "ToMap", Message: "value is not a JSON-serializable object"}
		}
	})
	return m, err
}

// ToSlice converts a JS array to a Go slice via JSON serialization.
func (v *Value) ToSlice() ([]any, error) {
	if v == nil || v.ptr == 0 {
		return nil, ErrNilValue
	}
	var s []any
	var err error
	v.rt.dispatch(func() {
		result := v.rt.jsObjectToGo(v.ptr)
		switch r := result.(type) {
		case []any:
			s = r
		default:
			err = &JSError{Context: "ToSlice", Message: "value is not a JSON-serializable array"}
		}
	})
	return s, err
}

// Attr returns a property by name from the JS object.
// Returns nil if this is not an object or the property doesn't exist.
func (v *Value) Attr(name string) *Value {
	if v == nil || v.ptr == 0 {
		return nil
	}
	var result *Value
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			return
		}
		jsName := v.rt.jsStringCreateWithUTF8CString(name)
		defer v.rt.jsStringRelease(jsName)
		prop := v.rt.jsObjectGetProperty(v.rt.ctx, obj, jsName, 0)
		if prop == 0 || v.rt.jsValueIsUndefined(v.rt.ctx, prop) {
			return
		}
		result = v.rt.newValue(prop)
	})
	return result
}

// SetAttr sets a property on the JS object.
func (v *Value) SetAttr(name string, val any) error {
	if v == nil || v.ptr == 0 {
		return ErrNilValue
	}
	var err error
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			err = &JSError{Context: "SetAttr", Message: "value is not an object"}
			return
		}
		jsVal, e := v.rt.goToJS(val)
		if e != nil {
			err = fmt.Errorf("ramune: SetAttr(%q): %w", name, e)
			return
		}
		jsName := v.rt.jsStringCreateWithUTF8CString(name)
		defer v.rt.jsStringRelease(jsName)
		v.rt.jsObjectSetProperty(v.rt.ctx, obj, jsName, jsVal, 0, 0)
	})
	return err
}

// AttrErr returns a property by name or an error.
func (v *Value) AttrErr(name string) (*Value, error) {
	if v == nil || v.ptr == 0 {
		return nil, ErrNilValue
	}
	result := v.Attr(name)
	if result == nil {
		return nil, &JSError{
			Context: fmt.Sprintf("Attr(%q)", name),
			Message: "property not found or value is not an object",
		}
	}
	return result, nil
}

// Call calls this value as a function with the given arguments.
func (v *Value) Call(args ...any) (*Value, error) {
	if v == nil || v.ptr == 0 {
		return nil, ErrNilValue
	}
	var val *Value
	var err error
	v.rt.dispatch(func() {
		obj := v.rt.jsValueToObject(v.rt.ctx, v.ptr, 0)
		runtime.KeepAlive(v)
		if obj == 0 {
			err = &JSError{Context: "Call", Message: "value is not callable"}
			return
		}

		jsArgs := make([]uintptr, len(args))
		for i, arg := range args {
			jsVal, e := v.rt.goToJS(arg)
			if e != nil {
				err = fmt.Errorf("ramune: Call arg %d: %w", i, e)
				return
			}
			jsArgs[i] = jsVal
		}

		var argsPtr []uintptr
		if len(jsArgs) > 0 {
			argsPtr = jsArgs
		}

		var exc uintptr
		result := v.rt.jsObjectCallAsFunction(v.rt.ctx, obj, 0, uint64(len(jsArgs)), argsPtr, uintptr(unsafe.Pointer(&exc)))
		if result == 0 {
			if exc != 0 {
				msg, stack := v.rt.getExceptionInfo(exc)
				err = &JSError{Context: "Call", Message: msg, Stack: stack}
				return
			}
			err = &JSError{Context: "Call", Message: "JavaScript exception occurred"}
			return
		}

		val = v.rt.newValue(result)
	})
	return val, err
}

// jsValueToGoString converts a JSValueRef to a Go string.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) jsValueToGoString(ptr uintptr) string {
	jsStr := r.jsValueToStringCopy(r.ctx, ptr, 0)
	if jsStr == 0 {
		return "<error>"
	}
	defer r.jsStringRelease(jsStr)
	return r.jsStringToGo(jsStr)
}

// jsStringToGo converts a JSStringRef to a Go string.
func (r *Runtime) jsStringToGo(jsStr uintptr) string {
	size := r.jsStringGetMaximumUTF8CStringSize(jsStr)
	if size == 0 {
		return ""
	}
	buf := make([]byte, size)
	written := r.jsStringGetUTF8CString(jsStr, buf, size)
	if written > 0 {
		return string(buf[:written-1]) // exclude null terminator
	}
	return ""
}
