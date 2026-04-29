package gotranspiler

import (
	"fmt"
	"go/format"
	"strings"
)

// goWriter is a strings.Builder wrapper that manages Go source generation with indentation.
type goWriter struct {
	buf            strings.Builder
	indent         int
	indentStr      string
	needsIndent    bool
	imports        map[string]string // import path -> alias (empty string = no alias)
	pendingImports map[string]string // alias → import path (for lazy resolution in renderFile)
}

func newGoWriter() *goWriter {
	return &goWriter{
		indentStr: "\t",
		imports:   make(map[string]string),
	}
}

// addImport registers an import. alias can be empty for default import.
func (w *goWriter) addImport(path string, alias string) {
	if _, ok := w.imports[path]; !ok {
		w.imports[path] = alias
	}
}

// write writes raw text (no newline, respects indentation on first write after newline).
func (w *goWriter) write(s string) {
	if w.needsIndent && s != "" {
		for range w.indent {
			w.buf.WriteString(w.indentStr)
		}
		w.needsIndent = false
	}
	w.buf.WriteString(s)
}

// writef writes formatted text.
func (w *goWriter) writef(format string, args ...any) {
	w.write(fmt.Sprintf(format, args...))
}

// writeln writes text followed by a newline.
func (w *goWriter) writeln(s string) {
	w.write(s)
	w.buf.WriteString("\n")
	w.needsIndent = true
}

// writelnf writes formatted text followed by a newline.
func (w *goWriter) writelnf(format string, args ...any) {
	w.writeln(fmt.Sprintf(format, args...))
}

// newline writes a blank line.
func (w *goWriter) newline() {
	w.buf.WriteString("\n")
	w.needsIndent = true
}

// openBlock writes " {" and increases indent.
func (w *goWriter) openBlock() {
	w.writeln(" {")
	w.indent++
}

// closeBlock decreases indent and writes "}".
func (w *goWriter) closeBlock() {
	w.indent--
	w.writeln("}")
}

// closeBlockInline decreases indent and writes "}" without newline.
func (w *goWriter) closeBlockInline() {
	w.indent--
	w.write("}")
}

// renderFile produces the final Go source code with package declaration and imports.
// isImportUsed checks if an import alias is used as a package qualifier in Go code.
// Looks for patterns like "alias.ExportedName" where ExportedName starts with uppercase.
func isImportUsed(body string, alias string) bool {
	needle := alias + "."
	idx := 0
	for {
		pos := strings.Index(body[idx:], needle)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		afterDot := absPos + len(needle)

		// Check the character after the dot — package member access should start with uppercase
		if afterDot < len(body) {
			nextChar := body[afterDot]
			if nextChar >= 'A' && nextChar <= 'Z' {
				// Check the character before — must be a non-identifier character
				if absPos == 0 {
					return true
				}
				prev := body[absPos-1]
				if prev == ' ' || prev == '\t' || prev == '\n' || prev == '(' || prev == ',' ||
					prev == '{' || prev == '[' || prev == '!' || prev == '=' || prev == ':' ||
					prev == ';' || prev == '&' || prev == '|' || prev == '<' || prev == '>' ||
					prev == '*' || prev == ')' || prev == '+' || prev == '-' || prev == '/' {
					return true
				}
			}
		}
		idx = absPos + len(needle)
	}
}

// isLocalVar checks if a name appears as a local variable/parameter declaration in the body.
// Detects patterns like "name any", "name string", "name :=", "name," in parameter lists.
func isLocalVar(body string, name string) bool {
	// Check for parameter declaration: (name any) or (name string) or func(name any)
	patterns := []string{
		name + " any",
		name + " string",
		name + " float64",
		name + " int",
		name + " bool",
		name + " *",
		name + " :=",
	}
	for _, p := range patterns {
		if strings.Contains(body, p) {
			return true
		}
	}
	return false
}

