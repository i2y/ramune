package core

import "github.com/i2y/ramune/internal/rslint/tsgo_pinned/tspath"

type ProjectReference struct {
	Path         string
	OriginalPath string
	Circular     bool
}

func ResolveProjectReferencePath(ref *ProjectReference) string {
	return ResolveConfigFileNameOfProjectReference(ref.Path)
}

func ResolveConfigFileNameOfProjectReference(path string) string {
	if tspath.FileExtensionIs(path, tspath.ExtensionJson) {
		return path
	}
	return tspath.CombinePaths(path, "tsconfig.json")
}
