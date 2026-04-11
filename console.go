package ramune

import (
	"fmt"
	"io"
	"strings"
)

// installConsole registers console.log/error/warn/info/debug backed by
// the Runtime's stdout/stderr writers. Called unconditionally during
// Runtime initialization (before NodeCompat).
func (r *Runtime) installConsole() error {
	if err := r.registerFuncLocked("__go_stdout", r.printFunc(r.stdout)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_stderr", r.printFunc(r.stderr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_stdout_raw", r.rawWriteFunc(r.stdout)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_stderr_raw", r.rawWriteFunc(r.stderr)); err != nil {
		return err
	}
	return r.execLocked(`
		if (typeof globalThis.console === 'undefined') globalThis.console = {};
		console.log = function() { __go_stdout.apply(null, Array.prototype.slice.call(arguments)); };
		console.info = console.log;
		console.debug = console.log;
		console.error = function() { __go_stderr.apply(null, Array.prototype.slice.call(arguments)); };
		console.warn = console.error;
	`)
}

func (r *Runtime) printFunc(w io.Writer) GoFunc {
	return func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		fmt.Fprintln(w, strings.Join(parts, " "))
		return nil, nil
	}
}

func (r *Runtime) rawWriteFunc(w io.Writer) GoFunc {
	return func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprint(w, args[0])
		}
		return true, nil
	}
}
