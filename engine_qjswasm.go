//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Engine returns the name of the JS engine backend.
func (r *Runtime) Engine() string { return "qjswasm" }

//go:embed third_party/ramune-qjs-shim/quickjs.wasm
var quickjsWASM []byte

// A stub wasm is 8 bytes ("\0asm\1\0\0\0"). Fail clearly if someone tries
// to run the backend without having built the real artifact.
func checkWasmBuilt() error {
	if len(quickjsWASM) < 1024 {
		return errors.New("ramune/qjswasm: embedded quickjs.wasm is a stub (" +
			fmt.Sprint(len(quickjsWASM)) + " bytes); run `make build-wasm-shim` " +
			"after installing wasi-sdk (see third_party/ramune-qjs-shim/README.md)")
	}
	return nil
}

// Runtime holds a wazero-hosted QuickJS-NG wasm module. One Runtime == one
// dedicated goroutine pinned via runtime.LockOSThread, one wazero.Runtime,
// one instantiated wasm module, one JSRuntime and one JSContext inside the
// module. All wasm operations are dispatched to the owning goroutine via
// callCh, mirroring the modernc QuickJS backend's concurrency model.
type Runtime struct {
	// Wazero state.
	wzRt     wazero.Runtime
	wzMod    api.Module
	wzMem    api.Memory
	wzExp    wasmExports // cached typed function handles
	wzCache  wazero.CompilationCache
	qjsRT    uint32 // JSRuntime * in wasm linear memory
	qjsCtx   uint32 // JSContext * in wasm linear memory
	wzCtx    context.Context
	wzCancel context.CancelFunc

	// Dedicated engine goroutine dispatch.
	callCh chan func()
	stopCh chan struct{}
	doneCh chan struct{}
	wakeCh chan struct{}
	qjsGID atomic.Int64

	// scratch{Base,End,Cursor} form a bump allocator on a pre-allocated
	// wasm-memory region used to avoid rmn_malloc / rmn_free per Eval.
	// writeStringLocked bumps `scratchCursor` forward; wasmFreeLocked
	// rewinds it when the freed ptr is the last-allocated slice (LIFO
	// freeing, which matches our `defer free` usage). Anything that
	// overflows the buffer or that frees out-of-order falls back to
	// per-call rmn_malloc.
	scratchBase   uint32
	scratchEnd    uint32
	scratchCursor uint32

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
	poolHandleFn    uintptr // unused in qjswasm but kept for cross-backend Runtime shape

	closeOnce sync.Once
	closed    atomic.Bool

	// insideMicrotasks guards against nested JS_ExecutePendingJob calls —
	// QuickJS's job queue is non-reentrant so when rawEvalLocked is invoked
	// from within a Go callback (which is itself running inside a
	// microtask), we skip the inner drain and let the outer loop pump.
	insideMicrotasks bool
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
	if err := checkWasmBuilt(); err != nil {
		return nil, err
	}

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

// Close tears down the wasm runtime and joins the engine goroutine.
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
// result (no-op for now; VM handles teardown).
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

// EvalWithContext runs Eval but returns after ctx is done. The wasm side is
// single-goroutine so we can only cancel BETWEEN snippets, not mid-eval;
// callers that need hard cancellation should use Runtime.Close.
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
		v, err := r.evalLocked("globalThis")
		if err == nil {
			out = v
		}
	})
	return out
}

// NewObject wraps a Go map as a JS object. Backend-agnostic wire via
// goToJS -> val_from_json.
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
	var err error
	r.dispatch(func() {
		h, e := r.newUint8ArrayLocked(b)
		if e != nil {
			err = e
			return
		}
		if isExceptionHandle(h) {
			err = r.pullExceptionLocked()
			return
		}
		out = r.wrapValue(h)
	})
	return out, err
}

// Wake is defined in eventloop.go (backend-agnostic).

// -----------------------------------------------------------------------
// Dispatch loop
// -----------------------------------------------------------------------

