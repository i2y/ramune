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
