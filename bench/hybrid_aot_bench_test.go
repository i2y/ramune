// Hybrid AOT benchmarks. Compares the same TS workload across:
//   - JSC interpretation (ramune default backend)
//   - goja interpretation
//   - Hybrid AOT Go (what the picker's emit produces, hand-written here
//     verbatim to keep the bench self-contained)
//   - TinyGo-built WASM, called from Go via wazero (only if a pre-built
//     wasm exists at /tmp/ramune_bench_*.wasm — the bench skips otherwise
//     so CI without TinyGo still runs the rest)
//
// Build the wasm fixtures once with:
//
//	cd /Users/i2y/ramune && go run ./cmd/ramune-toolchain compile \
//	    --target wasm-wasi -o /tmp/ramune_bench_fib.wasm \
//	    bench/fixtures/fib.ts
//
// then run `go test -bench=. ./bench/...`.
package bench_test

import (
	"context"
	"os"
	"testing"

	"github.com/dop251/goja"
	"github.com/i2y/ramune"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// nativeFib mirrors the Go output the picker produces for
// `function fib(n: number): number { ... }`. Keeping the body identical
// to the emit means the bench measures the AOT path as users would
// actually experience it (modulo Go inlining heuristics shared with
// the real emitted module).
func nativeFib(n float64) float64 {
	if n < 2 {
		return n
	}
	return nativeFib(n-1) + nativeFib(n-2)
}

// nativeSumArr mirrors the picker's output for a typed-array sum loop.
func nativeSumArr(xs []float64) float64 {
	total := float64(0)
	for i := 0; i < len(xs); i++ {
		total = total + xs[i]
	}
	return total
}

func BenchmarkHybridFib35_NativeAOT(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = nativeFib(35)
	}
}

func BenchmarkHybridSumArr10K_NativeAOT(b *testing.B) {
	xs := make([]float64, 10000)
	for i := range xs {
		xs[i] = float64(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativeSumArr(xs)
	}
}

const sumArrJS = `
function sumArr(xs) {
  var total = 0;
  for (var i = 0; i < xs.length; i++) total = total + xs[i];
  return total;
}
var arr = [];
for (var i = 0; i < 10000; i++) arr.push(i);
sumArr(arr);
`

func BenchmarkHybridSumArr10K_RamuneJSC(b *testing.B) {
	rt, err := ramune.New()
	if err != nil {
		b.Skip("JSC not available")
	}
	defer rt.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := rt.Eval(sumArrJS)
		if v != nil {
			v.Close()
		}
	}
}

func BenchmarkHybridSumArr10K_Goja(b *testing.B) {
	vm := goja.New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.RunString(sumArrJS)
	}
}

// BenchmarkHybridFib35_TinyGoWASM loads /tmp/ramune_bench_fib.wasm via
// wazero and calls the exported `fib` function. The .wasm must be
// pre-built (see file header). The benchmark skips when absent so the
// rest of the suite still runs on machines without TinyGo.
func BenchmarkHybridFib35_TinyGoWASM(b *testing.B) {
	wasmBytes, err := os.ReadFile("/tmp/ramune_bench_fib.wasm")
	if err != nil {
		b.Skip("/tmp/ramune_bench_fib.wasm not built — run `ramune compile --target wasm-wasi -o /tmp/ramune_bench_fib.wasm bench/fixtures/fib.ts` first")
	}
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	cfg := wazero.NewModuleConfig().WithStartFunctions("_initialize")
	mod, err := r.InstantiateWithConfig(ctx, wasmBytes, cfg)
	if err != nil {
		b.Fatalf("instantiate: %v", err)
	}
	fn := mod.ExportedFunction("fib")
	if fn == nil {
		b.Fatalf("`fib` export missing — wasm built from the wrong source?")
	}
	arg := api.EncodeF64(35)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fn.Call(ctx, arg); err != nil {
			b.Fatalf("call: %v", err)
		}
	}
}
