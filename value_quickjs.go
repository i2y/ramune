//go:build quickjs

package ramune

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"modernc.org/quickjs"
)

// Value wraps a QuickJS value with lifecycle management.
// Call Close() to free the value.
type Value struct {
	noCopy noCopy
	val    quickjs.Value
	rt     *Runtime
	closed atomic.Bool
}

// evalWithThis safely evaluates code with a value as `this` context.
// Uses a temporary global variable to avoid EvalThis which can crash
// when QuickJS's internal bytecode GC races with Go's GC.
func (r *Runtime) evalWithThis(v quickjs.Value, code string) (any, error) {
	g := r.vm.GlobalObject()
	atom, err := r.vm.NewAtom("__qjsTmp")
	if err != nil {
		return nil, err
	}
	g.SetPropertyValue(atom, v.Dup())
	result, err := r.vm.Eval("(function(){var t=__qjsTmp;delete globalThis.__qjsTmp;return (function(){return "+code+"}).call(t);})()", quickjs.EvalGlobal)
	return result, err
}

func (r *Runtime) wrapValue(v quickjs.Value) *Value {
	// Dup the value to ensure our wrapper owns a reference that won't be
	// freed by QuickJS internal operations.
	return &Value{val: v.Dup(), rt: r}
}

// Close marks the value as closed. QuickJS values are reference-counted
// internally; the VM releases all remaining references on VM.Close().
// Explicit Free() via dispatch is avoided because it can race with
// Runtime.Close() and cause use-after-free in the QuickJS heap.
func (v *Value) Close() error {
	if v == nil {
		return nil
	}
	v.closed.Swap(true)
	return nil
}

// Ptr returns 0 for QuickJS (no raw pointer).
func (v *Value) Ptr() uintptr {
	return 0
}

// String returns the string representation of the value.
func (v *Value) String() string {
	if v == nil {
		return "<nil>"
	}
	var result string
	v.rt.dispatch(func() {
		r, err := v.val.Any()
		if err != nil {
			result = "<error>"
			return
		}
		result = fmt.Sprintf("%v", r)
	})
	return result
}

// Float64 returns the value as float64.
func (v *Value) Float64() (float64, error) {
	if v == nil {
		return 0, ErrNilValue
	}
	var result float64
	var err error
	v.rt.dispatch(func() {
		r, e := v.val.Any()
		if e != nil {
			err = e
			return
		}
		switch n := r.(type) {
		case float64:
			result = n
		case int:
			result = float64(n)
		default:
			err = fmt.Errorf("not a number: %T", r)
		}
	})
	return result, err
}

// Int64 returns the value as int64.
func (v *Value) Int64() (int64, error) {
	f, err := v.Float64()
	if err != nil {
		return 0, err
	}
	return int64(f), nil
}

// Bool returns the value as bool.
func (v *Value) Bool() (bool, error) {
	if v == nil {
		return false, ErrNilValue
	}
	var result bool
	var err error
	v.rt.dispatch(func() {
		r, e := v.val.Any()
		if e != nil {
			err = e
			return
		}
		switch b := r.(type) {
		case bool:
			result = b
		default:
			err = fmt.Errorf("not a boolean: %T", r)
		}
	})
	return result, err
}

// Bytes returns the value as a byte slice.
// For Uint8Array/ArrayBuffer, extracts the raw bytes.
func (v *Value) Bytes() ([]byte, error) {
	if v == nil {
		return nil, ErrNilValue
	}
	var result []byte
	var err error
	v.rt.dispatch(func() {
		// Check if it's a typed array by reading its length and elements.
		r, e := v.rt.evalWithThis(v.val, "this instanceof Uint8Array ? Array.from(this) : null")
		if e == nil {
			if s, ok := r.(string); ok && s != "" {
				// Got JSON array string
				var arr []float64
				if json.Unmarshal([]byte(s), &arr) == nil {
					result = make([]byte, len(arr))
					for i, f := range arr {
						result[i] = byte(f)
					}
					return
				}
			}
		}
		// Fallback: try JSON.stringify to get array of numbers.
		r2, e2 := v.rt.evalWithThis(v.val, "JSON.stringify(Array.from(this))")
		if e2 == nil {
			if s, ok := r2.(string); ok {
				var arr []float64
				if json.Unmarshal([]byte(s), &arr) == nil {
					result = make([]byte, len(arr))
					for i, f := range arr {
						result[i] = byte(f)
					}
					return
				}
			}
		}
		// Last resort: string conversion.
		s, e := v.GoString()
		if e != nil {
			err = e
			return
		}
		result = []byte(s)
	})
	return result, err
}

