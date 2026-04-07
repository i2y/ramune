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

// OmitKeys returns a shallow copy of the value with the specified keys removed.
// Works with maps (string keys) and structs (copies to map, omitting named fields).
func OmitKeys(v any, keys ...string) any {
	if v == nil {
		return nil
	}
	omit := make(map[string]bool, len(keys))
	for _, k := range keys {
		omit[k] = true
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		result := make(map[string]any)
		for _, key := range rv.MapKeys() {
			name := key.String()
			if !omit[name] {
				result[name] = rv.MapIndex(key).Interface()
			}
		}
		return result
	case reflect.Struct:
		result := make(map[string]any)
		t := rv.Type()
		for i := range t.NumField() {
			name := t.Field(i).Name
			if !omit[name] && t.Field(i).IsExported() {
				result[name] = rv.Field(i).Interface()
			}
		}
		return result
	}
	return v
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
	case reflect.Struct:
		name, ok := key.(string)
		if ok {
			field := rv.FieldByName(name)
			if field.IsValid() {
				return field.Interface()
			}
		}
	}
	return nil
}

// CallMethod calls a method on an object by name at runtime using reflection.
// Returns the first return value, or nil if the method doesn't exist.
func CallMethod(obj any, method string, args ...any) any {
	if obj == nil {
		return nil
	}
	rv := reflect.ValueOf(obj)
	m := rv.MethodByName(method)
	if !m.IsValid() {
		// Try pointer receiver
		if rv.Kind() != reflect.Ptr {
			ptr := reflect.New(rv.Type())
			ptr.Elem().Set(rv)
			m = ptr.MethodByName(method)
		}
	}
	if !m.IsValid() {
		return nil
	}
	var in []reflect.Value
	for _, a := range args {
		in = append(in, reflect.ValueOf(a))
	}
	out := m.Call(in)
	if len(out) > 0 {
		return out[0].Interface()
	}
	return nil
}

// GoExportedName converts a camelCase JS name to Go exported PascalCase.
func GoExportedName(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

// OmitFields returns a copy of a map[string]any with the specified keys removed.
func OmitFields(obj any, keys ...string) map[string]any {
	result := map[string]any{}
	if m, ok := obj.(map[string]any); ok {
		omit := make(map[string]bool)
		for _, k := range keys {
			omit[k] = true
		}
		for k, v := range m {
			if !omit[k] {
				result[k] = v
			}
		}
	}
	return result
}

// Flat flattens a slice one level: [[1,2],[3]] → [1,2,3].
func Flat(v any) []any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return []any{v}
	}
	var result []any
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i).Interface()
		erv := reflect.ValueOf(elem)
		for erv.Kind() == reflect.Ptr || erv.Kind() == reflect.Interface {
			if erv.IsNil() {
				break
			}
			erv = erv.Elem()
		}
		if erv.Kind() == reflect.Slice {
			for j := 0; j < erv.Len(); j++ {
				result = append(result, erv.Index(j).Interface())
			}
		} else {
			result = append(result, elem)
		}
	}
	return result
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

// Keys returns the keys of a map or struct as a string slice (like Object.keys()).
func Keys(v any) []string {
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
	case reflect.Map:
		keys := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			keys = append(keys, fmt.Sprint(k.Interface()))
		}
		return keys
	case reflect.Struct:
		t := rv.Type()
		keys := make([]string, 0, t.NumField())
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				keys = append(keys, t.Field(i).Name)
			}
		}
		return keys
	}
	return nil
}

// Values returns the values of a map or struct as an any slice (like Object.values()).
func Values(v any) []any {
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
	case reflect.Map:
		vals := make([]any, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			vals = append(vals, rv.MapIndex(k).Interface())
		}
		return vals
	case reflect.Struct:
		t := rv.Type()
		vals := make([]any, 0, t.NumField())
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				vals = append(vals, rv.Field(i).Interface())
			}
		}
		return vals
	}
	return nil
}

// Entries returns [key, value] pairs from a map or struct (like Object.entries()).
func Entries(v any) []any {
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
	case reflect.Map:
		entries := make([]any, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			entries = append(entries, []any{fmt.Sprint(k.Interface()), rv.MapIndex(k).Interface()})
		}
		return entries
	case reflect.Struct:
		t := rv.Type()
		entries := make([]any, 0, t.NumField())
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				entries = append(entries, []any{t.Field(i).Name, rv.Field(i).Interface()})
			}
		}
		return entries
	}
	return nil
}

// FromEntries creates a map from [key, value] pairs (like Object.fromEntries()).
func FromEntries(entries any) map[string]any {
	result := make(map[string]any)
	if entries == nil {
		return result
	}
	rv := reflect.ValueOf(entries)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return result
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		return result
	}
	for i := range rv.Len() {
		entry := rv.Index(i).Interface()
		erv := reflect.ValueOf(entry)
		for erv.Kind() == reflect.Ptr || erv.Kind() == reflect.Interface {
			erv = erv.Elem()
		}
		if erv.Kind() == reflect.Slice && erv.Len() >= 2 {
			key := fmt.Sprint(erv.Index(0).Interface())
			result[key] = erv.Index(1).Interface()
		}
	}
	return result
}

// IsArray returns true if the value is a slice or array (like Array.isArray()).
func IsArray(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	return rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array
}
