package ramune

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/crgimenes/glaze"
)

// webViewMainCh receives functions to execute on the main OS thread.
// macOS requires all UI/WebKit operations on thread 0.
// Only one WebView window can be active at a time (wv.Run blocks the main thread).
var webViewMainCh chan func()

// InitWebViewMain enables WebView support by creating the main-thread
// dispatch channel. Must be called before any WebView is created.
// The caller must drain the channel on the main goroutine, e.g.:
//
//	ramune.InitWebViewMain()
//	go func() { /* run engine */ }()
//	ramune.DrainWebViewMain(done)
func InitWebViewMain() {
	webViewMainCh = make(chan func(), 4)
}

// DrainWebViewMain processes WebView operations on the main thread.
// Blocks until done is closed or all work is complete.
// Must be called from the main goroutine (pinned to thread 0 via
// runtime.LockOSThread in init).
func DrainWebViewMain(done <-chan struct{}) {
	for {
		select {
		case fn, ok := <-webViewMainCh:
			if ok {
				fn()
			}
		case <-done:
			return
		}
	}
}

type webviewInstance struct {
	wv     glaze.WebView
	id     int
	closed bool
}

type webviewManager struct {
	mu     sync.Mutex
	views  map[int]*webviewInstance
	events []webviewEvent
	nextID int
	wakeFn func()
}

type webviewEvent struct {
	Kind string `json:"Kind"`
	ID   int    `json:"ID"`
}

func newWebviewManager(wakeFn func()) *webviewManager {
	return &webviewManager{
		views:  make(map[int]*webviewInstance),
		wakeFn: wakeFn,
	}
}

var errWebViewNotEnabled = errors.New("WebView requires InitWebViewMain() and DrainWebViewMain() on the main goroutine")

func (m *webviewManager) create(opts webviewCreateOpts) (int, error) {
	if webViewMainCh == nil {
		return 0, errWebViewNotEnabled
	}

	m.mu.Lock()
	id := m.nextID
	m.nextID++
	inst := &webviewInstance{id: id}
	m.views[id] = inst
	m.mu.Unlock()

	ready := make(chan error, 1)

	// WebView must be created on the main OS thread (macOS requirement).
	select {
	case webViewMainCh <- func() {
		wv, err := glaze.New(opts.Debug)
		if err != nil {
			ready <- err
			return
		}

		inst.wv = wv

		if opts.Title != "" {
			wv.SetTitle(opts.Title)
		}
		if opts.Width > 0 && opts.Height > 0 {
			wv.SetSize(opts.Width, opts.Height, glaze.HintNone)
		}
		if opts.HTML != "" {
			wv.SetHtml(opts.HTML)
		} else if opts.URL != "" {
			wv.Navigate(opts.URL)
		}

		ready <- nil

		wv.Run()

		m.mu.Lock()
		inst.closed = true
		delete(m.views, id)
		m.events = append(m.events, webviewEvent{Kind: "close", ID: id})
		m.mu.Unlock()

		wv.Destroy()

		if m.wakeFn != nil {
			m.wakeFn()
		}
	}:
	default:
		m.mu.Lock()
		delete(m.views, id)
		m.mu.Unlock()
		return 0, errWebViewNotEnabled
	}

	if err := <-ready; err != nil {
		m.mu.Lock()
		delete(m.views, id)
		m.mu.Unlock()
		return 0, err
	}

	return id, nil
}

func (m *webviewManager) dispatch(id int, fn func(glaze.WebView)) error {
	m.mu.Lock()
	inst, ok := m.views[id]
	m.mu.Unlock()
	if !ok || inst.wv == nil || inst.closed {
		return fmt.Errorf("webview: instance %d not found or closed", id)
	}
	inst.wv.Dispatch(func() { fn(inst.wv) })
	return nil
}

func (m *webviewManager) destroy(id int) error {
	return m.dispatch(id, func(wv glaze.WebView) {
		wv.Terminate()
	})
}

func (m *webviewManager) processEvents(r *Runtime) {
	m.mu.Lock()
	if len(m.events) == 0 {
		m.mu.Unlock()
		return
	}
	events := m.events
	m.events = nil
	m.mu.Unlock()

	data, _ := json.Marshal(events)
	r.execLocked("if(typeof __webviewDeliverEvents==='function')__webviewDeliverEvents(" + string(data) + ")")
}

func (m *webviewManager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.views) > 0
}

