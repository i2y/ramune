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

func TestPerformanceObserver(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var entries = [];
		var obs = new PerformanceObserver(function(list, observer) {
			entries = entries.concat(list.getEntries());
		});
		obs.observe({ entryTypes: ['mark', 'measure'] });
		performance.mark('start');
		performance.mark('end');
		performance.measure('duration', 'start', 'end');
		obs.disconnect();
		performance.mark('ignored');
		JSON.stringify({
			count: entries.length,
			types: entries.map(function(e) { return e.entryType; }),
			names: entries.map(function(e) { return e.name; })
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"count":3,"types":["mark","mark","measure"],"names":["start","end","duration"]}` {
		t.Fatalf("got %s", s)
	}
}

func TestBunMarkdown(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var html = Bun.markdown.html('# Hello\n\nThis is **bold**.');
		html;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	expected := "<h1>Hello</h1>\n<p>This is <strong>bold</strong>.</p>\n"
	if s != expected {
		t.Fatalf("got %q, expected %q", s, expected)
	}
}

func TestBunArchiveTarUntar(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		(function() {
			var fs = require('fs');
			var dir = '/tmp/ramune_archive_test';
			try { fs.mkdirSync(dir, { recursive: true }); } catch(e) {}
			fs.writeFileSync(dir + '/a.txt', 'hello');
			fs.writeFileSync(dir + '/b.txt', 'world');
			var b64 = Bun.Archive.tar({ files: ['a.txt', 'b.txt'], cwd: dir, gzip: true });
			var outDir = '/tmp/ramune_archive_out';
			try { fs.mkdirSync(outDir, { recursive: true }); } catch(e) {}
			var files = Bun.Archive.untar({ output: outDir, gzip: true }, b64);
			var a = fs.readFileSync(outDir + '/a.txt', 'utf8');
			var b = fs.readFileSync(outDir + '/b.txt', 'utf8');
			return JSON.stringify({ files: files, a: a, b: b });
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"files":["a.txt","b.txt"],"a":"hello","b":"world"}` {
		t.Fatalf("got %s", s)
	}
}

func TestBunCSRF(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var token = Bun.CSRF.generate('my-secret');
		var valid = Bun.CSRF.verify('my-secret', token);
		var invalid = Bun.CSRF.verify('wrong-secret', token);
		var tampered = Bun.CSRF.verify('my-secret', token + 'x');
		JSON.stringify({ valid: valid, invalid: invalid, tampered: tampered });
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"valid":true,"invalid":false,"tampered":false}` {
		t.Fatalf("got %s", s)
	}
}

func TestURLPatternBasic(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var p = new URLPattern({ pathname: '/users/:id' });
		var result = p.exec('http://example.com/users/42');
		JSON.stringify({
			matched: result !== null,
			id: result.pathname.groups.id
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"matched":true,"id":"42"}` {
		t.Fatalf("got %s", s)
	}
}

func TestURLPatternTest(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var p = new URLPattern({ pathname: '/api/*' });
		JSON.stringify([
			p.test('http://localhost/api/users'),
			p.test('http://localhost/other')
		]);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "[true,false]" {
		t.Fatalf("got %s", s)
	}
}

func TestBunJSONCParse(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var result = Bun.JSONC.parse('{\n  // comment\n  "name": "test",\n  "value": 42 /* inline */\n}');
		JSON.stringify(result);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != `{"name":"test","value":42}` {
		t.Fatalf("got %s", s)
	}
}

// Regression test for Headers constructor accepting Headers-like shims
// (e.g. Hono's HeaderRecord) by iterating entries() rather than storing by
// reference. Prior behaviour dropped Content-Type from Hono responses.
func TestHeadersConstructorShapes(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var results = {};

		// 1. Plain record
		var h1 = new Headers({ "Content-Type": "application/json", "X-Foo": "bar" });
		results.plain = [h1.get("content-type"), h1.get("x-foo")];

		// 2. Headers instance copy — must be independent
		var h2 = new Headers(h1);
		results.copy = [h2.get("content-type"), h2.get("x-foo"), h1 !== h2 && h2._h !== h1._h];

		// 3. Iterable of [name, value] pairs
		var h3 = new Headers([["content-type", "text/html"], ["x-foo", "baz"]]);
		results.pairs = [h3.get("content-type"), h3.get("x-foo")];

		// 4. Headers-like shim exposing entries() (matches Hono's HeaderRecord).
		class FakeHeaders {
			constructor(dict) { this._h = dict; }
			get(n) { return this._h[n.toLowerCase()] ?? null; }
			forEach(cb) { for (var k in this._h) cb(this._h[k], k); }
			*entries() { for (var k in this._h) yield [k, this._h[k]]; }
		}
		var fake = new FakeHeaders({ "content-type": "text/html; charset=UTF-8" });
		var resp = new Response("hi", { headers: fake });
		results.shim = [resp.headers.get("content-type"), resp.headers._h !== fake];

		// 5. Array-valued record (e.g. set-cookie)
		var h5 = new Headers({ "set-cookie": ["a=1", "b=2"] });
		results.array = h5.get("set-cookie");

		// 6. null / undefined init
		var h6 = new Headers();
		var h7 = new Headers(null);
		var h8 = new Headers(undefined);
		results.empty = [h6.get("x") === null, h7.get("x") === null, h8.get("x") === null];

		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"plain":["application/json","bar"],"copy":["application/json","bar",true],"pairs":["text/html","baz"],"shim":["text/html; charset=UTF-8",true],"array":"a=1, b=2","empty":[true,true,true]}`
	if s != want {
		t.Fatalf("got  %s\nwant %s", s, want)
	}
}
