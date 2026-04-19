//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"github.com/fastschema/qjs"
)

// RegisterFunc exposes a Go function to JS as globalThis[name]. The
// adapter converts fastschema's This → our GoFunc([]any) shape and
// converts the Go return back into a *qjs.Value. JS function arguments
// come through as *JSFunc so callbacks can invoke them later.
func (r *Runtime) RegisterFunc(name string, fn GoFunc) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}
	var err error
	r.dispatch(func() {
		err = r.registerFuncLocked(name, fn)
	})
	return err
}

func (r *Runtime) registerFuncLocked(name string, fn GoFunc) error {
	r.qjsCtx.SetFunc(name, func(this *qjs.This) (*qjs.Value, error) {
		jsArgs := this.Args()
		goArgs := make([]any, len(jsArgs))
		for i, a := range jsArgs {
			goArgs[i] = jsArgToGo(r, a)
		}
		result, err := fn(goArgs)
		if err != nil {
			return nil, err
		}
		return qjs.ToJsValue(r.qjsCtx, result)
	})
	return nil
}

// RegisterFuncWithContext registers a GoFunc that receives a
// CallbackContext as its first arg.
func (r *Runtime) RegisterFuncWithContext(name string, fn GoFuncWithContext) error {
	cc := &CallbackContext{rt: r}
	return r.RegisterFunc(name, func(args []any) (any, error) {
		return fn(cc, args)
	})
}

// jsArgToGo converts a single JS argument from fastschema into a Go
// value consumable by GoFunc. Functions are wrapped in *JSFunc so Go
// callbacks can invoke them back.
func jsArgToGo(r *Runtime, a *qjs.Value) any {
	if a == nil {
		return nil
	}
	if a.IsFunction() {
		return &JSFunc{rt: r, fsv: a.Clone()}
	}
	if a.IsNull() || a.IsUndefined() {
		return nil
	}
	if a.IsBool() {
		return a.Bool()
	}
	if a.IsNumber() {
		return a.Float64()
	}
	if a.IsString() {
		return a.String()
	}
	// Objects / arrays: use JSON round-trip.
	j, err := a.JSONStringify()
	if err != nil {
		return nil
	}
	var v any
	_ = jsonUnmarshal([]byte(j), &v)
	return v
}
