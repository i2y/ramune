//go:build windows

package ramune

import (
	"fmt"
	"strings"
	"syscall"
)

// signalConstants returns a JS object literal with platform-correct signal numbers.
func signalConstants() string {
	type sig struct {
		name string
		num  syscall.Signal
	}
	sigs := []sig{
		{"SIGHUP", syscall.SIGHUP}, {"SIGINT", syscall.SIGINT}, {"SIGQUIT", syscall.SIGQUIT},
		{"SIGILL", syscall.SIGILL}, {"SIGTRAP", syscall.SIGTRAP}, {"SIGABRT", syscall.SIGABRT},
		{"SIGBUS", syscall.SIGBUS}, {"SIGFPE", syscall.SIGFPE}, {"SIGKILL", syscall.SIGKILL},
		{"SIGSEGV", syscall.SIGSEGV}, {"SIGPIPE", syscall.SIGPIPE}, {"SIGALRM", syscall.SIGALRM},
		{"SIGTERM", syscall.SIGTERM},
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range sigs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s:%d", s.name, int(s.num))
	}
	b.WriteByte('}')
	return b.String()
}
