//go:build darwin && !quickjs && !goja && !qjswasm

package ramune

// configureJSCSignal is a no-op on macOS.
// macOS JSC (system framework) does not use SIGUSR1 for GC.
func configureJSCSignal(handle uintptr) {}
