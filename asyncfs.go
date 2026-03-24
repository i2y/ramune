package ramune

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// fsManager handles asynchronous filesystem operations.
type fsManager struct {
	mu     sync.Mutex
	events []fsEvent
	wakeFn func()
}

type fsEvent struct {
	ID   int    `json:"id"`
	Kind string `json:"kind"` // "readFile", "writeFile", "stat", "readdir"
	Data string `json:"data"` // result data or error message
	Err  bool   `json:"err"`  // true if error
}

func newFSManager() *fsManager {
	return &fsManager{}
}

func (fm *fsManager) readFile(id int, path string) {
	go func() {
		data, err := os.ReadFile(path)
		fm.mu.Lock()
		if err != nil {
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "readFile", Err: true, Data: err.Error()})
		} else {
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "readFile", Data: string(data)})
		}
		fm.mu.Unlock()
		if fm.wakeFn != nil {
			fm.wakeFn()
		}
	}()
}

func (fm *fsManager) writeFile(id int, path, content string) {
	go func() {
		err := os.WriteFile(path, []byte(content), 0644)
		fm.mu.Lock()
		if err != nil {
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "writeFile", Err: true, Data: err.Error()})
		} else {
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "writeFile"})
		}
		fm.mu.Unlock()
		if fm.wakeFn != nil {
			fm.wakeFn()
		}
	}()
}

func (fm *fsManager) stat(id int, path string) {
	go func() {
		info, err := os.Stat(path)
		fm.mu.Lock()
		if err != nil {
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "stat", Err: true, Data: err.Error()})
		} else {
			s := map[string]any{
				"isFile":      !info.IsDir(),
				"isDirectory": info.IsDir(),
				"size":        info.Size(),
				"mtimeMs":     float64(info.ModTime().UnixMilli()),
			}
			data, _ := json.Marshal(s)
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "stat", Data: string(data)})
		}
		fm.mu.Unlock()
		if fm.wakeFn != nil {
			fm.wakeFn()
		}
	}()
}

func (fm *fsManager) readdir(id int, path string) {
	go func() {
		entries, err := os.ReadDir(path)
		fm.mu.Lock()
		if err != nil {
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "readdir", Err: true, Data: err.Error()})
		} else {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			data, _ := json.Marshal(names)
			fm.events = append(fm.events, fsEvent{ID: id, Kind: "readdir", Data: string(data)})
		}
		fm.mu.Unlock()
		if fm.wakeFn != nil {
			fm.wakeFn()
		}
	}()
}

func (fm *fsManager) processEvents(r *Runtime) {
	fm.mu.Lock()
	events := fm.events
	fm.events = nil
	fm.mu.Unlock()

	if len(events) == 0 {
		return
	}

	data, _ := json.Marshal(events)
	r.execLocked(fmt.Sprintf(`
		if (typeof __fsDeliverEvents === 'function') {
			__fsDeliverEvents(%q);
		}
	`, string(data)))
}

func (fm *fsManager) hasPending() bool {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return len(fm.events) > 0
}

// installAsyncFS sets up the async fs Go callbacks and JS delivery function.
func (r *Runtime) installAsyncFS() error {
	fm := newFSManager()
	fm.wakeFn = r.Wake
	r.fsMgr = fm

	if err := r.registerFuncLocked("__go_async_fs_read", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("async readFile: id and path required")
		}
		id := int(args[0].(float64))
		path, _ := args[1].(string)
		fm.readFile(id, path)
		return nil, nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_fs_write", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("async writeFile: id, path, and data required")
		}
		id := int(args[0].(float64))
		path, _ := args[1].(string)
		data, _ := args[2].(string)
		fm.writeFile(id, path, data)
		return nil, nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_fs_stat", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("async stat: id and path required")
		}
		id := int(args[0].(float64))
		path, _ := args[1].(string)
		fm.stat(id, path)
		return nil, nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_async_fs_readdir", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("async readdir: id and path required")
		}
		id := int(args[0].(float64))
		path, _ := args[1].(string)
		fm.readdir(id, path)
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(asyncFSJSSource())
}

