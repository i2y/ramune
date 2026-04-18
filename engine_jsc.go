//go:build !quickjs && !goja && !qjswasm

// Package ramune provides Go bindings for JavaScriptCore via [purego] — no Cgo required.
// The JSC runtime is dynamically loaded at startup and works with the system framework
// on macOS and libjavascriptcoregtk on Linux.
//
// # Basic Usage
//
// Evaluate JavaScript and read results:
//
//	rt, err := ramune.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer rt.Close()
//
//	val, err := rt.Eval("1 + 2")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer val.Close()
//	fmt.Println(val.Float64()) // 3
//
// # Constructing Objects
//
// Create JS objects and arrays directly from Go:
//
//	obj, _ := rt.NewObject(map[string]any{"name": "Alice", "age": 30})
//	arr, _ := rt.NewArray(1, "two", true)
//	obj.SetAttr("tags", arr)
//
// # Go Callbacks
//
// Register Go functions callable from JavaScript:
//
//	rt.RegisterFunc("add", func(args []any) (any, error) {
//	    return args[0].(float64) + args[1].(float64), nil
//	})
//	val, _ := rt.Eval("add(3, 4)") // 7
//
// # npm Packages
//
// Use npm packages via automatic esbuild bundling:
//
//	rt, _ := ramune.New(
//	    ramune.NodeCompat(),
//	    ramune.Dependencies("lodash@4"),
//	)
//	val, _ := rt.Eval(`lodash.chunk([1,2,3,4,5,6], 2)`)
//
// # Event Loop and Async
//
// setTimeout, setInterval, and Promises work with the built-in event loop:
//
//	val, _ := rt.EvalAsync(`
//	    new Promise(resolve => setTimeout(() => resolve(42), 100))
//	`)
//	fmt.Println(val.Float64()) // 42
//
// # Fetch
//
// HTTP requests via globalThis.fetch backed by Go's net/http:
//
//	rt, _ := ramune.New(ramune.WithFetch())
//	val, _ := rt.EvalAsync(`
//	    fetch("https://api.example.com/data").then(r => r.json())
//	`)
//
// [purego]: https://github.com/ebitengine/purego
package ramune

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Engine returns the name of the JS engine backend.
func (r *Runtime) Engine() string { return "jsc" }

