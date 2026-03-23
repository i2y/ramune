package ramune_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

func TestHTTPCreateServer(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`
		var http = require('http');
		var server = http.createServer(function(req, res) {
			res.writeHead(200);
			res.end('hello from createServer');
		});
		server.listen(0);
		server.address().port;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	portF, _ := v.Float64()
	port := int(portF)

	done := make(chan error, 1)
	go func() { done <- rt.RunEventLoopFor(3 * time.Second) }()
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/test", port))
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d, body: %q", resp.StatusCode, string(body))
	}
	if string(body) != "hello from createServer" {
		t.Fatalf("expected 'hello from createServer', got %q", string(body))
	}

	rt.Exec("server.close()")
	<-done
}

func TestHTTPCreateServerHeaders(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`
		var http = require('http');
		var server = http.createServer(function(req, res) {
			res.setHeader('x-custom', 'ramune');
			res.writeHead(201, {'x-another': 'header-val'});
			res.end('with headers');
		});
		server.listen(0);
		server.address().port;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	portF, _ := v.Float64()
	port := int(portF)

	done := make(chan error, 1)
	go func() { done <- rt.RunEventLoopFor(3 * time.Second) }()
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", port))
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatalf("expected status 201, got %d, body: %q", resp.StatusCode, string(body))
	}
	if string(body) != "with headers" {
		t.Fatalf("expected 'with headers', got %q", string(body))
	}

	rt.Exec("server.close()")
	<-done
}
