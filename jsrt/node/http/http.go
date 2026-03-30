// Package http provides Node.js http module equivalents for transpiled TypeScript code.
package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Server wraps a Go HTTP server with Node.js-like API.
type Server struct {
	server *http.Server
	mux    *http.ServeMux
}

// IncomingMessage wraps an HTTP request (Node.js http.IncomingMessage).
type IncomingMessage struct {
	Method  string
	URL     string
	Headers http.Header
	body    []byte
}

// ServerResponse wraps an HTTP response writer (Node.js http.ServerResponse).
type ServerResponse struct {
	w          http.ResponseWriter
	statusCode int
	written    bool
}

// CreateServer creates an HTTP server with the given handler.
func CreateServer(handler func(req *IncomingMessage, res *ServerResponse)) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		req := &IncomingMessage{
			Method:  r.Method,
			URL:     r.URL.String(),
			Headers: r.Header,
			body:    body,
		}
		res := &ServerResponse{w: w, statusCode: 200}
		handler(req, res)
	})
	return &Server{mux: mux}
}

// Listen starts the server on the given port.
func (s *Server) Listen(port int, callback ...func()) error {
	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: s.mux,
	}
	if len(callback) > 0 && callback[0] != nil {
		go callback[0]()
	}
	return s.server.ListenAndServe()
}

// Close stops the server.
func (s *Server) Close() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// Body returns the request body as a string.
func (m *IncomingMessage) Body() string {
	return string(m.body)
}

// WriteHead sets the status code and headers.
func (r *ServerResponse) WriteHead(statusCode int, headers ...map[string]string) {
	r.statusCode = statusCode
	if len(headers) > 0 {
		for k, v := range headers[0] {
			r.w.Header().Set(k, v)
		}
	}
}

// Write writes data to the response.
func (r *ServerResponse) Write(data string) {
	if !r.written {
		r.w.WriteHeader(r.statusCode)
		r.written = true
	}
	r.w.Write([]byte(data))
}

// End finishes the response, optionally writing data.
func (r *ServerResponse) End(data ...string) {
	if len(data) > 0 {
		r.Write(data[0])
	} else if !r.written {
		r.w.WriteHeader(r.statusCode)
	}
}

// SetHeader sets a response header.
func (r *ServerResponse) SetHeader(key, value string) {
	r.w.Header().Set(key, value)
}

// Get performs an HTTP GET request (simplified http.get).
func Get(url string) (*http.Response, error) {
	return http.Get(url)
}

// Request performs an HTTP request with options.
func Request(url string, opts RequestOptions) (*http.Response, error) {
	var body io.Reader
	if opts.Body != "" {
		body = strings.NewReader(opts.Body)
	}
	req, err := http.NewRequest(opts.Method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}

// RequestOptions holds options for http.request.
type RequestOptions struct {
	Method  string
	Headers map[string]string
	Body    string
}

// JSONStringify converts a value to a JSON string.
func JSONStringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// JSONParse parses a JSON string.
func JSONParse(s string) (any, error) {
	var result any
	err := json.Unmarshal([]byte(s), &result)
	return result, err
}
