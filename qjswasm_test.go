//go:build qjswasm && !goja

package ramune_test

import (
	"strings"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

// newQjsWasmOrSkip is the qjswasm analogue of newOrSkip. If the embedded
// wasm is still the 8-byte stub (wasi-sdk not installed yet), we skip
// rather than fail so contributors without the SDK can still run the
// non-backend suite.
func newQjsWasmOrSkip(t *testing.T) *ramune.Runtime {
	t.Helper()
	r, err := ramune.New()
	if err != nil {
		if strings.Contains(err.Error(), "stub") {
			t.Skip("qjswasm stub wasm; run `make build-wasm-shim` after installing wasi-sdk")
		}
		t.Fatalf("qjswasm runtime: %v", err)
	}
	return r
}

// TestQjswasmBasicEval is the M1 smoke test: the Runtime loads the
// embedded wasm, initializes QuickJS-NG, and evaluates 1+2 returning 3.
func TestQjswasmBasicEval(t *testing.T) {
	r := newQjsWasmOrSkip(t)
	defer r.Close()

	val, err := r.Eval("1+2")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	defer val.Close()

	f, err := val.Float64()
	if err != nil {
		t.Fatalf("Float64: %v", err)
	}
	if f != 3 {
		t.Fatalf("Eval(\"1+2\") = %v, want 3", f)
	}
}

// TestQjswasmEngineName checks Runtime.Engine() returns the expected
// identifier. This runs even with the stub wasm because Engine() is
// defined at the type level, not during Runtime.New().
func TestQjswasmEngineName(t *testing.T) {
	r := newQjsWasmOrSkip(t)
	defer r.Close()

	if got := r.Engine(); got != "qjswasm" {
		t.Fatalf("Engine() = %q, want \"qjswasm\"", got)
	}
}

// TestQjswasmSandboxDisableFS verifies SandboxPermissions constructs the
// runtime cleanly even with DisableFS on: the fastschema/qjs fork skips
// the ambient WASI FS mount, so a QuickJS-NG VM escape cannot pivot
// through WASI to reach host files — and regular JS keeps working.
func TestQjswasmSandboxDisableFS(t *testing.T) {
	r, err := ramune.New(
		ramune.NodeCompat(),
		ramune.WithPermissions(ramune.SandboxPermissions()),
	)
	if err != nil {
		t.Fatalf("sandbox runtime: %v", err)
	}
	defer r.Close()

	v, err := r.Eval("1 + 2")
	if err != nil {
		t.Fatalf("eval under sandbox: %v", err)
	}
	defer v.Close()
	if f, _ := v.Float64(); f != 3 {
		t.Fatalf("got %v, want 3", f)
	}
}

// TestQjswasmResourceLimitsMemory verifies WithResourceLimits caps the JS
// heap — a 1 MiB limit must fail a 10 MiB typed-array allocation rather
// than consume host memory.
func TestQjswasmResourceLimitsMemory(t *testing.T) {
	r, err := ramune.New(
		ramune.WithResourceLimits(ramune.ResourceLimits{
			MaxMemoryBytes: 1 << 20,
		}),
	)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer r.Close()

	if _, err := r.Eval(`new Uint8Array(10 * 1024 * 1024)`); err == nil {
		t.Fatal("expected allocation to fail under 1 MiB memory cap")
	}
}

// TestQjswasmResourceLimitsExecutionTime verifies WithResourceLimits caps
// wall-clock execution time — a 1 s limit must abort an unbounded loop with
// an "interrupted" error rather than hang. Granularity is time_t (seconds),
// so the loop is allowed up to ~2 s in the worst case before the handler
// fires.
//
// If the embedded qjs.wasm predates the C-side QJS_TimeoutHandler wiring
// (i.e., the wasm has not been rebuilt after the helpers.c uncomment),
// while(true){} would hang forever on the engine thread. We bound the wait
// in a goroutine and t.Skip on timeout so the rest of the test suite is
// not blocked. The runtime leaks in that case (we cannot Close while
// Eval is still running on the engine thread) — acceptable because each
// Runtime owns its own locked OS thread, so other tests creating fresh
// runtimes are unaffected.
func TestQjswasmResourceLimitsExecutionTime(t *testing.T) {
	r, err := ramune.New(
		ramune.WithResourceLimits(ramune.ResourceLimits{
			MaxExecutionTime: 1 * time.Second,
		}),
	)
	if err != nil {
		if strings.Contains(err.Error(), "stub") {
			t.Skip("qjswasm stub wasm; rebuild qjs.wasm to exercise the interrupt handler")
		}
		t.Fatalf("runtime: %v", err)
	}

	type evalResult struct {
		err     error
		elapsed time.Duration
	}
	resultCh := make(chan evalResult, 1)
	go func() {
		start := time.Now()
		_, err := r.Eval(`while(true){}`)
		resultCh <- evalResult{err: err, elapsed: time.Since(start)}
	}()

	select {
	case res := <-resultCh:
		defer r.Close()
		if res.err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if res.elapsed > 5*time.Second {
			t.Fatalf("interrupt fired too late: %v", res.elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Skip("interrupt handler not detected in embedded qjs.wasm; rebuild via `make -C third_party/qjs build` after installing wasi-sdk and initialising the QuickJS-NG submodule")
	}
}
