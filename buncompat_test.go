package ramune_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBunFileReadWrite(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		Bun.write("/tmp/ramune_bun_test.txt", "bun compat test");
		var f = Bun.file("/tmp/ramune_bun_test.txt");
		var result = {
			name: f.name,
			exists: f.exists()
		};
		JSON.stringify(result);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != `{"name":"ramune_bun_test.txt","exists":true}` {
		t.Fatalf("got %s", s)
	}

	r.Exec(`require('fs').rmSync('/tmp/ramune_bun_test.txt')`)
}

func TestBunVersion(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`typeof Bun.serve === 'function' && typeof Bun.file === 'function'`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	b, _ := v.Bool()
	if !b {
		t.Fatal("Bun.serve or Bun.file not available")
	}
}

func TestBunBuild(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.js")
	os.WriteFile(entry, []byte(`export const hello = "world";`), 0644)
	outdir := filepath.Join(dir, "out")

	v, err := r.EvalAsync(`
		Bun.build({
			entrypoints: ["` + entry + `"],
			outdir: "` + outdir + `"
		}).then(function(result) {
			return JSON.stringify({success: result.success, count: result.outputs.length});
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	got := v.String()
	if got != `{"success":true,"count":1}` {
		t.Fatalf("got %s", got)
	}

	entries, _ := os.ReadDir(outdir)
	if len(entries) == 0 {
		t.Fatal("no output files")
	}
}

func TestBunBuildMinify(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.js")
	os.WriteFile(entry, []byte(`export function longFunctionName() { return 42; }`), 0644)
	outdir := filepath.Join(dir, "out")

	v, err := r.EvalAsync(`
		Bun.build({
			entrypoints: ["` + entry + `"],
			outdir: "` + outdir + `",
			minify: true
		}).then(function(result) {
			return JSON.stringify({success: result.success});
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if got := v.String(); got != `{"success":true}` {
		t.Fatalf("got %s", got)
	}
}

func TestBunBuildErrors(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.EvalAsync(`
		Bun.build({
			entrypoints: ["/nonexistent/file.js"]
		}).then(function(result) {
			return JSON.stringify({success: result.success, hasLogs: result.logs.length > 0});
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if got := v.String(); got != `{"success":false,"hasLogs":true}` {
		t.Fatalf("got %s", got)
	}
}