func (w *goWriter) renderFile(pkgName string) (string, error) {
	var out strings.Builder
	body := w.buf.String()

	out.WriteString("package " + pkgName + "\n\n")

	// Auto-add imports for well-known packages referenced in the body
	if strings.Contains(body, "web.") {
		w.addImport("github.com/i2y/ramune/jsrt/web", "web")
	}
	if strings.Contains(body, "jsrt.") {
		w.addImport("github.com/i2y/ramune/jsrt", "")
	}
	if strings.Contains(body, "promise.") {
		w.addImport("github.com/i2y/ramune/jsrt/promise", "")
	}
	if strings.Contains(body, "jsarray.") {
		w.addImport("github.com/i2y/ramune/jsrt/array", "jsarray")
	}
	if strings.Contains(body, "console.") {
		w.addImport("github.com/i2y/ramune/jsrt/console", "")
	}
	// *ramune.JSFunc is the Go type emitted for TS function-typed
	// parameters in hybrid extraction. Narrower than the other
	// auto-imports because the bare `ramune` alias could false-trigger
	// on user identifiers; keyed on the exact emitted spelling.
	if strings.Contains(body, "*ramune.JSFunc") {
		w.addImport("github.com/i2y/ramune", "")
	}
	// jsbridge.Func is the TinyGo-backend counterpart of *ramune.JSFunc;
	// see the package doc for the host-decoupling rationale.
	if strings.Contains(body, "jsbridge.Func") {
		w.addImport("github.com/i2y/ramune/jsbridge", "")
	}

	// Resolve pending imports that appear as package qualifiers in the body.
	// Use stricter check: the alias must appear followed by .UpperCase AND not be a parameter name.
	if w.pendingImports != nil {
		for alias, path := range w.pendingImports {
			// Check if alias.ExportedName is used in the body but alias is not also used as a local variable
			if isImportUsed(body, alias) && !isLocalVar(body, alias) {
				w.addImport(path, alias)
			}
		}
	}

	if len(w.imports) > 0 {
		// Filter out unused imports by checking if the alias is referenced as a package in the body.
		// Use goimports-style analysis to only keep imports whose alias appears as a qualifier.
		var usedImports []struct{ path, alias string }
		for path, alias := range w.imports {
			ref := alias
			if ref == "" {
				if i := strings.LastIndex(path, "/"); i >= 0 {
					ref = path[i+1:]
				} else {
					ref = path
				}
			}
			if isImportUsed(body, ref) {
				usedImports = append(usedImports, struct{ path, alias string }{path, alias})
			}
		}

		if len(usedImports) > 0 {
			out.WriteString("import (\n")
			for _, imp := range usedImports {
				if imp.alias != "" {
					out.WriteString("\t" + imp.alias + " " + `"` + imp.path + `"` + "\n")
				} else {
					out.WriteString("\t" + `"` + imp.path + `"` + "\n")
				}
			}
			out.WriteString(")\n\n")
		}
	}

	out.WriteString(body)

	raw := out.String()
	// Safety net: fix trailing commas where inCallArg context doesn't fully cover
	raw = fixTrailingFuncArgs(raw)

	formatted, err := format.Source([]byte(raw))
	if err != nil {
		return raw, fmt.Errorf("gofmt error: %w", err)
	}

	return string(formatted), nil
}

