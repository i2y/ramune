//go:build linux && cgo && !goja && !qjswasm

package ramune

// Importing "C" enables Go's cgo signal handling infrastructure
// (_cgo_sigaction). This allows JSC's SIGUSR1-based GC to coexist
// with Go's runtime by properly forwarding signals between Go and C.
// Without cgo, Go intercepts SIGUSR1 sent by JSC's pthread_kill,
// causing SEGV in multi-runtime configurations.

/*
#include <signal.h>

// configureSigForGC attempts to call JSConfigureSignalForGC if available.
// This is a private API in JSBasePrivate.h but present in libjavascriptcoregtk.
// We declare it as weak so linking succeeds even if it's absent.
extern void JSConfigureSignalForGC(int) __attribute__((weak));

static void configure_jsc_signal() {
    if (JSConfigureSignalForGC) {
        // Move GC signal from SIGUSR1 to SIGUSR2 to reduce conflict.
        JSConfigureSignalForGC(SIGUSR2);
    }
}
*/
import "C"

// configureJSCSignal sets up JSC's GC signal for Linux via cgo.
// Called after dlopen but before creating any JSC context.
func configureJSCSignal(handle uintptr) {
	C.configure_jsc_signal()
}
