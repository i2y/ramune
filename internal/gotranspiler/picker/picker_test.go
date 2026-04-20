package picker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ramune/internal/gotranspiler/picker"
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/bundled"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

// pickOne compiles source as a single-file TS program, runs the picker, and
// returns the Result plus the teardown.
func pickOne(t *testing.T, source string) picker.Result {
	t.Helper()
	dir := t.TempDir()
	filename := filepath.Join(dir, "input.ts")
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	absFile, _ := filepath.Abs(filename)

	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCachedFSCompilerHost(filepath.Dir(absFile), fs, bundled.LibPath(), nil, nil)
	cfg := tsoptions.NewParsedCommandLine(
		&core.CompilerOptions{NoEmit: core.TSTrue, SkipLibCheck: core.TSTrue, AllowJs: core.TSTrue},
		[]string{absFile},
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          filepath.Dir(absFile),
		},
	)
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         cfg,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	var sf *ast.SourceFile
	for _, f := range program.SourceFiles() {
		if f.FileName() == absFile {
			sf = f
			break
		}
	}
	if sf == nil {
		t.Fatalf("source file not found in program: %s", absFile)
	}

	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	return picker.Pick(sf, ck, picker.Options{})
}

func byName(res picker.Result, name string) (picker.Candidate, bool) {
	for _, c := range res.Candidates {
		if c.Name == name {
			return c, true
		}
	}
	return picker.Candidate{}, false
}

func TestPicker_Accepts_PrimitiveAdd(t *testing.T) {
	src := `export function add(a: number, b: number): number { return a + b; }`
	res := pickOne(t, src)
	c, ok := byName(res, "add")
	if !ok {
		t.Fatalf("candidate `add` not found; got %+v", res.Candidates)
	}
	if !c.Extracted {
		t.Fatalf("expected `add` to be extracted, got reason %+v", c.Reason)
	}
}

func TestPicker_Accepts_ControlFlow(t *testing.T) {
	src := `
export function clamp(x: number, lo: number, hi: number): number {
  if (x < lo) return lo;
  if (x > hi) return hi;
  return x;
}`
	res := pickOne(t, src)
	c, ok := byName(res, "clamp")
	if !ok || !c.Extracted {
		t.Fatalf("expected `clamp` extracted; got %+v", res.Candidates)
	}
}

