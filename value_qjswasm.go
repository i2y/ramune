//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"encoding/json"
	"sync/atomic"

	"github.com/fastschema/qjs"
)

// Value wraps a fastschema/qjs *Value. Close() calls Free() on the
// underlying handle; the fastschema runtime refcounts QuickJS JSValues
// so explicit freeing is both safe and recommended.
type Value struct {
	noCopy noCopy
	fsv    *qjs.Value
	rt     *Runtime
	closed atomic.Bool
}

func (r *Runtime) wrapValue(v *qjs.Value) *Value {
	if v == nil {
		return nil
	}
	return &Value{fsv: v, rt: r}
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

// Close releases the underlying JSValue.
func (v *Value) Close() error {
	if v == nil || v.closed.Swap(true) {
		return nil
	}
	if v.rt == nil || v.rt.closed.Load() {
		return nil
	}
	v.rt.dispatch(func() {
		if v.fsv != nil {
			v.fsv.Free()
			v.fsv = nil
		}
	})
	return nil
}

// Ptr returns an opaque handle identifier (not a real pointer). Two
// different runtime objects may happen to share the same identifier
// value; callers should use Ptr only to compare JSValue identity within
// the same Runtime.
func (v *Value) Ptr() uintptr {
	if v == nil || v.fsv == nil {
		return 0
	}
	// fastschema exposes the raw QuickJS JSValue as a uint64 via Raw()
	// if available; fallback is a pointer identity on the wrapper.
	type rawer interface {
		Raw() uint64
	}
	if r, ok := any(v.fsv).(rawer); ok {
		return uintptr(r.Raw())
	}
	return uintptr(0)
}

// -----------------------------------------------------------------------
// Primitive accessors
// -----------------------------------------------------------------------

// String returns the JS string form (JS_ToString semantics).
func (v *Value) String() string {
	if err := v.preflight(); err != nil {
		return ""
	}
	var out string
	v.rt.dispatch(func() {
		out = v.fsv.String()
	})
	return out
}

// GoString returns the JS string form with a nil-error signal. fastschema
// doesn't expose a string conversion error separately, so we always
// return nil unless the value wrapper is invalid.
func (v *Value) GoString() (string, error) {
	if err := v.preflight(); err != nil {
		return "", err
	}
	var out string
	v.rt.dispatch(func() {
		out = v.fsv.String()
	})
	return out, nil
}

// Float64 returns the value as float64.
func (v *Value) Float64() (float64, error) {
	if err := v.preflight(); err != nil {
		return 0, err
	}
	var out float64
	v.rt.dispatch(func() {
		out = v.fsv.Float64()
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
		out = v.fsv.Int64()
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
		out = v.fsv.Bool()
	})
	return out, nil
}

// Bytes returns the underlying bytes for a Uint8Array or ArrayBuffer.
// Tries fastschema's JsTypedArrayToGo / JsArrayBufferToGo helpers first
// and falls back to Array.from(v) + JSON for anything else (covers cases
// where the value is a Uint8Array whose constructor signature makes the
// typed-array helpers reject it).
func (v *Value) Bytes() ([]byte, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out []byte
	v.rt.dispatch(func() {
		if b, e := qjs.JsArrayBufferToGo(v.fsv); e == nil && b != nil {
			out = b
			return
		}
		if b, e := qjs.JsTypedArrayToGo(v.fsv); e == nil && b != nil {
			out = b
			return
		}
		// Fallback: JSON.stringify gives {"0":10,"1":20,...} for typed
		// arrays, which isn't what we want. Use Array.from(v) to flatten
		// into a plain JS array and read the length/elements.
		helper, err := v.rt.qjsCtx.Eval("<bytes>", qjs.Code("((v)=>Array.from(v))"))
		if err != nil {
			return
		}
		defer helper.Free()
		arr, err := v.rt.qjsCtx.Invoke(helper, nil, v.fsv)
		if err != nil {
			return
		}
		defer arr.Free()
		n := int(arr.Len())
		out = make([]byte, n)
		for i := 0; i < n; i++ {
			el := arr.GetPropertyIndex(int64(i))
			if el != nil {
				out[i] = byte(el.Int32())
				el.Free()
			}
		}
	})
	return out, nil
}

// -----------------------------------------------------------------------
// Type checks
// -----------------------------------------------------------------------

func (v *Value) IsUndefined() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsUndefined() })
	return out
}
func (v *Value) IsNull() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsNull() })
	return out
}
func (v *Value) IsBool() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsBool() })
	return out
}
func (v *Value) IsNumber() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsNumber() })
	return out
}
func (v *Value) IsString() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsString() })
	return out
}
func (v *Value) IsObject() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsObject() })
	return out
}
func (v *Value) IsArray() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsArray() })
	return out
}
func (v *Value) IsFunction() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsFunction() })
	return out
}
func (v *Value) IsPromise() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsPromise() })
	return out
}
func (v *Value) IsException() bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.IsError() })
	return out
}

