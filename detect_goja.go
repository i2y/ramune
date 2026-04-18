//go:build goja

package ramune

// detectLibrary is a no-op for goja (pure Go, no shared library needed).
func detectLibrary() (string, error) {
	return "", nil
}

// getSharedHandle is a no-op for goja.
func getSharedHandle() (uintptr, error) {
	return 0, nil
}

// configureJSCSignal is a no-op for goja.
func configureJSCSignal() {}
