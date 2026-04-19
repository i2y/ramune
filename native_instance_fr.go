//go:build !qjswasm

package ramune

// installFinalizationRegistryHook wires the JS-side FinalizationRegistry to
// Ramune's __nativeRelease callback so JS GC of a native wrapper object
// decrements the Go-side registry. Used by the JSC and goja backends, both
// of which run FR callbacks asynchronously (never inline with a single
// allocation) so NativeInstanceCount remains observable between allocation
// and next GC cycle.
func (r *Runtime) installFinalizationRegistryHook() {
	// FinalizationRegistry is ES2021; JSC (Safari 14.1+) and goja both
	// provide it. If unavailable, instances are still cleaned up on
	// Runtime.Close().
	r.execLocked(`if(typeof FinalizationRegistry!=='undefined'){` +
		`globalThis.__nativeInstanceRegistry=new FinalizationRegistry(function(id){__nativeRelease(id)})` +
		`}`)
}
