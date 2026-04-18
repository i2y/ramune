//go:build qjswasm && !quickjs && !goja

package ramune

// vmManager holds the per-Runtime state for the Node-style vm module.
// On qjswasm, additional "contexts" in the future will map to fresh
// JSContexts inside the same JSRuntime. For M1-M7 we carry the type so
// the shared Runtime struct shape is preserved, even though
// vm.createContext currently just throws from JS.
type vmManager struct {
	// names is a dummy registry so vm.createContext can hand out handles.
	names map[string]struct{}
}

func newVMManager() *vmManager {
	return &vmManager{names: map[string]struct{}{}}
}

// installVM wires the Node-style vm module. For M1-M7 this exposes
// stubs that throw when the user actually tries to use them; full
// per-context isolation lands in M8 together with worker_threads.
func (r *Runtime) installVM() error {
	r.vmMgr = newVMManager()
	return r.execLocked(`
		globalThis.__ramune_vm_unsupported = function() {
			throw new Error("vm module not yet implemented on qjswasm backend");
		};
	`)
}
