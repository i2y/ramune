//go:build goja

package ramune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"
	esbuild "github.com/evanw/esbuild/pkg/api"
)

// gojaLowerCache stores esbuild-lowered source keyed by the original source.
// Lowering is deterministic so the cache is shared across Runtimes (safe under
// the pool use-case where multiple goja Runtimes may encounter the same user
// source). When the cache reaches gojaLowerCap entries the whole map is reset
// - lowering is cheap to recompute and this keeps the eviction logic simple.
var (
	gojaLowerMu    sync.RWMutex
	gojaLowerCache = make(map[string]string)
)

const gojaLowerCap = 1024

// isGojaParseError reports whether err came from goja's parser (vs a runtime
// exception). Only parse errors trigger the esbuild-lowering retry; runtime
// errors are returned as-is.
func isGojaParseError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SyntaxError") ||
		(strings.Contains(msg, "Line ") && strings.Contains(msg, "col ")) ||
		strings.Contains(msg, "Unexpected")
}

// lowerForGoja runs esbuild Transform at ES2017 target so modern JS source
// (private class fields, top-level await, Object.hasOwn, etc.) becomes parseable
// by goja. Result is cached by source string - same source reused inside a hot
// loop only pays the esbuild cost once.
//
// Kept on esbuild rather than the tsgo-backed tsgotranspile path because tsc
// does not lower top-level await in CommonJS emit (it raises TS1378 and emits
// the await verbatim). Since this function exists specifically to rescue code
// that goja's parser rejects - TLA included - esbuild's IIFE wrap is the only
// semantics that works here.
func lowerForGoja(src string) (string, error) {
	gojaLowerMu.RLock()
	if cached, ok := gojaLowerCache[src]; ok {
		gojaLowerMu.RUnlock()
		return cached, nil
	}
	gojaLowerMu.RUnlock()

	result := esbuild.Transform(src, esbuild.TransformOptions{
		Target:        esbuild.ES2017,
		Loader:        esbuild.LoaderJS,
		LegalComments: esbuild.LegalCommentsNone,
		Sourcemap:     esbuild.SourceMapNone,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("esbuild lower: %s", result.Errors[0].Text)
	}
	lowered := string(result.Code)

	gojaLowerMu.Lock()
	if len(gojaLowerCache) >= gojaLowerCap {
		gojaLowerCache = make(map[string]string, gojaLowerCap)
	}
	gojaLowerCache[src] = lowered
	gojaLowerMu.Unlock()

	return lowered, nil
}

// Engine returns the name of the JS engine backend.
func (r *Runtime) Engine() string { return "goja" }

// Runtime holds a goja VM and global JS context.
// Multiple Runtimes can coexist in the same process -- each has its own
// dedicated goroutine. All goja operations are dispatched to this goroutine
// via a channel (goja is not goroutine-safe).
type Runtime struct {
	vm *goja.Runtime

	// Dedicated engine goroutine dispatch.
	callCh  chan func()
	stopCh  chan struct{}
	doneCh  chan struct{}
	wakeCh  chan struct{}
	gojaGID atomic.Int64

	goFuncs            []GoFunc
	nativeMethodSeq    int
	nativeReg          *nativeTypeRegistry
	fsMgr              *fsManager
	fswatchMgr         *fsWatchManager
	vmMgr              *vmManager
	procMgr            *processManager
	sockMgr            *socketManager
	tcpSrvMgr          *tcpServerManager
	udpMgr             *udpManager
	webviewMgr         *webviewManager
	workerMgr          *workerManager
	http2Mgr           *http2Manager
	waitAsyncCount     atomic.Int32
	nativePromiseCount atomic.Int32 // pending Go *promise.Promise[T] -> JS Promise bridges
	sqliteMgr          *sqliteManager
	streamMgr          *streamManager
	fetchMgr           *fetchManager
	bunSrv             *bunServerState
	bunHandleFastFn    goja.Callable         // cached __bunHandleFast (fast HTTP dispatch)
	bunAsyncSetupFn    goja.Callable         // cached __bunAsyncSetup (async path promise wiring)
	bunTickFn          goja.Callable         // cached __eventLoop.tick
	bunNextDelayFn     goja.Callable         // cached __eventLoop.nextDelay
	bunMethodValCache  map[string]goja.Value // pre-baked goja.Value for common HTTP verbs
	bunCallArgs        []goja.Value          // reusable 4-slot args buffer for bunHandleFastFn
	customTickMgrs     []TickManager
	gcConfig           GCConfig
	perms              *Permissions
	stdout             io.Writer
	stderr             io.Writer
	poolHandleFn       uintptr // unused in goja but kept to preserve cross-backend Runtime shape

	// OnReady hook: fn is invoked once when the event loop first
	// observes no pending work during a RunEventLoop[For] call.
	onReadyMu   sync.Mutex
	onReadyFn   func()
	onReadyDone bool

	closeOnce sync.Once
	closed    atomic.Bool
}

// New creates a new goja runtime.
func New(opts ...Option) (*Runtime, error) {
	return newRuntime(opts)
}

// newInternal creates a Runtime without any restrictions.
func newInternal(opts ...Option) (*Runtime, error) {
	return newRuntime(opts)
}

func newRuntime(opts []Option) (*Runtime, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	r := &Runtime{
		callCh: make(chan func(), 64),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		wakeCh: make(chan struct{}, 1),
	}

	if cfg.gc != nil {
		r.gcConfig = *cfg.gc
	} else {
		r.gcConfig = DefaultGCConfig()
	}
	if cfg.permissions != nil {
		r.perms = cfg.permissions
	}
	r.customTickMgrs = cfg.tickManagers
	r.stdout = cfg.stdout
	if r.stdout == nil {
		r.stdout = os.Stdout
	}
	r.stderr = cfg.stderr
	if r.stderr == nil {
		r.stderr = os.Stderr
	}

	ready := make(chan error, 1)
	go r.gojaLoop(ready, cfg)

	if err := <-ready; err != nil {
		return nil, err
	}

	return r, nil
}

// gojaLoop is the dedicated goroutine for goja operations.
func (r *Runtime) gojaLoop(ready chan<- error, cfg *config) {
	runtime.LockOSThread()
	defer close(r.doneCh)

	r.gojaGID.Store(goid())

	vm := goja.New()
	// Use FieldNameMapper so Go struct fields are camelCased in JS, matching
	// the JSC/QuickJS backends' structToJSObject behavior closely.
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	r.vm = vm

	// Install event loop.
	if err := r.installEventLoop(); err != nil {
		ready <- fmt.Errorf("ramune: event loop: %w", err)
		return
	}

	// Install console (always).
	if err := r.installConsole(); err != nil {
		ready <- fmt.Errorf("ramune: console: %w", err)
		return
	}

	// NodeCompat, WinterTC, fetch, bundles etc mirror the QuickJS path.
	// These all go through RegisterFunc / execLocked, which are backend-neutral.
	if cfg.nodeCompat {
		if err := r.installNodeCompat(); err != nil {
			ready <- fmt.Errorf("ramune: nodecompat: %w", err)
			return
		}
		if err := r.installAsyncSpawn(); err != nil {
			ready <- fmt.Errorf("ramune: async spawn: %w", err)
			return
		}
		if err := r.installAsyncFS(); err != nil {
			ready <- fmt.Errorf("ramune: async fs: %w", err)
			return
		}
		if err := r.installFSWatch(); err != nil {
			ready <- fmt.Errorf("ramune: fs.watch: %w", err)
			return
		}
		if err := r.installVM(); err != nil {
			ready <- fmt.Errorf("ramune: vm: %w", err)
			return
		}
		if err := r.installAsyncNet(); err != nil {
			ready <- fmt.Errorf("ramune: async net: %w", err)
			return
		}
		if err := r.installTCPServer(); err != nil {
			ready <- fmt.Errorf("ramune: tcp server: %w", err)
			return
		}
		if err := r.installDgram(); err != nil {
			ready <- fmt.Errorf("ramune: dgram: %w", err)
			return
		}
		if err := r.installWorkerThreads(); err != nil {
			ready <- fmt.Errorf("ramune: worker_threads: %w", err)
			return
		}
		if err := r.installHTTP2(); err != nil {
			ready <- fmt.Errorf("ramune: http2: %w", err)
			return
		}
		if err := r.installSharedArrayBuffer(); err != nil {
			ready <- fmt.Errorf("ramune: SharedArrayBuffer: %w", err)
			return
		}
		if err := r.installWebStreams(); err != nil {
			ready <- fmt.Errorf("ramune: web streams: %w", err)
			return
		}
		if err := r.installStreamBridge(); err != nil {
			ready <- fmt.Errorf("ramune: stream bridge: %w", err)
			return
		}
		if err := r.installWebCrypto(); err != nil {
			ready <- fmt.Errorf("ramune: web crypto: %w", err)
			return
		}
		if err := r.installBunCompat(); err != nil {
			ready <- fmt.Errorf("ramune: bun compat: %w", err)
			return
		}
		if err := r.installCSRF(); err != nil {
			ready <- fmt.Errorf("ramune: csrf: %w", err)
			return
		}
		if err := r.installArchive(); err != nil {
			ready <- fmt.Errorf("ramune: archive: %w", err)
			return
		}
		if err := r.installCron(); err != nil {
			ready <- fmt.Errorf("ramune: cron: %w", err)
			return
		}
		if err := r.installMarkdown(); err != nil {
			ready <- fmt.Errorf("ramune: markdown: %w", err)
			return
		}
		if err := r.installWebView(); err != nil {
			ready <- fmt.Errorf("ramune: webview: %w", err)
			return
		}
		if err := r.installCDP(); err != nil {
			ready <- fmt.Errorf("ramune: cdp: %w", err)
			return
		}
		if err := r.installSQLite(); err != nil {
			ready <- fmt.Errorf("ramune: sqlite: %w", err)
			return
		}
		if err := r.installWinterTC(); err != nil {
			ready <- fmt.Errorf("ramune: WinterTC: %w", err)
			return
		}
	}

	if cfg.winterTC && !cfg.nodeCompat {
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				ready <- fmt.Errorf("ramune: web streams: %w", err)
				return
			}
		}
		if err := r.installWinterTC(); err != nil {
			ready <- fmt.Errorf("ramune: WinterTC: %w", err)
			return
		}
	}

	if cfg.withFetch || cfg.nodeCompat {
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				ready <- fmt.Errorf("ramune: web streams: %w", err)
				return
			}
			if err := r.installStreamBridge(); err != nil {
				ready <- fmt.Errorf("ramune: stream bridge: %w", err)
				return
			}
		}
		if err := r.installFetch(); err != nil {
			ready <- fmt.Errorf("ramune: fetch: %w", err)
			return
		}
	}

	if cfg.preloadJS != "" {
		if err := r.execLocked(cfg.preloadJS); err != nil {
			ready <- fmt.Errorf("ramune: failed to execute preload JS: %w", err)
			return
		}
	}

	if len(cfg.dependencies) > 0 {
		bundle, nodeModulesDir, err := ensureBundle(cfg.dependencies, cfg.nodeCompat)
		if err != nil {
			ready <- err
			return
		}
		if nodeModulesDir != "" {
			r.execLocked(fmt.Sprintf("globalThis.__nodeModulesDir = %q;", nodeModulesDir))
			r.execLocked(fmt.Sprintf("if (globalThis.process && globalThis.process.env) { globalThis.process.env.PATH = %q + ':' + (globalThis.process.env.PATH || ''); }", nodeModulesDir+"/.bin"))
		}
		if err := r.execLocked(bundle); err != nil {
			ready <- fmt.Errorf("ramune: failed to load bundle: %w", err)
			return
		}
	}

	for _, m := range cfg.modules {
		if err := r.loadModuleLocked(m); err != nil {
			ready <- fmt.Errorf("ramune: module %s: %w", m.Name, err)
			return
		}
	}

	ready <- nil

	for {
		select {
		case fn := <-r.callCh:
			fn()
		case <-r.stopCh:
			return
		}
	}
}

