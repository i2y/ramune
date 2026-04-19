//go:build darwin && !goja && !qjswasm

package ramune

// detectCandidates returns JavaScriptCore framework path on macOS.
// The system framework binary is in the shared cache (Big Sur+) and
// does not exist as a file on disk, but purego.Dlopen works with
// the framework path. We return it without checking file existence.
func detectCandidates() []candidate {
	return []candidate{
		{path: "/System/Library/Frameworks/JavaScriptCore.framework/JavaScriptCore"},
	}
}

func defaultSearchPaths() []string {
	return []string{
		"/System/Library/Frameworks/JavaScriptCore.framework/JavaScriptCore",
	}
}