// Runtime holds a loaded JavaScriptCore library and a global JS context.
// Multiple Runtimes can coexist in the same process — each gets a dedicated
// OS thread for JSC access. All JSC operations are dispatched to this thread
// via a channel, ensuring thread identity across calls (required by JSC).
// The Runtime is safe for concurrent use from multiple goroutines.
type Runtime struct {
	handle uintptr // Dlopen handle
	group  uintptr // JSContextGroupRef (independent VM per Runtime)
	ctx    uintptr // JSGlobalContextRef

	// Dedicated JSC thread dispatch. All JSC operations are serialized
	// through callCh to a goroutine permanently pinned to one OS thread.
	callCh chan func()   // dispatch JSC operations to dedicated goroutine
	stopCh chan struct{} // signal the dedicated goroutine to exit
	doneCh chan struct{} // closed when dedicated goroutine has exited
	wakeCh chan struct{} // wakes event loop when async events arrive (buffered 1)
	jscGID atomic.Int64  // goroutine ID of the dedicated JSC goroutine (0 = not set)

	// --- Context ---
	jsContextGroupCreate         func() uintptr
	jsContextGroupRelease        func(uintptr)
	jsGlobalContextCreate        func(uintptr) uintptr // used on Linux
	jsGlobalContextCreateInGroup func(uintptr, uintptr) uintptr
	jsGlobalContextRelease       func(uintptr)
	jsContextGetGlobalObject     func(uintptr) uintptr

	// --- Evaluation ---
	jsEvaluateScript func(uintptr, uintptr, uintptr, uintptr, int32, uintptr) uintptr

	// --- GC ---
	jsGarbageCollect func(uintptr)

	// --- String ---
	jsStringCreateWithUTF8CString     func(string) uintptr
	jsStringRelease                   func(uintptr)
	jsStringGetMaximumUTF8CStringSize func(uintptr) uint64
	jsStringGetUTF8CString            func(uintptr, []byte, uint64) uint64

	// --- Value type checking ---
	jsValueGetType     func(uintptr, uintptr) int32
	jsValueIsUndefined func(uintptr, uintptr) bool
	jsValueIsNull      func(uintptr, uintptr) bool
	jsValueIsBoolean   func(uintptr, uintptr) bool
	jsValueIsNumber    func(uintptr, uintptr) bool
	jsValueIsString    func(uintptr, uintptr) bool
	jsValueIsObject    func(uintptr, uintptr) bool

	// --- Value creation ---
	jsValueMakeUndefined func(uintptr) uintptr
	jsValueMakeNull      func(uintptr) uintptr
	jsValueMakeBoolean   func(uintptr, bool) uintptr
	jsValueMakeNumber    func(uintptr, float64) uintptr
	jsValueMakeString    func(uintptr, uintptr) uintptr

	// --- Value conversion ---
	jsValueToBoolean    func(uintptr, uintptr) bool
	jsValueToNumber     func(uintptr, uintptr, uintptr) float64
	jsValueToStringCopy func(uintptr, uintptr, uintptr) uintptr
	jsValueToObject     func(uintptr, uintptr, uintptr) uintptr

	// --- Value protection ---
	jsValueProtect   func(uintptr, uintptr)
	jsValueUnprotect func(uintptr, uintptr)

	// --- Object ---
	jsObjectGetProperty               func(uintptr, uintptr, uintptr, uintptr) uintptr
	jsObjectSetProperty               func(uintptr, uintptr, uintptr, uintptr, uint32, uintptr)
	jsObjectCallAsFunction            func(uintptr, uintptr, uintptr, uint64, []uintptr, uintptr) uintptr
	jsObjectMake                      func(uintptr, uintptr, uintptr) uintptr
	jsObjectMakeArray                 func(uintptr, uint64, []uintptr, uintptr) uintptr
	jsObjectMakeFunctionWithCallback  func(uintptr, uintptr, uintptr) uintptr
	jsObjectDeleteProperty            func(uintptr, uintptr, uintptr, uintptr) bool
	jsObjectGetPropertyAtIndex        func(uintptr, uintptr, uint32, uintptr) uintptr
	jsObjectCopyPropertyNames         func(uintptr, uintptr) uintptr
	jsPropertyNameArrayGetCount       func(uintptr) uint64
	jsPropertyNameArrayGetNameAtIndex func(uintptr, uint64) uintptr
	jsPropertyNameArrayRelease        func(uintptr)
	jsObjectIsFunction                func(uintptr, uintptr) bool
	jsValueIsArray                    func(uintptr, uintptr) bool

	// --- TypedArray / ArrayBuffer (optional, nil if unsupported) ---
	jsObjectMakeTypedArray           func(uintptr, int32, uint64, uintptr) uintptr
	jsObjectGetTypedArrayBytesPtr    func(uintptr, uintptr, uintptr) uintptr
	jsObjectGetTypedArrayByteLength  func(uintptr, uintptr, uintptr) uint64
	jsObjectGetArrayBufferBytesPtr   func(uintptr, uintptr, uintptr) uintptr
	jsObjectGetArrayBufferByteLength func(uintptr, uintptr, uintptr) uint64

	callbacks       []uintptr           // prevent GC of purego callbacks
	jsonStringifyFn uintptr             // cached JSON.stringify JSObjectRef
	jsonParseFn     uintptr             // cached JSON.parse JSObjectRef
	goFuncs         []GoFunc            // registered Go functions (ID dispatch)
	dispatcherReady bool                // single dispatcher callback created
	nativeMethodSeq int                 // counter for unique native method callback names
	nativeReg       *nativeTypeRegistry // per-type struct callback registry
	fsMgr           *fsManager          // async filesystem manager
	fswatchMgr      *fsWatchManager     // fs.watch() manager
	vmMgr           *vmManager          // vm module context manager
	procMgr         *processManager     // async subprocess manager
	sockMgr         *socketManager      // async socket manager
	tcpSrvMgr       *tcpServerManager   // TCP server manager
	udpMgr          *udpManager         // UDP/dgram manager
	webviewMgr      *webviewManager     // WebView manager
	workerMgr       *workerManager      // worker threads manager
	http2Mgr        *http2Manager       // HTTP/2 session manager
	waitAsyncCount  atomic.Int32        // pending Atomics.waitAsync operations
	sqliteMgr       *sqliteManager      // bun:sqlite database manager
	streamMgr       *streamManager      // bidirectional stream bridge
	fetchMgr        *fetchManager       // streaming fetch request manager
	bunSrv          *bunServerState     // Bun.serve() state
	customTickMgrs  []TickManager       // user-registered event loop managers
	gcConfig        GCConfig            // GC configuration
	perms           *Permissions        // permission policy
	stdout          io.Writer           // console.log output
	stderr          io.Writer           // console.error output
	poolHandleFn    uintptr             // cached __poolHandleFast JSObjectRef (for RuntimePool)

	// Protected value tracking: values are unprotected on Runtime.Close()
	// if not explicitly closed. No Go finalizers are used to avoid SIGTRAP
	// from concurrent GC touching JSC internals.
	unprotectMu    sync.Mutex
	unprotectQueue []uintptr       // legacy: drained at safe points
	protectedPtrs  map[uintptr]int // ref-counted protected ptrs, cleaned on Close()

	closeOnce sync.Once
	closed    atomic.Bool
}

