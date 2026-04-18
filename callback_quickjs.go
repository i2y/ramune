//go:build quickjs && !goja

package ramune

import (
	"encoding/json"
	"fmt"
	"reflect"

	"modernc.org/quickjs"
)

// RegisterFunc registers a Go function as a global JavaScript function.
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

// registerFuncLocked registers a Go function on the engine goroutine.
// Uses a dispatcher pattern: register a raw __goN function that returns
// JSON-encoded {r: result, e: "error"}, then wrap it in a JS function
// that parses the result and throws on error.
func (r *Runtime) registerFuncLocked(name string, fn GoFunc) error {
	id := len(r.goFuncs)
	r.goFuncs = append(r.goFuncs, fn)

	// Ensure the dispatcher is registered.
	if id == 0 {
		if err := r.ensureDispatcher(); err != nil {
			return err
		}
	}

	// Create a JS wrapper that calls __goDispatch(id, args) and handles errors.
	// Functions are stored as temp globals with __jsfunc_ref markers.
	code := `globalThis[` + jsQuoteName(name) + `] = function() {
		var a = [];
		for (var i = 0; i < arguments.length; i++) {
			if (typeof arguments[i] === 'function') {
				var ref = '__jsfunc_' + (globalThis.__jsfuncSeq = (globalThis.__jsfuncSeq || 0) + 1);
				globalThis[ref] = arguments[i];
				a.push({"__jsfunc_ref": ref});
			} else {
				a.push(arguments[i]);
			}
		}
		var r = __goDispatch(` + itoa(id) + `, JSON.stringify(a));
		var p = JSON.parse(r);
		if (p.e) throw p.e;
		if (p.__native_ref) {
			var obj = globalThis[p.__native_ref];
			delete globalThis[p.__native_ref];
			return obj;
		}
		return p.r;
	};`

	if err := r.execLocked(code); err != nil {
		return fmt.Errorf("registerFunc wrapper %s: %w", name, err)
	}
	return nil
}

// ensureDispatcher registers the single __goDispatch native function.
func (r *Runtime) ensureDispatcher() error {
	dispatch := func(id int, argsJSON string) string {
		if id < 0 || id >= len(r.goFuncs) {
			b, _ := json.Marshal(map[string]any{"e": "invalid function id"})
			return string(b)
		}

		// Parse args from JSON.
		var rawArgs []json.RawMessage
		json.Unmarshal([]byte(argsJSON), &rawArgs)

		goArgs := make([]any, len(rawArgs))
		for i, raw := range rawArgs {
			var v any
			json.Unmarshal(raw, &v)
			// Check for JSFunc reference marker.
			if m, ok := v.(map[string]any); ok {
				if refName, ok := m["__jsfunc_ref"].(string); ok {
					goArgs[i] = &JSFunc{rt: r, refName: refName}
					continue
				}
			}
			goArgs[i] = v
		}

		result, err := r.goFuncs[id](goArgs)
		if err != nil {
			b, _ := json.Marshal(map[string]any{"e": err.Error()})
			return string(b)
		}

		// Check if result is a struct/pointer-to-struct — needs special handling
		// because JSON serialization loses methods and live bindings.
		if result != nil {
			rv := reflect.ValueOf(result)
			isStruct := (rv.Kind() == reflect.Ptr && rv.Elem().Kind() == reflect.Struct) || rv.Kind() == reflect.Struct
			// Also check for Promise (has Await method)
			isPromise := rv.Kind() == reflect.Ptr && rv.MethodByName("Await").IsValid()
			if isStruct || isPromise {
				// Use goToJS for live bindings/promises, store in temp global
				r.nativeMethodSeq++
				tmpName := fmt.Sprintf("__qjs_ret_%d", r.nativeMethodSeq)
				jsVal, convErr := r.goToJS(result)
				if convErr == nil {
					global := r.vm.GlobalObject()
					atom, _ := r.vm.NewAtom(tmpName)
					global.SetPropertyValue(atom, jsVal)
					global.Free()
					b, _ := json.Marshal(map[string]any{"__native_ref": tmpName})
					return string(b)
				}
			}
		}

		b, err := json.Marshal(map[string]any{"r": result})
		if err != nil {
			b, _ = json.Marshal(map[string]any{"e": "marshal error: " + err.Error()})
			return string(b)
		}
		return string(b)
	}

	if err := r.vm.RegisterFunc("__goDispatch", dispatch, false); err != nil {
		return fmt.Errorf("registerFunc __goDispatch: %w", err)
	}
	return nil
}

// jsQuoteName returns a JSON-encoded string for safe JS embedding.
func jsQuoteName(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Ensure quickjs package is used.
var _ = quickjs.EvalGlobal
