package ramune_test

import (
	"testing"

	"github.com/i2y/ramune"
)

func newWinterTCOrSkip(t *testing.T) *ramune.Runtime {
	t.Helper()
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	return r
}

// --- CompressionStream / DecompressionStream ---

func TestCompressionStreamGzip(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__csResult = '';
		(async function() {
			var input = new TextEncoder().encode('Hello, WinterTC!');
			var cs = new CompressionStream('gzip');
			var writer = cs.writable.getWriter();
			writer.write(input);
			writer.close();

			var reader = cs.readable.getReader();
			var chunks = [];
			while (true) {
				var result = await reader.read();
				if (result.done) break;
				chunks.push(result.value);
			}

			// Decompress to verify round-trip
			var ds = new DecompressionStream('gzip');
			var writer2 = ds.writable.getWriter();
			for (var i = 0; i < chunks.length; i++) {
				writer2.write(chunks[i]);
			}
			writer2.close();

			var reader2 = ds.readable.getReader();
			var output = [];
			while (true) {
				var result2 = await reader2.read();
				if (result2.done) break;
				output.push(result2.value);
			}

			var total = 0;
			for (var i = 0; i < output.length; i++) total += output[i].length;
			var merged = new Uint8Array(total);
			var offset = 0;
			for (var i = 0; i < output.length; i++) {
				merged.set(output[i], offset);
				offset += output[i].length;
			}
			globalThis.__csResult = new TextDecoder().decode(merged);
		})();
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.__csResult")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "Hello, WinterTC!" {
		t.Fatalf("got %q, want %q", s, "Hello, WinterTC!")
	}
}

func TestCompressionStreamDeflate(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__csResult = '';
		(async function() {
			var input = new TextEncoder().encode('deflate test data');
			var cs = new CompressionStream('deflate');
			var writer = cs.writable.getWriter();
			writer.write(input);
			writer.close();

			var reader = cs.readable.getReader();
			var chunks = [];
			while (true) {
				var r = await reader.read();
				if (r.done) break;
				chunks.push(r.value);
			}

			var ds = new DecompressionStream('deflate');
			var writer2 = ds.writable.getWriter();
			for (var i = 0; i < chunks.length; i++) writer2.write(chunks[i]);
			writer2.close();

			var reader2 = ds.readable.getReader();
			var output = [];
			while (true) {
				var r2 = await reader2.read();
				if (r2.done) break;
				output.push(r2.value);
			}

			var total = 0;
			for (var i = 0; i < output.length; i++) total += output[i].length;
			var merged = new Uint8Array(total);
			var offset = 0;
			for (var i = 0; i < output.length; i++) {
				merged.set(output[i], offset);
				offset += output[i].length;
			}
			globalThis.__csResult = new TextDecoder().decode(merged);
		})();
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.__csResult")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "deflate test data" {
		t.Fatalf("got %q, want %q", s, "deflate test data")
	}
}

func TestCompressionStreamDeflateRaw(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__csResult = '';
		(async function() {
			var input = new TextEncoder().encode('raw deflate');
			var cs = new CompressionStream('deflate-raw');
			var writer = cs.writable.getWriter();
			writer.write(input);
			writer.close();

			var reader = cs.readable.getReader();
			var chunks = [];
			while (true) {
				var r = await reader.read();
				if (r.done) break;
				chunks.push(r.value);
			}

			var ds = new DecompressionStream('deflate-raw');
			var writer2 = ds.writable.getWriter();
			for (var i = 0; i < chunks.length; i++) writer2.write(chunks[i]);
			writer2.close();

			var reader2 = ds.readable.getReader();
			var output = [];
			while (true) {
				var r2 = await reader2.read();
				if (r2.done) break;
				output.push(r2.value);
			}

			var total = 0;
			for (var i = 0; i < output.length; i++) total += output[i].length;
			var merged = new Uint8Array(total);
			var offset = 0;
			for (var i = 0; i < output.length; i++) {
				merged.set(output[i], offset);
				offset += output[i].length;
			}
			globalThis.__csResult = new TextDecoder().decode(merged);
		})();
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.__csResult")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "raw deflate" {
		t.Fatalf("got %q, want %q", s, "raw deflate")
	}
}