func (m *webviewManager) closeAll() {
	m.mu.Lock()
	for id, inst := range m.views {
		if inst.wv != nil && !inst.closed {
			inst.wv.Dispatch(func() { inst.wv.Terminate() })
		}
		delete(m.views, id)
	}
	m.mu.Unlock()
}

type webviewCreateOpts struct {
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	URL    string `json:"url"`
	HTML   string `json:"html"`
	Debug  bool   `json:"debug"`
}

func (r *Runtime) installWebView() error {
	mgr := newWebviewManager(r.Wake)
	r.webviewMgr = mgr

	if err := r.registerFuncLocked("__go_webview_create", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("WebView: options required")
		}
		optsJSON, _ := args[0].(string)
		var opts webviewCreateOpts
		if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
			return nil, err
		}
		id, err := mgr.create(opts)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_navigate", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("WebView.navigate: id and url required")
		}
		id := int(args[0].(float64))
		url, _ := args[1].(string)
		return nil, mgr.dispatch(id, func(wv glaze.WebView) { wv.Navigate(url) })
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_eval", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("WebView.eval: id and js required")
		}
		id := int(args[0].(float64))
		js, _ := args[1].(string)
		return nil, mgr.dispatch(id, func(wv glaze.WebView) { wv.Eval(js) })
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_set_title", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("WebView.setTitle: id and title required")
		}
		id := int(args[0].(float64))
		title, _ := args[1].(string)
		return nil, mgr.dispatch(id, func(wv glaze.WebView) { wv.SetTitle(title) })
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_set_size", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("WebView.setSize: id, width, height required")
		}
		id := int(args[0].(float64))
		w := int(args[1].(float64))
		h := int(args[2].(float64))
		return nil, mgr.dispatch(id, func(wv glaze.WebView) { wv.SetSize(w, h, glaze.HintNone) })
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_set_html", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("WebView.setHtml: id and html required")
		}
		id := int(args[0].(float64))
		html, _ := args[1].(string)
		return nil, mgr.dispatch(id, func(wv glaze.WebView) { wv.SetHtml(html) })
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_init", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("WebView.init: id and js required")
		}
		id := int(args[0].(float64))
		js, _ := args[1].(string)
		return nil, mgr.dispatch(id, func(wv glaze.WebView) { wv.Init(js) })
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_webview_destroy", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("WebView.destroy: id required")
		}
		id := int(args[0].(float64))
		return nil, mgr.destroy(id)
	}); err != nil {
		return err
	}

	return r.execLocked(webviewJSSource())
}

func webviewJSSource() string {
	return `(function() {
	var __activeWebViews = {};

	function WebView(opts) {
		opts = opts || {};
		this._id = __go_webview_create(JSON.stringify(opts));
		this._closed = false;
		__activeWebViews[String(this._id)] = this;
		this._onclose = null;
	}

	WebView.prototype.navigate = function(url) {
		if (!this._closed) __go_webview_navigate(this._id, url);
		return this;
	};

	WebView.prototype.eval = function(js) {
		if (!this._closed) __go_webview_eval(this._id, js);
		return this;
	};

	WebView.prototype.setTitle = function(title) {
		if (!this._closed) __go_webview_set_title(this._id, title);
		return this;
	};

	WebView.prototype.setSize = function(w, h) {
		if (!this._closed) __go_webview_set_size(this._id, w, h);
		return this;
	};

	WebView.prototype.setHtml = function(html) {
		if (!this._closed) __go_webview_set_html(this._id, html);
		return this;
	};

	WebView.prototype.init = function(js) {
		if (!this._closed) __go_webview_init(this._id, js);
		return this;
	};

	WebView.prototype.destroy = function() {
		if (!this._closed) {
			this._closed = true;
			__go_webview_destroy(this._id);
			delete __activeWebViews[String(this._id)];
		}
	};

	WebView.prototype.onclose = function(fn) {
		this._onclose = fn;
		return this;
	};

	globalThis.__webviewDeliverEvents = function(events) {
		for (var i = 0; i < events.length; i++) {
			var ev = events[i];
			var wv = __activeWebViews[String(ev.ID)];
			if (!wv) continue;
			if (ev.Kind === 'close') {
				wv._closed = true;
				if (wv._onclose) wv._onclose();
				delete __activeWebViews[String(ev.ID)];
			}
		}
	};

	globalThis.Ramune.WebView = WebView;
})();`
}
