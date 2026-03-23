package ramune_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/i2y/ramune"
)

func TestNetConnect(t *testing.T) {
	// Start a simple TCP echo server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, _ := conn.Read(buf)
		conn.Write([]byte("echo:" + string(buf[:n])))
	}()

	addr := ln.Addr().(*net.TCPAddr)
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve, reject) {
			var net = require('net');
			var client = net.createConnection(%d, '127.0.0.1', function() {
				client.write('hello');
			});
			var data = '';
			client.on('data', function(chunk) { data += chunk; });
			client.on('end', function() { resolve(data); });
			client.on('error', function(e) { reject(e); });
		})
	`, addr.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "echo:hello" {
		t.Fatalf("got %q, want %q", s, "echo:hello")
	}
}

func TestTLSConnect(t *testing.T) {
	// Start an HTTPS server.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "secure ok")
	}))
	defer srv.Close()

	r, err := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	// Just verify tls.connect exists and is a function.
	v, err := r.Eval(`typeof require('tls').connect`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "function" {
		t.Fatalf("tls.connect type: %q", s)
	}
}

func TestNetSocketPipe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		conn.Write([]byte("line1\nline2\nline3\n"))
	}()

	addr := ln.Addr().(*net.TCPAddr)
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.EvalAsync(fmt.Sprintf(`
		new Promise(function(resolve) {
			var net = require('net');
			var chunks = [];
			var client = net.createConnection(%d, '127.0.0.1');
			client.on('data', function(chunk) { chunks.push(chunk); });
			client.on('end', function() { resolve(chunks.join('')); });
		})
	`, addr.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "line1\nline2\nline3\n" {
		t.Fatalf("got %q", s)
	}
}
