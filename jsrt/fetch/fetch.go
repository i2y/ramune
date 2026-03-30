// Package fetch provides the fetch() API for transpiled TypeScript code.
package fetch

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Response wraps an HTTP response with JS-like methods.
type Response struct {
	Status     int
	StatusText string
	Ok         bool
	Headers    http.Header
	body       []byte
}

// RequestInit holds options for a fetch request.
type RequestInit struct {
	Method  string
	Headers map[string]string
	Body    string
}

// Fetch performs an HTTP request (like globalThis.fetch).
func Fetch(url string, opts ...RequestInit) (*Response, error) {
	method := "GET"
	var body io.Reader
	var headers map[string]string

	if len(opts) > 0 {
		opt := opts[0]
		if opt.Method != "" {
			method = opt.Method
		}
		if opt.Body != "" {
			body = strings.NewReader(opt.Body)
		}
		headers = opt.Headers
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Ok:         resp.StatusCode >= 200 && resp.StatusCode < 300,
		Headers:    resp.Header,
		body:       respBody,
	}, nil
}

// Text returns the response body as a string.
func (r *Response) Text() (string, error) {
	return string(r.body), nil
}

// JSON parses the response body as JSON into the target.
func (r *Response) JSON(target any) error {
	return json.Unmarshal(r.body, target)
}

// JSONValue parses the response body as JSON and returns it.
func (r *Response) JSONValue() (any, error) {
	var result any
	err := json.Unmarshal(r.body, &result)
	return result, err
}

// Bytes returns the response body as bytes.
func (r *Response) Bytes() []byte {
	return r.body
}
