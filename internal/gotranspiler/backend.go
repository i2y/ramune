package gotranspiler

import "fmt"

// Backend selects the runtime concrete type the emitter uses for TS
// function-typed parameters and (transitively) the import set the
// emitted Go pulls in. The zero value is BackendGo for back-compat
// with callers that don't set it.
type Backend string

const (
	// BackendGo emits *ramune.JSFunc and auto-imports the ramune root
	// package. The artefact carries ramune's host runtime (sqlite,
	// esbuild, webview), suitable for `go build` of a hybrid module
	// loaded by a ramune host.
	BackendGo Backend = "go"
	// BackendTinyGo emits jsbridge.Func (an interface in
	// github.com/i2y/ramune/jsbridge with no host dependency) so the
	// emitted Go fits inside TinyGo's stdlib subset. Required for
	// `tinygo build -target=wasi`.
	BackendTinyGo Backend = "tinygo"
)

// ParseBackend converts the user-facing flag string ("", "go", "tinygo")
// into a Backend. Empty string maps to BackendGo. Anything else is an
// error so callers can surface a clean usage message.
func ParseBackend(s string) (Backend, error) {
	switch s {
	case "", string(BackendGo):
		return BackendGo, nil
	case string(BackendTinyGo):
		return BackendTinyGo, nil
	}
	return "", fmt.Errorf("invalid backend %q: must be %q or %q", s, BackendGo, BackendTinyGo)
}
