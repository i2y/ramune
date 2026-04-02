// Package web provides Go struct definitions for Web API types used by transpiled TypeScript code.
// These types mirror the Web API spec (Request, Response, Headers, URL, etc.) with
// Go-idiomatic field access and methods.
package web

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	cryptosubtle "crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/i2y/ramune/jsrt/promise"
)

// Headers wraps http.Header with Web API-compatible methods.
type Headers struct {
	h http.Header
}

func NewHeaders(init ...any) *Headers {
	h := &Headers{h: http.Header{}}
	if len(init) > 0 && init[0] != nil {
		switch v := init[0].(type) {
		case map[string]string:
			for k, val := range v {
				h.h.Set(k, val)
			}
		case *Headers:
			if v != nil {
				v.ForEach(func(val, key string) { h.h.Set(key, val) })
			}
		case map[string]any:
			for k, val := range v {
				if s, ok := val.(string); ok {
					h.h.Set(k, s)
				}
			}
		}
	}
	return h
}

func (h *Headers) Get(name string) string    { return h.h.Get(name) }
func (h *Headers) Set(name, value string)    { h.h.Set(name, value) }
func (h *Headers) Delete(name string)        { h.h.Del(name) }
func (h *Headers) Has(name string) bool      { _, ok := h.h[http.CanonicalHeaderKey(name)]; return ok }
func (h *Headers) Append(name, value string) { h.h.Add(name, value) }
func (h *Headers) Entries() [][]any {
	var entries [][]any
	for k, v := range h.h {
		if len(v) > 0 {
			entries = append(entries, []any{k, v[0]})
		}
	}
	return entries
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

// JSON parses the response body as JSON.
func (r *Response) JSON() any {
	if r.bodyBytes == nil {
		return nil
	}
	var v any
	json.Unmarshal(r.bodyBytes, &v)
	return v
}

// Json is an alias for JSON (used by transpiler's PascalCase convention).
func (r *Response) Json() any { return r.JSON() }

// ArrayBuffer returns the response body as a byte slice.
func (r *Response) ArrayBuffer() []byte {
	if r.bodyBytes == nil {
		return nil
	}
	return r.bodyBytes
}

// Blob returns the response body as a byte slice (simplified).
func (r *Response) Blob() any {
	return r.ArrayBuffer()
}

// FormData parses the response body as multipart form data.
func (r *Response) FormData() *promise.Promise[*FormData] {
	return promise.Resolve(r.parseFormData())
}

func (r *Response) parseFormData() *FormData {
	fd := NewFormData()
	if r.bodyBytes == nil {
		return fd
	}
	ct := ""
	if r.Headers != nil {
		ct = r.Headers.Get("Content-Type")
	}
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		vals, err := url.ParseQuery(string(r.bodyBytes))
		if err == nil {
			for k, vs := range vals {
				for _, v := range vs {
					fd.Append(k, v)
				}
			}
		}
	}
	return fd
}

// Request represents a Web API Request.
type Request struct {
	Url            string
	Method         string
	Headers        *Headers
	Body           io.Reader
	BodyUsed       bool
	Cache          string
	Credentials    string
	Integrity      string
	Keepalive      bool
	Mode           string
	Redirect       string
	Referrer       string
	ReferrerPolicy string
	Signal         any
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

// Clone creates a copy of the request.
func (r *Request) Clone() *Request {
	clone := &Request{
		Url:            r.Url,
		Method:         r.Method,
		Headers:        NewHeaders(),
		Cache:          r.Cache,
		Credentials:    r.Credentials,
		Integrity:      r.Integrity,
		Keepalive:      r.Keepalive,
		Mode:           r.Mode,
		Redirect:       r.Redirect,
		Referrer:       r.Referrer,
		ReferrerPolicy: r.ReferrerPolicy,
		Signal:         r.Signal,
	}
	if r.Headers != nil {
		r.Headers.ForEach(func(v, k string) { clone.Headers.Set(k, v) })
	}
	return clone
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
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		var v any
		json.Unmarshal(b, &v)
		return v
	}
	return nil
}

// Json is an alias for JSON (used by transpiler's PascalCase convention).
func (r *Request) Json() any { return r.JSON() }

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

func (e *TextEncoder) Encode(input any) []byte {
	switch v := input.(type) {
	case string:
		return []byte(v)
	case *string:
		if v == nil {
			return nil
		}
		return []byte(*v)
	case []byte:
		return v
	default:
		if v == nil {
			return nil
		}
		return []byte(fmt.Sprint(v))
	}
}

