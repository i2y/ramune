package ramune

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type archiveOpts struct {
	Files  []string `json:"files"`
	Output string   `json:"output"`
	Gzip   bool     `json:"gzip"`
	Cwd    string   `json:"cwd"`
}

type extractOpts struct {
	Input  string `json:"input"`
	Output string `json:"output"`
	Gzip   bool   `json:"gzip"`
}

func goBunArchiveTar(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("Archive.tar: options required")
	}
	optsJSON, _ := args[0].(string)
	var opts archiveOpts
	if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
		return nil, fmt.Errorf("Archive.tar: %w", err)
	}
	if len(opts.Files) == 0 {
		return nil, fmt.Errorf("Archive.tar: files required")
	}

	var buf bytes.Buffer
	var w io.WriteCloser = &nopWriteCloser{&buf}
	if opts.Gzip {
		w = gzip.NewWriter(&buf)
	}
	tw := tar.NewWriter(w)

	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}

	for _, f := range opts.Files {
		path := f
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			tw.Close()
			w.Close()
			return nil, err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			tw.Close()
			w.Close()
			return nil, err
		}
		hdr.Name = f
		if err := tw.WriteHeader(hdr); err != nil {
			tw.Close()
			w.Close()
			return nil, err
		}
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				tw.Close()
				w.Close()
				return nil, err
			}
			if _, err := tw.Write(data); err != nil {
				tw.Close()
				w.Close()
				return nil, err
			}
		}
	}
	tw.Close()
	w.Close()

	if opts.Output != "" {
		return nil, os.WriteFile(opts.Output, buf.Bytes(), 0644)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func goBunArchiveUntar(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("Archive.untar: options required")
	}
	optsJSON, _ := args[0].(string)
	var opts extractOpts
	if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
		return nil, fmt.Errorf("Archive.untar: %w", err)
	}

	var data []byte
	var err error
	if opts.Input != "" {
		data, err = os.ReadFile(opts.Input)
		if err != nil {
			return nil, err
		}
	} else if len(args) >= 2 {
		b64, _ := args[1].(string)
		data, err = base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("Archive.untar: input required")
	}

	var r io.Reader = bytes.NewReader(data)
	if opts.Gzip {
		gr, err := gzip.NewReader(r)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		r = gr
	}

	outDir := opts.Output
	if outDir == "" {
		outDir = "."
	}

	tr := tar.NewReader(r)
	var files []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		target := filepath.Join(outDir, hdr.Name)
		// Prevent path traversal.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(outDir)) {
			continue
		}
		if hdr.Typeflag == tar.TypeDir {
			os.MkdirAll(target, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(target), 0755)
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, content, os.FileMode(hdr.Mode)); err != nil {
			return nil, err
		}
		files = append(files, hdr.Name)
	}
	out, _ := json.Marshal(files)
	return string(out), nil
}

type nopWriteCloser struct{ io.Writer }

func (n *nopWriteCloser) Close() error { return nil }

func (r *Runtime) installArchive() error {
	if err := r.registerFuncLocked("__go_bun_archive_tar", goBunArchiveTar); err != nil {
		return err
	}
	return r.registerFuncLocked("__go_bun_archive_untar", goBunArchiveUntar)
}