// replaceUndefinedGenericTypes replaces PascalCase[...] patterns with "any" when the type
// is not defined in the current file (not declared as type/struct/interface).
func replaceUndefinedGenericTypes(raw string, body string) string {
	// Collect all defined names in this file (types, functions, variables)
	defined := make(map[string]bool)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type ") {
			rest := trimmed[5:]
			end := strings.IndexAny(rest, " =[{")
			if end > 0 {
				defined[rest[:end]] = true
			}
		}
		if strings.HasPrefix(trimmed, "func ") && !strings.HasPrefix(trimmed, "func (") {
			rest := trimmed[5:]
			end := strings.IndexAny(rest, "([< ")
			if end > 0 {
				defined[rest[:end]] = true
			}
		}
		if strings.HasPrefix(trimmed, "var ") {
			rest := trimmed[4:]
			end := strings.IndexAny(rest, " =")
			if end > 0 {
				defined[rest[:end]] = true
			}
		}
	}
	// Also add well-known types from imports
	for _, kw := range []string{"Promise", "Headers", "Request", "Response", "FormData", "URL"} {
		defined[kw] = true
	}

	// Replace PascalCase[...] patterns that are not defined
	var result strings.Builder
	i := 0
	for i < len(raw) {
		if raw[i] >= 'A' && raw[i] <= 'Z' &&
			(i == 0 || raw[i-1] == ' ' || raw[i-1] == ')' || raw[i-1] == '(' || raw[i-1] == '*' || raw[i-1] == ',' || raw[i-1] == '[') {
			j := i
			for j < len(raw) && (raw[j] == '_' || (raw[j] >= 'a' && raw[j] <= 'z') || (raw[j] >= 'A' && raw[j] <= 'Z') || (j > i && raw[j] >= '0' && raw[j] <= '9')) {
				j++
			}
			name := raw[i:j]
			if j < len(raw) && raw[j] == '[' && !defined[name] {
				// Skip Name[...] → replace with "any"
				depth := 1
				k := j + 1
				for k < len(raw) && depth > 0 {
					if raw[k] == '[' {
						depth++
					} else if raw[k] == ']' {
						depth--
					}
					k++
				}
				result.WriteString("any")
				i = k
				continue
			}
		}
		result.WriteByte(raw[i])
		i++
	}
	return result.String()
}

// removeDuplicateTypeFuncDecls removes `type X = func(...)` lines when `func X(...)` exists.
func removeDuplicateTypeFuncDecls(src string) string {
	lines := strings.Split(src, "\n")
	// Collect declared function names
	funcNames := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") && !strings.HasPrefix(trimmed, "func (") {
			// func Name(... — extract name
			rest := trimmed[5:]
			end := strings.IndexAny(rest, "([< ")
			if end > 0 {
				funcNames[rest[:end]] = true
			}
		}
	}
	// Remove type aliases that conflict with function declarations
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " = func(") {
			// type Name = func(... — extract name
			rest := trimmed[5:]
			end := strings.IndexAny(rest, " =")
			if end > 0 {
				name := rest[:end]
				if funcNames[name] {
					continue // skip duplicate
				}
			}
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// fixTrailingFuncArgs adds trailing commas where a closing brace (from a func literal)
// is followed by a line starting with ) or , — Go requires a trailing comma in this case.
func fixTrailingFuncArgs(src string) string {
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines)-1; i++ {
		trimmed := strings.TrimRight(lines[i], " \t")
		nextTrimmed := strings.TrimSpace(lines[i+1])
		if strings.HasSuffix(trimmed, "}") && !strings.HasSuffix(trimmed, "},") {
			// Only add comma when } is a func literal closing brace, not an if/for/else block.
			if strings.HasPrefix(nextTrimmed, ")") || strings.HasPrefix(nextTrimmed, "})") {
				if isFuncLiteralClose(lines, i) {
					lines[i] = trimmed + ","
				}
			} else if strings.HasPrefix(nextTrimmed, ",") {
				if isFuncLiteralClose(lines, i) {
					lines[i] = trimmed + ","
					indent := lines[i+1][:len(lines[i+1])-len(strings.TrimLeft(lines[i+1], " \t"))]
					lines[i+1] = indent + strings.TrimLeft(nextTrimmed[1:], " ")
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// isFuncLiteralClose checks if the } at line idx closes a func literal that is
// a function argument (not the RHS of an assignment).
func isFuncLiteralClose(lines []string, idx int) bool {
	depth := 0
	for j := idx; j >= 0; j-- {
		trimmed := strings.TrimSpace(lines[j])
		for k := len(trimmed) - 1; k >= 0; k-- {
			ch := trimmed[k]
			if ch == '}' {
				depth++
			} else if ch == '{' {
				depth--
				if depth == 0 {
					if !strings.Contains(lines[j], "func") {
						return false
					}
					// Check if this func literal is the RHS of an assignment (= or :=).
					// e.g. "s[method] = func(...) {" → not a function arg, no comma needed.
					funcIdx := strings.Index(lines[j], "func")
					prefix := strings.TrimSpace(lines[j][:funcIdx])
					if strings.HasSuffix(prefix, "=") {
						return false
					}
					return true
				}
			}
		}
	}
	return false
}