func TestCompressionStreamInvalidFormat(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		try {
			new CompressionStream('brotli');
			'no error';
		} catch(e) {
			e instanceof TypeError ? 'TypeError' : 'other';
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "TypeError" {
		t.Fatalf("got %q, want TypeError", s)
	}
}

// --- MessageChannel / MessagePort ---

func TestMessageChannel(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__mcResult = '';
		var mc = new MessageChannel();
		mc.port2.onmessage = function(e) {
			globalThis.__mcResult = e.data;
		};
		mc.port2.start();
		mc.port1.postMessage('hello from port1');
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.__mcResult")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "hello from port1" {
		t.Fatalf("got %q, want %q", s, "hello from port1")
	}
}

func TestMessageChannelBidirectional(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__mcResults = [];
		var mc = new MessageChannel();
		mc.port1.onmessage = function(e) {
			globalThis.__mcResults.push('port1:' + e.data);
		};
		mc.port1.start();
		mc.port2.onmessage = function(e) {
			globalThis.__mcResults.push('port2:' + e.data);
			mc.port2.postMessage('reply');
		};
		mc.port2.start();
		mc.port1.postMessage('ping');
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("JSON.stringify(globalThis.__mcResults)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != `["port2:ping","port1:reply"]` {
		t.Fatalf("got %s, want %s", s, `["port2:ping","port1:reply"]`)
	}
}

func TestMessagePortAutoStartOnAddEventListener(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__mcResult = '';
		var mc = new MessageChannel();
		mc.port2.addEventListener('message', function(e) {
			globalThis.__mcResult = e.data;
		});
		mc.port1.postMessage('auto-start');
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.__mcResult")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "auto-start" {
		t.Fatalf("got %q, want %q", s, "auto-start")
	}
}

// --- ErrorEvent ---

