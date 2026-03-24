//go:build quickjs

package ramune

import "modernc.org/quickjs"

// EvalFloat64 evaluates JS code and returns the result as float64.
func (cc *CallbackContext) EvalFloat64(code string) (float64, error) {
	result, err := cc.rt.vm.Eval(code, quickjs.EvalGlobal)
	if err != nil {
		return 0, err
	}
	switch n := result.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	default:
		return 0, nil
	}
}

// EvalString evaluates JS code and returns the result as string.
func (cc *CallbackContext) EvalString(code string) (string, error) {
	result, err := cc.rt.vm.Eval(code, quickjs.EvalGlobal)
	if err != nil {
		return "", err
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	return "", nil
}

// EvalBool evaluates JS code and returns the result as bool.
func (cc *CallbackContext) EvalBool(code string) (bool, error) {
	result, err := cc.rt.vm.Eval(code, quickjs.EvalGlobal)
	if err != nil {
		return false, err
	}
	if b, ok := result.(bool); ok {
		return b, nil
	}
	return false, nil
}

// Eval evaluates JS code and returns the result as any.
func (cc *CallbackContext) Eval(code string) (any, error) {
	return cc.rt.vm.Eval(code, quickjs.EvalGlobal)
}

// Exec executes JavaScript code, discarding the result.
func (cc *CallbackContext) Exec(code string) error {
	_, err := cc.rt.vm.Eval(code, quickjs.EvalGlobal)
	return err
}

// GetProperty reads a property from the global object.
func (cc *CallbackContext) GetProperty(name string) (any, error) {
	g := cc.rt.vm.GlobalObject()
	atom, err := cc.rt.vm.NewAtom(name)
	if err != nil {
		return nil, err
	}
	return cc.rt.vm.GetProperty(g, atom)
}

// SetProperty sets a property on the global object.
func (cc *CallbackContext) SetProperty(name string, value any) error {
	g := cc.rt.vm.GlobalObject()
	atom, err := cc.rt.vm.NewAtom(name)
	if err != nil {
		return err
	}
	return cc.rt.vm.SetProperty(g, atom, value)
}

// RegisterFuncWithContext registers a Go function that receives a CallbackContext.
func (r *Runtime) RegisterFuncWithContext(name string, fn GoFuncWithContext) error {
	cc := &CallbackContext{rt: r}
	return r.RegisterFunc(name, func(args []any) (any, error) {
		return fn(cc, args)
	})
}
