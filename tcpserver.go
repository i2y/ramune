package ramune

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
)

const (
	srvEventListening  = "listening"
	srvEventConnection = "connection"
	srvEventClose      = "close"
	srvEventError      = "error"
)

type tcpServerEvent struct {
	Kind   string `json:"Kind"`
	ConnID int    `json:"ConnID,omitempty"`
	Data   string `json:"Data,omitempty"`
}

type tcpServer struct {
	listener net.Listener
	mu       sync.Mutex
	events   []tcpServerEvent
	closed   bool
}

type tcpServerManager struct {
	mu      sync.Mutex
	servers map[int]*tcpServer
	nextID  int
	sockMgr *socketManager
	wakeFn  func()
}

func newTCPServerManager(sockMgr *socketManager, wakeFn func()) *tcpServerManager {
	return &tcpServerManager{
		servers: make(map[int]*tcpServer),
		sockMgr: sockMgr,
		wakeFn:  wakeFn,
	}
}

func (m *tcpServerManager) registerAndAccept(ln net.Listener) (int, int) {
	actualPort := ln.Addr().(*net.TCPAddr).Port
	srv := &tcpServer{listener: ln}

	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.servers[id] = srv
	m.mu.Unlock()

	go func() {
		srv.mu.Lock()
		srv.events = append(srv.events, tcpServerEvent{Kind: srvEventListening})
		srv.mu.Unlock()
		if m.wakeFn != nil {
			m.wakeFn()
		}

		for {
			conn, err := ln.Accept()
			if err != nil {
				srv.mu.Lock()
				if !srv.closed {
					srv.events = append(srv.events, tcpServerEvent{Kind: srvEventError, Data: err.Error()})
				}
				srv.events = append(srv.events, tcpServerEvent{Kind: srvEventClose})
				srv.closed = true
				srv.mu.Unlock()
				if m.wakeFn != nil {
					m.wakeFn()
				}
				return
			}
			connID := m.sockMgr.registerExisting(conn)
			srv.mu.Lock()
			srv.events = append(srv.events, tcpServerEvent{Kind: srvEventConnection, ConnID: connID})
			srv.mu.Unlock()
			if m.wakeFn != nil {
				m.wakeFn()
			}
		}
	}()

	return id, actualPort
}

func (m *tcpServerManager) listen(host string, port int) (int, int, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, 0, err
	}
	id, actualPort := m.registerAndAccept(ln)
	return id, actualPort, nil
}

func (m *tcpServerManager) listenTLS(host string, port int, certPEM, keyPEM string) (int, int, error) {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return 0, 0, fmt.Errorf("tls: %w", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	ln, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		return 0, 0, err
	}
	id, actualPort := m.registerAndAccept(ln)
	return id, actualPort, nil
}

func (m *tcpServerManager) close(id int) error {
	m.mu.Lock()
	srv, ok := m.servers[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	srv.mu.Lock()
	srv.closed = true
	srv.mu.Unlock()
	return srv.listener.Close()
}

func (m *tcpServerManager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers) > 0
}

func (m *tcpServerManager) processEvents(r *Runtime) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if len(m.servers) == 0 {
		m.mu.Unlock()
		return
	}
	type idEvents struct {
		id     int
		events []tcpServerEvent
		closed bool
	}
	var all []idEvents
	for id, srv := range m.servers {
		srv.mu.Lock()
		if len(srv.events) > 0 {
			all = append(all, idEvents{id, srv.events, srv.closed})
			srv.events = nil
		}
		srv.mu.Unlock()
	}
	for _, ie := range all {
		if ie.closed {
			delete(m.servers, ie.id)
		}
	}
	m.mu.Unlock()

	if len(all) == 0 {
		return
	}

	evMap := make(map[string][]tcpServerEvent, len(all))
	for _, ie := range all {
		evMap[itoa(ie.id)] = ie.events
	}
	data, _ := json.Marshal(evMap)
	r.execLocked("if(typeof __tcpServerDeliverEvents==='function')__tcpServerDeliverEvents(" + string(data) + ")")
}

func (m *tcpServerManager) closeAll() {
	m.mu.Lock()
	for id, srv := range m.servers {
		srv.mu.Lock()
		srv.closed = true
		srv.mu.Unlock()
		srv.listener.Close()
		delete(m.servers, id)
	}
	m.mu.Unlock()
}

