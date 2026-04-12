package ramune_test

import (
	"os"
	"testing"
)

func TestSharedArrayBufferBasic(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var sab = new SharedArrayBuffer(16);
			var view = new Int32Array(sab);
			view[0] = 42;
			view[1] = 100;
			return JSON.stringify({
				byteLength: sab.byteLength,
				len: view.length,
				v0: view[0],
				v1: view[1]
			});
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	expected := `{"byteLength":16,"len":4,"v0":42,"v1":100}`
	if got := v.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestAtomicsLoadStore(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var sab = new SharedArrayBuffer(16);
			var view = new Int32Array(sab);
			Atomics.store(view, 0, 42);
			Atomics.store(view, 1, 99);
			var v0 = Atomics.load(view, 0);
			var v1 = Atomics.load(view, 1);
			return JSON.stringify({v0: v0, v1: v1});
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	expected := `{"v0":42,"v1":99}`
	if got := v.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestAtomicsAddSub(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var sab = new SharedArrayBuffer(8);
			var view = new Int32Array(sab);
			Atomics.store(view, 0, 10);
			var oldAdd = Atomics.add(view, 0, 5);
			var afterAdd = Atomics.load(view, 0);
			var oldSub = Atomics.sub(view, 0, 3);
			var afterSub = Atomics.load(view, 0);
			return JSON.stringify({oldAdd: oldAdd, afterAdd: afterAdd, oldSub: oldSub, afterSub: afterSub});
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	expected := `{"oldAdd":10,"afterAdd":15,"oldSub":15,"afterSub":12}`
	if got := v.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestAtomicsCompareExchange(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var sab = new SharedArrayBuffer(8);
			var view = new Int32Array(sab);
			Atomics.store(view, 0, 5);
			var old1 = Atomics.compareExchange(view, 0, 5, 10);
			var val1 = Atomics.load(view, 0);
			var old2 = Atomics.compareExchange(view, 0, 5, 20);
			var val2 = Atomics.load(view, 0);
			return JSON.stringify({old1: old1, val1: val1, old2: old2, val2: val2});
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	// First CAS succeeds (5→10), second fails (expected 5, actual 10)
	expected := `{"old1":5,"val1":10,"old2":10,"val2":10}`
	if got := v.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestAtomicsIsLockFree(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		JSON.stringify({
			s1: Atomics.isLockFree(1),
			s2: Atomics.isLockFree(2),
			s4: Atomics.isLockFree(4),
			s8: Atomics.isLockFree(8),
			s3: Atomics.isLockFree(3)
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	expected := `{"s1":true,"s2":true,"s4":true,"s8":true,"s3":false}`
	if got := v.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestSharedArrayBufferWorker(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	workerFile := "/tmp/ramune_test_sab_worker.js"
	err := os.WriteFile(workerFile, []byte(`
		var { parentPort, workerData } = require('worker_threads');
		// workerData contains the SharedArrayBuffer
		var sab = workerData;
		var view = new Int32Array(sab);
		Atomics.store(view, 0, 42);
		parentPort.postMessage("done");
	`), 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(workerFile)

	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			var { Worker } = require('worker_threads');
			var sab = new SharedArrayBuffer(16);
			var view = new Int32Array(sab);
			Atomics.store(view, 0, 0);

			var w = new Worker('` + workerFile + `', { workerData: sab });
			w.on('message', function(msg) {
				// Worker wrote 42 to shared memory
				var val = Atomics.load(view, 0);
				w.terminate();
				resolve(val);
			});
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatalf("Float64: %v", err)
	}
	if f != 42 {
		t.Errorf("got %v, want 42 (shared memory not visible)", f)
	}
}

func TestSharedArrayBufferSlice(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var sab = new SharedArrayBuffer(16);
			var view = new Int32Array(sab);
			view[0] = 10; view[1] = 20; view[2] = 30; view[3] = 40;
			var sliced = sab.slice(4, 12);
			var slicedView = new Int32Array(sliced);
			return JSON.stringify({
				byteLength: sliced.byteLength,
				v0: slicedView[0],
				v1: slicedView[1]
			});
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	expected := `{"byteLength":8,"v0":20,"v1":30}`
	if got := v.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestAtomicsWaitAsync(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			var sab = new SharedArrayBuffer(4);
			var view = new Int32Array(sab);
			Atomics.store(view, 0, 0);

			var result = Atomics.waitAsync(view, 0, 0);
			if (!result.async) { resolve('not async'); return; }

			result.value.then(function(r) {
				resolve(r.value);
			});

			setTimeout(function() {
				Atomics.store(view, 0, 1);
				Atomics.notify(view, 0, 1);
			}, 10);
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "ok" {
		t.Fatalf("got %q, want ok", s)
	}
}

func TestAtomicsNotifyCount(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var sab = new SharedArrayBuffer(4);
			var view = new Int32Array(sab);
			// No waiters, so notify should return 0.
			var n = Atomics.notify(view, 0, 5);
			return n;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 0 {
		t.Fatalf("got %v, want 0 (no waiters)", f)
	}
}
