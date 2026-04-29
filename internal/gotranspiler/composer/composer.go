// Package composer orchestrates the hybrid TS→Go extraction pipeline.
//
// Given a TypeScript source file and its tsgo checker, it runs the picker,
// transpiles the approved top-level declarations to a Go source module, and
// produces a JS postlude that swaps module.exports entries at boot time with
// the native implementations registered via NativeModuleFromFuncs.
//
// The composer is backend-neutral: the resulting Go and JS artifacts compile
// and run under -tags goja, -tags qjswasm, or the default JSC backend.
package composer

import (
	"context"
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/picker"
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
	"github.com/i2y/ramune/internal/tsgo/compiler"
)

// Options controls composer behavior.
type Options struct {
	// PkgName is the Go package name for the emitted native module.
	// Defaults to "native_app".
	PkgName string
	// NativeModuleName is the JS require() specifier registered with
	// NativeModuleFromFuncs. Defaults to "native:__transpiled_app__".
	NativeModuleName string
	// Backend chooses the JS-function bridge concrete type emitted for
	// TS callable params. See gotranspiler.Backend for the semantics of
	// each value; the zero value selects gotranspiler.BackendGo.
	Backend gotranspiler.Backend
}

// Result holds the artifacts produced for one source file.
type Result struct {
	PickerResult picker.Result
	// GoSource is the formatted Go source for the extracted native module.
	// Empty when no candidates were extracted.
	GoSource string
	// ShimJS is the ES5 postlude to append to the JS bundle. Empty when no
	// candidates were extracted.
	ShimJS string
	// ExportedJSNames are the JS-visible function names (camelCase) that will
	// be swapped at boot. Same set as ShimJS function references, in source
	// order.
	ExportedJSNames []string
	// ExportedJSClasses are the JS-visible class names (PascalCase, as declared
	// in TS) that the shim swaps. Each Counter binds to the factory registered
	// under `mod.newCounter` so `new Counter(...)` returns the native instance.
	ExportedJSClasses []string
}

// ComposeFile is a file-path wrapper that builds the tsgo Program, enumerates
// every user TypeScript source file reachable from filename's import graph
// (excluding .d.ts and node_modules), picks extractable declarations across
// all of them, and merges the output into a single Go source + shim.
//
// Functions declared in imported files are now eligible for extraction — the
// picker walks each user SourceFile with a shared top-level function name
// set, so `entry.ts`'s call to `fib()` from `kernel.ts` is accepted.
//
// Callers that already hold a single-file *ast.SourceFile + *checker.Checker
// and want single-file behaviour can call Compose directly.
func ComposeFile(filename string, opts Options) (*Result, error) {
	program, sf, err := gotranspiler.BuildProgramForFile(filename)
	if err != nil {
		return nil, err
	}
	ck, done := program.GetTypeCheckerForFile(context.Background(), sf)
	defer done()

	userFiles := userSourceFiles(program)
	if len(userFiles) <= 1 {
		return Compose(sf, ck, opts)
	}
	return composeAll(userFiles, ck, opts)
}

