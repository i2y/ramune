package ramune

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func goReadFile(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("readFileSync: path required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("readFileSync: path must be string")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func goWriteFile(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("writeFileSync: path and data required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("writeFileSync: path must be string")
	}
	data, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("writeFileSync: data must be string")
	}
	return nil, os.WriteFile(path, []byte(data), 0o644)
}

func goFileExists(args []any) (any, error) {
	if len(args) < 1 {
		return false, nil
	}
	path, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	_, err := os.Stat(path)
	return err == nil, nil
}

func goMkdir(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("mkdirSync: path required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("mkdirSync: path must be string")
	}
	return nil, os.MkdirAll(path, 0o755)
}

func goReaddir(args []any) (any, error) {
	if len(args) < 1 {
		return "[]", nil
	}
	path, ok := args[0].(string)
	if !ok {
		return "[]", nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	// Check if withFileTypes option is set.
	withFileTypes := false
	if len(args) > 1 {
		if s, ok := args[1].(string); ok && s == "true" {
			withFileTypes = true
		}
	}
	if withFileTypes {
		type dirent struct {
			Name   string `json:"name"`
			IsDir  bool   `json:"isDirectory"`
			IsFile bool   `json:"isFile"`
		}
		var result []dirent
		for _, e := range entries {
			result = append(result, dirent{
				Name:   e.Name(),
				IsDir:  e.IsDir(),
				IsFile: !e.IsDir(),
			})
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	out, _ := json.Marshal(names)
	return string(out), nil
}

func goStat(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("stat: path required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("stat: path must be string")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"isFile":      !info.IsDir(),
		"isDirectory": info.IsDir(),
		"size":        info.Size(),
		"mtimeMs":     float64(info.ModTime().UnixMilli()),
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func goAppendFile(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("appendFileSync: path and data required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("appendFileSync: path must be string")
	}
	data, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("appendFileSync: data must be string")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = f.WriteString(data)
	return nil, err
}

func goRealpath(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("realpathSync: path required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("realpathSync: path must be string")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func goAccess(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("accessSync: path required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("accessSync: path must be string")
	}
	_, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func goCopyFile(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("copyFileSync: src and dest required")
	}
	src, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("copyFileSync: src must be string")
	}
	dst, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("copyFileSync: dest must be string")
	}
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return nil, err
	}
	return nil, nil
}

func goRm(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("rmSync: path required")
	}
	path, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("rmSync: path must be string")
	}
	recursive := false
	if len(args) > 1 {
		if s, ok := args[1].(string); ok && s == "true" {
			recursive = true
		}
	}
	if recursive {
		return nil, os.RemoveAll(path)
	}
	return nil, os.Remove(path)
}

func goChmod(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("chmodSync: path and mode required")
	}
	path, _ := args[0].(string)
	mode, _ := args[1].(float64)
	return nil, os.Chmod(path, os.FileMode(int(mode)))
}

func goSymlink(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("symlinkSync: target and path required")
	}
	target, _ := args[0].(string)
	link, _ := args[1].(string)
	return nil, os.Symlink(target, link)
}

func goReadlink(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("readlinkSync: path required")
	}
	path, _ := args[0].(string)
	return os.Readlink(path)
}

func goRename(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("renameSync: oldPath and newPath required")
	}
	oldPath, _ := args[0].(string)
	newPath, _ := args[1].(string)
	return nil, os.Rename(oldPath, newPath)
}

func goCpSync(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("cpSync: src and dest required")
	}
	src, _ := args[0].(string)
	dest, _ := args[1].(string)

	return nil, filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
