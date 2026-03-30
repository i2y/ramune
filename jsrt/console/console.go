// Package console provides console.log/error/warn for transpiled TypeScript code.
package console

import (
	"fmt"
	"os"
	"strings"
)

// Log prints arguments to stdout, separated by spaces with a trailing newline.
func Log(args ...any) {
	fmt.Println(args...)
}

// Error prints arguments to stderr, separated by spaces with a trailing newline.
func Error(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
}

// Warn prints arguments to stderr (alias for Error).
func Warn(args ...any) {
	Error(args...)
}

// Info prints arguments to stdout (alias for Log).
func Info(args ...any) {
	Log(args...)
}

// Debug prints arguments to stdout (alias for Log).
func Debug(args ...any) {
	Log(args...)
}

// Assert logs an assertion error if condition is false.
func Assert(condition bool, args ...any) {
	if !condition {
		msg := "Assertion failed"
		if len(args) > 0 {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = fmt.Sprint(a)
			}
			msg += ": " + strings.Join(parts, " ")
		}
		Error(msg)
	}
}
