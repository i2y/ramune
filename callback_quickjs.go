//go:build quickjs

package ramune

import (
	"encoding/json"
	"fmt"

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
	// Objects are kept as-is in the args array so Go receives proper types.
	code := `globalThis[` + jsQuoteName(name) + `] = function() {
		var a = [];
		for (var i = 0; i < arguments.length; i++) {
			a.push(arguments[i]);
		}
		var r = __goDispatch(` + itoa(id) + `, JSON.stringify(a));
		var p = JSON.parse(r);
		if (p.e) throw p.e;
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
			goArgs[i] = v
		}

		result, err := r.goFuncs[id](goArgs)
		if err != nil {
			b, _ := json.Marshal(map[string]any{"e": err.Error()})
			return string(b)
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
