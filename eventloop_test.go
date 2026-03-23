package ramune_test

import (
	"errors"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

func TestSetTimeout(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.fired = false;
		setTimeout(function() { globalThis.fired = true; }, 10);
	`); err != nil {
		t.Fatal(err)
	}

	// Before running the event loop, the callback hasn't fired.
	v, err := r.Eval("globalThis.fired")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := v.Bool()
	v.Close()
	if b {
		t.Fatal("callback should not have fired yet")
	}

	// Run the event loop.
	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v2, err := r.Eval("globalThis.fired")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	b2, _ := v2.Bool()
	if !b2 {
		t.Fatal("callback should have fired after RunEventLoop")
	}
}

func TestSetInterval(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.count = 0;
		var id = setInterval(function() {
			globalThis.count++;
			if (globalThis.count >= 3) clearInterval(id);
		}, 10);
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.count")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	n, _ := v.Float64()
	if n != 3.0 {
		t.Fatalf("got count=%f, want 3", n)
	}
}

func TestClearTimeout(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.fired = false;
		var id = setTimeout(function() { globalThis.fired = true; }, 10);
		clearTimeout(id);
	`); err != nil {
		t.Fatal(err)
	}

	// Event loop should exit immediately (no pending timers).
	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.fired")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	b, _ := v.Bool()
	if b {
		t.Fatal("cleared timeout should not fire")
	}
}

func TestTick(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.order = [];
		setTimeout(function() { globalThis.order.push('a'); }, 0);
		setTimeout(function() { globalThis.order.push('b'); }, 50);
	`); err != nil {
		t.Fatal(err)
	}

	// First tick should fire the immediate timer.
	pending, err := r.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("should still have pending timers")
	}

	v, err := r.Eval("JSON.stringify(globalThis.order)")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := v.GoString()
	v.Close()
	if s != `["a"]` {
		t.Fatalf("after first tick got %s, want [\"a\"]", s)
	}

	// Run the rest.
	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v2, err := r.Eval("JSON.stringify(globalThis.order)")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	s2, _ := v2.GoString()
	if s2 != `["a","b"]` {
		t.Fatalf("after RunEventLoop got %s, want [\"a\",\"b\"]", s2)
	}
}

func TestEvalAsync(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// EvalAsync with a setTimeout-based Promise.
	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			setTimeout(function() { resolve(42); }, 10);
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 42.0 {
		t.Fatalf("got %f, want 42", f)
	}
}

func TestEvalAsyncReject(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	_, err := r.EvalAsync(`
		new Promise(function(resolve, reject) {
			setTimeout(function() { reject(new Error('boom')); }, 10);
		})
	`)
	if err == nil {
		t.Fatal("expected error from rejected promise")
	}
	var jsErr *ramune.JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("expected *JSError, got %T: %v", err, err)
	}
	if jsErr.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestEvalAsyncSyncValue(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// EvalAsync should also work with non-Promise values.
	v, err := r.EvalAsync(`1 + 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 3.0 {
		t.Fatalf("got %f, want 3", f)
	}
}

func TestSetImmediate(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.order = [];
		setTimeout(function() { globalThis.order.push('timeout'); }, 0);
		setImmediate(function() { globalThis.order.push('immediate'); });
	`); err != nil {
		t.Fatal(err)
	}

	if err := r.RunEventLoop(); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("JSON.stringify(globalThis.order)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	// setImmediate should fire before setTimeout(0) since immediates
	// are processed first in each tick.
	if s != `["immediate","timeout"]` {
		t.Fatalf("got %s, want [\"immediate\",\"timeout\"]", s)
	}
}

func TestEventLoopTimeout(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// setInterval without clearInterval should hit the timeout.
	if err := r.Exec(`setInterval(function() {}, 10)`); err != nil {
		t.Fatal(err)
	}

	err := r.RunEventLoopFor(100 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestEvalAsyncChainedPromises(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			setTimeout(function() { resolve(10); }, 10);
		}).then(function(x) {
			return x * 2;
		}).then(function(x) {
			return x + 1;
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 21.0 {
		t.Fatalf("got %f, want 21 (10*2+1)", f)
	}
}