// dispatch executes a function on the dedicated goja goroutine.
func (r *Runtime) dispatch(fn func()) {
	if r.closed.Load() {
		return
	}
	if goid() == r.gojaGID.Load() {
		fn()
		return
	}
	done := make(chan struct{})
	r.callCh <- func() {
		fn()
		close(done)
	}
	<-done
}

// Close releases all resources.
func (r *Runtime) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.fetchMgr != nil {
			r.fetchMgr.closeAll()
		}
		if r.streamMgr != nil {
			r.streamMgr.closeAll()
		}
		if r.fswatchMgr != nil {
			r.fswatchMgr.closeAll()
		}
		if r.tcpSrvMgr != nil {
			r.tcpSrvMgr.closeAll()
		}
		if r.udpMgr != nil {
			r.udpMgr.closeAll()
		}
		if r.http2Mgr != nil {
			r.http2Mgr.closeAll()
		}
		if r.webviewMgr != nil {
			r.webviewMgr.closeAll()
		}
		if r.sqliteMgr != nil {
			r.sqliteMgr.closeAll()
		}
		for _, m := range r.customTickMgrs {
			m.Close()
		}
		if r.nativeReg != nil {
			r.nativeReg.clearInstances()
		}

		close(r.stopCh)
		<-r.doneCh
		// goja VM is GC'd -- no explicit close.
	})
	return nil
}

