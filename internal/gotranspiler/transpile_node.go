package gotranspiler

import (
	"fmt"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// TranspileNodes emits Go source for a specific set of top-level
// FunctionDeclaration nodes from a single TypeScript source file, producing a
// standalone Go package that can be compiled as a native extension module.
//
// The caller is responsible for ensuring the nodes are safe to translate
// (typically via the picker package). All nodes must originate from the same
// source file and must be top-level function declarations.
func TranspileNodes(ck *checker.Checker, nodes []*ast.Node, pkgName string) (string, error) {
	if pkgName == "" {
		pkgName = "native"
	}
	if ck == nil {
		return "", fmt.Errorf("nil checker")
	}

	t := &Transpiler{
		w:                  newGoWriter(),
		ck:                 ck,
		pkgName:            pkgName,
		isEntryFile:        false,
		classNames:         make(map[string]bool),
		localTypeNames:     make(map[string]bool),
		samePackageExports: make(map[string]bool),
		arrayCallbackIdx:   -1,
	}
	t.tm = newTypeMapper(ck)

	// Pre-scan: same-package exports let emitIdentifier route recursive and
	// peer calls through the PascalCase Go name. Register TS-exported
	// functions and (v1.5) class names; unexported helpers stay package-local
	// in Go.
	for _, n := range nodes {
		if n == nil {
			continue
		}
		switch n.Kind {
		case ast.KindFunctionDeclaration:
			if !isExported(n) {
				continue
			}
			fd := n.AsFunctionDeclaration()
			if fd == nil || fd.Name() == nil || fd.Name().Kind != ast.KindIdentifier {
				continue
			}
			t.samePackageExports[fd.Name().AsIdentifier().Text] = true
		case ast.KindClassDeclaration:
			id := n.Name()
			if id == nil || id.Kind != ast.KindIdentifier {
				continue
			}
			cname := id.AsIdentifier().Text
			// Register under both camelCase (export key) and PascalCase so
			// emitIdentifier picks the exported Go name for intra-file refs.
			t.samePackageExports[cname] = true
			t.localTypeNames[goTypeName(cname)] = true
		}
	}

	for _, n := range nodes {
		if n == nil {
			continue
		}
		switch n.Kind {
		case ast.KindFunctionDeclaration:
			t.emitFunctionDeclaration(n)
		case ast.KindInterfaceDeclaration:
			// Interface declarations referenced by extracted functions become
			// Go structs; the picker collects them so the param/return types
			// resolve.
			t.emitInterfaceDeclaration(n)
		case ast.KindClassDeclaration:
			t.emitClassDeclaration(n)
		default:
			return "", fmt.Errorf("unsupported node kind for TranspileNodes: %v", n.Kind)
		}
	}

	t.w.pendingImports = t.pendingImports
	return t.w.renderFile(pkgName)
}