// New creates a new JavaScriptCore runtime with a fresh global context.
// Each Runtime gets a dedicated OS thread for all JSC operations, ensuring
// thread identity across calls. Multiple Runtimes can coexist safely.
func New(opts ...Option) (*Runtime, error) {
	return newRuntime(opts)
}

// newInternal creates a Runtime without any restrictions.
// Used internally by worker_threads which requires a child Runtime.
func newInternal(opts ...Option) (*Runtime, error) {
	return newRuntime(opts)
}

// sharedHandle holds a single dlopen handle for the JSC library.
// Shared across all Runtimes to avoid redundant dlopen calls.
var (
	sharedHandleMu   sync.Mutex
	sharedHandle     uintptr
	sharedHandleErr  error
	sharedHandleDone bool
)

func getSharedHandle(cfg config) (uintptr, error) {
	sharedHandleMu.Lock()
	defer sharedHandleMu.Unlock()
	if sharedHandleDone {
		return sharedHandle, sharedHandleErr
	}
	path, err := detectLibrary(cfg)
	if err != nil {
		sharedHandleErr = err
		sharedHandleDone = true
		return 0, err
	}
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		sharedHandleErr = fmt.Errorf("ramune: failed to load %s: %w", path, err)
		sharedHandleDone = true
		return 0, sharedHandleErr
	}
	// On Linux, configure JSC's GC signal to avoid conflict with Go runtime.
	configureJSCSignal(handle)

	sharedHandle = handle
	sharedHandleDone = true
	return handle, nil
}

// puregoMu serializes purego.NewCallback calls which use global state.
var puregoMu sync.Mutex

func newRuntime(opts []Option) (*Runtime, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	handle, err := getSharedHandle(cfg)
	if err != nil {
		return nil, err
	}

	rt := &Runtime{
		handle: handle,
		callCh: make(chan func(), 64),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		wakeCh: make(chan struct{}, 1),
	}
	if cfg.gc != nil {
		rt.gcConfig = *cfg.gc
	} else {
		rt.gcConfig = DefaultGCConfig()
	}
	if cfg.permissions != nil {
		rt.perms = cfg.permissions
	} else {
		rt.perms = AllPermissions()
	}
	rt.customTickMgrs = cfg.tickManagers
	rt.stdout = cfg.stdout
	if rt.stdout == nil {
		rt.stdout = os.Stdout
	}
	rt.stderr = cfg.stderr
	if rt.stderr == nil {
		rt.stderr = os.Stderr
	}

	// Start the dedicated JSC goroutine. All JSC operations
	// (bind, create, eval, close) happen on this single pinned OS thread.
	initErr := make(chan error, 1)
	go rt.jscLoop(cfg, initErr)

	if err := <-initErr; err != nil {
		return nil, err
	}

	return rt, nil
}

