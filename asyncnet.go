package ramune

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

// Socket event kind constants.
const (
	sockEventConnect = "connect"
	sockEventData    = "data"
	sockEventEnd     = "end"
	sockEventClose   = "close"
	sockEventError   = "error"
)

// asyncSocket represents a managed TCP/TLS connection.
type asyncSocket struct {
	conn   net.Conn
	mu     sync.Mutex
	events []socketEvent
	closed bool
}

type socketEvent struct {
	Kind string `json:"Kind"` // "data", "end", "close", "error", "connect"
	Data string `json:"Data,omitempty"`
}

// socketManager tracks active sockets for a Runtime.
type socketManager struct {
	mu      sync.Mutex
	sockets map[int]*asyncSocket
	nextID  int
	wakeFn  func()
}

func newSocketManager() *socketManager {
	return &socketManager{
		sockets: make(map[int]*asyncSocket),
		nextID:  1,
	}
}

// startReader starts a background goroutine that reads from the socket's conn
// and queues data/end/error/close events.
func (sm *socketManager) startReader(sock *asyncSocket) {
	go func() {
		reader := bufio.NewReader(sock.conn)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				sock.mu.Lock()
				sock.events = append(sock.events, socketEvent{Kind: sockEventData, Data: string(buf[:n])})
				sock.mu.Unlock()
				if sm.wakeFn != nil {
					sm.wakeFn()
				}
			}
			if err != nil {
				sock.mu.Lock()
				if err == io.EOF {
					sock.events = append(sock.events, socketEvent{Kind: sockEventEnd})
				} else if !sock.closed {
					sock.events = append(sock.events, socketEvent{Kind: sockEventError, Data: err.Error()})
				}
				sock.events = append(sock.events, socketEvent{Kind: sockEventClose})
				sock.closed = true
				sock.mu.Unlock()
				if sm.wakeFn != nil {
					sm.wakeFn()
				}
				return
			}
		}
	}()
}

func (sm *socketManager) connect(host string, port int, useTLS bool) (int, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	var conn net.Conn
	var err error

	if useTLS {
		conn, err = tls.Dial("tcp", addr, &tls.Config{})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return 0, err
	}

	return sm.registerExisting(conn), nil
}

// registerExisting wraps an already-established net.Conn as a socket with
// a background reader. Used by TCP server to register accepted connections.
func (sm *socketManager) registerExisting(conn net.Conn) int {
	sock := &asyncSocket{conn: conn}

	sm.mu.Lock()
	id := sm.nextID
	sm.nextID++
	sm.sockets[id] = sock
	sm.mu.Unlock()

	sock.mu.Lock()
	sock.events = append(sock.events, socketEvent{Kind: sockEventConnect})
	sock.mu.Unlock()
	if sm.wakeFn != nil {
		sm.wakeFn()
	}

	sm.startReader(sock)
	return id
}

func (sm *socketManager) write(id int, data string) error {
	sm.mu.Lock()
	sock, ok := sm.sockets[id]
	sm.mu.Unlock()
	if !ok {
		return fmt.Errorf("socket %d not found", id)
	}
	_, err := io.WriteString(sock.conn, data)
	return err
}

func (sm *socketManager) end(id int, data string) error {
	sm.mu.Lock()
	sock, ok := sm.sockets[id]
	sm.mu.Unlock()
	if !ok {
		return fmt.Errorf("socket %d not found", id)
	}
	if data != "" {
		io.WriteString(sock.conn, data)
	}
	// Close write side (half-close for TCP).
	if tc, ok := sock.conn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}
	return nil
}

func (sm *socketManager) destroy(id int) error {
	sm.mu.Lock()
	sock, ok := sm.sockets[id]
	sm.mu.Unlock()
	if !ok {
		return nil
	}
	sock.mu.Lock()
	sock.closed = true
	sock.mu.Unlock()
	sock.conn.Close()
	sm.mu.Lock()
	delete(sm.sockets, id)
	sm.mu.Unlock()
	return nil
}

