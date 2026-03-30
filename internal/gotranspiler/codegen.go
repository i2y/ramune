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

	// Fix common Go formatting issues before gofmt:
	// - "}\n)" → "},\n)" — trailing comma needed when func literal is last call arg
	// - "}\n\t)" and deeper indentation variants
	raw := out.String()
	raw = fixTrailingFuncArgs(raw)

	formatted, err := format.Source([]byte(raw))
	if err != nil {
		return raw, fmt.Errorf("gofmt error: %w", err)
	}

	return string(formatted), nil
}

// fixTrailingFuncArgs adds trailing commas where a closing brace (from a func literal)
// is followed by a line starting with ) or , — Go requires a trailing comma in this case.
func fixTrailingFuncArgs(src string) string {
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines)-1; i++ {
		trimmed := strings.TrimRight(lines[i], " \t")
		nextTrimmed := strings.TrimSpace(lines[i+1])
		if strings.HasSuffix(trimmed, "}") && !strings.HasSuffix(trimmed, "},") {
			if strings.HasPrefix(nextTrimmed, ")") || strings.HasPrefix(nextTrimmed, "})") {
				lines[i] = trimmed + ","
			} else if strings.HasPrefix(nextTrimmed, ",") {
				// }, followed by , "key": ... — add comma and remove leading comma on next line
				lines[i] = trimmed + ","
				indent := lines[i+1][:len(lines[i+1])-len(strings.TrimLeft(lines[i+1], " \t"))]
				lines[i+1] = indent + strings.TrimLeft(nextTrimmed[1:], " ")
			}
		}
	}
	return strings.Join(lines, "\n")
}
