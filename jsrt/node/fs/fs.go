// Package fs provides Node.js fs module equivalents for transpiled TypeScript code.
package fs

import (
	"io"
	"os"
	"path/filepath"
)

// ReadFileSync reads a file and returns its contents as a string.
func ReadFileSync(path string, encoding ...string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadFileSyncBytes reads a file and returns its contents as bytes.
func ReadFileSyncBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFileSync writes data to a file.
func WriteFileSync(path string, data string, mode ...os.FileMode) error {
	m := os.FileMode(0o644)
	if len(mode) > 0 {
		m = mode[0]
	}
	return os.WriteFile(path, []byte(data), m)
}

// ExistsSync returns true if the path exists.
func ExistsSync(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MkdirSync creates a directory. With recursive=true, creates parent directories.
func MkdirSync(path string, recursive ...bool) error {
	if len(recursive) > 0 && recursive[0] {
		return os.MkdirAll(path, 0o755)
	}
	return os.Mkdir(path, 0o755)
}

// ReaddirSync reads a directory and returns entry names.
func ReaddirSync(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

// StatSync returns file info.
func StatSync(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

// AppendFileSync appends data to a file.
func AppendFileSync(path string, data string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(data)
	return err
}

// RealpathSync returns the resolved absolute path.
func RealpathSync(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// CopyFileSync copies a file from src to dst.
func CopyFileSync(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err = io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return err
	}
	return dstFile.Close()
}

// RmSync removes a file or directory.
func RmSync(path string, recursive ...bool) error {
	if len(recursive) > 0 && recursive[0] {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// ChmodSync changes file permissions.
func ChmodSync(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}

// RenameSync renames (moves) a file.
func RenameSync(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

// UnlinkSync removes a file.
func UnlinkSync(path string) error {
	return os.Remove(path)
}