func (sm *socketManager) drain(id int) []socketEvent {
	sm.mu.Lock()
	sock, ok := sm.sockets[id]
	sm.mu.Unlock()
	if !ok {
		return nil
	}
	sock.mu.Lock()
	events := sock.events
	sock.events = nil
	closed := sock.closed
	sock.mu.Unlock()

	if closed {
		sm.mu.Lock()
		delete(sm.sockets, id)
		sm.mu.Unlock()
	}
	return events
}

func (sm *socketManager) hasActive() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.sockets) > 0
}

// processEvents drains events from all sockets and delivers them to JS.
func (sm *socketManager) processEvents(r *Runtime) {
	if sm == nil {
		return
	}
	sm.mu.Lock()
	if len(sm.sockets) == 0 {
		sm.mu.Unlock()
		return
	}
	type idEvents struct {
		id     int
		events []socketEvent
		closed bool
	}
	var all []idEvents
	for id, sock := range sm.sockets {
		sock.mu.Lock()
		if len(sock.events) > 0 {
			all = append(all, idEvents{id, sock.events, sock.closed})
			sock.events = nil
		}
		sock.mu.Unlock()
	}
	// Remove closed sockets.
	for _, ie := range all {
		if ie.closed {
			delete(sm.sockets, ie.id)
		}
	}
	sm.mu.Unlock()

	if len(all) == 0 {
		return
	}

	evMap := make(map[string][]socketEvent, len(all))
	for _, ie := range all {
		evMap[itoa(ie.id)] = ie.events
	}
	data, _ := json.Marshal(evMap)
	r.execLocked("if(typeof __socketDeliverEvents==='function')__socketDeliverEvents(" + string(data) + ")")
}

// --- Go callbacks ---

func goNetConnect(sm *socketManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("connect: host and port required")
		}
		host, _ := args[0].(string)
		port, _ := args[1].(float64)
		useTLS := false
		if len(args) > 2 {
			if v, ok := args[2].(bool); ok {
				useTLS = v
			}
		}
		id, err := sm.connect(host, int(port), useTLS)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}
}

func goNetWrite(sm *socketManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("write: id and data required")
		}
		id, _ := args[0].(float64)
		data, _ := args[1].(string)
		return nil, sm.write(int(id), data)
	}
}

func goNetEnd(sm *socketManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("end: id required")
		}
		id, _ := args[0].(float64)
		data := ""
		if len(args) > 1 {
			data, _ = args[1].(string)
		}
		return nil, sm.end(int(id), data)
	}
}

func goNetDestroy(sm *socketManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("destroy: id required")
		}
		id, _ := args[0].(float64)
		return nil, sm.destroy(int(id))
	}
}

func goNetDrain(sm *socketManager) GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return "[]", nil
		}
		id, _ := args[0].(float64)
		events := sm.drain(int(id))
		if len(events) == 0 {
			return "[]", nil
		}
		out, _ := json.Marshal(events)
		return string(out), nil
	}
}

func goNetHasActive(sm *socketManager) GoFunc {
	return func(args []any) (any, error) {
		return sm.hasActive(), nil
	}
}

