package gotranspiler

import (
	"path/filepath"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
)

// nodeModuleToGoImport maps Node.js built-in module names to jsrt Go package paths.
var nodeModuleToGoImport = map[string]string{
	"fs":            "github.com/i2y/ramune/jsrt/node/fs",
	"path":          "github.com/i2y/ramune/jsrt/node/path",
	"crypto":        "github.com/i2y/ramune/jsrt/node/crypto",
	"os":            "github.com/i2y/ramune/jsrt/node/os",
	"http":          "github.com/i2y/ramune/jsrt/node/http",
	"https":         "github.com/i2y/ramune/jsrt/node/http",
	"events":        "github.com/i2y/ramune/jsrt/node/events",
	"stream":        "github.com/i2y/ramune/jsrt/node/stream",
	"url":           "github.com/i2y/ramune/jsrt/node/url",
	"util":          "github.com/i2y/ramune/jsrt/node/util",
	"zlib":          "github.com/i2y/ramune/jsrt/node/zlib",
	"child_process": "github.com/i2y/ramune/jsrt/node/child_process",
	"assert":        "github.com/i2y/ramune/jsrt/node/assert",
	"querystring":   "github.com/i2y/ramune/jsrt/node/querystring",
	"buffer":        "github.com/i2y/ramune/jsrt/node/buffer",
}

// npmToGoImport maps popular npm package names to Go adapter package paths.
var npmToGoImport = map[string]string{
	"uuid":      "github.com/i2y/ramune/jsrt/compat/uuid",
	"lodash":    "github.com/i2y/ramune/jsrt/compat/lodash",
	"lodash-es": "github.com/i2y/ramune/jsrt/compat/lodash",
	"zod":       "github.com/i2y/ramune/jsrt/compat/zod",
}

// npmPackageRefExports maps export names that represent the entire package namespace.
// e.g., `import { z } from 'zod'` — `z` is the package namespace, not a single function.
var npmPackageRefExports = map[string]map[string]bool{
	"zod": {"z": true},
}

// goStdPackages is a set of Go standard library package names that may collide with user imports.
var goStdPackages = map[string]bool{
	"math": true, "fmt": true, "strings": true, "os": true, "io": true,
	"net": true, "http": true, "sync": true, "time": true, "sort": true,
	"errors": true, "context": true, "regexp": true, "reflect": true,
	"json": true, "crypto": true, "hash": true, "path": true,
}

// emitImportDeclaration handles TS import statements.
func (t *Transpiler) emitImportDeclaration(node *ast.Node) {
	importDecl := node.AsImportDeclaration()
	if importDecl.ModuleSpecifier == nil {
		return
	}

	// Type-only imports (import type { ... } from '...'): still resolve the path and track names
	// for lazy import, but don't eagerly add to Go imports.
	isTypeOnly := false
	if importDecl.ImportClause != nil {
		clause := importDecl.ImportClause.AsImportClause()
		if clause.PhaseModifier == ast.KindTypeKeyword {
			isTypeOnly = true
		}
	}
	_ = isTypeOnly // used below in emitPackageImport path

	var moduleSpec string
	if importDecl.ModuleSpecifier.Kind == ast.KindStringLiteral {
		moduleSpec = importDecl.ModuleSpecifier.AsStringLiteral().Text
	} else {
		return
	}

	// Strip node: prefix
	moduleSpec = strings.TrimPrefix(moduleSpec, "node:")

	// 1. Node.js built-in module
	if goImport, ok := nodeModuleToGoImport[moduleSpec]; ok {
		alias := moduleSpec
		if i := strings.LastIndex(alias, "/"); i >= 0 {
			alias = alias[i+1:]
		}
		alias = strings.ReplaceAll(alias, "_", "")
		t.emitPackageImport(importDecl, goImport, alias)
		return
	}

	// 2. npm adapter package
	if goImport, ok := npmToGoImport[moduleSpec]; ok {
		alias := filepath.Base(goImport)
		t.emitPackageImport(importDecl, goImport, alias)
		return
	}

	// 3. Relative import
	if strings.HasPrefix(moduleSpec, ".") {
		t.emitRelativeImport(importDecl, moduleSpec)
		return
	}

	// 4. Try to resolve as transpilable npm package
	if t.npmResolver != nil {
		analysis := t.npmResolver.resolve(moduleSpec)
		if analysis != nil {
			alias := sanitizePkgName(moduleSpec)
			t.emitPackageImport(importDecl, analysis.GoImportPath, alias)
			return
		}
	}

	// 5. Go package import (go: prefix)
	if goImportPath, ok := strings.CutPrefix(moduleSpec, "go:"); ok {
		alias := filepath.Base(goImportPath)
		t.emitPackageImport(importDecl, goImportPath, alias)
		t.trackGoNativeImport(alias, goImportPath)
		return
	}

	// 6. Unknown module
	t.w.writelnf("// import %q — not yet supported", moduleSpec)
}

