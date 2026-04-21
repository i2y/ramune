package picker_test

import (
	"strings"
	"testing"
)

// TestPicker_JSFunc_Accept_PrimitiveCallback covers the simplest callback
// shape: a function from number to number, called once, with primitive args.
func TestPicker_JSFunc_Accept_PrimitiveCallback(t *testing.T) {
	src := `export function apply(cb: (n: number) => number, x: number): number { return cb(x); }`
	res := pickOne(t, src)
	c, ok := byName(res, "apply")
	if !ok {
		t.Fatalf("candidate `apply` missing: %+v", res.Candidates)
	}
	if !c.Extracted {
		t.Fatalf("expected `apply` extracted; got reason %+v", c.Reason)
	}
}

// TestPicker_JSFunc_Accept_VoidCallback covers callbacks that return void —
// the emitter lowers these to a no-return IIFE and must still surface errors.
func TestPicker_JSFunc_Accept_VoidCallback(t *testing.T) {
	src := `export function invoke(cb: (n: number) => void, x: number): void { cb(x); }`
	res := pickOne(t, src)
	c, _ := byName(res, "invoke")
	if !c.Extracted {
		t.Fatalf("expected `invoke` extracted; got reason %+v", c.Reason)
	}
}

// TestPicker_JSFunc_Accept_StringCallback covers string-in / string-out
// callbacks so the type-assertion path for non-float64 returns is exercised.
func TestPicker_JSFunc_Accept_StringCallback(t *testing.T) {
	src := `export function wrap(cb: (s: string) => string, raw: string): string { return cb(raw); }`
	res := pickOne(t, src)
	c, _ := byName(res, "wrap")
	if !c.Extracted {
		t.Fatalf("expected `wrap` extracted; got reason %+v", c.Reason)
	}
}

// TestPicker_JSFunc_Accept_MultiArgCallback covers a callback with more than
// one arg — ensures the variadic Call(args...) lowering walks every arg.
func TestPicker_JSFunc_Accept_MultiArgCallback(t *testing.T) {
	src := `export function combine(cb: (a: number, b: number) => number, x: number, y: number): number { return cb(x, y); }`
	res := pickOne(t, src)
	c, _ := byName(res, "combine")
	if !c.Extracted {
		t.Fatalf("expected `combine` extracted; got reason %+v", c.Reason)
	}
}

// TestPicker_JSFunc_Reject_ValueUse ensures a callback referenced outside
// call-head position gets rejected. Storing a callback in a local implicitly
// requires a non-extractable local type (a callable), which fails the
// extractable-type gate; the rejection is a correctness guard regardless of
// which gate fires first.
func TestPicker_JSFunc_Reject_ValueUse(t *testing.T) {
	src := `export function saveThenApply(cb: (n: number) => number, x: number): number { const c = cb; return c(x); }`
	res := pickOne(t, src)
	c, _ := byName(res, "saveThenApply")
	if c.Extracted {
		t.Fatalf("expected `saveThenApply` rejected; got extracted")
	}
}

// TestPicker_JSFunc_Reject_PassedAsArg ensures a callback passed to another
// function (rather than invoked) is rejected. This is the value-use path that
// lives purely in the walker — checkIdentifierRef must flag it.
func TestPicker_JSFunc_Reject_PassedAsArg(t *testing.T) {
	src := `
export function neighbor(cb: (n: number) => number): number { return cb(1); }
export function outer(cb: (n: number) => number): number { return neighbor(cb); }
`
	res := pickOne(t, src)
	o, _ := byName(res, "outer")
	if o.Extracted {
		t.Fatalf("expected `outer` rejected (cb passed to neighbor as value); got extracted")
	}
	if !strings.Contains(o.Reason.Detail, "call-head") {
		t.Fatalf("expected call-head rejection reason, got %+v", o.Reason)
	}
}

// TestPicker_JSFunc_Reject_Nested ensures a callback-of-callback (higher-order)
// param signature is not admitted.
func TestPicker_JSFunc_Reject_Nested(t *testing.T) {
	src := `export function twice(cb: (inner: (n: number) => number) => number): number { return cb((n) => n + 1); }`
	res := pickOne(t, src)
	c, _ := byName(res, "twice")
	if c.Extracted {
		t.Fatalf("expected `twice` rejected (nested callable param); got extracted")
	}
}

// TestPicker_JSFunc_Reject_AnyReturn ensures a callback whose declared return
// type is `any` falls through the same extractable-return guard that applies
// to plain param types.
func TestPicker_JSFunc_Reject_AnyReturn(t *testing.T) {
	src := `export function apply(cb: (n: number) => any, x: number): number { return cb(x) + 0; }`
	res := pickOne(t, src)
	c, _ := byName(res, "apply")
	if c.Extracted {
		t.Fatalf("expected `apply` rejected (any return in callback); got extracted")
	}
}

