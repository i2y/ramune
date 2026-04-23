package ramune

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// wsConn represents a single WebSocket connection.
type wsConn struct {
	conn   net.Conn
	bufrw  *bufio.ReadWriter
	mu     sync.Mutex
	closed bool
}

// wsEvent is a WebSocket event queued for delivery to JS.
type wsEvent struct {
	Kind   string `json:"kind"`   // "open", "message", "close", "error"
	ConnID int    `json:"connId"` // connection ID
	Data   string `json:"data,omitempty"`
}

// wsManager tracks active WebSocket connections for a bunServerState.
type wsManager struct {
	mu     sync.Mutex
	conns  map[int]*wsConn
	events []wsEvent
	nextID int

	// wakeFn is set by installWebSocket to r.Wake; it's called after
	// every events append so the event loop doesn't sit on its
	// pendingPollDefault (10ms) cap before ticking the new event.
	wakeFn func()
}

// wake invokes wakeFn if registered. Safe to call from any goroutine
// after appending to events (release the mu first to avoid holding it
// across the channel send inside Wake).
func (wm *wsManager) wake() {
	if wm.wakeFn != nil {
		wm.wakeFn()
	}
}

func newWSManager() *wsManager {
	return &wsManager{
		conns:  make(map[int]*wsConn),
		nextID: 1,
	}
}

// upgrade performs the WebSocket handshake on an HTTP connection.
// It hijacks the connection from the HTTP handler and starts a goroutine
// to read frames and queue events.
func (wm *wsManager) upgrade(w http.ResponseWriter, r *http.Request) (int, error) {
	// Validate WebSocket upgrade request.
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return 0, fmt.Errorf("not a websocket upgrade request")
	}
	if !headerContains(r.Header, "Connection", "upgrade") {
		return 0, fmt.Errorf("missing Connection: upgrade header")
	}
	wsKey := r.Header.Get("Sec-WebSocket-Key")
	if wsKey == "" {
		return 0, fmt.Errorf("missing Sec-WebSocket-Key header")
	}

	// Compute accept key per RFC 6455.
	acceptKey := computeAcceptKey(wsKey)

	// Hijack the connection.
	hj, ok := w.(http.Hijacker)
	if !ok {
		return 0, fmt.Errorf("http.ResponseWriter does not support hijacking")
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return 0, fmt.Errorf("hijack failed: %w", err)
	}

	// Send the WebSocket handshake response.
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey + "\r\n" +
		"\r\n"
	if _, err := bufrw.WriteString(response); err != nil {
		conn.Close()
		return 0, fmt.Errorf("failed to write handshake: %w", err)
	}
	if err := bufrw.Flush(); err != nil {
		conn.Close()
		return 0, fmt.Errorf("failed to flush handshake: %w", err)
	}

	ws := &wsConn{
		conn:  conn,
		bufrw: bufrw,
	}

	wm.mu.Lock()
	id := wm.nextID
	wm.nextID++
	wm.conns[id] = ws
	wm.events = append(wm.events, wsEvent{Kind: "open", ConnID: id})
	wm.mu.Unlock()
	wm.wake()

	// Read frames in background.
	go wm.readLoop(id, ws)

	return id, nil
}

// readLoop reads WebSocket frames and queues events.
func (wm *wsManager) readLoop(id int, ws *wsConn) {
	defer func() {
		ws.mu.Lock()
		if !ws.closed {
			ws.closed = true
			ws.conn.Close()
		}
		ws.mu.Unlock()

		wm.mu.Lock()
		wm.events = append(wm.events, wsEvent{Kind: "close", ConnID: id})
		delete(wm.conns, id)
		wm.mu.Unlock()
		wm.wake()
	}()

	for {
		msg, err := wm.readWSFrame(ws)
		if err != nil {
			if err != io.EOF {
				ws.mu.Lock()
				isClosed := ws.closed
				ws.mu.Unlock()
				if !isClosed {
					wm.mu.Lock()
					wm.events = append(wm.events, wsEvent{Kind: "error", ConnID: id, Data: err.Error()})
					wm.mu.Unlock()
					wm.wake()
				}
			}
			return
		}
		if msg == nil {
			// Close frame received.
			return
		}

		wm.mu.Lock()
		wm.events = append(wm.events, wsEvent{Kind: "message", ConnID: id, Data: *msg})
		wm.mu.Unlock()
		wm.wake()
	}
}