func TestPicker_Accepts_WhileLoop(t *testing.T) {
	src := `
export function countdown(n: number): number {
  let total = 0;
  while (n > 0) {
    total = total + n;
    n = n - 1;
  }
  return total;
}`
	res := pickOne(t, src)
	// `n` is a parameter that gets mutated. v1 rejects that.
	c, ok := byName(res, "countdown")
	if !ok {
		t.Fatalf("candidate not found")
	}
	if c.Extracted {
		t.Fatalf("expected rejection (parameter mutation), got extracted")
	}
	if c.Reason.Code != "mutates-parameter" {
		t.Fatalf("expected mutates-parameter reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_WhileLoopWithLocalCounter(t *testing.T) {
	src := `
export function countdown(n: number): number {
  let total = 0;
  let i = n;
  while (i > 0) {
    total = total + i;
    i = i - 1;
  }
  return total;
}`
	res := pickOne(t, src)
	c, ok := byName(res, "countdown")
	if !ok || !c.Extracted {
		t.Fatalf("expected extracted; got %+v", res.Candidates)
	}
}

func TestPicker_Accepts_SelfRecursion(t *testing.T) {
	src := `
export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n - 1) + fib(n - 2);
}`
	res := pickOne(t, src)
	c, ok := byName(res, "fib")
	if !ok || !c.Extracted {
		t.Fatalf("expected fib extracted; got %+v", res.Candidates)
	}
}

func TestPicker_Accepts_PeerCall(t *testing.T) {
	src := `
export function square(x: number): number { return x * x; }
export function sumOfSquares(a: number, b: number): number { return square(a) + square(b); }
`
	res := pickOne(t, src)
	for _, name := range []string{"square", "sumOfSquares"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected %s extracted; got reason %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_AnyParam(t *testing.T) {
	src := `export function echo(x: any): any { return x; }`
	res := pickOne(t, src)
	c, _ := byName(res, "echo")
	if c.Extracted {
		t.Fatalf("expected rejection for any")
	}
	if c.Reason.Code != "any-type" {
		t.Fatalf("expected any-type reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_Generic(t *testing.T) {
	src := `export function id<T>(x: T): T { return x; }`
	res := pickOne(t, src)
	c, _ := byName(res, "id")
	if c.Extracted {
		t.Fatalf("expected rejection for generic")
	}
	if c.Reason.Code != "generic-func" {
		t.Fatalf("expected generic-func reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_Async(t *testing.T) {
	src := `export async function fetchIt(): Promise<number> { return 42; }`
	res := pickOne(t, src)
	c, _ := byName(res, "fetchIt")
	if c.Extracted {
		t.Fatalf("expected rejection for async")
	}
	if c.Reason.Code != "async-func" {
		t.Fatalf("expected async-func reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_Generator(t *testing.T) {
	src := `export function* gen() { yield 1; }`
	res := pickOne(t, src)
	c, _ := byName(res, "gen")
	if c.Extracted {
		t.Fatalf("expected rejection for generator")
	}
	if c.Reason.Code != "generator-func" {
		t.Fatalf("expected generator-func reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_ObjectParam(t *testing.T) {
	src := `export function name(u: { first: string }): string { return u.first; }`
	res := pickOne(t, src)
	c, _ := byName(res, "name")
	if c.Extracted {
		t.Fatalf("expected rejection for object param")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_ArrayParam(t *testing.T) {
	src := `export function sum(xs: number[]): number { return 0; }`
	res := pickOne(t, src)
	c, _ := byName(res, "sum")
	if c.Extracted {
		t.Fatalf("expected rejection for array param")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_LooseEquality(t *testing.T) {
	src := `export function eqish(a: number, b: number): boolean { return a == b; }`
	res := pickOne(t, src)
	c, _ := byName(res, "eqish")
	if c.Extracted {
		t.Fatalf("expected rejection for ==")
	}
	if c.Reason.Code != "forbidden-operator" {
		t.Fatalf("expected forbidden-operator, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_PropertyAccess(t *testing.T) {
	src := `export function upper(s: string): string { return s.toUpperCase(); }`
	res := pickOne(t, src)
	c, _ := byName(res, "upper")
	if c.Extracted {
		t.Fatalf("expected rejection for property access")
	}
}

func TestPicker_Rejects_BuiltinCall(t *testing.T) {
	src := `export function flip(n: number): number { return Math.abs(n); }`
	res := pickOne(t, src)
	c, _ := byName(res, "flip")
	if c.Extracted {
		t.Fatalf("expected rejection for Math.abs (property-access callee)")
	}
}

func TestPicker_Rejects_ClosureCapture(t *testing.T) {
	src := `
const K = 10;
export function scale(x: number): number { return x * K; }
`
	res := pickOne(t, src)
	c, _ := byName(res, "scale")
	if c.Extracted {
		t.Fatalf("expected rejection for closure capture")
	}
	if c.Reason.Code != "closure-capture" {
		t.Fatalf("expected closure-capture, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_ThrowAndTry(t *testing.T) {
	for _, src := range []string{
		`export function bomb(): number { throw 1; }`,
		`export function safe(): number { try { return 1; } catch { return 0; } }`,
	} {
		res := pickOne(t, src)
		for _, c := range res.Candidates {
			if c.Extracted {
				t.Fatalf("expected rejection for %q", src)
			}
		}
	}
}

func TestPicker_ReportFormat(t *testing.T) {
	src := `
export function add(a: number, b: number): number { return a + b; }
export function echo(x: any): any { return x; }
`
	res := pickOne(t, src)
	var b strings.Builder
	res.Format(&b)
	out := b.String()
	if !strings.Contains(out, "extracted  function add") {
		t.Fatalf("report missing add extraction: %s", out)
	}
	if !strings.Contains(out, "skipped    function echo") || !strings.Contains(out, "[any-type]") {
		t.Fatalf("report missing echo skip: %s", out)
	}
	if !strings.Contains(out, "1 extracted, 1 skipped") {
		t.Fatalf("report missing summary: %s", out)
	}
}
