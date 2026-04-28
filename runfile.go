package ramune

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	esbuild "github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/tsgotranspile"
)

// RunFileOptions tunes RunFile. The zero value is fine.
type RunFileOptions struct {
	// Argv supplies the script arguments visible as process.argv inside the
	// script. RunFile follows Node.js convention: process.argv is always set
	// to [runner, scriptPath, ...Argv], so callers should pass only the
	// script-side args. If Argv[0] is exactly "ramune", RunFile treats Argv
	// as a fully-formed process.argv and uses it verbatim instead.
	//
	// Examples (script path "/abs/foo.ts"):
	//   Argv: nil                    -> process.argv = ["ramune", "/abs/foo.ts"]
	//   Argv: []string{"--flag"}     -> process.argv = ["ramune", "/abs/foo.ts", "--flag"]
	//   Argv: []string{"ramune","X"} -> process.argv = ["ramune", "X"]   (verbatim)
	Argv []string
}

// RunFile executes a JS or TypeScript file in rt with the same setup the
// `ramune run` CLI applies: __filename / __dirname / process.argv globals,
// tsgo TS transpile (with experimental decorators), esbuild ESM bundling
// when the source has import/export, IIFE wrap.
//
// path must be a real on-disk file (relative or absolute); RunFile reads
// it directly so esbuild's bundler can resolve sibling imports the same
// way the CLI does.
//
// rt is expected to have been built with NodeCompat() — RunFile relies on
// the Node-compat layer for require()/process/__filename/__dirname. Without
// it, process.argv is silently skipped and require() in the loaded source
// will fail. WithFetch() is similarly recommended if the script does network
// I/O.
//
// RunFile drives the event loop to completion before returning, so any
// timeout from rt.RunEventLoop (30s default for non-server scripts) is
// surfaced to the caller as the return value.
//
// Use this when embedding Ramune as a library and you need to run a TS
// entrypoint that pulls in its own dependency graph (e.g. pyright-internal
// reached via vendored `./src/...` imports). For a JS string with no
// imports, Runtime.Exec is enough.
func RunFile(rt *Runtime, path string, opts RunFileOptions) error {
	abs := path
	if !filepath.IsAbs(abs) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("ramune: RunFile: %w", err)
		}
		abs = filepath.Join(cwd, path)
	}
	code, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("ramune: RunFile: %w", err)
	}

	if err := setRunGlobals(rt, abs, opts.Argv); err != nil {
		return err
	}

	if isTypeScriptFile(abs) {
		code, err = transformTypeScriptForRun(abs, code)
		if err != nil {
			return err
		}
	}
	if isESMSource(abs, string(code)) {
		code, err = bundleESMForRun(abs, code)
		if err != nil {
			return err
		}
	} else {
		// Already CJS-shaped (post-tsgo or hand-written) — wrap in an IIFE
		// so top-level vars don't leak to the global scope.
		code = []byte("(function(){\n" + string(code) + "\n})();\n")
	}

	if err := rt.Exec(string(code)); err != nil {
		return err
	}
	return rt.RunEventLoop()
}

// setRunGlobals installs __filename / __dirname / process.argv on rt
// matching the `ramune run` CLI's preamble.
func setRunGlobals(rt *Runtime, abs string, argv []string) error {
	dir := filepath.Dir(abs)
	if err := rt.Exec(fmt.Sprintf(`globalThis.__filename = %q; globalThis.__dirname = %q;`, abs, dir)); err != nil {
		return err
	}
	if argv == nil {
		argv = []string{"ramune", abs}
	} else if len(argv) == 0 || argv[0] != "ramune" {
		argv = append([]string{"ramune", abs}, argv...)
	}
	jsArgv, err := json.Marshal(argv)
	if err != nil {
		return fmt.Errorf("ramune: RunFile: marshal argv: %w", err)
	}
	return rt.Exec(fmt.Sprintf(`if(typeof process!=='undefined')process.argv=%s;`, string(jsArgv)))
}

func isTypeScriptFile(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".ts" || ext == ".tsx"
}

func transformTypeScriptForRun(filename string, code []byte) ([]byte, error) {
	r, err := tsgotranspile.Transpile(string(code), tsgotranspile.Options{
		FileName:               filepath.Base(filename),
		Target:                 tsgoTarget(),
		Module:                 core.ModuleKindCommonJS,
		ExperimentalDecorators: true,
	})
	if err != nil {
		return nil, fmt.Errorf("ramune: TypeScript %s: %w", filepath.Base(filename), err)
	}
	if e := tsgotranspile.FirstError(r.Diagnostics); e != nil {
		return nil, fmt.Errorf("ramune: TypeScript %s: %w", filepath.Base(filename), e)
	}
	return []byte(r.JS), nil
}

func bundleESMForRun(filename string, code []byte) ([]byte, error) {
	external := append([]string{}, NodeBuiltins...)
	external = append(external, "bun:sqlite", "native:*")

	result := esbuild.Build(esbuild.BuildOptions{
		EntryPoints: []string{filename},
		Bundle:      true,
		Format:      esbuild.FormatCommonJS,
		Platform:    esbuild.PlatformNode,
		Write:       false,
		External:    external,
		LogLevel:    esbuild.LogLevelSilent,
		MainFields:  []string{"module", "main"},
		Conditions:  []string{"import", "node", "default"},
	})
	if len(result.Errors) > 0 || len(result.OutputFiles) == 0 {
		return nil, fmt.Errorf("ramune: bundleESM %s: %s", filename, esbuildErrorSummary(result.Errors))
	}
	out := string(result.OutputFiles[0].Contents)
	out = strings.ReplaceAll(out, "import(", "__dynamicImport(")
	if strings.Contains(string(code), "await ") {
		out = "(async function() {\n" + out + "\n})();\n"
	} else {
		out = "(function() {\n" + out + "\n})();\n"
	}
	return []byte(out), nil
}

func esbuildErrorSummary(errs []esbuild.Message) string {
	if len(errs) == 0 {
		return "(no diagnostics)"
	}
	parts := make([]string, 0, len(errs))
	for _, m := range errs {
		parts = append(parts, m.Text)
	}
	return strings.Join(parts, "; ")
}
