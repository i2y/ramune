package gotranspiler

import (
	"strings"
	"unicode"

	"github.com/i2y/ramune/internal/tsgo/ast"
)

// toPascalCase converts a camelCase or snake_case identifier to PascalCase (exported Go name).
func toPascalCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// toCamelCase ensures the first letter is lowercase (unexported Go name).
func toCamelCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// goExportedName converts a TypeScript identifier to an exported Go name.
// e.g., "myFunction" -> "MyFunction", "greet" -> "Greet"
func goExportedName(name string) string {
	return toPascalCase(name)
}

// goTypeName converts a TypeScript type/class/interface name to a Go type name (exported).
func goTypeName(name string) string {
	return toPascalCase(name)
}

// goParamName converts a TypeScript parameter name to a Go parameter name.
// Go parameter names are unexported but we keep them as-is since they are local.
func goParamName(name string) string {
	if isGoKeyword(name) {
		return name + "_"
	}
	return name
}

// goVarName converts a TypeScript variable name to a Go local variable name.
func goVarName(name string) string {
	if isGoKeyword(name) {
		return name + "_"
	}
	return name
}

// isGoKeyword returns true if s is a Go keyword or predeclared identifier.
func isGoKeyword(s string) bool {
	switch s {
	case "break", "case", "chan", "const", "continue",
		"default", "defer", "else", "fallthrough", "for",
		"func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return",
		"select", "struct", "switch", "type", "var",
		// Predeclared identifiers that could conflict
		"bool", "byte", "complex64", "complex128", "error",
		"float32", "float64", "int", "int8", "int16", "int32", "int64",
		"rune", "string", "uint", "uint8", "uint16", "uint32", "uint64",
		"uintptr", "true", "false", "nil", "iota",
		"append", "cap", "close", "copy", "delete",
		"len", "make", "new", "panic", "print", "println", "recover",
		"real", "imag", "complex":
		return true
	}
	return false
}

// GoPackageName turns an arbitrary string (typically a filename stem or npm
// package spec) into a valid Go package identifier: ASCII letters, digits,
// underscore; must start with a letter. Non-alphanumeric characters are
// replaced with `_`, a leading digit is prefixed with `_`. Empty input
// returns "app".
func GoPackageName(s string) string {
	if s == "" {
		return "app"
	}
	var b strings.Builder
	b.Grow(len(s) + 1)
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// isValidGoIdentifier returns true if s is a valid Go identifier (ASCII letters, digits, underscore).
func isValidGoIdentifier(s string) bool {
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return len(s) > 0
}

// nodeText safely extracts the text from an Identifier or PrivateIdentifier node.
// For PrivateIdentifier (#foo), it strips the # prefix.
func nodeText(node *ast.Node) string {
	if node == nil {
		return ""
	}
	if ast.IsPrivateIdentifier(node) {
		return strings.TrimPrefix(node.AsPrivateIdentifier().Text, "#")
	}
	if node.Kind == ast.KindIdentifier {
		return node.AsIdentifier().Text
	}
	return ""
}

// discriminantFieldNames are the property names recognized as discriminant keys
// for discriminated union detection.
var discriminantFieldNames = []string{"kind", "type", "tag", "_type", "discriminator"}

// sliceElemType extracts the element type from a Go slice type string.
// Returns ("float64", true) for "[]float64", ("", false) for non-slice types.
func sliceElemType(goType string) (string, bool) {
	if len(goType) > 2 && goType[0] == '[' && goType[1] == ']' {
		return goType[2:], true
	}
	return "", false
}

// formatSpecifier returns the fmt.Sprintf format specifier for a Go type.
func formatSpecifier(goType string) string {
	switch goType {
	case "string":
		return "%s"
	default:
		// Use %v for all non-string types — it handles int, float64, bool correctly
		// and avoids int/float mismatch issues with %g/%d
		return "%v"
	}
}