// Eval evaluates JavaScript code and returns the result.
func (r *Runtime) Eval(code string) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	r.dispatch(func() {
		val, err = r.evalLocked(code)
	})
	return val, err
}

// EvalWithContext evaluates JavaScript code with context support.
func (r *Runtime) EvalWithContext(ctx context.Context, code string) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	done := make(chan struct{})
	r.callCh <- func() {
		val, err = r.evalLocked(code)
		close(done)
	}
	select {
	case <-done:
		return val, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Exec executes JavaScript code, discarding the result.
func (r *Runtime) Exec(code string) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}
	var err error
	r.dispatch(func() {
		err = r.execLocked(code)
	})
	return err
}

// safeRunString wraps goja's RunString with panic recovery plus transparent
// esbuild ES2017 lowering on parse failure. goja accepts ES5.1 + a subset of
// ES6+; for source that uses private class fields, top-level await, or other
// ES2022/2023 syntax we retry once with esbuild-lowered code. Lowered result
// is cached (see gojaLowerCache) so the esbuild cost is amortized for
// repeated source. Runtime errors (TypeError, ReferenceError) are returned
// without retry.
func (r *Runtime) safeRunString(code string) (result goja.Value, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("goja runtime panic: %v", rec)
			result = nil
		}
	}()
	result, err = r.vm.RunString(code)
	if err != nil && isGojaParseError(err) {
		lowered, lerr := lowerForGoja(code)
		if lerr != nil {
			return nil, err
		}
		// esbuild produced identical output (source was already ES2017-compatible),
		// so a retry would hit the same parse error; return the original instead.
		if lowered == code {
			return nil, err
		}
		result, err = r.vm.RunString(lowered)
	}
	return result, err
}

