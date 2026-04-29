package gotranspiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
)

// ExportedFunc represents an exported Go function discovered in transpiled
// code. Params/Return are populated as best-effort Go type strings (Ident,
// SelectorExpr, StarExpr, ArrayType, MapType — anything more exotic falls
// back to a `<%T>` placeholder); callers that only need the name set
// (existing JS-bridge wiring) can ignore those fields.
type ExportedFunc struct {
	GoName  string   // PascalCase Go name (e.g., "Fibonacci")
	JSName  string   // camelCase JS name (e.g., "fibonacci")
	Generic bool     // True if the function has type parameters
	Params  []string // One entry per param slot, in declaration order
	Return  string   // "" for void / no return clause
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

		var params []string
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				ty := goExprString(field.Type)
				count := 1
				if len(field.Names) > 0 {
					count = len(field.Names)
				}
				for i := 0; i < count; i++ {
					params = append(params, ty)
				}
			}
		}
		var ret string
		if fn.Type.Results != nil && len(fn.Type.Results.List) == 1 && len(fn.Type.Results.List[0].Names) <= 1 {
			ret = goExprString(fn.Type.Results.List[0].Type)
		}

		funcs = append(funcs, ExportedFunc{
			GoName:  fn.Name.Name,
			JSName:  GoNameToJS(fn.Name.Name),
			Generic: isGeneric,
			Params:  params,
			Return:  ret,
		})
	}

	return funcs, nil
}

// goExprString stringifies a Go AST type expression for downstream
// signature analysis. Covers the shapes the transpiler actually emits;
// anything else gets a `<%T>` placeholder that ABI checks can reject
// without crashing.
func goExprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return goExprString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + goExprString(e.X)
	case *ast.ArrayType:
		return "[]" + goExprString(e.Elt)
	case *ast.MapType:
		return "map[" + goExprString(e.Key) + "]" + goExprString(e.Value)
	}
	return fmt.Sprintf("<%T>", expr)
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