func asyncFSJSSource() string {
	return `
(function() {
	var _fsCallbacks = {};
	var _fsNextID = 1;
	var _fsPendingCount = 0;

	globalThis.__fsPendingCount = function() { return _fsPendingCount; };

	globalThis.__fsDeliverEvents = function(eventsJSON) {
		var events = JSON.parse(eventsJSON);
		for (var i = 0; i < events.length; i++) {
			var ev = events[i];
			var cb = _fsCallbacks[ev.id];
			if (!cb) continue;
			delete _fsCallbacks[ev.id];
			_fsPendingCount--;
			if (ev.err) {
				var err = new Error(ev.data);
				err.code = ev.data.indexOf('no such file') >= 0 ? 'ENOENT' :
				           ev.data.indexOf('permission denied') >= 0 ? 'EACCES' : 'ERR';
				cb.reject(err);
			} else {
				cb.resolve(ev.data);
			}
		}
	};

	// Patch fs module to add real async methods.
	var fs = globalThis.require._modules && globalThis.require._modules['fs'];
	if (!fs) return;

	function fsAsync(goFn, id, args, resolveFn, rejectFn, cb) {
		_fsPendingCount++;
		_fsCallbacks[id] = { resolve: function(d) { if (cb) resolveFn(d, cb); else resolveFn(d); }, reject: function(e) { if (cb) rejectFn(e, cb); else rejectFn(e); } };
		goFn.apply(null, [id].concat(args));
	}

	fs.readFile = function(path, opts, cb) {
		if (typeof opts === 'function') { cb = opts; opts = null; }
		var isText = opts === 'utf8' || opts === 'utf-8' || (opts && opts.encoding);
		var id = _fsNextID++;
		if (cb) {
			fsAsync(__go_async_fs_read, id, [String(path)],
				function(data) { cb(null, isText ? data : globalThis.Buffer.from(data)); },
				function(err) { cb(err); });
		} else {
			var p = {};
			var promise = new Promise(function(res, rej) { p.resolve = res; p.reject = rej; });
			fsAsync(__go_async_fs_read, id, [String(path)],
				function(data) { p.resolve(isText ? data : globalThis.Buffer.from(data)); },
				function(err) { p.reject(err); });
			return promise;
		}
	};

	fs.writeFile = function(path, data, opts, cb) {
		if (typeof opts === 'function') { cb = opts; opts = null; }
		var id = _fsNextID++;
		if (cb) {
			fsAsync(__go_async_fs_write, id, [String(path), String(data)],
				function() { cb(null); }, function(err) { cb(err); });
		} else {
			var p = {};
			var promise = new Promise(function(res, rej) { p.resolve = res; p.reject = rej; });
			fsAsync(__go_async_fs_write, id, [String(path), String(data)],
				function() { p.resolve(); }, function(err) { p.reject(err); });
			return promise;
		}
	};

	fs.stat = function(path, cb) {
		var id = _fsNextID++;
		function parse(raw) {
			var s = JSON.parse(raw);
			return { isFile: function() { return s.isFile; }, isDirectory: function() { return s.isDirectory; }, size: s.size, mtimeMs: s.mtimeMs, mtime: new Date(s.mtimeMs) };
		}
		if (cb) {
			fsAsync(__go_async_fs_stat, id, [String(path)],
				function(data) { cb(null, parse(data)); }, function(err) { cb(err); });
		} else {
			var p = {};
			var promise = new Promise(function(res, rej) { p.resolve = res; p.reject = rej; });
			fsAsync(__go_async_fs_stat, id, [String(path)],
				function(data) { p.resolve(parse(data)); }, function(err) { p.reject(err); });
			return promise;
		}
	};

	fs.readdir = function(path, opts, cb) {
		if (typeof opts === 'function') { cb = opts; opts = null; }
		var id = _fsNextID++;
		if (cb) {
			fsAsync(__go_async_fs_readdir, id, [String(path)],
				function(data) { cb(null, JSON.parse(data)); }, function(err) { cb(err); });
		} else {
			var p = {};
			var promise = new Promise(function(res, rej) { p.resolve = res; p.reject = rej; });
			fsAsync(__go_async_fs_readdir, id, [String(path)],
				function(data) { p.resolve(JSON.parse(data)); }, function(err) { p.reject(err); });
			return promise;
		}
	};

	// Override fs.promises to use the async versions.
	fs.promises.readFile = function(path, opts) { return fs.readFile(path, opts); };
	fs.promises.writeFile = function(path, data, opts) { return fs.writeFile(path, data, opts); };
	fs.promises.stat = function(path) { return fs.stat(path); };
	fs.promises.readdir = function(path, opts) { return fs.readdir(path, opts); };
})();
`
}
