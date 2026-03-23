package ramune_test

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

// wsTestClient is a minimal WebSocket client for testing.
type wsTestClient struct {
	conn  net.Conn
	bufrw *bufio.ReadWriter
}

func dialWS(t *testing.T, port int) *wsTestClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	bufrw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	// Send WebSocket upgrade request.
	key := base64.StdEncoding.EncodeToString([]byte("test-key-1234567"))
	req := "GET / HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	bufrw.WriteString(req)
	bufrw.Flush()

	// Read response.
	resp, err := http.ReadResponse(bufio.NewReader(bufrw), nil)
	if err != nil {
		conn.Close()
		t.Fatalf("read response failed: %v", err)
	}
	if resp.StatusCode != 101 {
		conn.Close()
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	// Verify accept key.
	expectedAccept := computeTestAcceptKey(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != expectedAccept {
		conn.Close()
		t.Fatalf("bad accept key: got %q, want %q", got, expectedAccept)
	}

	return &wsTestClient{conn: conn, bufrw: bufrw}
}

func computeTestAcceptKey(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// sendText sends a masked text frame (client-to-server frames must be masked).
func (c *wsTestClient) sendText(msg string) error {
	payload := []byte(msg)
	// FIN + text opcode.
	c.bufrw.WriteByte(0x81)
	// Mask bit set + payload length.
	if len(payload) <= 125 {
		c.bufrw.WriteByte(0x80 | byte(len(payload)))
	} else {
		c.bufrw.WriteByte(0x80 | 126)
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(len(payload)))
		c.bufrw.Write(lenBytes)
	}
	// Masking key.
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	c.bufrw.Write(mask[:])
	// Masked payload.
	for i, b := range payload {
		c.bufrw.WriteByte(b ^ mask[i%4])
	}
	return c.bufrw.Flush()
}

// readText reads a text frame from the server (server-to-client frames are NOT masked).
func (c *wsTestClient) readText() (string, error) {
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.bufrw, header); err != nil {
		return "", err
	}
	payloadLen := uint64(header[1] & 0x7F)
	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(c.bufrw, ext); err != nil {
			return "", err
		}
		payloadLen = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(c.bufrw, ext); err != nil {
			return "", err
		}
		payloadLen = binary.BigEndian.Uint64(ext)
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(c.bufrw, payload); err != nil {
		return "", err
	}
	return string(payload), nil
}

// sendClose sends a close frame.
func (c *wsTestClient) sendClose() error {
	c.bufrw.WriteByte(0x88)           // FIN + close opcode
	c.bufrw.WriteByte(0x80)           // Masked, 0 length
	c.bufrw.Write([]byte{0, 0, 0, 0}) // Mask key
	return c.bufrw.Flush()
}

func (c *wsTestClient) close() {
	c.conn.Close()
}

func TestWebSocketEcho(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Start server with WebSocket support.
	v, err := rt.Eval(`
		var wsLog = [];
		var server = Bun.serve({
			port: 0,
			fetch: function(req, server) {
				if (server.upgrade(req)) {
					return;
				}
				return new Response("Not a WebSocket");
			},
			websocket: {
				open: function(ws) {
					wsLog.push("open");
				},
				message: function(ws, msg) {
					wsLog.push("msg:" + msg);
					ws.send("echo:" + msg);
				},
				close: function(ws) {
					wsLog.push("close");
				}
			}
		});
		server.port;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	portF, _ := v.Float64()
	port := int(portF)
	t.Logf("Server started on port %d", port)

	// Run event loop in background — it processes HTTP requests and WS events.
	done := make(chan error, 1)
	go func() {
		done <- rt.RunEventLoopFor(3 * time.Second)
	}()

	// Give the event loop a moment to start.
	time.Sleep(100 * time.Millisecond)

	// Connect WebSocket client.
	ws := dialWS(t, port)
	defer ws.close()

	// Wait for open event to be processed.
	time.Sleep(200 * time.Millisecond)

	// Send a message.
	if err := ws.sendText("hello"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Read the echo response.
	msg, err := ws.readText()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if msg != "echo:hello" {
		t.Fatalf("got %q, want %q", msg, "echo:hello")
	}

	// Send another message.
	if err := ws.sendText("world"); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	msg, err = ws.readText()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if msg != "echo:world" {
		t.Fatalf("got %q, want %q", msg, "echo:world")
	}

	// Close the WebSocket.
	ws.sendClose()
	time.Sleep(200 * time.Millisecond)

	// Stop the server and wait for event loop goroutine to finish.
	rt.Exec("server.stop()")
	<-done

	// Check the log.
	logV, err := rt.Eval("JSON.stringify(wsLog)")
	if err != nil {
		t.Fatal(err)
	}
	defer logV.Close()
	logStr, _ := logV.GoString()
	if !strings.Contains(logStr, `"open"`) {
		t.Fatalf("missing open event in log: %s", logStr)
	}
	if !strings.Contains(logStr, `"msg:hello"`) {
		t.Fatalf("missing msg:hello event in log: %s", logStr)
	}
	if !strings.Contains(logStr, `"msg:world"`) {
		t.Fatalf("missing msg:world event in log: %s", logStr)
	}
}

func TestWebSocketNonUpgradeRequest(t *testing.T) {
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`
		var server = Bun.serve({
			port: 0,
			fetch: function(req, server) {
				if (server.upgrade(req)) {
					return;
				}
				return new Response("Not a WebSocket", {status: 200});
			},
			websocket: {
				message: function(ws, msg) { ws.send(msg); }
			}
		});
		server.port;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	portF, _ := v.Float64()
	port := int(portF)

	// Run event loop in background.
	done := make(chan error, 1)
	go func() {
		done <- rt.RunEventLoopFor(3 * time.Second)
	}()

	time.Sleep(100 * time.Millisecond)

	// Send a normal HTTP request (not WebSocket upgrade).
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/test", port))
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "Not a WebSocket" {
		t.Fatalf("got %q, want %q", string(body), "Not a WebSocket")
	}

	rt.Exec("server.stop()")
	<-done
}
