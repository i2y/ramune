//go:build qjswasm && !goja

package ramune

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/i2y/ramune/third_party/qjs"
)

// goToJSPublic is the exported-to-package entry for Go→JS conversion.
// Wraps goToJSLocked with a dispatch to the engine goroutine.
func (r *Runtime) goToJSPublic(v any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var out *Value
	var err error
	r.dispatch(func() {
		fv, e := r.goToJSLocked(v)
		if e != nil {
			err = e
			return
		}
		out = r.wrapValue(fv)
	})
	return out, err
}

// goToJSLocked converts a Go value to a *qjs.Value on the engine goroutine.
// We handle every shape ourselves rather than delegating to qjs.ToJsValue
// because fastschema's default conversion attaches __go_type / __registry_id
// metadata to every object (including plain map/slice results) which leaks
// through JSON.stringify and Object.keys, breaking Ramune's value semantics.
func (r *Runtime) goToJSLocked(v any) (*qjs.Value, error) {
	switch x := v.(type) {
	case nil:
		return r.qjsCtx.NewNull(), nil
	case *Value:
		if x == nil || x.rt != r || x.fsv == nil {
			return r.qjsCtx.NewUndefined(), nil
		}
		// Clone so the caller's ownership contract (returned *qjs.Value is
		// freshly owned) is preserved.
		return x.fsv.Clone(), nil
	case *JSFunc:
		if x == nil || x.fsv == nil {
			return r.qjsCtx.NewUndefined(), nil
		}
		return x.fsv.Clone(), nil
	case bool:
		return r.qjsCtx.NewBool(x), nil
	case int:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case int8:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case int16:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case int32:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case int64:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case uint:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case uint8:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case uint16:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case uint32:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case uint64:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case float32:
		return r.qjsCtx.NewFloat64(float64(x)), nil
	case float64:
		return r.qjsCtx.NewFloat64(x), nil
	case string:
		return r.qjsCtx.NewString(x), nil
	case []byte:
		return r.newUint8ArrayLocked(x)
	case map[string]any:
		return r.jsonToJSLocked(x)
	case []any:
		return r.jsonToJSLocked(x)
	}

	rv := reflect.ValueOf(v)
	origVal := rv

	// Promise (has Await() method) — convert to JS Promise lazily.
	if rv.Kind() == reflect.Ptr && rv.MethodByName("Await").IsValid() {
		return r.promiseToJSLocked(rv)
	}

	// Arbitrary slices via reflection (e.g. []int, []string).
	if rv.Kind() == reflect.Slice {
		elems := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			elems[i] = rv.Index(i).Interface()
		}
		return r.jsonToJSLocked(elems)
	}

	// Arbitrary maps with string keys via reflection (e.g. map[string]int).
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		m := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			m[iter.Key().String()] = iter.Value().Interface()
		}
		return r.jsonToJSLocked(m)
	}

	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Struct {
		return r.structToJSObjectQJSWasm(origVal, rv)
	}

	return nil, fmt.Errorf("ramune: unsupported Go type %T", v)
}

// jsonToJSLocked round-trips a Go value through JSON and parses it as a
// fresh JS object. This preserves nested structure without attaching any
// bookkeeping properties.
func (r *Runtime) jsonToJSLocked(v any) (*qjs.Value, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("ramune: goToJS json.Marshal: %w", err)
	}
	return r.qjsCtx.ParseJSON(string(data)), nil
}

// newUint8ArrayLocked creates a JS Uint8Array from a []byte. qjsCtx.NewBytes
// returns an opaque WASM handle (not a typed array), so we allocate an
// ArrayBuffer and wrap it with `new Uint8Array(buffer)`. The Uint8Array
// constructor is cached on the Runtime.
func (r *Runtime) newUint8ArrayLocked(b []byte) (*qjs.Value, error) {
	if b == nil {
		return r.qjsCtx.NewNull(), nil
	}
	ab := r.qjsCtx.NewArrayBuffer(b)
	out := r.uint8ArrayCtor.New(ab)
	ab.Free()
	return out, nil
}

