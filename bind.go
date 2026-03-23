package ramune

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// Bind exposes a Go struct as a JavaScript object on globalThis.
// Exported struct fields become JS properties (read from current Go state),
// and methods on the pointer receiver become callable JS functions.
//
// Field naming: if a field has a `js:"name"` tag, that name is used;
// otherwise the first letter is lowercased (e.g. Name → name).
//
// Method naming: Go method names are converted to camelCase
// (e.g. Greet → greet, SetAge → setAge).
//
// Fields are backed by the Go struct: reading a property in JS always
// returns the current Go value. Setting a property in JS updates the
// Go struct. Methods that mutate the struct are reflected on subsequent
// property reads.
func (r *Runtime) Bind(name string, v any) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("ramune: Bind requires a pointer to struct, got %T", v)
	}

	structVal := rv.Elem()
	structType := structVal.Type()

	// Register getter/setter functions for each exported field.
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}

		jsName := fieldJSName(field)
		idx := i

		// Getter: __bind_<name>_get_<field>
		getterName := "__bind_" + name + "_get_" + jsName
		err := r.RegisterFunc(getterName, func(args []any) (any, error) {
			fv := structVal.Field(idx)
			return reflectToAny(fv), nil
		})
		if err != nil {
			return fmt.Errorf("ramune: Bind field getter %q: %w", jsName, err)
		}

		// Setter: __bind_<name>_set_<field>
		setterName := "__bind_" + name + "_set_" + jsName
		err = r.RegisterFunc(setterName, func(args []any) (any, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("setter requires one argument")
			}
			fv := structVal.Field(idx)
			if err := setReflectValue(fv, args[0]); err != nil {
				return nil, err
			}
			return nil, nil
		})
		if err != nil {
			return fmt.Errorf("ramune: Bind field setter %q: %w", jsName, err)
		}
	}

	// Register methods on the pointer receiver.
	methodNames := make([]string, 0)
	for i := 0; i < rv.NumMethod(); i++ {
		method := rv.Type().Method(i)
		if !method.IsExported() {
			continue
		}

		jsMethodName := toCamelCase(method.Name)
		funcName := "__bind_" + name + "_method_" + jsMethodName
		methodIdx := i

		err := r.RegisterFunc(funcName, func(args []any) (any, error) {
			m := rv.Method(methodIdx)
			mType := m.Type()

			// Build arguments via reflection.
			in := make([]reflect.Value, mType.NumIn())
			for j := 0; j < mType.NumIn(); j++ {
				if j < len(args) {
					converted, err := convertArg(args[j], mType.In(j))
					if err != nil {
						return nil, fmt.Errorf("arg %d: %w", j, err)
					}
					in[j] = converted
				} else {
					in[j] = reflect.Zero(mType.In(j))
				}
			}

			out := m.Call(in)

			// Handle return values.
			switch len(out) {
			case 0:
				return nil, nil
			case 1:
				// Could be a value or an error.
				if mType.Out(0).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
					if out[0].IsNil() {
						return nil, nil
					}
					return nil, out[0].Interface().(error)
				}
				return reflectToAny(out[0]), nil
			case 2:
				// Assume (value, error) pattern.
				var err error
				if !out[1].IsNil() {
					err = out[1].Interface().(error)
				}
				return reflectToAny(out[0]), err
			default:
				// Return first value only.
				return reflectToAny(out[0]), nil
			}
		})
		if err != nil {
			return fmt.Errorf("ramune: Bind method %q: %w", jsMethodName, err)
		}

		methodNames = append(methodNames, jsMethodName)
	}

	// Build JS object with Object.defineProperty for getters/setters,
	// and plain function wrappers for methods.
	var sb strings.Builder
	sb.WriteString("(function() {\n")
	sb.WriteString("  var obj = {};\n")

	// Define properties with getters and setters.
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if !field.IsExported() {
			continue
		}
		jsName := fieldJSName(field)
		getterName := "__bind_" + name + "_get_" + jsName
		setterName := "__bind_" + name + "_set_" + jsName

		jsNameEsc, _ := json.Marshal(jsName)
		sb.WriteString(fmt.Sprintf("  Object.defineProperty(obj, %s, {\n", string(jsNameEsc)))
		sb.WriteString(fmt.Sprintf("    get: function() { return %s(); },\n", getterName))
		sb.WriteString(fmt.Sprintf("    set: function(v) { %s(v); },\n", setterName))
		sb.WriteString("    enumerable: true,\n")
		sb.WriteString("    configurable: true\n")
		sb.WriteString("  });\n")
	}

	// Define methods.
	for _, jsMethodName := range methodNames {
		funcName := "__bind_" + name + "_method_" + jsMethodName
		jsNameEsc, _ := json.Marshal(jsMethodName)
		sb.WriteString(fmt.Sprintf("  obj[%s] = function() { return %s.apply(null, Array.prototype.slice.call(arguments)); };\n",
			string(jsNameEsc), funcName))
	}

	sb.WriteString(fmt.Sprintf("  globalThis[%s] = obj;\n", mustJSONString(name)))
	sb.WriteString("})();\n")

	return r.Exec(sb.String())
}

