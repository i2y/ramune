//go:build linux && !goja && !qjswasm

package ramune

// detectCandidates returns JavaScriptCore GTK library paths on Linux.
func detectCandidates() []candidate {
	dirs := []string{
		"/usr/lib",
		"/usr/lib/x86_64-linux-gnu",
		"/usr/lib/aarch64-linux-gnu",
	}
	// Newest first.
	patterns := []string{
		"libjavascriptcoregtk-6.0.so*",
		"libjavascriptcoregtk-4.1.so*",
		"libjavascriptcoregtk-4.0.so*",
	}
	return globCandidates(dirs, patterns)
}

func defaultSearchPaths() []string {
	return []string{
		"/usr/lib/libjavascriptcoregtk-6.0.so*",
		"/usr/lib/libjavascriptcoregtk-4.1.so*",
		"/usr/lib/libjavascriptcoregtk-4.0.so*",
		"/usr/lib/x86_64-linux-gnu/libjavascriptcoregtk-*.so*",
		"/usr/lib/aarch64-linux-gnu/libjavascriptcoregtk-*.so*",
	}
}