// GoString returns the value as a Go string.
func (v *Value) GoString() (string, error) {
	if v == nil {
		return "", ErrNilValue
	}
	var result string
	var err error
	v.rt.dispatch(func() {
		r, e := v.val.Any()
		if e != nil {
			err = e
			return
		}
		switch s := r.(type) {
		case string:
			result = s
		default:
			result = fmt.Sprintf("%v", r)
		}
	})
	return result, err
}

// IsNull returns true if the value is null.
func (v *Value) IsNull() bool {
	if v == nil {
		return true
	}
	var result bool
	v.rt.dispatch(func() {
		r, err := v.val.Any()
		if err != nil {
			return
		}
		result = r == nil
	})
	return result
}

// IsUndefined returns true if the value is undefined.
func (v *Value) IsUndefined() bool {
	if v == nil {
		return true
	}
	var result bool
	v.rt.dispatch(func() {
		result = v.val.IsUndefined()
	})
	return result
}

// IsArray returns true if the value is an array.
func (v *Value) IsArray() bool {
	if v == nil {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		r, err := v.rt.evalWithThis(v.val, "Array.isArray(this)")
		if err != nil {
			return
		}
		if b, ok := r.(bool); ok {
			result = b
		}
	})
	return result
}

// IsFunction returns true if the value is a function.
func (v *Value) IsFunction() bool {
	if v == nil {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		r, err := v.rt.evalWithThis(v.val, "typeof this === 'function'")
		if err != nil {
			return
		}
		if b, ok := r.(bool); ok {
			result = b
		}
	})
	return result
}

// Keys returns the property names of the object.
func (v *Value) Keys() ([]string, error) {
	if v == nil {
		return nil, ErrNilValue
	}
	var result []string
	var err error
	v.rt.dispatch(func() {
		r, e := v.rt.evalWithThis(v.val, "JSON.stringify(Object.keys(this))")
		if e != nil {
			err = e
			return
		}
		if s, ok := r.(string); ok {
			json.Unmarshal([]byte(s), &result)
		}
	})
	return result, err
}

// Len returns the length property of the value.
func (v *Value) Len() (int, error) {
	if v == nil {
		return 0, ErrNilValue
	}
	var result int
	var err error
	v.rt.dispatch(func() {
		r, e := v.rt.evalWithThis(v.val, "this.length")
		if e != nil {
			err = e
			return
		}
		switch n := r.(type) {
		case float64:
			result = int(n)
		case int:
			result = n
		}
	})
	return result, err
}

// Has returns true if the object has the named property.
func (v *Value) Has(name string) bool {
	if v == nil {
		return false
	}
	var result bool
	v.rt.dispatch(func() {
		atom, err := v.rt.vm.NewAtom(name)
		if err != nil {
			return
		}
		prop, err := v.val.GetPropertyValue(atom)
		if err != nil {
			return
		}
		result = !prop.IsUndefined()
		prop.Free()
	})
	return result
}

// Delete removes a property from the object.
func (v *Value) Delete(name string) error {
	if v == nil {
		return ErrNilValue
	}
	var err error
	v.rt.dispatch(func() {
		code := fmt.Sprintf("delete this[%q]", name)
		_, err = v.rt.evalWithThis(v.val, code)
	})
	return err
}