// dispatch runs fn on the engine goroutine. If the caller is already on
// that goroutine (reentrant callback), it runs inline.
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

// qjswasmLoop owns the wasm module. Everything wasm-side happens here.
func (r *Runtime) qjswasmLoop(ready chan<- error, cfg *config) {
	runtime.LockOSThread()
	defer close(r.doneCh)

	r.qjsGID.Store(goid())

	ctx, cancel := context.WithCancel(context.Background())
	r.wzCtx = ctx
	r.wzCancel = cancel

	// Compiler mode: AOT-compile wasm to native machine code. This is the
	// performance thesis of the qjswasm backend. The `eval` wasm export
	// must never be re-entered from inside a Go host callback — doing so
	// corrupts the outer trampoline's uint64 return under wazero's
	// compiler mode. JSFunc.Call / fn.Close use dedicated shim exports
	// (global_get_prop / global_delete_prop / val_call) that re-enter
	// safely. User code that calls CallbackContext.Eval / Exec from
	// inside a Go callback will trip the bug, which is a known limitation
	// until wazero's compiler handles eval re-entry correctly.
	r.wzCache = wazero.NewCompilationCache()
	rtCfg := wazero.NewRuntimeConfigCompiler().
		WithCompilationCache(r.wzCache).
		WithCloseOnContextDone(true)
	r.wzRt = wazero.NewRuntimeWithConfig(ctx, rtCfg)

	// WASI imports (wasi_snapshot_preview1).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r.wzRt); err != nil {
		ready <- fmt.Errorf("ramune: wasi instantiate: %w", err)
		r.wzRt.Close(ctx)
		return
	}

	// Our env.* host module (go_dispatch, host_log, host_panic).
	if err := r.installWazeroHost(ctx); err != nil {
		ready <- fmt.Errorf("ramune: host module: %w", err)
		r.wzRt.Close(ctx)
		return
	}

	// Use CompileModule + InstantiateModule with a ModuleConfig that
	// wires real host-backed time/clock syscalls through wazero. Without
	// this, QuickJS-NG falls back to stubs emitted by wasi-emulated-*
	// libs that it links against — the stubs are functional but emit
	// zero for clock_gettime, which causes QuickJS's GC heuristics to
	// run more aggressively than intended. Matching fastschema/qjs
	// (which uses WithSysWalltime / WithSysNanotime / WithSysNanosleep)
	// closes the performance gap.
	compiled, err := r.wzRt.CompileModule(ctx, quickjsWASM)
	if err != nil {
		ready <- fmt.Errorf("ramune: wasm compile: %w", err)
		r.wzRt.Close(ctx)
		return
	}
	mod, err := r.wzRt.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().
			WithSysWalltime().
			WithSysNanotime().
			WithSysNanosleep().
			WithStdout(r.stdout).
			WithStderr(r.stderr))
	if err != nil {
		ready <- fmt.Errorf("ramune: wasm instantiate: %w", err)
		r.wzRt.Close(ctx)
		return
	}
	r.wzMod = mod
	r.wzMem = mod.Memory()
	if err := r.wzExp.resolve(mod); err != nil {
		ready <- fmt.Errorf("ramune: resolve exports: %w", err)
		_ = mod.Close(ctx)
		r.wzRt.Close(ctx)
		return
	}

	// rt_new / ctx_new
	rtRes, err := r.wzExp.rtNew.Call(ctx)
	if err != nil {
		ready <- fmt.Errorf("ramune: rt_new: %w", err)
		r.teardownLocked()
		return
	}
	r.qjsRT = uint32(rtRes[0])
	if r.qjsRT == 0 {
		ready <- errors.New("ramune: rt_new returned 0")
		r.teardownLocked()
		return
	}
	ctxRes, err := r.wzExp.ctxNew.Call(ctx, uint64(r.qjsRT))
	if err != nil {
		ready <- fmt.Errorf("ramune: ctx_new: %w", err)
		r.teardownLocked()
		return
	}
	r.qjsCtx = uint32(ctxRes[0])
	if r.qjsCtx == 0 {
		ready <- errors.New("ramune: ctx_new returned 0")
		r.teardownLocked()
		return
	}

	// Pre-allocate a scratch bump-allocator. Strings crossing the wasm
	// boundary during Eval / Attr / Call / etc. go here so we don't
	// rmn_malloc + rmn_free on every operation.
	const scratchSize = 64 * 1024
	scratchRes, err := r.wzExp.rmnMalloc.Call(ctx, scratchSize)
	if err != nil || scratchRes[0] == 0 {
		ready <- fmt.Errorf("ramune: scratch malloc: %w", err)
		r.teardownLocked()
		return
	}
	r.scratchBase = uint32(scratchRes[0])
	r.scratchEnd = r.scratchBase + scratchSize
	r.scratchCursor = r.scratchBase

	// Install the JS-side event loop and console polyfills. Both are
	// backend-agnostic (they go through RegisterFunc + execLocked) so
	// they port straight from the other backends. NodeCompat / fetch /
	// Bun.serve / etc. are opt-in via cfg flags and wire up below.
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

	// NodeCompat installs the full Node.js API surface. Each install* is
	// backend-agnostic (uses RegisterFunc + execLocked), so the set mirrors
	// engine_goja.go / engine_quickjs.go. Exceptions:
	//   - installSharedArrayBuffer depends on host SharedArrayBuffer support
	//     (not implemented in the wasm shim yet).
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
	ctx := r.wzCtx
	if r.qjsCtx != 0 {
		_, _ = r.wzExp.ctxFree.Call(ctx, uint64(r.qjsCtx))
		r.qjsCtx = 0
	}
	if r.qjsRT != 0 {
		_, _ = r.wzExp.rtFree.Call(ctx, uint64(r.qjsRT))
		r.qjsRT = 0
	}
	if r.wzMod != nil {
		_ = r.wzMod.Close(ctx)
		r.wzMod = nil
	}
	if r.wzRt != nil {
		_ = r.wzRt.Close(ctx)
		r.wzRt = nil
	}
	if r.wzCache != nil {
		_ = r.wzCache.Close(ctx)
		r.wzCache = nil
	}
	if r.wzCancel != nil {
		r.wzCancel()
	}
}

