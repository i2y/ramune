package composer_test

import (
	"context"
	gomath "math"
	"os"
	"os/exec"
	"path/filepath"
	gostrings "strings"
	"strings"
	"testing"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/composer"
)

// fib matches what the transpiler emits for
//
//	export function fib(n: number): number { if (n < 2) return n; return fib(n-1) + fib(n-2); }
//
// Hand-writing the equivalent stands in for the missing runtime-compilation
// step: we still exercise the shim installation + NativeModuleFromFuncs +
// Runtime evaluation path end-to-end. A follow-up smoke that subprocess-
// compiles the emitted Go would close the last gap.
func fib(n float64) float64 {
	if int(n) < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func add(a float64, b float64) float64 { return a + b }

// sumArr mirrors what the transpiler should emit for
//
//	export function sumArr(xs: number[]): number { let t=0; for (let i=0; i<xs.length; i++) t+=xs[i]; return t; }
func sumArr(xs []float64) float64 {
	var t float64
	for i := 0; i < len(xs); i++ {
		t = t + xs[i]
	}
	return t
}

// newRamune returns a live Runtime or skips when the configured backend is
// unavailable (e.g. JSC on Windows / Linux without the shared library).
func newRamune(t *testing.T, opts ...ramune.Option) *ramune.Runtime {
	t.Helper()
	r, err := ramune.New(opts...)
	if err != nil {
		t.Skipf("runtime unavailable: %v", err)
	}
	return r
}

func TestHybrid_ShimSwapsAndCallsNative(t *testing.T) {
	src := `
export function add(a: number, b: number): number { return a + b; }
export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n-1) + fib(n-2);
}
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	const modName = "native:hybrid_smoke"
	res, err := composer.Compose(sf, ck, composer.Options{
		PkgName:          "native_app",
		NativeModuleName: modName,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if res.GoSource == "" || res.ShimJS == "" {
		t.Fatalf("expected non-empty artifacts, got goSource=%q shim=%q", res.GoSource, res.ShimJS)
	}

	// The emitted Go must contain both functions with exported names matching
	// the TS exports. DiscoverExportedFuncs is what cmd/ramune-toolchain uses
	// downstream, so go through it to mirror that code path.
	funcs, err := gotranspiler.DiscoverExportedFuncs(res.GoSource)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	have := map[string]bool{}
	for _, f := range funcs {
		have[f.JSName] = true
	}
	for _, want := range []string{"add", "fib"} {
		if !have[want] {
			t.Fatalf("emitted Go missing JS export %q: funcs=%+v", want, funcs)
		}
	}

	mod := ramune.NativeModuleFromFuncs(modName, map[string]any{
		"add": add,
		"fib": fib,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()

	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim exec: %v", err)
	}

	// After the shim runs, both functions must be visible on globalThis.
	// Calling them should hit the Go implementations.
	v, err := r.Eval(`add(2, 3)`)
	if err != nil {
		t.Fatalf("eval add: %v", err)
	}
	got, err := v.Float64()
	if err != nil {
		t.Fatalf("add result not numeric: %v", err)
	}
	if got != 5 {
		t.Fatalf("add(2,3) = %v, want 5", got)
	}

	v, err = r.Eval(`fib(10)`)
	if err != nil {
		t.Fatalf("eval fib: %v", err)
	}
	got, err = v.Float64()
	if err != nil {
		t.Fatalf("fib result not numeric: %v", err)
	}
	if got != 55 {
		t.Fatalf("fib(10) = %v, want 55", got)
	}
}

func TestHybrid_ShimIsIdempotent(t *testing.T) {
	src := `export function add(a: number, b: number): number { return a + b; }`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{
		NativeModuleName: "native:idempotent",
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	mod := ramune.NativeModuleFromFuncs("native:idempotent", map[string]any{
		"add": add,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()

	// Running the shim twice must be a no-op — the __ramuneNativeInstalled
	// guard keeps a second swap from executing even though the first already
	// took. If the guard were missing, the second run would still succeed
	// identically, so also verify that globalThis.__ramuneNativeInstalled is
	// truthy after both runs.
	for i := 0; i < 2; i++ {
		if err := r.Exec(res.ShimJS); err != nil {
			t.Fatalf("shim exec #%d: %v", i+1, err)
		}
	}
	v, err := r.Eval(`globalThis.__ramuneNativeInstalled === true`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	truthy, err := v.Bool()
	if err != nil {
		t.Fatalf("bool: %v", err)
	}
	if !truthy {
		t.Fatalf("expected __ramuneNativeInstalled === true")
	}
	v, err = r.Eval(`add(7, 8)`)
	if err != nil {
		t.Fatalf("eval add: %v", err)
	}
	got, _ := v.Float64()
	if got != 15 {
		t.Fatalf("add(7,8) = %v, want 15", got)
	}
}

// TestHybrid_EmittedGoCompiles invokes `go build` on the emitter's output,
// closing the last verification gap: parseability (go/parser) does not imply
// buildability — a reference to an undeclared identifier or a wrong type
// slips past parsing. The v1 scope emits dependency-free standalone Go (no
// ramune imports inside the native package), so the temp module needs no
// replace directive.
func TestHybrid_EmittedGoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	src := `
export function add(a: number, b: number): number { return a + b; }
export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n-1) + fib(n-2);
}
export function clamp(x: number, lo: number, hi: number): number {
  if (x < lo) return lo;
  if (x > hi) return hi;
  return x;
}
export function dist(x: number, y: number): number {
  return Math.sqrt(Math.pow(x, 2) + Math.pow(y, 2));
}
export function sumArr(xs: number[]): number {
  let total = 0;
  for (let i = 0; i < xs.length; i++) total = total + xs[i];
  return total;
}
export function shout(s: string): string { return s.toUpperCase().trim(); }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "native_app"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module nativeapp_smoke\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "native.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("native.go: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\noutput:\n%s\nsource:\n%s", err, out, res.GoSource)
	}
}