// Index returns the value at the given array index.
func (v *Value) Index(i int) *Value {
	if v == nil {
		return nil
	}
	var result *Value
	v.rt.dispatch(func() {
		atom, err := v.rt.vm.NewAtom(fmt.Sprintf("%d", i))
		if err != nil {
			return
		}
		prop, err := v.val.GetPropertyValue(atom)
		if err != nil || prop.IsUndefined() {
			prop.Free()
			return
		}
		result = v.rt.wrapValue(prop)
	})
	return result
}

// ToMap converts the value to a Go map.
func (v *Value) ToMap() (map[string]any, error) {
	if v == nil {
		return nil, ErrNilValue
	}
	var result map[string]any
	var err error
	v.rt.dispatch(func() {
		r, e := v.rt.evalWithThis(v.val, "JSON.stringify(this)")
		if e != nil {
			err = e
			return
		}
		if s, ok := r.(string); ok {
			err = json.Unmarshal([]byte(s), &result)
		}
	})
	return result, err
}

// ToSlice converts the value to a Go slice.
func (v *Value) ToSlice() ([]any, error) {
	if v == nil {
		return nil, ErrNilValue
	}
	var result []any
	var err error
	v.rt.dispatch(func() {
		r, e := v.rt.evalWithThis(v.val, "JSON.stringify(this)")
		if e != nil {
			err = e
			return
		}
		if s, ok := r.(string); ok {
			err = json.Unmarshal([]byte(s), &result)
		}
	})
	return result, err
}

// Attr returns a property value by name.
func (v *Value) Attr(name string) *Value {
	if v == nil {
		return nil
	}
	var result *Value
	v.rt.dispatch(func() {
		atom, err := v.rt.vm.NewAtom(name)
		if err != nil {
			return
		}
		prop, err := v.val.GetPropertyValue(atom)
		if err != nil {
			return
		}
		result = v.rt.wrapValue(prop)
	})
	return result
}

// SetAttr sets a property value by name.
func (v *Value) SetAttr(name string, val any) error {
	if v == nil {
		return ErrNilValue
	}
	var err error
	v.rt.dispatch(func() {
		atom, e := v.rt.vm.NewAtom(name)
		if e != nil {
			err = e
			return
		}
		// If val is a *Value, use SetPropertyValue with the underlying quickjs.Value.
		if qv, ok := val.(*Value); ok {
			err = v.val.SetPropertyValue(atom, qv.val.Dup())
			return
		}
		err = v.val.SetProperty(atom, val)
	})
	return err
}

// AttrErr returns a property value by name, with an error.
func (v *Value) AttrErr(name string) (*Value, error) {
	if v == nil {
		return nil, ErrNilValue
	}
	var result *Value
	var err error
	v.rt.dispatch(func() {
		atom, e := v.rt.vm.NewAtom(name)
		if e != nil {
			err = e
			return
		}
		prop, e := v.val.GetPropertyValue(atom)
		if e != nil {
			err = e
			return
		}
		result = v.rt.wrapValue(prop)
	})
	return result, err
}

// Call invokes the value as a function with the given arguments.
func (v *Value) Call(args ...any) (*Value, error) {
	if v == nil {
		return nil, ErrNilValue
	}
	var result *Value
	var err error
	v.rt.dispatch(func() {
		// Store function temporarily, call it with args.
		// Use a temp global to avoid complex argument marshaling.
		code := "__qjsTmpCallTarget"
		atom, e := v.rt.vm.NewAtom(code)
		if e != nil {
			err = e
			return
		}
		g := v.rt.vm.GlobalObject()
		g.SetPropertyValue(atom, v.val.Dup())

		// Build call expression.
		argCode := ""
		for i, arg := range args {
			if i > 0 {
				argCode += ","
			}
			b, _ := json.Marshal(arg)
			argCode += string(b)
		}
		callCode := code + "(" + argCode + ")"
		r, e := v.rt.vm.EvalValue(callCode, quickjs.EvalGlobal)

		// Clean up temp.
		cleanAtom, _ := v.rt.vm.NewAtom(code)
		g.SetProperty(cleanAtom, nil)

		if e != nil {
			err = &JSError{Message: e.Error()}
			return
		}
		result = v.rt.wrapValue(r)
	})
	return result, err
}
