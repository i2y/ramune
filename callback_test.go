package ramune_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/i2y/ramune"
	"sync"
)

var sharedNodeRT *ramune.Runtime
var sharedNodeOnce sync.Once

func sharedNodeCompat(t *testing.T) *ramune.Runtime {
	t.Helper()
	sharedNodeOnce.Do(func() {
		r, err := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
		if err != nil {
			return
		}
		sharedNodeRT = r
	})
	if sharedNodeRT == nil {
		t.Skip("JSC not available")
	}
	return sharedNodeRT
}

func TestRegisterFunc(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("add", func(args []any) (any, error) {
		a, _ := args[0].(float64)
		b, _ := args[1].(float64)
		return a + b, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("add(3, 4)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 7.0 {
		t.Fatalf("got %f, want 7.0", f)
	}
}

func TestRegisterFuncString(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("greet", func(args []any) (any, error) {
		name, _ := args[0].(string)
		return fmt.Sprintf("Hello, %s!", name), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`greet("World")`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "Hello, World!" {
		t.Fatalf("got %q, want %q", s, "Hello, World!")
	}
}

func TestRegisterFuncError(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("fail", func(args []any) (any, error) {
		return nil, fmt.Errorf("intentional error")
	})
	if err != nil {
		t.Fatal(err)
	}

	// The error should be catchable from JS.
	v, err := r.Eval(`
		var result;
		try { fail(); result = "no error"; }
		catch(e) { result = "caught: " + e; }
		result;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "caught: intentional error" {
		t.Fatalf("got %q", s)
	}
}

func TestRegisterFuncObjectArg(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("getAge", func(args []any) (any, error) {
		m, ok := args[0].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected map, got %T", args[0])
		}
		return m["age"], nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`getAge({name: "Alice", age: 30})`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 30.0 {
		t.Fatalf("got %f, want 30.0", f)
	}
}

func TestRegisterFuncArrayArg(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("sumArray", func(args []any) (any, error) {
		arr, ok := args[0].([]any)
		if !ok {
			return nil, fmt.Errorf("expected []any, got %T", args[0])
		}
		sum := 0.0
		for _, v := range arr {
			n, _ := v.(float64)
			sum += n
		}
		return sum, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`sumArray([1, 2, 3, 4])`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 10.0 {
		t.Fatalf("got %f, want 10.0", f)
	}
}

func TestJSFuncBasic(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("callWith42", func(args []any) (any, error) {
		fn, ok := args[0].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc, got %T", args[0])
		}
		defer fn.Close()
		return fn.Call(42.0)
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`callWith42(function(x) { return x * 2; })`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 84.0 {
		t.Fatalf("got %f, want 84.0", f)
	}
}

func TestJSFuncMultipleCalls(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("callThrice", func(args []any) (any, error) {
		fn, ok := args[0].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc, got %T", args[0])
		}
		defer fn.Close()
		sum := 0.0
		for i := 1; i <= 3; i++ {
			result, err := fn.Call(float64(i))
			if err != nil {
				return nil, err
			}
			n, _ := result.(float64)
			sum += n
		}
		return sum, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`callThrice(function(x) { return x * 10; })`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 60.0 {
		t.Fatalf("got %f, want 60.0 (10+20+30)", f)
	}
}

func TestJSFuncStringReturn(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("applyToHello", func(args []any) (any, error) {
		fn, ok := args[0].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc, got %T", args[0])
		}
		defer fn.Close()
		return fn.Call("hello")
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`applyToHello(function(s) { return s.toUpperCase(); })`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "HELLO" {
		t.Fatalf("got %q, want %q", s, "HELLO")
	}
}

func TestJSFuncMixedArgs(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("mixedArgs", func(args []any) (any, error) {
		prefix, _ := args[0].(string)
		fn, ok := args[1].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc at args[1], got %T", args[1])
		}
		defer fn.Close()
		n, _ := args[2].(float64)
		result, err := fn.Call(n)
		if err != nil {
			return nil, err
		}
		s, _ := result.(string)
		return prefix + s, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`mixedArgs("result:", function(x) { return String(x * 2); }, 21)`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "result:42" {
		t.Fatalf("got %q, want %q", s, "result:42")
	}
}

func TestJSFuncClosure(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("callFn", func(args []any) (any, error) {
		fn, ok := args[0].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc, got %T", args[0])
		}
		defer fn.Close()
		return fn.Call()
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`
		var captured = 99;
		callFn(function() { return captured; });
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 99.0 {
		t.Fatalf("got %f, want 99.0", f)
	}
}

func TestJSFuncCloseBeforeCall(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.RegisterFunc("closeAndCall", func(args []any) (any, error) {
		fn, ok := args[0].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc, got %T", args[0])
		}
		fn.Close()
		_, err := fn.Call()
		if err == nil {
			return nil, fmt.Errorf("expected error after Close, got nil")
		}
		return "got error", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`closeAndCall(function() { return 1; })`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "got error" {
		t.Fatalf("got %q, want %q", s, "got error")
	}
}

func TestJSFuncReentrant(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// Register a Go function that the JS callback will call.
	err := r.RegisterFunc("double", func(args []any) (any, error) {
		n, _ := args[0].(float64)
		return n * 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = r.RegisterFunc("callFnWith5", func(args []any) (any, error) {
		fn, ok := args[0].(*ramune.JSFunc)
		if !ok {
			return nil, fmt.Errorf("expected *JSFunc, got %T", args[0])
		}
		defer fn.Close()
		return fn.Call(5.0)
	})
	if err != nil {
		t.Fatal(err)
	}

	// JS function calls back into Go (double), testing re-entrance.
	v, err := r.Eval(`callFnWith5(function(x) { return double(x); })`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 10.0 {
		t.Fatalf("got %f, want 10.0", f)
	}
}

func TestNodeCompatFsReadWrite(t *testing.T) {
	r := sharedNodeCompat(t)

	// Write a file, read it back, verify content, then clean up.
	v, err := r.Eval(`
		var fs = require('fs');
		var tmpFile = require('os').tmpdir() + '/ramune_test_rw.txt';
		fs.writeFileSync(tmpFile, 'hello ramune');
		var content = fs.readFileSync(tmpFile, 'utf8');
		fs.rmSync(tmpFile);
		content;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello ramune" {
		t.Fatalf("got %q, want %q", s, "hello ramune")
	}
}

func TestNodeCompatFsPromises(t *testing.T) {
	r := sharedNodeCompat(t)

	// Write a file synchronously, then read it back via fs.promises (async).
	if err := r.Exec(`
		var fs = require('fs');
		var tmpFile = require('os').tmpdir() + '/ramune_test_promises.txt';
		fs.writeFileSync(tmpFile, 'promise test');
	`); err != nil {
		t.Fatal(err)
	}

	v, err := r.EvalAsync(`
		fs.promises.readFile(tmpFile, 'utf8')
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "promise test" {
		t.Fatalf("got %q, want %q", s, "promise test")
	}

	// Clean up.
	r.Exec(`require('fs').rmSync(tmpFile)`)
}

func TestNodeCompatSpawnChunked(t *testing.T) {
	r := sharedNodeCompat(t)

	// Spawn printf with multiple lines, verify data is received.
	// Use EvalAsync because spawn defers execution via setImmediate.
	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			var cp = require('child_process');
			var data = '';
			var child = cp.spawn('printf', ['line1\\nline2\\nline3\\n']);
			child.stdout.on('data', function(chunk) { data += chunk; });
			child.on('close', function() { resolve(data); });
			child.stdin.end();
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\nline3\n"
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}

func TestNodeCompatFsExtra(t *testing.T) {
	r := sharedNodeCompat(t)

	// Test realpathSync, accessSync, copyFileSync, rmSync.
	v, err := r.Eval(`
		var fs = require('fs');
		var os = require('os');
		var tmpDir = os.tmpdir();
		var src = tmpDir + '/ramune_test_copy_src.txt';
		var dst = tmpDir + '/ramune_test_copy_dst.txt';
		var results = [];

		fs.writeFileSync(src, 'copy me');

		// realpathSync
		var real = fs.realpathSync(tmpDir);
		results.push(typeof real === 'string' && real.length > 0);

		// accessSync — should not throw for existing file
		try { fs.accessSync(src); results.push(true); }
		catch(e) { results.push(false); }

		// copyFileSync
		fs.copyFileSync(src, dst);
		results.push(fs.readFileSync(dst, 'utf8') === 'copy me');

		// rmSync
		fs.rmSync(src);
		fs.rmSync(dst);
		results.push(!fs.existsSync(src));
		results.push(!fs.existsSync(dst));

		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "[true,true,true,true,true]" {
		t.Fatalf("got %s", s)
	}
}

func TestNodeCompatPathComplete(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var p = require('path');
		var results = [];
		results.push(p.relative('/a/b/c', '/a/d') === '../../d');
		results.push(p.normalize('/a/b/../c') === '/a/c');
		results.push(p.isAbsolute('/foo') === true);
		results.push(p.isAbsolute('foo') === false);
		var parsed = p.parse('/home/user/file.txt');
		results.push(parsed.root === '/');
		results.push(parsed.dir === '/home/user');
		results.push(parsed.base === 'file.txt');
		results.push(parsed.ext === '.txt');
		results.push(parsed.name === 'file');
		results.push(p.format(parsed) === '/home/user/file.txt');
		results.push(p.basename('file.html', '.html') === 'file');
		results.push(p.delimiter === ':');
		results.push(p.posix === p);
		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := "[true,true,true,true,true,true,true,true,true,true,true,true,true]"
	if s != want {
		t.Fatalf("got %s", s)
	}
}

func TestNodeCompat(t *testing.T) {
	r := sharedNodeCompat(t)

	// Test process.env
	v, err := r.Eval(`process.env.HOME`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s == "" {
		t.Fatal("process.env.HOME is empty")
	}

	// Test path.join
	v2, err := r.Eval(`require('path').join('/foo', 'bar', 'baz')`)
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	s2, _ := v2.GoString()
	if s2 != "/foo/bar/baz" {
		t.Fatalf("path.join got %q, want /foo/bar/baz", s2)
	}

	// Test fs.existsSync
	v3, err := r.Eval(`require('fs').existsSync('/tmp')`)
	if err != nil {
		t.Fatal(err)
	}
	defer v3.Close()
	b, _ := v3.Bool()
	if !b {
		t.Fatal("fs.existsSync('/tmp') should be true")
	}

	// Test child_process.spawnSync
	v4, err := r.Eval(`
		var cp = require('child_process');
		var result = cp.spawnSync('echo', ['hello']);
		result.stdout.trim();
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	s4, _ := v4.GoString()
	if s4 != "hello" {
		t.Fatalf("spawnSync echo got %q, want 'hello'", s4)
	}
}

func TestCryptoRandomBytes(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var buf = crypto.randomBytes(16);
		var hex = buf.toString('hex');
		// Should be 32 hex chars (16 bytes).
		hex.length === 32 ? hex : 'wrong length: ' + hex.length;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 32 {
		t.Fatalf("expected 32 hex chars, got %q", s)
	}

	// Verify two calls produce different values.
	v2, err := r.Eval(`crypto.randomBytes(16).toString('hex')`)
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	s2, _ := v2.GoString()
	if s == s2 {
		t.Fatal("two randomBytes calls returned the same value")
	}
}

func TestCryptoHash(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		crypto.createHash('sha256').update('hello').digest('hex');
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	// SHA256("hello") is well-known.
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}

func TestCryptoHmac(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		crypto.createHmac('sha256', 'secret').update('hello').digest('hex');
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	// HMAC-SHA256("hello", "secret") is well-known.
	want := "88aab3ede8d3adf94d26ab90d3bafd4a2083070c3bcce9c014ee04a443847c0b"
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}

func TestCryptoScrypt(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var key = crypto.scryptSync('password', 'salt', 32);
		key.toString('hex');
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	// scrypt('password', 'salt', N=16384, r=8, p=1, keylen=32) is deterministic.
	if len(s) != 64 {
		t.Fatalf("expected 64 hex chars, got %d: %s", len(s), s)
	}
}

func TestCryptoPbkdf2(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var key = crypto.pbkdf2Sync('password', 'salt', 100000, 32, 'sha256');
		key.toString('hex');
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	// PBKDF2-SHA256('password', 'salt', 100000, 32) is well-known.
	want := "0394a2ede332c9a13eb82e9b24631604c31df978b4e2f0fbd2c549944f9d79a5"
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}

func TestNodeCompatOsHostname(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var os = require('os');
		var results = [];
		results.push(os.hostname().length > 0);
		results.push(os.type() === 'Darwin' || os.type() === 'Linux');
		results.push(os.cpus().length > 0);
		results.push(os.totalmem() > 0);
		results.push(typeof os.userInfo().username === 'string');
		results.push(os.endianness() === 'LE');
		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "[true,true,true,true,true,true]" {
		t.Fatalf("got %s", s)
	}
}

func TestZlibGzipRoundTrip(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var zlib = require('zlib');
		var original = 'hello zlib compression test';
		var compressed = zlib.gzipSync(original);
		var decompressed = zlib.gunzipSync(compressed);
		decompressed.toString();
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "hello zlib compression test" {
		t.Fatalf("got %q, want %q", s, "hello zlib compression test")
	}
}

func TestTextEncoderDecoder(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var enc = new TextEncoder();
		var dec = new TextDecoder();
		var encoded = enc.encode('hello');
		var decoded = dec.decode(encoded);
		JSON.stringify({
			len: encoded.length,
			match: decoded === 'hello'
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"len":5,"match":true}` {
		t.Fatalf("got %s", s)
	}
}

func TestUrlParse(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var url = require('url');
		var parsed = url.parse('https://user:pass@example.com:8080/path?q=1#hash');
		var results = [];
		results.push(parsed.protocol === 'https:');
		results.push(parsed.hostname === 'example.com');
		results.push(parsed.port === '8080');
		results.push(parsed.pathname === '/path');
		results.push(parsed.search === '?q=1');
		results.push(parsed.hash === '#hash');
		results.push(parsed.auth === 'user:pass');
		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "[true,true,true,true,true,true,true]" {
		t.Fatalf("got %s", s)
	}
}

func TestQuerystring(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var qs = require('querystring');
		var str = qs.stringify({a: '1', b: 'hello world'});
		var parsed = qs.parse(str);
		JSON.stringify({str: str, a: parsed.a, b: parsed.b});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"str":"a=1&b=hello%20world","a":"1","b":"hello world"}` {
		t.Fatalf("got %s", s)
	}
}

func TestStreamReadable(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var stream = require('stream');
		var chunks = [];
		var rs = new stream.Readable({ read: function() {} });
		rs.on('data', function(chunk) { chunks.push(chunk); });
		rs.on('end', function() { chunks.push('END'); });
		rs.push('hello ');
		rs.push('world');
		rs.push(null);
		JSON.stringify(chunks);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `["hello ","world","END"]` {
		t.Fatalf("got %s", s)
	}
}

func TestStreamPipe(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var stream = require('stream');
		var output = [];
		var rs = new stream.Readable({ read: function() {} });
		var ws = new stream.Writable({
			write: function(chunk, enc, cb) {
				output.push(chunk.toUpperCase());
				cb();
			}
		});
		rs.pipe(ws);
		rs.push('hello');
		rs.push(' world');
		rs.push(null);
		JSON.stringify(output);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `["HELLO"," WORLD"]` {
		t.Fatalf("got %s", s)
	}
}

func TestStreamTransform(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var stream = require('stream');
		var result = [];
		var upper = new stream.Transform({
			transform: function(chunk, enc, cb) { cb(null, chunk.toUpperCase()); }
		});
		upper.on('data', function(d) { result.push(d); });
		upper.write('hello');
		upper.write('world');
		JSON.stringify(result);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `["HELLO","WORLD"]` {
		t.Fatalf("got %s", s)
	}
}

func TestHttpRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	r := sharedNodeCompat(t)

	v, err := r.Eval(fmt.Sprintf(`
		var http = require('http');
		var body = '';
		var statusCode = 0;
		http.get('%s', function(res) {
			statusCode = res.statusCode;
			res.on('data', function(chunk) { body += chunk; });
		});
		JSON.stringify({statusCode: statusCode, body: body});
	`, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"statusCode":200,"body":"{\"ok\":true}"}` {
		t.Fatalf("got %s", s)
	}
}

func TestHttpPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "got:%s", body)
	}))
	defer srv.Close()

	r := sharedNodeCompat(t)

	// Parse the URL to get hostname and port for the options object.
	v, err := r.Eval(fmt.Sprintf(`
		var http = require('http');
		var url = require('url');
		var parsed = url.parse('%s');
		var body = '';
		var req = http.request({
			hostname: parsed.hostname,
			port: parsed.port,
			path: '/',
			method: 'POST',
			headers: {'Content-Type': 'text/plain'}
		}, function(res) {
			res.on('data', function(chunk) { body += chunk; });
		});
		req.write('hello');
		req.end();
		body;
	`, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "got:hello" {
		t.Fatalf("got %q, want %q", s, "got:hello")
	}
}

func TestBufferReadWrite(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var buf = Buffer.alloc(4);
		buf.writeUInt8(0xCA, 0);
		buf.writeUInt8(0xFE, 1);
		buf.writeUInt16BE(0x1234, 2);
		JSON.stringify([buf.readUInt8(0), buf.readUInt8(1), buf.readUInt16BE(2)]);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "[202,254,4660]" {
		t.Fatalf("got %s", s)
	}
}

func TestCryptoAesCbc(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var key = Buffer.from('0123456789abcdef0123456789abcdef');
		var iv = Buffer.from('abcdef0123456789');
		var cipher = crypto.createCipheriv('aes-256-cbc', key, iv);
		cipher.update('secret message');
		var encrypted = cipher.final('hex');
		var decipher = crypto.createDecipheriv('aes-256-cbc', key, iv);
		decipher.update(encrypted, 'hex');
		var decrypted = decipher.final('utf8');
		decrypted;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "secret message" {
		t.Fatalf("got %q, want %q", s, "secret message")
	}
}

func TestCryptoRandomInt(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var results = [];
		for (var i = 0; i < 100; i++) {
			var n = crypto.randomInt(10, 20);
			results.push(n >= 10 && n < 20);
		}
		results.every(function(x) { return x; });
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	b, _ := v.Bool()
	if !b {
		t.Fatal("randomInt values out of range")
	}
}

func TestCryptoSignVerifyRSA(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var keys = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
		var sign = crypto.createSign('RSA-SHA256');
		sign.update('hello world');
		var sig = sign.sign(keys.privateKey, 'hex');
		var verify = crypto.createVerify('RSA-SHA256');
		verify.update('hello world');
		var valid = verify.verify(keys.publicKey, sig, 'hex');
		valid === true ? 'ok' : 'fail:' + valid;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.String() != "ok" {
		t.Fatalf("RSA sign/verify failed: %s", v.String())
	}
}

func TestCryptoSignVerifyEC(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var keys = crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' });
		var sign = crypto.createSign('SHA256');
		sign.update('test data');
		var sig = sign.sign(keys.privateKey, 'hex');
		var verify = crypto.createVerify('SHA256');
		verify.update('test data');
		var valid = verify.verify(keys.publicKey, sig, 'hex');
		valid === true ? 'ok' : 'fail';
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.String() != "ok" {
		t.Fatalf("EC sign/verify failed: %s", v.String())
	}
}

func TestCryptoSignVerifyWrongData(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var crypto = require('crypto');
		var keys = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
		var sign = crypto.createSign('RSA-SHA256');
		sign.update('hello world');
		var sig = sign.sign(keys.privateKey, 'hex');
		var verify = crypto.createVerify('RSA-SHA256');
		verify.update('wrong data');
		var valid = verify.verify(keys.publicKey, sig, 'hex');
		valid === false ? 'ok' : 'fail';
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.String() != "ok" {
		t.Fatalf("expected verification to fail: %s", v.String())
	}
}

func TestAssertModule(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var assert = require('assert');
		var results = [];
		assert.ok(true);
		results.push('ok');
		assert.strictEqual(1, 1);
		results.push('strictEqual');
		assert.deepStrictEqual({a:1}, {a:1});
		results.push('deepStrictEqual');
		assert.throws(function() { throw new Error('x'); });
		results.push('throws');
		assert.doesNotThrow(function() {});
		results.push('doesNotThrow');
		try { assert.strictEqual(1, 2); } catch(e) { results.push('caught:' + e.name); }
		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `["ok","strictEqual","deepStrictEqual","throws","doesNotThrow","caught:AssertionError"]` {
		t.Fatalf("got %s", s)
	}
}

func TestFsCreateReadStream(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var fs = require('fs');
		var os = require('os');
		var tmpFile = os.tmpdir() + '/ramune_test_stream.txt';
		fs.writeFileSync(tmpFile, 'stream test data');
		var chunks = [];
		var rs = fs.createReadStream(tmpFile);
		rs.on('data', function(chunk) { chunks.push(chunk); });
		var result = chunks.join('');
		fs.rmSync(tmpFile);
		result;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "stream test data" {
		t.Fatalf("got %q", s)
	}
}

func TestProcessHrtime(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var t1 = process.hrtime();
		var results = [];
		results.push(Array.isArray(t1));
		results.push(t1.length === 2);
		results.push(t1[0] > 0);
		var t2 = process.hrtime(t1);
		results.push(t2[0] >= 0);
		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "[true,true,true,true]" {
		t.Fatalf("got %s", s)
	}
}

func TestNodeCompatRequire(t *testing.T) {
	r := sharedNodeCompat(t)

	// Test that require returns polyfilled modules.
	v, err := r.Eval(`
		var results = [];
		results.push(typeof require('path').join === 'function');
		results.push(typeof require('fs').readFileSync === 'function');
		results.push(typeof require('child_process').spawnSync === 'function');
		results.push(typeof require('events').EventEmitter === 'function');
		results.push(typeof require('os').platform === 'function');
		JSON.stringify(results);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "[true,true,true,true,true]" {
		t.Fatalf("require results: %s", s)
	}
}

func TestDispatcherCallbackLimit(t *testing.T) {
	// Verify that many Runtimes can be created without hitting callback limit.
	for i := 0; i < 20; i++ {
		r, err := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
		if err != nil {
			t.Skipf("JSC not available: %v", err)
		}
		r.Close()
	}
}

func TestRegisterFuncMany(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// Register 100 functions via single dispatcher.
	for i := 0; i < 100; i++ {
		n := i
		err := r.RegisterFunc(fmt.Sprintf("fn%d", i), func(args []any) (any, error) {
			return float64(n), nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	v, err := r.Eval("fn0() + fn50() + fn99()")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 149.0 {
		t.Fatalf("got %f, want 149 (0+50+99)", f)
	}
}