// installTCPServer registers TCP server callbacks and JS module.
func (r *Runtime) installTCPServer() error {
	mgr := newTCPServerManager(r.sockMgr, r.Wake)
	r.tcpSrvMgr = mgr

	if err := r.registerFuncLocked("__go_tcp_listen", func(args []any) (any, error) {
		host := "0.0.0.0"
		if len(args) >= 1 {
			if s, ok := args[0].(string); ok && s != "" {
				host = s
			}
		}
		port := 0
		if len(args) >= 2 {
			if p, ok := args[1].(float64); ok {
				port = int(p)
			}
		}
		id, actualPort, err := mgr.listen(host, port)
		if err != nil {
			return nil, err
		}
		resp := map[string]any{"serverId": float64(id), "port": float64(actualPort)}
		b, _ := json.Marshal(resp)
		return string(b), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_tcp_server_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("tcp server close: id required")
		}
		id, _ := args[0].(float64)
		return nil, mgr.close(int(id))
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_tls_listen", func(args []any) (any, error) {
		host := "0.0.0.0"
		if len(args) >= 1 {
			if s, ok := args[0].(string); ok && s != "" {
				host = s
			}
		}
		port := 0
		if len(args) >= 2 {
			if p, ok := args[1].(float64); ok {
				port = int(p)
			}
		}
		certPEM := ""
		if len(args) >= 3 {
			certPEM, _ = args[2].(string)
		}
		keyPEM := ""
		if len(args) >= 4 {
			keyPEM, _ = args[3].(string)
		}
		id, actualPort, err := mgr.listenTLS(host, port, certPEM, keyPEM)
		if err != nil {
			return nil, err
		}
		resp := map[string]any{"serverId": float64(id), "port": float64(actualPort)}
		b, _ := json.Marshal(resp)
		return string(b), nil
	}); err != nil {
		return err
	}

	return r.execLocked(tcpServerJSSource())
}

func tcpServerJSSource() string {
	return strings.TrimSpace(`
(function() {
	var EventEmitter = globalThis.require('events').EventEmitter;
	var netModule = globalThis.require('net');

	var __activeServers = {};

	class Server extends EventEmitter {
		constructor(connectionListener) {
			super();
			this._id = null;
			this._port = 0;
			this._host = '0.0.0.0';
			this._listening = false;
			if (typeof connectionListener === 'function') {
				this.on('connection', connectionListener);
			}
		}
		listen(port, host, cb) {
			if (typeof port === 'object') {
				var opts = port;
				port = opts.port || 0;
				host = opts.host || '0.0.0.0';
				cb = host;
			}
			if (typeof host === 'function') { cb = host; host = '0.0.0.0'; }
			host = host || '0.0.0.0';
			if (typeof cb === 'function') this.once('listening', cb);

			var result = JSON.parse(__go_tcp_listen(host, port || 0));
			this._id = result.serverId;
			this._port = result.port;
			this._host = host;
			__activeServers[String(this._id)] = this;
			return this;
		}
		close(cb) {
			if (this._id != null) {
				__go_tcp_server_close(this._id);
				this._listening = false;
				delete __activeServers[String(this._id)];
				this._id = null;
			}
			if (typeof cb === 'function') this.once('close', cb);
			var self = this;
			setImmediate(function() { self.emit('close'); });
			return this;
		}
		address() {
			return { port: this._port, family: 'IPv4', address: this._host };
		}
		ref() { return this; }
		unref() { return this; }
	}

	globalThis.__tcpServerDeliverEvents = function(eventsMap) {
		var ids = Object.keys(eventsMap);
		for (var s = 0; s < ids.length; s++) {
			var id = ids[s];
			var srv = __activeServers[id];
			if (!srv) continue;
			var events = eventsMap[id];
			for (var i = 0; i < events.length; i++) {
				var ev = events[i];
				if (ev.Kind === 'listening') {
					srv._listening = true;
					srv.emit('listening');
				} else if (ev.Kind === 'connection') {
					var sock = new netModule.Socket();
					sock._id = ev.ConnID;
					sock._connected = true;
					if (typeof globalThis.__activeSockets !== 'undefined') {
						globalThis.__activeSockets[String(ev.ConnID)] = sock;
					}
					srv.emit('connection', sock);
				} else if (ev.Kind === 'error') {
					srv.emit('error', new Error(ev.Data));
				} else if (ev.Kind === 'close') {
					srv._listening = false;
					srv.emit('close');
					delete __activeServers[id];
				}
			}
		}
	};

	netModule.createServer = function(opts, connectionListener) {
		if (typeof opts === 'function') {
			connectionListener = opts;
			opts = {};
		}
		return new Server(connectionListener);
	};
	netModule.Server = Server;

	// --- tls.createServer ---
	var tlsModule = globalThis.require('tls');

	class TLSServer extends Server {
		constructor(opts, connectionListener) {
			super(connectionListener);
			this._cert = opts.cert || '';
			this._key = opts.key || '';
		}
		listen(port, host, cb) {
			if (typeof host === 'function') { cb = host; host = '0.0.0.0'; }
			host = host || '0.0.0.0';
			if (typeof cb === 'function') this.once('listening', cb);

			var result = JSON.parse(__go_tls_listen(host, port || 0, this._cert, this._key));
			this._id = result.serverId;
			this._port = result.port;
			this._host = host;
			__activeServers[String(this._id)] = this;
			return this;
		}
	}

	tlsModule.createServer = function(opts, connectionListener) {
		if (typeof opts === 'function') {
			connectionListener = opts;
			opts = {};
		}
		return new TLSServer(opts || {}, connectionListener);
	};
	tlsModule.Server = TLSServer;
})();
`)
}
