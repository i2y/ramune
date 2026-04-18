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
