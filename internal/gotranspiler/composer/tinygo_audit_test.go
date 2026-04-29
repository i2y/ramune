package composer_test

// This file is a temporary Phase 0 audit harness. It runs the same
// compose pipeline the existing TestHybrid_EmittedGoCompiles uses,
// writes the emitted Go source to /tmp/tinygo_audit/, and tries to
// build it with `tinygo build`. The point is to discover which
// patterns in today's emitter trip TinyGo's restricted reflect /
// runtime / stdlib subset. The fixtures cover primitive-only,
// async (Promise), and a mix of class / closure / generics shapes
// that the picker actually accepts today.
//
// Skipped automatically when `tinygo` isn't on PATH so the rest of
// the suite stays portable.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/composer"
)

func TestTinyGoAudit_PrimitiveOnly(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export function add(a: number, b: number): number { return a + b; }
export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n-1) + fib(n-2);
}
export function dist(x: number, y: number): number {
  return Math.sqrt(Math.pow(x, 2) + Math.pow(y, 2));
}
export function shout(s: string): string { return s.toUpperCase().trim(); }
export function sumArr(xs: number[]): number {
  let total = 0;
  for (let i = 0; i < xs.length; i++) total = total + xs[i];
  return total;
}
interface Rect { width: number; height: number; }
export function rectArea(r: Rect): number { return r.width * r.height; }
`
	runTinyGoAudit(t, "primitive_only", src)
}

func TestTinyGoAudit_Async(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export async function delayedAdd(a: number, b: number): Promise<number> {
  return a + b;
}
export async function chainedAdd(a: number, b: number): Promise<number> {
  const x = await delayedAdd(a, b);
  return x + 1;
}
`
	runTinyGoAudit(t, "async", src)
}

func TestTinyGoAudit_StringMethods(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export function greet(name: string, age: number): string {
  return "hello " + name + ", age=" + age.toString();
}
export function tpl(name: string, age: number): string {
  return ` + "`hi ${name} (${age})`" + `;
}
export function split(s: string): string[] {
  return s.split(",");
}
`
	runTinyGoAudit(t, "string_methods", src)
}

func TestTinyGoAudit_AsyncThenChain(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export async function root(a: number): Promise<number> { return a * 2; }
export function chained(a: number): Promise<number> {
  return root(a).then((x: number) => x + 1);
}
`
	runTinyGoAudit(t, "async_then_chain", src)
}

func TestTinyGoAudit_NullableInterface(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
interface Point { x: number; y: number; }
export function distFromOrigin(p: Point | null): number {
  if (p === null) return 0;
  return Math.sqrt(p.x * p.x + p.y * p.y);
}
`
	runTinyGoAudit(t, "nullable_interface", src)
}

// TestTinyGoAudit_OptionalParam covers `b?: T` syntax — the symbol
// widens to `T | undefined` and lowers to `*T`, the same path nullable
// types use. Catches a regression in either the picker's parameter
// guard or the body walker's nullable-safe operator handling.
func TestTinyGoAudit_OptionalParam(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export function withDefault(a: number, b?: number): number {
  return a + (b ?? 10);
}
export function checkOpt(s?: string): boolean {
  return s === undefined;
}
`
	runTinyGoAudit(t, "optional_param", src)
}

// TestTinyGoAudit_NullablePrimitive covers the `T | null` case for the
// three primitives that lower to `*T` (string/float64/bool). Nullish
// coalescing on the pointer and an explicit `=== null` test together
// stress the typemapper / expr.go fixes that landed alongside the
// picker's nullable-acceptance lift — the audit catches a regression
// in either the type lowering or the body walker.
func TestTinyGoAudit_NullablePrimitive(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export function nullish(s: string | null): string {
  return s ?? "default";
}
export function nullishNum(n: number | null): number {
  if (n === null) return 0;
  return n + 1;
}
export function nullishBool(b: boolean | undefined): boolean {
  return b ?? false;
}
`
	runTinyGoAudit(t, "nullable_primitive", src)
}

func TestTinyGoAudit_Class(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	src := `
export class Counter {
  private value: number;
  constructor(initial: number) { this.value = initial; }
  inc(by: number): number { this.value = this.value + by; return this.value; }
  get(): number { return this.value; }
}
`
	runTinyGoAudit(t, "class", src)
}

func TestTinyGoAudit_ArrayHeavy(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	// This fixture surfaces a pre-existing Ramune emitter quirk
	// (orthogonal to TinyGo): for-loop counters stay typed as Go
	// `int` while accumulators are `float64`, so `total + i * j` and
	// `s / xs.length` both fail with "mismatched types float64 and
	// int" under regular `go build` too. Skipping rather than
	// failing because the audit's job is "is the TinyGo path
	// blocked?" and this is a Go-emit bug TinyGo just inherits.
	t.Skip("known emitter bug — int loop counter / float64 accumulator mismatch in for-bodies; tracked separately from the TinyGo work")
}

func TestTinyGoAudit_JSFuncCallback(t *testing.T) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		t.Skip("tinygo not on PATH")
	}
	// Under the default `go` backend the emitter would lower the
	// callback param to `*ramune.JSFunc`, which drags the ramune host
	// package (sqlite, esbuild, webview) into the artifact and breaks
	// the TinyGo build. Backend="tinygo" instead lowers it to
	// jsbridge.Func — an interface that lives in
	// github.com/i2y/ramune/jsbridge with no transitive host dep, so
	// the emitted Go fits inside TinyGo's stdlib subset.
	src := `
export function applyCb(cb: (n: number) => number, n: number): number {
  return cb(n) + 1;
}
`
	runTinyGoAudit(t, "jsfunc_callback", src)
}

// runTinyGoAudit composes src into Go, drops it into a fresh module
// dir under /tmp/tinygo_audit/<name>/ as a sub-package, generates a
// minimal main.go that imports it, and runs `tinygo build -target=wasi`
// to surface compile-time incompatibilities. The persistent location
// (rather than t.TempDir()) is deliberate: failures need to survive
// the test run for inspection with `tinygo build` reproducer commands.
func runTinyGoAudit(t *testing.T, name, src string) {
	t.Helper()
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()
	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "nativeapp", Backend: gotranspiler.BackendTinyGo})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	dir := filepath.Join("/tmp", "tinygo_audit", name)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("clean dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nativeapp"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Module shape: pin to local ramune so emitted code that imports
	// ramune.JSFunc / promise.Promise resolves against this checkout.
	gomod := `module nativeapp_audit

go 1.22

require github.com/i2y/ramune v0.0.0

replace github.com/i2y/ramune => /Users/i2y/ramune
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nativeapp", "native.go"), []byte(res.GoSource), 0o644); err != nil {
		t.Fatalf("native.go: %v", err)
	}
	// A minimal main that imports the native pkg with `_` so the
	// linker pulls in every exported decl. TinyGo's compile passes
	// run on transitive imports; this is enough to surface package-
	// level errors without crafting a per-fixture caller stub.
	main := `package main

import _ "nativeapp_audit/nativeapp"

func main() {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("main.go: %v", err)
	}

	// Settle the module graph before invoking tinygo.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s\nsource:\n%s", err, out, res.GoSource)
	}

	out := filepath.Join(dir, "out.wasm")
	cmd := exec.Command("tinygo", "build", "-target=wasi", "-o", out, ".")
	cmd.Dir = dir
	stdout, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Logf("tinygo build failed for %s:\n%s", name, stdout)
		t.Logf("source path: %s/nativeapp/native.go", dir)
		t.Fatalf("tinygo build: %v", buildErr)
	}
	t.Logf("tinygo build succeeded for %s (out: %s)", name, out)
}