// send sends a text message to a WebSocket connection.
func (wm *wsManager) send(id int, data string) error {
	wm.mu.Lock()
	ws, ok := wm.conns[id]
	wm.mu.Unlock()
	if !ok {
		return fmt.Errorf("ws connection %d not found", id)
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.closed {
		return fmt.Errorf("ws connection %d is closed", id)
	}

	return writeWSFrame(ws.bufrw.Writer, 1, []byte(data)) // opcode 1 = text
}

// closeConn sends a close frame and closes the connection.
func (wm *wsManager) closeConn(id int) error {
	wm.mu.Lock()
	ws, ok := wm.conns[id]
	wm.mu.Unlock()
	if !ok {
		return nil
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.closed {
		return nil
	}
	ws.closed = true

	// Send close frame (best-effort).
	writeWSFrame(ws.bufrw.Writer, 8, nil) // opcode 8 = close
	ws.conn.Close()

	wm.mu.Lock()
	delete(wm.conns, id)
	wm.mu.Unlock()

	return nil
}

// drain returns all pending events and clears the queue.
func (wm *wsManager) drain() []wsEvent {
	wm.mu.Lock()
	events := wm.events
	wm.events = nil
	wm.mu.Unlock()
	return events
}

// hasActive returns true if there are active WebSocket connections.
func (wm *wsManager) hasActive() bool {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return len(wm.conns) > 0
}

// closeAll closes all active WebSocket connections.
func (wm *wsManager) closeAll() {
	wm.mu.Lock()
	conns := make(map[int]*wsConn, len(wm.conns))
	for id, ws := range wm.conns {
		conns[id] = ws
	}
	wm.conns = make(map[int]*wsConn)
	wm.mu.Unlock()

	for _, ws := range conns {
		ws.mu.Lock()
		if !ws.closed {
			ws.closed = true
			writeWSFrame(ws.bufrw.Writer, 8, nil)
			ws.conn.Close()
		}
		ws.mu.Unlock()
	}
}

// --- WebSocket frame protocol (RFC 6455) ---

// computeAcceptKey computes the Sec-WebSocket-Accept value.
func computeAcceptKey(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// headerContains checks if an HTTP header contains a specific value
// (case-insensitive, comma-separated).
func headerContains(h http.Header, key, value string) bool {
	for _, v := range h[http.CanonicalHeaderKey(key)] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(s), value) {
				return true
			}
		}
	}
	return false
}

const maxWSFrameSize = 16 << 20 // 16 MB

// readWSFrame reads a single WebSocket frame. Returns the message text,
// or nil for close frames. Handles masking and responds to Ping with Pong.
func (wm *wsManager) readWSFrame(ws *wsConn) (*string, error) {
	r := ws.bufrw.Reader

	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	opcode := header[0] & 0x0F
	masked := (header[1] & 0x80) != 0
	payloadLen := uint64(header[1] & 0x7F)

	switch payloadLen {
	case 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return nil, err
		}
		payloadLen = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return nil, err
		}
		payloadLen = binary.BigEndian.Uint64(ext)
	}

	if payloadLen > uint64(maxWSFrameSize) {
		return nil, fmt.Errorf("websocket frame too large: %d bytes (max %d)", payloadLen, maxWSFrameSize)
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}

	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}

	switch opcode {
	case 8: // Close
		return nil, nil
	case 9: // Ping — respond with Pong (RFC 6455 Section 5.5.3).
		ws.mu.Lock()
		if !ws.closed {
			writeWSFrame(ws.bufrw.Writer, 10, payload) // opcode 10 = Pong
		}
		ws.mu.Unlock()
		return wm.readWSFrame(ws) // Continue reading the next frame.
	case 10: // Pong — ignore.
		return wm.readWSFrame(ws)
	case 1, 2: // Text or Binary.
		msg := string(payload)
		return &msg, nil
	default:
		return nil, fmt.Errorf("unsupported websocket opcode: %d", opcode)
	}
}

// writeWSFrame writes a WebSocket frame. Server-to-client frames are NOT masked.
func writeWSFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	// FIN bit + opcode.
	if err := w.WriteByte(0x80 | opcode); err != nil {
		return err
	}

	// Payload length (no mask bit — server to client).
	payloadLen := len(payload)
	switch {
	case payloadLen <= 125:
		if err := w.WriteByte(byte(payloadLen)); err != nil {
			return err
		}
	case payloadLen <= 65535:
		if err := w.WriteByte(126); err != nil {
			return err
		}
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(payloadLen))
		if _, err := w.Write(lenBytes); err != nil {
			return err
		}
	default:
		if err := w.WriteByte(127); err != nil {
			return err
		}
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(payloadLen))
		if _, err := w.Write(lenBytes); err != nil {
			return err
		}
	}

	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}

	return w.Flush()
}

// --- Integration with bunServerState ---

// installWebSocket registers WebSocket Go callbacks and JS glue.
// Must be called with rt.mu held (called from installBunCompat).
func (r *Runtime) installWebSocket() error {
	wm := newWSManager()
	wm.wakeFn = r.Wake
	r.bunSrv.wsMgr = wm

	// __go_ws_send(connId, data) — send a message to a WebSocket client.
	if err := r.registerFuncLocked("__go_ws_send", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("ws_send: connId and data required")
		}
		connID, _ := args[0].(float64)
		data, _ := args[1].(string)
		return nil, wm.send(int(connID), data)
	}); err != nil {
		return err
	}

	// __go_ws_close(connId) — close a WebSocket connection.
	if err := r.registerFuncLocked("__go_ws_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("ws_close: connId required")
		}
		connID, _ := args[0].(float64)
		return nil, wm.closeConn(int(connID))
	}); err != nil {
		return err
	}

	// __go_ws_drain() — drain pending WebSocket events.
	if err := r.registerFuncLocked("__go_ws_drain", func(args []any) (any, error) {
		events := wm.drain()
		if len(events) == 0 {
			return "[]", nil
		}
		out, _ := json.Marshal(events)
		return string(out), nil
	}); err != nil {
		return err
	}

	return nil
}
