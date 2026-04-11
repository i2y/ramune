package ramune

import "fmt"

// installConsole registers console.log/error/warn/info/debug backed by
// the Runtime's stdout/stderr writers. Called unconditionally during
// Runtime initialization (before NodeCompat).
func (r *Runtime) installConsole() error {
	if err := r.registerFuncLocked("__go_stdout", func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		fmt.Fprintln(r.stdout, join(parts, " "))
		return nil, nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_stderr", func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		fmt.Fprintln(r.stderr, join(parts, " "))
		return nil, nil
	}); err != nil {
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

// join concatenates strings with a separator (avoids importing strings).
func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += sep + p
	}
	return s
}