// -----------------------------------------------------------------------
// Wasm-side eval helpers (called from the engine goroutine)
// -----------------------------------------------------------------------

// evalLocked runs JS_Eval on the wasm side and wraps the result.
func (r *Runtime) evalLocked(code string) (*Value, error) {
	h, err := r.rawEvalLocked(code, "<eval>", 0)
	if err != nil {
		return nil, err
	}
	if isExceptionHandle(h) {
		return nil, r.pullExceptionLocked()
	}
	return r.wrapValue(h), nil
}

// execLocked runs JS_Eval and discards the result.
func (r *Runtime) execLocked(code string) error {
	h, err := r.rawEvalLocked(code, "<exec>", 0)
	if err != nil {
		return err
	}
	if isExceptionHandle(h) {
		return r.pullExceptionLocked()
	}
	r.freeValueLocked(h)
	return nil
}

// evalScriptLocked runs a JS snippet with a specific filename for better
// stack traces and returns the wrapped value.
func (r *Runtime) evalScriptLocked(code, filename string) (*Value, error) {
	h, err := r.rawEvalLocked(code, filename, 0)
	if err != nil {
		return nil, err
	}
	if isExceptionHandle(h) {
		return nil, r.pullExceptionLocked()
	}
	return r.wrapValue(h), nil
}

