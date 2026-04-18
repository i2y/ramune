//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// Value wraps a QuickJS JSValue (NaN-boxed uint64) living inside the wasm
// module. Multiple Go references to the same JSValue are allowed — we use
// the shim's val_dup to keep refcounts correct when a Value is cloned by
// the Go side. M1 Close() is a deliberate no-op; the VM releases everything
// on Runtime.Close().
type Value struct {
	noCopy noCopy
	handle uint64 // NaN-boxed JSValue
	rt     *Runtime
	closed atomic.Bool
}

func (r *Runtime) wrapValue(h uint64) *Value {
	return &Value{handle: h, rt: r}
}

// Close marks the Value as released. Actual val_free is deferred to
// Runtime.Close(); see the engine_quickjs.go comment explaining why
// explicit frees race with async promise callbacks.
func (v *Value) Close() error {
	if v == nil {
		return nil
	}
	v.closed.Swap(true)
	return nil
}

// Ptr returns an opaque handle (the NaN-boxed JSValue) so external code can
// key on per-value identity.
func (v *Value) Ptr() uintptr {
	if v == nil {
		return 0
	}
	return uintptr(v.handle)
}

func (v *Value) preflight() error {
	if v == nil || v.rt == nil {
		return ErrNilValue
	}
	if v.rt.closed.Load() {
		return ErrAlreadyClosed
	}
	return nil
}

// -----------------------------------------------------------------------
// Primitive accessors
// -----------------------------------------------------------------------

// String returns the JS value's string form (JS_ToString), or empty string
// on conversion error. Use GoString for error-returning variant.
func (v *Value) String() string {
	s, _ := v.GoString()
	return s
}

// GoString returns the JS value's string form with error propagation.
func (v *Value) GoString() (string, error) {
	if err := v.preflight(); err != nil {
		return "", err
	}
	var out string
	var err error
	v.rt.dispatch(func() {
		s, e := v.rt.valToStringLocked(v.handle)
		out = s
		err = e
	})
	return out, err
}

// Float64 returns the value as float64.
func (v *Value) Float64() (float64, error) {
	if err := v.preflight(); err != nil {
		return 0, err
	}
	var out float64
	v.rt.dispatch(func() {
		res, e := v.rt.wzExp.valToFloat64.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle)
		if e == nil {
			// wazero encodes f64 results as bit pattern in the result slot.
			out = api64ToFloat(res[0])
		}
	})
	return out, nil
}

// Int64 returns the value as int64.
func (v *Value) Int64() (int64, error) {
	if err := v.preflight(); err != nil {
		return 0, err
	}
	var out int64
	v.rt.dispatch(func() {
		res, _ := v.rt.wzExp.valToInt64.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle)
		out = int64(res[0])
	})
	return out, nil
}

// Bool returns the value as bool.
func (v *Value) Bool() (bool, error) {
	if err := v.preflight(); err != nil {
		return false, err
	}
	var out bool
	v.rt.dispatch(func() {
		res, _ := v.rt.wzExp.valToBool.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle)
		out = int32(res[0]) != 0
	})
	return out, nil
}

// Bytes returns the underlying bytes for a Uint8Array or ArrayBuffer-like
// value. M1: JSON fallback path; M6 will add a direct buffer export.
func (v *Value) Bytes() ([]byte, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	// For now, go through JSON: Array.from(u8) -> [n1,n2,...] -> []byte.
	var out []byte
	var retErr error
	v.rt.dispatch(func() {
		coerce, err := v.rt.evalLocked("(function(v){return Array.from(v);})")
		if err != nil {
			retErr = err
			return
		}
		defer coerce.Close()
		arr, err := v.rt.callFunctionLocked(coerce.handle, 0, []uint64{v.handle})
		if err != nil {
			retErr = err
			return
		}
		defer v.rt.freeValueLocked(arr)
		j, err := v.rt.valToJSONLocked(arr)
		if err != nil {
			retErr = err
			return
		}
		var nums []int
		if e := json.Unmarshal([]byte(j), &nums); e != nil {
			retErr = e
			return
		}
		out = make([]byte, len(nums))
		for i, n := range nums {
			out[i] = byte(n)
		}
	})
	return out, retErr
}

// -----------------------------------------------------------------------
// Type checks (cheap: single val_kind call)
// -----------------------------------------------------------------------

func (v *Value) kind() uint32 {
	if err := v.preflight(); err != nil {
		return 0
	}
	var out uint32
	v.rt.dispatch(func() {
		res, _ := v.rt.wzExp.valKind.Call(v.rt.wzCtx, uint64(v.rt.qjsCtx), v.handle)
		out = uint32(res[0])
	})
	return out
}

