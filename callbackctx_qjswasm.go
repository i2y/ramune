//go:build qjswasm && !goja

package ramune

// EvalFloat64 evaluates JS code and returns the result as float64.
func (cc *CallbackContext) EvalFloat64(code string) (float64, error) {
	if cc.rt == nil || cc.rt.closed.Load() {
		return 0, ErrAlreadyClosed
	}
	v, err := cc.rt.evalLocked(code)
	if err != nil {
		return 0, err
	}
	defer v.Close()
	return v.Float64()
}

// EvalString evaluates JS code and returns the result as string.
func (cc *CallbackContext) EvalString(code string) (string, error) {
	if cc.rt == nil || cc.rt.closed.Load() {
		return "", ErrAlreadyClosed
	}
	v, err := cc.rt.evalLocked(code)
	if err != nil {
		return "", err
	}
	defer v.Close()
	return v.GoString()
}

// EvalBool evaluates JS code and returns the result as bool.
func (cc *CallbackContext) EvalBool(code string) (bool, error) {
	if cc.rt == nil || cc.rt.closed.Load() {
		return false, ErrAlreadyClosed
	}
	v, err := cc.rt.evalLocked(code)
	if err != nil {
		return false, err
	}
	defer v.Close()
	return v.Bool()
}

// Eval evaluates JS code and returns the result as any.
func (cc *CallbackContext) Eval(code string) (any, error) {
	if cc.rt == nil || cc.rt.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	v, err := cc.rt.evalLocked(code)
	if err != nil {
		return nil, err
	}
	defer v.Close()
	return cc.rt.jsToGoLocked(v.fsv)
}

// Exec executes JS code, discarding the result.
func (cc *CallbackContext) Exec(code string) error {
	if cc.rt == nil || cc.rt.closed.Load() {
		return ErrAlreadyClosed
	}
	return cc.rt.execLocked(code)
}

// GetProperty reads a property from the global object.
func (cc *CallbackContext) GetProperty(name string) (any, error) {
	if cc.rt == nil || cc.rt.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	g := cc.rt.qjsCtx.Global()
	defer g.Free()
	p := g.GetPropertyStr(name)
	if p == nil {
		return nil, nil
	}
	defer p.Free()
	return cc.rt.jsToGoLocked(p)
}

// SetProperty sets a property on the global object.
func (cc *CallbackContext) SetProperty(name string, value any) error {
	if cc.rt == nil || cc.rt.closed.Load() {
		return ErrAlreadyClosed
	}
	jv, err := cc.rt.goToJSLocked(value)
	if err != nil {
		return err
	}
	g := cc.rt.qjsCtx.Global()
	defer g.Free()
	g.SetPropertyStr(name, jv)
	return nil
}