// installAsyncNet registers the async networking callbacks.
func (r *Runtime) installAsyncNet() error {
	sm := newSocketManager()
	sm.wakeFn = r.Wake
	r.sockMgr = sm

	if err := r.registerFuncLocked("__go_net_connect", goNetConnect(sm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_net_write", goNetWrite(sm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_net_end", goNetEnd(sm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_net_destroy", goNetDestroy(sm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_net_drain", goNetDrain(sm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_net_has_active", goNetHasActive(sm)); err != nil {
		return err
	}

	return r.execLocked(asyncNetJSSource())
}

func asyncNetJSSource() string {
	return strings.TrimSpace(`
(function() {
	var EventEmitter = globalThis.require('events').EventEmitter;
	var netModule = globalThis.require('net');

	function Socket(opts) {
		EventEmitter.call(this);
		this._id = null;
		this._connected = false;
		this._destroyed = false;
		this.readable = true;
		this.writable = true;
		this.remoteAddress = '';
		this.remotePort = 0;
	}
	Socket.prototype = Object.create(EventEmitter.prototype);
	Socket.prototype.constructor = Socket;

	Socket.prototype.connect = function(portOrOpts, host, cb) {
		var port, useTLS = false;
		if (typeof portOrOpts === 'object') {
			port = portOrOpts.port;
			host = portOrOpts.host || 'localhost';
			useTLS = !!portOrOpts.tls;
			if (typeof host === 'function') { cb = host; host = 'localhost'; }
		} else {
			port = portOrOpts;
			host = host || 'localhost';
		}
		if (typeof cb === 'function') this.once('connect', cb);

		this.remoteAddress = host;
		this.remotePort = port;

		var self = this;
		try {
			self._id = __go_net_connect(host, port, useTLS);
		} catch(e) {
			setImmediate(function() { self.emit('error', e); });
			return self;
		}

		// Register in socket registry for event delivery by Go.
		__activeSockets[String(self._id)] = self;

		return self;
	};

	Socket.prototype.write = function(data, encoding, cb) {
		if (typeof encoding === 'function') { cb = encoding; }
		if (this._id != null) {
			try { __go_net_write(this._id, String(data)); }
			catch(e) { this.emit('error', e); }
		}
		if (cb) cb();
		return true;
	};

	Socket.prototype.end = function(data, encoding, cb) {
		if (typeof data === 'function') { cb = data; data = undefined; }
		if (this._id != null) {
			try { __go_net_end(this._id, data ? String(data) : ''); }
			catch(e) {}
		}
		if (cb) cb();
		return this;
	};

	Socket.prototype.destroy = function(err) {
		this._destroyed = true;
		if (this._id != null) {
			try { __go_net_destroy(this._id); } catch(e) {}
		}
		if (err) this.emit('error', err);
		this.emit('close');
		return this;
	};

	Socket.prototype.setEncoding = function() { return this; };
	Socket.prototype.setNoDelay = function() { return this; };
	Socket.prototype.setKeepAlive = function() { return this; };
	Socket.prototype.setTimeout = function(ms, cb) {
		if (cb) this.once('timeout', cb);
		return this;
	};
	Socket.prototype.ref = function() { return this; };
	Socket.prototype.unref = function() { return this; };
	Socket.prototype.pipe = function(dest) {
		this.on('data', function(d) { dest.write(d); });
		this.on('end', function() { dest.end(); });
		return dest;
	};

	// Override net module.
	netModule.Socket = Socket;
	netModule.createConnection = function(portOrOpts, host, cb) {
		var sock = new Socket();
		return sock.connect(portOrOpts, host, cb);
	};
	netModule.connect = netModule.createConnection;

	// TLS module uses same Socket with tls flag.
	var tlsModule = globalThis.require('tls');
	tlsModule.connect = function(opts, cb) {
		if (typeof opts === 'number') opts = {port: opts, host: 'localhost'};
		opts.tls = true;
		var sock = new Socket();
		sock.once('connect', function() { sock.emit('secureConnect'); });
		if (cb) sock.once('secureConnect', cb);
		return sock.connect(opts);
	};

	// Registry of active sockets for event delivery by Go.
	// Exposed on globalThis so TCP server can register accepted connections.
	var __activeSockets = {};
	globalThis.__activeSockets = __activeSockets;

	// Called by Go during event loop tick to deliver socket events.
	globalThis.__socketDeliverEvents = function(eventsMap) {
		var ids = Object.keys(eventsMap);
		for (var s = 0; s < ids.length; s++) {
			var id = ids[s];
			var sock = __activeSockets[id];
			if (!sock) continue;
			var events = eventsMap[id];
			for (var i = 0; i < events.length; i++) {
				var ev = events[i];
				if (ev.Kind === 'connect') {
					sock._connected = true;
					sock.emit('connect');
				} else if (ev.Kind === 'data') {
					sock.emit('data', ev.Data);
				} else if (ev.Kind === 'end') {
					sock.emit('end');
				} else if (ev.Kind === 'error') {
					sock.emit('error', new Error(ev.Data));
				} else if (ev.Kind === 'close') {
					sock._connected = false;
					sock.emit('close');
					delete __activeSockets[id];
				}
			}
		}
	};
})();
`)
}