// safeCallable wraps a goja.Callable invocation with the same recovery path
// as safeRunString, for compiled-function dispatch sites (e.g. cached
// __bunHandleFast, __eventLoop.tick).
func (r *Runtime) safeCallable(fn goja.Callable, this goja.Value, args ...goja.Value) (result goja.Value, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("goja runtime panic: %v", rec)
			result = nil
		}
	}()
	return fn(this, args...)
}

// evalLocked evaluates JS on the engine goroutine.
func (r *Runtime) evalLocked(code string) (*Value, error) {
	result, err := r.safeRunString(code)
	if err != nil {
		return nil, &JSError{Message: err.Error()}
	}
	return r.wrapValue(result), nil
}

// evalScriptLocked evaluates JS and returns the raw goja.Value.
func (r *Runtime) evalScriptLocked(code, context string) (goja.Value, error) {
	result, err := r.safeRunString(code)
	if err != nil {
		return nil, &JSError{Context: context, Message: err.Error()}
	}
	return result, nil
}

// execLocked evaluates JS, discards the result.
func (r *Runtime) execLocked(code string) error {
	_, err := r.safeRunString(code)
	if err != nil {
		return &JSError{Message: err.Error()}
	}
	return nil
}

// GlobalObject returns the global JS object.
func (r *Runtime) GlobalObject() *Value {
	var val *Value
	r.dispatch(func() {
		g := r.vm.GlobalObject()
		val = r.wrapValue(g)
	})
	return val
}

// NewObject creates a new JavaScript object with optional properties.
func (r *Runtime) NewObject(props map[string]any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	r.dispatch(func() {
		val, err = r.newObjectLocked(props)
	})
	return val, err
}

func (r *Runtime) newObjectLocked(props map[string]any) (*Value, error) {
	b, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("marshal props: %w", err)
	}
	result, err := r.safeRunString("(" + string(b) + ")")
	if err != nil {
		return nil, err
	}
	return r.wrapValue(result), nil
}

// NewArray creates a new JavaScript array.
func (r *Runtime) NewArray(items ...any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	r.dispatch(func() {
		b, e := json.Marshal(items)
		if e != nil {
			err = e
			return
		}
		result, e := r.safeRunString(string(b))
		if e != nil {
			err = e
			return
		}
		val = r.wrapValue(result)
	})
	return val, err
}

// NewUint8Array creates a new Uint8Array from a Go byte slice.
func (r *Runtime) NewUint8Array(data []byte) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	r.dispatch(func() {
		code := "new Uint8Array(["
		for i, b := range data {
			if i > 0 {
				code += ","
			}
			code += itoa(int(b))
		}
		code += "])"
		result, e := r.safeRunString(code)
		if e != nil {
			err = e
			return
		}
		val = r.wrapValue(result)
	})
	return val, err
}

// drainUnprotectQueue is a no-op for goja (GC-managed, no protect lifecycle).
func (r *Runtime) drainUnprotectQueue() {}
