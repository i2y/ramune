//go:build !windows

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
		{"SIGUSR1", syscall.SIGUSR1}, {"SIGSEGV", syscall.SIGSEGV}, {"SIGUSR2", syscall.SIGUSR2},
		{"SIGPIPE", syscall.SIGPIPE}, {"SIGALRM", syscall.SIGALRM}, {"SIGTERM", syscall.SIGTERM},
		{"SIGCHLD", syscall.SIGCHLD}, {"SIGCONT", syscall.SIGCONT}, {"SIGSTOP", syscall.SIGSTOP},
		{"SIGTSTP", syscall.SIGTSTP}, {"SIGTTIN", syscall.SIGTTIN}, {"SIGTTOU", syscall.SIGTTOU},
		{"SIGURG", syscall.SIGURG}, {"SIGXCPU", syscall.SIGXCPU}, {"SIGXFSZ", syscall.SIGXFSZ},
		{"SIGVTALRM", syscall.SIGVTALRM}, {"SIGPROF", syscall.SIGPROF}, {"SIGWINCH", syscall.SIGWINCH},
		{"SIGIO", syscall.SIGIO}, {"SIGSYS", syscall.SIGSYS},
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