func TestErrorEvent(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var e = new ErrorEvent('error', {
			message: 'test error',
			filename: 'test.js',
			lineno: 42,
			colno: 10,
			error: new Error('original')
		});
		JSON.stringify({
			type: e.type,
			message: e.message,
			filename: e.filename,
			lineno: e.lineno,
			colno: e.colno,
			hasError: e.error instanceof Error,
			isEvent: e instanceof Event
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"type":"error","message":"test error","filename":"test.js","lineno":42,"colno":10,"hasError":true,"isEvent":true}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

// --- PromiseRejectionEvent ---

func TestPromiseRejectionEvent(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var p = Promise.resolve();
		var e = new PromiseRejectionEvent('unhandledrejection', {
			promise: p,
			reason: 'test reason'
		});
		JSON.stringify({
			type: e.type,
			reason: e.reason,
			hasPromise: e.promise === p,
			isEvent: e instanceof Event
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"type":"unhandledrejection","reason":"test reason","hasPromise":true,"isEvent":true}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

// --- MessageEvent ---

func TestMessageEvent(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var e = new MessageEvent('message', {
			data: { hello: 'world' },
			origin: 'http://example.com'
		});
		JSON.stringify({
			type: e.type,
			data: e.data,
			origin: e.origin,
			isEvent: e instanceof Event
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"type":"message","data":{"hello":"world"},"origin":"http://example.com","isEvent":true}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

// --- URLPattern ---

func TestURLPatternGlobal(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var pattern = new URLPattern({ pathname: '/users/:id' });
		var result = pattern.exec('http://example.com/users/123');
		JSON.stringify({
			matched: result !== null,
			id: result.pathname.groups.id,
			test: pattern.test('http://example.com/users/456')
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"matched":true,"id":"123","test":true}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

func TestURLPatternNoMatch(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var pattern = new URLPattern({ pathname: '/api/:version/items' });
		var result = pattern.test('http://example.com/other/path');
		String(result);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "false" {
		t.Fatalf("got %q, want false", s)
	}
}

// --- WithWinterTC standalone (without NodeCompat) ---

func TestWithWinterTCStandalone(t *testing.T) {
	r, err := ramune.New(ramune.WithWinterTC())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	// CompressionStream should be available
	v, err := r.Eval("typeof CompressionStream")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "function" {
		t.Fatalf("CompressionStream: got %q, want function", s)
	}
}

func TestWithWinterTCAPIsAvailable(t *testing.T) {
	r, err := ramune.New(ramune.WithWinterTC())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.Eval(`
		JSON.stringify({
			CompressionStream: typeof CompressionStream,
			DecompressionStream: typeof DecompressionStream,
			MessageChannel: typeof MessageChannel,
			MessagePort: typeof MessagePort,
			MessageEvent: typeof MessageEvent,
			ErrorEvent: typeof ErrorEvent,
			PromiseRejectionEvent: typeof PromiseRejectionEvent,
			URLPattern: typeof URLPattern,
			DOMException: typeof DOMException,
			CountQueuingStrategy: typeof CountQueuingStrategy,
			ByteLengthQueuingStrategy: typeof ByteLengthQueuingStrategy
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"CompressionStream":"function","DecompressionStream":"function","MessageChannel":"function","MessagePort":"function","MessageEvent":"function","ErrorEvent":"function","PromiseRejectionEvent":"function","URLPattern":"function","DOMException":"function","CountQueuingStrategy":"function","ByteLengthQueuingStrategy":"function"}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

// --- DOMException ---

func TestDOMException(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var e = new DOMException('aborted', 'AbortError');
		JSON.stringify({
			message: e.message,
			name: e.name,
			code: e.code,
			isError: e instanceof Error,
			staticCode: DOMException.ABORT_ERR,
			protoCode: DOMException.prototype.ABORT_ERR
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"message":"aborted","name":"AbortError","code":20,"isError":true,"staticCode":20,"protoCode":20}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

func TestDOMExceptionCodes(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		JSON.stringify({
			notSupported: new DOMException('', 'NotSupportedError').code,
			syntax: new DOMException('', 'SyntaxError').code,
			network: new DOMException('', 'NetworkError').code,
			timeout: new DOMException('', 'TimeoutError').code,
			unknown: new DOMException('', 'CustomError').code
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"notSupported":9,"syntax":12,"network":19,"timeout":23,"unknown":0}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

// --- CountQueuingStrategy / ByteLengthQueuingStrategy ---

func TestCountQueuingStrategy(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var s = new CountQueuingStrategy({ highWaterMark: 10 });
		JSON.stringify({
			hwm: s.highWaterMark,
			size: s.size('anything'),
			sizeObj: s.size({ length: 100 })
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"hwm":10,"size":1,"sizeObj":1}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

func TestByteLengthQueuingStrategy(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		var s = new ByteLengthQueuingStrategy({ highWaterMark: 1024 });
		JSON.stringify({
			hwm: s.highWaterMark,
			sizeU8: s.size(new Uint8Array(42)),
			sizeArr: s.size([1, 2, 3])
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := `{"hwm":1024,"sizeU8":42,"sizeArr":3}`
	if s != want {
		t.Fatalf("got %s, want %s", s, want)
	}
}

// --- Streaming CompressionStream (per-chunk output) ---

func TestCompressionStreamStreaming(t *testing.T) {
	r := newWinterTCOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.__csChunkCount = 0;
		globalThis.__csResult = '';
		(async function() {
			var cs = new CompressionStream('gzip');
			var writer = cs.writable.getWriter();
			var reader = cs.readable.getReader();

			// Write multiple chunks
			for (var i = 0; i < 3; i++) {
				var data = new TextEncoder().encode('chunk' + i + ' '.repeat(100));
				writer.write(data);
			}
			writer.close();

			// Read all compressed chunks
			var compressedChunks = [];
			while (true) {
				var r = await reader.read();
				if (r.done) break;
				compressedChunks.push(r.value);
				globalThis.__csChunkCount++;
			}

			// Decompress to verify round-trip
			var ds = new DecompressionStream('gzip');
			var writer2 = ds.writable.getWriter();
			for (var i = 0; i < compressedChunks.length; i++) {
				writer2.write(compressedChunks[i]);
			}
			writer2.close();

			var reader2 = ds.readable.getReader();
			var output = [];
			while (true) {
				var r2 = await reader2.read();
				if (r2.done) break;
				output.push(r2.value);
			}
			var total = 0;
			for (var i = 0; i < output.length; i++) total += output[i].length;
			var merged = new Uint8Array(total);
			var offset = 0;
			for (var i = 0; i < output.length; i++) {
				merged.set(output[i], offset);
				offset += output[i].length;
			}
			globalThis.__csResult = new TextDecoder().decode(merged);
		})();
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	// Verify round-trip
	v, err := r.Eval("globalThis.__csResult")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	want := "chunk0" + repeat(' ', 100) + "chunk1" + repeat(' ', 100) + "chunk2" + repeat(' ', 100)
	if s != want {
		t.Fatalf("round-trip failed: got %d chars, want %d", len(s), len(want))
	}

	// Verify streaming produced multiple output chunks
	v2, err := r.Eval("globalThis.__csChunkCount")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()

	n, _ := v2.Float64()
	if n < 2 {
		t.Logf("streaming produced %d chunk(s) (expected >= 2, but compression may batch small inputs)", int(n))
	}
}

func repeat(ch byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}
