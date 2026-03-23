package ramune_test

import (
	"os"
	"testing"

	"github.com/i2y/ramune"
)

func TestWorkerBasic(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Write a worker script that doubles workerData and sends it back.
	workerFile := "/tmp/ramune_test_worker_basic.js"
	err = os.WriteFile(workerFile, []byte(`
		var { parentPort, workerData } = require('worker_threads');
		parentPort.postMessage(workerData * 2);
	`), 0644)
	if err != nil {
		t.Fatalf("failed to write worker file: %v", err)
	}
	defer os.Remove(workerFile)

	// Create a worker with workerData 21, expect 42 back.
	v, err := rt.EvalAsync(`
		new Promise(function(resolve) {
			var { Worker } = require('worker_threads');
			var w = new Worker('` + workerFile + `', { workerData: 21 });
			w.on('message', function(msg) {
				w.terminate();
				resolve(msg);
			});
		})
	`)
	if err != nil {
		t.Fatalf("EvalAsync failed: %v", err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatalf("Float64 failed: %v", err)
	}
	if f != 42.0 {
		t.Fatalf("got %f, want 42", f)
	}
}

func TestWorkerIsMainThread(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`require('worker_threads').isMainThread`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer v.Close()

	b, err := v.Bool()
	if err != nil {
		t.Fatalf("Bool failed: %v", err)
	}
	if !b {
		t.Fatal("expected isMainThread to be true in parent")
	}
}

func TestWorkerPostMessage(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Write a worker script that echoes received messages back.
	workerFile := "/tmp/ramune_test_worker_echo.js"
	err = os.WriteFile(workerFile, []byte(`
		var { parentPort } = require('worker_threads');
		parentPort.on('message', function(msg) {
			parentPort.postMessage(msg);
		});
	`), 0644)
	if err != nil {
		t.Fatalf("failed to write worker file: %v", err)
	}
	defer os.Remove(workerFile)

	// Create worker, send a message, and wait for the echo.
	// Note: do not call w.terminate() inside the message handler because
	// the worker has an active event loop (setInterval from parentPort.on)
	// and terminate() would deadlock trying to Close() the worker runtime.
	// The worker is cleaned up when the parent runtime closes.
	v, err := rt.EvalAsync(`
		new Promise(function(resolve) {
			var { Worker } = require('worker_threads');
			var w = new Worker('` + workerFile + `', { workerData: null });
			w.on('message', function(msg) {
				resolve(msg);
			});
			// Give the worker a moment to start and register its listener,
			// then send the message.
			setTimeout(function() {
				w.postMessage("hello from parent");
			}, 100);
		})
	`)
	if err != nil {
		t.Fatalf("EvalAsync failed: %v", err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatalf("GoString failed: %v", err)
	}
	if s != "hello from parent" {
		t.Fatalf("got %q, want %q", s, "hello from parent")
	}
}
