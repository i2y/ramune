//go:build goja

package ramune

import (
	"fmt"

	"github.com/dop251/goja"
)

// vmManager manages isolated goja runtimes for the vm module.
type vmManager struct {
	contexts map[string]*goja.Runtime
}

func newVMManager() *vmManager {
	return &vmManager{contexts: make(map[string]*goja.Runtime)}
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
		vm := goja.New()
		r.vmMgr.contexts[name] = vm
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
		vm, ok := r.vmMgr.contexts[name]
		if !ok {
			return nil, fmt.Errorf("vm.runInContext: context %q not found", name)
		}
		result, err := vm.RunString(code)
		if err != nil {
			return nil, err
		}
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, nil
		}
		return result.Export(), nil
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
