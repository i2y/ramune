//go:build qjswasm && !goja

package ramune

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/i2y/ramune/third_party/qjs"
)

// Engine returns the name of the JS engine backend.
func (r *Runtime) Engine() string { return "qjswasm" }

// Runtime wraps a fastschema/qjs runtime. fastschema's runtime is itself
// QuickJS-NG compiled to WebAssembly, driven by wazero's compiler-mode
// JIT — we delegate all the hot-path work to it and keep a thin Go
// wrapper that matches Ramune's cross-backend API surface (same shape
// as engine_jsc.go / engine_goja.go).
type Runtime struct {
	qjsRT  *qjs.Runtime
	qjsCtx *qjs.Context

	// Dedicated engine goroutine dispatch. fastschema/qjs's wazero
	// module isn't goroutine-safe, so we pin every JS call to one OS
	// thread. This mirrors the other backends' concurrency model and
	// lets re-entrant callbacks (detected via goid) run inline.
	callCh chan func()
	stopCh chan struct{}
	doneCh chan struct{}
	wakeCh chan struct{}
	qjsGID atomic.Int64

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
	customTickMgrs     []TickManager
	gcConfig           GCConfig
	perms              *Permissions
	stdout             io.Writer
	stderr             io.Writer
	poolHandleFn       uintptr // unused in qjswasm but needed for pool.go shared code

	// uint8ArrayCtor caches globalThis.Uint8Array so newUint8ArrayLocked
	// doesn't walk the global object on every conversion.
	uint8ArrayCtor *qjs.Value

	// OnReady hook: fn is invoked once when the event loop first
	// observes no pending work during a RunEventLoop[For] call.
	onReadyMu   sync.Mutex
	onReadyFn   func()
	onReadyDone bool

	closeOnce sync.Once
	closed    atomic.Bool
}

// New creates a new qjswasm runtime.
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
	go r.qjswasmLoop(ready, cfg)
	if err := <-ready; err != nil {
		return nil, err
	}
	return r, nil
}

// Close tears down the underlying fastschema/qjs runtime and joins the
// engine goroutine.
func (r *Runtime) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	r.closeOnce.Do(func() {
		close(r.stopCh)
		<-r.doneCh
	})
	return nil
}

// Eval runs a JS snippet and returns the value. Caller must Close() the
// result.
func (r *Runtime) Eval(code string) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var out *Value
	var err error
	r.dispatch(func() {
		out, err = r.evalLocked(code)
	})
	return out, err
}

// Exec runs a JS snippet and discards the result.
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

