// Package path provides Node.js path module equivalents for transpiled TypeScript code.
package path

import (
	"os"
	"path/filepath"
	"strings"
)

// Join joins path segments.
func Join(parts ...string) string {
	return filepath.Join(parts...)
}

// Resolve resolves a sequence of paths to an absolute path.
func Resolve(parts ...string) string {
	if len(parts) == 0 {
		cwd, _ := os.Getwd()
		return cwd
	}
	result := parts[0]
	for _, p := range parts[1:] {
		if filepath.IsAbs(p) {
			result = p
		} else {
			result = filepath.Join(result, p)
		}
	}
	if !filepath.IsAbs(result) {
		cwd, _ := os.Getwd()
		result = filepath.Join(cwd, result)
	}
	return filepath.Clean(result)
}

// Dirname returns the directory of a path.
func Dirname(p string) string {
	return filepath.Dir(p)
}

// Basename returns the last element of a path, optionally stripping an extension.
func Basename(p string, ext ...string) string {
	base := filepath.Base(p)
	if len(ext) > 0 && strings.HasSuffix(base, ext[0]) {
		base = base[:len(base)-len(ext[0])]
	}
	return base
}

// Extname returns the file extension.
func Extname(p string) string {
	return filepath.Ext(p)
}

// IsAbsolute returns true if the path is absolute.
func IsAbsolute(p string) bool {
	return filepath.IsAbs(p)
}

// Relative returns a relative path from 'from' to 'to'.
func Relative(from, to string) (string, error) {
	return filepath.Rel(from, to)
}

// Normalize normalizes a path.
func Normalize(p string) string {
	return filepath.Clean(p)
}

// Sep is the OS-specific path separator.
var Sep = string(filepath.Separator)

// Delimiter is the OS-specific path list delimiter.
var Delimiter = string(filepath.ListSeparator)

// Parse parses a path into components.
type ParsedPath struct {
	Root string
	Dir  string
	Base string
	Ext  string
	Name string
}

// Parse splits a path into its components.
func Parse(p string) ParsedPath {
	dir := filepath.Dir(p)
	base := filepath.Base(p)
	ext := filepath.Ext(p)
	name := base
	if ext != "" {
		name = base[:len(base)-len(ext)]
	}
	root := ""
	if filepath.IsAbs(p) {
		root = string(filepath.Separator)
	}
	return ParsedPath{Root: root, Dir: dir, Base: base, Ext: ext, Name: name}
}
