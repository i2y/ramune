//go:build quickjs

package ramune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"modernc.org/quickjs"
)

// Engine returns the name of the JS engine backend.
func (r *Runtime) Engine() string { return "quickjs" }

// Runtime holds a QuickJS VM and global JS context.
// Multiple Runtimes can coexist in the same process — each gets a dedicated
// OS thread. All QuickJS operations are dispatched to this thread via a channel.
type Runtime struct {
	vm *quickjs.VM

	// Dedicated engine thread dispatch.
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
	workerMgr       *workerManager
	sqliteMgr       *sqliteManager
	streamMgr       *streamManager
	fetchMgr        *fetchManager
	bunSrv          *bunServerState
	customTickMgrs  []TickManager // user-registered event loop managers
	gcConfig        GCConfig
	perms           *Permissions
	stdout          io.Writer
	stderr          io.Writer
	poolHandleFn    uintptr // unused in quickjs but needed for pool.go shared code

	closeOnce sync.Once
	closed    atomic.Bool
}

// New creates a new QuickJS runtime.
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

	// Start the dedicated engine goroutine.
	ready := make(chan error, 1)
	go r.qjsLoop(ready, cfg)

	if err := <-ready; err != nil {
		return nil, err
	}

	return r, nil
}

// qjsLoop is the dedicated goroutine for QuickJS operations.
func (r *Runtime) qjsLoop(ready chan<- error, cfg *config) {
	runtime.LockOSThread()
	defer close(r.doneCh)

	r.qjsGID.Store(goid())

	vm, err := quickjs.NewVM()
	if err != nil {
		ready <- fmt.Errorf("ramune: quickjs: %w", err)
		return
	}
	r.vm = vm

	// Install event loop.
	if err := r.installEventLoop(); err != nil {
		vm.Close()
		ready <- fmt.Errorf("ramune: event loop: %w", err)
		return
	}

	// Install console (always -- console.log should work in all modes).
	if err := r.installConsole(); err != nil {
		vm.Close()
		ready <- fmt.Errorf("ramune: console: %w", err)
		return
	}

	// Install Node.js compatibility layer if requested.
	if cfg.nodeCompat {
		if err := r.installNodeCompat(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: nodecompat: %w", err)
			return
		}
		if err := r.installAsyncSpawn(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: async spawn: %w", err)
			return
		}
		if err := r.installAsyncFS(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: async fs: %w", err)
			return
		}
		if err := r.installFSWatch(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: fs.watch: %w", err)
			return
		}
		if err := r.installVM(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: vm: %w", err)
			return
		}
		if err := r.installAsyncNet(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: async net: %w", err)
			return
		}
		if err := r.installTCPServer(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: tcp server: %w", err)
			return
		}
		if err := r.installWorkerThreads(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: worker_threads: %w", err)
			return
		}
		if err := r.installSharedArrayBuffer(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: SharedArrayBuffer: %w", err)
			return
		}
		if err := r.installWebStreams(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: web streams: %w", err)
			return
		}
		if err := r.installStreamBridge(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: stream bridge: %w", err)
			return
		}
		if err := r.installWebCrypto(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: web crypto: %w", err)
			return
		}
		if err := r.installBunCompat(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: bun compat: %w", err)
			return
		}
		if err := r.installCSRF(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: csrf: %w", err)
			return
		}
		if err := r.installArchive(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: archive: %w", err)
			return
		}
		if err := r.installCron(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: cron: %w", err)
			return
		}
		if err := r.installMarkdown(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: markdown: %w", err)
			return
		}
		if err := r.installSQLite(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: sqlite: %w", err)
			return
		}
	}

	// Install fetch polyfill if requested (or if nodeCompat is enabled).
	if cfg.withFetch || cfg.nodeCompat {
		if r.streamMgr == nil {
			if err := r.installWebStreams(); err != nil {
				vm.Close()
				ready <- fmt.Errorf("ramune: web streams: %w", err)
				return
			}
			if err := r.installStreamBridge(); err != nil {
				vm.Close()
				ready <- fmt.Errorf("ramune: stream bridge: %w", err)
				return
			}
		}
		if err := r.installFetch(); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: fetch: %w", err)
			return
		}
	}

	// Execute preload JS (polyfills, etc.) before loading dependency bundles.
	if cfg.preloadJS != "" {
		if err := r.execLocked(cfg.preloadJS); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: failed to execute preload JS: %w", err)
			return
		}
	}

	// If Dependencies were specified, bundle and evaluate them.
	if len(cfg.dependencies) > 0 {
		bundle, err := ensureBundle(cfg.dependencies, cfg.nodeCompat)
		if err != nil {
			vm.Close()
			ready <- err
			return
		}
		if err := r.execLocked(bundle); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: failed to load bundle: %w", err)
			return
		}
	}

	// Load user modules.
	for _, m := range cfg.modules {
		if err := r.loadModuleLocked(m); err != nil {
			vm.Close()
			ready <- fmt.Errorf("ramune: module %s: %w", m.Name, err)
			return
		}
	}

	ready <- nil

	// Main dispatch loop.
	for {
		select {
		case fn := <-r.callCh:
			fn()
		case <-r.stopCh:
			return
		}
	}
}

