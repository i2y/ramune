//go:build quickjs

package ramune

// detectLibrary is a no-op for QuickJS (compiled in, no shared library needed).
func detectLibrary() (string, error) {
	return "", nil
}

// getSharedHandle is a no-op for QuickJS.
func getSharedHandle() (uintptr, error) {
	return 0, nil
}

// configureJSCSignal is a no-op for QuickJS.
func configureJSCSignal() {}
