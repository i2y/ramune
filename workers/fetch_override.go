package workers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/i2y/ramune"
)

// installPerWorkerFetch registers a __wk_fetch_impl Go callback and
// overrides globalThis.fetch for this Runtime to route through
// cfg.Fetch. Called from AttachPrepared when WithFetchFunc was used.
//
// The current implementation is synchronous: __wk_fetch_impl does not
// return until cfg.Fetch has finished. For the Firecracker guest use
// case (fetch tunneled over vsock, sub-ms host RTT) this is
// acceptable and considerably simpler than streaming plumbing. A
// future upgrade can mirror the streaming path in fetch.go.
func installPerWorkerFetch(rt *ramune.Runtime, fn func(*http.Request) (*http.Response, error)) error {
	if err := regFunc(rt, "__wk_fetch_impl", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("__wk_fetch_impl: url required")
		}
		url, _ := args[0].(string)
		optsRaw := ""
		if len(args) > 1 {
			optsRaw, _ = args[1].(string)
		}
		var opts struct {
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if optsRaw != "" {
			_ = json.Unmarshal([]byte(optsRaw), &opts)
		}
		method := opts.Method
		if method == "" {
			method = "GET"
		}
		var body io.Reader
		if opts.Body != "" {
			body = strings.NewReader(opts.Body)
		}
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, err
		}
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}
		resp, err := fn(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		respHeaders := make(map[string]string, len(resp.Header))
		for k := range resp.Header {
			respHeaders[strings.ToLower(k)] = resp.Header.Get(k)
		}
		out, err := json.Marshal(map[string]any{
			"status":     resp.StatusCode,
			"statusText": resp.Status,
			"headers":    respHeaders,
			"body":       string(respBody),
		})
		if err != nil {
			return nil, err
		}
		return string(out), nil
	}); err != nil {
		return err
	}
	return rt.Exec(perWorkerFetchJS)
}

// perWorkerFetchJS overrides globalThis.fetch with a thin wrapper that
// marshals (url, options) to JSON and delegates to __wk_fetch_impl. The
// returned Promise resolves with a Response (if BunCompat is present)
// or a minimal Response-shaped object otherwise.
//
// Installed by WithFetchFunc AFTER the module has been evaluated, so
// workers that captured a reference to the Ramune-default fetch are
// unaffected — only call sites that reach fetch via globalThis (the
// normal spelling) see the override.
const perWorkerFetchJS = `
(function() {
    globalThis.fetch = function(url, options) {
        options = options || {};
        var h = options.headers || {};
        if (h && typeof h.forEach === "function") {
            var plain = {};
            h.forEach(function(v, k) { plain[k] = v; });
            h = plain;
        }
        var bodyStr = "";
        if (options.body != null) {
            bodyStr = typeof options.body === "string" ? options.body : String(options.body);
        }
        var optsJSON = JSON.stringify({
            method: options.method || "GET",
            headers: h,
            body: bodyStr
        });
        return new Promise(function(resolve, reject) {
            var raw;
            try {
                raw = __wk_fetch_impl(String(url), optsJSON);
            } catch (e) {
                reject(e);
                return;
            }
            var ev;
            try { ev = JSON.parse(raw); }
            catch (e) { reject(new TypeError("workers: __wk_fetch_impl returned invalid JSON")); return; }

            var headers = ev.headers || {};
            if (typeof Response === "function") {
                resolve(new Response(ev.body, {
                    status: ev.status,
                    headers: headers
                }));
                return;
            }
            resolve({
                ok: ev.status >= 200 && ev.status < 300,
                status: ev.status,
                statusText: ev.statusText || "",
                headers: {
                    get: function(name) { return headers[String(name).toLowerCase()] || null; },
                    has: function(name) { return String(name).toLowerCase() in headers; },
                    forEach: function(cb) { for (var k in headers) cb(headers[k], k); }
                },
                text: function() { return Promise.resolve(ev.body); },
                json: function() { try { return Promise.resolve(JSON.parse(ev.body)); } catch (e) { return Promise.reject(e); } },
                arrayBuffer: function() { return Promise.resolve(new TextEncoder().encode(ev.body).buffer); }
            });
        });
    };
})();
`