// jscLoop is the dedicated goroutine for all JSC operations.
// It pins itself to a single OS thread and never unpins, ensuring
// every JSC call for this Runtime happens on the exact same thread.
func (r *Runtime) jscLoop(cfg config, initErr chan<- error) {
	defer close(r.doneCh)
	runtime.LockOSThread()
	// NOTE: do NOT call runtime.UnlockOSThread().
	// This goroutine stays pinned to this OS thread for its entire lifetime.

	r.jscGID.Store(goid())

	// Bind JSC function pointers from the dlopen handle on this thread.
	if err := r.bindFunctions(); err != nil {
		initErr <- err
		return
	}
	r.bindTypedArrayFunctions()

	// Create JSC context. Platform-specific strategy:
	// - macOS: JSContextGroupCreate + JSGlobalContextCreateInGroup to bypass
	//   macOS's backwards-compat shared VM in JSGlobalContextCreate(NULL)
	//   (NSVersionOfLinkTimeLibrary returns -1 for dlopen'd libraries).
	// - Linux: JSGlobalContextCreate(NULL) because libjavascriptcoregtk
	//   crashes on JSContextGroupRelease + recreation.
	if runtime.GOOS == "darwin" {
		r.group = r.jsContextGroupCreate()
		if r.group == 0 {
			initErr <- fmt.Errorf("ramune: JSContextGroupCreate returned NULL")
			return
		}
		r.ctx = r.jsGlobalContextCreateInGroup(r.group, 0)
		if r.ctx == 0 {
			r.jsContextGroupRelease(r.group)
			initErr <- fmt.Errorf("ramune: JSGlobalContextCreateInGroup returned NULL")
			return
		}
	} else {
		r.ctx = r.jsGlobalContextCreate(0)
		if r.ctx == 0 {
			initErr <- fmt.Errorf("ramune: JSGlobalContextCreate returned NULL")
			return
		}
	}

	// releaseCtx cleans up context and group on init failure.
	releaseCtx := func() {
		r.jsGlobalContextRelease(r.ctx)
		r.jsContextGroupRelease(r.group)
	}

	// Install event loop (always — provides working setTimeout/setInterval).
	if err := r.installEventLoop(); err != nil {
		releaseCtx()
		initErr <- fmt.Errorf("ramune: failed to install event loop: %w", err)
		return
	}

	// Install console (always — console.log should work in all modes).
	if err := r.installConsole(); err != nil {
		releaseCtx()
		initErr <- fmt.Errorf("ramune: failed to install console: %w", err)
		return
	}

	// Install Node.js compatibility layer if requested.
	if cfg.nodeCompat {
		if err := r.installNodeCompat(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install Node.js compat: %w", err)
			return
		}
		if err := r.installAsyncSpawn(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install async spawn: %w", err)
			return
		}
		if err := r.installAsyncFS(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install async fs: %w", err)
			return
		}
		if err := r.installFSWatch(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install fs.watch: %w", err)
			return
		}
		if err := r.installVM(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install vm module: %w", err)
			return
		}
		if err := r.installAsyncNet(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install async net: %w", err)
			return
		}
		if err := r.installTCPServer(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install tcp server: %w", err)
			return
		}
		if err := r.installDgram(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install dgram: %w", err)
			return
		}
		if err := r.installWorkerThreads(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install worker_threads: %w", err)
			return
		}
		if err := r.installHTTP2(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install http2: %w", err)
			return
		}
		if err := r.installSharedArrayBuffer(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install SharedArrayBuffer: %w", err)
			return
		}
		if err := r.installWebStreams(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install Web Streams: %w", err)
			return
		}
		if err := r.installStreamBridge(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install stream bridge: %w", err)
			return
		}
		if err := r.installWebCrypto(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install Web Crypto: %w", err)
			return
		}
		if err := r.installBunCompat(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install Bun compat: %w", err)
			return
		}
		if err := r.installCSRF(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install CSRF: %w", err)
			return
		}
		if err := r.installArchive(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install Archive: %w", err)
			return
		}
		if err := r.installCron(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install cron: %w", err)
			return
		}
		if err := r.installMarkdown(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install markdown: %w", err)
			return
		}
		if err := r.installWebView(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install WebView: %w", err)
			return
		}
		if err := r.installCDP(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install CDP: %w", err)
			return
		}
		if err := r.installSQLite(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install bun:sqlite: %w", err)
			return
		}
		// WinterTC gap APIs (CompressionStream, MessageChannel, etc.)
		if err := r.installWinterTC(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install WinterTC: %w", err)
			return
		}
	}

	// Install WinterTC standalone (without NodeCompat).
	if cfg.winterTC && !cfg.nodeCompat {
		// Ensure prerequisites are available.
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				releaseCtx()
				initErr <- fmt.Errorf("ramune: failed to install Web Streams: %w", err)
				return
			}
		}
		if err := r.installWinterTC(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install WinterTC: %w", err)
			return
		}
	}

	// Install fetch polyfill if requested (or if nodeCompat is enabled).
	if cfg.withFetch || cfg.nodeCompat {
		// Ensure web streams and stream bridge are available for streaming fetch.
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				releaseCtx()
				initErr <- fmt.Errorf("ramune: failed to install Web Streams: %w", err)
				return
			}
			if err := r.installStreamBridge(); err != nil {
				releaseCtx()
				initErr <- fmt.Errorf("ramune: failed to install stream bridge: %w", err)
				return
			}
		}
		if err := r.installFetch(); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to install fetch: %w", err)
			return
		}
	}

	// Install user-provided modules.
	for _, m := range cfg.modules {
		if err := r.loadModuleLocked(m); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to load module %q: %w", m.Name, err)
			return
		}
	}

	// Execute preload JS (polyfills, etc.) before loading dependency bundles.
	if cfg.preloadJS != "" {
		if err := r.execLocked(cfg.preloadJS); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to execute preload JS: %w", err)
			return
		}
	}

	// If Dependencies were specified, bundle and evaluate them.
	if len(cfg.dependencies) > 0 {
		bundle, nodeModulesDir, err := ensureBundle(cfg.dependencies, cfg.nodeCompat)
		if err != nil {
			releaseCtx()
			initErr <- err
			return
		}
		if nodeModulesDir != "" {
			r.execLocked(fmt.Sprintf("globalThis.__nodeModulesDir = %q;", nodeModulesDir))
			r.execLocked(fmt.Sprintf("if (globalThis.process && globalThis.process.env) { globalThis.process.env.PATH = %q + ':' + (globalThis.process.env.PATH || ''); }", nodeModulesDir+"/.bin"))
		}
		if err := r.execLocked(bundle); err != nil {
			releaseCtx()
			initErr <- fmt.Errorf("ramune: failed to load bundle: %w", err)
			return
		}
	}

	// Clean up initialization artifacts.
	r.jsGarbageCollect(r.ctx)

	// Signal successful initialization.
	initErr <- nil

	// Main dispatch loop — process JSC operations from callCh.
	for {
		select {
		case fn := <-r.callCh:
			fn()
		case <-r.stopCh:
			return
		}
	}
}