func (r *Runtime) rawEvalLocked(code, fname string, flags uint32) (uint64, error) {
	codePtr, codeLen, err := r.writeStringLocked(code)
	if err != nil {
		return 0, err
	}
	defer r.wasmFreeLocked(codePtr)

	var fPtr, fLen uint32
	if fname != "" {
		fPtr, fLen, err = r.writeStringLocked(fname)
		if err != nil {
			return 0, err
		}
		defer r.wasmFreeLocked(fPtr)
	}

	res, err := r.wzExp.eval.Call(r.wzCtx,
		uint64(r.qjsCtx),
		uint64(codePtr), uint64(codeLen),
		uint64(fPtr), uint64(fLen),
		uint64(flags))
	if err != nil {
		return 0, fmt.Errorf("ramune: eval: %w", err)
	}
	if !r.insideMicrotasks {
		r.insideMicrotasks = true
		defer func() { r.insideMicrotasks = false }()
		_, _ = r.wzExp.executePendingJobs.Call(r.wzCtx, uint64(r.qjsRT))
	}
	return res[0], nil
}

// writeStringLocked writes s (NUL-terminated) into wasm memory and
// returns (ptr, len). Short strings bump the pre-allocated scratch
// buffer — callers free by rewinding the cursor via wasmFreeLocked.
// Long strings (or nested allocations that overflow scratch) fall back
// to per-call rmn_malloc. The NUL byte is required because QuickJS-NG's
// tokenizer peeks one byte past the declared end in some paths.
func (r *Runtime) writeStringLocked(s string) (uint32, uint32, error) {
	if s == "" {
		return 0, 0, nil
	}
	n := uint32(len(s))
	// 8-byte align the next cursor to keep subsequent allocations happy.
	need := (n + 1 + 7) &^ 7
	if r.scratchBase != 0 && r.scratchCursor+need <= r.scratchEnd {
		ptr := r.scratchCursor
		if !r.wzMem.Write(ptr, []byte(s)) {
			return 0, 0, errors.New("ramune: wasm scratch write out of range")
		}
		if !r.wzMem.WriteByte(ptr+n, 0) {
			return 0, 0, errors.New("ramune: wasm scratch NUL out of range")
		}
		r.scratchCursor += need
		return ptr, n, nil
	}
	ptr, err := r.wasmMallocLocked(n + 1)
	if err != nil {
		return 0, 0, err
	}
	if !r.wzMem.Write(ptr, []byte(s)) {
		r.wasmFreeLocked(ptr)
		return 0, 0, errors.New("ramune: wasm memory write out of range")
	}
	if !r.wzMem.WriteByte(ptr+n, 0) {
		r.wasmFreeLocked(ptr)
		return 0, 0, errors.New("ramune: wasm memory NUL-terminate out of range")
	}
	return ptr, n, nil
}

func (r *Runtime) readStringLocked(ptr, length uint32) (string, error) {
	if ptr == 0 || length == 0 {
		return "", nil
	}
	buf, ok := r.wzMem.Read(ptr, length)
	if !ok {
		return "", errors.New("ramune: wasm memory read out of range")
	}
	return string(buf), nil
}

func (r *Runtime) wasmMallocLocked(size uint32) (uint32, error) {
	if size == 0 {
		return 0, nil
	}
	res, err := r.wzExp.rmnMalloc.Call(r.wzCtx, uint64(size))
	if err != nil {
		return 0, fmt.Errorf("ramune: rmn_malloc: %w", err)
	}
	if res[0] == 0 {
		return 0, errors.New("ramune: rmn_malloc returned 0")
	}
	return uint32(res[0]), nil
}

func (r *Runtime) wasmFreeLocked(ptr uint32) {
	if ptr == 0 {
		return
	}
	// Scratch slice: rewind the cursor back to this ptr. LIFO matches
	// our `defer r.wasmFreeLocked(x)` pattern — nested frees return the
	// cursor to exactly where the outer write left it.
	if ptr >= r.scratchBase && ptr < r.scratchEnd {
		if ptr < r.scratchCursor {
			r.scratchCursor = ptr
		}
		return
	}
	_, _ = r.wzExp.rmnFree.Call(r.wzCtx, uint64(ptr))
}

