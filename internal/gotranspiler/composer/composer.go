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
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/gotranspiler"
	"github.com/i2y/ramune/internal/gotranspiler/picker"
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// Options controls composer behavior.
type Options struct {
	// PkgName is the Go package name for the emitted native module.
	// Defaults to "native_app".
	PkgName string
	// NativeModuleName is the JS require() specifier registered with
	// NativeModuleFromFuncs. Defaults to "native:__transpiled_app__".
	NativeModuleName string
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
	// ExportedJSNames are the JS-visible names (camelCase) that will be swapped
	// at boot. Same set as ShimJS references, in source order.
	ExportedJSNames []string
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

	goSrc, err := gotranspiler.TranspileNodes(ck, nodes, opts.PkgName)
	if err != nil {
		return nil, fmt.Errorf("transpile nodes: %w", err)
	}
	res.GoSource = goSrc

	// The JS-visible names match the set of extracted top-level TS functions.
	res.ExportedJSNames = pick.ExtractedFunctions()
	res.ShimJS = BuildShim(opts.NativeModuleName, res.ExportedJSNames)

	return res, nil
}

// BuildShim returns the ES5 JS postlude that swaps module.exports entries for
// the named functions with the implementations returned by the native module.
//
// The shim is idempotent via the __ramuneNativeInstalled guard. It is safe to
// embed repeatedly (second and subsequent evaluations are no-ops) and safe to
// prepend or append to an esbuild bundle.
//
// Only ES5 syntax is used so the shim runs under goja (pre-ESNext) without
// lowering and under QuickJS-NG without incident.
func BuildShim(moduleName string, jsNames []string) string {
	if len(jsNames) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n;(function(){\n")
	b.WriteString("  if (globalThis.__ramuneNativeInstalled) return;\n")
	b.WriteString("  globalThis.__ramuneNativeInstalled = true;\n")
	fmt.Fprintf(&b, "  var mod = require(%q);\n", moduleName)
	b.WriteString("  globalThis.__ramuneNativeExports = mod;\n")
	b.WriteString("  var _me = (typeof module !== 'undefined' && module && module.exports) ? module.exports : null;\n")
	for _, name := range jsNames {
		fmt.Fprintf(&b, "  if (mod.%s) {\n", name)
		fmt.Fprintf(&b, "    if (_me) _me.%s = mod.%s;\n", name, name)
		fmt.Fprintf(&b, "    globalThis.%s = mod.%s;\n", name, name)
		b.WriteString("  }\n")
	}
	b.WriteString("})();\n")
	return b.String()
}