// userSourceFiles returns the SourceFiles in program that represent user
// code: not .d.ts declaration files, not under a node_modules path. Lib
// files bundled with tsgo are all .d.ts so they drop out via the first
// predicate.
func userSourceFiles(program *compiler.Program) []*ast.SourceFile {
	var out []*ast.SourceFile
	for _, f := range program.SourceFiles() {
		if f == nil || f.IsDeclarationFile {
			continue
		}
		if strings.Contains(f.FileName(), "/node_modules/") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// composeAll runs the picker across all supplied user SourceFiles with a
// shared top-level function name set, transpiles the merged extracted nodes
// into one Go package, and builds one shim for all of them.
func composeAll(files []*ast.SourceFile, ck *checker.Checker, opts Options) (*Result, error) {
	if opts.PkgName == "" {
		opts.PkgName = "native_app"
	}
	if opts.NativeModuleName == "" {
		opts.NativeModuleName = "native:__transpiled_app__"
	}

	// Union of every top-level function name across the user files. Fed
	// back into the picker so a call to fib() in app.ts resolves to
	// kernel.ts's extractable fib instead of tripping the
	// "callee is not a same-file function" rejection.
	globalFuncs := map[string]struct{}{}
	for _, sf := range files {
		if sf.Statements == nil {
			continue
		}
		for _, stmt := range sf.Statements.Nodes {
			if stmt.Kind != ast.KindFunctionDeclaration {
				continue
			}
			fd := stmt.AsFunctionDeclaration()
			if fd == nil || fd.Name() == nil {
				continue
			}
			globalFuncs[fd.Name().AsIdentifier().Text] = struct{}{}
		}
	}

	// Cross-file static-method registry. Pre-collected across every user
	// file before any per-file Pick runs, so a function in file B can
	// resolve `Class.method(...)` against a class declared later in file
	// A regardless of compose order.
	globalStaticMethods := map[string]map[string]bool{}
	for _, sf := range files {
		picker.PreCollectStaticMethods(sf, globalStaticMethods)
	}
	pickerOpts := picker.Options{TopLevelFuncs: globalFuncs, StaticMethods: globalStaticMethods}
	merged := &picker.Result{File: files[0].FileName()}
	var allNodes []*ast.Node
	var allFuncNames []string
	var allClassNames []string
	for _, sf := range files {
		pick := picker.Pick(sf, ck, pickerOpts)
		merged.Candidates = append(merged.Candidates, pick.Candidates...)
		allNodes = append(allNodes, pick.ExtractedNodes()...)
		allFuncNames = append(allFuncNames, pick.ExtractedFunctions()...)
		allClassNames = append(allClassNames, pick.ExtractedClasses()...)
	}

	res := &Result{PickerResult: *merged}
	if len(allNodes) == 0 {
		return res, nil
	}

	goSrc, err := gotranspiler.TranspileNodes(ck, allNodes, opts.PkgName, opts.Backend)
	if err != nil {
		return nil, fmt.Errorf("transpile nodes: %w", err)
	}
	res.GoSource = goSrc
	res.ExportedJSNames = allFuncNames
	res.ExportedJSClasses = allClassNames
	res.ShimJS = BuildShimWithClasses(opts.NativeModuleName, allFuncNames, allClassNames)
	return res, nil
}

// Compose runs the picker, transpiles approved candidates, and builds the shim
// for a single TypeScript source file.
func Compose(sf *ast.SourceFile, ck *checker.Checker, opts Options) (*Result, error) {
	if opts.PkgName == "" {
		opts.PkgName = "native_app"
	}
	if opts.NativeModuleName == "" {
		opts.NativeModuleName = "native:__transpiled_app__"
	}

	pick := picker.Pick(sf, ck, picker.Options{})
	res := &Result{PickerResult: pick}

	nodes := pick.ExtractedNodes()
	if len(nodes) == 0 {
		return res, nil
	}

	goSrc, err := gotranspiler.TranspileNodes(ck, nodes, opts.PkgName, opts.Backend)
	if err != nil {
		return nil, fmt.Errorf("transpile nodes: %w", err)
	}
	res.GoSource = goSrc

	// The JS-visible names match the set of extracted top-level TS functions.
	res.ExportedJSNames = pick.ExtractedFunctions()
	res.ExportedJSClasses = pick.ExtractedClasses()
	res.ShimJS = BuildShimWithClasses(opts.NativeModuleName, res.ExportedJSNames, res.ExportedJSClasses)

	return res, nil
}

// classFactoryJSName is the JS module-export key for a class's Go factory.
// The transpiler emits `func New<Class>(...)`; DiscoverExportedFuncs then
// runs it through GoNameToJS.
func classFactoryJSName(className string) string {
	if className == "" {
		return ""
	}
	return gotranspiler.GoNameToJS("New" + className)
}

// BuildShim returns the ES5 JS postlude that swaps module.exports entries for
// the named functions with the implementations returned by the native module.
// Classes are not installed by this overload — use BuildShimWithClasses when
// the source file contains extracted classes.
//
// The shim is idempotent via the __ramuneNativeInstalled guard. It is safe to
// embed repeatedly (second and subsequent evaluations are no-ops) and safe to
// prepend or append to an esbuild bundle.
//
// Only ES5 syntax is used so the shim runs under goja (pre-ESNext) without
// lowering and under QuickJS-NG without incident.
func BuildShim(moduleName string, jsNames []string) string {
	return BuildShimWithClasses(moduleName, jsNames, nil)
}

// BuildShimWithClasses is the class-aware variant. classNames are the TS class
// names as declared (PascalCase); each one is installed as `globalThis.<Name>`
// pointing at the Go factory registered under `mod.new<Name>`. Calling
// `new Counter(...)` or `Counter(...)` both return the native instance object.
// JS-side globals the shim writes through. Centralised so tests and
// future hooks can reach them by the same name as the runtime emit.
const (
	shimInstalledKey = "__ramuneNativeInstalled"
	shimExportsKey   = "__ramuneNativeExports"
)

func BuildShimWithClasses(moduleName string, jsNames []string, classNames []string) string {
	if len(jsNames) == 0 && len(classNames) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(280 + 180*(len(jsNames)+len(classNames)))
	b.WriteString("\n;(function(){\n")
	fmt.Fprintf(&b, "  if (globalThis.%s) return;\n", shimInstalledKey)
	fmt.Fprintf(&b, "  globalThis.%s = true;\n", shimInstalledKey)
	fmt.Fprintf(&b, "  var mod = require(%q);\n", moduleName)
	fmt.Fprintf(&b, "  globalThis.%s = mod;\n", shimExportsKey)
	b.WriteString("  var _me = (typeof module !== 'undefined' && module && module.exports) ? module.exports : null;\n")
	// esbuild's CommonJS output defines exports via Object.defineProperty with
	// only a getter, so a plain assignment throws "no setter for property" on
	// strict engines like QuickJS-NG. defineProperty with writable+configurable
	// replaces the getter with a writable data slot. Try/catch protects against
	// non-configurable exports from other bundlers.
	b.WriteString("  function _install(obj, key, value) {\n")
	b.WriteString("    try { Object.defineProperty(obj, key, {value: value, writable: true, configurable: true, enumerable: true}); }\n")
	b.WriteString("    catch (e) { try { obj[key] = value; } catch (e2) { /* getter-only, no setter - skip */ } }\n")
	b.WriteString("  }\n")
	for _, name := range jsNames {
		fmt.Fprintf(&b, "  if (mod.%s) {\n", name)
		fmt.Fprintf(&b, "    if (_me) _install(_me, %q, mod.%s);\n", name, name)
		fmt.Fprintf(&b, "    _install(globalThis, %q, mod.%s);\n", name, name)
		b.WriteString("  }\n")
	}
	for _, cname := range classNames {
		factory := classFactoryJSName(cname)
		// Wrap the factory so Counter !== mod.newCounter — prevents surprises
		// if user code later reassigns one without affecting the other.
		fmt.Fprintf(&b, "  if (mod.%s) {\n", factory)
		fmt.Fprintf(&b, "    var _ctor_%s = (function(factory){ return function(){ return factory.apply(null, arguments); }; })(mod.%s);\n", factory, factory)
		fmt.Fprintf(&b, "    if (_me) _install(_me, %q, _ctor_%s);\n", cname, factory)
		fmt.Fprintf(&b, "    _install(globalThis, %q, _ctor_%s);\n", cname, factory)
		b.WriteString("  }\n")
	}
	b.WriteString("})();\n")
	// Module-scope reassignments override the hoisted local bindings the
	// IIFE above can't reach. Per-name try/catch so a missing or
	// non-bundled symbol doesn't take the rest of the swap down with it.
	for _, name := range jsNames {
		fmt.Fprintf(&b, "try { if (globalThis.%s && globalThis.%s.%s) %s = globalThis.%s.%s; } catch (e) {}\n", shimExportsKey, shimExportsKey, name, name, shimExportsKey, name)
	}
	for _, cname := range classNames {
		fmt.Fprintf(&b, "try { if (globalThis.%s) %s = globalThis.%s; } catch (e) {}\n", cname, cname, cname)
	}
	return b.String()
}
