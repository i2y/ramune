// Command ramune-toolchain is the development toolchain for ramune: check, fmt,
// lint, transpile, typegen, compile. It is split out from the main ramune CLI
// to keep `ramune run`/`serve`/`eval`/etc. free of the tsgo + rslint + gotranspiler
// init overhead (which adds ~8ms of startup on first launch and inflates the
// main binary by ~100MB). `ramune` dispatches to this binary for those
// subcommands.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune"
	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/composer"
	"github.com/i2y/ramune/internal/rslint/config"
	"github.com/i2y/ramune/internal/rslint/linter"
	"github.com/i2y/ramune/internal/rslint/rule"
	rast "github.com/i2y/ramune/internal/rslint/shim/ast"
	rcompiler "github.com/i2y/ramune/internal/rslint/shim/compiler"
	rcore "github.com/i2y/ramune/internal/rslint/shim/core"
	rscanner "github.com/i2y/ramune/internal/rslint/shim/scanner"
	rosvfs "github.com/i2y/ramune/internal/rslint/shim/vfs/osvfs"
	rslintutils "github.com/i2y/ramune/internal/rslint/utils"
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/format"
	"github.com/i2y/ramune/internal/tsgo/ls/lsutil"
	"github.com/i2y/ramune/internal/tsgo/parser"
	"github.com/i2y/ramune/internal/tsgo/scanner"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

// Set at build time via -ldflags "-X main.version=..."
var version = ""

func getVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ramune-toolchain <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: check, check-single, fmt, lint, transpile, typegen, compile")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		checkCmd(os.Args[2:])
	case "check-single":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: ramune-toolchain check-single <file>")
			os.Exit(2)
		}
		if err := runTypeCheck(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "fmt":
		fmtCmd(os.Args[2:])
	case "lint":
		lintCmd(os.Args[2:])
	case "transpile":
		transpileCmd(os.Args[2:])
	case "typegen":
		typegenCmd(os.Args[2:])
	case "compile":
		compileCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(getVersion())
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func isTypeScript(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".ts" || ext == ".tsx"
}

func isJSOrTS(filename string) bool {
	ext := filepath.Ext(filename)
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs", ".cts":
		return true
	}
	return false
}

func scriptKindForFile(filename string) core.ScriptKind {
	kind := core.GetScriptKindFromFileName(filename)
	if kind == core.ScriptKindUnknown {
		return core.ScriptKindJS
	}
	return kind
}

func transformTypeScript(filename string, code []byte) ([]byte, error) {
	loader := api.LoaderTS
	if strings.HasSuffix(filename, ".tsx") {
		loader = api.LoaderTSX
	}
	result := api.Transform(string(code), api.TransformOptions{
		Sourcefile: filepath.Base(filename),
		Loader:     loader,
		Target:     api.ESNext,
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Text
		}
		return nil, fmt.Errorf("TypeScript: %s", strings.Join(msgs, "; "))
	}
	out := string(result.Code)
	exportedNames := []string{}
	out = regexp.MustCompile(`export\s+default\s+`).ReplaceAllStringFunc(out, func(m string) string {
		exportedNames = append(exportedNames, "default")
		return "module.exports = "
	})
	re := regexp.MustCompile(`export\s+(const|let|var|function|class)\s+(\w+)`)
	out = re.ReplaceAllStringFunc(out, func(m string) string {
		parts := re.FindStringSubmatch(m)
		if len(parts) >= 3 {
			exportedNames = append(exportedNames, parts[2])
		}
		return parts[1] + " " + parts[2]
	})
	exportBlockRe := regexp.MustCompile(`(?m)^export\s*\{([^}]*)\};?\s*$`)
	out = exportBlockRe.ReplaceAllStringFunc(out, func(m string) string {
		inner := exportBlockRe.FindStringSubmatch(m)
		if len(inner) >= 2 {
			for _, name := range strings.Split(inner[1], ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					exportedNames = append(exportedNames, name)
				}
			}
		}
		return ""
	})
	for _, name := range exportedNames {
		if name != "default" {
			out += fmt.Sprintf("\nexports.%s = %s;", name, name)
		}
	}
	return []byte(out), nil
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ", ") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func findRamuneModPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for dir != "/" && dir != "." {
		modFile := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modFile); err == nil {
			if strings.Contains(string(data), "module github.com/i2y/ramune") {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	cwd, _ := os.Getwd()
	for cwd != "/" && cwd != "." {
		modFile := filepath.Join(cwd, "go.mod")
		if data, err := os.ReadFile(modFile); err == nil {
			if strings.Contains(string(data), "module github.com/i2y/ramune") {
				return cwd
			}
		}
		cwd = filepath.Dir(cwd)
	}
	return ""
}

// collectFiles walks the given targets, skipping node_modules, and returns
// all paths passing filter. If normalize is true, paths are returned as
// tspath.NormalizePath(filepath.Abs(path)).
func collectFiles(targets []string, filter func(string) bool, normalize bool) ([]string, error) {
	normOne := func(p string) string {
		if !normalize {
			return p
		}
		abs, _ := filepath.Abs(p)
		return tspath.NormalizePath(abs)
	}
	var files []string
	for _, arg := range targets {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			filepath.Walk(arg, func(path string, fi os.FileInfo, err error) error {
				if fi != nil && fi.IsDir() && fi.Name() == "node_modules" {
					return filepath.SkipDir
				}
				if filter(path) {
					files = append(files, normOne(path))
				}
				return nil
			})
		} else {
			files = append(files, normOne(arg))
		}
	}
	return files, nil
}