// globalGetPropLocked fetches globalThis[name] via the dedicated shim
// export (no `eval` re-entry). Used by JSFunc.Call and jsFuncToHandle so
// they stay safe when called from inside Go callbacks under wazero
// compiler mode.
func (r *Runtime) globalGetPropLocked(name string) (uint64, error) {
	ptr, length, err := r.writeStringLocked(name)
	if err != nil {
		return 0, err
	}
	defer r.wasmFreeLocked(ptr)
	res, err := r.wzExp.globalGetProp.Call(r.wzCtx,
		uint64(r.qjsCtx), uint64(ptr), uint64(length))
	if err != nil {
		return 0, err
	}
	return res[0], nil
}

// globalDeletePropLocked removes globalThis[name] via the dedicated shim
// export (no `eval` re-entry).
func (r *Runtime) globalDeletePropLocked(name string) error {
	ptr, length, err := r.writeStringLocked(name)
	if err != nil {
		return err
	}
	defer r.wasmFreeLocked(ptr)
	_, err = r.wzExp.globalDeleteProp.Call(r.wzCtx,
		uint64(r.qjsCtx), uint64(ptr), uint64(length))
	return err
}

// freeValueLocked is a deliberate no-op today; values live until
// Runtime.Close(). This mirrors engine_quickjs.go's Value.Close() decision
// to avoid races with pending promise callbacks. Callers still route
// through this single seam so growing an unprotect queue later is
// transparent to them.
func (r *Runtime) freeValueLocked(h uint64) {}

// pullExceptionLocked grabs the pending exception and wraps it in JSError.
func (r *Runtime) pullExceptionLocked() error {
	res, err := r.wzExp.getException.Call(r.wzCtx, uint64(r.qjsCtx))
	if err != nil {
		return &JSError{Context: "eval", Message: err.Error()}
	}
	exc := res[0]

	info, err := r.wzExp.exceptionToJson.Call(r.wzCtx, uint64(r.qjsCtx), exc)
	if err != nil {
		r.freeValueLocked(exc)
		return &JSError{Context: "eval", Message: err.Error()}
	}
	r.freeValueLocked(exc)

	ptr, length := unpackPtrLen(info[0])
	if ptr == 0 {
		return &JSError{Context: "eval", Message: "unknown error"}
	}
	defer r.wasmFreeLocked(ptr)

	raw, rerr := r.readStringLocked(ptr, length)
	if rerr != nil {
		return &JSError{Context: "eval", Message: rerr.Error()}
	}
	var parsed struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return &JSError{Context: "eval", Message: raw}
	}
	return &JSError{Context: "eval", Message: parsed.Message, Stack: parsed.Stack}
}

// -----------------------------------------------------------------------
// Helpers exposed to other backend files
// -----------------------------------------------------------------------

func isExceptionHandle(v uint64) bool {
	// JS_TAG_EXCEPTION = 6, JS_EXCEPTION = JS_MKVAL(6, 0) = (6<<32) | 0
	return v == (uint64(jsTagException) << 32)
}

func unpackPtrLen(v uint64) (uint32, uint32) {
	return uint32(v >> 32), uint32(v)
}

// JS tag constants mirror third_party/quickjs-ng/quickjs.h
const (
	jsTagException = 6
	jsTagUndefined = 3
	jsTagNull      = 2
	jsTagBool      = 1
	jsTagInt       = 0
	jsTagFloat64   = 7
	jsTagString    = -7
	jsTagObject    = -1
)

// -----------------------------------------------------------------------
// Value kind bitfield (mirrors ramune_shim.h VAL_KIND_*)
// -----------------------------------------------------------------------

const (
	valKindUndefined = 0x001
	valKindNull      = 0x002
	valKindBool      = 0x004
	valKindNumber    = 0x008
	valKindString    = 0x010
	valKindObject    = 0x020
	valKindArray     = 0x040
	valKindFunction  = 0x080
	valKindPromise   = 0x100
	valKindException = 0x200
)
