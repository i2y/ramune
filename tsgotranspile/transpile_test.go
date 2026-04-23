package tsgotranspile

import (
	"strings"
	"testing"

	"github.com/i2y/ramune/internal/tsgo/core"
)

func TestTranspileTypeStrip(t *testing.T) {
	src := `const greet = (name: string): string => "Hello, " + name;
console.log(greet("world"));
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if strings.Contains(r.JS, ": string") {
		t.Fatalf("type annotation not stripped:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, `const greet`) && !strings.Contains(r.JS, `var greet`) {
		t.Fatalf("expected declaration retained:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, `console.log`) {
		t.Fatalf("expected body retained:\n%s", r.JS)
	}
}

func TestTranspileCJSExportsShape(t *testing.T) {
	src := `export const a = 1;
export function f() { return 2; }
export default 3;
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	// tsgo CJS emit wires exports directly; shape must not contain raw ESM keywords.
	if strings.Contains(r.JS, "\nexport ") {
		t.Fatalf("ESM export kept in CJS emit:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, "exports.a") {
		t.Fatalf("expected exports.a in CJS emit:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, "exports.f") {
		t.Fatalf("expected exports.f in CJS emit:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, "exports.default") {
		t.Fatalf("expected exports.default in CJS emit:\n%s", r.JS)
	}
}

func TestTranspileEnum(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
const c = Color.Green;
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	// Enum should be lowered to a runtime object.
	if strings.Contains(r.JS, "enum ") {
		t.Fatalf("enum keyword not lowered:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, "Color") {
		t.Fatalf("enum identifier missing:\n%s", r.JS)
	}
}

func TestTranspileNamespace(t *testing.T) {
	src := `namespace M {
  export const v = 1;
}
const x = M.v;
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if strings.Contains(r.JS, "namespace ") {
		t.Fatalf("namespace keyword not lowered:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, "M.v") {
		t.Fatalf("namespace usage missing:\n%s", r.JS)
	}
}

func TestTranspileParameterProperty(t *testing.T) {
	src := `class P {
  constructor(public x: number) {}
}
const p = new P(1);
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	// Parameter property should be materialized as this.x = x.
	if !strings.Contains(r.JS, "this.x = x") {
		t.Fatalf("parameter property not expanded:\n%s", r.JS)
	}
}

func TestTranspileAsyncAtES2017(t *testing.T) {
	src := `async function f() { return 1; }
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetES2017,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	// At ES2017 async/await is native, so "async function" should remain.
	if !strings.Contains(r.JS, "async function") {
		t.Fatalf("async function lost at ES2017:\n%s", r.JS)
	}
}

func TestTranspileForAwaitAtES2017(t *testing.T) {
	src := `async function f(it) {
  for await (const x of it) {
    console.log(x);
  }
}
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetES2017,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	// for await ... of is post-ES2017; at this target tsgo should lower it.
	if strings.Contains(r.JS, "for await") {
		t.Fatalf("for-await not lowered at ES2017:\n%s", r.JS)
	}
}

func TestTranspilePrivateClassFieldAtES2017(t *testing.T) {
	src := `class C {
  #x = 1;
  get() { return this.#x; }
}
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetES2017,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if strings.Contains(r.JS, "#x") {
		t.Fatalf("private field not lowered at ES2017:\n%s", r.JS)
	}
}

func TestTranspilePrivateClassFieldAtESNext(t *testing.T) {
	src := `class C {
  #x = 1;
  get() { return this.#x; }
}
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if !strings.Contains(r.JS, "#x") {
		t.Fatalf("private field should be preserved at ESNext:\n%s", r.JS)
	}
}

func TestTranspileTopLevelAwaitVerbatimInCJS(t *testing.T) {
	// tsc / tsgo CJS emit does NOT lower top-level await. The standard
	// TypeScript behaviour is to emit it verbatim and raise the semantic
	// diagnostic "TS1378". Callers that need TLA-in-CJS lowered to an
	// IIFE (the goja fallback) must pre-wrap or stay on esbuild.
	src := `const v = await Promise.resolve(1);
`
	r, err := Transpile(src, Options{
		Target: core.ScriptTargetES2017,
		Module: core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if !strings.Contains(r.JS, "await Promise.resolve(1)") {
		t.Fatalf("expected TLA to pass through verbatim, got:\n%s", r.JS)
	}
}

func TestTranspileJSXPreserve(t *testing.T) {
	src := `const el = <div>hi</div>;
`
	r, err := Transpile(src, Options{
		FileName: "input.tsx",
		Target:   core.ScriptTargetESNext,
		Module:   core.ModuleKindCommonJS,
		JSX:      core.JsxEmitPreserve,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if !strings.Contains(r.JS, "<div>") {
		t.Fatalf("JSX should be preserved:\n%s", r.JS)
	}
}

func TestTranspileJSXReact(t *testing.T) {
	src := `const el = <div>hi</div>;
`
	r, err := Transpile(src, Options{
		FileName: "input.tsx",
		Target:   core.ScriptTargetESNext,
		Module:   core.ModuleKindCommonJS,
		JSX:      core.JsxEmitReact,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if strings.Contains(r.JS, "<div>") {
		t.Fatalf("JSX should be transformed out at JsxEmitReact:\n%s", r.JS)
	}
	if !strings.Contains(r.JS, "createElement") {
		t.Fatalf("expected React.createElement in JSX emit:\n%s", r.JS)
	}
}

func TestTranspileAllowJsInput(t *testing.T) {
	src := `const x = 1;
module.exports = x;
`
	r, err := Transpile(src, Options{
		FileName: "input.js",
		Target:   core.ScriptTargetESNext,
		Module:   core.ModuleKindCommonJS,
	})
	if err != nil {
		t.Fatalf("Transpile: %v", err)
	}
	if !strings.Contains(r.JS, "module.exports") {
		t.Fatalf("expected module.exports retained in JS passthrough:\n%s", r.JS)
	}
}

func TestTranspileSyntaxError(t *testing.T) {
	src := `const x = ;
`
	r, _ := Transpile(src, Options{
		Target: core.ScriptTargetESNext,
		Module: core.ModuleKindCommonJS,
	})
	if len(r.Diagnostics) == 0 {
		t.Fatalf("expected syntactic diagnostic for broken input, got none; js=%q", r.JS)
	}
}