// emitPackageImport handles the import clause for a resolved Go package.
// This is shared between Node.js builtins, npm adapters, and relative imports.
func (t *Transpiler) emitPackageImport(importDecl *ast.ImportDeclaration, goImportPath string, pkgAlias string) {
	if importDecl.ImportClause == nil {
		// Side-effect import
		t.w.addImport(goImportPath, pkgAlias)
		return
	}

	clause := importDecl.ImportClause.AsImportClause()

	// Default import: import Foo from 'pkg'
	if clause.Name() != nil {
		t.trackPendingImport(pkgAlias, goImportPath)
		localName := clause.Name().AsIdentifier().Text
		t.trackImportedName(localName, pkgAlias)
	}

	// Named/namespace bindings
	if clause.NamedBindings != nil {
		switch clause.NamedBindings.Kind {
		case ast.KindNamespaceImport:
			ns := clause.NamedBindings.AsNamespaceImport()
			alias := ns.Name().AsIdentifier().Text
			// Namespace imports are always used as package references, add eagerly
			t.w.addImport(goImportPath, alias)

		case ast.KindNamedImports:
			named := clause.NamedBindings.AsNamedImports()
			// Register as pending — only added to output when actually used in code
			t.trackPendingImport(pkgAlias, goImportPath)
			if named.Elements != nil {
				for _, spec := range named.Elements.Nodes {
					is := spec.AsImportSpecifier()
					localName := spec.Name().AsIdentifier().Text

					// Track original export name for renamed imports
					// e.g., import { v4 as uuidv4 } → local="uuidv4", original="v4"
					originalName := localName
					if is.PropertyName != nil && is.PropertyName.Kind == ast.KindIdentifier {
						originalName = is.PropertyName.AsIdentifier().Text
					}

					// Check if this export is a package namespace reference
					if refs, ok := npmPackageRefExports[pkgAlias]; ok && refs[originalName] {
						t.trackPackageRef(localName, pkgAlias)
					} else {
						t.trackImportedName(localName, pkgAlias)
						if originalName != localName {
							t.trackOriginalName(localName, originalName)
						}
					}
				}
			}
		}
	}
}

