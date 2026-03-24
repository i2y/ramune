//go:build quickjs

package ramune

import (
	"fmt"

	"modernc.org/quickjs"
)

// goToJS converts a Go value to a QuickJS value.
func (r *Runtime) goToJS(v any) (quickjs.Value, error) {
	if v == nil {
		result, err := r.vm.EvalValue("null", quickjs.EvalGlobal)
		return result, err
	}
	switch val := v.(type) {
	case bool:
		if val {
			r, err := r.vm.EvalValue("true", quickjs.EvalGlobal)
			return r, err
		}
		r, err := r.vm.EvalValue("false", quickjs.EvalGlobal)
		return r, err
	case int:
		return r.vm.NewInt(val), nil
	case int64:
		return r.vm.NewInt(int(val)), nil
	case float64:
		return r.vm.NewFloat64(val), nil
	case string:
		return r.vm.NewString(val)
	case *Value:
		if val == nil {
			r, err := r.vm.EvalValue("undefined", quickjs.EvalGlobal)
			return r, err
		}
		return val.val.Dup(), nil
	default:
		return quickjs.Value{}, fmt.Errorf("unsupported type: %T", v)
	}
}

// jsToGo converts a QuickJS value to a Go value.
func (r *Runtime) jsToGo(v quickjs.Value) (any, error) {
	return v.Any()
}