// dispatch executes a function on the dedicated QuickJS goroutine.
func (r *Runtime) dispatch(fn func()) {
	if r.closed.Load() {
		return
	}
	// Re-entrance detection: if already on the QJS goroutine, run directly.
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

// Close releases all resources.
func (r *Runtime) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	r.closeOnce.Do(func() {
		// Stop managers.
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

		if r.vm != nil {
			r.vm.Close()
		}
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

// evalLocked evaluates JS on the engine goroutine.
func (r *Runtime) evalLocked(code string) (*Value, error) {
	result, err := r.vm.EvalValue(code, quickjs.EvalGlobal)
	if err != nil {
		return nil, &JSError{Message: err.Error()}
	}
	return r.wrapValue(result), nil
}

// evalScriptLocked evaluates JS and returns the raw quickjs.Value.
func (r *Runtime) evalScriptLocked(code, context string) (quickjs.Value, error) {
	result, err := r.vm.EvalValue(code, quickjs.EvalGlobal)
	if err != nil {
		return quickjs.Value{}, &JSError{Context: context, Message: err.Error()}
	}
	return result, nil
}

// execLocked evaluates JS, discards result.
func (r *Runtime) execLocked(code string) error {
	result, err := r.vm.EvalValue(code, quickjs.EvalGlobal)
	if err != nil {
		return &JSError{Message: err.Error()}
	}
	result.Free()
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
	// Serialize the entire object to JSON and eval it — handles nested maps/slices.
	b, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("marshal props: %w", err)
	}
	result, err := r.vm.EvalValue("("+string(b)+")", quickjs.EvalGlobal)
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
		// Build array by serializing items to JSON and evaluating.
		code := "["
		for i, item := range items {
			if i > 0 {
				code += ","
			}
			switch v := item.(type) {
			case string:
				b, _ := json.Marshal(v)
				code += string(b)
			case bool:
				if v {
					code += "true"
				} else {
					code += "false"
				}
			case int:
				code += fmt.Sprintf("%d", v)
			case int64:
				code += fmt.Sprintf("%d", v)
			case float64:
				code += fmt.Sprintf("%v", v)
			case nil:
				code += "null"
			default:
				b, e := json.Marshal(v)
				if e != nil {
					code += "null"
				} else {
					code += string(b)
				}
			}
		}
		code += "]"
		result, e := r.vm.EvalValue(code, quickjs.EvalGlobal)
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
		// Build Uint8Array from JSON array of byte values.
		code := "new Uint8Array(["
		for i, b := range data {
			if i > 0 {
				code += ","
			}
			code += itoa(int(b))
		}
		code += "])"
		result, e := r.vm.EvalValue(code, quickjs.EvalGlobal)
		if e != nil {
			err = e
			return
		}
		val = r.wrapValue(result)
	})
	return val, err
}

// drainUnprotectQueue is a no-op for QuickJS (no protect/unprotect lifecycle).
func (r *Runtime) drainUnprotectQueue() {}
