package ramune_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFsReadFileCallback(t *testing.T) {
	r := sharedNodeCompat(t)

	// Write a temp file to read back.
	tmp := filepath.Join(t.TempDir(), "cb_test.txt")
	if err := os.WriteFile(tmp, []byte("callback data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// fs.readFile uses setTimeout(cb, 0) so we need EvalAsync to drive the event loop.
	v, err := r.EvalAsync(`
		new Promise(function(resolve, reject) {
			var fs = require('fs');
			fs.readFile('` + tmp + `', 'utf8', function(err, data) {
				if (err) reject(err);
				else resolve(data);
			});
		})
	`)
	if err != nil {
		t.Fatalf("EvalAsync failed: %v", err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "callback data" {
		t.Fatalf("got %q, want %q", s, "callback data")
	}
}

func TestFsPromisesReadFile(t *testing.T) {
	r := sharedNodeCompat(t)

	tmp := filepath.Join(t.TempDir(), "promise_read.txt")
	if err := os.WriteFile(tmp, []byte("promise data"), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := r.EvalAsync(`
		(async function() {
			var fs = require('fs');
			return await fs.promises.readFile('` + tmp + `', 'utf8');
		})()
	`)
	if err != nil {
		t.Fatalf("EvalAsync failed: %v", err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "promise data" {
		t.Fatalf("got %q, want %q", s, "promise data")
	}
}

func TestFsPromisesWriteReadRoundTrip(t *testing.T) {
	r := sharedNodeCompat(t)

	tmp := filepath.Join(t.TempDir(), "roundtrip.txt")

	v, err := r.EvalAsync(`
		(async function() {
			var fs = require('fs');
			await fs.promises.writeFile('` + tmp + `', 'round trip content');
			return await fs.promises.readFile('` + tmp + `', 'utf8');
		})()
	`)
	if err != nil {
		t.Fatalf("EvalAsync failed: %v", err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "round trip content" {
		t.Fatalf("got %q, want %q", s, "round trip content")
	}

	// Verify file was actually written to disk.
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "round trip content") {
		t.Fatalf("file content: %q", string(data))
	}
}
