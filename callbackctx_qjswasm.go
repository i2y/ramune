//go:build qjswasm && !quickjs && !goja

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
	h, err := cc.rt.rawEvalLocked(code, "<cc.eval>", 0)
	if err != nil {
		return nil, err
	}
	if isExceptionHandle(h) {
		return nil, cc.rt.pullExceptionLocked()
	}
	defer cc.rt.freeValueLocked(h)
	return cc.rt.jsToGoLocked(h)
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
	return cc.Eval("globalThis[" + jsQuoteName(name) + "]")
}

// SetProperty sets a property on the global object.
func (cc *CallbackContext) SetProperty(name string, value any) error {
	if cc.rt == nil || cc.rt.closed.Load() {
		return ErrAlreadyClosed
	}
	handle, err := cc.rt.goToJSLocked(value)
	if err != nil {
		return err
	}
	// globalThis[name] = value; we do this via val_call on a helper
	// function so we don't have to serialize the value through JSON.
	fnCode := "(function(n,v){globalThis[n]=v;})"
	fnH, err := cc.rt.rawEvalLocked(fnCode, "<setprop>", 0)
	if err != nil {
		return err
	}
	defer cc.rt.freeValueLocked(fnH)
	nameH, err := cc.rt.newStringLocked(name)
	if err != nil {
		return err
	}
	defer cc.rt.freeValueLocked(nameH)
	// Call with args [name, value]. Use the handle-aware call path.
	_, err = cc.rt.callFunctionLocked(fnH, 0, []uint64{nameH, handle})
	return err
}
