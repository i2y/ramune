package ramune

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// Register registers a typed Go function as a global JavaScript function.
// Unlike RegisterFunc, which requires manual args[]any casting, Register uses
// reflection to automatically convert JS arguments to the function's parameter
// types and convert return values back.
//
// Supported parameter types: float64, float32, int, int8/16/32/64, uint,
// uint8/16/32/64, string, bool, map[string]any, []any.
//
// Supported return signatures:
//   - func(...)                → no return value
//   - func(...) T              → single return value (non-error)
//   - func(...) error          → single error return
//   - func(...) (T, error)     → value + error
//
// Example:
//
//	ramune.Register(rt, "add", func(a, b float64) float64 {
//	    return a + b
//	})
func Register[F any](rt *Runtime, name string, fn F) error {
	fnType := reflect.TypeOf(fn)
	if fnType == nil || fnType.Kind() != reflect.Func {
		return fmt.Errorf("ramune: Register: expected function, got %T", fn)
	}
	if fnType.NumOut() > 2 {
		return fmt.Errorf("ramune: Register: function has %d return values, max 2 supported", fnType.NumOut())
	}
	errorInterface := reflect.TypeOf((*error)(nil)).Elem()
	if fnType.NumOut() == 2 && !fnType.Out(1).Implements(errorInterface) {
		return fmt.Errorf("ramune: Register: second return value must be error, got %s", fnType.Out(1))
	}
	return rt.RegisterFunc(name, wrapTypedFunc(name, fn))
}

// convertArg converts a JS callback argument (any) to the target Go type.
func convertArg(arg any, target reflect.Type) (reflect.Value, error) {
	// Handle nil → zero value.
	if arg == nil {
		return reflect.Zero(target), nil
	}

	// Handle *JSFunc pass-through.
	if target == reflect.TypeOf((*JSFunc)(nil)) {
		if jf, ok := arg.(*JSFunc); ok {
			return reflect.ValueOf(jf), nil
		}
		return reflect.Value{}, fmt.Errorf("cannot convert %T to *JSFunc", arg)
	}

	switch target.Kind() {
	case reflect.Float64:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to float64", arg)
		}
		return reflect.ValueOf(f), nil

	case reflect.Float32:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to float32", arg)
		}
		return reflect.ValueOf(float32(f)), nil

	case reflect.Int:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to int", arg)
		}
		return reflect.ValueOf(int(f)), nil

	case reflect.Int8:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to int8", arg)
		}
		return reflect.ValueOf(int8(f)), nil

	case reflect.Int16:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to int16", arg)
		}
		return reflect.ValueOf(int16(f)), nil

	case reflect.Int32:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to int32", arg)
		}
		return reflect.ValueOf(int32(f)), nil

	case reflect.Int64:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to int64", arg)
		}
		return reflect.ValueOf(int64(f)), nil

	case reflect.Uint:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to uint", arg)
		}
		return reflect.ValueOf(uint(f)), nil

	case reflect.Uint8:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to uint8", arg)
		}
		return reflect.ValueOf(uint8(f)), nil

	case reflect.Uint16:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to uint16", arg)
		}
		return reflect.ValueOf(uint16(f)), nil

	case reflect.Uint32:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to uint32", arg)
		}
		return reflect.ValueOf(uint32(f)), nil

	case reflect.Uint64:
		f, ok := toFloat64(arg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to uint64", arg)
		}
		return reflect.ValueOf(uint64(f)), nil

	case reflect.String:
		s, ok := arg.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to string", arg)
		}
		return reflect.ValueOf(s), nil

	case reflect.Bool:
		b, ok := arg.(bool)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to bool", arg)
		}
		return reflect.ValueOf(b), nil

	case reflect.Map:
		// Pass through map[string]any.
		if target == reflect.TypeOf(map[string]any(nil)) {
			m, ok := arg.(map[string]any)
			if !ok {
				return reflect.Value{}, fmt.Errorf("cannot convert %T to map[string]any", arg)
			}
			return reflect.ValueOf(m), nil
		}
		return reflect.Value{}, fmt.Errorf("unsupported map type %s", target)

	case reflect.Slice:
		// Pass through []any.
		if target == reflect.TypeOf([]any(nil)) {
			s, ok := arg.([]any)
			if !ok {
				return reflect.Value{}, fmt.Errorf("cannot convert %T to []any", arg)
			}
			return reflect.ValueOf(s), nil
		}
		// Typed slices: convert from []any element by element.
		if srcSlice, ok := arg.([]any); ok {
			elemType := target.Elem()
			result := reflect.MakeSlice(target, len(srcSlice), len(srcSlice))
			for i, elem := range srcSlice {
				converted, err := convertArg(elem, elemType)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("slice element %d: %w", i, err)
				}
				result.Index(i).Set(converted)
			}
			return result, nil
		}
		return reflect.Value{}, fmt.Errorf("cannot convert %T to %s", arg, target)

	case reflect.Interface:
		// any / interface{} — pass through as-is.
		return reflect.ValueOf(arg), nil

	case reflect.Struct:
		// Convert map[string]any → struct by matching field names
		m, ok := arg.(map[string]any)
		if !ok {
			return reflect.Value{}, fmt.Errorf("cannot convert %T to struct %s", arg, target)
		}
		result := reflect.New(target).Elem()
		for i := 0; i < target.NumField(); i++ {
			field := target.Field(i)
			if !field.IsExported() {
				continue
			}
			// Try json tag, then lowercase field name
			name := field.Name
			if tag := field.Tag.Get("json"); tag != "" {
				parts := strings.SplitN(tag, ",", 2)
				if parts[0] != "" && parts[0] != "-" {
					name = parts[0]
				}
			} else {
				runes := []rune(name)
				runes[0] = unicode.ToLower(runes[0])
				name = string(runes)
			}
			if val, exists := m[name]; exists {
				converted, err := convertArg(val, field.Type)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("struct field %s: %w", field.Name, err)
				}
				result.Field(i).Set(converted)
			}
		}
		return result, nil

	case reflect.Ptr:
		// Pointer to struct: allocate and fill
		if target.Elem().Kind() == reflect.Struct {
			inner, err := convertArg(arg, target.Elem())
			if err != nil {
				return reflect.Value{}, err
			}
			ptr := reflect.New(target.Elem())
			ptr.Elem().Set(inner)
			return ptr, nil
		}
		return reflect.Value{}, fmt.Errorf("unsupported pointer type %s", target)

	default:
		return reflect.Value{}, fmt.Errorf("unsupported parameter type %s", target)
	}
}

// toFloat64 attempts to convert an argument to float64.
// JS numbers arrive as float64 from the callback dispatcher.
func toFloat64(arg any) (float64, bool) {
	switch v := arg.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}
