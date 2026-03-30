package ramune

import (
	"fmt"
	"reflect"
	"strings"
)

// Module defines a custom module that can be loaded via require() in JS.
type Module struct {
	// Name is the module name used with require('name').
	Name string

	// Exports maps JS property names to Go functions.
	// Each function becomes a property on the module object.
	Exports map[string]GoFunc

	// Init is called after exports are registered, allowing
	// additional setup such as evaluating JS code.
	// The Runtime's JSC lock is held — use execLocked/evalLocked only.
	Init func(rt *Runtime) error
}

// WithModule returns an Option that registers a module during Runtime creation.
// The module is available via require('name') if NodeCompat is enabled.
func WithModule(m Module) Option {
	return func(c *config) {
		c.modules = append(c.modules, m)
	}
}

// LoadModule registers a module on an existing Runtime.
func (r *Runtime) LoadModule(m Module) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}
	var err error
	r.dispatch(func() {
		err = r.loadModuleLocked(m)
	})
	return err
}

// loadModuleLocked registers module exports as Go callbacks and creates
// the JS module object in require._modules.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) loadModuleLocked(m Module) error {
	if m.Name == "" {
		return fmt.Errorf("ramune: module name is required")
	}

	// Register each exported function with a namespaced callback name.
	prefix := "__mod_" + sanitizeModName(m.Name) + "_"
	exportNames := make([]string, 0, len(m.Exports))
	for name, fn := range m.Exports {
		goName := prefix + name
		if err := r.registerFuncLocked(goName, fn); err != nil {
			return fmt.Errorf("ramune: module %q export %q: %w", m.Name, name, err)
		}
		exportNames = append(exportNames, name+"\x00"+goName)
	}

	// Build JS that creates the module object and registers it.
	var js strings.Builder
	js.WriteString("(function(){var m={};")
	for _, pair := range exportNames {
		parts := strings.SplitN(pair, "\x00", 2)
		jsName, goName := parts[0], parts[1]
		fmt.Fprintf(&js, "m[%q]=function(){return %s.apply(null,Array.prototype.slice.call(arguments));};", jsName, goName)
	}
	// Register in require._modules if available (NodeCompat enabled).
	fmt.Fprintf(&js, "if(typeof globalThis.require==='function'&&globalThis.require._modules){")
	fmt.Fprintf(&js, "globalThis.require._modules[%q]=m;", m.Name)
	// Also register with node: prefix.
	fmt.Fprintf(&js, "globalThis.require._modules['node:'+%q]=m;", m.Name)
	js.WriteString("}")
	js.WriteString("})();")

	if err := r.execLocked(js.String()); err != nil {
		return fmt.Errorf("ramune: module %q JS registration: %w", m.Name, err)
	}

	// Call optional Init hook.
	if m.Init != nil {
		if err := m.Init(r); err != nil {
			return fmt.Errorf("ramune: module %q init: %w", m.Name, err)
		}
	}
	return nil
}

// NativeModuleFromFuncs creates a Module from a map of typed Go functions.
// Each function is automatically wrapped with reflection-based argument conversion,
// similar to Register[F]. This is used to expose transpiled Go code as a JS module.
//
// Example:
//
//	mod := ramune.NativeModuleFromFuncs("native:math", map[string]any{
//	    "fibonacci": mymath.Fibonacci,  // func(float64) float64
//	    "isPrime":   mymath.IsPrime,    // func(float64) bool
//	})
//	rt, _ := ramune.New(ramune.WithModule(mod))
//	rt.Eval(`require('native:math').fibonacci(10)`) // 55
func NativeModuleFromFuncs(name string, funcs map[string]any) Module {
	exports := make(map[string]GoFunc, len(funcs))
	for jsName, fn := range funcs {
		exports[jsName] = wrapTypedFunc(jsName, fn)
	}
	return Module{
		Name:    name,
		Exports: exports,
		Init: func(r *Runtime) error {
			r.installNativeReleaseBridge()
			return nil
		},
	}
}

// wrapTypedFunc wraps a typed Go function into a GoFunc using reflection.
// Handles argument conversion, return value processing, and panic recovery.
func wrapTypedFunc(name string, fn any) GoFunc {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		return func(args []any) (any, error) {
			return nil, fmt.Errorf("native:%s: expected function, got %s", name, fnType.Kind())
		}
	}

	numIn := fnType.NumIn()
	numOut := fnType.NumOut()
	errorInterface := reflect.TypeOf((*error)(nil)).Elem()

	paramTypes := make([]reflect.Type, numIn)
	for i := 0; i < numIn; i++ {
		paramTypes[i] = fnType.In(i)
	}

	return func(args []any) (result any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("native:%s: %v", name, r)
			}
		}()

		if len(args) < numIn {
			return nil, fmt.Errorf("native:%s: expected %d arguments, got %d", name, numIn, len(args))
		}

		in := make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			converted, convErr := convertArg(args[i], paramTypes[i])
			if convErr != nil {
				return nil, fmt.Errorf("native:%s: argument %d: %w", name, i, convErr)
			}
			in[i] = converted
		}

		out := fnVal.Call(in)

		switch numOut {
		case 0:
			return nil, nil
		case 1:
			if fnType.Out(0).Implements(errorInterface) {
				if out[0].IsNil() {
					return nil, nil
				}
				return nil, out[0].Interface().(error)
			}
			return out[0].Interface(), nil
		case 2:
			var retErr error
			if !out[1].IsNil() {
				retErr = out[1].Interface().(error)
			}
			return out[0].Interface(), retErr
		default:
			return nil, nil
		}
	}
}

// sanitizeModName converts a module name to a valid identifier fragment.
func sanitizeModName(name string) string {
	r := strings.NewReplacer("/", "_", "-", "_", ":", "_", "@", "", ".", "_")
	return r.Replace(name)
}
