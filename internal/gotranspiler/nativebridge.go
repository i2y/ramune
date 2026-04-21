package gotranspiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
)

// ExportedFunc represents an exported Go function discovered in transpiled code.
type ExportedFunc struct {
	GoName  string // PascalCase Go name (e.g., "Fibonacci")
	JSName  string // camelCase JS name (e.g., "fibonacci")
	Generic bool   // True if the function has type parameters
}

// DiscoverExportedFuncs parses Go source code and returns all exported
// top-level functions (including generics, which are flagged for warning).
func DiscoverExportedFuncs(goSource string) ([]ExportedFunc, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "source.go", goSource, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing Go source: %w", err)
	}

	var funcs []ExportedFunc
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Recv != nil {
			continue
		}
		if !fn.Name.IsExported() {
			continue
		}
		if fn.Name.Name == "main" || fn.Name.Name == "init" {
			continue
		}

		isGeneric := fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) > 0

		funcs = append(funcs, ExportedFunc{
			GoName:  fn.Name.Name,
			JSName:  GoNameToJS(fn.Name.Name),
			Generic: isGeneric,
		})
	}

	return funcs, nil
}

// GenerateBridgeCode generates Go source code that registers transpiled
// functions as a native JS module via NativeModuleFromFuncs.
// Generic functions are skipped with a warning message.
func GenerateBridgeCode(moduleName, pkgAlias string, funcs []ExportedFunc) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\t\tramune.WithModule(ramune.NativeModuleFromFuncs(%q, map[string]any{\n", moduleName)
	for _, fn := range funcs {
		if fn.Generic {
			continue // Generic functions can't be passed as values in Go
		}
		fmt.Fprintf(&b, "\t\t\t%q: %s.%s,\n", fn.JSName, pkgAlias, fn.GoName)
	}
	b.WriteString("\t\t})),\n")

	return b.String()
}

// GenericWarnings returns warning messages for generic functions that can't be
// exposed as native module exports.
func GenericWarnings(funcs []ExportedFunc) []string {
	var warnings []string
	for _, fn := range funcs {
		if fn.Generic {
			warnings = append(warnings, fmt.Sprintf("  warning: %s is generic — cannot be exposed as native module export (wrap in a non-generic function)", fn.JSName))
		}
	}
	return warnings
}

// GenerateNativeImport generates the import statement for a native package.
func GenerateNativeImport(pkgImport, pkgAlias string) string {
	return fmt.Sprintf("\t%s %q\n", pkgAlias, pkgImport)
}

// GoNameToJS converts a PascalCase Go name to camelCase JS name.
func GoNameToJS(name string) string {
	if name == "" {
		return name
	}
	runes := []rune(name)
	i := 0
	for i < len(runes) && unicode.IsUpper(runes[i]) {
		i++
	}
	if i == 0 {
		return name
	}
	if i == 1 {
		runes[0] = unicode.ToLower(runes[0])
	} else if i == len(runes) {
		for j := range runes {
			runes[j] = unicode.ToLower(runes[j])
		}
	} else {
		for j := 0; j < i-1; j++ {
			runes[j] = unicode.ToLower(runes[j])
		}
	}
	return string(runes)
}