// Close releases the JS global context and stops the dedicated JSC goroutine.
func (r *Runtime) Close() error {
	r.closeOnce.Do(func() {
		r.closed.Store(true)
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
		// Send cleanup work to the dedicated JSC goroutine.
		done := make(chan struct{})
		r.callCh <- func() {
			// Disable Go GC during JSC cleanup to prevent concurrent GC
			// from corrupting JSC internals during bulk value unprotection.
			prevGC := debug.SetGCPercent(-1)

			// Release VM contexts on the JSC thread (JSC API requires same-thread access).
			if r.vmMgr != nil {
				r.vmMgr.closeAll(r)
			}
			r.drainUnprotectQueue()
			// Unprotect all tracked values that were not explicitly closed.
			r.unprotectMu.Lock()
			ptrs := r.protectedPtrs
			r.protectedPtrs = nil
			r.unprotectMu.Unlock()
			for ptr, count := range ptrs {
				for i := 0; i < count; i++ {
					r.jsValueUnprotect(r.ctx, ptr)
				}
			}
			// Unprotect cached JSC references not tracked in protectedPtrs.
			if r.jsonStringifyFn != 0 {
				r.jsValueUnprotect(r.ctx, r.jsonStringifyFn)
				r.jsonStringifyFn = 0
			}
			if r.jsonParseFn != 0 {
				r.jsValueUnprotect(r.ctx, r.jsonParseFn)
				r.jsonParseFn = 0
			}
			if r.poolHandleFn != 0 {
				r.jsValueUnprotect(r.ctx, r.poolHandleFn)
				r.poolHandleFn = 0
			}
			if r.bunSrv != nil {
				r.bunSrv.releaseCachedRefs(r)
			}
			// Flush GC synchronously before releasing the context.
			r.jsGarbageCollect(r.ctx)
			r.jsGlobalContextRelease(r.ctx)
			if r.group != 0 {
				r.jsContextGroupRelease(r.group)
			}

			debug.SetGCPercent(prevGC)
			close(done)
		}
		<-done
		// Signal the dedicated goroutine to exit and wait.
		close(r.stopCh)
		<-r.doneCh
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

// EvalWithContext evaluates JavaScript code and returns the result,
// respecting the provided context for cancellation and deadlines.
// Since JSC cannot be interrupted mid-execution, the context is checked
// before evaluation begins. For timeout control over async operations,
// use EvalAsyncWithContext instead.
func (r *Runtime) EvalWithContext(ctx context.Context, code string) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return r.Eval(code)
}

// evalScriptLocked evaluates JS with retry on transient NULL.
// JSC can return NULL without an exception when Go's GC interferes
// with JSC internals. Retrying once after draining the unprotect
// queue resolves these transient failures.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) evalScriptLocked(code, context string) (uintptr, error) {
	r.drainUnprotectQueue()
	jsStr := r.jsStringCreateWithUTF8CString(code)
	defer r.jsStringRelease(jsStr)

	var exc uintptr
	result := r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, uintptr(unsafe.Pointer(&exc)))
	if result == 0 {
		if exc != 0 {
			msg, stack := r.getExceptionInfo(exc)
			return 0, &JSError{Context: context, Message: msg, Stack: stack}
		}
		r.drainUnprotectQueue()
		exc = 0
		result = r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, uintptr(unsafe.Pointer(&exc)))
		if result == 0 {
			if exc != 0 {
				msg, stack := r.getExceptionInfo(exc)
				return 0, &JSError{Context: context, Message: msg, Stack: stack}
			}
			return 0, &JSError{Context: context, Message: "JavaScript exception occurred"}
		}
	}
	return result, nil
}

