// Package web provides Go struct definitions for Web API types used by transpiled TypeScript code.
// These types mirror the Web API spec (Request, Response, Headers, URL, etc.) with
// Go-idiomatic field access and methods.
package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Headers wraps http.Header with Web API-compatible methods.
type Headers struct {
	h http.Header
}

func NewHeaders(init ...map[string]string) *Headers {
	h := &Headers{h: http.Header{}}
	if len(init) > 0 {
		for k, v := range init[0] {
			h.h.Set(k, v)
		}
	}
	return h
}

func (h *Headers) Get(name string) string    { return h.h.Get(name) }
func (h *Headers) Set(name, value string)    { h.h.Set(name, value) }
func (h *Headers) Delete(name string)        { h.h.Del(name) }
func (h *Headers) Has(name string) bool      { _, ok := h.h[http.CanonicalHeaderKey(name)]; return ok }
func (h *Headers) Append(name, value string) { h.h.Add(name, value) }
func (h *Headers) Entries() map[string]string {
	m := make(map[string]string)
	for k, v := range h.h {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
func (h *Headers) ForEach(fn func(value, key string)) {
	for k, vs := range h.h {
		for _, v := range vs {
			fn(v, k)
		}
	}
}
func (h *Headers) GetSetCookie() []string { return h.h.Values("Set-Cookie") }
func (h *Headers) Raw() http.Header       { return h.h }

// Response represents a Web API Response.
type Response struct {
	Body       io.Reader
	Headers    *Headers
	Status     int
	StatusText string
	Ok         bool
	bodyBytes  []byte
}

func NewResponse(body any, init ...any) *Response {
	r := &Response{Status: 200, StatusText: "OK", Ok: true, Headers: NewHeaders()}
	// Handle body
	switch b := body.(type) {
	case string:
		r.Body = strings.NewReader(b)
		r.bodyBytes = []byte(b)
	case []byte:
		r.Body = strings.NewReader(string(b))
		r.bodyBytes = b
	case io.Reader:
		r.Body = b
	case nil:
		// empty body
	}
	// Handle init options
	if len(init) > 0 {
		if opts, ok := init[0].(map[string]any); ok {
			if s, ok := opts["status"]; ok {
				if si, ok := s.(int); ok {
					r.Status = si
				} else if sf, ok := s.(float64); ok {
					r.Status = int(sf)
				}
			}
			if st, ok := opts["statusText"]; ok {
				if s, ok := st.(string); ok {
					r.StatusText = s
				}
			}
			if h, ok := opts["headers"]; ok {
				if hd, ok := h.(*Headers); ok {
					r.Headers = hd
				}
			}
		}
		// Also accept Response as second arg (for cloning status/headers)
		if resp, ok := init[0].(*Response); ok {
			r.Status = resp.Status
			r.StatusText = resp.StatusText
			r.Headers = resp.Headers
		}
	}
	r.Ok = r.Status >= 200 && r.Status < 300
	return r
}

// ResponseJSON creates a JSON response.
func ResponseJSON(data any, status ...int) *Response {
	// Simplified — actual implementation would marshal to JSON
	r := NewResponse(nil)
	if len(status) > 0 {
		r.Status = status[0]
	}
	r.Headers.Set("Content-Type", "application/json")
	return r
}

// ResponseRedirect creates a redirect response.
func ResponseRedirect(url string, status ...int) *Response {
	code := 302
	if len(status) > 0 {
		code = status[0]
	}
	r := NewResponse(nil)
	r.Status = code
	r.Headers.Set("Location", url)
	return r
}

func (r *Response) Text() string {
	if r.bodyBytes != nil {
		return string(r.bodyBytes)
	}
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		r.bodyBytes = b
		return string(b)
	}
	return ""
}

func (r *Response) Clone() *Response {
	clone := &Response{
		Status:     r.Status,
		StatusText: r.StatusText,
		Ok:         r.Ok,
		Headers:    NewHeaders(),
		bodyBytes:  r.bodyBytes,
	}
	if r.bodyBytes != nil {
		clone.Body = strings.NewReader(string(r.bodyBytes))
	}
	for k, vs := range r.Headers.Raw() {
		for _, v := range vs {
			clone.Headers.Append(k, v)
		}
	}
	return clone
}

// Request represents a Web API Request.
type Request struct {
	Url     string
	Method  string
	Headers *Headers
	Body    io.Reader
}

func NewRequest(urlStr string, init ...map[string]any) *Request {
	r := &Request{Url: urlStr, Method: "GET", Headers: NewHeaders()}
	if len(init) > 0 {
		if m, ok := init[0]["method"]; ok {
			if s, ok := m.(string); ok {
				r.Method = strings.ToUpper(s)
			}
		}
		if h, ok := init[0]["headers"]; ok {
			if hd, ok := h.(*Headers); ok {
				r.Headers = hd
			}
		}
		if b, ok := init[0]["body"]; ok {
			if s, ok := b.(string); ok {
				r.Body = strings.NewReader(s)
			} else if rd, ok := b.(io.Reader); ok {
				r.Body = rd
			}
		}
	}
	return r
}

// Text returns the request body as a string.
func (r *Request) Text() string {
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	return ""
}

// JSON parses the request body as JSON.
func (r *Request) JSON() any {
	return nil // simplified
}

// FormData returns the request body parsed as FormData.
func (r *Request) FormData() *FormData {
	return NewFormData() // simplified — real impl would parse multipart/urlencoded
}

// Await is a no-op for sync types, providing compatibility when the transpiler emits
// await on a synchronous method (e.g., await request.formData() where Go FormData is sync).
func (f *FormData) Await() (*FormData, error) { return f, nil }

// Blob returns the request body as a Blob (simplified).
func (r *Request) Blob() any {
	return nil
}

// ArrayBuffer returns the request body as bytes.
func (r *Request) ArrayBuffer() []byte {
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		return b
	}
	return nil
}