// typeCheckFiles runs tsgo's semantic + syntactic diagnostics over the
// given normalized absolute paths, emitting each diagnostic via emit.
func typeCheckFiles(normalizedPaths []string, emit func(string)) {
	cwd, _ := os.Getwd()
	fs := osvfs.FS()
	host := compiler.NewCachedFSCompilerHost(cwd, fs, "", nil, nil)

	opts := &core.CompilerOptions{
		NoEmit: core.TSTrue,
		Strict: core.TSTrue,
		// Match `ramune run` / `runfile.go` defaults so .tsx files
		// don't fail with TS17004 "Cannot use JSX unless --jsx is
		// provided". The factory dotted name is resolved by tsgo at
		// emit time the same way TypeScript proper handles
		// "React.createElement".
		Jsx:                core.JsxEmitReact,
		JsxFactory:         "Ramune.tui.h",
		JsxFragmentFactory: "Ramune.tui.Fragment",
	}
	config := tsoptions.NewParsedCommandLine(opts, normalizedPaths, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
		CurrentDirectory:          cwd,
	})
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	ctx := context.Background()
	for _, sourceFile := range program.SourceFiles() {
		if sourceFile.IsDeclarationFile {
			continue
		}
		diags := program.GetSemanticDiagnostics(ctx, sourceFile)
		diags = append(diags, program.GetSyntacticDiagnostics(ctx, sourceFile)...)
		for _, d := range diags {
			if d.File() != nil {
				line, col := scanner.GetECMALineAndUTF16CharacterOfPosition(d.File(), d.Pos())
				emit(fmt.Sprintf("%s(%d,%d): error TS%d: %s",
					d.File().FileName(), line+1, col+1, d.Code(), d.String()))
			} else {
				emit(fmt.Sprintf("error TS%d: %s", d.Code(), d.String()))
			}
		}
	}
}