// evalLocked evaluates JS code with the mutex already held.
func (r *Runtime) evalLocked(code string) (*Value, error) {
	result, err := r.evalScriptLocked(code, "Eval")
	if err != nil {
		return nil, err
	}
	return r.newValue(result), nil
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

// execLocked evaluates code with the mutex already held.
func (r *Runtime) execLocked(code string) error {
	_, err := r.evalScriptLocked(code, "Exec")
	return err
}

// GlobalObject returns the global object for this context.
func (r *Runtime) GlobalObject() *Value {
	if r.closed.Load() {
		return nil
	}
	var v *Value
	r.dispatch(func() {
		ptr := r.jsContextGetGlobalObject(r.ctx)
		v = r.newValue(ptr)
	})
	return v
}

// NewObject creates a new JavaScript object with the given properties.
func (r *Runtime) NewObject(props map[string]any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	r.dispatch(func() {
		obj := r.jsObjectMake(r.ctx, 0, 0)
		if obj == 0 {
			err = &JSError{Context: "NewObject", Message: "JSObjectMake returned NULL"}
			return
		}
		for key, v := range props {
			jsVal, e := r.goToJS(v)
			if e != nil {
				err = fmt.Errorf("ramune: NewObject property %q: %w", key, e)
				return
			}
			jsKey := r.jsStringCreateWithUTF8CString(key)
			r.jsObjectSetProperty(r.ctx, obj, jsKey, jsVal, 0, 0)
			r.jsStringRelease(jsKey)
		}
		val = r.newValue(obj)
	})
	return val, err
}

// NewArray creates a new JavaScript array with the given items.
func (r *Runtime) NewArray(items ...any) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	var val *Value
	var err error
	r.dispatch(func() {
		jsArgs := make([]uintptr, len(items))
		for i, item := range items {
			jsVal, e := r.goToJS(item)
			if e != nil {
				err = fmt.Errorf("ramune: NewArray item %d: %w", i, e)
				return
			}
			jsArgs[i] = jsVal
		}
		var exc uintptr
		arr := r.jsObjectMakeArray(r.ctx, uint64(len(jsArgs)), jsArgs, uintptr(unsafe.Pointer(&exc)))
		if exc != 0 {
			msg := r.jsValueToGoString(exc)
			err = &JSError{Context: "NewArray", Message: msg}
			return
		}
		val = r.newValue(arr)
	})
	return val, err
}

// goid returns the current goroutine ID.
// dispatch sends fn to the dedicated JSC goroutine and blocks until it completes.
// If called from within the JSC goroutine (e.g., inside a GoFunc callback),
// the function is executed directly to avoid deadlock.
func (r *Runtime) dispatch(fn func()) {
	// Re-entrance detection: if we're already on the JSC goroutine
	// (inside a callback or during init), execute directly.
	if r.jscGID.Load() == goid() {
		fn()
		return
	}
	done := make(chan struct{})
	select {
	case r.callCh <- func() {
		fn()
		close(done)
	}:
		<-done
	case <-r.doneCh:
		// JSC goroutine already exited (runtime closed).
	}
}

// drainUnprotectQueue unprotects all values queued by GC finalizers.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) drainUnprotectQueue() {
	r.unprotectMu.Lock()
	queue := r.unprotectQueue
	r.unprotectQueue = nil
	r.unprotectMu.Unlock()

	for _, ptr := range queue {
		r.jsValueUnprotect(r.ctx, ptr)
	}
}

// getExceptionInfo extracts message and stack trace from a JS exception value.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) getExceptionInfo(exc uintptr) (msg, stack string) {
	msg = r.jsValueToGoString(exc)

	// Try to read .stack property if the exception is an object.
	if !r.jsValueIsObject(r.ctx, exc) {
		return msg, ""
	}
	obj := r.jsValueToObject(r.ctx, exc, 0)
	if obj == 0 {
		return msg, ""
	}
	stackName := r.jsStringCreateWithUTF8CString("stack")
	defer r.jsStringRelease(stackName)
	stackVal := r.jsObjectGetProperty(r.ctx, obj, stackName, 0)
	if stackVal == 0 || r.jsValueIsUndefined(r.ctx, stackVal) {
		return msg, ""
	}
	return msg, r.jsValueToGoString(stackVal)
}

