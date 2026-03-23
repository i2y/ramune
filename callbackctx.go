package ramune

import "unsafe"

// CallbackContext provides safe access to JSC from within a GoFunc.
// Value methods like Attr() and Call() dispatch to the JSC goroutine,
// which deadlocks inside a callback (already on the JSC goroutine).
// CallbackContext calls JSC functions directly and returns Go values.
type CallbackContext struct {
	rt *Runtime
}

// EvalFloat64 evaluates JS code and returns the result as float64.
func (cc *CallbackContext) EvalFloat64(code string) (float64, error) {
	result, err := cc.rt.evalScriptLocked(code, "CallbackEval")
	if err != nil {
		return 0, err
	}
	return cc.rt.jsValueToNumber(cc.rt.ctx, result, 0), nil
}

// EvalString evaluates JS code and returns the result as string.
func (cc *CallbackContext) EvalString(code string) (string, error) {
	result, err := cc.rt.evalScriptLocked(code, "CallbackEval")
	if err != nil {
		return "", err
	}
	return cc.rt.jsValueToGoString(result), nil
}

// EvalBool evaluates JS code and returns the result as bool.
func (cc *CallbackContext) EvalBool(code string) (bool, error) {
	result, err := cc.rt.evalScriptLocked(code, "CallbackEval")
	if err != nil {
		return false, err
	}
	return cc.rt.jsValueToBoolean(cc.rt.ctx, result), nil
}

// Eval evaluates JS code and returns the result as any (Go value).
func (cc *CallbackContext) Eval(code string) (any, error) {
	result, err := cc.rt.evalScriptLocked(code, "CallbackEval")
	if err != nil {
		return nil, err
	}
	return cc.rt.jsToGo(result)
}

// Exec executes JavaScript code, discarding the result.
func (cc *CallbackContext) Exec(code string) error {
	_, err := cc.rt.evalScriptLocked(code, "CallbackExec")
	return err
}

// GetProperty reads a property from the global object.
func (cc *CallbackContext) GetProperty(name string) (any, error) {
	global := cc.rt.jsContextGetGlobalObject(cc.rt.ctx)
	propName := cc.rt.jsStringCreateWithUTF8CString(name)
	defer cc.rt.jsStringRelease(propName)
	var exc uintptr
	val := cc.rt.jsObjectGetProperty(cc.rt.ctx, global, propName, uintptr(unsafe.Pointer(&exc)))
	if val == 0 {
		return nil, nil
	}
	return cc.rt.jsToGo(val)
}

// SetProperty sets a property on the global object.
func (cc *CallbackContext) SetProperty(name string, value any) error {
	global := cc.rt.jsContextGetGlobalObject(cc.rt.ctx)
	propName := cc.rt.jsStringCreateWithUTF8CString(name)
	defer cc.rt.jsStringRelease(propName)
	jsVal, err := cc.rt.goToJS(value)
	if err != nil {
		return err
	}
	cc.rt.jsObjectSetProperty(cc.rt.ctx, global, propName, jsVal, 0, 0)
	return nil
}

// GoFuncWithContext is a callback that receives a CallbackContext for safe
// JSC access. Use RegisterFuncWithContext to register these.
type GoFuncWithContext func(ctx *CallbackContext, args []any) (any, error)

// RegisterFuncWithContext registers a Go function that receives a CallbackContext,
// allowing safe JSC access from within the callback without deadlocking.
func (r *Runtime) RegisterFuncWithContext(name string, fn GoFuncWithContext) error {
	cc := &CallbackContext{rt: r}
	return r.RegisterFunc(name, func(args []any) (any, error) {
		return fn(cc, args)
	})
}
