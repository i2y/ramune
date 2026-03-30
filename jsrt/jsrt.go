// Package jsrt provides runtime support for TypeScript-to-Go transpiled code.
package jsrt

import (
	"fmt"
	"reflect"
)

// TypeOf emulates JavaScript's typeof operator at runtime.
func TypeOf(v any) string {
	if v == nil {
		return "undefined"
	}
	switch v.(type) {
	case string:
		return "string"
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number"
	case bool:
		return "boolean"
	default:
		if reflect.TypeOf(v).Kind() == reflect.Func {
			return "function"
		}
		return "object"
	}
}

// Ptr returns a pointer to the given value. Useful for passing literals to *T parameters.
func Ptr[T any](v T) *T { return &v }

// ToAnySlice converts a typed slice to []any.
func ToAnySlice[T any](s []T) []any {
	r := make([]any, len(s))
	for i, v := range s {
		r[i] = v
	}
	return r
}

// JSError represents a JavaScript error thrown with throw statement.
type JSError struct {
	Value   any
	Message string
	Stack   string
}

func (e *JSError) Error() string {
	return e.Message
}

// Throw panics with a JSError, simulating JavaScript's throw statement.
func Throw(v any) {
	switch val := v.(type) {
	case *JSError:
		panic(val)
	case error:
		panic(&JSError{Value: val, Message: val.Error()})
	case string:
		panic(&JSError{Value: val, Message: val})
	default:
		panic(&JSError{Value: val, Message: fmt.Sprint(val)})
	}
}

// ThrowError simulates throw new Error(msg).
func ThrowError(msg string) {
	panic(&JSError{Value: msg, Message: msg})
}

// ThrowTypeError simulates throw new TypeError(msg).
func ThrowTypeError(msg string) {
	panic(&JSError{Value: msg, Message: "TypeError: " + msg})
}

// WrapError wraps a Go error into a JSError for throw.
func WrapError(err error) *JSError {
	return &JSError{Value: err, Message: err.Error()}
}

// GetField accesses a field on an any-typed value by name, like JavaScript property access.
// Supports structs (by exported field name), maps (by original or exported key), and pointers.
// For maps, tries the original name first, then the exported (PascalCase) name.
// For structs, tries the exported name first, then the original name.
func GetField(v any, name string) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	// Dereference pointers and interfaces
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		f := rv.FieldByName(name)
		if f.IsValid() {
			return f.Interface()
		}
	case reflect.Map:
		// Try exact name first, then lowercase first char (camelCase from Go exported name)
		key := reflect.ValueOf(name)
		f := rv.MapIndex(key)
		if f.IsValid() {
			return f.Interface()
		}
		// Try lowercase-first version (PascalCase → camelCase)
		if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
			lower := string(name[0]+32) + name[1:]
			f = rv.MapIndex(reflect.ValueOf(lower))
			if f.IsValid() {
				return f.Interface()
			}
		}
	}
	return nil
}

// ToBool converts any value to boolean using JavaScript truthiness rules.
// nil, 0, "", false → false; everything else → true.
func ToBool(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case int:
		return val != 0
	case string:
		return val != ""
	default:
		return true
	}
}

// SetIndex sets a value at a given index in a slice, array, or map at runtime.
func SetIndex(v any, key any, val any) {
	if v == nil {
		return
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		idx := toInt(key)
		if idx >= 0 && idx < rv.Len() {
			rv.Index(idx).Set(reflect.ValueOf(val))
		}
	case reflect.Map:
		mk := reflect.ValueOf(key)
		rv.SetMapIndex(mk, reflect.ValueOf(val))
	}
}

// Index indexes into a slice, array, map, or string at runtime.
func Index(v any, key any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		idx := toInt(key)
		if idx >= 0 && idx < rv.Len() {
			return rv.Index(idx).Interface()
		}
	case reflect.Map:
		mk := reflect.ValueOf(key)
		val := rv.MapIndex(mk)
		if val.IsValid() {
			return val.Interface()
		}
	case reflect.String:
		idx := toInt(key)
		s := rv.String()
		if idx >= 0 && idx < len(s) {
			return string(s[idx])
		}
	}
	return nil
}

// Len returns the length of a slice, array, map, string, or channel at runtime.
func Len(v any) int {
	if v == nil {
		return 0
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return 0
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String, reflect.Chan:
		return rv.Len()
	}
	return 0
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}

// GetFieldAs is a typed version of GetField that returns a value of the specified type.
func GetFieldAs[T any](v any, name string) T {
	result := GetField(v, name)
	if result == nil {
		var zero T
		return zero
	}
	if typed, ok := result.(T); ok {
		return typed
	}
	var zero T
	return zero
}

// CatchValue converts a recovered panic value into a *JSError.
func CatchValue(r any) *JSError {
	switch v := r.(type) {
	case *JSError:
		return v
	case error:
		return &JSError{Value: v, Message: v.Error()}
	case string:
		return &JSError{Value: v, Message: v}
	default:
		return &JSError{Value: v, Message: fmt.Sprint(v)}
	}
}