// dist stands in for the transpiler's emission of
//
//	export function dist(x: number, y: number): number {
//	  return Math.sqrt(Math.pow(x, 2) + Math.pow(y, 2));
//	}
func dist(x float64, y float64) float64 {
	return gomath.Sqrt(gomath.Pow(x, 2) + gomath.Pow(y, 2))
}

// TestHybrid_MathSafelistCompiles guards against drift between the picker's
// mathSafeMethods set and the emitter's capability. If the picker admits a
// Math.<method> that the emitter's default branch can't lower to a real Go
// symbol (e.g. `math.Sign`, which does not exist), the generated Go fails
// to compile and this test fires. Update this when changing the safelist.
func TestHybrid_MathSafelistCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	// Must enumerate every picker-admitted Math method with the correct arity.
	src := `
export function useMath(x: number, y: number): number {
  let r = 0;
  r = r + Math.random();
  r = r + Math.abs(x);
  r = r + Math.floor(x);
  r = r + Math.ceil(x);
  r = r + Math.round(x);
  r = r + Math.trunc(x);
  r = r + Math.sqrt(x);
  r = r + Math.cbrt(x);
  r = r + Math.exp(x);
  r = r + Math.log(x);
  r = r + Math.log2(x);
  r = r + Math.log10(x);
  r = r + Math.sin(x);
  r = r + Math.cos(x);
  r = r + Math.tan(x);
  r = r + Math.asin(x);
  r = r + Math.acos(x);
  r = r + Math.atan(x);
  r = r + Math.pow(x, y);
  r = r + Math.atan2(x, y);
  r = r + Math.hypot(x, y);
  r = r + Math.min(x, y);
  r = r + Math.max(x, y);
  return r;
}
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "mathsmoke"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if res.GoSource == "" {
		t.Fatalf("picker skipped useMath - one of the safelisted methods is not extractable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module mathsmoke\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "m.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed - picker admits a Math method the emitter can't lower:\n%s\nsource:\n%s", out, res.GoSource)
	}
}

func TestHybrid_MathCalls_RoundTrip(t *testing.T) {
	src := `
