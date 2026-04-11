package ramune

import (
	"encoding/json"

	"golang.org/x/term"
)

const defaultTermSize = `{"columns":80,"rows":24}`

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

func goTTYGetSize(args []any) (any, error) {
	if len(args) < 1 {
		return defaultTermSize, nil
	}
	fd, ok := args[0].(float64)
	if !ok {
		return defaultTermSize, nil
	}
	w, h, err := term.GetSize(int(fd))
	if err != nil {
		return defaultTermSize, nil
	}
	b, _ := json.Marshal(map[string]int{"columns": w, "rows": h})
	return string(b), nil
}
