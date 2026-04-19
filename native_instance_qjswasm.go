//go:build qjswasm && !goja

package ramune

// installFinalizationRegistryHook deliberately installs only a placeholder
// __nativeInstanceRegistry (so test code can still observe its presence)
// and does NOT wire FR to __nativeRelease. fastschema/qjs (the qjswasm
// engine) runs QuickJS-NG's reference-counted GC + FR callbacks inline
// with every allocation. That makes NativeInstanceCount() unobservable
// for transient wrappers — a tight `for (i=0; i<10; i++) m.newCounter(i)`
// loop would see every iteration's object released during the *next*
// iteration's allocation. Without the FR hook, JS wrappers become
// inaccessible on GC but the Go-side registry retains them until
// Runtime.Close() (or until the caller explicitly invokes
// __nativeRelease(id) from JS, which the bridge still exposes). This
// matches the documented "instances cleaned up on Runtime.Close()"
// behavior in CLAUDE.md.
func (r *Runtime) installFinalizationRegistryHook() {
	r.execLocked(`if(typeof FinalizationRegistry!=='undefined'){` +
		`globalThis.__nativeInstanceRegistry={` +
		`register:function(){},` +
		`unregister:function(){}` +
		`}` +
		`}`)
}
