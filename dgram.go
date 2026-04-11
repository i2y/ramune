package ramune

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
)

type udpEvent struct {
	Kind    string `json:"Kind"`
	ID      int    `json:"ID"`
	Data    string `json:"Data,omitempty"`
	Address string `json:"Address,omitempty"`
	Port    int    `json:"Port,omitempty"`
}

type udpSocket struct {
	conn    *net.UDPConn
	id      int
	network string
	closed  bool
}

type udpManager struct {
	mu      sync.Mutex
	sockets map[int]*udpSocket
	events  []udpEvent
	nextID  int
	wakeFn  func()
}

func newUDPManager(wakeFn func()) *udpManager {
	return &udpManager{
		sockets: make(map[int]*udpSocket),
		wakeFn:  wakeFn,
	}
}

func (m *udpManager) createSocket(socketType string) (int, error) {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	sock := &udpSocket{id: id}
	sock.network = "udp4"
	if socketType == "udp6" {
		sock.network = "udp6"
	}
	m.sockets[id] = sock
	m.mu.Unlock()
	return id, nil
}

func (m *udpManager) bind(id int, port int, address string) (int, error) {
	m.mu.Lock()
	sock, ok := m.sockets[id]
	m.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("dgram: socket %d not found", id)
	}

	if sock.conn != nil {
		sock.conn.Close()
	}

	network := sock.network
	if address == "" {
		address = "0.0.0.0"
	}
	addr, err := net.ResolveUDPAddr(network, net.JoinHostPort(address, fmt.Sprintf("%d", port)))
	if err != nil {
		return 0, err
	}
	conn, err := net.ListenUDP(network, addr)
	if err != nil {
		return 0, err
	}
	sock.conn = conn

	actualPort := conn.LocalAddr().(*net.UDPAddr).Port

	m.mu.Lock()
	m.events = append(m.events, udpEvent{Kind: "listening", ID: id, Port: actualPort})
	m.mu.Unlock()
	if m.wakeFn != nil {
		m.wakeFn()
	}

	go m.readLoop(sock)
	return actualPort, nil
}

func (m *udpManager) send(id int, data string, port int, address string) error {
	m.mu.Lock()
	sock, ok := m.sockets[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("dgram: socket %d not found", id)
	}

	// Auto-bind if not yet bound.
	if sock.conn == nil {
		bindAddr, err := net.ResolveUDPAddr(sock.network, ":0")
		if err != nil {
			m.mu.Unlock()
			return err
		}
		conn, err := net.ListenUDP(sock.network, bindAddr)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		sock.conn = conn
		go m.readLoop(sock)
	}
	conn := sock.conn
	m.mu.Unlock()

	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(address, fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	_, err = conn.WriteToUDP([]byte(data), addr)
	return err
}

func (m *udpManager) closeSocket(id int) error {
	m.mu.Lock()
	sock, ok := m.sockets[id]
	if ok {
		sock.closed = true
		delete(m.sockets, id)
	}
	m.mu.Unlock()
	if ok && sock.conn != nil {
		return sock.conn.Close()
	}
	return nil
}

func (m *udpManager) readLoop(sock *udpSocket) {
	buf := make([]byte, 65536)
	for {
		n, addr, err := sock.conn.ReadFromUDP(buf)
		if err != nil {
			m.mu.Lock()
			if !sock.closed {
				m.events = append(m.events, udpEvent{Kind: "error", ID: sock.id, Data: err.Error()})
			}
			m.events = append(m.events, udpEvent{Kind: "close", ID: sock.id})
			m.mu.Unlock()
			if m.wakeFn != nil {
				m.wakeFn()
			}
			return
		}
		m.mu.Lock()
		m.events = append(m.events, udpEvent{
			Kind:    "message",
			ID:      sock.id,
			Data:    string(buf[:n]),
			Address: addr.IP.String(),
			Port:    addr.Port,
		})
		m.mu.Unlock()
		if m.wakeFn != nil {
			m.wakeFn()
		}
	}
}

func (m *udpManager) processEvents(r *Runtime) {
	m.mu.Lock()
	if len(m.events) == 0 {
		m.mu.Unlock()
		return
	}
	events := m.events
	m.events = nil
	m.mu.Unlock()

	data, _ := json.Marshal(events)
	r.execLocked("if(typeof __dgramDeliverEvents==='function')__dgramDeliverEvents(" + string(data) + ")")
}

func (m *udpManager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sockets) > 0
}

func (m *udpManager) closeAll() {
	m.mu.Lock()
	for id, sock := range m.sockets {
		sock.closed = true
		sock.conn.Close()
		delete(m.sockets, id)
	}
	m.mu.Unlock()
}

