package picker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/picker"
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
	program, sf, err := gotranspiler.BuildProgramForFile(filename)
	if err != nil {
		t.Fatalf("build program: %v", err)
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

func TestPicker_Accepts_PrimitiveArrayRead(t *testing.T) {
	src := `
export function sum(xs: number[]): number {
  let total = 0;
  for (let i = 0; i < xs.length; i++) {
    total = total + xs[i];
  }
  return total;
}`
	res := pickOne(t, src)
	c, ok := byName(res, "sum")
	if !ok || !c.Extracted {
		t.Fatalf("expected `sum` extracted; got %+v", c.Reason)
	}
}

func TestPicker_Accepts_StringArrayAndReadonlyArray(t *testing.T) {
	src := `
export function firstOrEmpty(xs: readonly string[]): string {
  if (xs.length === 0) return "";
  return xs[0];
}`
	res := pickOne(t, src)
	c, ok := byName(res, "firstOrEmpty")
	if !ok || !c.Extracted {
		t.Fatalf("expected `firstOrEmpty` extracted; got %+v", c.Reason)
	}
}

func TestPicker_Rejects_ArrayOfAny(t *testing.T) {
	src := `export function leak(xs: any[]): number { return xs.length; }`
	res := pickOne(t, src)
	c, _ := byName(res, "leak")
	if c.Extracted {
		t.Fatalf("expected rejection for any[]")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_NestedArray(t *testing.T) {
	src := `export function flatLen(xs: number[][]): number { return xs.length; }`
	res := pickOne(t, src)
	c, _ := byName(res, "flatLen")
	if c.Extracted {
		t.Fatalf("expected rejection for number[][] (inner element is object-typed array)")
	}
}

func TestPicker_Rejects_ArrayIndexByString(t *testing.T) {
	src := `export function pick(xs: number[], k: string): number { return xs[k as any]; }`
	res := pickOne(t, src)
	c, _ := byName(res, "pick")
	if c.Extracted {
		t.Fatalf("expected rejection for non-numeric index")
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
	// charAt has different byte-vs-char semantics between JS (UTF-16 code unit)
	// and Go (byte), so the emitter's `string(str[i])` would diverge on
	// multi-byte characters. Picker must stay conservative.
	src := `export function first(s: string): string { return s.charAt(0); }`
	res := pickOne(t, src)
	c, _ := byName(res, "first")
	if c.Extracted {
		t.Fatalf("expected rejection for unsafelisted method .charAt")
	}
	if c.Reason.Code != "builtin-call" {
		t.Fatalf("expected builtin-call reason for charAt, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_MathAbs(t *testing.T) {
	src := `export function flip(n: number): number { return Math.abs(n); }`
	res := pickOne(t, src)
	c, ok := byName(res, "flip")
	if !ok || !c.Extracted {
		t.Fatalf("expected `flip` extracted (Math.abs is safelisted); got %+v", c.Reason)
	}
}

func TestPicker_Accepts_MathVariety(t *testing.T) {
	src := `
export function geo(x: number, y: number): number {
  return Math.sqrt(Math.pow(x, 2) + Math.pow(y, 2));
}
export function clampToInt(x: number): number {
  return Math.min(Math.max(Math.floor(x), 0), 100);
}`
	res := pickOne(t, src)
	for _, name := range []string{"geo", "clampToInt"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected %s extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_MathUnsafe(t *testing.T) {
	// Math.fround is not in the safelist - reject even though tsgo knows it.
	src := `export function r(x: number): number { return Math.fround(x); }`
	res := pickOne(t, src)
	c, _ := byName(res, "r")
	if c.Extracted {
		t.Fatalf("expected rejection for Math.fround (not in safelist)")
	}
	if c.Reason.Code != "builtin-call" {
		t.Fatalf("expected builtin-call, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_MathConstants(t *testing.T) {
	src := `
export function circumference(r: number): number { return 2 * Math.PI * r; }
export function natLog(x: number): number { return Math.log(x) / Math.log(Math.E); }
`
	res := pickOne(t, src)
	for _, name := range []string{"circumference", "natLog"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_MathUnknownConstant(t *testing.T) {
	// Math.LN2 is a real JS constant but not in our safelist.
	src := `export function ln2(): number { return Math.LN2; }`
	res := pickOne(t, src)
	c, _ := byName(res, "ln2")
	if c.Extracted {
		t.Fatalf("expected rejection for Math.LN2 (not in constant safelist)")
	}
	if c.Reason.Code != "builtin-call" {
		t.Fatalf("expected builtin-call reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_MathShadowed(t *testing.T) {
	src := `
export function surprise(n: number): number {
  const Math = { abs: (x: number) => x + 100 };
  return Math.abs(n);
}`
	res := pickOne(t, src)
	c, _ := byName(res, "surprise")
	if c.Extracted {
		t.Fatalf("expected rejection when Math is locally shadowed")
	}
}

func TestPicker_Accepts_SafeGlobalCallees(t *testing.T) {
	src := `
export function valid(x: number): boolean { return !isNaN(x) && isFinite(x); }
export function toNum(s: string): number { return parseFloat(s); }
`
	res := pickOne(t, src)
	for _, name := range []string{"valid", "toNum"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_UnsafeGlobalCallees(t *testing.T) {
	// `parseInt` is in the emitter but produces `(int, error)` - not in safelist.
	src := `export function toInt(s: string): number { return parseInt(s); }`
	res := pickOne(t, src)
	c, _ := byName(res, "toInt")
	if c.Extracted {
		t.Fatalf("expected rejection for parseInt (emitter type mismatch)")
	}
	if c.Reason.Code != "builtin-call" {
		t.Fatalf("expected builtin-call reason, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_NonMathBuiltinCall(t *testing.T) {
	src := `export function s(x: string): string { return String(x); }`
	res := pickOne(t, src)
	c, _ := byName(res, "s")
	if c.Extracted {
		t.Fatalf("expected rejection for String() (only Math is safelisted in v1.3)")
	}
}

func TestPicker_Accepts_StringLengthAndMethods(t *testing.T) {
	src := `
export function shout(s: string): string { return s.toUpperCase().trim(); }
export function lenOf(s: string): number { return s.length; }
export function hasFoo(s: string): boolean { return s.includes("foo"); }
export function words(s: string): string[] { return s.split(" "); }
export function countWords(s: string): number { return s.split(" ").length; }
`
	res := pickOne(t, src)
	for _, name := range []string{"shout", "lenOf", "hasFoo", "words", "countWords"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_StringMethodUnsafe(t *testing.T) {
	// replace with callback emits a JS-arrow path the walker bans; picker must reject.
	src := `export function r(s: string): string { return s.replace("a", (m: string) => m); }`
	res := pickOne(t, src)
	c, _ := byName(res, "r")
	if c.Extracted {
		t.Fatalf("expected rejection for .replace (not in string safelist)")
	}
}

func TestPicker_Rejects_StringIndexing(t *testing.T) {
	// str[i] has different semantics in Go (byte vs char) - picker must stay away.
	src := `export function first(s: string): number { return s.length > 0 ? s[0].length : 0; }`
	res := pickOne(t, src)
	c, _ := byName(res, "first")
	if c.Extracted {
		t.Fatalf("expected rejection for s[0] (not an array receiver)")
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

func TestPicker_Accepts_PrimitiveArrayLiterals(t *testing.T) {
	src := `
export function pair(a: number, b: number): number[] { return [a, b]; }
export function origin(): number[] { return [0, 0]; }
export function words(): string[] { return ["a", "b", "c"]; }
`
	res := pickOne(t, src)
	for _, name := range []string{"pair", "origin", "words"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_MixedArrayLiteral(t *testing.T) {
	src := `export function mix(a: number, b: string): (number|string)[] { return [a, b]; }`
	res := pickOne(t, src)
	c, _ := byName(res, "mix")
	if c.Extracted {
		t.Fatalf("expected rejection for mixed-type array literal (union element)")
	}
}

func TestPicker_Rejects_ArrayLiteralWithSpread(t *testing.T) {
	src := `
export function dupe(xs: number[]): number[] { return [0, ...xs]; }
`
	res := pickOne(t, src)
	c, _ := byName(res, "dupe")
	if c.Extracted {
		t.Fatalf("expected rejection for array literal with spread")
	}
}

func TestPicker_Accepts_ArrayMethods(t *testing.T) {
	src := `
export function has(xs: number[], v: number): boolean { return xs.includes(v); }
export function firstIdx(xs: string[], v: string): number { return xs.indexOf(v); }
export function lastIdx(xs: number[], v: number): number { return xs.lastIndexOf(v); }
`
	res := pickOne(t, src)
	for _, name := range []string{"has", "firstIdx", "lastIdx"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_ArrayMapCallback(t *testing.T) {
	src := `export function doubled(xs: number[]): number[] { return xs.map(x => x * 2); }`
	res := pickOne(t, src)
	c, _ := byName(res, "doubled")
	if c.Extracted {
		t.Fatalf("expected rejection for .map (callback not in v1 array safelist)")
	}
}

func TestPicker_Accepts_SwitchStatement(t *testing.T) {
	src := `
export function classify(n: number): string {
  switch (n) {
    case 0: return "zero";
    case 1: return "one";
    default: return "other";
  }
}
export function dayName(d: number): string {
  switch (d) {
    case 0: return "sun";
    case 1: return "mon";
    case 2: return "tue";
    default: return "?";
  }
}`
	res := pickOne(t, src)
	for _, name := range []string{"classify", "dayName"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_SwitchOnObjectDiscriminant(t *testing.T) {
	src := `
export function weird(xs: number[]): string {
  switch (xs) { default: return "huh"; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "weird")
	if c.Extracted {
		t.Fatalf("expected rejection for non-primitive switch discriminant")
	}
}

func TestPicker_Accepts_TemplateLiterals(t *testing.T) {
	src := `
export function greet(name: string, age: number): string { return ` + "`Hello, ${name}, age ${age}!`" + `; }
export function fmtPair(a: number, b: number): string { return ` + "`(${a}, ${b})`" + `; }
`
	res := pickOne(t, src)
	for _, name := range []string{"greet", "fmtPair"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_TaggedTemplate(t *testing.T) {
	src := "export function bad(x: number): string { return String.raw`literal ${x}`; }"
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection for tagged template")
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