func checkCmd(args []string) {
	if len(args) < 1 {
		args = []string{"."}
	}
	files, err := collectFiles(args, isTypeScript, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No TypeScript files found")
		os.Exit(1)
	}

	hasErrors := false
	typeCheckFiles(files, func(msg string) {
		hasErrors = true
		fmt.Fprintln(os.Stderr, msg)
	})
	if hasErrors {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✓ %d file(s) checked, no errors\n", len(files))
}

func lintCmd(args []string) {
	fset := flag.NewFlagSet("lint", flag.ExitOnError)
	fix := fset.Bool("fix", false, "automatically fix lint issues")
	fset.Parse(args)

	targets := fset.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}
	files, err := collectFiles(targets, isJSOrTS, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No JS/TS files found")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	fs := rosvfs.FS()
	host := rslintutils.CreateCompilerHost(cwd, fs)

	compilerOpts := &rcore.CompilerOptions{
		NoEmit:       rcore.TSTrue,
		AllowJs:      rcore.TSTrue,
		CheckJs:      rcore.TSFalse,
		Strict:       rcore.TSTrue,
		SkipLibCheck: rcore.TSTrue,
	}

	program, err := rslintutils.CreateProgramFromOptions(true, compilerOpts, files, host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	config.RegisterAllRules()

	loader := config.NewConfigLoader(fs, cwd)
	rslintConfig, _, _, configErr := loader.LoadConfiguration("")
	useAllRules := configErr != nil
	hasErrors := false

	_, err = linter.RunLinter(
		[]*rcompiler.Program{program},
		true,
		files,
		nil,
		nil,
		func(sourceFile *rast.SourceFile) []linter.ConfiguredRule {
			if useAllRules {
				var rules []linter.ConfiguredRule
				for name, r := range config.GlobalRuleRegistry.GetAllRules() {
					ruleCopy := r
					rules = append(rules, linter.ConfiguredRule{
						Name:     name,
						Severity: rule.SeverityWarning,
						Run: func(ctx rule.RuleContext) rule.RuleListeners {
							return ruleCopy.Run(ctx, nil)
						},
					})
				}
				return rules
			}
			rules, _ := config.GlobalRuleRegistry.GetEnabledRules(rslintConfig, sourceFile.FileName(), cwd, false)
			return rules
		},
		false,
		func(d rule.RuleDiagnostic) {
			hasErrors = true
			severity := "warning"
			if d.Severity == rule.SeverityError {
				severity = "error"
			}
			if d.SourceFile != nil {
				line, col := rscanner.GetECMALineAndUTF16CharacterOfPosition(d.SourceFile, d.Range.Pos())
				fmt.Fprintf(os.Stderr, "%s(%d,%d): %s [%s] %s\n",
					d.SourceFile.FileName(), line+1, col+1, d.Message.Description, d.RuleName, severity)
			} else {
				fmt.Fprintf(os.Stderr, "%s [%s] %s\n", d.Message.Description, d.RuleName, severity)
			}

			if *fix && d.SourceFile != nil {
				fixes := d.Fixes()
				if len(fixes) > 0 {
					source := d.SourceFile.Text()
					changes := make([]rcore.TextChange, len(fixes))
					for i, f := range fixes {
						changes[i] = rcore.TextChange{TextRange: f.Range, NewText: f.Text}
					}
					result := rcore.ApplyBulkEdits(source, changes)
					os.WriteFile(d.SourceFile.FileName(), []byte(result), 0644)
				}
			}
		},
		nil,
		nil,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if hasErrors {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✓ %d file(s) linted, no issues\n", len(files))
}

func runTypeCheck(filename string) error {
	absPath, _ := filepath.Abs(filename)
	var errs []string
	typeCheckFiles([]string{tspath.NormalizePath(absPath)}, func(msg string) {
		errs = append(errs, msg)
	})
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

func fmtCmd(args []string) {
	fset := flag.NewFlagSet("fmt", flag.ExitOnError)
	checkOnly := fset.Bool("check", false, "check formatting without writing changes")
	fset.Parse(args)

	targets := fset.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}
	files, err := collectFiles(targets, isJSOrTS, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No JS/TS files found")
		os.Exit(1)
	}

	opts := lsutil.GetDefaultFormatCodeSettings()
	ctx := format.WithFormatCodeSettings(context.Background(), opts, "\n")

	hasErrors := false
	formatted := 0
	for _, f := range files {
		source, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", f, err)
			hasErrors = true
			continue
		}

		absPath, _ := filepath.Abs(f)
		normalizedPath := tspath.NormalizePath(absPath)
		sourceText := string(source)
		sourceFile := parser.ParseSourceFile(
			ast.SourceFileParseOptions{
				FileName: normalizedPath,
				Path:     tspath.Path(normalizedPath),
			},
			sourceText,
			scriptKindForFile(f),
		)

		changes := format.FormatDocument(ctx, sourceFile)
		if len(changes) == 0 {
			continue
		}

		if *checkOnly {
			fmt.Fprintf(os.Stderr, "%s\n", f)
			hasErrors = true
			continue
		}

		result := core.ApplyBulkEdits(sourceText, changes)
		if err := os.WriteFile(f, []byte(result), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", f, err)
			hasErrors = true
			continue
		}
		formatted++
	}

	if *checkOnly {
		if hasErrors {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "✓ %d file(s) already formatted\n", len(files))
	} else if !hasErrors {
		fmt.Fprintf(os.Stderr, "✓ %d file(s) formatted\n", formatted)
	}

	if hasErrors && !*checkOnly {
		os.Exit(1)
	}
}

func transpileCmd(args []string) {
	fs := flag.NewFlagSet("transpile", flag.ExitOnError)
	var outDir, moduleName string
	var doCompile bool
	fs.StringVar(&outDir, "o", "", "output directory (or binary name with --compile)")
	fs.StringVar(&moduleName, "module", "", "Go module name (default: derived from entry file)")
	fs.BoolVar(&doCompile, "compile", false, "compile to binary after transpilation")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: ramune transpile <files...> [-o output] [--compile] [--module name]")
		os.Exit(1)
	}

	files := fs.Args()
	entryFile := files[0]

	if moduleName == "" {
		moduleName = strings.TrimSuffix(filepath.Base(entryFile), filepath.Ext(entryFile))
	}

	var outBinary string
	if doCompile {
		outBinary = outDir
		if outBinary == "" {
			outBinary = moduleName
		}
		tmpDir, err := os.MkdirTemp("", "ramune-transpile-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tmpDir)
		outDir = tmpDir
	} else if outDir == "" {
		outDir = "."
	}

	hasNodeModules := false
	if cwd, err := os.Getwd(); err == nil {
		if info, err := os.Stat(filepath.Join(cwd, "node_modules")); err == nil && info.IsDir() {
			hasNodeModules = true
		}
	}

	if len(files) == 1 {
		fmt.Fprintf(os.Stderr, "transpiling %s → Go...\n", entryFile)
		if err := gotranspiler.TranspileToDir(entryFile, outDir, "main"); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else if hasNodeModules {
		fmt.Fprintf(os.Stderr, "transpiling %d files → Go project %s (with npm)...\n", len(files), moduleName)
		if err := gotranspiler.TranspileProjectToDirWithNpm(files, entryFile, outDir, moduleName); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "transpiling %d files → Go project %s...\n", len(files), moduleName)
		if err := gotranspiler.TranspileProjectToDir(files, entryFile, outDir, moduleName); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	if doCompile {
		if ramuneModPath := findRamuneModPath(); ramuneModPath != "" {
			goModPath := filepath.Join(outDir, "go.mod")
			goModData, _ := os.ReadFile(goModPath)
			goModData = append(goModData, []byte(fmt.Sprintf("\nreplace github.com/i2y/ramune => %s\n", ramuneModPath))...)
			os.WriteFile(goModPath, goModData, 0o644)
		}

		tidyCmd := exec.Command("go", "mod", "tidy")
		tidyCmd.Dir = outDir
		tidyCmd.Stderr = os.Stderr
		if err := tidyCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "go mod tidy error: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "compiling %s...\n", outBinary)
		absOut, _ := filepath.Abs(outBinary)
		buildCmd := exec.Command("go", "build", "-o", absOut, ".")
		buildCmd.Dir = outDir
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "go build error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "✓ built %s\n", outBinary)
	} else {
		fmt.Fprintf(os.Stderr, "✓ wrote to %s\n", outDir)
	}
}

func typegenCmd(args []string) {
	fs := flag.NewFlagSet("typegen", flag.ExitOnError)
	var outFile string
	fs.StringVar(&outFile, "o", "go.d.ts", "output .d.ts file")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: ramune typegen <go:pkg...> [-o output.d.ts]")
		os.Exit(1)
	}

	content, err := gotranspiler.GenerateDTS(fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outFile, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", outFile, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", outFile)
}

// writeRamuneModule drops a go.mod into tmpDir wired against either the
// local ramune checkout (preferred — picks up the toolchain's own
// in-flight changes) or the published version that matches the running
// toolchain. fallbackVer is used only when neither is detectable. Runs
// `go mod tidy` so callers can immediately invoke the build tool.
func writeRamuneModule(tmpDir, moduleName, fallbackVer string) error {
	goMod := fmt.Sprintf("module %s\n\ngo 1.26\n", moduleName)
	if local := findRamuneModPath(); local != "" {
		goMod += fmt.Sprintf("\nrequire github.com/i2y/ramune v0.0.0\n\nreplace github.com/i2y/ramune => %s\n", local)
	} else {
		ver := getVersion()
		if ver == "" || ver == "dev" {
			ver = fallbackVer
		} else if !strings.HasPrefix(ver, "v") {
			ver = "v" + ver
		}
		goMod += fmt.Sprintf("\nrequire github.com/i2y/ramune %s\n", ver)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}
	return nil
}

// wasmABINumerics is the set of Go type names that round-trip through
// TinyGo's WASI export ABI as wasm i32/i64/f32/f64. Anything outside it
// (string, slice, struct, interface, pointer-to-struct, …) gets dropped
// from the export list with a CLI warning.
var wasmABINumerics = map[string]bool{
	"float64": true, "float32": true,
	"int": true, "int32": true, "int64": true,
	"uint": true, "uint32": true, "uint64": true,
	"bool": true,
}

// wasmExportSkip records why an exported Go function couldn't be wrapped
// for the WASI ABI; the CLI surfaces these as warnings.
type wasmExportSkip struct {
	name   string
	reason string
}

// wasmExportableFuncs partitions the picker-emitted Go source's exported
// top-level functions into ABI-shaped exports and a skipped list with
// reasons. Reuses gotranspiler.DiscoverExportedFuncs's parse pass — the
// new ABI gate runs purely against ExportedFunc.Params/.Return.
func wasmExportableFuncs(goSource string) ([]gotranspiler.ExportedFunc, []wasmExportSkip, error) {
	funcs, err := gotranspiler.DiscoverExportedFuncs(goSource)
	if err != nil {
		return nil, nil, err
	}
	var exports []gotranspiler.ExportedFunc
	var skipped []wasmExportSkip
	for _, fn := range funcs {
		if fn.Generic {
			skipped = append(skipped, wasmExportSkip{fn.GoName, "generic"})
			continue
		}
		bad := ""
		for _, ty := range fn.Params {
			if !wasmABINumerics[ty] {
				bad = fmt.Sprintf("non-numeric param type %q", ty)
				break
			}
		}
		if bad == "" && fn.Return != "" && !wasmABINumerics[fn.Return] {
			bad = fmt.Sprintf("non-numeric return type %q", fn.Return)
		}
		if bad != "" {
			skipped = append(skipped, wasmExportSkip{fn.GoName, bad})
			continue
		}
		exports = append(exports, fn)
	}
	return exports, skipped, nil
}

// generateWasmMainGo builds the main package for the wasm-wasi build:
// `_` import on the extracted package + a `//go:wasmexport` wrapper per
// numerics-only function so the produced .wasm exposes named exports
// callable from a wasm host (wazero, wasmtime …).
func generateWasmMainGo(pkgAlias string, exports []gotranspiler.ExportedFunc) string {
	var b strings.Builder
	b.WriteString("package main\n\n")
	if len(exports) == 0 {
		// No numerics-only exports — emit the bare-minimum module so
		// the build still produces a wasm artifact for inspection.
		fmt.Fprintf(&b, "import _ %q\n\nfunc main() {}\n", "ramune-wasm-app/"+pkgAlias)
		return b.String()
	}
	fmt.Fprintf(&b, "import app %q\n\n", "ramune-wasm-app/"+pkgAlias)
	b.WriteString("func main() {}\n\n")
	for _, e := range exports {
		fmt.Fprintf(&b, "//go:wasmexport %s\n", e.JSName)
		fmt.Fprintf(&b, "func wasmExport_%s(", e.GoName)
		var argNames []string
		for i, ty := range e.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			argName := fmt.Sprintf("a%d", i)
			argNames = append(argNames, argName)
			fmt.Fprintf(&b, "%s %s", argName, ty)
		}
		b.WriteString(") ")
		if e.Return != "" {
			b.WriteString(e.Return + " ")
		}
		b.WriteString("{ ")
		if e.Return != "" {
			b.WriteString("return ")
		}
		fmt.Fprintf(&b, "app.%s(%s)", e.GoName, strings.Join(argNames, ", "))
		b.WriteString(" }\n\n")
	}
	return b.String()
}

// compileWasmWASI runs the standalone-WASM build path: TS source → picker
// extraction → emitted Go (Backend="tinygo") → tinygo build -target=wasi.
// The artifact is a single .wasm file containing only the picker-extracted
// Go and the jsbridge interface. No JS runtime is bundled, so unextracted
// TS code (top-level statements, untyped helpers) is silently dropped —
// the compile is "library-style": the wasm exposes the extracted functions
// for a wasm host (wazero, wasmtime, …) to call. Top-level TS code that
// the picker rejects produces no observable behaviour at runtime.
func compileWasmWASI(filename, output string, hybridReport bool) {
	if _, err := exec.LookPath("tinygo"); err != nil {
		fmt.Fprintln(os.Stderr, "error: --target wasm-wasi requires `tinygo` on PATH (https://tinygo.org/getting-started/install/)")
		os.Exit(1)
	}
	if !isTypeScript(filename) {
		fmt.Fprintf(os.Stderr, "error: --target wasm-wasi requires a TypeScript source (got %q)\n", filename)
		os.Exit(1)
	}

	safeBase := gotranspiler.GoPackageName(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	pkgAlias := "nativehybrid_" + safeBase
	res, err := composer.ComposeFile(filename, composer.Options{
		PkgName:          pkgAlias,
		NativeModuleName: "native:__hybrid_" + safeBase + "__",
		Backend:          gotranspiler.BackendTinyGo,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wasm compose error: %v\n", err)
		os.Exit(1)
	}
	if hybridReport {
		res.PickerResult.Format(os.Stderr)
	}
	if res.GoSource == "" {
		fmt.Fprintln(os.Stderr, "error: picker extracted no functions — nothing to emit. Add explicit return types and avoid dynamic features (eval, reflect, late `this`).")
		os.Exit(1)
	}

	if output == "" {
		output = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)) + ".wasm"
	}
	if !strings.HasSuffix(output, ".wasm") {
		output += ".wasm"
	}

	tmpDir, err := os.MkdirTemp("", "ramune-wasm-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	pkgDir := filepath.Join(tmpDir, pkgAlias)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating wasm pkg dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "module.go"), []byte(res.GoSource), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing wasm module: %v\n", err)
		os.Exit(1)
	}

	exports, skipped, err := wasmExportableFuncs(res.GoSource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wasm export discovery: %v\n", err)
		os.Exit(1)
	}
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "  warning: skipping wasm export for %s — %s (only float64/int/bool numerics are exportable under TinyGo's WASI ABI)\n", s.name, s.reason)
	}

	mainGo := generateWasmMainGo(pkgAlias, exports)
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing wasm main: %v\n", err)
		os.Exit(1)
	}

	if err := writeRamuneModule(tmpDir, "ramune-wasm-app", "v0.21.0"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	absOutput, _ := filepath.Abs(output)
	// -buildmode=c-shared produces a WASI reactor (exports `_initialize`,
	// no `_start`), which host runtimes need to call individual exports
	// after runtime init. The default buildmode produces a one-shot
	// command that panics inside wasmExportCheckRun when an export is
	// invoked outside of _start's scope.
	tinygoCmd := exec.Command("tinygo", "build", "-target=wasi", "-buildmode=c-shared", "-o", absOutput, ".")
	tinygoCmd.Dir = tmpDir
	if out, err := tinygoCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "tinygo build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "compiled wasm: %s\n", absOutput)
	if len(exports) > 0 {
		fmt.Fprintf(os.Stderr, "exports (call after `_initialize`):\n")
		for _, e := range exports {
			fmt.Fprintf(os.Stderr, "  %s(%s)", e.JSName, strings.Join(e.Params, ", "))
			if e.Return != "" {
				fmt.Fprintf(os.Stderr, " %s", e.Return)
			}
			fmt.Fprintln(os.Stderr)
		}
	}
}