// emitRelativeImport handles relative imports like import { X } from './utils'.
func (t *Transpiler) emitRelativeImport(importDecl *ast.ImportDeclaration, moduleSpec string) {
	cleanPath := moduleSpec
	cleanPath = strings.TrimPrefix(cleanPath, "./")
	cleanPath = strings.TrimSuffix(cleanPath, ".ts")
	cleanPath = strings.TrimSuffix(cleanPath, ".js")

	// Resolve relative to the current file's directory (for multi-file transpilation).
	// e.g., if currentFileDir="utils" and moduleSpec="./crypto", resolve to "utils/crypto"
	if t.currentFileDir != "" && t.currentFileDir != "." {
		cleanPath = filepath.Join(t.currentFileDir, cleanPath)
	}

	cleanPath = filepath.Clean(cleanPath)
	// Normalize away leading ../ that go above project root
	for strings.HasPrefix(cleanPath, "..") {
		cleanPath = strings.TrimPrefix(cleanPath, "../")
		cleanPath = strings.TrimPrefix(cleanPath, "..")
	}
	if cleanPath == "" || cleanPath == "." {
		cleanPath = filepath.Base(moduleSpec)
		cleanPath = strings.TrimSuffix(cleanPath, ".ts")
		cleanPath = strings.TrimSuffix(cleanPath, ".js")
	}

	// Sanitize path components for Go (replace hyphens)
	cleanPath = strings.ReplaceAll(cleanPath, "-", "_")

	// In Go, all .go files in a directory share one package.
	// Determine which Go package directory this import resolves to.
	// "utils/html" → the file is utils/html.go, Go package is "utils"
	// "compose" → the file is compose/compose.go, Go package is "compose"
	goPkgDir := cleanPath
	dir := filepath.Dir(cleanPath)
	if dir != "." && dir != "" {
		// Has a directory component: "utils/html" → package is "utils"
		goPkgDir = dir
	}

	// If the resolved package is the same as the current file's package, skip the import.
	// In Go, files in the same directory are in the same package — no import needed.
	currentPkgDir := t.currentFileDir
	if currentPkgDir == "" || currentPkgDir == "." {
		currentPkgDir = t.pkgName
		if t.isEntryFile {
			currentPkgDir = "."
		}
	}
	if goPkgDir == currentPkgDir {
		// Same package — no import needed, but track exported names for correct casing.
		// In Go, exported names from the same package use PascalCase (goExportedName).
		if importDecl.ImportClause != nil {
			clause := importDecl.ImportClause.AsImportClause()
			if clause.NamedBindings != nil && clause.NamedBindings.Kind == ast.KindNamedImports {
				for _, spec := range clause.NamedBindings.AsNamedImports().Elements.Nodes {
					is := spec.AsImportSpecifier()
					localName := nodeText(is.Name())
					if t.samePackageExports == nil {
						t.samePackageExports = make(map[string]bool)
					}
					t.samePackageExports[localName] = true
				}
			}
			// Default import from same package
			if clause.Name() != nil {
				localName := clause.Name().AsIdentifier().Text
				if t.samePackageExports == nil {
					t.samePackageExports = make(map[string]bool)
				}
				t.samePackageExports[localName] = true
			}
		}
		return
	}

	pkgName := sanitizePkgName(filepath.Base(goPkgDir))
	// Avoid alias collision with Go standard library packages
	if goStdPackages[pkgName] {
		pkgName = "pkg" + pkgName
	}
	goImportPath := goPkgDir
	if t.goModuleName != "" {
		goImportPath = t.goModuleName + "/" + goPkgDir
	}

	t.emitPackageImport(importDecl, goImportPath, pkgName)
}

// trackImportedName records that a TS identifier was imported from a Go package.
// This enables qualifyTypeName() to add package prefixes like "types.Env".
func (t *Transpiler) trackImportedName(tsName string, goPkg string) {
	if t.importedNames == nil {
		t.importedNames = make(map[string]string)
	}
	t.importedNames[tsName] = goPkg
}

// trackOriginalName records the original export name for a renamed import.
func (t *Transpiler) trackOriginalName(localName string, originalName string) {
	if t.importedOriginalNames == nil {
		t.importedOriginalNames = make(map[string]string)
	}
	t.importedOriginalNames[localName] = originalName
}

// trackGoNativeImport records a go: prefix import (alias → full path).
func (t *Transpiler) trackGoNativeImport(alias, goImportPath string) {
	if t.goNativeImports == nil {
		t.goNativeImports = make(map[string]string)
	}
	t.goNativeImports[alias] = goImportPath
}

// trackPendingImport records an import path for lazy resolution.
// The import is only added to the Go output when the alias is actually used in code.
func (t *Transpiler) trackPendingImport(alias, goImportPath string) {
	if t.pendingImports == nil {
		t.pendingImports = make(map[string]string)
	}
	t.pendingImports[alias] = goImportPath
}

// resolvePendingImport adds a pending import to the Go output if it exists.
func (t *Transpiler) resolvePendingImport(alias string) {
	if t.pendingImports == nil {
		return
	}
	if path, ok := t.pendingImports[alias]; ok {
		t.w.addImport(path, alias)
		delete(t.pendingImports, alias)
	}
}

// trackPackageRef records that a name is a direct package reference.
func (t *Transpiler) trackPackageRef(localName string, goPkg string) {
	if t.packageRefs == nil {
		t.packageRefs = make(map[string]string)
	}
	t.packageRefs[localName] = goPkg
}

// isExported checks if a node has the export keyword modifier.
func isExported(node *ast.Node) bool {
	if node == nil {
		return false
	}
	return ast.HasSyntacticModifier(node, ast.ModifierFlagsExport)
}
