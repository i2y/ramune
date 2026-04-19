//go:build qjswasm && !quickjs && !goja

package ramune_test

import (
	"strings"
	"testing"

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
