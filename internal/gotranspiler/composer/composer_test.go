package composer_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/composer"
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/bundled"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

func setupProgram(t *testing.T, source string) (*ast.SourceFile, *compiler.Program, string) {
	t.Helper()
	dir := t.TempDir()
	filename := filepath.Join(dir, "input.ts")
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	abs, _ := filepath.Abs(filename)
	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCachedFSCompilerHost(filepath.Dir(abs), fs, bundled.LibPath(), nil, nil)
	cfg := tsoptions.NewParsedCommandLine(
		&core.CompilerOptions{NoEmit: core.TSTrue, SkipLibCheck: core.TSTrue, AllowJs: core.TSTrue},
		[]string{abs},
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          filepath.Dir(abs),
		},
	)
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         cfg,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})
	var sf *ast.SourceFile
	for _, f := range program.SourceFiles() {
		if f.FileName() == abs {
			sf = f
			break
		}
	}
	if sf == nil {
		t.Fatalf("source file missing: %s", abs)
	}
	return sf, program, abs
}

func TestCompose_MixedExtractAndSkip(t *testing.T) {
	src := `
export function add(a: number, b: number): number { return a + b; }
export function scale(x: number, k: number): number { return x * k; }
export function parseUser(input: any): any { return input; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{
		PkgName:          "native_app",
		NativeModuleName: "native:app",
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	// Picker decisions.
	var addC, scaleC, parseC bool
	for _, c := range res.PickerResult.Candidates {
		switch c.Name {
		case "add":
			addC = c.Extracted
		case "scale":
			scaleC = c.Extracted
		case "parseUser":
			parseC = c.Extracted
		}
	}
	if !addC || !scaleC {
		t.Fatalf("expected add and scale extracted, got: %+v", res.PickerResult.Candidates)
	}
	if parseC {
		t.Fatalf("expected parseUser skipped, got extracted")
	}

	// Go source sanity checks.
	if res.GoSource == "" {
		t.Fatalf("expected non-empty Go source")
	}
	if !strings.Contains(res.GoSource, "package native_app") {
		t.Fatalf("Go source missing package decl: %s", res.GoSource)
	}
	if !strings.Contains(res.GoSource, "func Add(") {
		t.Fatalf("Go source missing func Add: %s", res.GoSource)
	}
	if !strings.Contains(res.GoSource, "func Scale(") {
		t.Fatalf("Go source missing func Scale: %s", res.GoSource)
	}
	if strings.Contains(res.GoSource, "func ParseUser(") {
		t.Fatalf("Go source unexpectedly contains ParseUser: %s", res.GoSource)
	}

	// Parse the Go source to verify syntactic validity.
	_, err = parser.ParseFile(token.NewFileSet(), "out.go", res.GoSource, 0)
	if err != nil {
		t.Fatalf("emitted Go fails to parse: %v\n%s", err, res.GoSource)
	}

	// DiscoverExportedFuncs integration — the downstream compileCmd uses this.
	funcs, err := gotranspiler.DiscoverExportedFuncs(res.GoSource)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	names := map[string]bool{}
	for _, f := range funcs {
		names[f.JSName] = true
	}
	if !names["add"] || !names["scale"] {
		t.Fatalf("discovered functions missing add/scale: %+v", funcs)
	}
	if names["parseUser"] {
		t.Fatalf("discovered unexpectedly contains parseUser")
	}

	// Shim JS sanity checks.
	if res.ShimJS == "" {
		t.Fatalf("expected non-empty shim")
	}
	for _, want := range []string{
		"__ramuneNativeInstalled",
		`require("native:app")`,
		"_me.add = mod.add",
		"_me.scale = mod.scale",
		"globalThis.add = mod.add",
	} {
		if !strings.Contains(res.ShimJS, want) {
			t.Fatalf("shim missing %q; shim:\n%s", want, res.ShimJS)
		}
	}
	if strings.Contains(res.ShimJS, "parseUser") {
		t.Fatalf("shim unexpectedly references parseUser")
	}

	// ExportedJSNames order matches source order.
	if len(res.ExportedJSNames) != 2 || res.ExportedJSNames[0] != "add" || res.ExportedJSNames[1] != "scale" {
		t.Fatalf("ExportedJSNames wrong: %v", res.ExportedJSNames)
	}
}

func TestCompose_AllSkipped_EmptyArtifacts(t *testing.T) {
	src := `export function echo(x: any): any { return x; }`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if res.GoSource != "" {
		t.Fatalf("expected empty Go source, got %q", res.GoSource)
	}
	if res.ShimJS != "" {
		t.Fatalf("expected empty shim, got %q", res.ShimJS)
	}
	if len(res.ExportedJSNames) != 0 {
		t.Fatalf("expected no exports")
	}
}

func TestCompose_ArtifactInspection(t *testing.T) {
	// Visual-inspection test: prints the actual artifacts for a canonical input
	// so developers can eyeball the output. Run with `go test -v -run ArtifactInspection`.
	src := `
export function add(a: number, b: number): number { return a + b; }
export function fib(n: number): number {
  if (n < 2) return n;
  return fib(n-1) + fib(n-2);
}
export function parseUser(x: any): any { return x; }
`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{PkgName: "native_app", NativeModuleName: "native:demo"})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	var b strings.Builder
	res.PickerResult.Format(&b)
	t.Logf("--- Picker Report ---\n%s", b.String())
	t.Logf("--- Emitted Go ---\n%s", res.GoSource)
	t.Logf("--- Shim JS ---\n%s", res.ShimJS)
}

func TestCompose_SelfRecursiveExtracted(t *testing.T) {
	src := `export function fib(n: number): number { if (n < 2) return n; return fib(n-1) + fib(n-2); }`
	sf, program, _ := setupProgram(t, src)
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	res, err := composer.Compose(sf, ck, composer.Options{})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(res.GoSource, "func Fib(") {
		t.Fatalf("Fib decl missing: %s", res.GoSource)
	}
	// Recursive call inside the body must resolve to the exported name (Fib),
	// not the package-local goVarName form (fib) — else the emitted Go won't
	// compile. Count `Fib(` occurrences: one for the decl, two for recursive calls.
	if n := strings.Count(res.GoSource, "Fib("); n < 3 {
		t.Fatalf("expected >=3 Fib( occurrences (decl + 2 recursive calls), got %d: %s", n, res.GoSource)
	}
	// And confirm no stray lowercase `fib(` call slipped through.
	if strings.Contains(res.GoSource, " fib(") || strings.Contains(res.GoSource, "+fib(") || strings.Contains(res.GoSource, "(fib(") {
		t.Fatalf("unexpected lowercase fib( call: %s", res.GoSource)
	}
	// Verify Go syntactic validity.
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", res.GoSource, 0); err != nil {
		t.Fatalf("parse: %v\n%s", err, res.GoSource)
	}
}