func (r *Runtime) installDgram() error {
	mgr := newUDPManager(r.Wake)
	r.udpMgr = mgr

	if err := r.registerFuncLocked("__go_dgram_create", func(args []any) (any, error) {
		stype := "udp4"
		if len(args) >= 1 {
			if s, ok := args[0].(string); ok {
				stype = s
			}
		}
		id, err := mgr.createSocket(stype)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_dgram_bind", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("dgram bind: id required")
		}
		id, _ := args[0].(float64)
		port := 0
		if len(args) >= 2 {
			port = int(args[1].(float64))
		}
		address := ""
		if len(args) >= 3 {
			address, _ = args[2].(string)
		}
		actualPort, err := mgr.bind(int(id), port, address)
		if err != nil {
			return nil, err
		}
		return float64(actualPort), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_dgram_send", func(args []any) (any, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("dgram send: id, data, port, address required")
		}
		id, _ := args[0].(float64)
		data, _ := args[1].(string)
		port, _ := args[2].(float64)
		address, _ := args[3].(string)
		return nil, mgr.send(int(id), data, int(port), address)
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_dgram_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("dgram close: id required")
		}
		id, _ := args[0].(float64)
		return nil, mgr.closeSocket(int(id))
	}); err != nil {
		return err
	}

	return r.execLocked(dgramJSSource())
}

func dgramJSSource() string {
	return strings.TrimSpace(`
(function() {
	var EventEmitter = globalThis.require('events').EventEmitter;
	var __activeDgramSockets = {};

	function Socket(type) {
		EventEmitter.call(this);
		this._type = type || 'udp4';
		this._id = __go_dgram_create(this._type);
		this._bound = false;
		__activeDgramSockets[String(this._id)] = this;
	}
	Socket.prototype = Object.create(EventEmitter.prototype);
	Socket.prototype.constructor = Socket;

	Socket.prototype.bind = function(port, address, cb) {
		if (typeof port === 'object') {
			var opts = port;
			cb = address;
			port = opts.port || 0;
			address = opts.address || '';
		}
		if (typeof address === 'function') { cb = address; address = ''; }
		if (typeof cb === 'function') this.once('listening', cb);
		var actualPort = __go_dgram_bind(this._id, port || 0, address || '');
		this._port = actualPort;
		this._bound = true;
		return this;
	};

	Socket.prototype.send = function(msg) {
		var args = Array.prototype.slice.call(arguments, 1);
		var cb, address, port;
		if (typeof args[args.length - 1] === 'function') cb = args.pop();
		if (args.length >= 4 && typeof args[0] === 'number' && typeof args[1] === 'number') {
			msg = msg.slice(args[0], args[0] + args[1]);
			port = args[2];
			address = args[3];
		} else {
			port = args[0];
			address = args[1];
		}
		if (typeof msg !== 'string') msg = String(msg);
		try {
			__go_dgram_send(this._id, msg, port, address || '127.0.0.1');
			if (cb) cb(null);
		} catch(e) {
			if (cb) cb(e);
			else this.emit('error', e);
		}
	};

	Socket.prototype.close = function(cb) {
		if (typeof cb === 'function') this.once('close', cb);
		try { __go_dgram_close(this._id); } catch(e) {}
		delete __activeDgramSockets[String(this._id)];
		return this;
	};

	Socket.prototype.address = function() {
		return { port: this._port || 0, family: this._type === 'udp6' ? 'IPv6' : 'IPv4', address: '0.0.0.0' };
	};

	Socket.prototype.ref = function() { return this; };
	Socket.prototype.unref = function() { return this; };
	Socket.prototype.setRecvBufferSize = function() {};
	Socket.prototype.setSendBufferSize = function() {};

	globalThis.__dgramDeliverEvents = function(events) {
		for (var i = 0; i < events.length; i++) {
			var ev = events[i];
			var sock = __activeDgramSockets[String(ev.ID)];
			if (!sock) continue;
			if (ev.Kind === 'message') {
				var rinfo = { address: ev.Address, port: ev.Port, family: 'IPv4', size: ev.Data.length };
				sock.emit('message', ev.Data, rinfo);
			} else if (ev.Kind === 'listening') {
				sock.emit('listening');
			} else if (ev.Kind === 'error') {
				sock.emit('error', new Error(ev.Data));
			} else if (ev.Kind === 'close') {
				sock.emit('close');
				delete __activeDgramSockets[String(ev.ID)];
			}
		}
	};

	var dgram = {
		createSocket: function(opts, cb) {
			var type = typeof opts === 'string' ? opts : (opts && opts.type || 'udp4');
			var sock = new Socket(type);
			if (typeof cb === 'function') sock.on('message', cb);
			return sock;
		},
		Socket: Socket
	};

	if (typeof globalThis.require !== 'undefined' && globalThis.require._modules) {
		globalThis.require._modules['dgram'] = dgram;
	}
})();
`)
}