// TextDecoder provides TextDecoder.decode().
type TextDecoder struct {
	encoding string
}

func NewTextDecoder(args ...string) *TextDecoder {
	enc := "utf-8"
	if len(args) > 0 && args[0] != "" {
		enc = args[0]
	}
	return &TextDecoder{encoding: enc}
}

func (d *TextDecoder) Decode(buf any) string {
	switch v := buf.(type) {
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return ""
	}
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

// Btoa encodes a string to base64 (Web API btoa).
func Btoa(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// Atob decodes a base64 string (Web API atob).
func Atob(s string) string {
	b, _ := base64.StdEncoding.DecodeString(s)
	return string(b)
}

// CryptoKey holds key material for Web Crypto API operations.
type CryptoKey struct {
	Algorithm string
	Key       []byte
}

// CryptoSubtle implements a subset of the Web Crypto API (SubtleCrypto).
type CryptoSubtle struct{}

// Subtle is the package-level SubtleCrypto singleton.
var Subtle = &CryptoSubtle{}

// Crypto is a truthy sentinel indicating crypto is always available in Go.
var Crypto = true

func getHashFunc(name string) func() hash.Hash {
	switch strings.ToUpper(strings.ReplaceAll(name, "-", "")) {
	case "SHA256", "SHA2":
		return sha256.New
	case "SHA1":
		return sha1.New
	case "SHA384":
		return sha512.New384
	case "SHA512":
		return sha512.New
	case "MD5":
		return md5.New
	default:
		return sha256.New
	}
}

func extractAlgorithmName(algorithm any) (name string, hashName string) {
	switch a := algorithm.(type) {
	case string:
		return a, ""
	case map[string]any:
		if n, ok := a["name"]; ok {
			name, _ = n.(string)
		}
		if h, ok := a["hash"]; ok {
			switch hv := h.(type) {
			case string:
				hashName = hv
			case map[string]any:
				if hn, ok := hv["name"]; ok {
					hashName, _ = hn.(string)
				}
			}
		}
		return name, hashName
	}
	return "", ""
}

func (c *CryptoSubtle) ImportKey(format string, keyData any, algorithm any, extractable bool, keyUsages []string) *promise.Promise[any] {
	return promise.Resolve[any](func() any {
		var rawKey []byte
		switch k := keyData.(type) {
		case []byte:
			rawKey = k
		case string:
			rawKey = []byte(k)
		default:
			rawKey = nil
		}
		_, hashName := extractAlgorithmName(algorithm)
		if hashName == "" {
			hashName = "SHA-256"
		}
		return &CryptoKey{Algorithm: hashName, Key: rawKey}
	}())
}

func (c *CryptoSubtle) Sign(algorithm any, key any, data any) *promise.Promise[[]byte] {
	return promise.Resolve(func() []byte {
		ck, ok := key.(*CryptoKey)
		if !ok {
			return nil
		}
		h := getHashFunc(ck.Algorithm)
		mac := hmac.New(h, ck.Key)
		switch d := data.(type) {
		case []byte:
			mac.Write(d)
		case string:
			mac.Write([]byte(d))
		}
		return mac.Sum(nil)
	}())
}

func (c *CryptoSubtle) Verify(algorithm any, key any, signature any, data any) *promise.Promise[bool] {
	return promise.Resolve(func() bool {
		ck, ok := key.(*CryptoKey)
		if !ok {
			return false
		}
		var sigBytes []byte
		switch s := signature.(type) {
		case []byte:
			sigBytes = s
		}
		h := getHashFunc(ck.Algorithm)
		mac := hmac.New(h, ck.Key)
		switch d := data.(type) {
		case []byte:
			mac.Write(d)
		case string:
			mac.Write([]byte(d))
		}
		expected := mac.Sum(nil)
		return cryptosubtle.ConstantTimeCompare(sigBytes, expected) == 1
	}())
}

func (c *CryptoSubtle) Digest(algorithm any, data any) *promise.Promise[[]byte] {
	return promise.Resolve(func() []byte {
		name, _ := extractAlgorithmName(algorithm)
		h := getHashFunc(name)()
		switch d := data.(type) {
		case []byte:
			h.Write(d)
		case string:
			h.Write([]byte(d))
		}
		return h.Sum(nil)
	}())
}
