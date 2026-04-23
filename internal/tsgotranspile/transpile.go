// Package tsgotranspile wraps tsgo's compiler.Program.Emit for in-memory
// TypeScript -> JavaScript transpilation. It is Ramune's canonical TS->JS
// path (the role esbuild.Transform used to fill for CLI-level type stripping
// and down-level lowering). Bundling stays on esbuild.
package tsgotranspile

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/bundled"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/diagnostics"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

// Options controls a single Transpile call. Every caller in Ramune picks
// Target / Module deliberately per backend, so there is no default.
type Options struct {
	FileName               string            // defaults to "input.ts"
	Target                 core.ScriptTarget // ScriptTargetESNext / ES2017 / ...
	Module                 core.ModuleKind   // usually ModuleKindCommonJS
	JSX                    core.JsxEmit      // JsxEmitPreserve / JsxEmitReact / ...
	RemoveComments         bool
	InlineSourceMap        bool
	ExperimentalDecorators bool
	EmitDecoratorMetadata  bool
}

// Result is the output of a Transpile call. Diagnostics covers parse
// errors and emitter diagnostics; semantic (type-check) diagnostics are
// intentionally omitted - use `ramune check` for that.
type Result struct {
	JS          string
	Diagnostics []*ast.Diagnostic
}

// Transpile is a convenience wrapper that calls the package-level default
// Transpiler. For repeated calls (REPL, nodecompat require) construct a
// dedicated *Transpiler via New() - it amortizes the parse cost of
// bundled lib.d.ts across calls.
func Transpile(source string, opts Options) (Result, error) {
	return defaultTranspiler().Transpile(source, opts)
}

// Transpiler is a reusable tsgo-backed TS->JS transpiler. It caches the
// parsed lib.d.ts SourceFile ASTs across calls so repeated Transpile
// invocations (REPL, nodecompat loader) skip the dominant cost of
// re-parsing the bundled TypeScript lib set each time.
//
// A Transpiler is safe for concurrent use - calls serialize internally
// because binding mutates AST node state on the shared lib SourceFiles.
// For workloads that want true parallelism, create multiple Transpilers.
type Transpiler struct {
	baseFS  vfs.FS
	libPath string

	mu   sync.Mutex
	libs map[string]*ast.SourceFile // absolute path -> parsed lib AST
}

// New returns a Transpiler with a fresh lib.d.ts cache. The first call to
// Transpile populates the cache (paying the one-time parse cost);
// subsequent calls reuse it.
func New() *Transpiler {
	return &Transpiler{
		baseFS:  bundled.WrapFS(osvfs.FS()),
		libPath: bundled.LibPath(),
		libs:    make(map[string]*ast.SourceFile),
	}
}

// Transpile runs tsgo's full emit pipeline (TypeEraser, ImportElision,
// RuntimeSyntax, Decorators, JSX, ESTransform, ModuleTransform, printer)
// over source. The source is overlaid onto a synthetic absolute path so
// bundled lib.d.ts lookups continue to resolve through the shared host.
func (t *Transpiler) Transpile(source string, opts Options) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if opts.FileName == "" {
		opts.FileName = "input.ts"
	}

	synthDir := syntheticDir()
	absPath := tspath.NormalizeSlashes(filepath.Join(synthDir, opts.FileName))
	// OutDir differs from the input directory so JS-in/JS-out Transpile
	// calls (the AllowJs passthrough, the .mjs->.js rename in nodecompat)
	// don't get silently skipped by the same-path overwrite guard.
	outDir := tspath.NormalizeSlashes(filepath.Join(synthDir, ".out"))

	overlay := &singleFileFS{
		base: t.baseFS,
		path: absPath,
		dir:  synthDir,
		src:  source,
	}

	inner := compiler.NewCachedFSCompilerHost(synthDir, overlay, t.libPath, nil, nil)
	host := &libCachingHost{inner: inner, cache: t.libs, libPath: t.libPath}

	co := &core.CompilerOptions{
		Target:       opts.Target,
		Module:       opts.Module,
		Jsx:          opts.JSX,
		OutDir:       outDir,
		SkipLibCheck: core.TSTrue,
		AllowJs:      core.TSTrue,
	}
	if opts.RemoveComments {
		co.RemoveComments = core.TSTrue
	}
	if opts.InlineSourceMap {
		co.InlineSourceMap = core.TSTrue
		co.InlineSources = core.TSTrue
	}
	if opts.ExperimentalDecorators {
		co.ExperimentalDecorators = core.TSTrue
	}
	if opts.EmitDecoratorMetadata {
		co.EmitDecoratorMetadata = core.TSTrue
	}

	cfg := tsoptions.NewParsedCommandLine(
		co,
		[]string{absPath},
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: overlay.UseCaseSensitiveFileNames(),
			CurrentDirectory:          synthDir,
		},
	)

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         cfg,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	var target *ast.SourceFile
	for _, f := range program.SourceFiles() {
		if f.FileName() == absPath {
			target = f
			break
		}
	}
	if target == nil {
		return Result{}, fmt.Errorf("tsgotranspile: source file not found in program: %s", absPath)
	}

	ctx := context.Background()

	var wmu sync.Mutex
	outputs := map[string]string{}
	writeFile := func(name string, text string, _ *compiler.WriteFileData) error {
		wmu.Lock()
		outputs[name] = text
		wmu.Unlock()
		return nil
	}

	emitResult := program.Emit(ctx, compiler.EmitOptions{
		TargetSourceFile: target,
		EmitOnly:         compiler.EmitOnlyJs,
		WriteFile:        writeFile,
	})

	diags := append([]*ast.Diagnostic(nil), program.GetSyntacticDiagnostics(ctx, target)...)
	if emitResult != nil {
		diags = append(diags, emitResult.Diagnostics...)
	}

	var js string
	for name, text := range outputs {
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs" {
			js = text
		}
	}

	return Result{JS: js, Diagnostics: diags}, nil
}

