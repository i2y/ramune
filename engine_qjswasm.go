//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/fastschema/qjs"
)

// Engine returns the name of the JS engine backend.
func (r *Runtime) Engine() string { return "qjswasm" }

// Runtime wraps a fastschema/qjs runtime. fastschema's runtime is itself
// QuickJS-NG compiled to WebAssembly, driven by wazero's compiler-mode
// JIT — we delegate all the hot-path work to it and keep a thin Go
// wrapper that matches Ramune's cross-backend API surface (same shape
// as engine_jsc.go / engine_quickjs.go / engine_goja.go).
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

	goFuncs         []GoFunc
	nativeMethodSeq int
	nativeReg       *nativeTypeRegistry
	fsMgr           *fsManager
	fswatchMgr      *fsWatchManager
	vmMgr           *vmManager
	procMgr         *processManager
	sockMgr         *socketManager
	tcpSrvMgr       *tcpServerManager
	udpMgr          *udpManager
	webviewMgr      *webviewManager
	workerMgr       *workerManager
	http2Mgr        *http2Manager
	waitAsyncCount  atomic.Int32
	sqliteMgr       *sqliteManager
	streamMgr       *streamManager
	fetchMgr        *fetchManager
	bunSrv          *bunServerState
	customTickMgrs  []TickManager
	gcConfig        GCConfig
	perms           *Permissions
	stdout          io.Writer
	stderr          io.Writer
	poolHandleFn    uintptr

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

// NewUint8Array wraps a []byte as a Uint8Array.
func (r *Runtime) NewUint8Array(b []byte) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var out *Value
	r.dispatch(func() {
		out = r.wrapValue(r.qjsCtx.NewBytes(b))
	})
	return out, nil
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

	rt, err := qjs.New()
	if err != nil {
		ready <- fmt.Errorf("ramune: qjs.New: %w", err)
		return
	}
	r.qjsRT = rt
	r.qjsCtx = rt.Context()

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

func (r *Runtime) teardownLocked() {
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
	v, err := r.qjsCtx.Eval("<exec>", qjs.Code(code))
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