// fieldJSName returns the JS property name for a struct field.
// Uses the `js` tag if present, otherwise lowercases the first letter.
func fieldJSName(f reflect.StructField) string {
	if tag := f.Tag.Get("js"); tag != "" {
		// Support `js:"name,omitempty"` style — take the first part.
		if idx := strings.IndexByte(tag, ','); idx != -1 {
			tag = tag[:idx]
		}
		if tag != "" && tag != "-" {
			return tag
		}
	}
	return lcFirst(f.Name)
}

// lcFirst lowercases the first rune of s.
func lcFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// toCamelCase converts a PascalCase Go identifier to camelCase.
// Examples: Greet → greet, SetAge → setAge, HTTPServer → httpServer.
func toCamelCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)

	// Find the length of the leading uppercase run.
	upper := 0
	for upper < len(runes) && unicode.IsUpper(runes[upper]) {
		upper++
	}

	if upper == 0 {
		return s
	}

	// If the entire string is uppercase, lowercase all.
	if upper == len(runes) {
		return strings.ToLower(s)
	}

	// If only one leading uppercase letter, just lowercase it.
	if upper == 1 {
		runes[0] = unicode.ToLower(runes[0])
		return string(runes)
	}

	// Multiple leading uppercase: lowercase all except the last one
	// (which starts a new word). E.g. HTTPServer → httpServer.
	for i := 0; i < upper-1; i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

// reflectToAny converts a reflect.Value to an any suitable for goToJS.
func reflectToAny(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}

	kind := v.Kind()

	// Handle pointers and interfaces.
	if kind == reflect.Ptr || kind == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return reflectToAny(v.Elem())
	}

	switch kind {
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return int(v.Uint())
	case reflect.Uint64:
		return float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.String:
		return v.String()
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Bytes()
		}
		result := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			result[i] = reflectToAny(v.Index(i))
		}
		return result
	case reflect.Map:
		result := make(map[string]any)
		for _, key := range v.MapKeys() {
			result[fmt.Sprint(key.Interface())] = reflectToAny(v.MapIndex(key))
		}
		return result
	case reflect.Struct:
		result := make(map[string]any)
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			result[fieldJSName(f)] = reflectToAny(v.Field(i))
		}
		return result
	default:
		return fmt.Sprint(v.Interface())
	}
}

// setReflectValue sets a reflect.Value from a JS-converted Go value.
// Reuses convertArg from register.go for type conversion.
func setReflectValue(fv reflect.Value, val any) error {
	if !fv.CanSet() {
		return fmt.Errorf("field is not settable")
	}
	converted, err := convertArg(val, fv.Type())
	if err != nil {
		return err
	}
	fv.Set(converted)
	return nil
}

// mustJSONString returns a JSON-quoted string literal.
func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