// promiseToJSLocked converts a Go *promise.Promise[T] to a JS Promise.
//
// The bridge stores the new Promise on a globalThis slot rather than
// returning it from the eval directly: fastschema/qjs's QJS_Eval calls
// `js_std_await` whenever the script's top-level value is a thenable,
// which would block the wasm goroutine until the Promise resolves. But
// the resolver fires from a *different* goroutine via dispatch, so that
// goroutine can never reach the wasm side - classic deadlock. Using
// execLocked (which appends `;undefined;`) sidesteps `js_std_await`
// entirely, and we then pluck the Promise off globalThis.
func (r *Runtime) promiseToJSLocked(rv reflect.Value) (*qjs.Value, error) {
	r.nativeMethodSeq++
	seq := r.nativeMethodSeq
	resolveName := fmt.Sprintf("__promise_resolve_%d", seq)
	rejectName := fmt.Sprintf("__promise_reject_%d", seq)
	slotName := fmt.Sprintf("__promise_slot_%d", seq)

	setupCode := fmt.Sprintf(
		`globalThis.%s=new Promise(function(resolve,reject){globalThis.%s=function(v){resolve(v)};globalThis.%s=function(e){reject(e)}});`,
		slotName, resolveName, rejectName,
	)
	if err := r.execLocked(setupCode); err != nil {
		return nil, fmt.Errorf("promiseToJSQJSWasm setup: %w", err)
	}
	global := r.qjsCtx.Global()
	jsPromise := global.GetPropertyStr(slotName)
	if jsPromise == nil {
		return nil, fmt.Errorf("promiseToJSQJSWasm: bridge promise slot empty")
	}
	// Clear the slot once we have a handle so it doesn't leak across calls.
	// JS_GetProperty bumps the refcount, so the Promise stays alive on the
	// C side via our handle even after the global property is deleted.
	if err := r.execLocked(fmt.Sprintf("delete globalThis.%s;", slotName)); err != nil {
		return nil, fmt.Errorf("promiseToJSQJSWasm slot cleanup: %w", err)
	}

	awaitMethod := rv.MethodByName("Await")

	go func() {
		results := awaitMethod.Call(nil)
		r.dispatch(func() {
			if len(results) >= 2 && !results[1].IsNil() {
				errMsg := results[1].Interface().(error).Error()
				msgLit, _ := json.Marshal(errMsg)
				r.execLocked(fmt.Sprintf("globalThis.%s(new Error(%s));delete globalThis.%s;delete globalThis.%s;",
					rejectName, string(msgLit), resolveName, rejectName))
				return
			}
			if len(results) >= 1 {
				val := results[0].Interface()
				jsVal, convErr := r.goToJSLocked(val)
				if convErr != nil {
					msgLit, _ := json.Marshal(convErr.Error())
					r.execLocked(fmt.Sprintf("globalThis.%s(new Error(%s));delete globalThis.%s;delete globalThis.%s;",
						rejectName, string(msgLit), resolveName, rejectName))
					return
				}
				tmp := fmt.Sprintf("__pval_%d", seq)
				global := r.qjsCtx.Global()
				global.SetPropertyStr(tmp, jsVal)
				r.execLocked(fmt.Sprintf("globalThis.%s(globalThis.%s);delete globalThis.%s;delete globalThis.%s;delete globalThis.%s;",
					resolveName, tmp, resolveName, rejectName, tmp))
			}
		})
		r.Wake()
	}()

	return jsPromise, nil
}

// structToJSObjectQJSWasm creates a JS object with live getter/setter
// properties and methods for a Go struct value. Mirrors
// structToJSObjectQJS / structToJSObject in the other backends.
func (r *Runtime) structToJSObjectQJSWasm(origVal, rv reflect.Value) (*qjs.Value, error) {
	methodTarget := origVal
	if methodTarget.Kind() != reflect.Ptr {
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		methodTarget = ptr
	}

	r.ensureNativeReg()
	info := r.nativeReg.ensureTypeRegistered(r, rv.Type())
	instanceID := r.nativeReg.registerInstance(methodTarget)
	jsCode := info.generateJSObject(instanceID)

	out, err := r.evalScriptLocked(jsCode, "native-struct")
	if err != nil {
		r.nativeReg.releaseInstance(instanceID)
		return nil, fmt.Errorf("structToJSObjectQJSWasm: %w", err)
	}
	return out.fsv, nil
}

// jsToGoLocked converts a *qjs.Value into a plain Go value. Delegates to
// the helpers in callback_qjswasm.go via jsArgToGo for consistency with
// callback argument marshaling.
func (r *Runtime) jsToGoLocked(v *qjs.Value) (any, error) {
	return jsArgToGo(r, v), nil
}
