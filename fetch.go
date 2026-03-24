package ramune

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WithFetch installs a globalThis.fetch polyfill backed by Go's net/http.
// This is also automatically enabled when NodeCompat() is used.
func WithFetch() Option {
	return func(c *config) { c.withFetch = true }
}

// installFetch sets up the fetch polyfill. Must be called with rt.mu held.
func (r *Runtime) installFetch() error {
	if err := r.registerFuncLocked("__go_http_request", goHTTPRequest); err != nil {
		return err
	}
	return r.execLocked(fetchJSSource())
}

// goHTTPRequest performs an HTTP request.
// args: [url, optionsJSON]
// optionsJSON: {"method":"GET","headers":{},"body":""}
// Returns JSON: {"status":200,"statusText":"OK","headers":{},"body":"..."}
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

	globalThis.fetch = function(url, options) {
		try {
			var optsJSON = '';
			if (options) {
				var h = options.headers || {};
				if (h && typeof h.forEach === 'function') {
					var plain = {}; h.forEach(function(v, k) { plain[k] = v; }); h = plain;
				}
				optsJSON = JSON.stringify({
					method: options.method || 'GET',
					headers: h,
					body: options.body || ''
				});
			}
			var raw = __go_http_request(String(url), optsJSON);
			var resp = JSON.parse(raw);
			if (typeof Response === 'function') {
				return Promise.resolve(new Response(resp.body || '', {
					status: resp.status,
					statusText: resp.statusText,
					headers: resp.headers || {}
				}));
			}
			var _body = resp.body;
			var _headers = resp.headers || {};
			return Promise.resolve({
				ok: resp.status >= 200 && resp.status < 300,
				status: resp.status,
				statusText: resp.statusText,
				headers: {
					get: function(name) { return _headers[name.toLowerCase()] || null; },
					has: function(name) { return name.toLowerCase() in _headers; }
				},
				json: function() {
					try { return Promise.resolve(JSON.parse(_body)); }
					catch(e) { return Promise.reject(e); }
				},
				text: function() { return Promise.resolve(_body); },
				arrayBuffer: function() { return Promise.resolve(_body); }
			});
		} catch(e) {
			return Promise.reject(e);
		}
	};
})();
`
}
