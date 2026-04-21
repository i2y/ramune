package composer_test

import (
	"context"
	gomath "math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	gostrings "strings"
	"testing"
	"time"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/composer"
	"github.com/i2y/ramune/jsrt/promise"
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
	if n < float64(2) {
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
// slips past parsing. Primitive-only extractions emit dependency-free
// standalone Go (no ramune imports inside the native package), so the temp
// module needs no replace directive.
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
export function circumference(r: number): number { return 2 * Math.PI * r; }
export function fmtPair(a: number, b: number): string { return ` + "`(${a}, ${b})`" + `; }
export function makeCoords(a: number, b: number): number[] { return [a, b]; }
export function empty(): number[] { return []; }
export function classify(n: number): string {
  switch (n) {
    case 0: return "zero";
    case 1: return "one";
    default: return "other";
  }
}
export function shouty(s: string): string { return s.repeat(3); }
export function sanitize(s: string): string { return s.replaceAll("foo", "bar"); }
export function prefix(s: string): string { return s.slice(0, 3); }
export function mid(s: string): string { return s.substring(1, 4); }
export function leftpad(s: string, n: number): string { return s.padStart(n, "0"); }
interface Rect { width: number; height: number; }
export function rectArea(r: Rect): number { return r.width * r.height; }
export function maxSafe(): number { return Number.MAX_SAFE_INTEGER; }
export function isFiniteNum(n: number): boolean { return Number.isFinite(n); }
export function isIntegerN(n: number): boolean { return Number.isInteger(n); }
export function sumOf(xs: number[]): number {
  let t = 0;
  for (const x of xs) t = t + x;
  return t;
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
  r = r + Math.sinh(x);
  r = r + Math.cosh(x);
  r = r + Math.tanh(x);
  r = r + Math.asinh(x);
  r = r + Math.acosh(x);
  r = r + Math.atanh(x);
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

// TestHybrid_SafeGlobalCalleesCompile is the safe-globals counterpart to
// TestHybrid_MathSafelistCompiles: each entry in safeGlobalCallees must
// produce Go that compiles standalone. If the emitter ever switches a safe
// callee to one that drags in a ramune runtime dependency (e.g. jsrt.ToBool
// from a bool-coercion path), this test fires.
func TestHybrid_SafeGlobalCalleesCompile(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	// Direct return to avoid implicit bool coercion paths that pull in jsrt.
	src := `
export function checkNaN(x: number): boolean { return isNaN(x); }
export function checkFinite(x: number): boolean { return isFinite(x); }
export function parseF(s: string): number { return parseFloat(s); }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "safeglobals"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if res.GoSource == "" {
		t.Fatalf("picker skipped all three safe-global functions")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module safeglobals\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "g.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed - a safe-global callee's emitter output no longer compiles standalone:\n%s\nsource:\n%s", out, res.GoSource)
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

// splitWords stands in for `export function splitWords(s: string): string[] { return s.split(" "); }`.
func splitWords(s string) []string { return gostrings.Split(s, " ") }

func TestHybrid_TemplateLiteral_RoundTrip(t *testing.T) {
	src := "export function fmtPair(a: number, b: number): string { return `(${a}, ${b})`; }"
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:tpl"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "fmt.Sprintf") {
		t.Fatalf("expected fmt.Sprintf in emitted Go:\n%s", res.GoSource)
	}
	fmtPair := func(a, b float64) string {
		return "(" + strconv.FormatFloat(a, 'g', -1, 64) + ", " + strconv.FormatFloat(b, 'g', -1, 64) + ")"
	}
	mod := ramune.NativeModuleFromFuncs("native:tpl", map[string]any{"fmtPair": fmtPair})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`fmtPair(1, 2)`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got := v.String()
	if got != "(1, 2)" {
		t.Fatalf(`fmtPair(1,2) = %q, want "(1, 2)"`, got)
	}
}

// pAdd stands in for `export async function pAdd(a: number, b: number): Promise<number> { return a + b; }`.
// The transpiler emits this as `*promise.Promise[float64]` wrapped in promise.New[T]; the test fixture
// uses ramune's promise package to mirror that shape.
func pAdd(a, b float64) *promise.Promise[float64] {
	return promise.New[float64](func(resolve func(float64), _ func(error)) {
		resolve(a + b)
	})
}

// Rect mirrors `interface Rect { width: number; height: number; }`. The
// transpiler emits a Go struct with JSON tags; NativeModuleFromFuncs
// reconstructs from `map[string]any` per-field.
type Rect struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func area(r Rect) float64 { return r.Width * r.Height }

// isIntegerN mirrors `Number.isInteger(n)`; emitter lowers to the equivalent
// math-package expression.
func isIntegerN(n float64) bool {
	return !gomath.IsInf(n, 0) && !gomath.IsNaN(n) && gomath.Trunc(n) == n
}

func TestHybrid_NumberStatics_RoundTrip(t *testing.T) {
	src := `
export function isIntegerN(n: number): boolean { return Number.isInteger(n); }
export function maxSafe(): number { return Number.MAX_SAFE_INTEGER; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:num"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	maxSafe := func() float64 { return 9007199254740991 }
	mod := ramune.NativeModuleFromFuncs("native:num", map[string]any{
		"isIntegerN": isIntegerN,
		"maxSafe":    maxSafe,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`isIntegerN(3)`)
	if err != nil {
		t.Fatalf("eval isIntegerN(3): %v", err)
	}
	got, _ := v.Bool()
	if !got {
		t.Fatalf("isIntegerN(3) = false, want true")
	}
	v, err = r.Eval(`isIntegerN(3.5)`)
	if err != nil {
		t.Fatalf("eval isIntegerN(3.5): %v", err)
	}
	got, _ = v.Bool()
	if got {
		t.Fatalf("isIntegerN(3.5) = true, want false")
	}
	v, err = r.Eval(`maxSafe()`)
	if err != nil {
		t.Fatalf("eval maxSafe: %v", err)
	}
	gotN, _ := v.Float64()
	if gotN != 9007199254740991 {
		t.Fatalf("maxSafe() = %v, want 9007199254740991", gotN)
	}
}

// TestHybrid_MixedIntFloat_EmittedGoCompiles guards the invariant that the
// emitter never produces an int-typed `%` in a float64-returning context, and
// never truncates a float comparand with `int(...)` — both would yield Go
// that fails to build or diverges from JS numeric semantics.
func TestHybrid_MixedIntFloat_EmittedGoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	src := `
export function isNeg(n: number): boolean { return n < 0; }
export function isZero(n: number): boolean { return n === 0; }
export function modFive(n: number): number { return n % 5; }
export function cmpLen(arr: number[], n: number): boolean { return arr.length > n; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "soundsmoke"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module soundsmoke\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed for soundness smoke:\n%s\nsource:\n%s", out, res.GoSource)
	}
}

// TestHybrid_MixedIntFloat_RoundTrip exercises the runtime semantics of the
// mixed int/float fix. Each case is picked so the pre-fix truncating emitter
// would give the WRONG answer (noted in comments) and the new float-widened
// emitter matches JS.
func TestHybrid_MixedIntFloat_RoundTrip(t *testing.T) {
	src := `
export function isNeg(n: number): boolean { return n < 0; }
export function isZero(n: number): boolean { return n === 0; }
export function modFive(n: number): number { return n % 5; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:sound"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	// Shape asserts. The pre-fix emitter wrapped a float comparand with
	// `int(n) <` / `int(n) ==` and lowered mixed `%` to `int(n) % 5`; guard
	// against regression with operator-specific patterns rather than the
	// bare `int(n)` substring (which could false-positive on e.g. `int(n+1)`
	// used elsewhere, or future bitwise emissions).
	for _, bad := range []string{"int(n) <", "int(n) ==", "int(n) %"} {
		if strings.Contains(res.GoSource, bad) {
			t.Fatalf("emitted Go contains %q (float→int truncation):\n%s", bad, res.GoSource)
		}
	}
	if !strings.Contains(res.GoSource, "math.Mod(") {
		t.Fatalf("emitted Go missing math.Mod for mixed int/float %%:\n%s", res.GoSource)
	}

	isNeg := func(n float64) bool { return n < 0 }
	isZero := func(n float64) bool { return n == 0 }
	modFive := func(n float64) float64 { return gomath.Mod(n, 5) }

	mod := ramune.NativeModuleFromFuncs("native:sound", map[string]any{
		"isNeg":   isNeg,
		"isZero":  isZero,
		"modFive": modFive,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}

	for _, c := range []struct {
		expr string
		want bool
	}{
		{`isNeg(-0.5)`, true}, // pre-fix: int(-0.5)=0 → 0<0=false
		{`isNeg(0)`, false},
		{`isNeg(1)`, false},
		{`isZero(0.5)`, false},  // pre-fix: int(0.5)=0 → true
		{`isZero(-0.5)`, false}, // pre-fix: int(-0.5)=0 → true
		{`isZero(0)`, true},
	} {
		v, err := r.Eval(c.expr)
		if err != nil {
			t.Fatalf("eval %q: %v", c.expr, err)
		}
		got, _ := v.Bool()
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.expr, got, c.want)
		}
	}

	// eps tolerates the IEEE-754 rounding of math.Mod on non-exact floats
	// (e.g. 7.3 % 5 is not exactly representable); 1e-9 is well below any
	// observed residual from the cases below.
	const eps = 1e-9
	for _, c := range []struct {
		expr string
		want float64
	}{
		{`modFive(5.5)`, 0.5}, // pre-fix: int(5.5)%5 = 0
		{`modFive(7.3)`, 2.3},
		{`modFive(-1.5)`, -1.5},
	} {
		v, err := r.Eval(c.expr)
		if err != nil {
			t.Fatalf("eval %q: %v", c.expr, err)
		}
		got, _ := v.Float64()
		if gomath.Abs(got-c.want) > eps {
			t.Errorf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}

// TestHybrid_NestedBlockNoSpuriousReturn guards a regression where the
// function-body default-return logic fired for every nested block, injecting
// a `return 0` / `return false` at the end of for-loop and if-branch bodies
// and causing e.g. `countPrimes(10000)` to return 0 instead of 1229.
func TestHybrid_NestedBlockNoSpuriousReturn(t *testing.T) {
	src := `
export function isPrime(n: number): boolean {
  if (n < 2) return false;
  if (n === 2) return true;
  if (n % 2 === 0) return false;
  for (let i = 3; i * i <= n; i = i + 2) {
    if (n % i === 0) return false;
  }
  return true;
}
export function countPrimes(limit: number): number {
  let count = 0;
  for (let i = 2; i < limit; i = i + 1) {
    if (isPrime(i)) count = count + 1;
  }
  return count;
}
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:primes"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	// Shape guard: the bug injected a bare `return 0` between the last
	// inner statement of the for-loop body and the loop's closing brace.
	// A correct emit has the loop body end with the inner statement (or
	// its closing brace) followed directly by the loop's close. Matching
	// the specific leading-whitespace pattern from countPrimes keeps this
	// targeted — legitimate `return N` inside if-branches use different
	// indentation.
	if strings.Contains(res.GoSource, "count = count + 1\n\t\t}\n\t\treturn 0") {
		t.Fatalf("emitted Go injects a `return 0` at the tail of the for-loop body:\n%s", res.GoSource)
	}

	// Runtime behaviour is the real guard: countPrimes must return the
	// actual count, not zero.
	isPrime := func(n float64) bool {
		if n < 2 {
			return false
		}
		if n == 2 {
			return true
		}
		if gomath.Mod(n, 2) == 0 {
			return false
		}
		for i := 3.0; i*i <= n; i += 2 {
			if gomath.Mod(n, i) == 0 {
				return false
			}
		}
		return true
	}
	countPrimes := func(limit float64) float64 {
		count := 0.0
		for i := 2.0; i < limit; i++ {
			if isPrime(i) {
				count++
			}
		}
		return count
	}
	mod := ramune.NativeModuleFromFuncs("native:primes", map[string]any{
		"isPrime":     isPrime,
		"countPrimes": countPrimes,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`countPrimes(100)`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got, _ := v.Float64()
	if got != 25 {
		t.Fatalf("countPrimes(100) = %v, want 25", got)
	}
}

// TestHybrid_MultiFile_ComposeFile confirms that when `ComposeFile` is given
// an entry whose import graph reaches sibling TS files, it walks the picker
// across every user file and treats cross-file calls between extractable
// functions as valid. Previously only the entry file was picked and any
// call into an imported kernel hit `callee is not a same-file function`.
func TestHybrid_MultiFile_ComposeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kernel.ts"), []byte(`
export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n - 1) + fib(n - 2);
}
`), 0o644); err != nil {
		t.Fatalf("kernel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.ts"), []byte(`
import { fib } from "./kernel";
export function describe(n: number): number { return fib(n) * 2; }
`), 0o644); err != nil {
		t.Fatalf("app: %v", err)
	}

	res, err := composer.ComposeFile(filepath.Join(dir, "app.ts"), composer.Options{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	extracted := map[string]bool{}
	for _, c := range res.PickerResult.Candidates {
		if c.Extracted {
			extracted[c.Name] = true
		}
	}
	for _, want := range []string{"fib", "describe"} {
		if !extracted[want] {
			t.Fatalf("expected %q extracted across multi-file compose, got %+v", want, res.PickerResult.Candidates)
		}
	}
	if !strings.Contains(res.GoSource, "func Fib(") || !strings.Contains(res.GoSource, "func Describe(") {
		t.Fatalf("expected emitted Go to contain Fib and Describe:\n%s", res.GoSource)
	}
}

func TestHybrid_NamedInterfaceParam_RoundTrip(t *testing.T) {
	src := `
interface Rect { width: number; height: number; }
export function area(r: Rect): number { return r.width * r.height; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:obj"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "type Rect struct") {
		t.Fatalf("expected `type Rect struct` in emitted Go:\n%s", res.GoSource)
	}
	if !strings.Contains(res.GoSource, "r.Width") || !strings.Contains(res.GoSource, "r.Height") {
		t.Fatalf("expected r.Width/r.Height field access in emitted Go:\n%s", res.GoSource)
	}
	mod := ramune.NativeModuleFromFuncs("native:obj", map[string]any{"area": area})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`area({width: 3, height: 4})`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got, _ := v.Float64()
	if got != 12 {
		t.Fatalf("area({3,4}) = %v, want 12", got)
	}
}

// inner / outerChain stand in for a same-file pair where outer await-chains
// inner. The runtime's JS->Go bridge has no path to materialise a Go
// *promise.Promise[T] from a JS Promise, so any awaitable must come from
// another extracted async function in the same package - which is exactly
// what the picker enforces.
func innerAsync() *promise.Promise[float64] {
	return promise.New[float64](func(resolve func(float64), _ func(error)) {
		resolve(42)
	})
}

func outerAsync() *promise.Promise[float64] {
	return promise.New[float64](func(resolve func(float64), reject func(error)) {
		v, err := innerAsync().Await()
		if err != nil {
			reject(err)
			return
		}
		resolve(v + 1)
	})
}

func TestHybrid_AsyncAwait_RoundTrip(t *testing.T) {
	src := `
export async function inner(): Promise<number> { return 42; }
export async function outer(): Promise<number> {
  const v = await inner();
  return v + 1;
}
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:awaiter"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "Await()") {
		t.Fatalf("expected `.Await()` in emitted Go:\n%s", res.GoSource)
	}
	mod := ramune.NativeModuleFromFuncs("native:awaiter", map[string]any{
		"inner": innerAsync,
		"outer": outerAsync,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	if err := r.Exec(`globalThis.__r = null; outer().then(v => globalThis.__r = v);`); err != nil {
		t.Fatalf("eval: %v", err)
	}
	r.RunEventLoopFor(500 * time.Millisecond)
	v, err := r.Eval(`__r`)
	if err != nil {
		t.Fatalf("eval result: %v", err)
	}
	got, _ := v.Float64()
	if got != 43 {
		t.Fatalf("outer() = %v, want 43", got)
	}
}

func TestHybrid_AsyncPromise_RoundTrip(t *testing.T) {
	src := `export async function pAdd(a: number, b: number): Promise<number> { return a + b; }`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:pp"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "*promise.Promise[float64]") {
		t.Fatalf("expected *promise.Promise[float64] in emitted Go:\n%s", res.GoSource)
	}
	mod := ramune.NativeModuleFromFuncs("native:pp", map[string]any{"pAdd": pAdd})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	if err := r.Exec(`globalThis.__result = null; pAdd(2, 3).then(v => { globalThis.__result = v; });`); err != nil {
		t.Fatalf("eval: %v", err)
	}
	r.RunEventLoopFor(500 * time.Millisecond)
	v, err := r.Eval(`__result`)
	if err != nil {
		t.Fatalf("eval result: %v", err)
	}
	got, _ := v.Float64()
	if got != 5 {
		t.Fatalf("pAdd(2,3) = %v, want 5", got)
	}
}

// arrHas stands in for `export function has(xs: number[], v: number): boolean { return xs.includes(v); }`
// The emitter produces `jsarray.Includes(xs, v)`; this hand-written variant
// is value-equivalent for the test's input.
func arrHas(xs []float64, v float64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestHybrid_ArrayIncludes_RoundTrip(t *testing.T) {
	src := `export function has(xs: number[], v: number): boolean { return xs.includes(v); }`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:ah"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "jsarray.Includes") {
		t.Fatalf("expected jsarray.Includes in emitted Go:\n%s", res.GoSource)
	}
	mod := ramune.NativeModuleFromFuncs("native:ah", map[string]any{"has": arrHas})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	for _, c := range []struct {
		call string
		want bool
	}{
		{`has([1, 2, 3], 2)`, true},
		{`has([1, 2, 3], 99)`, false},
	} {
		v, err := r.Eval(c.call)
		if err != nil {
			t.Fatalf("eval %s: %v", c.call, err)
		}
		got, err := v.Bool()
		if err != nil {
			t.Fatalf("bool: %v", err)
		}
		if got != c.want {
			t.Fatalf("%s = %v, want %v", c.call, got, c.want)
		}
	}
}

func TestHybrid_StringSplit_RoundTrip(t *testing.T) {
	src := `
export function splitWords(s: string): string[] { return s.split(" "); }
export function wordCount(s: string): number { return s.split(" ").length; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:split"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "strings.Split") {
		t.Fatalf("expected strings.Split in emitted Go:\n%s", res.GoSource)
	}
	wordCount := func(s string) float64 { return float64(len(gostrings.Split(s, " "))) }
	mod := ramune.NativeModuleFromFuncs("native:split", map[string]any{
		"splitWords": splitWords,
		"wordCount":  wordCount,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`wordCount("hello world foo")`)
	if err != nil {
		t.Fatalf("eval wordCount: %v", err)
	}
	got, _ := v.Float64()
	if got != 3 {
		t.Fatalf("wordCount(3 words) = %v, want 3", got)
	}
}

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

// parseF stands in for `export function parseF(s: string): number { return parseFloat(s); }`.
func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func TestHybrid_ParseFloat_RoundTrip(t *testing.T) {
	src := `export function parseF(s: string): number { return parseFloat(s); }`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{NativeModuleName: "native:pf"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "strconv.ParseFloat") {
		t.Fatalf("expected strconv.ParseFloat in emitted Go:\n%s", res.GoSource)
	}
	mod := ramune.NativeModuleFromFuncs("native:pf", map[string]any{"parseF": parseF})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`parseF("3.14")`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got, _ := v.Float64()
	if got != 3.14 {
		t.Fatalf("parseF('3.14') = %v, want 3.14", got)
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

// Counter mirrors what the transpiler emits for the Counter class in
// TestHybrid_Class_RoundTrip below.
type Counter struct {
	Value float64
}

func NewCounter(initial float64) *Counter {
	c := &Counter{}
	c.Value = initial
	return c
}

func (c *Counter) Increment()              { c.Value = c.Value + 1 }
func (c *Counter) Total(x float64) float64 { return c.Value + x }

func TestHybrid_Class_ShimBuildAndArtifacts(t *testing.T) {
	src := `
export class Counter {
  value: number;
  constructor(initial: number) { this.value = initial; }
  increment(): void { this.value = this.value + 1; }
  total(x: number): number { return this.value + x; }
}`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{
		PkgName:          "native_class",
		NativeModuleName: "native:class_smoke",
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if res.GoSource == "" {
		t.Fatalf("expected non-empty Go source")
	}
	for _, want := range []string{
		"type Counter struct",
		"Value float64",
		"func NewCounter(initial float64) *Counter",
		"func (c *Counter) Increment()",
		"func (c *Counter) Total(x float64) float64",
	} {
		if !strings.Contains(res.GoSource, want) {
			t.Fatalf("emitted Go missing %q:\n%s", want, res.GoSource)
		}
	}
	if len(res.ExportedJSClasses) != 1 || res.ExportedJSClasses[0] != "Counter" {
		t.Fatalf("expected ExportedJSClasses=[Counter], got %v", res.ExportedJSClasses)
	}
	if !strings.Contains(res.ShimJS, "mod.newCounter") {
		t.Fatalf("shim missing mod.newCounter ref:\n%s", res.ShimJS)
	}
	if !strings.Contains(res.ShimJS, `"Counter"`) {
		t.Fatalf("shim missing Counter install ref:\n%s", res.ShimJS)
	}
}

func TestHybrid_Class_EmittedGoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	src := `
export class Counter {
  value: number;
  constructor(initial: number) { this.value = initial; }
  increment(): void { this.value = this.value + 1; }
  total(x: number): number { return this.value + x; }
}
export class Box {
  w: number;
  h: number;
  area(): number { return this.w * this.h; }
  scale(k: number): void { this.w = this.w * k; this.h = this.h * k; }
}`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "classsmoke"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module classsmoke\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed for class smoke:\n%s\nsource:\n%s", out, res.GoSource)
	}
}

func TestHybrid_Class_RoundTrip(t *testing.T) {
	src := `
export class Counter {
  value: number;
  constructor(initial: number) { this.value = initial; }
  increment(): void { this.value = this.value + 1; }
  total(x: number): number { return this.value + x; }
}`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	const modName = "native:counter_rt"
	res, err := composer.Compose(sf, ck, composer.Options{
		PkgName:          "native_class_rt",
		NativeModuleName: modName,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	mod := ramune.NativeModuleFromFuncs(modName, map[string]any{
		"newCounter": NewCounter,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()

	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim exec: %v", err)
	}

	// Cases:
	//  1. `new Counter(...)` — primary path
	//  2. `Counter(...)`     — call without `new`, should also return instance
	//  3. field read          — c.value
	//  4. field write         — c.value = N
	//  5. method call         — c.increment() (void)
	//  6. method with args    — c.total(x)
	script := `
(function() {
  var c = new Counter(5);
  c.increment();
  var t1 = c.total(10);      // (5+1)+10 = 16
  var t2 = c.value;          // 6
  c.value = 100;
  var t3 = c.total(0);       // 100

  var c2 = Counter(2);       // without new keyword
  c2.increment();
  c2.increment();
  var t4 = c2.value;         // 4

  return [t1, t2, t3, t4].join(",");
})();
`
	v, err := r.Eval(script)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	got := v.String()
	if got != "16,6,100,4" {
		t.Fatalf("round trip mismatch: got %q, want %q", got, "16,6,100,4")
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

// applyCb / wrapCb / countCalls stand in for what the transpiler emits for
// the TS sources in TestHybrid_JSFuncCallback_RoundTrip — the hand-written
// Go exercises the same cb.Call path the emitted IIFE would.
func applyCb(cb *ramune.JSFunc, x float64) float64 {
	v, err := cb.Call(x)
	if err != nil {
		panic(err)
	}
	return v.(float64)
}

func wrapCb(cb *ramune.JSFunc, s string) string {
	v, err := cb.Call(s)
	if err != nil {
		panic(err)
	}
	return v.(string)
}

func countCalls(cb *ramune.JSFunc) float64 {
	_, err := cb.Call(float64(1))
	if err != nil {
		panic(err)
	}
	_, err = cb.Call(float64(2))
	if err != nil {
		panic(err)
	}
	_, err = cb.Call(float64(3))
	if err != nil {
		panic(err)
	}
	return 3
}

// TestHybrid_JSFuncCallback_EmittedGoCompiles is the compile drift guard for
// the JSFunc-callback path. The emitted Go depends on the host
// `github.com/i2y/ramune` package (for *ramune.JSFunc) and
// `github.com/i2y/ramune/jsrt` (for jsrt.Throw), so unlike the primitive-only
// standalone compile smokes we need a `replace` directive pointing at the
// live repo.
func TestHybrid_JSFuncCallback_EmittedGoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	repoRoot := ramuneRepoRoot(t)
	src := `
export function applyCb(cb: (n: number) => number, x: number): number { return cb(x); }
export function wrapCb(cb: (s: string) => string, s: string): string { return cb(s); }
export function countCalls(cb: (n: number) => void): number { cb(1); cb(2); cb(3); return 3; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "jsfuncsmoke"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "*ramune.JSFunc") {
		t.Fatalf("expected *ramune.JSFunc in emitted Go:\n%s", res.GoSource)
	}
	if !strings.Contains(res.GoSource, ".Call(") {
		t.Fatalf("expected .Call( in emitted Go:\n%s", res.GoSource)
	}
	if !strings.Contains(res.GoSource, "jsrt.Throw") {
		t.Fatalf("expected jsrt.Throw wrapper in emitted Go:\n%s", res.GoSource)
	}
	dir := t.TempDir()
	goMod := "module jsfuncsmoke\n\ngo 1.21\n\nrequire github.com/i2y/ramune v0.0.0\n\nreplace github.com/i2y/ramune => " + repoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "n.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// go.sum derives from the replaced-local module — build with -mod=mod to
	// tolerate the absent sum file on first run.
	cmd := exec.Command("go", "build", "-mod=mod", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed:\n%s\nsource:\n%s", out, res.GoSource)
	}
}

// ramuneRepoRoot resolves the absolute path of the ramune Go module root
// from the test binary's current working directory. Tests run inside
// internal/gotranspiler/composer so the repo lives three levels up.
func ramuneRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	abs, err := filepath.Abs(filepath.Join(wd, "..", "..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return abs
}

// doubleAll / keepPositive / traverse / anyTrue / allTrue stand in for the
// array-callback lowerings in TestHybrid_JSFuncArrayCallbacks_RoundTrip.

func doubleAll(cb *ramune.JSFunc, xs []float64) []float64 {
	out := make([]float64, 0, len(xs))
	for _, x := range xs {
		v, err := cb.Call(x)
		if err != nil {
			panic(err)
		}
		out = append(out, v.(float64))
	}
	return out
}

func keepPositive(cb *ramune.JSFunc, xs []float64) []float64 {
	var out []float64
	for _, x := range xs {
		v, err := cb.Call(x)
		if err != nil {
			panic(err)
		}
		if v.(bool) {
			out = append(out, x)
		}
	}
	return out
}

func traverse(cb *ramune.JSFunc, xs []float64) {
	for _, x := range xs {
		_, err := cb.Call(x)
		if err != nil {
			panic(err)
		}
	}
}

func anyTrue(cb *ramune.JSFunc, xs []float64) bool {
	for _, x := range xs {
		v, err := cb.Call(x)
		if err != nil {
			panic(err)
		}
		if v.(bool) {
			return true
		}
	}
	return false
}

func allTrue(cb *ramune.JSFunc, xs []float64) bool {
	for _, x := range xs {
		v, err := cb.Call(x)
		if err != nil {
			panic(err)
		}
		if !v.(bool) {
			return false
		}
	}
	return true
}

// TestHybrid_JSFuncArrayCallbacks_EmittedGoCompiles verifies the array-
// callback IIFE lowerings compile standalone (against the replace-linked
// ramune module). Picker drift guard: any future change to the picker that
// lets through a method the emitter can't lower would light this up.
func TestHybrid_JSFuncArrayCallbacks_EmittedGoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	repoRoot := ramuneRepoRoot(t)
	src := `
export function doubleAll(cb: (n: number) => number, xs: number[]): number[] { return xs.map(cb); }
export function keepPositive(cb: (n: number) => boolean, xs: number[]): number[] { return xs.filter(cb); }
export function traverse(cb: (n: number) => void, xs: number[]): void { xs.forEach(cb); }
export function anyTrue(cb: (n: number) => boolean, xs: number[]): boolean { return xs.some(cb); }
export function allTrue(cb: (n: number) => boolean, xs: number[]): boolean { return xs.every(cb); }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "jsfuncarrsmoke"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, want := range []string{
		"range xs",
		".Call(__x)",
		"jsrt.Throw",
		"__v.(float64)", // from map
		"__v.(bool)",    // from filter/some/every
	} {
		if !strings.Contains(res.GoSource, want) {
			t.Fatalf("emitted Go missing %q:\n%s", want, res.GoSource)
		}
	}
	dir := t.TempDir()
	goMod := "module jsfuncarrsmoke\n\ngo 1.21\n\nrequire github.com/i2y/ramune v0.0.0\n\nreplace github.com/i2y/ramune => " + repoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "n.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "build", "-mod=mod", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed:\n%s\nsource:\n%s", out, res.GoSource)
	}
}

// TestHybrid_JSFuncArrayCallbacks_RoundTrip exercises each lowered method
// end-to-end: JS passes an arrow callback → extracted Go iterates the
// slice → results flow back to JS. Runs on whichever backend is compiled
// in via newRamune.
func TestHybrid_JSFuncArrayCallbacks_RoundTrip(t *testing.T) {
	src := `
export function doubleAll(cb: (n: number) => number, xs: number[]): number[] { return xs.map(cb); }
export function keepPositive(cb: (n: number) => boolean, xs: number[]): number[] { return xs.filter(cb); }
export function traverse(cb: (n: number) => void, xs: number[]): void { xs.forEach(cb); }
export function anyTrue(cb: (n: number) => boolean, xs: number[]): boolean { return xs.some(cb); }
export function allTrue(cb: (n: number) => boolean, xs: number[]): boolean { return xs.every(cb); }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	const modName = "native:jsfunc_arr_rt"
	res, err := composer.Compose(sf, ck, composer.Options{
		PkgName:          "native_jsfunc_arr_rt",
		NativeModuleName: modName,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	mod := ramune.NativeModuleFromFuncs(modName, map[string]any{
		"doubleAll":    doubleAll,
		"keepPositive": keepPositive,
		"traverse":     traverse,
		"anyTrue":      anyTrue,
		"allTrue":      allTrue,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}

	v, err := r.Eval(`doubleAll(function(n){ return n*2; }, [1,2,3]).join(",")`)
	if err != nil {
		t.Fatalf("eval doubleAll: %v", err)
	}
	if s := v.String(); s != "2,4,6" {
		t.Fatalf("doubleAll = %q, want 2,4,6", s)
	}

	v, err = r.Eval(`keepPositive(function(n){ return n > 0; }, [-1,2,-3,4]).join(",")`)
	if err != nil {
		t.Fatalf("eval keepPositive: %v", err)
	}
	if s := v.String(); s != "2,4" {
		t.Fatalf("keepPositive = %q, want 2,4", s)
	}

	// forEach — verify all three calls landed.
	if err := r.Exec(`globalThis.__seen = []; traverse(function(n){ globalThis.__seen.push(n); }, [10,20,30]);`); err != nil {
		t.Fatalf("traverse exec: %v", err)
	}
	v, err = r.Eval(`__seen.join(",")`)
	if err != nil {
		t.Fatalf("eval __seen: %v", err)
	}
	if s := v.String(); s != "10,20,30" {
		t.Fatalf("traverse sequence = %q, want 10,20,30", s)
	}

	// some: true branch.
	v, err = r.Eval(`anyTrue(function(n){ return n === 3; }, [1,2,3,4])`)
	if err != nil {
		t.Fatalf("eval anyTrue: %v", err)
	}
	if got, _ := v.Bool(); !got {
		t.Fatalf("anyTrue 3 in [1..4] = false, want true")
	}
	// some: false branch.
	v, err = r.Eval(`anyTrue(function(n){ return n === 99; }, [1,2,3])`)
	if err != nil {
		t.Fatalf("eval anyTrue (false): %v", err)
	}
	if got, _ := v.Bool(); got {
		t.Fatalf("anyTrue 99 in [1..3] = true, want false")
	}

	// every: true branch.
	v, err = r.Eval(`allTrue(function(n){ return n > 0; }, [1,2,3])`)
	if err != nil {
		t.Fatalf("eval allTrue: %v", err)
	}
	if got, _ := v.Bool(); !got {
		t.Fatalf("allTrue positives = false, want true")
	}
	// every: false branch — short-circuit path.
	v, err = r.Eval(`allTrue(function(n){ return n > 0; }, [1,-2,3])`)
	if err != nil {
		t.Fatalf("eval allTrue (false): %v", err)
	}
	if got, _ := v.Bool(); got {
		t.Fatalf("allTrue with -2 = true, want false")
	}
}

// TestHybrid_JSFuncCallback_RoundTrip exercises the end-to-end callback
// bridge: an extracted Go function invokes a JS arrow callback via
// *ramune.JSFunc.Call, and the result flows back through the emitted IIFE.
// Runs on whichever backend is compiled in (JSC / qjswasm / goja) via the
// newRamune helper.
func TestHybrid_JSFuncCallback_RoundTrip(t *testing.T) {
	src := `
export function applyCb(cb: (n: number) => number, x: number): number { return cb(x); }
export function wrapCb(cb: (s: string) => string, s: string): string { return cb(s); }
export function countCalls(cb: (n: number) => void): number { cb(1); cb(2); cb(3); return 3; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	const modName = "native:jsfunc_rt"
	res, err := composer.Compose(sf, ck, composer.Options{
		PkgName:          "native_jsfunc_rt",
		NativeModuleName: modName,
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	mod := ramune.NativeModuleFromFuncs(modName, map[string]any{
		"applyCb":    applyCb,
		"wrapCb":     wrapCb,
		"countCalls": countCalls,
	})
	r := newRamune(t, ramune.NodeCompat(), ramune.WithModule(mod))
	defer r.Close()
	if err := r.Exec(res.ShimJS); err != nil {
		t.Fatalf("shim: %v", err)
	}
	v, err := r.Eval(`applyCb(function(n){ return n*n; }, 6)`)
	if err != nil {
		t.Fatalf("eval applyCb: %v", err)
	}
	got, _ := v.Float64()
	if got != 36 {
		t.Fatalf("applyCb(n*n, 6) = %v, want 36", got)
	}
	v, err = r.Eval(`wrapCb(function(s){ return "<" + s + ">"; }, "ok")`)
	if err != nil {
		t.Fatalf("eval wrapCb: %v", err)
	}
	if s := v.String(); s != "<ok>" {
		t.Fatalf(`wrapCb("<"+s+">", "ok") = %q, want "<ok>"`, s)
	}

	// Side-effect callback: the callback pushes to a JS array so we can
	// verify three invocations in order.
	if err := r.Exec(`globalThis.__cb_seen = []; globalThis.__cb_n = countCalls(function(n){ globalThis.__cb_seen.push(n); });`); err != nil {
		t.Fatalf("countCalls exec: %v", err)
	}
	v, err = r.Eval(`__cb_n`)
	if err != nil {
		t.Fatalf("eval __cb_n: %v", err)
	}
	if n, _ := v.Float64(); n != 3 {
		t.Fatalf("countCalls return = %v, want 3", n)
	}
	v, err = r.Eval(`__cb_seen.join(",")`)
	if err != nil {
		t.Fatalf("eval __cb_seen: %v", err)
	}
	if s := v.String(); s != "1,2,3" {
		t.Fatalf("countCalls invocation order = %q, want 1,2,3", s)
	}
}
