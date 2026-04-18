//go:build goja

package ramune

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/dop251/goja"
)

// Value wraps a goja value. goja is GC-managed, so Close() is a no-op marker.
type Value struct {
	noCopy noCopy
	val    goja.Value
	rt     *Runtime
	closed atomic.Bool
}

func (r *Runtime) wrapValue(v goja.Value) *Value {
	return &Value{val: v, rt: r}
}

// Close marks the value as closed. No-op under goja (values are GC'd).
func (v *Value) Close() error {
	if v == nil {
		return nil
	}
	v.closed.Swap(true)
	return nil
}

// Ptr returns 0 for goja (no raw pointer).
func (v *Value) Ptr() uintptr { return 0 }

// String returns the string representation of the value.
func (v *Value) String() string {
	if v == nil || v.val == nil {
		return "<nil>"
	}
	var result string
	v.rt.dispatch(func() {
		result = v.val.String()
	})
	return result
}

// Float64 returns the value as float64.
func (v *Value) Float64() (float64, error) {
	if v == nil || v.val == nil {
		return 0, ErrNilValue
	}
	var result float64
	v.rt.dispatch(func() {
		result = v.val.ToFloat()
	})
	return result, nil
}

// Int64 returns the value as int64.
func (v *Value) Int64() (int64, error) {
	if v == nil || v.val == nil {
		return 0, ErrNilValue
	}
	var result int64
	v.rt.dispatch(func() {
		result = v.val.ToInteger()
	})
	return result, nil
}

// Bool returns the value as bool.
func (v *Value) Bool() (bool, error) {
	if v == nil || v.val == nil {
		return false, ErrNilValue
	}
	var result bool
	v.rt.dispatch(func() {
		result = v.val.ToBoolean()
	})
	return result, nil
}

// Bytes returns the value as a byte slice.
// For Uint8Array/ArrayBuffer, extracts the raw bytes.
func (v *Value) Bytes() ([]byte, error) {
	if v == nil || v.val == nil {
		return nil, ErrNilValue
	}
	var result []byte
	var err error
	v.rt.dispatch(func() {
		// Prefer Export: Uint8Array exports as []byte in goja.
		exp := v.val.Export()
		switch b := exp.(type) {
		case []byte:
			result = b
			return
		case string:
			result = []byte(b)
			return
		}
		// Fallback: JSON.stringify(Array.from(this)) to recover byte sequence.
		if v.rt.vm == nil {
			err = ErrNilValue
			return
		}
		v.rt.vm.Set("__gojaTmp", v.val)
		res, e := v.rt.vm.RunString("(function(){try{return JSON.stringify(Array.from(__gojaTmp))}catch(e){return null}})()")
		v.rt.vm.Set("__gojaTmp", goja.Undefined())
		if e == nil && res != nil && !goja.IsNull(res) && !goja.IsUndefined(res) {
			var arr []float64
			if json.Unmarshal([]byte(res.String()), &arr) == nil {
				result = make([]byte, len(arr))
				for i, f := range arr {
					result[i] = byte(f)
				}
				return
			}
		}
		result = []byte(v.val.String())
	})
	return result, err
}

// GoString returns the value as a Go string.
func (v *Value) GoString() (string, error) {
	if v == nil || v.val == nil {
		return "", ErrNilValue
	}
	var result string
	v.rt.dispatch(func() {
		result = v.val.String()
	})
	return result, nil
}

// IsNull returns true if the value is null.
func (v *Value) IsNull() bool {
	if v == nil || v.val == nil {
		return true
	}
	var result bool
	v.rt.dispatch(func() {
		result = goja.IsNull(v.val)
	})
	return result
}

// IsUndefined returns true if the value is undefined.
func (v *Value) IsUndefined() bool {
	if v == nil || v.val == nil {
		return true
	}
	var result bool
	v.rt.dispatch(func() {
		result = goja.IsUndefined(v.val)
	})
	return result
}

// IsArray returns true if the value is an array.
func (v *Value) IsArray() bool {
	if v == nil || v.val == nil {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		result = obj.ClassName() == "Array"
	})
	return result
}

// IsFunction returns true if the value is a function.
func (v *Value) IsFunction() bool {
	if v == nil || v.val == nil {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		_, ok := goja.AssertFunction(v.val)
		result = ok
	})
	return result
}

// Keys returns the property names of the object.
func (v *Value) Keys() ([]string, error) {
	if v == nil || v.val == nil {
		return nil, ErrNilValue
	}
	var result []string
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		result = obj.Keys()
	})
	return result, nil
}

