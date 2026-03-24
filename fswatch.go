package ramune

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// fsWatchManager manages file watchers for fs.watch().
type fsWatchManager struct {
	mu       sync.Mutex
	watchers map[int]*fsWatchEntry
	events   []fsWatchEvent
	nextID   int
	wakeFn   func()
}

type fsWatchEntry struct {
	watcher *fsnotify.Watcher
	id      int
	closed  bool
}

type fsWatchEvent struct {
	ID       int    `json:"id"`
	Event    string `json:"event"`    // "rename" or "change"
	Filename string `json:"filename"` // relative filename
}

func newFSWatchManager() *fsWatchManager {
	return &fsWatchManager{
		watchers: make(map[int]*fsWatchEntry),
		nextID:   1,
	}
}

func (wm *fsWatchManager) watch(path string) (int, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return 0, err
	}
	if err := w.Add(path); err != nil {
		w.Close()
		return 0, err
	}

	wm.mu.Lock()
	id := wm.nextID
	wm.nextID++
	entry := &fsWatchEntry{watcher: w, id: id}
	wm.watchers[id] = entry
	wm.mu.Unlock()

	go func() {
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				var eventType string
				if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					eventType = "rename"
				} else {
					eventType = "change"
				}
				filename := filepath.Base(event.Name)
				wm.mu.Lock()
				if !entry.closed {
					wm.events = append(wm.events, fsWatchEvent{ID: id, Event: eventType, Filename: filename})
				}
				wm.mu.Unlock()
				if wm.wakeFn != nil {
					wm.wakeFn()
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return id, nil
}

func (wm *fsWatchManager) close(id int) {
	wm.mu.Lock()
	entry, ok := wm.watchers[id]
	if ok {
		entry.closed = true
		delete(wm.watchers, id)
	}
	wm.mu.Unlock()
	if ok {
		entry.watcher.Close()
	}
}

func (wm *fsWatchManager) closeAll() {
	wm.mu.Lock()
	entries := make([]*fsWatchEntry, 0, len(wm.watchers))
	for _, e := range wm.watchers {
		entries = append(entries, e)
	}
	wm.watchers = make(map[int]*fsWatchEntry)
	wm.mu.Unlock()
	for _, e := range entries {
		e.closed = true
		e.watcher.Close()
	}
}

func (wm *fsWatchManager) processEvents(r *Runtime) {
	wm.mu.Lock()
	events := wm.events
	wm.events = nil
	wm.mu.Unlock()

	if len(events) == 0 {
		return
	}

	data, _ := json.Marshal(events)
	r.execLocked(fmt.Sprintf(`
		if (typeof __fsWatchDeliverEvents === 'function') {
			__fsWatchDeliverEvents(%q);
		}
	`, string(data)))
}

func (wm *fsWatchManager) hasActive() bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return len(wm.watchers) > 0
}

// installFSWatch sets up fs.watch() support.
func (r *Runtime) installFSWatch() error {
	wm := newFSWatchManager()
	wm.wakeFn = r.Wake
	r.fswatchMgr = wm

	if err := r.registerFuncLocked("__go_fs_watch", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("fs.watch: path required")
		}
		path, _ := args[0].(string)
		id, err := wm.watch(path)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_fs_watch_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("fs.watch close: id required")
		}
		id := int(args[0].(float64))
		wm.close(id)
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(fsWatchJSSource())
}

func fsWatchJSSource() string {
	return `
(function() {
	var _watchCallbacks = {};

	globalThis.__fsWatchDeliverEvents = function(eventsJSON) {
		var events = JSON.parse(eventsJSON);
		for (var i = 0; i < events.length; i++) {
			var ev = events[i];
			var cb = _watchCallbacks[ev.id];
			if (cb) cb(ev.event, ev.filename);
		}
	};

	var fs = globalThis.require._modules && globalThis.require._modules['fs'];
	if (!fs) return;

	fs.watch = function(path, opts, listener) {
		if (typeof opts === 'function') { listener = opts; opts = {}; }
		opts = opts || {};
		var id = __go_fs_watch(String(path));
		if (listener) _watchCallbacks[id] = listener;

		var watcher = new (require('events').EventEmitter || function(){})();
		watcher.close = function() {
			__go_fs_watch_close(id);
			delete _watchCallbacks[id];
			watcher.emit('close');
		};
		_watchCallbacks[id] = function(eventType, filename) {
			if (listener) listener(eventType, filename);
			watcher.emit('change', eventType, filename);
		};
		return watcher;
	};
})();
`
}
