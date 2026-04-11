package ramune_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"
)

func generateTestCertKey() (string, string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ramune-test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func TestTCPServerBasic(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// Start TCP server that echoes data back.
	v, err := r.Eval(`
		(function() {
			var net = require('net');
			var received = [];
			var server = net.createServer(function(sock) {
				sock.on('data', function(data) {
					received.push(data);
					sock.write('echo:' + data);
				});
			});
			server.listen(0, function() {
				globalThis.__tcpTestPort = server.address().port;
				globalThis.__tcpTestReceived = received;
				globalThis.__tcpTestServer = server;
			});
			return 'ok';
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()

	// Tick until listening event is delivered.
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		pv, _ := r.Eval(`globalThis.__tcpTestPort || 0`)
		port, _ := pv.Float64()
		pv.Close()
		if port > 0 {
			break
		}
	}

	pv, _ := r.Eval(`globalThis.__tcpTestPort`)
	port, _ := pv.Float64()
	pv.Close()
	if port == 0 {
		t.Fatal("server did not start listening")
	}

	// Connect with Go client and send data.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", int(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Write([]byte("hello"))

	// Read echo response with short per-iteration deadlines so event loop keeps ticking.
	buf := make([]byte, 1024)
	var got string
	for i := 0; i < 100; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, _ := conn.Read(buf)
		if n > 0 {
			got = string(buf[:n])
			break
		}
	}
	conn.Close()

	if got != "echo:hello" {
		t.Errorf("got %q, want %q", got, "echo:hello")
	}

	// Verify server received the data.
	rv, _ := r.Eval(`JSON.stringify(globalThis.__tcpTestReceived)`)
	received := rv.String()
	rv.Close()
	if received != `["hello"]` {
		t.Errorf("received %s, want [\"hello\"]", received)
	}

	// Clean up.
	r.Exec(`globalThis.__tcpTestServer.close()`)
	r.Tick()
}

func TestTCPServerMultipleConnections(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var net = require('net');
			var connCount = 0;
			var server = net.createServer(function(sock) {
				connCount++;
				sock.write('conn:' + connCount);
			});
			server.listen(0, function() {
				globalThis.__tcpTestPort2 = server.address().port;
				globalThis.__tcpTestServer2 = server;
			});
			globalThis.__tcpConnCount = function() { return connCount; };
			return 'ok';
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()

	// Wait for listen.
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		pv, _ := r.Eval(`globalThis.__tcpTestPort2 || 0`)
		port, _ := pv.Float64()
		pv.Close()
		if port > 0 {
			break
		}
	}

	pv, _ := r.Eval(`globalThis.__tcpTestPort2`)
	port, _ := pv.Float64()
	pv.Close()
	if port == 0 {
		t.Fatal("server did not start listening")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", int(port))

	// Connect two clients.
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	// Tick to process connections.
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		cv, _ := r.Eval(`globalThis.__tcpConnCount()`)
		count, _ := cv.Float64()
		cv.Close()
		if count >= 2 {
			break
		}
	}

	cv, _ := r.Eval(`globalThis.__tcpConnCount()`)
	count, _ := cv.Float64()
	cv.Close()
	if count < 2 {
		t.Errorf("expected 2 connections, got %v", count)
	}

	r.Exec(`globalThis.__tcpTestServer2.close()`)
	r.Tick()
}

func TestTCPServerClose(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	v, err := r.Eval(`
		(function() {
			var net = require('net');
			var server = net.createServer(function(sock) {});
			server.listen(0, function() {
				globalThis.__tcpTestPort3 = server.address().port;
				globalThis.__tcpTestServer3 = server;
			});
			return 'ok';
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()

	// Wait for listen.
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		pv, _ := r.Eval(`globalThis.__tcpTestPort3 || 0`)
		port, _ := pv.Float64()
		pv.Close()
		if port > 0 {
			break
		}
	}

	pv, _ := r.Eval(`globalThis.__tcpTestPort3`)
	port, _ := pv.Float64()
	pv.Close()
	if port == 0 {
		t.Fatal("server did not start")
	}

	// Close the server.
	r.Exec(`globalThis.__tcpTestServer3.close()`)
	for i := 0; i < 20; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
	}

	// Verify we can't connect anymore.
	_, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", int(port)), 200*time.Millisecond)
	if err == nil {
		t.Error("expected connection refused after server close")
	}
}

func TestTLSServerBasic(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	cert, key := generateTestCertKey()
	v, err := r.Eval(fmt.Sprintf(`
		(function() {
			var tls = require('tls');
			var server = tls.createServer({
				cert: %q,
				key: %q
			}, function(sock) {`, cert, key) + `
				sock.on('data', function(data) {
					sock.write('echo:' + data);
				});
			});
			server.listen(0, function() {
				globalThis.__tlsTestPort = server.address().port;
				globalThis.__tlsTestServer = server;
			});
			return 'ok';
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()

	// Wait for listen.
	var port float64
	for i := 0; i < 50; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
		pv, _ := r.Eval(`globalThis.__tlsTestPort || 0`)
		port, _ = pv.Float64()
		pv.Close()
		if port > 0 {
			break
		}
	}
	if port == 0 {
		t.Fatal("TLS server did not start")
	}

	// Connect with TLS client.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp",
		fmt.Sprintf("127.0.0.1:%d", int(port)),
		&tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("TLS dial failed: %v", err)
	}
	conn.Write([]byte("hello"))
	time.Sleep(100 * time.Millisecond)

	// Tick to process events.
	for i := 0; i < 20; i++ {
		r.Tick()
		time.Sleep(10 * time.Millisecond)
	}

	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := conn.Read(buf)
	conn.Close()

	if n > 0 && string(buf[:n]) != "echo:hello" {
		t.Fatalf("expected 'echo:hello', got %q", string(buf[:n]))
	}

	r.Exec(`globalThis.__tlsTestServer.close()`)
	r.Tick()
}