// NewUint8Array creates a JavaScript Uint8Array containing a copy of the given bytes.
func (r *Runtime) NewUint8Array(data []byte) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	if r.jsObjectMakeTypedArray == nil {
		return nil, fmt.Errorf("ramune: TypedArray API not available")
	}
	var val *Value
	var err error
	r.dispatch(func() {
		var exc uintptr
		obj := r.jsObjectMakeTypedArray(r.ctx, jsTypedArrayTypeUint8Array, uint64(len(data)), uintptr(unsafe.Pointer(&exc)))
		if exc != 0 {
			msg := r.jsValueToGoString(exc)
			err = &JSError{Context: "NewUint8Array", Message: msg}
			return
		}
		if obj == 0 {
			err = &JSError{Context: "NewUint8Array", Message: "JSObjectMakeTypedArray returned NULL"}
			return
		}
		if len(data) > 0 {
			bytesPtr := r.jsObjectGetTypedArrayBytesPtr(r.ctx, obj, 0)
			if bytesPtr == 0 {
				err = &JSError{Context: "NewUint8Array", Message: "failed to get TypedArray bytes pointer"}
				return
			}
			dst := unsafe.Slice((*byte)(unsafe.Pointer(bytesPtr)), len(data))
			copy(dst, data)
		}
		val = r.newValue(obj)
	})
	return val, err
}

// JSTypedArrayType constants matching JavaScriptCore's JSTypedArrayType enum.
const (
	jsTypedArrayTypeUint8Array int32 = 3
)

