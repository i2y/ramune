package ramune

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// workerManager manages worker threads for a Runtime.
type workerManager struct {
	mu      sync.Mutex
	workers map[int]*worker
	nextID  int
	parent  *Runtime
	wakeFn  func()
}

type worker struct {
	id          int
	rt          *Runtime
	msgToWorker chan string // parent → worker
	msgToParent chan string // worker → parent
	done        chan struct{}
}

func newWorkerManager(parent *Runtime) *workerManager {
	return &workerManager{
		workers: make(map[int]*worker),
		nextID:  1,
		parent:  parent,
		wakeFn:  parent.Wake,
	}
}

// installWorkerThreads sets up the worker_threads module.
func (r *Runtime) installWorkerThreads() error {
	r.workerMgr = newWorkerManager(r)

	if err := r.registerFuncLocked("__go_worker_create", goWorkerCreate(r.workerMgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_worker_post", goWorkerPost(r.workerMgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_worker_drain", goWorkerDrain(r.workerMgr)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_worker_terminate", goWorkerTerminate(r.workerMgr)); err != nil {
		return err
	}

	return r.execLocked(workerThreadsJSSource())
}

func goWorkerCreate(wm *workerManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("Worker: filename required")
		}
		filename, _ := args[0].(string)
		var workerData string
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				workerData = s
			}
		}

		// Read the worker script.
		code, err := os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("Worker: %w", err)
		}

		wm.mu.Lock()
		id := wm.nextID
		wm.nextID++
		w := &worker{
			id:          id,
			msgToWorker: make(chan string, 100),
			msgToParent: make(chan string, 100),
			done:        make(chan struct{}),
		}
		wm.workers[id] = w
		wm.mu.Unlock()

		// Create and run the worker Runtime in a goroutine to avoid
		// holding the parent's JSC lock during New() (which acquires
		// the global JSC mutex).
		go func() {
			defer close(w.done)

			rt, err := newInternal(NodeCompat(), WithFetch())
			if err != nil {
				w.msgToParent <- fmt.Sprintf(`{"__error":%q}`, err.Error())
				return
			}
			defer rt.Close()
			w.rt = rt

			rt.RegisterFunc("__go_parent_post", func(args []any) (any, error) {
				if len(args) < 1 {
					return nil, nil
				}
				msg, _ := args[0].(string)
				select {
				case w.msgToParent <- msg:
				default:
				}
				// Wake the parent's event loop to deliver the message.
				if wm.wakeFn != nil {
					wm.wakeFn()
				}
				return nil, nil
			})

			rt.RegisterFunc("__go_parent_drain", func(args []any) (any, error) {
				var msgs []string
				for {
					select {
					case msg := <-w.msgToWorker:
						msgs = append(msgs, msg)
					default:
						out, _ := json.Marshal(msgs)
						return string(out), nil
					}
				}
			})

			rt.Exec(fmt.Sprintf(`
				globalThis.workerData = %s;
				globalThis.isMainThread = false;
				globalThis.parentPort = {
					postMessage: function(msg) {
						__go_parent_post(JSON.stringify(msg, globalThis.__sabReplacer));
					},
					on: function(event, cb) {
						if (event === 'message') {
							setInterval(function() {
								var msgs = JSON.parse(__go_parent_drain());
								for (var i = 0; i < msgs.length; i++) {
									cb(JSON.parse(msgs[i], globalThis.__sabReviver));
								}
							}, 10);
						}
						return this;
					}
				};
				var wt = require('worker_threads');
				wt.parentPort = globalThis.parentPort;
				wt.workerData = globalThis.workerData;
				wt.isMainThread = false;
			`, workerData))

			if err := rt.Exec(string(code)); err != nil {
				w.msgToParent <- fmt.Sprintf(`{"__error":%q}`, err.Error())
				return
			}
			rt.RunEventLoop()
		}()

		return float64(id), nil
	}
}

func goWorkerPost(wm *workerManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, nil
		}
		id, _ := args[0].(float64)
		msg, _ := args[1].(string)

		wm.mu.Lock()
		w, ok := wm.workers[int(id)]
		wm.mu.Unlock()
		if !ok {
			return nil, nil
		}

		select {
		case w.msgToWorker <- msg:
		default:
		}
		return nil, nil
	}
}

func goWorkerDrain(wm *workerManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return "[]", nil
		}
		id, _ := args[0].(float64)

		wm.mu.Lock()
		w, ok := wm.workers[int(id)]
		wm.mu.Unlock()
		if !ok {
			return "[]", nil
		}

		var msgs []string
		for {
			select {
			case msg := <-w.msgToParent:
				msgs = append(msgs, msg)
			default:
				out, _ := json.Marshal(msgs)
				return string(out), nil
			}
		}
	}
}

