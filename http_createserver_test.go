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

func TestHTTPCreateServerReqProps(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`
		var http = require('http');
		var captured = {};
		var server = http.createServer(function(req, res) {
			captured.method = req.method;
			captured.url = req.url;
			captured.httpVersion = req.httpVersion;
			captured.hasSocket = !!req.socket;
			captured.hasRawHeaders = Array.isArray(req.rawHeaders);
			captured.hasHeaders = typeof req.headers === 'object';
			captured.hasOn = typeof req.on === 'function';
			captured.hasEmit = typeof req.emit === 'function';
			res.writeHead(200);
			res.end(JSON.stringify(captured));
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

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/test?q=1", port))
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	expected := `{"method":"GET","url":"/test?q=1","httpVersion":"1.1","hasSocket":true,"hasRawHeaders":true,"hasHeaders":true,"hasOn":true,"hasEmit":true}`
	if string(body) != expected {
		t.Fatalf("got %s", string(body))
	}

	rt.Exec("server.close()")
	<-done
}

func TestHTTPCreateServerResProps(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`
		var http = require('http');
		var server = http.createServer(function(req, res) {
			res.setHeader('x-test', 'val');
			var result = {
				hasHeader: res.hasHeader('x-test'),
				getHeader: res.getHeader('x-test'),
				headersSentBefore: res.headersSent
			};
			res.removeHeader('x-test');
			result.removedHeader = !res.hasHeader('x-test');
			res.setHeader('x-final', 'yes');
			result.getHeaders = res.getHeaders();
			res.writeHead(200);
			result.headersSentAfter = res.headersSent;
			res.end(JSON.stringify(result));
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
	expected := `{"hasHeader":true,"getHeader":"val","headersSentBefore":false,"removedHeader":true,"getHeaders":{"x-final":"yes"},"headersSentAfter":true}`
	if string(body) != expected {
		t.Fatalf("got %s", string(body))
	}

	rt.Exec("server.close()")
	<-done
}
