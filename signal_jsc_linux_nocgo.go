//go:build linux && !cgo && !quickjs && !goja

package ramune

import (
	"os/signal"
	"syscall"
)

func init() {
	// Without cgo, Go's runtime intercepts SIGUSR1 sent by JSC's GC
	// via pthread_kill, causing SEGV in multi-runtime configurations.
	// Release SIGUSR1 to let JSC install its handler. This allows
	// single-runtime usage but multi-runtime will not work without cgo.
	signal.Reset(syscall.SIGUSR1)
}

// configureJSCSignal is a no-op without cgo.
func configureJSCSignal(handle uintptr) {}