export function dist(x: number, y: number): number {
  return Math.sqrt(Math.pow(x, 2) + Math.pow(y, 2));
}`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:mth"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "math.Sqrt") || !strings.Contains(res.GoSource, "math.Pow") {
		t.Fatalf("expected math.Sqrt/Pow in emitted Go:\n%s", res.GoSource)
	}

	mod := ramune.NativeModuleFromFuncs("native:mth", map[string]any{"dist": dist})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`dist(3, 4)`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got, _ := v.Float64()
	if got != 5 {
		t.Fatalf("dist(3,4) = %v, want 5", got)
	}
}

// shout stands in for `export function shout(s: string): string { return s.toUpperCase().trim(); }`.
func shout(s string) string { return gostrings.TrimSpace(gostrings.ToUpper(s)) }

func TestHybrid_StringMethods_RoundTrip(t *testing.T) {
	src := `
export function shout(s: string): string { return s.toUpperCase().trim(); }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:str"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "strings.ToUpper") || !strings.Contains(res.GoSource, "strings.TrimSpace") {
		t.Fatalf("expected strings.ToUpper / strings.TrimSpace in emitted Go:\n%s", res.GoSource)
	}

	mod := ramune.NativeModuleFromFuncs("native:str", map[string]any{"shout": shout})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`shout("  hello  ")`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got := v.String()
	if got != "HELLO" {
		t.Fatalf(`shout("  hello  ") = %q, want "HELLO"`, got)
	}
}

func TestHybrid_ArrayRead_RoundTrips(t *testing.T) {
	src := `
export function sumArr(xs: number[]): number {
  let t = 0;
  for (let i = 0; i < xs.length; i++) t = t + xs[i];
  return t;
}
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:arr"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "func SumArr(") {
		t.Fatalf("expected SumArr in emitted Go, got:\n%s", res.GoSource)
	}
	if !strings.Contains(res.GoSource, "[]float64") {
		t.Fatalf("expected []float64 in emitted Go, got:\n%s", res.GoSource)
	}

	mod := ramune.NativeModuleFromFuncs("native:arr", map[string]any{"sumArr": sumArr})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim exec: %v", err)
	}
	v, err := r.Eval(`sumArr([1, 2, 3, 4, 5])`)
	if err != nil {
		t.Fatalf("eval sumArr: %v", err)
	}
	got, err := v.Float64()
	if err != nil {
		t.Fatalf("sumArr result not numeric: %v", err)
	}
	if got != 15 {
		t.Fatalf("sumArr([1..5]) = %v, want 15", got)
	}
}

func TestHybrid_MixedExtractAndSkip_SkippedStaysInJS(t *testing.T) {
	// parseUser has `any` param — picker skips. The shim must not swap it.
	// The JS-only fallback behavior is simulated here by the test: we never
	// register parseUser in the native module, and the shim's `if (mod.xxx)`
	// guard must skip it rather than clobbering an undefined entry.
	src := `
export function add(a: number, b: number): number { return a + b; }
export function parseUser(x: any): any { return x; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{
		NativeModuleName: "native:mixed",
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if strings.Contains(res.ShimJS, "parseUser") {
		t.Fatalf("shim unexpectedly references parseUser")
	}
	mod := ramune.NativeModuleFromFuncs("native:mixed", map[string]any{
		"add": add,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()

	// Pre-declare parseUser in JS — simulates the bundle retaining its JS
	// implementation. The shim must not overwrite it.
	if err := r.Exec(`globalThis.parseUser = function(x) { return {origin: "js", input: x}; };`); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`parseUser(42).origin`)
	if err != nil {
		t.Fatalf("eval parseUser: %v", err)
	}
	if origin := v.String(); origin != "js" {
		t.Fatalf("parseUser.origin = %q, want \"js\"", origin)
	}
	v, err = r.Eval(`add(1, 2)`)
	if err != nil {
		t.Fatalf("eval add: %v", err)
	}
	got, _ := v.Float64()
	if got != 3 {
		t.Fatalf("add(1,2) = %v, want 3", got)
	}
}
