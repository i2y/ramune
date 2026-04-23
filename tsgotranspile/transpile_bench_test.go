package tsgotranspile

import (
	"testing"

	"github.com/i2y/ramune/internal/tsgo/core"
)

// Small single-expression REPL line.
var benchREPLLine = `const x: number = 1; x + 2`

// Typical Workers handler — ~800 bytes of TS with type annotations.
var benchWorkersHandler = `export default {
  route: "/api/hello",
  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const name = url.searchParams.get("name") || "world";
    return Response.json({
      message: ` + "`Hello, ${name}!`" + `,
      method: request.method,
      style: "workers",
    });
  },
};`

// Medium-size module: class + enum + generics.
var benchMediumModule = `enum Color { Red, Green, Blue }
export class Box<T> {
  #value: T;
  constructor(v: T) { this.#value = v; }
  get value(): T { return this.#value; }
  map<U>(f: (t: T) => U): Box<U> { return new Box(f(this.#value)); }
}
export function tint(c: Color): string {
  switch (c) {
    case Color.Red:   return "#f00";
    case Color.Green: return "#0f0";
    case Color.Blue:  return "#00f";
  }
}
export async function* gen(): AsyncGenerator<number> {
  for (let i = 0; i < 5; i++) yield i;
}
`

func benchOptsPreserve() Options {
	return Options{
		FileName: "bench.ts",
		Target:   core.ScriptTargetESNext,
		Module:   core.ModuleKindPreserve,
	}
}

func benchOptsCJS() Options {
	return Options{
		FileName: "bench.ts",
		Target:   core.ScriptTargetESNext,
		Module:   core.ModuleKindCommonJS,
	}
}

func BenchmarkTranspileREPLLine(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Transpile(benchREPLLine, benchOptsPreserve()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTranspileWorkersHandler(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Transpile(benchWorkersHandler, benchOptsCJS()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTranspileMediumModule(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Transpile(benchMediumModule, benchOptsCJS()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTranspileColdPerCall constructs a *fresh* Transpiler for each
// call, forcing the lib.d.ts parse cost every time. This is what a naive
// implementation without caching would pay. Use this as the upper bound
// when evaluating whether a caller needs to hold a Transpiler.
func BenchmarkTranspileColdPerCall(b *testing.B) {
	for i := 0; i < b.N; i++ {
		t := New()
		if _, err := t.Transpile(benchREPLLine, benchOptsPreserve()); err != nil {
			b.Fatal(err)
		}
	}
}
