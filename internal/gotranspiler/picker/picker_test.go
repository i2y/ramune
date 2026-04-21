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
	// `n` is a parameter that gets mutated. The picker rejects that.
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

func TestPicker_Rejects_UnaryPlusOnString(t *testing.T) {
	src := `export function strToNum(s: string): number { return +s as number; }`
	res := pickOne(t, src)
	c, _ := byName(res, "strToNum")
	if c.Extracted {
		t.Fatalf("expected rejection for unary + on string")
	}
	if c.Reason.Code != "object-type" && c.Reason.Code != "any-type" {
		t.Fatalf("expected object-type or any-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_UnaryMinusOnNumber(t *testing.T) {
	src := `export function flip(n: number): number { return -n; }`
	res := pickOne(t, src)
	c, ok := byName(res, "flip")
	if !ok || !c.Extracted {
		t.Fatalf("expected `flip` extracted; got %+v", c.Reason)
	}
}

func TestPicker_Rejects_AsAnyCast(t *testing.T) {
	src := `export function bad(n: number, s: string): number { return n * (s as any); }`
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection for `as any` cast")
	}
	if c.Reason.Code != "any-type" {
		t.Fatalf("expected any-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_AsUnknownCast(t *testing.T) {
	src := `export function bad(s: string): number { return (s as unknown) as number; }`
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection for `as unknown` cast")
	}
}

func TestPicker_Rejects_LogicalAndOnNumbers(t *testing.T) {
	src := `export function pickFirst(a: number, b: number): number { return a && b; }`
	res := pickOne(t, src)
	c, _ := byName(res, "pickFirst")
	if c.Extracted {
		t.Fatalf("expected rejection for `&&` on numbers")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_LogicalOrOnStrings(t *testing.T) {
	src := `export function fallback(a: string, b: string): string { return a || b; }`
	res := pickOne(t, src)
	c, _ := byName(res, "fallback")
	if c.Extracted {
		t.Fatalf("expected rejection for `||` on strings")
	}
}

func TestPicker_Rejects_LogicalNotOnNumber(t *testing.T) {
	src := `export function isZero(n: number): boolean { return !n; }`
	res := pickOne(t, src)
	c, _ := byName(res, "isZero")
	if c.Extracted {
		t.Fatalf("expected rejection for `!` on number")
	}
}

func TestPicker_Rejects_BitwiseOnNumber(t *testing.T) {
	src := `export function lowBit(n: number): number { return n & 1; }`
	res := pickOne(t, src)
	c, _ := byName(res, "lowBit")
	if c.Extracted {
		t.Fatalf("expected rejection for bitwise on number-returning context")
	}
	if c.Reason.Code != "forbidden-operator" {
		t.Fatalf("expected forbidden-operator, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_BooleanAndOr(t *testing.T) {
	// Sanity: && / || / ! on actual booleans must still extract.
	src := `
export function bothPositive(a: number, b: number): boolean { return a > 0 && b > 0; }
export function negate(b: boolean): boolean { return !b; }
export function eitherTrue(a: boolean, b: boolean): boolean { return a || b; }
`
	res := pickOne(t, src)
	for _, name := range []string{"bothPositive", "negate", "eitherTrue"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_NonBoolIfCondition(t *testing.T) {
	src := `
export function isTruthy(n: number): number {
  if (n) return 1;
  return 0;
}`
	res := pickOne(t, src)
	c, _ := byName(res, "isTruthy")
	if c.Extracted {
		t.Fatalf("expected rejection for if (number) — JS truthy coercion not preserved")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_NonBoolWhileCondition(t *testing.T) {
	src := `
export function countDown(n: number): number {
  let m = n;
  while (m) m = m - 1;
  return 0;
}`
	res := pickOne(t, src)
	c, _ := byName(res, "countDown")
	if c.Extracted {
		t.Fatalf("expected rejection for while (number)")
	}
}

func TestPicker_Rejects_NonBoolTernary(t *testing.T) {
	src := `export function pick(n: number): number { return n ? 1 : 0; }`
	res := pickOne(t, src)
	c, _ := byName(res, "pick")
	if c.Extracted {
		t.Fatalf("expected rejection for ternary on non-boolean")
	}
}

func TestPicker_Accepts_BoolFromComparison(t *testing.T) {
	// Sanity check: comparisons return bool so the new requireBoolCondition
	// must accept them. These were all extractable before; assert non-regression.
	src := `
export function nonNegative(n: number): boolean { return n >= 0; }
export function clampPos(x: number): number { return x > 0 ? x : 0; }
`
	res := pickOne(t, src)
	for _, name := range []string{"nonNegative", "clampPos"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_DefaultParam(t *testing.T) {
	src := `export function withDefault(x: number = 5): number { return x + 1; }`
	res := pickOne(t, src)
	c, _ := byName(res, "withDefault")
	if c.Extracted {
		t.Fatalf("expected rejection for default parameter")
	}
	if c.Reason.Code != "unhandled-ast-kind" {
		t.Fatalf("expected unhandled-ast-kind, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_OptionalParam(t *testing.T) {
	src := `export function withOptional(x?: number): number { return (x ?? 0) + 1; }`
	res := pickOne(t, src)
	c, _ := byName(res, "withOptional")
	if c.Extracted {
		t.Fatalf("expected rejection for optional parameter")
	}
	if c.Reason.Code != "unhandled-ast-kind" {
		t.Fatalf("expected unhandled-ast-kind, got %q", c.Reason.Code)
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

func TestPicker_Accepts_AsyncAwaitChain(t *testing.T) {
	// `await` is only useful on a Promise that's reachable from extractable
	// code. The runtime's JS->Go bridge doesn't materialise a Go
	// *promise.Promise[T] from a JS Promise, so Promise<T> is rejected as a
	// param/local type. The only path that survives is awaiting a same-file
	// async function whose return type is Promise<extractable>.
	src := `
export async function inner(): Promise<number> { return 42; }
export async function outer(): Promise<number> {
  const v = await inner();
  return v + 1;
}
`
	res := pickOne(t, src)
	for _, name := range []string{"inner", "outer"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_PromiseAsParam(t *testing.T) {
	src := `
export async function bad(p: Promise<number>): Promise<number> {
  return await p;
}`
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection: Promise<T> param has no JS->Go bridge")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_AwaitOutsideAsync(t *testing.T) {
	// TS would flag this at compile, but the picker must defend against the
	// case where the body walker sees await without the async modifier (e.g.,
	// future support for top-level await or async arrow nesting).
	src := `export function bad(p: Promise<number>): number { return await p; }`
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection for await in non-async function")
	}
}

func TestPicker_Rejects_AwaitNonPromise(t *testing.T) {
	// Awaiting a non-Promise (number) - JS no-op, but emitter would call
	// .Await() on float64 which won't compile. Without `as any` so the
	// as-cast guard doesn't short-circuit before checkAwaitExpr runs.
	src := `export async function bad(n: number): Promise<number> { return await n; }`
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection for await on non-Promise")
	}
	if c.Reason.Code != "await-expression" {
		t.Fatalf("expected await-expression, got %q (detail=%q)", c.Reason.Code, c.Reason.Detail)
	}
}

func TestPicker_Rejects_AwaitObjectPromise(t *testing.T) {
	// Promise<{x: number}> - inner type is non-primitive object. Same-file
	// async returning Promise<R> would be rejected at the inner function's
	// own return-type check before we even reach the await call site, so
	// shape this around a non-primitive Promise inner.
	src := `
interface R { x: number; }
export async function inner(): Promise<R> { return { x: 1 }; }
export async function outer(): Promise<number> {
  const r = await inner();
  return r.x;
}`
	res := pickOne(t, src)
	c, _ := byName(res, "inner")
	if c.Extracted {
		t.Fatalf("expected rejection for Promise<R> return type")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_AsyncReturningPromise(t *testing.T) {
	// Sync-resolving async functions extract via promise.New[T]; the body
	// walker still rejects await/yield so this is a strict subset.
	src := `export async function fetchIt(): Promise<number> { return 42; }`
	res := pickOne(t, src)
	c, ok := byName(res, "fetchIt")
	if !ok || !c.Extracted {
		t.Fatalf("expected `fetchIt` extracted; got %+v", c.Reason)
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

func TestPicker_Rejects_AnonymousObjectParam(t *testing.T) {
	// Anonymous inline object types lower to `jsrt.Obj(u).Get("First").Unwrap().(string)`
	// in the emitter (ramune-runtime reflection). Keep them out.
	src := `export function nameOf(u: { first: string }): string { return u.first; }`
	res := pickOne(t, src)
	c, _ := byName(res, "nameOf")
	if c.Extracted {
		t.Fatalf("expected rejection for anonymous object param")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_NamedInterfaceObjectParam(t *testing.T) {
	// Named interfaces are emitted as Go structs with JSON tags, which the
	// NativeModuleFromFuncs bridge reconstructs field-by-field. Field access
	// (`r.width`) lowers to direct Go struct field access.
	src := `
interface Rect { width: number; height: number; }
export function area(r: Rect): number { return r.width * r.height; }
export function perimeter(r: Rect): number { return 2 * (r.width + r.height); }
`
	res := pickOne(t, src)
	for _, name := range []string{"area", "perimeter"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_NamedInterfaceWithObjectField(t *testing.T) {
	// Nested object field would force the emitter to do recursive jsrt.Obj
	// lookups. Reject named interfaces whose any field is non-primitive.
	src := `
interface Inner { x: number; }
interface Outer { inner: Inner; }
export function getX(o: Outer): number { return o.inner.x; }
`
	res := pickOne(t, src)
	c, _ := byName(res, "getX")
	if c.Extracted {
		t.Fatalf("expected rejection for nested-object interface")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_NamedInterfaceWithMethod(t *testing.T) {
	src := `
interface Greeter { greet(): string; }
export function greet(g: Greeter): string { return g.greet(); }
`
	res := pickOne(t, src)
	c, _ := byName(res, "greet")
	if c.Extracted {
		t.Fatalf("expected rejection for interface with method (call signature)")
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
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_ArrayIndexByString(t *testing.T) {
	src := `export function pick(xs: number[], k: string): number { return xs[k as any]; }`
	res := pickOne(t, src)
	c, _ := byName(res, "pick")
	if c.Extracted {
		t.Fatalf("expected rejection for non-numeric index")
	}
	if c.Reason.Code != "any-type" && c.Reason.Code != "unhandled-ast-kind" {
		// `k as any` widens to `any`; that gets caught either at the index-type
		// gate (unhandled-ast-kind) or at the param walker's any check.
		t.Fatalf("expected any-type or unhandled-ast-kind, got %q", c.Reason.Code)
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

func TestPicker_Accepts_NumberStatics(t *testing.T) {
	src := `
export function big(): number { return Number.MAX_SAFE_INTEGER; }
export function eps(): number { return Number.EPSILON; }
export function inf(): number { return Number.POSITIVE_INFINITY; }
export function isFiniteN(n: number): boolean { return Number.isFinite(n); }
export function isNaNStrict(n: number): boolean { return Number.isNaN(n); }
export function isInt(n: number): boolean { return Number.isInteger(n); }
export function isSafeInt(n: number): boolean { return Number.isSafeInteger(n); }
export function parseF(s: string): number { return Number.parseFloat(s); }
`
	res := pickOne(t, src)
	for _, name := range []string{"big", "eps", "inf", "isFiniteN", "isNaNStrict", "isInt", "isSafeInt", "parseF"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_NumberUnknownConstant(t *testing.T) {
	// Use a constant that exists in lib.d.ts but isn't in our safelist.
	// `Number.NaN` is in safelist; `Number.MIN_NORMAL` would not exist.
	// Pick something tsgo recognizes: extend the namespace via declaration
	// merging so the type-check passes, then verify the picker rejects.
	src := `
declare global { interface NumberConstructor { CUSTOM: number; } }
export function nz(): number { return Number.CUSTOM; }
`
	res := pickOne(t, src)
	c, _ := byName(res, "nz")
	if c.Extracted {
		t.Fatalf("expected rejection for unknown Number constant")
	}
	if c.Reason.Code != "builtin-call" {
		t.Fatalf("expected builtin-call, got %q (detail=%q)", c.Reason.Code, c.Reason.Detail)
	}
}

func TestPicker_Rejects_NumberShadowed(t *testing.T) {
	src := `
export function surprise(n: number): boolean {
  const Number = { isNaN: (_: number) => true };
  return Number.isNaN(n);
}`
	res := pickOne(t, src)
	c, _ := byName(res, "surprise")
	if c.Extracted {
		t.Fatalf("expected rejection when Number is locally shadowed")
	}
}

func TestPicker_Rejects_NumberParseInt(t *testing.T) {
	// Number.parseInt shares the emitter type-mismatch issue with global parseInt.
	src := `export function bad(s: string): number { return Number.parseInt(s); }`
	res := pickOne(t, src)
	c, _ := byName(res, "bad")
	if c.Extracted {
		t.Fatalf("expected rejection for Number.parseInt")
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
	// Rejection fires at checkVarDecl's local-type check - `const Math = { abs }`
	// has object type which isExtractableType rejects, well before the
	// Math.abs call would be examined. Pinning the cascade catches a refactor
	// that admits object types and silently stops exercising the shadow path.
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type (local var has object type), got %q", c.Reason.Code)
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
		t.Fatalf("expected rejection for String() (only Math/Number/string/array are safelisted)")
	}
}

func TestPicker_Accepts_ExtendedStringMethods(t *testing.T) {
	src := `
export function shouty(s: string): string { return s.repeat(3); }
export function sanitize(s: string): string { return s.replaceAll("foo", "bar"); }
export function prefix(s: string): string { return s.slice(0, 3); }
export function mid(s: string): string { return s.substring(1, 4); }
export function leftpad(s: string, n: number): string { return s.padStart(n, "0"); }
export function rightpad(s: string, n: number): string { return s.padEnd(n, " "); }
`
	res := pickOne(t, src)
	for _, name := range []string{"shouty", "sanitize", "prefix", "mid", "leftpad", "rightpad"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
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

func TestPicker_Accepts_ChainedArrayAccess(t *testing.T) {
	src := `
export function firstWord(s: string): string { return s.split(" ")[0]; }
export function pickFirst(a: number, b: number): number { return [a, b][0]; }
`
	res := pickOne(t, src)
	for _, name := range []string{"firstWord", "pickFirst"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
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
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type (array element must be primitive), got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_EmptyArrayLiteral(t *testing.T) {
	// tsgo types `[]` as `never[]` even with a `number[]` return annotation,
	// so the element-must-be-primitive gate trips. Emitter would still produce
	// `[]any{}` which compiles, but accepting it would be inconsistent with
	// our other primitive-element guarantees. Skip rather than emit something
	// behaviorally inert.
	src := `export function empty(): number[] { return []; }`
	res := pickOne(t, src)
	c, _ := byName(res, "empty")
	if c.Extracted {
		t.Fatalf("expected rejection: tsgo gives [] type never[], not number[]")
	}
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
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
	if c.Reason.Code != "spread-element" {
		t.Fatalf("expected spread-element, got %q", c.Reason.Code)
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

// TestPicker_Rejects_ArrayMapCallback: inline arrow callbacks to callback-
// taking array methods are rejected because the hybrid picker has no way to
// extract a free-floating closure. A bare *JSFunc parameter would be
// accepted (see TestPicker_JSFunc_Accept_ArrayMapWithParam in jsfunc_test.go).
func TestPicker_Rejects_ArrayMapCallback(t *testing.T) {
	src := `export function doubled(xs: number[]): number[] { return xs.map(x => x * 2); }`
	res := pickOne(t, src)
	c, _ := byName(res, "doubled")
	if c.Extracted {
		t.Fatalf("expected rejection for .map with inline arrow")
	}
	if c.Reason.Code != "inline-function-literal" {
		t.Fatalf("expected inline-function-literal, got %q", c.Reason.Code)
	}
}

func TestPicker_Accepts_ArrayJoinSliceConcatReverse(t *testing.T) {
	src := `
export function joined(xs: string[]): string { return xs.join(","); }
export function first2(xs: number[]): number[] { return xs.slice(0, 2); }
export function combined(a: number[], b: number[]): number[] { return a.concat(b); }
export function reversed(xs: number[]): number[] { return xs.reverse(); }
`
	res := pickOne(t, src)
	for _, name := range []string{"joined", "first2", "combined", "reversed"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Accepts_ForOfPrimitiveArray(t *testing.T) {
	src := `
export function sumOf(xs: number[]): number {
  let t = 0;
  for (const x of xs) t = t + x;
  return t;
}
export function joinedSpace(xs: string[]): string {
  let out = "";
  for (const s of xs) out = out + " " + s;
  return out.trim();
}`
	res := pickOne(t, src)
	for _, name := range []string{"sumOf", "joinedSpace"} {
		c, ok := byName(res, name)
		if !ok || !c.Extracted {
			t.Fatalf("expected `%s` extracted; got %+v", name, c.Reason)
		}
	}
}

func TestPicker_Rejects_ForOfDestructuring(t *testing.T) {
	src := `
export function pairs(xs: number[]): number {
  let t = 0;
  for (const [a, b] of [[1, 2]]) t = t + a;
  return t;
}`
	res := pickOne(t, src)
	c, _ := byName(res, "pairs")
	if c.Extracted {
		t.Fatalf("expected rejection for for-of with destructuring")
	}
	if c.Reason.Code != "unhandled-ast-kind" {
		t.Fatalf("expected unhandled-ast-kind, got %q", c.Reason.Code)
	}
}

func TestPicker_Rejects_ForAwaitOf(t *testing.T) {
	src := `
export async function collect(xs: AsyncIterable<number>): Promise<number> {
  let t = 0;
  for await (const x of xs) t = t + x;
  return t;
}`
	res := pickOne(t, src)
	c, _ := byName(res, "collect")
	if c.Extracted {
		t.Fatalf("expected rejection for for-await-of body")
	}
	// AsyncIterable<number> is an object type at the param level, so the
	// signature gate fires first. If async-only Promise<T> functions ever
	// gain extra body checks, this assertion needs to update to match the
	// new precedence.
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type (AsyncIterable param), got %q", c.Reason.Code)
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
	if c.Reason.Code != "object-type" {
		t.Fatalf("expected object-type, got %q", c.Reason.Code)
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
	if c.Reason.Code != "unhandled-ast-kind" {
		t.Fatalf("expected unhandled-ast-kind, got %q", c.Reason.Code)
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