// EvalWithContext runs Eval but returns after ctx is done. The wasm
// side is single-goroutine so cancellation only takes effect between
// snippets.
func (r *Runtime) EvalWithContext(ctx context.Context, code string) (*Value, error) {
	if ctx == nil {
		return r.Eval(code)
	}
	done := make(chan struct{})
	var out *Value
	var err error
	go func() {
		out, err = r.Eval(code)
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GlobalObject returns a reference to globalThis.
func (r *Runtime) GlobalObject() *Value {
	if r.closed.Load() {
		return nil
	}
	var out *Value
	r.dispatch(func() {
		out = r.wrapValue(r.qjsCtx.Global())
	})
	return out
}

// NewObject wraps a Go map as a JS object.
func (r *Runtime) NewObject(m map[string]any) (*Value, error) {
	return r.goToJSPublic(m)
}

// NewArray wraps a []any as a JS array.
func (r *Runtime) NewArray(items ...any) (*Value, error) {
	return r.goToJSPublic(items)
}

// NewUint8Array wraps a []byte as a Uint8Array. We construct the array via
// `new Uint8Array(buffer)` rather than qjsCtx.NewBytes: NewBytes returns an
// opaque WASM memory handle (not a typed array), so length/property access
// on the JS side would fail.
func (r *Runtime) NewUint8Array(b []byte) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var out *Value
	var err error
	r.dispatch(func() {
		fv, e := r.newUint8ArrayLocked(b)
		if e != nil {
			err = e
			return
		}
		out = r.wrapValue(fv)
	})
	return out, err
}

// -----------------------------------------------------------------------
// Dispatch loop
// -----------------------------------------------------------------------

func (r *Runtime) dispatch(fn func()) {
	if r.closed.Load() {
		return
	}
	if goid() == r.qjsGID.Load() {
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

// qjswasmLoop owns the fastschema/qjs runtime. All JS work happens here.
func (r *Runtime) qjswasmLoop(ready chan<- error, cfg *config) {
	runtime.LockOSThread()
	defer close(r.doneCh)

	r.qjsGID.Store(goid())

	rt, err := qjs.New(buildQJSOption(cfg))
	if err != nil {
		ready <- fmt.Errorf("ramune: qjs.New: %w", err)
		return
	}
	r.qjsRT = rt
	r.qjsCtx = rt.Context()

	// Cache the Uint8Array constructor once. Pulled from Global by name,
	// held via .Clone() so we never need to walk globalThis on the hot path.
	if g := r.qjsCtx.Global(); g != nil {
		if ctor := g.GetPropertyStr("Uint8Array"); ctor != nil {
			r.uint8ArrayCtor = ctor
		}
	}

	if err := r.installEventLoop(); err != nil {
		ready <- fmt.Errorf("ramune: event loop: %w", err)
		r.teardownLocked()
		return
	}
	if err := r.installConsole(); err != nil {
		ready <- fmt.Errorf("ramune: console: %w", err)
		r.teardownLocked()
		return
	}

	if cfg.nodeCompat {
		steps := []struct {
			name string
			fn   func() error
		}{
			{"nodecompat", r.installNodeCompat},
			{"async spawn", r.installAsyncSpawn},
			{"async fs", r.installAsyncFS},
			{"fs.watch", r.installFSWatch},
			{"vm", r.installVM},
			{"async net", r.installAsyncNet},
			{"tcp server", r.installTCPServer},
			{"dgram", r.installDgram},
			{"worker_threads", r.installWorkerThreads},
			{"http2", r.installHTTP2},
			{"web streams", r.installWebStreams},
			{"stream bridge", r.installStreamBridge},
			{"web crypto", r.installWebCrypto},
			{"bun compat", r.installBunCompat},
			{"csrf", r.installCSRF},
			{"archive", r.installArchive},
			{"cron", r.installCron},
			{"markdown", r.installMarkdown},
			{"webview", r.installWebView},
			{"cdp", r.installCDP},
			{"sqlite", r.installSQLite},
			{"WinterTC", r.installWinterTC},
		}
		for _, s := range steps {
			if err := s.fn(); err != nil {
				ready <- fmt.Errorf("ramune: %s: %w", s.name, err)
				r.teardownLocked()
				return
			}
		}
	}

	if cfg.winterTC && !cfg.nodeCompat {
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				ready <- fmt.Errorf("ramune: web streams: %w", err)
				r.teardownLocked()
				return
			}
		}
		if err := r.installWinterTC(); err != nil {
			ready <- fmt.Errorf("ramune: WinterTC: %w", err)
			r.teardownLocked()
			return
		}
	}

	if cfg.withFetch || cfg.nodeCompat {
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				ready <- fmt.Errorf("ramune: web streams: %w", err)
				r.teardownLocked()
				return
			}
			if err := r.installStreamBridge(); err != nil {
				ready <- fmt.Errorf("ramune: stream bridge: %w", err)
				r.teardownLocked()
				return
			}
		}
		if err := r.installFetch(); err != nil {
			ready <- fmt.Errorf("ramune: fetch: %w", err)
			r.teardownLocked()
			return
		}
	}

	if cfg.preloadJS != "" {
		if err := r.execLocked(cfg.preloadJS); err != nil {
			ready <- fmt.Errorf("ramune: preload JS: %w", err)
			r.teardownLocked()
			return
		}
	}

	if len(cfg.dependencies) > 0 {
		bundle, nodeModulesDir, err := ensureBundle(cfg.dependencies, cfg.nodeCompat)
		if err != nil {
			ready <- err
			r.teardownLocked()
			return
		}
		if nodeModulesDir != "" {
			r.execLocked(fmt.Sprintf("globalThis.__nodeModulesDir = %q;", nodeModulesDir))
			r.execLocked(fmt.Sprintf("if (globalThis.process && globalThis.process.env) { globalThis.process.env.PATH = %q + ':' + (globalThis.process.env.PATH || ''); }", nodeModulesDir+"/.bin"))
		}
		if err := r.execLocked(bundle); err != nil {
			ready <- fmt.Errorf("ramune: failed to load bundle: %w", err)
			r.teardownLocked()
			return
		}
	}

	for _, m := range cfg.modules {
		if err := r.loadModuleLocked(m); err != nil {
			ready <- fmt.Errorf("ramune: module %s: %w", m.Name, err)
			r.teardownLocked()
			return
		}
	}

	ready <- nil

	for {
		select {
		case fn := <-r.callCh:
			fn()
		case <-r.stopCh:
			r.teardownLocked()
			return
		}
	}
}