// -----------------------------------------------------------------------
// Property / array ops
// -----------------------------------------------------------------------

// Attr reads a property.
func (v *Value) Attr(name string) *Value {
	out, _ := v.AttrErr(name)
	return out
}

func (v *Value) AttrErr(name string) (*Value, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out *Value
	v.rt.dispatch(func() {
		p := v.fsv.GetPropertyStr(name)
		if p != nil {
			out = v.rt.wrapValue(p)
		}
	})
	return out, nil
}

func (v *Value) SetAttr(name string, value any) error {
	if err := v.preflight(); err != nil {
		return err
	}
	var err error
	v.rt.dispatch(func() {
		jv, e := v.rt.goToJSLocked(value)
		if e != nil {
			err = e
			return
		}
		v.fsv.SetPropertyStr(name, jv)
	})
	return err
}

func (v *Value) Keys() ([]string, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out []string
	var err error
	v.rt.dispatch(func() {
		out, err = v.fsv.GetOwnPropertyNames()
	})
	return out, err
}

func (v *Value) Len() (int, error) {
	if err := v.preflight(); err != nil {
		return 0, err
	}
	var out int
	v.rt.dispatch(func() {
		out = int(v.fsv.Len())
	})
	return out, nil
}

// Index returns the array element at i. Out-of-bounds returns nil.
func (v *Value) Index(i int) *Value {
	if err := v.preflight(); err != nil {
		return nil
	}
	var out *Value
	v.rt.dispatch(func() {
		if !v.fsv.HasPropertyIndex(int64(i)) {
			return
		}
		p := v.fsv.GetPropertyIndex(int64(i))
		if p != nil {
			out = v.rt.wrapValue(p)
		}
	})
	return out
}

func (v *Value) Has(name string) bool {
	if err := v.preflight(); err != nil {
		return false
	}
	var out bool
	v.rt.dispatch(func() { out = v.fsv.HasProperty(name) })
	return out
}

func (v *Value) Delete(name string) error {
	if err := v.preflight(); err != nil {
		return err
	}
	v.rt.dispatch(func() { v.fsv.DeleteProperty(name) })
	return nil
}

// Call invokes v as a function with args and returns the result as
// *Value.
func (v *Value) Call(args ...any) (*Value, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out *Value
	var err error
	v.rt.dispatch(func() {
		jsArgs := make([]*qjs.Value, 0, len(args))
		for _, a := range args {
			jv, e := v.rt.goToJSLocked(a)
			if e != nil {
				err = e
				return
			}
			jsArgs = append(jsArgs, jv)
		}
		res, e := v.rt.qjsCtx.Invoke(v.fsv, nil, jsArgs...)
		if e != nil {
			err = e
			return
		}
		out = v.rt.wrapValue(res)
	})
	return out, err
}

// ToMap converts a JS object to a Go map via JSON round-trip.
func (v *Value) ToMap() (map[string]any, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out map[string]any
	var err error
	v.rt.dispatch(func() {
		j, e := v.fsv.JSONStringify()
		if e != nil {
			err = e
			return
		}
		err = json.Unmarshal([]byte(j), &out)
	})
	return out, err
}

// ToSlice converts a JS array to a Go slice via JSON round-trip.
func (v *Value) ToSlice() ([]any, error) {
	if err := v.preflight(); err != nil {
		return nil, err
	}
	var out []any
	var err error
	v.rt.dispatch(func() {
		j, e := v.fsv.JSONStringify()
		if e != nil {
			err = e
			return
		}
		err = json.Unmarshal([]byte(j), &out)
	})
	return out, err
}