// defaultTranspiler is the singleton used by the package-level Transpile
// function. Lazy construction keeps startup cost off the critical path
// for programs that never transpile (e.g., a Go binary that embeds
// Ramune but never runs TS).
var defaultTranspiler = sync.OnceValue(New)

// libCachingHost wraps a tsgo CompilerHost and returns cached SourceFile
// ASTs for files under libPath. Non-lib files (notably the overlaid
// input) are always parsed fresh. Safe for sequential use only - the
// outer Transpiler.mu guarantees that.
type libCachingHost struct {
	inner   compiler.CompilerHost
	cache   map[string]*ast.SourceFile
	libPath string
}

func (h *libCachingHost) FS() vfs.FS                  { return h.inner.FS() }
func (h *libCachingHost) DefaultLibraryPath() string  { return h.inner.DefaultLibraryPath() }
func (h *libCachingHost) GetCurrentDirectory() string { return h.inner.GetCurrentDirectory() }
func (h *libCachingHost) Trace(msg *diagnostics.Message, args ...any) {
	h.inner.Trace(msg, args...)
}

func (h *libCachingHost) GetSourceFile(opts compilerSourceFileOpts) *ast.SourceFile {
	if strings.HasPrefix(opts.FileName, h.libPath) {
		if sf, ok := h.cache[opts.FileName]; ok {
			return sf
		}
		sf := h.inner.GetSourceFile(opts)
		if sf != nil {
			h.cache[opts.FileName] = sf
		}
		return sf
	}
	return h.inner.GetSourceFile(opts)
}

func (h *libCachingHost) GetResolvedProjectReference(fileName string, path tspath.Path) *tsoptions.ParsedCommandLine {
	return h.inner.GetResolvedProjectReference(fileName, path)
}

// compilerSourceFileOpts mirrors ast.SourceFileParseOptions; aliased here
// so the public GetSourceFile signature matches CompilerHost exactly.
type compilerSourceFileOpts = ast.SourceFileParseOptions

// FirstError returns a formatted error matching the first CategoryError
// diagnostic in diags, or nil when none are at error severity. Callers
// that only care about fail/pass can compare the return to nil.
func FirstError(diags []*ast.Diagnostic) error {
	for _, d := range diags {
		if d != nil && d.Category() == diagnostics.CategoryError {
			return fmt.Errorf("TS%d: %s", d.Code(), d.String())
		}
	}
	return nil
}

// syntheticDir returns the absolute directory used to anchor in-memory
// inputs. tsgo only uses it as a virtual path; nothing is read from or
// written to disk through it.
var syntheticDir = sync.OnceValue(func() string {
	return tspath.NormalizeSlashes(filepath.Join(os.TempDir(), "ramune-tsgotranspile"))
})

// singleFileFS overlays one in-memory source over a base FS. All reads
// for the overlaid path come from memory; everything else (notably
// bundled lib.d.ts) falls through to base.
type singleFileFS struct {
	base vfs.FS
	path string
	dir  string
	src  string
}

func (f *singleFileFS) UseCaseSensitiveFileNames() bool { return f.base.UseCaseSensitiveFileNames() }

func (f *singleFileFS) FileExists(p string) bool {
	if f.matchFile(p) {
		return true
	}
	return f.base.FileExists(p)
}

func (f *singleFileFS) ReadFile(p string) (string, bool) {
	if f.matchFile(p) {
		return f.src, true
	}
	return f.base.ReadFile(p)
}

func (f *singleFileFS) WriteFile(p string, data string) error {
	return f.base.WriteFile(p, data)
}

func (f *singleFileFS) Remove(p string) error { return f.base.Remove(p) }

func (f *singleFileFS) Chtimes(p string, a, m time.Time) error {
	return f.base.Chtimes(p, a, m)
}

func (f *singleFileFS) DirectoryExists(p string) bool {
	if tspath.NormalizeSlashes(p) == f.dir {
		return true
	}
	return f.base.DirectoryExists(p)
}

func (f *singleFileFS) GetAccessibleEntries(p string) vfs.Entries {
	return f.base.GetAccessibleEntries(p)
}

func (f *singleFileFS) Stat(p string) vfs.FileInfo {
	if f.matchFile(p) {
		return overlayFileInfo{name: filepath.Base(f.path), size: int64(len(f.src))}
	}
	return f.base.Stat(p)
}

func (f *singleFileFS) WalkDir(root string, walkFn vfs.WalkDirFunc) error {
	return f.base.WalkDir(root, walkFn)
}

func (f *singleFileFS) Realpath(p string) string {
	if f.matchFile(p) {
		return f.path
	}
	return f.base.Realpath(p)
}

func (f *singleFileFS) matchFile(p string) bool {
	return tspath.NormalizeSlashes(p) == f.path
}

type overlayFileInfo struct {
	name string
	size int64
}

func (o overlayFileInfo) Name() string       { return o.name }
func (o overlayFileInfo) Size() int64        { return o.size }
func (o overlayFileInfo) Mode() fs.FileMode  { return 0o644 }
func (o overlayFileInfo) ModTime() time.Time { return time.Time{} }
func (o overlayFileInfo) IsDir() bool        { return false }
func (o overlayFileInfo) Sys() any           { return nil }