// accumulateNativeFuncs appends the native-import line and bridge-code block
// for a single native extension module to imports/modules, prints the discovery
// summary and any generic warnings, and is a no-op when funcs is empty.
func accumulateNativeFuncs(modName, pkgImport, pkgAlias string, funcs []gotranspiler.ExportedFunc, imports, modules *string) {
	if len(funcs) == 0 {
		return
	}
	*imports += gotranspiler.GenerateNativeImport(pkgImport, pkgAlias)
	*modules += gotranspiler.GenerateBridgeCode("native:"+modName, pkgAlias, funcs)
	nonGeneric := 0
	for _, f := range funcs {
		if !f.Generic {
			nonGeneric++
		}
	}
	fmt.Fprintf(os.Stderr, "native: %s → %d functions\n", modName, nonGeneric)
	for _, w := range gotranspiler.GenericWarnings(funcs) {
		fmt.Fprintln(os.Stderr, w)
	}
}

func compileCmd(args []string) {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	var output, buildTags, hybridBackend, target string
	var minify, httpMode, hybrid, hybridReport bool
	var nativeFiles stringSliceFlag
	fs.StringVar(&output, "o", "", "output binary name")
	fs.BoolVar(&minify, "minify", false, "minify bundled JS")
	fs.BoolVar(&httpMode, "http", false, "run event loop for HTTP server")
	fs.BoolVar(&hybrid, "hybrid", false, "extract type-confirmed TS functions to native Go (JS runtime remains the fallback)")
	fs.BoolVar(&hybridReport, "hybrid-report", false, "print the picker's extraction report to stderr (implies --hybrid)")
	fs.Var(&nativeFiles, "native", "TypeScript file to transpile as native extension (repeatable)")
	fs.StringVar(&buildTags, "tags", "", "comma-separated Go build tags for the embedded runtime (e.g. qjswasm,goja)")
	fs.StringVar(&hybridBackend, "hybrid-backend", "", "emit Go for backend: \"go\" (default, drags ramune host) or \"tinygo\" (jsbridge.Func interface, TinyGo-compat). Empty = \"go\".")
	fs.StringVar(&target, "target", "", "build target: empty (default, native binary with JS runtime) or \"wasm-wasi\" (TinyGo standalone .wasm of picker-extracted Go, no JS runtime). \"wasm-wasi\" implies --hybrid --hybrid-backend tinygo and requires `tinygo` on PATH.")
	fs.Parse(args)
	if hybridReport {
		hybrid = true
	}
	if target == "wasm-wasi" {
		hybrid = true
		if hybridBackend == "" {
			hybridBackend = string(gotranspiler.BackendTinyGo)
		}
		if hybridBackend != string(gotranspiler.BackendTinyGo) {
			fmt.Fprintf(os.Stderr, "error: --target wasm-wasi requires --hybrid-backend tinygo (got %q)\n", hybridBackend)
			os.Exit(2)
		}
	}
	backend, err := gotranspiler.ParseBackend(hybridBackend)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --hybrid-backend %v\n", err)
		os.Exit(2)
	}
	switch target {
	case "", "wasm-wasi":
		// ok
	default:
		fmt.Fprintf(os.Stderr, "error: --target must be empty or \"wasm-wasi\" (got %q)\n", target)
		os.Exit(2)
	}

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: ramune compile <file> [-o output] [--minify] [--http]")
		os.Exit(1)
	}
	filename := fs.Arg(0)

	if target == "wasm-wasi" {
		compileWasmWASI(filename, output, hybridReport)
		return
	}

	code, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if isTypeScript(filename) {
		code, err = transformTypeScript(filename, code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	absPath, _ := filepath.Abs(filename)
	external := append(ramune.NodeBuiltins, "bun:sqlite", "native:*")
	buildOpts := api.BuildOptions{
		EntryPoints: []string{absPath},
		Bundle:      true,
		Format:      api.FormatCommonJS,
		Platform:    api.PlatformNode,
		Write:       false,
		External:    external,
		LogLevel:    api.LogLevelWarning,
		MainFields:  []string{"module", "main"},
	}
	if minify {
		buildOpts.MinifySyntax = true
		buildOpts.MinifyWhitespace = true
		buildOpts.MinifyIdentifiers = true
	}

	buildResult := api.Build(buildOpts)
	bundledJS := string(code)
	if len(buildResult.Errors) == 0 && len(buildResult.OutputFiles) > 0 {
		bundledJS = string(buildResult.OutputFiles[0].Contents)
	}

	if output == "" {
		output = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}

	tmpDir, err := os.MkdirTemp("", "ramune-compile-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	var nativeImports, nativeModules string
	if hybrid && isTypeScript(filename) {
		safeBase := gotranspiler.GoPackageName(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
		pkgAlias := "nativehybrid_" + safeBase
		modName := "__hybrid_" + safeBase + "__"
		res, err := composer.ComposeFile(filename, composer.Options{
			PkgName:          pkgAlias,
			NativeModuleName: "native:" + modName,
			Backend:          backend,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "hybrid compose error: %v\n", err)
			os.Exit(1)
		}
		if hybridReport {
			res.PickerResult.Format(os.Stderr)
		}
		if res.GoSource != "" {
			pkgDir := filepath.Join(tmpDir, pkgAlias)
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error creating hybrid dir: %v\n", err)
				os.Exit(1)
			}
			// Fixed file name avoids `_test.go` / `_<GOOS>.go` /
			// `_<GOARCH>.go` suffixes that the Go toolchain would treat
			// as build-constrained — e.g. a TS file called cross-test.ts
			// would otherwise produce nativehybrid_cross_test.go and get
			// excluded from the build as a test file.
			if err := os.WriteFile(filepath.Join(pkgDir, "module.go"), []byte(res.GoSource), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing hybrid module: %v\n", err)
				os.Exit(1)
			}
			funcs, err := gotranspiler.DiscoverExportedFuncs(res.GoSource)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hybrid discovery error: %v\n", err)
				os.Exit(1)
			}
			accumulateNativeFuncs(modName, "ramune-compiled-app/"+pkgAlias, pkgAlias, funcs, &nativeImports, &nativeModules)
			// Prepend so the shim's module-scope reassignments override
			// hoisted user bindings before any top-level call runs. See
			// BuildShimWithClasses for the hoisting rationale.
			bundledJS = res.ShimJS + bundledJS
		}
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "app.bundle.js"), []byte(bundledJS), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	eventLoop := `rt.RunEventLoop()`
	if httpMode {
		eventLoop = `for { if err := rt.RunEventLoopFor(365 * 24 * time.Hour); err != nil { log.Fatal(err) } }`
	}
	timeImport := ""
	if httpMode {
		timeImport = `"time"`
	}

	if len(nativeFiles) == 1 {
		nf := nativeFiles[0]
		baseName := strings.TrimSuffix(filepath.Base(nf), filepath.Ext(nf))
		pkgAlias := "native" + gotranspiler.GoPackageName(baseName)
		pkgDir := filepath.Join(tmpDir, pkgAlias)
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating native dir: %v\n", err)
			os.Exit(1)
		}
		result, err := gotranspiler.TranspileLibraryFile(nf, pkgAlias)
		if err != nil {
			fmt.Fprintf(os.Stderr, "native transpile error (%s): %v\n", nf, err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "module.go"), []byte(result.GoSource), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing native module: %v\n", err)
			os.Exit(1)
		}
		funcs, err := gotranspiler.DiscoverExportedFuncs(result.GoSource)
		if err != nil {
			fmt.Fprintf(os.Stderr, "native discovery error (%s): %v\n", nf, err)
			os.Exit(1)
		}
		accumulateNativeFuncs(baseName, "ramune-compiled-app/"+pkgAlias, pkgAlias, funcs, &nativeImports, &nativeModules)
	} else if len(nativeFiles) > 1 {
		projResult, err := gotranspiler.TranspileProject(nativeFiles, "__none__", "", "ramune-compiled-app")
		if err != nil {
			fmt.Fprintf(os.Stderr, "native transpile error: %v\n", err)
			os.Exit(1)
		}
		for relPath, goSource := range projResult.Files {
			outPath := filepath.Join(tmpDir, relPath)
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "error creating native dir: %v\n", err)
				os.Exit(1)
			}
			if err := os.WriteFile(outPath, []byte(goSource), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error writing native module: %v\n", err)
				os.Exit(1)
			}
			funcs, err := gotranspiler.DiscoverExportedFuncs(goSource)
			if err != nil || len(funcs) == 0 {
				continue
			}
			dir := filepath.Dir(relPath)
			var pkgAlias, modName, pkgImport string
			if dir == "." {
				base := strings.TrimSuffix(filepath.Base(relPath), ".go")
				pkgAlias = "native" + base
				modName = base
				pkgImport = "ramune-compiled-app/" + base
			} else {
				pkgAlias = "native" + strings.ReplaceAll(dir, "/", "_")
				modName = filepath.Base(dir)
				pkgImport = "ramune-compiled-app/" + dir
			}
			accumulateNativeFuncs(modName, pkgImport, pkgAlias, funcs, &nativeImports, &nativeModules)
		}
	}

	runtimeOpts := "ramune.NodeCompat(), ramune.WithFetch()"
	if nativeModules != "" {
		runtimeOpts += ",\n" + nativeModules
	}

	mainGo := fmt.Sprintf(`package main

import (
	_ "embed"
	"log"
	%s

	"github.com/i2y/ramune"
%s)

//go:embed app.bundle.js
var appJS string

func main() {
	rt, err := ramune.New(%s)
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	if err := rt.Exec(appJS); err != nil {
		log.Fatal(err)
	}
	%s
}
`, timeImport, nativeImports, runtimeOpts, eventLoop)

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := writeRamuneModule(tmpDir, "ramune-compiled-app", "v0.15.1"); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	absOutput, _ := filepath.Abs(output)
	goBuildArgs := []string{"build", "-o", absOutput}
	// Default to nowebview so the embedded JS runtime doesn't pull
	// glaze → purego → libdl/libpthread/libc into ELF NEEDED. Apps that
	// genuinely need the WebView surface can opt back in by passing
	// `--tags webview` (which we honour by dropping nowebview).
	tagSet := map[string]bool{"nowebview": true}
	for _, t := range strings.Split(buildTags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tagSet[t] = true
		}
	}
	if tagSet["webview"] {
		delete(tagSet, "nowebview")
	}
	mergedTags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		mergedTags = append(mergedTags, t)
	}
	sort.Strings(mergedTags)
	if len(mergedTags) > 0 {
		goBuildArgs = append(goBuildArgs, "-tags", strings.Join(mergedTags, ","))
	}
	goBuildArgs = append(goBuildArgs, ".")
	buildCmd := exec.Command("go", goBuildArgs...)
	buildCmd.Dir = tmpDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "go build failed: %v\n", err)
		os.Exit(1)
	}

	if goruntime.GOOS == "darwin" {
		entPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>com.apple.security.cs.allow-jit</key><true/>
<key>com.apple.security.cs.allow-unsigned-executable-memory</key><true/>
</dict></plist>`
		entFile := filepath.Join(tmpDir, "ent.plist")
		os.WriteFile(entFile, []byte(entPlist), 0o644)
		exec.Command("codesign", "--force", "--sign", "-", "--entitlements", entFile, absOutput).Run()
	}

	fmt.Printf("compiled: %s\n", output)
}