func (r *Runtime) bindFunctions() (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("ramune: failed to bind C function: %v", v)
		}
	}()

	// Context — use JSContextGroupCreate + JSGlobalContextCreateInGroup
	// to bypass macOS's backwards-compat shared VM in JSGlobalContextCreate.
	// When loaded via dlopen (not linked at build time), NSVersionOfLinkTimeLibrary
	// returns -1, causing JSGlobalContextCreate to force all contexts into one shared VM.
	purego.RegisterLibFunc(&r.jsContextGroupCreate, r.handle, "JSContextGroupCreate")
	purego.RegisterLibFunc(&r.jsContextGroupRelease, r.handle, "JSContextGroupRelease")
	purego.RegisterLibFunc(&r.jsGlobalContextCreate, r.handle, "JSGlobalContextCreate")
	purego.RegisterLibFunc(&r.jsGlobalContextCreateInGroup, r.handle, "JSGlobalContextCreateInGroup")
	purego.RegisterLibFunc(&r.jsGlobalContextRelease, r.handle, "JSGlobalContextRelease")
	purego.RegisterLibFunc(&r.jsContextGetGlobalObject, r.handle, "JSContextGetGlobalObject")

	// Evaluation
	purego.RegisterLibFunc(&r.jsEvaluateScript, r.handle, "JSEvaluateScript")

	// GC
	purego.RegisterLibFunc(&r.jsGarbageCollect, r.handle, "JSGarbageCollect")

	// String
	purego.RegisterLibFunc(&r.jsStringCreateWithUTF8CString, r.handle, "JSStringCreateWithUTF8CString")
	purego.RegisterLibFunc(&r.jsStringRelease, r.handle, "JSStringRelease")
	purego.RegisterLibFunc(&r.jsStringGetMaximumUTF8CStringSize, r.handle, "JSStringGetMaximumUTF8CStringSize")
	purego.RegisterLibFunc(&r.jsStringGetUTF8CString, r.handle, "JSStringGetUTF8CString")

	// Value type checking
	purego.RegisterLibFunc(&r.jsValueGetType, r.handle, "JSValueGetType")
	purego.RegisterLibFunc(&r.jsValueIsUndefined, r.handle, "JSValueIsUndefined")
	purego.RegisterLibFunc(&r.jsValueIsNull, r.handle, "JSValueIsNull")
	purego.RegisterLibFunc(&r.jsValueIsBoolean, r.handle, "JSValueIsBoolean")
	purego.RegisterLibFunc(&r.jsValueIsNumber, r.handle, "JSValueIsNumber")
	purego.RegisterLibFunc(&r.jsValueIsString, r.handle, "JSValueIsString")
	purego.RegisterLibFunc(&r.jsValueIsObject, r.handle, "JSValueIsObject")

	// Value creation
	purego.RegisterLibFunc(&r.jsValueMakeUndefined, r.handle, "JSValueMakeUndefined")
	purego.RegisterLibFunc(&r.jsValueMakeNull, r.handle, "JSValueMakeNull")
	purego.RegisterLibFunc(&r.jsValueMakeBoolean, r.handle, "JSValueMakeBoolean")
	purego.RegisterLibFunc(&r.jsValueMakeNumber, r.handle, "JSValueMakeNumber")
	purego.RegisterLibFunc(&r.jsValueMakeString, r.handle, "JSValueMakeString")

	// Value conversion
	purego.RegisterLibFunc(&r.jsValueToBoolean, r.handle, "JSValueToBoolean")
	purego.RegisterLibFunc(&r.jsValueToNumber, r.handle, "JSValueToNumber")
	purego.RegisterLibFunc(&r.jsValueToStringCopy, r.handle, "JSValueToStringCopy")
	purego.RegisterLibFunc(&r.jsValueToObject, r.handle, "JSValueToObject")

	// Value protection
	purego.RegisterLibFunc(&r.jsValueProtect, r.handle, "JSValueProtect")
	purego.RegisterLibFunc(&r.jsValueUnprotect, r.handle, "JSValueUnprotect")

	// Object
	purego.RegisterLibFunc(&r.jsObjectMake, r.handle, "JSObjectMake")
	purego.RegisterLibFunc(&r.jsObjectMakeArray, r.handle, "JSObjectMakeArray")
	purego.RegisterLibFunc(&r.jsObjectGetProperty, r.handle, "JSObjectGetProperty")
	purego.RegisterLibFunc(&r.jsObjectSetProperty, r.handle, "JSObjectSetProperty")
	purego.RegisterLibFunc(&r.jsObjectCallAsFunction, r.handle, "JSObjectCallAsFunction")
	purego.RegisterLibFunc(&r.jsObjectMakeFunctionWithCallback, r.handle, "JSObjectMakeFunctionWithCallback")
	purego.RegisterLibFunc(&r.jsObjectDeleteProperty, r.handle, "JSObjectDeleteProperty")
	purego.RegisterLibFunc(&r.jsObjectGetPropertyAtIndex, r.handle, "JSObjectGetPropertyAtIndex")
	purego.RegisterLibFunc(&r.jsObjectCopyPropertyNames, r.handle, "JSObjectCopyPropertyNames")
	purego.RegisterLibFunc(&r.jsPropertyNameArrayGetCount, r.handle, "JSPropertyNameArrayGetCount")
	purego.RegisterLibFunc(&r.jsPropertyNameArrayGetNameAtIndex, r.handle, "JSPropertyNameArrayGetNameAtIndex")
	purego.RegisterLibFunc(&r.jsPropertyNameArrayRelease, r.handle, "JSPropertyNameArrayRelease")
	purego.RegisterLibFunc(&r.jsObjectIsFunction, r.handle, "JSObjectIsFunction")
	purego.RegisterLibFunc(&r.jsValueIsArray, r.handle, "JSValueIsArray")

	return nil
}

// bindTypedArrayFunctions binds TypedArray/ArrayBuffer functions.
// These are optional — if unavailable, the function pointers remain nil.
func (r *Runtime) bindTypedArrayFunctions() {
	defer func() {
		if v := recover(); v != nil {
			r.jsObjectMakeTypedArray = nil
			r.jsObjectGetTypedArrayBytesPtr = nil
			r.jsObjectGetTypedArrayByteLength = nil
			r.jsObjectGetArrayBufferBytesPtr = nil
			r.jsObjectGetArrayBufferByteLength = nil
		}
	}()

	purego.RegisterLibFunc(&r.jsObjectMakeTypedArray, r.handle, "JSObjectMakeTypedArray")
	purego.RegisterLibFunc(&r.jsObjectGetTypedArrayBytesPtr, r.handle, "JSObjectGetTypedArrayBytesPtr")
	purego.RegisterLibFunc(&r.jsObjectGetTypedArrayByteLength, r.handle, "JSObjectGetTypedArrayByteLength")
	purego.RegisterLibFunc(&r.jsObjectGetArrayBufferBytesPtr, r.handle, "JSObjectGetArrayBufferBytesPtr")
	purego.RegisterLibFunc(&r.jsObjectGetArrayBufferByteLength, r.handle, "JSObjectGetArrayBufferByteLength")
}

// drainUnprotectQueue is kept for legacy compatibility but should
// be empty since finalizers are no longer used on Value objects.
