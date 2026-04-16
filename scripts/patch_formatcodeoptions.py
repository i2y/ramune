#!/usr/bin/env python3
"""Patch formatcodeoptions.go to remove heavy dependencies (printer, tsoptions, lsproto)."""
import re
import sys

filepath = sys.argv[1]

with open(filepath) as f:
    content = f.read()

# Remove import lines for printer, tsoptions, lsproto
content = re.sub(r'\s*"[^"]*/(printer|tsoptions|lsp/lsproto)"\n', '\n', content)

# Replace printer.GetDefaultIndentSize() with 4
content = content.replace('printer.GetDefaultIndentSize()', '4')

# Replace tsoptions.ParseTristate(value) with parseTristate(value)
content = content.replace('tsoptions.ParseTristate(', 'parseTristate(')

# Replace tsoptions.ParseString(value) with parseString(value)
content = content.replace('tsoptions.ParseString(', 'parseString(')

# Replace core.GetNewLineKind(parseString(value)).GetNewLineCharacter()
content = content.replace(
    'core.GetNewLineKind(parseString(value)).GetNewLineCharacter()',
    'parseNewLineCharacter(value)'
)

# Remove FromLSFormatOptions function (uses lsproto)
content = re.sub(
    r'func FromLSFormatOptions\(.*?\n\}\n',
    '', content, flags=re.DOTALL
)

# Remove ToLSFormatOptions method (uses lsproto). Receiver may be value or
# pointer depending on tsgo version.
content = re.sub(
    r'func \(settings \*?FormatCodeSettings\) ToLSFormatOptions\(\).*?\n\}\n',
    '', content, flags=re.DOTALL
)

# Add inline helper functions before the type definitions
helpers = """
func parseTristate(v any) core.Tristate {
	switch val := v.(type) {
	case bool:
		if val {
			return core.TSTrue
		}
		return core.TSFalse
	case string:
		switch strings.ToLower(val) {
		case "true":
			return core.TSTrue
		case "false":
			return core.TSFalse
		}
	}
	return core.TSUnknown
}

func parseNewLineCharacter(v any) string {
	if s, ok := v.(string); ok {
		switch strings.ToLower(s) {
		case "\\r\\n", "crlf":
			return "\\r\\n"
		}
	}
	return "\\n"
}

func parseBoolWithDefault(val any, defaultV bool) bool {
	if v, ok := val.(bool); ok {
		return v
	}
	return defaultV
}

func parseIntWithDefault(val any, defaultV int) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return defaultV
}

"""

# Insert helpers before IndentStyle type
content = content.replace('type IndentStyle int', helpers + 'type IndentStyle int')

with open(filepath, 'w') as f:
    f.write(content)

print(f"Patched {filepath}")