func (v *Value) IsUndefined() bool { return v.kind()&valKindUndefined != 0 }
func (v *Value) IsNull() bool      { return v.kind()&valKindNull != 0 }
func (v *Value) IsBool() bool      { return v.kind()&valKindBool != 0 }
func (v *Value) IsNumber() bool    { return v.kind()&valKindNumber != 0 }
func (v *Value) IsString() bool    { return v.kind()&valKindString != 0 }
func (v *Value) IsObject() bool    { return v.kind()&valKindObject != 0 }
func (v *Value) IsArray() bool     { return v.kind()&valKindArray != 0 }
func (v *Value) IsFunction() bool  { return v.kind()&valKindFunction != 0 }
func (v *Value) IsPromise() bool   { return v.kind()&valKindPromise != 0 }
func (v *Value) IsException() bool { return v.kind()&valKindException != 0 }

// -----------------------------------------------------------------------
// Property / array ops — M1-scope stubs
// -----------------------------------------------------------------------

// Attr reads a property. Returns a new Value that must be Closed.
func (v *Value) Attr(name string) *Value {
	val, _ := v.AttrErr(name)
	return val
}

func (v *Value) AttrErr(name string) (*Value, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out *Value
	var err error
	v.rt.dispatch(func() {
		ptr, length, e := v.rt.writeStringLocked(name)
		if e != nil {
			err = e
			return
		}
		defer v.rt.wasmFreeLocked(ptr)
		res, e := v.rt.wzExp.objGetProp.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle, uint64(ptr), uint64(length))
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(res[0]) {
			err = v.rt.pullExceptionLocked()
			return
		}
		out = v.rt.wrapValue(res[0])
	})
	return out, err
}

func (v *Value) SetAttr(name string, value any) error {
	if err := v.preflight(); err != nil {
		return err
	}
	var err error
	v.rt.dispatch(func() {
		val, e := v.rt.goToJSLocked(value)
		if e != nil {
			err = e
			return
		}
		ptr, length, e := v.rt.writeStringLocked(name)
		if e != nil {
			err = e
			return
		}
		defer v.rt.wasmFreeLocked(ptr)
		res, e := v.rt.wzExp.objSetProp.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle,
			uint64(ptr), uint64(length), val)
		if e != nil {
			err = e
			return
		}
		if int32(res[0]) < 0 {
			err = v.rt.pullExceptionLocked()
		}
	})
	return err
}

// Keys returns the property names of an object (via JSON fallback for M1).
func (v *Value) Keys() ([]string, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out []string
	var err error
	v.rt.dispatch(func() {
		fnH, e := v.rt.rawEvalLocked("(function(o){return Object.keys(o);})",
			"<keys>", 0)
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(fnH) {
			err = v.rt.pullExceptionLocked()
			return
		}
		defer v.rt.freeValueLocked(fnH)
		arrH, e := v.rt.callFunctionLocked(fnH, 0, []uint64{v.handle})
		if e != nil {
			err = e
			return
		}
		defer v.rt.freeValueLocked(arrH)
		j, e := v.rt.valToJSONLocked(arrH)
		if e != nil {
			err = e
			return
		}
		err = json.Unmarshal([]byte(j), &out)
	})
	return out, err
}

// Len returns the length of an array (or string).
func (v *Value) Len() (int, error) {
	if err := v.preflight(); err != nil {
		return 0, err
	}
	attr, err := v.AttrErr("length")
	if err != nil {
		return 0, err
	}
	defer attr.Close()
	n, err := attr.Float64()
	return int(n), err
}

// Index returns the array element at i.
func (v *Value) Index(i int) *Value {
	if err := v.preflight(); err != nil {
		return nil
	}
	var out *Value
	v.rt.dispatch(func() {
		val, err := v.AttrErr(fmt.Sprint(i))
		if err == nil {
			out = val
		}
	})
	return out
}

// Has / Delete stubs for M1 - will be filled in M6.
func (v *Value) Has(name string) bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() {
		ptr, length, e := v.rt.writeStringLocked(name)
		if e != nil {
			return
		}
		defer v.rt.wasmFreeLocked(ptr)
		res, e := v.rt.wzExp.objHasProp.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle, uint64(ptr), uint64(length))
		if e == nil && int32(res[0]) != 0 {
			out = true
		}
	})
	return out
}

func (v *Value) Delete(name string) error {
	if err := v.preflight(); err != nil {
		return err
	}
	var err error
	v.rt.dispatch(func() {
		ptr, length, e := v.rt.writeStringLocked(name)
		if e != nil {
			err = e
			return
		}
		defer v.rt.wasmFreeLocked(ptr)
		_, err = v.rt.wzExp.objDeleteProp.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle, uint64(ptr), uint64(length))
	})
	return err
}

