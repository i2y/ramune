package ramune

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func goPathJoin(args []any) (any, error) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		}
	}
	return filepath.Join(parts...), nil
}

func goPathResolve(args []any) (any, error) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return os.Getwd()
	}
	p := filepath.Join(parts...)
	if !filepath.IsAbs(p) {
		cwd, _ := os.Getwd()
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p), nil
}

func goPathDirname(args []any) (any, error) {
	if len(args) < 1 {
		return ".", nil
	}
	s, ok := args[0].(string)
	if !ok {
		return ".", nil
	}
	return filepath.Dir(s), nil
}

func goPathBasename(args []any) (any, error) {
	if len(args) < 1 {
		return "", nil
	}
	s, ok := args[0].(string)
	if !ok {
		return "", nil
	}
	return filepath.Base(s), nil
}

func goPathRelative(args []any) (any, error) {
	if len(args) < 2 {
		return "", nil
	}
	from, _ := args[0].(string)
	to, _ := args[1].(string)
	rel, err := filepath.Rel(from, to)
	if err != nil {
		return to, nil
	}
	return rel, nil
}

func goPathNormalize(args []any) (any, error) {
	if len(args) < 1 {
		return ".", nil
	}
	p, _ := args[0].(string)
	return filepath.Clean(p), nil
}

func goPathIsAbsolute(args []any) (any, error) {
	if len(args) < 1 {
		return false, nil
	}
	p, _ := args[0].(string)
	return filepath.IsAbs(p), nil
}

func goEnviron(args []any) (any, error) {
	env := os.Environ()
	result := make(map[string]string, len(env))
	for _, e := range env {
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			result[e[:idx]] = e[idx+1:]
		}
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func goGetenv(args []any) (any, error) {
	if len(args) < 1 {
		return "", nil
	}
	key, ok := args[0].(string)
	if !ok {
		return "", nil
	}
	return os.Getenv(key), nil
}

func goCwd(args []any) (any, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return d, nil
}

func goHrtime(args []any) (any, error) {
	ns := time.Now().UnixNano()
	sec := ns / 1e9
	nsec := ns % 1e9
	result := fmt.Sprintf("[%d,%d]", sec, nsec)
	return result, nil
}
