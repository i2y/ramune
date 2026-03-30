package jsrt

import "reflect"

// JSObject wraps an any value with JavaScript-like dynamic property access and method calls.
// Used by the transpiler as a fallback when TypeScript types cannot be statically mapped to Go types.
// When the TypeScript checker provides concrete type information (including narrowed types),
// the transpiler generates concrete Go types with type assertions instead.
type JSObject struct {
	V any // The underlying value
}

// Obj wraps a value in a JSObject.
func Obj(v any) *JSObject {
	if v == nil {
		return &JSObject{}
	}
	if o, ok := v.(*JSObject); ok {
		return o
	}
	return &JSObject{V: v}
}

// Get accesses a property by name, returning a new JSObject.
// Supports structs (exported fields), maps (string keys), and *JSObject nesting.
func (o *JSObject) Get(name string) *JSObject {
	if o.V == nil {
		return &JSObject{}
	}
	return Obj(GetField(o.V, name))
}

// Call invokes the underlying value as a function with the given arguments.
func (o *JSObject) Call(args ...any) *JSObject {
	if o.V == nil {
		return &JSObject{}
	}
	rv := reflect.ValueOf(o.V)
	if rv.Kind() != reflect.Func {
		return &JSObject{}
	}
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		if a == nil {
			in[i] = reflect.Zero(rv.Type().In(i))
		} else {
			in[i] = reflect.ValueOf(a)
		}
	}
	out := rv.Call(in)
	if len(out) == 0 {
		return &JSObject{}
	}
	return Obj(out[0].Interface())
}

// Set sets a property by name on the underlying map or struct.
func (o *JSObject) Set(name string, value any) {
	if o.V == nil {
		return
	}
	rv := reflect.ValueOf(o.V)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Map {
		// Try exact name first, then camelCase
		rv.SetMapIndex(reflect.ValueOf(name), reflect.ValueOf(value))
	}
}

// String returns the underlying value as a string.
func (o *JSObject) String() string {
	if o.V == nil {
		return ""
	}
	if s, ok := o.V.(string); ok {
		return s
	}
	return ""
}

// Float64 returns the underlying value as a float64.
func (o *JSObject) Float64() float64 {
	if o.V == nil {
		return 0
	}
	switch v := o.V.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

// Int returns the underlying value as an int.
func (o *JSObject) Int() int {
	return int(o.Float64())
}

// Bool returns the underlying value using JavaScript truthiness rules.
func (o *JSObject) Bool() bool {
	return ToBool(o.V)
}

// IsNil returns true if the underlying value is nil.
func (o *JSObject) IsNil() bool {
	return o.V == nil
}

// Unwrap returns the raw underlying value.
func (o *JSObject) Unwrap() any {
	return o.V
}

// Len returns the length of the underlying slice, array, map, or string.
func (o *JSObject) Len() int {
	return Len(o.V)
}

// Index returns the element at the given index.
func (o *JSObject) Index(key any) *JSObject {
	return Obj(Index(o.V, key))
}
