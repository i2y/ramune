//go:build goja

package ramune

import "github.com/dop251/goja"

// EvalFloat64 evaluates JS code and returns the result as float64.
func (cc *CallbackContext) EvalFloat64(code string) (float64, error) {
	result, err := cc.rt.vm.RunString(code)
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	return result.ToFloat(), nil
}

// EvalString evaluates JS code and returns the result as string.
func (cc *CallbackContext) EvalString(code string) (string, error) {
	result, err := cc.rt.vm.RunString(code)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.String(), nil
}

// EvalBool evaluates JS code and returns the result as bool.
func (cc *CallbackContext) EvalBool(code string) (bool, error) {
	result, err := cc.rt.vm.RunString(code)
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}
	return result.ToBoolean(), nil
}

// Eval evaluates JS code and returns the result as any (Go value).
func (cc *CallbackContext) Eval(code string) (any, error) {
	result, err := cc.rt.vm.RunString(code)
	if err != nil {
		return nil, err
	}
	if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
		return nil, nil
	}
	return result.Export(), nil
}

// Exec executes JavaScript code, discarding the result.
func (cc *CallbackContext) Exec(code string) error {
	_, err := cc.rt.vm.RunString(code)
	return err
}

// GetProperty reads a property from the global object.
func (cc *CallbackContext) GetProperty(name string) (any, error) {
	v := cc.rt.vm.Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	return v.Export(), nil
}

// SetProperty sets a property on the global object.
func (cc *CallbackContext) SetProperty(name string, value any) error {
	jsVal, err := cc.rt.goToJS(value)
	if err != nil {
		return err
	}
	return cc.rt.vm.Set(name, jsVal)
}

// RegisterFuncWithContext registers a Go function that receives a CallbackContext.
func (r *Runtime) RegisterFuncWithContext(name string, fn GoFuncWithContext) error {
	cc := &CallbackContext{rt: r}
	return r.RegisterFunc(name, func(args []any) (any, error) {
		return fn(cc, args)
	})
}
