//go:build qjswasm && !goja

package ramune

// detectLibrary is a no-op for qjswasm. The QuickJS-NG wasm module is
// embedded via //go:embed in engine_qjswasm.go, so no external library
// resolution is needed.
func detectLibrary() (string, error) {
	return "", nil
}

// getSharedHandle is a no-op for qjswasm.
func getSharedHandle() (uintptr, error) {
	return 0, nil
}

// configureJSCSignal is a no-op for qjswasm.
func configureJSCSignal() {}
