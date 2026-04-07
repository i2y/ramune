package ramune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WithFetch installs a globalThis.fetch polyfill backed by Go's net/http.
// This is also automatically enabled when NodeCompat() is used.
func WithFetch() Option {
	return func(c *config) { c.withFetch = true }
}

// fetchManager tracks in-flight streaming fetch requests.
type fetchManager struct {
	mu       sync.Mutex
	requests map[int]*fetchRequest
	events   []fetchEvent
	nextID   int
	wakeFn   func()
}

type fetchRequest struct {
	id       int
	cancel   context.CancelFunc
	streamID int
}

type fetchEvent struct {
	ID         int               `json:"id"`
	Status     int               `json:"status,omitempty"`
	StatusText string            `json:"statusText,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	StreamID   int               `json:"streamId,omitempty"`
	Err        string            `json:"err,omitempty"`
	Redirected bool              `json:"redirected,omitempty"`
	URL        string            `json:"url,omitempty"`
}

func newFetchManager() *fetchManager {
	return &fetchManager{
		requests: make(map[int]*fetchRequest),
		nextID:   1,
	}
}

func (fm *fetchManager) processEvents(r *Runtime) {
	if fm == nil {
		return
	}
	fm.mu.Lock()
	if len(fm.events) == 0 {
		fm.mu.Unlock()
		return
	}
	events := fm.events
	fm.events = nil
	fm.mu.Unlock()

	data, _ := json.Marshal(events)
	r.execLocked("if(typeof __fetchDeliverHeaders==='function')__fetchDeliverHeaders(" + string(data) + ")")
}

func (fm *fetchManager) hasActive() bool {
	if fm == nil {
		return false
	}
	fm.mu.Lock()
	n := len(fm.requests)
	fm.mu.Unlock()
	return n > 0
}

func (fm *fetchManager) cancelRequest(id int) {
	fm.mu.Lock()
	req, ok := fm.requests[id]
	fm.mu.Unlock()
	if ok && req.cancel != nil {
		req.cancel()
	}
}

func (fm *fetchManager) removeRequest(id int) {
	fm.mu.Lock()
	delete(fm.requests, id)
	fm.mu.Unlock()
}

func (fm *fetchManager) closeAll() {
	if fm == nil {
		return
	}
	fm.mu.Lock()
	for id, req := range fm.requests {
		if req.cancel != nil {
			req.cancel()
		}
		delete(fm.requests, id)
	}
	fm.mu.Unlock()
}

// installFetch sets up the fetch polyfill. Must be called with rt.mu held.
func (r *Runtime) installFetch() error {
	r.fetchMgr = newFetchManager()
	r.fetchMgr.wakeFn = r.Wake

	// Legacy sync fetch for simple cases.
	if err := r.registerFuncLocked("__go_http_request", goHTTPRequest); err != nil {
		return err
	}

	// Streaming fetch: starts request in goroutine, delivers headers via events.
	if err := r.registerFuncLocked("__go_http_request_streaming", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("fetch: URL and options required")
		}
		url, _ := args[0].(string)
		optsRaw, _ := args[1].(string)

		var opts struct {
			Method   string            `json:"method"`
			Headers  map[string]string `json:"headers"`
			Body     string            `json:"body"`
			Redirect string            `json:"redirect"`
			Timeout  float64           `json:"timeout"`
		}
		if optsRaw != "" {
			json.Unmarshal([]byte(optsRaw), &opts)
		}
		if opts.Method == "" {
			opts.Method = "GET"
		}
		if opts.Redirect == "" {
			opts.Redirect = "follow"
		}
		timeout := 30 * time.Second
		if opts.Timeout > 0 {
			timeout = time.Duration(opts.Timeout) * time.Millisecond
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		fm := r.fetchMgr
		fm.mu.Lock()
		reqID := fm.nextID
		fm.nextID++
		fr := &fetchRequest{id: reqID, cancel: cancel}
		fm.requests[reqID] = fr
		fm.mu.Unlock()

		go func() {
			defer cancel()

			var bodyReader io.Reader
			if opts.Body != "" {
				bodyReader = strings.NewReader(opts.Body)
			}

			req, err := http.NewRequestWithContext(ctx, opts.Method, url, bodyReader)
			if err != nil {
				fm.mu.Lock()
				fm.events = append(fm.events, fetchEvent{ID: reqID, Err: err.Error()})
				delete(fm.requests, reqID)
				fm.mu.Unlock()
				if fm.wakeFn != nil {
					fm.wakeFn()
				}
				return
			}
			for k, v := range opts.Headers {
				req.Header.Set(k, v)
			}

			client := &http.Client{}

			// Redirect handling.
			redirected := false
			finalURL := url
			switch opts.Redirect {
			case "manual":
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				}
			case "error":
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					return fmt.Errorf("redirect not allowed")
				}
			default: // "follow"
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					if len(via) >= 10 {
						return fmt.Errorf("too many redirects")
					}
					redirected = true
					finalURL = req.URL.String()
					return nil
				}
			}

			resp, err := client.Do(req)
			if err != nil {
				fm.mu.Lock()
				fm.events = append(fm.events, fetchEvent{ID: reqID, Err: err.Error()})
				delete(fm.requests, reqID)
				fm.mu.Unlock()
				if fm.wakeFn != nil {
					fm.wakeFn()
				}
				return
			}

			// createGoToJS spawns a reader goroutine that reads resp.Body
			// and closes it when done (EOF or cancel).
			actualStreamID := r.streamMgr.createGoToJS(resp.Body)

			fm.mu.Lock()
			fr.streamID = actualStreamID
			fm.mu.Unlock()

			respHeaders := make(map[string]string)
			for k := range resp.Header {
				respHeaders[strings.ToLower(k)] = resp.Header.Get(k)
			}

			fm.mu.Lock()
			fm.events = append(fm.events, fetchEvent{
				ID:         reqID,
				Status:     resp.StatusCode,
				StatusText: resp.Status,
				Headers:    respHeaders,
				StreamID:   actualStreamID,
				Redirected: redirected,
				URL:        finalURL,
			})
			fm.mu.Unlock()
			if fm.wakeFn != nil {
				fm.wakeFn()
			}

			// Clean up immediately — the go2js stream goroutine handles reading.
			fm.removeRequest(reqID)
		}()

		return float64(reqID), nil
	}); err != nil {
		return err
	}

	// Abort a streaming fetch request.
	if err := r.registerFuncLocked("__go_fetch_abort", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("fetch_abort: id required")
		}
		id := int(args[0].(float64))
		r.fetchMgr.cancelRequest(id)
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(fetchJSSource())
}

// goHTTPRequest performs a synchronous HTTP request (legacy path).
func goHTTPRequest(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("fetch: URL required")
	}
	url, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("fetch: URL must be string")
	}

	method := "GET"
	var body string
	headers := make(map[string]string)

	if len(args) > 1 {
		if optsRaw, ok := args[1].(string); ok && optsRaw != "" {
			var opts struct {
				Method  string            `json:"method"`
				Headers map[string]string `json:"headers"`
				Body    string            `json:"body"`
			}
			json.Unmarshal([]byte(optsRaw), &opts)
			if opts.Method != "" {
				method = opts.Method
			}
			if opts.Headers != nil {
				headers = opts.Headers
			}
			body = opts.Body
		}
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[strings.ToLower(k)] = resp.Header.Get(k)
	}

	result := map[string]any{
		"status":     resp.StatusCode,
		"statusText": resp.Status,
		"headers":    respHeaders,
		"body":       string(respBody),
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func fetchJSSource() string {
	return `
(function() {
	if (typeof globalThis.fetch !== 'undefined') return;

	// --- AbortController / AbortSignal polyfill ---
	if (typeof globalThis.AbortController === 'undefined') {
		function AbortSignal() {
			this.aborted = false;
			this.reason = undefined;
			this._listeners = [];
		}
		AbortSignal.prototype.addEventListener = function(type, fn) {
			if (type === 'abort') this._listeners.push(fn);
		};
		AbortSignal.prototype.removeEventListener = function(type, fn) {
			if (type === 'abort') this._listeners = this._listeners.filter(function(f) { return f !== fn; });
		};
		AbortSignal.prototype.throwIfAborted = function() {
			if (this.aborted) throw this.reason;
		};
		AbortSignal.timeout = function(ms) {
			var signal = new AbortSignal();
			setTimeout(function() {
				signal.aborted = true;
				signal.reason = new Error('TimeoutError: The operation timed out.');
				for (var i = 0; i < signal._listeners.length; i++) {
					try { signal._listeners[i](); } catch(e) {}
				}
			}, ms);
			return signal;
		};
		AbortSignal.abort = function(reason) {
			var signal = new AbortSignal();
			signal.aborted = true;
			signal.reason = reason || new Error('AbortError: The operation was aborted.');
			return signal;
		};

		function AbortController() {
			this.signal = new AbortSignal();
		}
		AbortController.prototype.abort = function(reason) {
			var sig = this.signal;
			if (sig.aborted) return;
			sig.aborted = true;
			sig.reason = reason || new Error('AbortError: The operation was aborted.');
			for (var i = 0; i < sig._listeners.length; i++) {
				try { sig._listeners[i](); } catch(e) {}
			}
		};

		globalThis.AbortController = AbortController;
		globalThis.AbortSignal = AbortSignal;
	}

	// --- DOMException polyfill ---
	if (typeof globalThis.DOMException === 'undefined') {
		globalThis.DOMException = function(message, name) {
			this.message = message || '';
			this.name = name || 'Error';
		};
		DOMException.prototype = Object.create(Error.prototype);
		DOMException.prototype.constructor = DOMException;
	}

	// Pending streaming fetch requests: reqId -> {resolve, reject}
	var __fetchPending = {};

	// Deliver headers from Go for streaming fetch.
	globalThis.__fetchDeliverHeaders = function(events) {
		for (var i = 0; i < events.length; i++) {
			var ev = events[i];
			var pending = __fetchPending[ev.id];
			if (!pending) continue;
			delete __fetchPending[ev.id];

			if (ev.err) {
				pending.reject(new TypeError(ev.err));
				continue;
			}

			// Create Response with ReadableStream body backed by go2js stream.
			var bodyStream = __streamCreateReadable(ev.streamId);
			var resp;
			if (typeof Response === 'function') {
				resp = new Response(bodyStream, {
					status: ev.status,
					headers: ev.headers || {}
				});
			} else {
				// Lightweight response when BunCompat is not loaded.
				var _headers = ev.headers || {};
				resp = {
					ok: ev.status >= 200 && ev.status < 300,
					status: ev.status,
					statusText: ev.statusText || '',
					headers: {
						get: function(name) { return _headers[name.toLowerCase()] || null; },
						has: function(name) { return name.toLowerCase() in _headers; },
						forEach: function(cb) { for (var k in _headers) cb(_headers[k], k); }
					},
					body: bodyStream,
					bodyUsed: false,
					_stream: bodyStream,
					_body: null,
					text: function() {
						if (this.bodyUsed) return Promise.reject(new TypeError('Body already consumed'));
						this.bodyUsed = true;
						var reader = bodyStream.getReader();
						var chunks = [];
						function pump() {
							return reader.read().then(function(r) {
								if (r.done) return chunks.join('');
								var v = r.value;
								if (typeof v === 'string') { chunks.push(v); }
								else if (v instanceof Uint8Array) {
									var s = '';
									for (var j = 0; j < v.length; j++) s += String.fromCharCode(v[j]);
									chunks.push(s);
								} else { chunks.push(String(v)); }
								return pump();
							});
						}
						return pump();
					},
					json: function() {
						return this.text().then(function(t) { return JSON.parse(t); });
					},
					arrayBuffer: function() {
						return this.text().then(function(t) { return new TextEncoder().encode(t).buffer; });
					}
				};
			}
			resp.url = ev.url || '';
			resp.redirected = !!ev.redirected;
			pending.resolve(resp);
		}
	};

	globalThis.fetch = function(url, options) {
		options = options || {};

		// Check for AbortSignal.
		var signal = options.signal;
		if (signal && signal.aborted) {
			return Promise.reject(new DOMException(signal.reason || 'The operation was aborted.', 'AbortError'));
		}

		var h = options.headers || {};
		if (h && typeof h.forEach === 'function') {
			var plain = {}; h.forEach(function(v, k) { plain[k] = v; }); h = plain;
		}

		var bodyStr = '';
		if (options.body != null) {
			bodyStr = typeof options.body === 'string' ? options.body : String(options.body);
		}

		var optsJSON = JSON.stringify({
			method: options.method || 'GET',
			headers: h,
			body: bodyStr,
			redirect: options.redirect || 'follow',
			timeout: options.timeout || 0
		});

		var reqId = __go_http_request_streaming(String(url), optsJSON);

		return new Promise(function(resolve, reject) {
			__fetchPending[reqId] = { resolve: resolve, reject: reject };

			// Wire up abort signal.
			if (signal) {
				signal.addEventListener('abort', function() {
					__go_fetch_abort(reqId);
					var pending = __fetchPending[reqId];
					if (pending) {
						delete __fetchPending[reqId];
						pending.reject(new DOMException(signal.reason || 'The operation was aborted.', 'AbortError'));
					}
				});
			}
		});
	};
})();
`
}