// buildQJSOption translates Ramune's config into qjs.Option. DisableFS
// closes the WASI FS mount so a QuickJS-NG VM escape can't pivot through
// it to reach the host filesystem — Ramune's CheckRead/CheckWrite gate
// the Go-side bridges, but an ambient WASI mount bypasses them entirely.
func buildQJSOption(cfg *config) qjs.Option {
	opt := qjs.Option{}
	if cfg == nil {
		return opt
	}

	if cfg.permissions.DeniesFS() {
		opt.DisableFS = true
	}

	if l := cfg.resourceLimits; l != nil {
		opt.MemoryLimit = clampToInt(l.MaxMemoryBytes)
		opt.MaxStackSize = clampToInt(l.MaxStackBytes)
		opt.GCThreshold = clampToInt(l.GCThresholdBytes)
	}

	return opt
}

// clampToInt narrows an int64 byte count to the int qjs.Option expects.
// On 32-bit targets int is 32-bit, so a value over math.MaxInt32 would
// silently wrap negative and C-side cast to uint64 would read it as
// ~18 EiB — effectively "unlimited", the opposite of what the caller
// asked. Clamp to math.MaxInt instead; the address space can't hold more
// than that anyway.
func clampToInt(v int64) int {
	if v > math.MaxInt {
		return math.MaxInt
	}
	if v < 0 {
		return 0
	}
	return int(v)
}

func (r *Runtime) teardownLocked() {
	if r.uint8ArrayCtor != nil {
		r.uint8ArrayCtor.Free()
		r.uint8ArrayCtor = nil
	}
	if r.qjsRT != nil {
		r.qjsRT.Close()
		r.qjsRT = nil
		r.qjsCtx = nil
	}
}

// -----------------------------------------------------------------------
// eval / exec helpers (run on the engine goroutine)
// -----------------------------------------------------------------------

func (r *Runtime) evalLocked(code string) (*Value, error) {
	v, err := r.qjsCtx.Eval("<eval>", qjs.Code(code))
	if err != nil {
		return nil, &JSError{Context: "eval", Message: err.Error()}
	}
	return r.wrapValue(v), nil
}

func (r *Runtime) execLocked(code string) error {
	// qjswasm-specific: fastschema/qjs's QJS_Eval C helper calls
	// js_std_await() whenever the script's final expression is a Promise,
	// which blocks the wasm thread forever if the Promise resolves via
	// our Go-side event loop (setTimeout, etc.). Exec discards the
	// result anyway — appending ;undefined; guarantees the top-level
	// expression is not a Promise, so js_std_await is never entered.
	v, err := r.qjsCtx.Eval("<exec>", qjs.Code(code+";undefined;"))
	if err != nil {
		return &JSError{Context: "exec", Message: err.Error()}
	}
	if v != nil {
		v.Free()
	}
	return nil
}

// evalScriptLocked runs a JS snippet with a specific filename for better
// stack traces and returns the wrapped value.
func (r *Runtime) evalScriptLocked(code, filename string) (*Value, error) {
	if filename == "" {
		filename = "<eval>"
	}
	v, err := r.qjsCtx.Eval(filename, qjs.Code(code))
	if err != nil {
		return nil, &JSError{Context: "eval", Message: err.Error()}
	}
	return r.wrapValue(v), nil
}
