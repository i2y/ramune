//go:build !quickjs && !goja

package ramune

import (
	"fmt"
	"runtime"
	"unsafe"
)

// vmManager manages isolated JS contexts for the vm module.
// All methods are called on the dedicated JSC goroutine (no mutex needed).
type vmManager struct {
	contexts map[int]uintptr // context ID -> JSGlobalContextRef
	nextID   int
}

func newVMManager() *vmManager {
	return &vmManager{
		contexts: make(map[int]uintptr),
		nextID:   1,
	}
}

// closeAll releases all VM contexts. Must be called on the JSC goroutine.
func (vm *vmManager) closeAll(r *Runtime) {
	for _, ctx := range vm.contexts {
		r.jsGlobalContextRelease(ctx)
	}
	vm.contexts = make(map[int]uintptr)
}

// installVM sets up the vm module (createContext, runInContext, Script).
func (r *Runtime) installVM() error {
	mgr := newVMManager()
	r.vmMgr = mgr

	if err := r.registerFuncLocked("__go_vm_create_context", func(args []any) (any, error) {
		var ctx uintptr
		if runtime.GOOS == "darwin" && r.group != 0 {
			ctx = r.jsGlobalContextCreateInGroup(r.group, 0)
		} else {
			ctx = r.jsGlobalContextCreate(0)
		}
		if ctx == 0 {
			return nil, fmt.Errorf("vm: failed to create context")
		}

		id := mgr.nextID
		mgr.nextID++
		mgr.contexts[id] = ctx

		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_vm_run_in_context", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("vm.runInContext: code and contextId required")
		}
		code, _ := args[0].(string)
		ctxID := int(args[1].(float64))

		ctx, ok := mgr.contexts[ctxID]
		if !ok {
			return nil, fmt.Errorf("vm.runInContext: invalid context %d", ctxID)
		}

		// Evaluate in the isolated context.
		jsStr := r.jsStringCreateWithUTF8CString(code)
		defer r.jsStringRelease(jsStr)

		var exc uintptr
		result := r.jsEvaluateScript(ctx, jsStr, 0, 0, 0, uintptr(unsafe.Pointer(&exc)))
		if result == 0 {
			if exc != 0 {
				// Extract error from the isolated context.
				excStr := r.jsValueToStringCopy(ctx, exc, 0)
				if excStr != 0 {
					msg := r.jsStringToGo(excStr)
					r.jsStringRelease(excStr)
					return nil, &JSError{Context: "vm.runInContext", Message: msg}
				}
				return nil, &JSError{Context: "vm.runInContext", Message: "JavaScript exception occurred"}
			}
			return nil, &JSError{Context: "vm.runInContext", Message: "JavaScript exception occurred"}
		}

		// Convert result from the isolated context.
		return r.jsValueToGoFromContext(ctx, result), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_vm_close_context", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("vm: contextId required")
		}
		ctxID := int(args[0].(float64))

		ctx, ok := mgr.contexts[ctxID]
		if ok {
			delete(mgr.contexts, ctxID)
		}

		if ok {
			r.jsGlobalContextRelease(ctx)
		}
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(vmJSSource())
}

// jsValueToGoFromContext converts a JSValueRef from an arbitrary context to a Go value.
// JSC type constants: 0=undefined, 1=null, 2=boolean, 3=number, 4=string, 5=object, 6=symbol
func (r *Runtime) jsValueToGoFromContext(ctx, val uintptr) any {
	jsType := r.jsValueGetType(ctx, val)
	switch jsType {
	case 2: // kJSTypeBoolean
		return r.jsValueToBoolean(ctx, val)
	case 3: // kJSTypeNumber
		return r.jsValueToNumber(ctx, val, 0)
	case 4: // kJSTypeString
		jsStr := r.jsValueToStringCopy(ctx, val, 0)
		if jsStr == 0 {
			return ""
		}
		defer r.jsStringRelease(jsStr)
		return r.jsStringToGo(jsStr)
	case 5: // kJSTypeObject — JSON.stringify via toString of JSON.stringify result
		// Evaluate JSON.stringify(result) in the isolated context to extract objects.
		return r.jsonStringifyInContext(ctx, val)
	default: // 0=undefined, 1=null, 6=symbol
		return nil
	}
}

// jsonStringifyInContext evaluates JSON.stringify on a value in an arbitrary context.
func (r *Runtime) jsonStringifyInContext(ctx, val uintptr) string {
	// Get JSON.stringify: evaluate "JSON.stringify" to get the function, then call it.
	// Simpler approach: convert the value to string via JSValueToStringCopy which calls toString().
	// For objects this gives "[object Object]". Instead, we evaluate a small script.
	script := r.jsStringCreateWithUTF8CString("JSON")
	defer r.jsStringRelease(script)
	var exc uintptr
	jsonObj := r.jsEvaluateScript(ctx, script, 0, 0, 0, uintptr(unsafe.Pointer(&exc)))
	if jsonObj == 0 {
		return ""
	}
	strName := r.jsStringCreateWithUTF8CString("stringify")
	defer r.jsStringRelease(strName)
	stringifyFn := r.jsObjectGetProperty(ctx, jsonObj, strName, 0)
	if stringifyFn == 0 {
		return ""
	}
	args := []uintptr{val}
	result := r.jsObjectCallAsFunction(ctx, stringifyFn, 0, 1, args, 0)
	if result == 0 {
		return ""
	}
	jsStr := r.jsValueToStringCopy(ctx, result, 0)
	if jsStr == 0 {
		return ""
	}
	defer r.jsStringRelease(jsStr)
	return r.jsStringToGo(jsStr)
}

func vmJSSource() string {
	return `
(function() {
	var vm = {
		createContext: function(sandbox) {
			var id = __go_vm_create_context();
			return { _vmContextId: id, _sandbox: sandbox || {} };
		},

		runInContext: function(code, context, opts) {
			if (!context || !context._vmContextId) {
				throw new TypeError('vm.runInContext: invalid context (use vm.createContext())');
			}
			var result = __go_vm_run_in_context(String(code), context._vmContextId);
			if (typeof result === 'string') {
				try { var parsed = JSON.parse(result); if (typeof parsed === 'object' && parsed !== null) return parsed; } catch(e) {}
			}
			return result;
		},

		runInNewContext: function(code, sandbox, opts) {
			var ctx = vm.createContext(sandbox);
			try {
				return vm.runInContext(code, ctx, opts);
			} finally {
				__go_vm_close_context(ctx._vmContextId);
			}
		},

		Script: function(code) {
			this._code = code;
		}
	};

	vm.Script.prototype.runInContext = function(context, opts) {
		return vm.runInContext(this._code, context, opts);
	};
	vm.Script.prototype.runInNewContext = function(sandbox, opts) {
		return vm.runInNewContext(this._code, sandbox, opts);
	};

	if (globalThis.require && globalThis.require._modules) {
		globalThis.require._modules['vm'] = vm;
	}
})();
`
}
