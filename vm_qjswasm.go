//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"fmt"

	"github.com/fastschema/qjs"
)

// vmManager holds the per-Runtime state for the Node-style vm module.
// Each vm.Context maps to its own fastschema/qjs.Runtime — which is a
// fresh QuickJS-NG wasm instance (its globals are isolated from the
// parent and from other contexts). fastschema caches the compiled wasm
// module at the package level, so spawning extra contexts is cheap.
type vmManager struct {
	contexts map[string]*qjs.Runtime
}

func newVMManager() *vmManager {
	return &vmManager{contexts: make(map[string]*qjs.Runtime)}
}

func (r *Runtime) installVM() error {
	r.vmMgr = newVMManager()

	if err := r.registerFuncLocked("__go_vm_create_context", func(args []any) (any, error) {
		name := "default"
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				name = s
			}
		}
		sub, err := qjs.New()
		if err != nil {
			return nil, fmt.Errorf("vm.createContext: %w", err)
		}
		r.vmMgr.contexts[name] = sub
		return name, nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_vm_run_in_context", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("vm.runInContext: code and contextName required")
		}
		code, _ := args[0].(string)
		name, _ := args[1].(string)
		sub, ok := r.vmMgr.contexts[name]
		if !ok {
			return nil, fmt.Errorf("vm.runInContext: context %q not found", name)
		}
		result, err := sub.Context().Eval("<vm>", qjs.Code(code))
		if err != nil {
			return nil, err
		}
		defer result.Free()
		if result.IsNull() || result.IsUndefined() {
			return nil, nil
		}
		if result.IsBool() {
			return result.Bool(), nil
		}
		if result.IsNumber() {
			return result.Float64(), nil
		}
		if result.IsString() {
			return result.String(), nil
		}
		// Composite result: round-trip via JSON.
		j, err := result.JSONStringify()
		if err != nil {
			return nil, err
		}
		return j, nil
	}); err != nil {
		return err
	}

	return r.execLocked(vmJSSource())
}

func vmJSSource() string {
	return `
(function() {
	var vm = {};
	vm.createContext = function(sandbox) {
		var name = '__ctx_' + Date.now() + '_' + Math.random();
		__go_vm_create_context(name);
		return name;
	};
	vm.runInContext = function(code, contextName) {
		return __go_vm_run_in_context(code, contextName);
	};
	vm.runInNewContext = function(code, sandbox) {
		var ctx = vm.createContext(sandbox);
		return vm.runInContext(code, ctx);
	};
	vm.Script = function(code) { this.code = code; };
	vm.Script.prototype.runInContext = function(ctx) { return vm.runInContext(this.code, ctx); };
	vm.Script.prototype.runInNewContext = function(sandbox) { return vm.runInNewContext(this.code, sandbox); };
	if (globalThis.require && globalThis.require._modules) {
		globalThis.require._modules['vm'] = vm;
		globalThis.require._modules['node:vm'] = vm;
	}
})();
`
}
