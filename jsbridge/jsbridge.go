// Package jsbridge holds the minimum interfaces the hybrid AOT
// emitter generates against. It deliberately has no transitive
// dependency on ramune's host runtime (sqlite, esbuild, webview, …)
// so emitted Go can be compiled by TinyGo and shipped as a WASM /
// native plugin without dragging a 50MB host into the artifact.
//
// The host (`github.com/i2y/ramune`) supplies concrete values that
// satisfy these interfaces — `*ramune.JSFunc` already has the right
// shape, so satisfying `Func` is automatic via Go's structural
// typing. No manual binding is needed at the host boundary.
package jsbridge

// Func wraps a JavaScript function reference, allowing the emitted
// Go to invoke it. The host's *ramune.JSFunc satisfies this verbatim;
// any future engine-specific JSFunc has only to expose the same two
// methods for its plugins to keep working.
type Func interface {
	// Call invokes the underlying JS function with `args` marshalled
	// through the host engine's value bridge. The first non-nil
	// return value is the converted result; the second is non-nil
	// if the JS side threw (or the bridge surfaced a marshalling
	// error). Args / result follow ramune's existing conversion
	// rules (bool, float64, string, nil, map[string]any, []any).
	Call(args ...any) (any, error)

	// Close releases the underlying JS reference. Safe on nil and
	// safe to call multiple times.
	Close() error
}
