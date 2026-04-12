package ramune_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	ramune "github.com/i2y/ramune"
)

// startH2CServer starts a cleartext HTTP/2 server for testing.
func startH2CServer(t *testing.T, handler http.HandlerFunc) (int, func()) {
	t.Helper()
	h2s := &http2.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)
	srv := &http.Server{Handler: h2c.NewHandler(mux, h2s)}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { srv.Close(); ln.Close() }
}

func TestHTTP2Connect(t *testing.T) {
	port, cleanup := startH2CServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	})
	defer cleanup()

	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve, reject) {
			var http2 = require('http2');
			var session = http2.connect('http://127.0.0.1:%d');
			session.on('connect', function() {
				var req = session.request({':method': 'GET', ':path': '/'});
				var data = '';
				req.on('response', function(headers) {});
				req.on('data', function(chunk) { data += chunk; });
				req.on('end', function() {
					session.close();
					resolve(data);
				});
				req.end();
			});
			session.on('error', function(e) { reject(e); });
		})
	`, port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"ok":true}` {
		t.Fatalf("got %q", s)
	}
}

func TestHTTP2PostData(t *testing.T) {
	// Go h2c server that echoes POST body.
	port, cleanup := startH2CServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("x-echo", "true")
		w.WriteHeader(200)
		w.Write(body)
	})
	defer cleanup()

	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve, reject) {
			var http2 = require('http2');
			var session = http2.connect('http://127.0.0.1:%d');
			session.on('connect', function() {
				var req = session.request({':method': 'POST', ':path': '/echo', 'content-type': 'text/plain'});
				var data = '';
				req.on('response', function(headers) {});
				req.on('data', function(chunk) { data += chunk; });
				req.on('end', function() {
					session.close();
					resolve(data);
				});
				req.write('hello ');
				req.end('world');
			});
			session.on('error', function(e) { reject(e); });
		})
	`, port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "hello world" {
		t.Fatalf("got %q", s)
	}
}

func TestHTTP2Trailers(t *testing.T) {
	// Go h2c server that sends trailers.
	port, cleanup := startH2CServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "grpc-status, grpc-message")
		w.Header().Set("content-type", "application/grpc")
		w.WriteHeader(200)
		w.Write([]byte("data"))
		w.Header().Set(http.TrailerPrefix+"grpc-status", "0")
		w.Header().Set(http.TrailerPrefix+"grpc-message", "OK")
	})
	defer cleanup()

	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve, reject) {
			var http2 = require('http2');
			var session = http2.connect('http://127.0.0.1:%d');
			session.on('connect', function() {
				var req = session.request({':method': 'POST', ':path': '/rpc'});
				var trailers = null;
				req.on('response', function(headers) {});
				req.on('trailers', function(t) { trailers = t; });
				req.on('end', function() {
					session.close();
					resolve(JSON.stringify(trailers));
				});
				req.end();
			});
			session.on('error', function(e) { reject(e); });
		})
	`, port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if !strings.Contains(s, "grpc-status") {
		t.Fatalf("trailers missing grpc-status: %q", s)
	}
}

func TestHTTP2Constants(t *testing.T) {
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.Eval(`
		var http2 = require('http2');
		JSON.stringify({
			method: http2.constants.HTTP2_HEADER_METHOD,
			path: http2.constants.HTTP2_HEADER_PATH,
			noError: http2.constants.NGHTTP2_NO_ERROR,
			ok: http2.constants.HTTP_STATUS_OK
		});
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	expected := `{"method":":method","path":":path","noError":0,"ok":200}`
	if s != expected {
		t.Fatalf("got %q, want %q", s, expected)
	}
}

func TestHTTP2GRPCUnaryCall(t *testing.T) {
	// Simulates a gRPC unary call pattern:
	// POST /pkg.Service/Method, content-type: application/grpc
	// Response body + trailers with grpc-status.
	port, cleanup := startH2CServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Read the request body (gRPC frame).
		body, _ := io.ReadAll(r.Body)

		// Verify gRPC headers.
		ct := r.Header.Get("Content-Type")
		if ct != "application/grpc" {
			w.WriteHeader(400)
			return
		}

		// Send response with gRPC trailers.
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "grpc-status, grpc-message")
		w.WriteHeader(200)
		// Echo the body back as response.
		w.Write(body)
		// Set trailers.
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
		w.Header().Set(http.TrailerPrefix+"Grpc-Message", "OK")
	})
	defer cleanup()

	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve, reject) {
			var http2 = require('http2');
			var session = http2.connect('http://127.0.0.1:%d');
			session.on('connect', function() {
				var req = session.request({
					':method': 'POST',
					':path': '/pkg.Service/Method',
					'content-type': 'application/grpc',
					'te': 'trailers'
				});

				var respHeaders = null;
				var data = '';
				var trailers = null;

				req.on('response', function(h) { respHeaders = h; });
				req.on('data', function(chunk) { data += chunk; });
				req.on('trailers', function(t) { trailers = t; });
				req.on('end', function() {
					session.close();
					resolve(JSON.stringify({
						status: respHeaders[':status'],
						contentType: respHeaders['content-type'],
						body: data,
						grpcStatus: trailers ? trailers['grpc-status'] : null,
						grpcMessage: trailers ? trailers['grpc-message'] : null
					}));
				});

				req.end('grpc-payload');
			});
			session.on('error', function(e) { reject(e); });
		})
	`, port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()

	if !strings.Contains(s, `"status":"200"`) {
		t.Fatalf("unexpected response: %s", s)
	}
	if !strings.Contains(s, `"body":"grpc-payload"`) {
		t.Fatalf("body not echoed: %s", s)
	}
	if !strings.Contains(s, `"grpcStatus":"0"`) {
		t.Fatalf("grpc-status missing: %s", s)
	}
}

func TestHTTP2MultipleStreams(t *testing.T) {
	// Verify multiplexing: multiple concurrent streams on one connection.
	port, cleanup := startH2CServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(r.URL.Path))
	})
	defer cleanup()

	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve, reject) {
			var http2 = require('http2');
			var session = http2.connect('http://127.0.0.1:%d');
			session.on('connect', function() {
				var results = {};
				var done = 0;
				var paths = ['/a', '/b', '/c'];

				paths.forEach(function(p) {
					var req = session.request({':method': 'GET', ':path': p});
					var data = '';
					req.on('data', function(chunk) { data += chunk; });
					req.on('end', function() {
						results[p] = data;
						done++;
						if (done === paths.length) {
							session.close();
							resolve(JSON.stringify(results));
						}
					});
					req.end();
				});
			});
			session.on('error', function(e) { reject(e); });
		})
	`, port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()

	if !strings.Contains(s, `"/a":"/a"`) || !strings.Contains(s, `"/b":"/b"`) || !strings.Contains(s, `"/c":"/c"`) {
		t.Fatalf("multiplexing failed: %s", s)
	}
}