// Call invokes a JS function value with args and returns the result as
// *Value. Caller must Close() the result.
func (v *Value) Call(args ...any) (*Value, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out *Value
	var err error
	v.rt.dispatch(func() {
		argsJSON, e := json.Marshal(args)
		if e != nil {
			err = e
			return
		}
		argPtr, argLen, e := v.rt.writeStringLocked(string(argsJSON))
		if e != nil {
			err = e
			return
		}
		defer v.rt.wasmFreeLocked(argPtr)
		res, e := v.rt.wzExp.valCall.Call(v.rt.wzCtx,
			uint64(v.rt.qjsCtx), v.handle, 0,
			uint64(argPtr), uint64(argLen))
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(res[0]) {
			err = v.rt.pullExceptionLocked()
			return
		}
		out = v.rt.wrapValue(res[0])
	})
	return out, err
}

// ToMap / ToSlice dump the value through JSON; same as QuickJS backend.
func (v *Value) ToMap() (map[string]any, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out map[string]any
	var err error
	v.rt.dispatch(func() {
		j, e := v.rt.valToJSONLocked(v.handle)
		if e != nil {
			err = e
			return
		}
		err = json.Unmarshal([]byte(j), &out)
	})
	return out, err
}

func (v *Value) ToSlice() ([]any, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out []any
	var err error
	v.rt.dispatch(func() {
		j, e := v.rt.valToJSONLocked(v.handle)
		if e != nil {
			err = e
			return
		}
		err = json.Unmarshal([]byte(j), &out)
	})
	return out, err
}

// -----------------------------------------------------------------------
// Internal helpers (engine goroutine only)
// -----------------------------------------------------------------------

func (r *Runtime) valToStringLocked(h uint64) (string, error) {
	res, err := r.wzExp.valToString.Call(r.wzCtx, uint64(r.qjsCtx), h)
	if err != nil {
		return "", err
	}
	ptr, length := unpackPtrLen(res[0])
	if ptr == 0 {
		return "", nil
	}
	defer r.wasmFreeLocked(ptr)
	return r.readStringLocked(ptr, length)
}

func (r *Runtime) valToJSONLocked(h uint64) (string, error) {
	res, err := r.wzExp.valToJSON.Call(r.wzCtx, uint64(r.qjsCtx), h)
	if err != nil {
		return "", err
	}
	ptr, length := unpackPtrLen(res[0])
	if ptr == 0 {
		return "", fmt.Errorf("ramune: val_to_json returned 0")
	}
	defer r.wasmFreeLocked(ptr)
	return r.readStringLocked(ptr, length)
}

func (r *Runtime) callFunctionLocked(fn, this uint64, args []uint64) (uint64, error) {
	argsJSON, err := json.Marshal(argsAsAny(args, r))
	if err != nil {
		return 0, err
	}
	// The shim expects a JSON array of VALUES, not handles. For our
	// internal call paths we already have handles — convert each via
	// val_to_json then build a JSON array.
	// TODO(M3): bypass JSON by exposing a handle-array call export.
	jsonArr, err := r.buildArgsJSONFromHandles(args)
	_ = argsJSON // retained for when we switch encoding
	if err != nil {
		return 0, err
	}
	ptr, length, err := r.writeStringLocked(jsonArr)
	if err != nil {
		return 0, err
	}
	defer r.wasmFreeLocked(ptr)
	res, err := r.wzExp.valCall.Call(r.wzCtx,
		uint64(r.qjsCtx), fn, this, uint64(ptr), uint64(length))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

// buildArgsJSONFromHandles JSON-encodes each argv by roundtripping through
// val_to_json. Only used for internal call paths (Keys, Bytes); user-facing
// Value.Call uses plain JSON args because the caller supplies plain Go
// values. This is a minor inefficiency that M3's handle-aware call export
// will fix.
func (r *Runtime) buildArgsJSONFromHandles(args []uint64) (string, error) {
	if len(args) == 0 {
		return "[]", nil
	}
	parts := make([]string, 0, len(args))
	for _, h := range args {
		j, err := r.valToJSONLocked(h)
		if err != nil {
			return "", err
		}
		if j == "" {
			j = "null"
		}
		parts = append(parts, j)
	}
	buf := "[" + parts[0]
	for i := 1; i < len(parts); i++ {
		buf += "," + parts[i]
	}
	buf += "]"
	return buf, nil
}

// argsAsAny is a placeholder used by the retained marshal call above.
// It intentionally returns nil to signal "use handle route instead".
func argsAsAny(_ []uint64, _ *Runtime) []any { return nil }