func goWorkerTerminate(wm *workerManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, nil
		}
		id, _ := args[0].(float64)

		wm.mu.Lock()
		w, ok := wm.workers[int(id)]
		if ok {
			delete(wm.workers, int(id))
		}
		wm.mu.Unlock()

		if ok {
			w.rt.Close()
		}
		return nil, nil
	}
}

// hasActive returns true if any workers are running.
func (wm *workerManager) hasActive() bool {
	if wm == nil {
		return false
	}
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return len(wm.workers) > 0
}

// processEvents drains messages from all workers and delivers them to JS.
func (wm *workerManager) processEvents(r *Runtime) {
	if wm == nil {
		return
	}
	wm.mu.Lock()
	if len(wm.workers) == 0 {
		wm.mu.Unlock()
		return
	}
	type workerMsgs struct {
		id   int
		msgs []string
	}
	var all []workerMsgs
	for id, w := range wm.workers {
		var msgs []string
		for {
			select {
			case msg := <-w.msgToParent:
				msgs = append(msgs, msg)
			default:
				goto done
			}
		}
	done:
		if len(msgs) > 0 {
			all = append(all, workerMsgs{id, msgs})
		}
	}
	wm.mu.Unlock()

	if len(all) == 0 {
		return
	}

	evMap := make(map[string][]string, len(all))
	for _, wm := range all {
		evMap[itoa(wm.id)] = wm.msgs
	}
	data, _ := json.Marshal(evMap)
	r.execLocked("if(typeof __workerDeliverEvents==='function')__workerDeliverEvents(" + string(data) + ")")
}

func workerThreadsJSSource() string {
	return strings.TrimSpace(`
(function() {
	var EventEmitter = globalThis.require('events').EventEmitter;

	class Worker extends EventEmitter {
		constructor(filename, opts) {
			super();
			var self = this;
			opts = opts || {};
			var workerDataJSON = opts.workerData !== undefined ? JSON.stringify(opts.workerData) : 'null';

			try {
				self.threadId = __go_worker_create(filename, workerDataJSON);
			} catch(e) {
				setImmediate(function() { self.emit('error', e); });
				return;
			}

			// Register in worker registry for event delivery by Go.
			__activeWorkers[String(self.threadId)] = self;
		}
		postMessage(msg) {
			__go_worker_post(this.threadId, JSON.stringify(msg, __sabReplacer));
		}
		terminate() {
			delete __activeWorkers[String(this.threadId)];
			__go_worker_terminate(this.threadId);
			this.emit('exit', 0);
		}
	}

	// Structured clone helpers for SharedArrayBuffer transfer via JSON.
	function __sabReplacer(key, value) {
		if (value && typeof value === 'object' && value._sabId !== undefined && value.byteLength !== undefined) {
			return { __sab: true, _sabId: value._sabId, byteLength: value.byteLength };
		}
		return value;
	}
	function __sabReviver(key, value) {
		if (value && typeof value === 'object' && value.__sab === true) {
			var sab = Object.create(globalThis.SharedArrayBuffer.prototype);
			sab._sabId = value._sabId;
			sab.byteLength = value.byteLength;
			return sab;
		}
		return value;
	}
	globalThis.__sabReplacer = __sabReplacer;
	globalThis.__sabReviver = __sabReviver;

	// Registry of active workers for event delivery by Go.
	var __activeWorkers = {};

	// Called by Go during event loop tick to deliver worker messages.
	globalThis.__workerDeliverEvents = function(msgsMap) {
		var ids = Object.keys(msgsMap);
		for (var w = 0; w < ids.length; w++) {
			var id = ids[w];
			var worker = __activeWorkers[id];
			if (!worker) continue;
			var msgs = msgsMap[id];
			for (var i = 0; i < msgs.length; i++) {
				var parsed = JSON.parse(msgs[i], __sabReviver);
				if (parsed && parsed.__error) {
					worker.emit('error', new Error(parsed.__error));
					worker.emit('exit', 1);
					delete __activeWorkers[id];
					return;
				}
				worker.emit('message', parsed);
			}
		}
	};

	var workerThreads = {
		Worker: Worker,
		isMainThread: globalThis.isMainThread !== false,
		parentPort: globalThis.parentPort || null,
		workerData: globalThis.workerData || null
	};

	// Register module.
	var _modules = globalThis.require._modules || {};
	_modules['worker_threads'] = workerThreads;
	if (!globalThis.require._modules) {
		var origReq = globalThis.require;
		globalThis.require = function(mod) {
			if (mod === 'worker_threads' || mod === 'node:worker_threads') return workerThreads;
			return origReq(mod);
		};
		globalThis.require.resolve = origReq.resolve;
		globalThis.require._modules = _modules;
	}
})();
`)
}
