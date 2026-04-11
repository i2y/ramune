package ramune

import (
	"encoding/json"

	"golang.org/x/term"
)

// goTTYIsatty checks if a file descriptor is a terminal.
func goTTYIsatty(args []any) (any, error) {
	if len(args) < 1 {
		return false, nil
	}
	fd, ok := args[0].(float64)
	if !ok {
		return false, nil
	}
	return term.IsTerminal(int(fd)), nil
}

// goTTYGetSize returns the terminal size as JSON {"columns":N,"rows":M}.
func goTTYGetSize(args []any) (any, error) {
	if len(args) < 1 {
		return `{"columns":80,"rows":24}`, nil
	}
	fd, ok := args[0].(float64)
	if !ok {
		return `{"columns":80,"rows":24}`, nil
	}
	w, h, err := term.GetSize(int(fd))
	if err != nil {
		return `{"columns":80,"rows":24}`, nil
	}
	b, _ := json.Marshal(map[string]int{"columns": w, "rows": h})
	return string(b), nil
}