// Len returns the length property of the value.
func (v *Value) Len() (int, error) {
	if v == nil || v.val == nil {
		return 0, ErrNilValue
	}
	var result int
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		lenV := obj.Get("length")
		if lenV == nil {
			return
		}
		result = int(lenV.ToInteger())
	})
	return result, nil
}

// Has returns true if the object has the named property.
func (v *Value) Has(name string) bool {
	if v == nil || v.val == nil {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		p := obj.Get(name)
		result = p != nil && !goja.IsUndefined(p)
	})
	return result
}

// Delete removes a property from the object.
func (v *Value) Delete(name string) error {
	if v == nil || v.val == nil {
		return ErrNilValue
	}
	var err error
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		err = obj.Delete(name)
	})
	return err
}

// Index returns the value at the given array index.
func (v *Value) Index(i int) *Value {
	if v == nil || v.val == nil {
		return nil
	}
	var result *Value
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		p := obj.Get(fmt.Sprintf("%d", i))
		if p == nil || goja.IsUndefined(p) {
			return
		}
		result = v.rt.wrapValue(p)
	})
	return result
}

// ToMap converts the value to a Go map.
// Always goes through JSON round-trip so numbers are normalized to float64,
// matching the JSC/QuickJS backends (goja exports small ints as int64).
func (v *Value) ToMap() (map[string]any, error) {
	if v == nil || v.val == nil {
		return nil, ErrNilValue
	}
	var result map[string]any
	var err error
	v.rt.dispatch(func() {
		exp := v.val.Export()
		b, e := json.Marshal(exp)
		if e != nil {
			err = e
			return
		}
		if e := json.Unmarshal(b, &result); e != nil {
			err = e
			return
		}
	})
	return result, err
}

// ToSlice converts the value to a Go slice.
// Always goes through JSON round-trip so numbers are normalized to float64,
// matching the JSC/QuickJS backends (goja exports small ints as int64).
func (v *Value) ToSlice() ([]any, error) {
	if v == nil || v.val == nil {
		return nil, ErrNilValue
	}
	var result []any
	var err error
	v.rt.dispatch(func() {
		exp := v.val.Export()
		b, e := json.Marshal(exp)
		if e != nil {
			err = e
			return
		}
		if e := json.Unmarshal(b, &result); e != nil {
			err = e
			return
		}
	})
	return result, err
}

// Attr returns a property value by name.
func (v *Value) Attr(name string) *Value {
	if v == nil || v.val == nil {
		return nil
	}
	var result *Value
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			return
		}
		p := obj.Get(name)
		if p == nil {
			return
		}
		result = v.rt.wrapValue(p)
	})
	return result
}

// SetAttr sets a property value by name.
func (v *Value) SetAttr(name string, val any) error {
	if v == nil || v.val == nil {
		return ErrNilValue
	}
	var err error
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			err = fmt.Errorf("not an object")
			return
		}
		if gv, ok := val.(*Value); ok && gv != nil {
			err = obj.Set(name, gv.val)
			return
		}
		err = obj.Set(name, val)
	})
	return err
}

// AttrErr returns a property value by name, with an error.
func (v *Value) AttrErr(name string) (*Value, error) {
	if v == nil || v.val == nil {
		return nil, ErrNilValue
	}
	var result *Value
	var err error
	v.rt.dispatch(func() {
		obj := v.val.ToObject(v.rt.vm)
		if obj == nil {
			err = fmt.Errorf("not an object")
			return
		}
		p := obj.Get(name)
		if p == nil {
			err = fmt.Errorf("property %q not found", name)
			return
		}
		result = v.rt.wrapValue(p)
	})
	return result, err
}

// Call invokes the value as a function with the given arguments.
func (v *Value) Call(args ...any) (*Value, error) {
	if v == nil || v.val == nil {
		return nil, ErrNilValue
	}
	var result *Value
	var err error
	v.rt.dispatch(func() {
		fn, ok := goja.AssertFunction(v.val)
		if !ok {
			err = fmt.Errorf("not a function")
			return
		}
		jsArgs := make([]goja.Value, len(args))
		for i, a := range args {
			jv, e := v.rt.goToJS(a)
			if e != nil {
				err = fmt.Errorf("arg %d: %w", i, e)
				return
			}
			jsArgs[i] = jv
		}
		r, e := fn(goja.Undefined(), jsArgs...)
		if e != nil {
			err = &JSError{Message: e.Error()}
			return
		}
		result = v.rt.wrapValue(r)
	})
	return result, err
}