// TestPicker_JSFunc_Reject_OptionalParam ensures a callback whose param list
// contains an optional/default/rest parameter is rejected. These would all
// cause the JS-side call to succeed with a shape the extracted Go signature
// cannot accept cleanly.
func TestPicker_JSFunc_Reject_RestParamInCallback(t *testing.T) {
	src := `export function apply(cb: (...xs: number[]) => number): number { return cb(1, 2, 3); }`
	res := pickOne(t, src)
	c, _ := byName(res, "apply")
	if c.Extracted {
		t.Fatalf("expected `apply` rejected (rest in callback); got extracted")
	}
}

// Callback-taking array methods (map / filter / forEach / some / every).

func TestPicker_JSFunc_Accept_ArrayMapWithParam(t *testing.T) {
	src := `export function doubled(cb: (n: number) => number, xs: number[]): number[] { return xs.map(cb); }`
	res := pickOne(t, src)
	c, _ := byName(res, "doubled")
	if !c.Extracted {
		t.Fatalf("expected `doubled` extracted; got reason %+v", c.Reason)
	}
}

func TestPicker_JSFunc_Accept_ArrayFilterWithParam(t *testing.T) {
	src := `export function positives(cb: (n: number) => boolean, xs: number[]): number[] { return xs.filter(cb); }`
	res := pickOne(t, src)
	c, _ := byName(res, "positives")
	if !c.Extracted {
		t.Fatalf("expected `positives` extracted; got reason %+v", c.Reason)
	}
}

func TestPicker_JSFunc_Accept_ArrayForEachWithParam(t *testing.T) {
	src := `export function visit(cb: (n: number) => void, xs: number[]): void { xs.forEach(cb); }`
	res := pickOne(t, src)
	c, _ := byName(res, "visit")
	if !c.Extracted {
		t.Fatalf("expected `visit` extracted; got reason %+v", c.Reason)
	}
}

func TestPicker_JSFunc_Accept_ArraySomeEveryWithParam(t *testing.T) {
	src := `
export function anyPositive(cb: (n: number) => boolean, xs: number[]): boolean { return xs.some(cb); }
export function allPositive(cb: (n: number) => boolean, xs: number[]): boolean { return xs.every(cb); }
`
	res := pickOne(t, src)
	for _, name := range []string{"anyPositive", "allPositive"} {
		c, _ := byName(res, name)
		if !c.Extracted {
			t.Fatalf("expected `%s` extracted; got reason %+v", name, c.Reason)
		}
	}
}

// TestPicker_JSFunc_Reject_ArrayFilterReturnsNumber: filter/some/every all
// require the callback to return boolean — a number-returning callback
// would silently coerce in JS but have no sound Go lowering.
func TestPicker_JSFunc_Reject_ArrayFilterReturnsNumber(t *testing.T) {
	src := `export function f(cb: (n: number) => number, xs: number[]): number[] { return xs.filter(cb); }`
	res := pickOne(t, src)
	c, _ := byName(res, "f")
	if c.Extracted {
		t.Fatalf("expected `f` rejected (filter requires bool-returning cb); got extracted")
	}
}

// TestPicker_JSFunc_Reject_ArrayMapInlineArrow: inline arrow callbacks stay
// rejected — the picker has no way to hoist a closure into the extracted
// Go module.
func TestPicker_JSFunc_Reject_ArrayMapInlineArrow(t *testing.T) {
	src := `export function doubled(xs: number[]): number[] { return xs.map(x => x * 2); }`
	res := pickOne(t, src)
	c, _ := byName(res, "doubled")
	if c.Extracted {
		t.Fatalf("expected `doubled` rejected (inline arrow); got extracted")
	}
	if c.Reason.Code != "inline-function-literal" {
		t.Fatalf("want inline-function-literal, got %q", c.Reason.Code)
	}
}

// TestPicker_JSFunc_Reject_ArrayMapExtraCallbackArg: extra args on
// callback-taking methods (e.g. `.map(cb, thisArg)`) are rejected — the
// extracted IIFE has no equivalent slot and the JS semantics would drift.
func TestPicker_JSFunc_Reject_ArrayMapExtraCallbackArg(t *testing.T) {
	src := `export function f(cb: (n: number) => number, xs: number[]): number[] { return xs.map(cb, cb); }`
	res := pickOne(t, src)
	c, _ := byName(res, "f")
	if c.Extracted {
		t.Fatalf("expected `f` rejected (extra .map arg); got extracted")
	}
}

// TestPicker_JSFunc_Reject_ArrayMapCbWithTwoParams: the extracted loop passes
// only the element to .Call — a two-param callback would read `undefined`
// for index in JS, breaking observable semantics. Reject to keep the
// lowering honest.
func TestPicker_JSFunc_Reject_ArrayMapCbWithTwoParams(t *testing.T) {
	src := `export function f(cb: (n: number, i: number) => number, xs: number[]): number[] { return xs.map(cb); }`
	res := pickOne(t, src)
	c, _ := byName(res, "f")
	if c.Extracted {
		t.Fatalf("expected `f` rejected (2-param cb not allowed on map); got extracted")
	}
}