func (r *Request) Clone() *Request {
	return &Request{
		Url:     r.Url,
		Method:  r.Method,
		Headers: r.Headers, // shallow copy of headers
	}
}

// URL represents the Web API URL.
type URL struct {
	Pathname string
	Search   string
	Hash     string
	Host     string
	Hostname string
	Port     string
	Protocol string
	Origin   string
	Href     string
}

func NewURL(rawURL string) *URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		return &URL{Href: rawURL}
	}
	return &URL{
		Pathname: u.Path,
		Search:   u.RawQuery,
		Hash:     u.Fragment,
		Host:     u.Host,
		Hostname: u.Hostname(),
		Port:     u.Port(),
		Protocol: u.Scheme + ":",
		Origin:   u.Scheme + "://" + u.Host,
		Href:     rawURL,
	}
}

// TextEncoder provides TextEncoder.encode().
type TextEncoder struct{}

func NewTextEncoder() *TextEncoder { return &TextEncoder{} }

func (e *TextEncoder) Encode(s *string) []byte {
	if s == nil {
		return nil
	}
	return []byte(*s)
}

// FormData is a simplified FormData representation.
type FormData struct {
	data map[string][]string
}

func NewFormData() *FormData {
	return &FormData{data: make(map[string][]string)}
}

func (f *FormData) Get(name string) string {
	if vs, ok := f.data[name]; ok && len(vs) > 0 {
		return vs[0]
	}
	return ""
}

func (f *FormData) Set(name, value string)    { f.data[name] = []string{value} }
func (f *FormData) Append(name, value string) { f.data[name] = append(f.data[name], value) }
func (f *FormData) Has(name string) bool      { _, ok := f.data[name]; return ok }
func (f *FormData) Delete(name string)        { delete(f.data, name) }
func (f *FormData) ForEach(fn func(value any, key string)) {
	for k, vs := range f.data {
		for _, v := range vs {
			fn(v, k)
		}
	}
}
