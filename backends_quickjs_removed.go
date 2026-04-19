//go:build quickjs

package ramune

// The modernc.org/quickjs backend was removed after qjswasm (QuickJS-NG on
// wazero) matched or beat it on CPU, throughput, and multi-worker scaling
// across the full bench matrix. Use -tags qjswasm for the pure-Go path,
// or drop the tag to get the JSC backend.
//
// The reference to the undefined symbol below forces a hard build error,
// so old scripts that pass -tags quickjs fail loudly instead of silently
// falling through to JSC.
var _ = modernc_quickjs_backend_removed_use_tags_qjswasm
