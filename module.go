package ramune

import (
	"fmt"
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

// sanitizeModName converts a module name to a valid identifier fragment.
func sanitizeModName(name string) string {
	r := strings.NewReplacer("/", "_", "-", "_", ":", "_", "@", "", ".", "_")
	return r.Replace(name)
}
