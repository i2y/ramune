package ramune

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// ---------- types ----------

type h2EventKind string

const (
	h2EventConnect  h2EventKind = "connect"
	h2EventResponse h2EventKind = "response"
	h2EventData     h2EventKind = "data"
	h2EventEnd      h2EventKind = "end"
	h2EventTrailers h2EventKind = "trailers"
	h2EventClose    h2EventKind = "close"
	h2EventError    h2EventKind = "error"
	h2EventStream   h2EventKind = "stream" // server: new incoming stream
	h2EventPing     h2EventKind = "ping"
)

type h2Event struct {
	Kind    h2EventKind       `json:"kind"`
	Data    string            `json:"data,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// h2Stream represents one HTTP/2 stream (request/response pair).
type h2Stream struct {
	mu     sync.Mutex
	id     int
	sessID int
	events []h2Event
	closed bool
	pipeW  *io.PipeWriter // JS writes request body here
	pipeR  *io.PipeReader
	resp   *http.Response
	// server-side
	serverReq      *http.Request
	serverW        http.ResponseWriter
	serverDone     chan struct{}
	serverDoneOnce sync.Once
}

// h2Session represents one HTTP/2 connection (client or server).
type h2Session struct {
	mu      sync.Mutex
	id      int
	events  []h2Event
	streams map[int]*h2Stream
	closed  bool
	// client-side
	rawConn net.Conn
	h2cc    *http2.ClientConn
	// server-side
	listener net.Listener
	server   *http.Server
}

// ---------- manager ----------

type http2Manager struct {
	mu       sync.Mutex
	sessions map[int]*h2Session
	nextID   int
	streamID int
	wakeFn   func()
}

func newHTTP2Manager() *http2Manager {
	return &http2Manager{
		sessions: make(map[int]*h2Session),
		nextID:   1,
		streamID: 1,
	}
}

func (m *http2Manager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions) > 0
}

func (m *http2Manager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, sess := range m.sessions {
		sess.mu.Lock()
		sess.closed = true
		for _, st := range sess.streams {
			if st.pipeW != nil {
				st.pipeW.Close()
			}
			if st.pipeR != nil {
				st.pipeR.Close()
			}
			if st.serverDone != nil {
				st.serverDoneOnce.Do(func() { close(st.serverDone) })
			}
		}
		if sess.rawConn != nil {
			sess.rawConn.Close()
		}
		if sess.listener != nil {
			sess.listener.Close()
		}
		if sess.server != nil {
			sess.server.Close()
		}
		sess.mu.Unlock()
		delete(m.sessions, id)
	}
}

// getStream looks up a session and stream by ID.
func (m *http2Manager) getStream(sessID, streamID int) (*h2Stream, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("h2: session %d not found", sessID)
	}
	sess.mu.Lock()
	st, ok := sess.streams[streamID]
	sess.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("h2: stream %d not found", streamID)
	}
	return st, nil
}

func (m *http2Manager) processEvents(r *Runtime) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if len(m.sessions) == 0 {
		m.mu.Unlock()
		return
	}

	type sessEvents struct {
		sessID int
		events []h2Event
		// per-stream events
		streamEvents map[int][]h2Event
	}

	var all []sessEvents

	for _, sess := range m.sessions {
		sess.mu.Lock()
		se := sessEvents{sessID: sess.id}
		if len(sess.events) > 0 {
			se.events = sess.events
			sess.events = nil
		}
		for _, st := range sess.streams {
			st.mu.Lock()
			if len(st.events) > 0 {
				if se.streamEvents == nil {
					se.streamEvents = make(map[int][]h2Event)
				}
				se.streamEvents[st.id] = st.events
				st.events = nil
			}
			if st.closed {
				delete(sess.streams, st.id)
			}
			st.mu.Unlock()
		}
		sess.mu.Unlock()

		if len(se.events) > 0 || len(se.streamEvents) > 0 {
			all = append(all, se)
		}
	}
	m.mu.Unlock()

	if len(all) == 0 {
		return
	}

	// Build JSON for JS delivery.
	type jsStreamEvent struct {
		StreamID int       `json:"streamId"`
		Events   []h2Event `json:"events"`
	}
	type jsDelivery struct {
		SessionID    int             `json:"sessionId"`
		Events       []h2Event       `json:"events,omitempty"`
		StreamEvents []jsStreamEvent `json:"streamEvents,omitempty"`
	}

	deliveries := make([]jsDelivery, 0, len(all))
	for _, se := range all {
		d := jsDelivery{SessionID: se.sessID, Events: se.events}
		for sid, sevs := range se.streamEvents {
			d.StreamEvents = append(d.StreamEvents, jsStreamEvent{StreamID: sid, Events: sevs})
		}
		deliveries = append(deliveries, d)
	}

	data, _ := json.Marshal(deliveries)
	r.execLocked("if(typeof __http2DeliverEvents==='function')__http2DeliverEvents(" + string(data) + ")")
}

// ---------- Go callbacks ----------

func (m *http2Manager) allocSessionID() int {
	id := m.nextID
	m.nextID++
	return id
}

func (m *http2Manager) allocStreamID() int {
	id := m.streamID
	m.streamID++
	return id
}

// goH2Connect implements http2.connect(authority, optsJSON) → sessionID.
func goH2Connect(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("http2.connect: authority required")
		}
		authority, _ := args[0].(string)
		if authority == "" {
			return nil, fmt.Errorf("http2.connect: authority required")
		}

		var useTLS bool
		var certCheck = true
		if len(args) >= 2 {
			if optsStr, ok := args[1].(string); ok && optsStr != "" {
				var opts map[string]any
				if err := json.Unmarshal([]byte(optsStr), &opts); err == nil {
					if v, ok := opts["rejectUnauthorized"]; ok {
						if b, ok := v.(bool); ok {
							certCheck = b
						}
					}
				}
			}
		}

		// Determine if TLS: authority starting with https:// or port 443.
		host := authority
		if strings.HasPrefix(host, "https://") {
			host = strings.TrimPrefix(host, "https://")
			useTLS = true
		} else if strings.HasPrefix(host, "http://") {
			host = strings.TrimPrefix(host, "http://")
		}
		if !strings.Contains(host, ":") {
			if useTLS {
				host += ":443"
			} else {
				host += ":80"
			}
		}

		m.mu.Lock()
		sessID := m.allocSessionID()
		sess := &h2Session{
			id:      sessID,
			streams: make(map[int]*h2Stream),
		}
		m.sessions[sessID] = sess
		m.mu.Unlock()

		// Connect in background goroutine.
		go func() {
			var conn net.Conn
			var err error

			if useTLS {
				tlsHost := host
				if h, _, e := net.SplitHostPort(host); e == nil {
					tlsHost = h
				}
				tlsCfg := &tls.Config{
					NextProtos:         []string{"h2"},
					ServerName:         tlsHost,
					InsecureSkipVerify: !certCheck,
				}
				conn, err = tls.Dial("tcp", host, tlsCfg)
			} else {
				conn, err = net.Dial("tcp", host)
			}

			if err != nil {
				sess.mu.Lock()
				sess.events = append(sess.events, h2Event{Kind: h2EventError, Data: err.Error()})
				sess.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
				return
			}

			t := &http2.Transport{
				AllowHTTP: true,
			}
			h2conn, err := t.NewClientConn(conn)
			if err != nil {
				conn.Close()
				sess.mu.Lock()
				sess.events = append(sess.events, h2Event{Kind: h2EventError, Data: err.Error()})
				sess.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
				return
			}

			sess.mu.Lock()
			sess.rawConn = conn
			sess.h2cc = h2conn
			sess.events = append(sess.events, h2Event{Kind: h2EventConnect})
			sess.mu.Unlock()

			if m.wakeFn != nil {
				m.wakeFn()
			}
		}()

		return float64(sessID), nil
	}
}

// goH2Request implements session.request(headers) → streamID.
func goH2Request(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("h2 request: sessionId and headersJSON required")
		}
		sessID := int(args[0].(float64))
		headersJSON, _ := args[1].(string)

		var headers map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			return nil, fmt.Errorf("h2 request: invalid headers JSON: %w", err)
		}

		m.mu.Lock()
		sess, ok := m.sessions[sessID]
		if !ok {
			m.mu.Unlock()
			return nil, fmt.Errorf("h2 request: session %d not found", sessID)
		}
		streamID := m.allocStreamID()
		m.mu.Unlock()

		pr, pw := io.Pipe()
		st := &h2Stream{
			id:     streamID,
			sessID: sessID,
			pipeW:  pw,
			pipeR:  pr,
		}

		sess.mu.Lock()
		if sess.h2cc == nil {
			sess.mu.Unlock()
			pw.Close()
			pr.Close()
			return nil, fmt.Errorf("h2 request: session not connected")
		}
		sess.streams[streamID] = st
		h2cc := sess.h2cc
		rawConn := sess.rawConn
		sess.mu.Unlock()

		// Build HTTP request.
		method := headers[":method"]
		if method == "" {
			method = "POST"
		}
		path := headers[":path"]
		if path == "" {
			path = "/"
		}
		scheme := headers[":scheme"]
		if scheme == "" {
			scheme = "https"
		}
		authority := headers[":authority"]
		if authority == "" {
			if h, _, err := net.SplitHostPort(rawConn.RemoteAddr().String()); err == nil {
				authority = h
			}
		}

		url := scheme + "://" + authority + path
		req, err := http.NewRequest(method, url, pr)
		if err != nil {
			pw.Close()
			pr.Close()
			return nil, fmt.Errorf("h2 request: %w", err)
		}

		// Set non-pseudo headers.
		for k, v := range headers {
			if !strings.HasPrefix(k, ":") {
				req.Header.Set(k, v)
			}
		}
		go func() {
			resp, err := h2cc.RoundTrip(req)
			if err != nil {
				st.mu.Lock()
				st.events = append(st.events, h2Event{Kind: h2EventError, Data: err.Error()})
				st.closed = true
				st.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
				return
			}
			defer resp.Body.Close()

			// Deliver response headers.
			respHeaders := make(map[string]string)
			respHeaders[":status"] = strconv.Itoa(resp.StatusCode)
			for k, v := range resp.Header {
				respHeaders[strings.ToLower(k)] = strings.Join(v, ", ")
			}
			st.mu.Lock()
			st.resp = resp
			st.events = append(st.events, h2Event{Kind: h2EventResponse, Headers: respHeaders})
			st.mu.Unlock()
			if m.wakeFn != nil {
				m.wakeFn()
			}

			// Read response body in chunks.
			buf := make([]byte, 32*1024)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					st.mu.Lock()
					st.events = append(st.events, h2Event{Kind: h2EventData, Data: string(buf[:n])})
					st.mu.Unlock()
					if m.wakeFn != nil {
						m.wakeFn()
					}
				}
				if readErr != nil {
					break
				}
			}

			// Deliver trailers if present.
			if len(resp.Trailer) > 0 {
				trailers := make(map[string]string)
				for k, v := range resp.Trailer {
					trailers[strings.ToLower(k)] = strings.Join(v, ", ")
				}
				st.mu.Lock()
				st.events = append(st.events, h2Event{Kind: h2EventTrailers, Headers: trailers})
				st.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
			}

			// End of stream.
			st.mu.Lock()
			st.events = append(st.events, h2Event{Kind: h2EventEnd})
			st.closed = true
			st.mu.Unlock()
			if m.wakeFn != nil {
				m.wakeFn()
			}
		}()

		return float64(streamID), nil
	}
}

// goH2StreamWrite writes data to an HTTP/2 stream's request body.
func goH2StreamWrite(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("h2 stream write: sessionId, streamId, data required")
		}
		st, err := m.getStream(int(args[0].(float64)), int(args[1].(float64)))
		if err != nil {
			return nil, err
		}
		data, _ := args[2].(string)
		if st.pipeW != nil {
			if _, err := st.pipeW.Write([]byte(data)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
}

// goH2StreamEnd closes the write side of an HTTP/2 stream.
func goH2StreamEnd(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("h2 stream end: sessionId, streamId required")
		}
		st, err := m.getStream(int(args[0].(float64)), int(args[1].(float64)))
		if err != nil {
			return nil, err
		}
		if st.pipeW != nil {
			st.pipeW.Close()
		}
		return nil, nil
	}
}

// goH2SessionClose closes an HTTP/2 session.
func goH2SessionClose(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("h2 session close: sessionId required")
		}
		sessID := int(args[0].(float64))

		m.mu.Lock()
		sess, ok := m.sessions[sessID]
		if ok {
			delete(m.sessions, sessID)
		}
		m.mu.Unlock()
		if !ok {
			return nil, nil
		}

		sess.mu.Lock()
		sess.closed = true
		if sess.rawConn != nil {
			sess.rawConn.Close()
		}
		if sess.listener != nil {
			sess.listener.Close()
		}
		if sess.server != nil {
			sess.server.Close()
		}
		for _, st := range sess.streams {
			if st.pipeW != nil {
				st.pipeW.Close()
			}
			if st.pipeR != nil {
				st.pipeR.Close()
			}
			if st.serverDone != nil {
				st.serverDoneOnce.Do(func() { close(st.serverDone) })
			}
		}
		sess.mu.Unlock()
		return nil, nil
	}
}

// goH2CreateServer creates an HTTP/2 server.
func goH2CreateServer(m *http2Manager, useTLS bool) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("h2 createServer: optsJSON required")
		}
		optsJSON, _ := args[0].(string)

		var opts struct {
			Port int    `json:"port"`
			Host string `json:"host"`
			Cert string `json:"cert"`
			Key  string `json:"key"`
		}
		if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
			return nil, fmt.Errorf("h2 createServer: invalid opts: %w", err)
		}
		if opts.Host == "" {
			opts.Host = "0.0.0.0"
		}

		addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)

		var ln net.Listener
		var err error

		if useTLS {
			cert, certErr := tls.X509KeyPair([]byte(opts.Cert), []byte(opts.Key))
			if certErr != nil {
				return nil, fmt.Errorf("h2 createSecureServer: %w", certErr)
			}
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{cert},
				NextProtos:   []string{"h2", "http/1.1"},
			}
			ln, err = tls.Listen("tcp", addr, tlsCfg)
		} else {
			ln, err = net.Listen("tcp", addr)
		}
		if err != nil {
			return nil, fmt.Errorf("h2 createServer: listen: %w", err)
		}

		m.mu.Lock()
		sessID := m.allocSessionID()

		sess := &h2Session{
			id:       sessID,
			streams:  make(map[int]*h2Stream),
			listener: ln,
		}
		m.sessions[sessID] = sess
		m.mu.Unlock()

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			m.mu.Lock()
			streamID := m.allocStreamID()
			m.mu.Unlock()

			done := make(chan struct{})
			st := &h2Stream{
				id:         streamID,
				sessID:     sessID,
				serverReq:  req,
				serverW:    w,
				serverDone: done,
			}

			// Collect request headers.
			reqHeaders := make(map[string]string)
			reqHeaders[":method"] = req.Method
			reqHeaders[":path"] = req.URL.RequestURI()
			reqHeaders[":scheme"] = "https"
			if req.TLS == nil {
				reqHeaders[":scheme"] = "http"
			}
			reqHeaders[":authority"] = req.Host
			for k, v := range req.Header {
				reqHeaders[strings.ToLower(k)] = strings.Join(v, ", ")
			}

			sess.mu.Lock()
			sess.streams[streamID] = st
			sess.events = append(sess.events, h2Event{
				Kind:    h2EventStream,
				Data:    strconv.Itoa(streamID),
				Headers: reqHeaders,
			})
			sess.mu.Unlock()

			if m.wakeFn != nil {
				m.wakeFn()
			}

			// Read request body and deliver as data events.
			go func() {
				buf := make([]byte, 32*1024)
				for {
					n, readErr := req.Body.Read(buf)
					if n > 0 {
						st.mu.Lock()
						st.events = append(st.events, h2Event{Kind: h2EventData, Data: string(buf[:n])})
						st.mu.Unlock()
						if m.wakeFn != nil {
							m.wakeFn()
						}
					}
					if readErr != nil {
						break
					}
				}
				st.mu.Lock()
				st.events = append(st.events, h2Event{Kind: h2EventEnd})
				st.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
			}()

			// Wait for JS to finish processing (stream.respond + stream.end).
			<-done
		})

		srv := &http.Server{Handler: mux}
		if !useTLS {
			// For cleartext HTTP/2, wrap with h2c handler.
			h2s := &http2.Server{}
			srv.Handler = h2c.NewHandler(mux, h2s)
		} else {
			// For TLS, configure HTTP/2 on the server.
			if err := http2.ConfigureServer(srv, nil); err != nil {
				ln.Close()
				return nil, fmt.Errorf("h2 createSecureServer: configure: %w", err)
			}
		}

		sess.mu.Lock()
		sess.server = srv
		sess.mu.Unlock()

		// Actual port (for ephemeral :0).
		actualPort := ln.Addr().(*net.TCPAddr).Port

		go srv.Serve(ln)

		result := map[string]any{
			"sessionId": float64(sessID),
			"port":      float64(actualPort),
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	}
}

// goH2StreamRespond sends response headers on a server stream.
func goH2StreamRespond(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("h2 respond: sessionId, streamId, headersJSON required")
		}
		st, err := m.getStream(int(args[0].(float64)), int(args[1].(float64)))
		if err != nil {
			return nil, err
		}
		headersJSON, _ := args[2].(string)

		var headers map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			return nil, fmt.Errorf("h2 respond: invalid headers: %w", err)
		}

		if st.serverW == nil {
			return nil, fmt.Errorf("h2 respond: not a server stream")
		}

		status := 200
		for k, v := range headers {
			if k == ":status" {
				if s, err := strconv.Atoi(v); err == nil {
					status = s
				}
				continue
			}
			if strings.HasPrefix(k, ":") {
				continue
			}
			st.serverW.Header().Set(k, v)
		}
		st.serverW.WriteHeader(status)
		if f, ok := st.serverW.(http.Flusher); ok {
			f.Flush()
		}
		return nil, nil
	}
}

// goH2ServerStreamWrite writes data to a server stream response.
func goH2ServerStreamWrite(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("h2 server write: sessionId, streamId, data required")
		}
		st, err := m.getStream(int(args[0].(float64)), int(args[1].(float64)))
		if err != nil {
			return nil, err
		}
		if st.serverW != nil {
			st.serverW.Write([]byte(args[2].(string)))
			if f, ok := st.serverW.(http.Flusher); ok {
				f.Flush()
			}
		}
		return nil, nil
	}
}

// goH2ServerStreamEnd ends a server stream.
func goH2ServerStreamEnd(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("h2 server end: sessionId, streamId required")
		}
		st, err := m.getStream(int(args[0].(float64)), int(args[1].(float64)))
		if err != nil {
			return nil, nil
		}
		st.mu.Lock()
		st.closed = true
		st.mu.Unlock()
		if st.serverDone != nil {
			st.serverDoneOnce.Do(func() { close(st.serverDone) })
		}
		return nil, nil
	}
}

// goH2SendTrailers sends trailing headers on a server stream.
func goH2SendTrailers(m *http2Manager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("h2 sendTrailers: sessionId, streamId, headersJSON required")
		}
		st, err := m.getStream(int(args[0].(float64)), int(args[1].(float64)))
		if err != nil {
			return nil, nil
		}
		headersJSON, _ := args[2].(string)

		var trailers map[string]string
		if err := json.Unmarshal([]byte(headersJSON), &trailers); err != nil {
			return nil, fmt.Errorf("h2 sendTrailers: invalid headers: %w", err)
		}
		if st.serverW != nil {
			for k, v := range trailers {
				st.serverW.Header().Set(http.TrailerPrefix+k, v)
			}
		}
		return nil, nil
	}
}

// ---------- installation ----------

func (r *Runtime) installHTTP2() error {
	m := newHTTP2Manager()
	m.wakeFn = r.Wake
	r.http2Mgr = m

	for name, fn := range map[string]GoFunc{
		"__go_h2_connect":              goH2Connect(m),
		"__go_h2_request":              goH2Request(m),
		"__go_h2_stream_write":         goH2StreamWrite(m),
		"__go_h2_stream_end":           goH2StreamEnd(m),
		"__go_h2_session_close":        goH2SessionClose(m),
		"__go_h2_create_server":        goH2CreateServer(m, false),
		"__go_h2_create_secure_server": goH2CreateServer(m, true),
		"__go_h2_respond":              goH2StreamRespond(m),
		"__go_h2_server_write":         goH2ServerStreamWrite(m),
		"__go_h2_server_end":           goH2ServerStreamEnd(m),
		"__go_h2_send_trailers":        goH2SendTrailers(m),
	} {
		if err := r.registerFuncLocked(name, fn); err != nil {
			return err
		}
	}

	return r.execLocked(http2JSSource())
}

// ---------- JS source ----------

func http2JSSource() string {
	return strings.TrimSpace(`
(function() {
	var EventEmitter = globalThis.require('events').EventEmitter;
	var stream = globalThis.require('stream');

	var __activeSessions = {};

	// --- Http2Session ---
	class Http2Session extends EventEmitter {
		constructor(type, id) {
			super();
			this._type = type;
			this._id = id;
			this._closed = false;
			this._streams = {};
			this.alpnProtocol = 'h2';
			this.encrypted = true;
			this.socket = {};
		}
		get closed() { return this._closed; }
		close(cb) {
			if (this._closed) return;
			this._closed = true;
			delete __activeSessions[String(this._id)];
			try { __go_h2_session_close(this._id); } catch(e) {}
			this.emit('close');
			if (typeof cb === 'function') cb();
		}
		destroy(err) {
			this.close();
			if (err) this.emit('error', err);
		}
		ping(cb) {
			if (typeof cb === 'function') setImmediate(function() { cb(null, 0, Buffer.alloc(8)); });
			return true;
		}
		settings(settings) {
			this.localSettings = Object.assign(this.localSettings || {}, settings);
		}
		get remoteSettings() { return { headerTableSize: 4096, enablePush: false, maxConcurrentStreams: 100, initialWindowSize: 65535, maxFrameSize: 16384, maxHeaderListSize: 65535 }; }
		get localSettings() { return this._localSettings || { headerTableSize: 4096, enablePush: false, maxConcurrentStreams: 100, initialWindowSize: 65535, maxFrameSize: 16384, maxHeaderListSize: 65535 }; }
		set localSettings(v) { this._localSettings = v; }
	}

	// --- ClientHttp2Session ---
	class ClientHttp2Session extends Http2Session {
		constructor(id) {
			super('client', id);
		}
		request(headers, opts) {
			headers = headers || {};
			var headersJSON = JSON.stringify(headers);
			var streamId = __go_h2_request(this._id, headersJSON);
			var s = new ClientHttp2Stream(this._id, streamId, headers);
			this._streams[String(streamId)] = s;
			return s;
		}
	}

	// --- ServerHttp2Session ---
	class ServerHttp2Session extends Http2Session {
		constructor(id) {
			super('server', id);
		}
	}

	// --- Http2Stream ---
	class Http2Stream extends stream.Duplex {
		constructor(sessId, streamId) {
			super();
			this._sessId = sessId;
			this._streamId = streamId;
			this._headers = null;
			this._trailers = null;
			this.sentHeaders = {};
			this.sentTrailers = {};
			this.rstCode = -1;
			this.aborted = false;
		}
		_write(chunk, encoding, cb) {
			cb();
		}
		close(code) {
			this.rstCode = code || 0;
			this.emit('close');
		}
	}

	// --- ClientHttp2Stream ---
	class ClientHttp2Stream extends Http2Stream {
		constructor(sessId, streamId, sentHeaders) {
			super(sessId, streamId);
			this.sentHeaders = sentHeaders;
		}
		_write(chunk, encoding, cb) {
			try {
				__go_h2_stream_write(this._sessId, this._streamId, String(chunk));
			} catch(e) {
				this.emit('error', e);
			}
			cb();
		}
		end(chunk, encoding, cb) {
			if (typeof chunk === 'function') { cb = chunk; chunk = undefined; }
			if (typeof encoding === 'function') { cb = encoding; encoding = undefined; }
			if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
			try {
				__go_h2_stream_end(this._sessId, this._streamId);
			} catch(e) {}
			this._finished = true;
			this.emit('finish');
			if (cb) cb();
			return this;
		}
	}

	// --- ServerHttp2Stream ---
	class ServerHttp2Stream extends Http2Stream {
		constructor(sessId, streamId, headers) {
			super(sessId, streamId);
			this._headers = headers;
			this._responded = false;
		}
		respond(headers, opts) {
			if (this._responded) return;
			this._responded = true;
			this.sentHeaders = headers || {};
			var headersJSON = JSON.stringify(this.sentHeaders);
			__go_h2_respond(this._sessId, this._streamId, headersJSON);
		}
		_write(chunk, encoding, cb) {
			try {
				__go_h2_server_write(this._sessId, this._streamId, String(chunk));
			} catch(e) {
				this.emit('error', e);
			}
			cb();
		}
		end(chunk, encoding, cb) {
			if (typeof chunk === 'function') { cb = chunk; chunk = undefined; }
			if (typeof encoding === 'function') { cb = encoding; encoding = undefined; }
			if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
			try {
				__go_h2_server_end(this._sessId, this._streamId);
			} catch(e) {}
			this._finished = true;
			this.emit('finish');
			if (cb) cb();
			return this;
		}
		sendTrailers(trailers) {
			this.sentTrailers = trailers;
			var headersJSON = JSON.stringify(trailers);
			try { __go_h2_send_trailers(this._sessId, this._streamId, headersJSON); } catch(e) {}
		}
	}

	// --- Event delivery ---
	globalThis.__http2DeliverEvents = function(deliveries) {
		for (var i = 0; i < deliveries.length; i++) {
			var d = deliveries[i];
			var sess = __activeSessions[String(d.sessionId)];
			if (!sess) continue;

			// Session-level events.
			if (d.events) {
				for (var j = 0; j < d.events.length; j++) {
					var ev = d.events[j];
					if (ev.kind === 'connect') {
						sess.emit('connect', sess, {});
					} else if (ev.kind === 'error') {
						sess.emit('error', new Error(ev.data));
					} else if (ev.kind === 'stream') {
						var streamId = parseInt(ev.data, 10);
						var st = new ServerHttp2Stream(sess._id, streamId, ev.headers);
						sess._streams[String(streamId)] = st;
						sess.emit('stream', st, ev.headers);
					} else if (ev.kind === 'close') {
						sess._closed = true;
						sess.emit('close');
					}
				}
			}

			// Stream-level events.
			if (d.streamEvents) {
				for (var k = 0; k < d.streamEvents.length; k++) {
					var se = d.streamEvents[k];
					var st = sess._streams[String(se.streamId)];
					if (!st) continue;

					for (var l = 0; l < se.events.length; l++) {
						var sev = se.events[l];
						if (sev.kind === 'response') {
							st._headers = sev.headers;
							st.emit('response', sev.headers, 0);
						} else if (sev.kind === 'data') {
							st.push(sev.data);
						} else if (sev.kind === 'trailers') {
							st._trailers = sev.headers;
							st.emit('trailers', sev.headers);
						} else if (sev.kind === 'end') {
							st.push(null);
						} else if (sev.kind === 'error') {
							st.emit('error', new Error(sev.data));
						} else if (sev.kind === 'close') {
							st.emit('close');
						}
					}
				}
			}
		}
	};

	function _createServerSession(goFn, opts, handler) {
		if (typeof opts === 'function') { handler = opts; opts = {}; }
		opts = opts || {};
		var result = JSON.parse(goFn(JSON.stringify(opts)));
		var session = new ServerHttp2Session(result.sessionId);
		session._port = result.port;
		__activeSessions[String(result.sessionId)] = session;
		if (typeof handler === 'function') session.on('stream', handler);
		session.listen = function(port, host, cb) {
			if (typeof host === 'function') { cb = host; host = undefined; }
			if (typeof cb === 'function') cb();
			return session;
		};
		session.address = function() { return { port: session._port, family: 'IPv4', address: opts.host || '0.0.0.0' }; };
		session.close = function(cb) { Http2Session.prototype.close.call(session, cb); };
		return session;
	}

	var http2Module = {
		connect: function(authority, opts, cb) {
			if (typeof opts === 'function') { cb = opts; opts = undefined; }
			var optsJSON = opts ? JSON.stringify(opts) : '';
			var sessionId = __go_h2_connect(authority, optsJSON);
			var session = new ClientHttp2Session(sessionId);
			__activeSessions[String(sessionId)] = session;
			if (typeof cb === 'function') session.once('connect', cb);
			return session;
		},
		createServer: function(opts, handler) {
			return _createServerSession(__go_h2_create_server, opts, handler);
		},
		createSecureServer: function(opts, handler) {
			return _createServerSession(__go_h2_create_secure_server, opts, handler);
		},
		constants: {
			NGHTTP2_SESSION_SERVER: 0,
			NGHTTP2_SESSION_CLIENT: 1,
			NGHTTP2_NO_ERROR: 0,
			NGHTTP2_PROTOCOL_ERROR: 1,
			NGHTTP2_INTERNAL_ERROR: 2,
			NGHTTP2_FLOW_CONTROL_ERROR: 3,
			NGHTTP2_SETTINGS_TIMEOUT: 4,
			NGHTTP2_STREAM_CLOSED: 5,
			NGHTTP2_FRAME_SIZE_ERROR: 6,
			NGHTTP2_REFUSED_STREAM: 7,
			NGHTTP2_CANCEL: 8,
			NGHTTP2_COMPRESSION_ERROR: 9,
			NGHTTP2_CONNECT_ERROR: 10,
			NGHTTP2_ENHANCE_YOUR_CALM: 11,
			NGHTTP2_INADEQUATE_SECURITY: 12,
			NGHTTP2_HTTP_1_1_REQUIRED: 13,
			HTTP2_HEADER_STATUS: ':status',
			HTTP2_HEADER_METHOD: ':method',
			HTTP2_HEADER_AUTHORITY: ':authority',
			HTTP2_HEADER_SCHEME: ':scheme',
			HTTP2_HEADER_PATH: ':path',
			HTTP2_HEADER_CONTENT_TYPE: 'content-type',
			HTTP2_HEADER_CONTENT_LENGTH: 'content-length',
			HTTP2_HEADER_ACCEPT_ENCODING: 'accept-encoding',
			HTTP2_HEADER_CONTENT_ENCODING: 'content-encoding',
			HTTP2_METHOD_GET: 'GET',
			HTTP2_METHOD_POST: 'POST',
			HTTP_STATUS_OK: 200,
			HTTP_STATUS_NO_CONTENT: 204,
			HTTP_STATUS_NOT_FOUND: 404,
			HTTP_STATUS_INTERNAL_SERVER_ERROR: 500
		},
		sensitiveHeaders: Symbol('http2.sensitiveHeaders'),
		getDefaultSettings: function() {
			return { headerTableSize: 4096, enablePush: false, maxConcurrentStreams: 100, initialWindowSize: 65535, maxFrameSize: 16384, maxHeaderListSize: 65535 };
		}
	};

	// Register module.
	if (typeof globalThis.require !== 'undefined' && globalThis.require._modules) {
		globalThis.require._modules['http2'] = http2Module;
	}
})();
`)
}
