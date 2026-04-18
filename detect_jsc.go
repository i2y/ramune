//go:build !quickjs && !goja

package ramune

import (
	"fmt"
	"os"
	"path/filepath"
)

// candidate represents a found JSC shared library on disk.
type candidate struct {
	path string
}

// detectLibrary returns the path to the JavaScriptCore shared library.
func detectLibrary(cfg config) (string, error) {
	if cfg.libraryPath != "" {
		return cfg.libraryPath, nil
	}

	candidates := detectCandidates()
	if len(candidates) == 0 {
		return "", &LibraryNotFoundError{Searched: defaultSearchPaths()}
	}

	return candidates[0].path, nil
}

// globCandidates searches for shared libraries matching patterns in directories.
func globCandidates(dirs []string, patterns []string) []candidate {
	var results []candidate
	for _, dir := range dirs {
		for _, pat := range patterns {
			matches, _ := filepath.Glob(filepath.Join(dir, pat))
			for _, m := range matches {
				if info, err := os.Stat(m); err == nil && !info.IsDir() {
					results = append(results, candidate{path: m})
				}
			}
		}
	}
	return results
}

func frameworkCandidate(path string) candidate {
	return candidate{path: fmt.Sprintf("%s/JavaScriptCore", path)}
}
