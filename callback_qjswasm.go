//go:build qjswasm && !goja

package ramune

import (
	"encoding/json"

	"github.com/fastschema/qjs"
)

// RegisterFunc exposes a Go function to JS as globalThis[name].
//
// We can't use fastschema's SetFunc directly because its error-path throws
// `new Error(msg)`, which stringifies as "Error: <msg>" — the other
// Ramune backends (JSC/QuickJS/goja) propagate errors whose toString() is
// the raw message so `"" + e` yields just "<msg>". To match, we register
// an internal dispatch function via SetFunc that returns the result in a
// normal (non-throw) path using a {__ramuneError, msg} envelope for
// errors, then install a short JS wrapper under `name` that parses the
// envelope and throws an Error with toString overridden.
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
	internalName := "__ramuneGo_" + sanitizeJSIdent(name)

	r.qjsCtx.SetFunc(internalName, func(this *qjs.This) (*qjs.Value, error) {
		jsArgs := this.Args()
		goArgs := make([]any, len(jsArgs))
		for i, a := range jsArgs {
			goArgs[i] = jsArgToGo(r, a)
		}
		result, callErr := fn(goArgs)
		if callErr != nil {
			// Build the error envelope as a JS object so the JS wrapper can
			// detect and re-throw with a custom toString. Returning the
			// envelope *through the normal value path* avoids fastschema's
			// ThrowError wrapping (which would prepend "Error: ").
			return r.makeErrorEnvelopeLocked(callErr.Error()), nil
		}
		jsVal, convErr := r.goToJSLocked(result)
		if convErr != nil {
			return r.makeErrorEnvelopeLocked(convErr.Error()), nil
		}
		return jsVal, nil
	})

	// Install the JS wrapper under the user-visible name. It calls the
	// internal dispatch, checks for the __ramuneError envelope, and throws
	// an Error whose toString() returns the bare message (matching the
	// JSC/QuickJS/goja backends).
	wrapperCode := "globalThis[" + jsQuoteName(name) + "]=function(){" +
		"var r=" + internalName + ".apply(null,arguments);" +
		"if(r&&typeof r==='object'&&r.__ramuneError===true){" +
		"var m=r.msg;" +
		"var e=new Error(m);" +
		"e.toString=function(){return m};" +
		"throw e;" +
		"}" +
		"return r;" +
		"};"
	if err := r.execLocked(wrapperCode); err != nil {
		return &JSError{Context: "registerFunc wrapper " + name, Message: err.Error()}
	}
	return nil
}

// makeErrorEnvelopeLocked creates a {__ramuneError:true, msg:"..."} JS
// object. The JS wrapper in registerFuncLocked converts it into a thrown
// Error with an overridden toString.
func (r *Runtime) makeErrorEnvelopeLocked(msg string) *qjs.Value {
	obj := r.qjsCtx.NewObject()
	obj.SetPropertyStr("__ramuneError", r.qjsCtx.NewBool(true))
	obj.SetPropertyStr("msg", r.qjsCtx.NewString(msg))
	return obj
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
	_ = json.Unmarshal([]byte(j), &v)
	return v
}

// sanitizeJSIdent returns a JS-identifier-safe form of s (replacing any
// non-alphanumeric character with `_`). Only used to derive the internal
// dispatch name; callers should still quote `name` itself when writing
// JS code that references it.
func sanitizeJSIdent(s string) string {
	if s == "" {
		return "_"
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			b = append(b, c)
			continue
		}
		b = append(b, '_')
	}
	if c := b[0]; c >= '0' && c <= '9' {
		b = append([]byte{'_'}, b...)
	}
	return string(b)
}
