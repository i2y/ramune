//go:build qjswasm && !quickjs && !goja

package ramune

// vmManager holds the per-Runtime state for the Node-style vm module.
// The type is carried for cross-backend Runtime shape parity; creating
// isolated contexts (separate JSContext per "sandbox") is not yet
// implemented on qjswasm.
type vmManager struct {
	names map[string]struct{}
}

func newVMManager() *vmManager {
	return &vmManager{names: map[string]struct{}{}}
}

// installVM exposes a stub that throws on use. Per-context isolation
// (one JSContext per vm.Context) is not yet ported.
func (r *Runtime) installVM() error {
	r.vmMgr = newVMManager()
	return r.execLocked(`
		globalThis.__ramune_vm_unsupported = function() {
			throw new Error("vm module not yet implemented on qjswasm backend");
		};
	`)
}
