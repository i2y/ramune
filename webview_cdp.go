package ramune

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// cdpManager manages headless Chrome instances via Chrome DevTools Protocol.
type cdpManager struct {
	mu        sync.Mutex
	instances map[int]*cdpInstance
	events    []cdpEvent
	nextID    int
	wakeFn    func()
}

type cdpInstance struct {
	id   int
	cmd  *exec.Cmd
	conn net.Conn
	bufw *bufio.Writer
	bufr *bufio.Reader

	mu        sync.Mutex
	closed    bool
	nextCmdID int
}

type cdpEvent struct {
	InstanceID int    `json:"instanceId"`
	Kind       string `json:"kind"` // "response", "event", "close", "error"
	CmdID      int    `json:"cmdId,omitempty"`
	Data       string `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
	Method     string `json:"method,omitempty"`
	Params     string `json:"params,omitempty"`
}

type cdpMessage struct {
	ID     int              `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params *json.RawMessage `json:"params,omitempty"`
	Result *json.RawMessage `json:"result,omitempty"`
	Error  *cdpMsgError     `json:"error,omitempty"`
}

type cdpMsgError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newCDPManager(wakeFn func()) *cdpManager {
	return &cdpManager{
		instances: make(map[int]*cdpInstance),
		nextID:    1,
		wakeFn:    wakeFn,
	}
}

// --- Chrome process lifecycle ---

func findChrome() (string, error) {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p, nil
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"google-chrome",
		"google-chrome-stable",
		"chromium-browser",
		"chromium",
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Chrome/Chromium not found. Set CHROME_PATH or install Chrome")
}

type cdpCreateOpts struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (m *cdpManager) create(opts cdpCreateOpts) (int, error) {
	chromePath, err := findChrome()
	if err != nil {
		return 0, err
	}

	args := []string{
		"--headless=new",
		"--remote-debugging-port=0",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-extensions",
		"--disable-background-networking",
	}
	w, h := opts.Width, opts.Height
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 720
	}
	args = append(args, fmt.Sprintf("--window-size=%d,%d", w, h))

	cmd := exec.Command(chromePath, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("cdp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("cdp: start chrome: %w", err)
	}

	// Parse WebSocket URL from stderr.
	wsURL := ""
	scanner := bufio.NewScanner(stderr)
	deadline := time.After(10 * time.Second)
	done := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "DevTools listening on ") {
				done <- strings.TrimPrefix(line, "DevTools listening on ")
				return
			}
		}
		done <- ""
	}()
	select {
	case url := <-done:
		wsURL = url
	case <-deadline:
		cmd.Process.Kill()
		return 0, fmt.Errorf("cdp: timeout waiting for Chrome DevTools URL")
	}
	if wsURL == "" {
		cmd.Process.Kill()
		return 0, fmt.Errorf("cdp: Chrome exited without providing DevTools URL")
	}

	// Get page target WebSocket URL via HTTP.
	host := wsURL
	if idx := strings.Index(host, "ws://"); idx >= 0 {
		host = host[5:]
	}
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	pageWSURL, err := getPageTarget("http://" + host)
	if err != nil {
		cmd.Process.Kill()
		return 0, fmt.Errorf("cdp: get page target: %w", err)
	}

	// Connect WebSocket to page target.
	conn, err := wsClientConnect(pageWSURL)
	if err != nil {
		cmd.Process.Kill()
		return 0, fmt.Errorf("cdp: ws connect: %w", err)
	}

	inst := &cdpInstance{
		cmd:       cmd,
		conn:      conn,
		bufw:      bufio.NewWriter(conn),
		bufr:      bufio.NewReader(conn),
		nextCmdID: 1,
	}

	m.mu.Lock()
	inst.id = m.nextID
	m.nextID++
	m.instances[inst.id] = inst
	m.mu.Unlock()

	// Enable CDP domains.
	inst.sendCDP("Page.enable", nil)
	inst.sendCDP("Runtime.enable", nil)

	// Start background reader.
	go m.readLoop(inst)

	return inst.id, nil
}

func getPageTarget(baseURL string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL + "/json/list")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("no page target found")
}

// --- WebSocket client ---

func wsClientConnect(wsURL string) (net.Conn, error) {
	// Parse ws://host:port/path
	url := wsURL
	if strings.HasPrefix(url, "ws://") {
		url = url[5:]
	}
	host := url
	path := "/"
	if idx := strings.Index(url, "/"); idx >= 0 {
		host = url[:idx]
		path = url[idx:]
	}

	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return nil, err
	}

	// WebSocket upgrade handshake.
	keyBytes := make([]byte, 16)
	rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, host, key)
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}

	// Read response.
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(statusLine, "101") {
		conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", strings.TrimSpace(statusLine))
	}
	// Drain headers.
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	// Validate Sec-WebSocket-Accept.
	magic := "258EAFA5-E914-47DA-95CA-5AB4085B9188"
	h := sha1.New()
	h.Write([]byte(key + magic))
	// We skip strict validation — Chrome always sends the correct accept header.
	_ = h

	return conn, nil
}

// writeWSFrameClient writes a WebSocket frame with client-side masking.
func writeWSFrameClient(w *bufio.Writer, opcode byte, payload []byte) error {
	w.WriteByte(0x80 | opcode)

	payloadLen := len(payload)
	switch {
	case payloadLen <= 125:
		w.WriteByte(0x80 | byte(payloadLen))
	case payloadLen <= 65535:
		w.WriteByte(0x80 | 126)
		lenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBytes, uint16(payloadLen))
		w.Write(lenBytes)
	default:
		w.WriteByte(0x80 | 127)
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(payloadLen))
		w.Write(lenBytes)
	}

	mask := make([]byte, 4)
	rand.Read(mask)
	w.Write(mask)

	masked := make([]byte, payloadLen)
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	w.Write(masked)
	return w.Flush()
}

// readWSFrameClient reads a WebSocket frame (server-to-client, unmasked).
// Handles continuation frames for large payloads.
func readWSFrameClient(r *bufio.Reader) (opcode byte, payload []byte, err error) {
	var fullPayload []byte
	var firstOpcode byte

	for {
		header := make([]byte, 2)
		if _, err = io.ReadFull(r, header); err != nil {
			return 0, nil, err
		}

		fin := (header[0] & 0x80) != 0
		op := header[0] & 0x0F
		pLen := uint64(header[1] & 0x7F)

		switch pLen {
		case 126:
			ext := make([]byte, 2)
			if _, err = io.ReadFull(r, ext); err != nil {
				return 0, nil, err
			}
			pLen = uint64(binary.BigEndian.Uint16(ext))
		case 127:
			ext := make([]byte, 8)
			if _, err = io.ReadFull(r, ext); err != nil {
				return 0, nil, err
			}
			pLen = binary.BigEndian.Uint64(ext)
		}

		chunk := make([]byte, pLen)
		if pLen > 0 {
			if _, err = io.ReadFull(r, chunk); err != nil {
				return 0, nil, err
			}
		}

		if op == 8 { // Close
			return 8, nil, nil
		}
		if op == 9 { // Ping — ignore for client
			continue
		}
		if op == 10 { // Pong — ignore
			continue
		}

		if op != 0 {
			firstOpcode = op
		}
		fullPayload = append(fullPayload, chunk...)

		if fin {
			return firstOpcode, fullPayload, nil
		}
	}
}

// --- CDP command/response ---

func (inst *cdpInstance) sendCDP(method string, params any) int {
	inst.mu.Lock()
	id := inst.nextCmdID
	inst.nextCmdID++
	inst.mu.Unlock()

	msg := struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{ID: id, Method: method, Params: params}

	data, _ := json.Marshal(msg)

	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.closed {
		return id
	}
	writeWSFrameClient(inst.bufw, 1, data) // opcode 1 = text
	return id
}

func (m *cdpManager) readLoop(inst *cdpInstance) {
	defer func() {
		m.mu.Lock()
		m.events = append(m.events, cdpEvent{InstanceID: inst.id, Kind: "close"})
		m.mu.Unlock()
		if m.wakeFn != nil {
			m.wakeFn()
		}
	}()

	for {
		opcode, payload, err := readWSFrameClient(inst.bufr)
		if err != nil {
			inst.mu.Lock()
			closed := inst.closed
			inst.mu.Unlock()
			if closed {
				return
			}
			m.mu.Lock()
			m.events = append(m.events, cdpEvent{InstanceID: inst.id, Kind: "error", Error: err.Error()})
			m.mu.Unlock()
			if m.wakeFn != nil {
				m.wakeFn()
			}
			return
		}
		if opcode == 8 {
			return
		}

		var msg cdpMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}

		if msg.ID > 0 {
			// Response to a command.
			ev := cdpEvent{InstanceID: inst.id, Kind: "response", CmdID: msg.ID}
			if msg.Error != nil {
				ev.Error = msg.Error.Message
			} else if msg.Result != nil {
				ev.Data = string(*msg.Result)
			} else {
				ev.Data = "{}"
			}
			m.mu.Lock()
			m.events = append(m.events, ev)
			m.mu.Unlock()
		} else if msg.Method != "" {
			// CDP event.
			params := "{}"
			if msg.Params != nil {
				params = string(*msg.Params)
			}
			m.mu.Lock()
			m.events = append(m.events, cdpEvent{
				InstanceID: inst.id,
				Kind:       "event",
				Method:     msg.Method,
				Params:     params,
			})
			m.mu.Unlock()
		}

		if m.wakeFn != nil {
			m.wakeFn()
		}
	}
}

// --- TickManager interface ---

func (m *cdpManager) ProcessEvents(r *Runtime) {
	m.mu.Lock()
	events := m.events
	m.events = nil
	m.mu.Unlock()

	if len(events) == 0 {
		return
	}

	data, _ := json.Marshal(events)
	r.execLocked("if(typeof __cdpDeliverEvents==='function')__cdpDeliverEvents(" + string(data) + ")")
}

func (m *cdpManager) HasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.instances) > 0
}

func (m *cdpManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		inst.close()
	}
	m.instances = make(map[int]*cdpInstance)
}

func (inst *cdpInstance) close() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.closed {
		return
	}
	inst.closed = true
	if inst.conn != nil {
		inst.conn.Close()
	}
	if inst.cmd != nil && inst.cmd.Process != nil {
		inst.cmd.Process.Kill()
		inst.cmd.Wait()
	}
}

// --- Runtime integration ---

func (r *Runtime) installCDP() error {
	mgr := newCDPManager(r.Wake)
	r.customTickMgrs = append(r.customTickMgrs, mgr)

	if err := r.registerFuncLocked("__go_cdp_create", func(args []any) (any, error) {
		optsJSON := "{}"
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				optsJSON = s
			}
		}
		var opts cdpCreateOpts
		json.Unmarshal([]byte(optsJSON), &opts)
		id, err := mgr.create(opts)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_cdp_send", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("cdp_send requires (id, method, paramsJSON)")
		}
		id := int(args[0].(float64))
		method := args[1].(string)
		paramsJSON := args[2].(string)

		mgr.mu.Lock()
		inst, ok := mgr.instances[id]
		mgr.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("cdp instance %d not found", id)
		}

		var params any
		if paramsJSON != "" && paramsJSON != "{}" {
			json.Unmarshal([]byte(paramsJSON), &params)
		} else {
			params = map[string]any{}
		}
		cmdID := inst.sendCDP(method, params)
		return float64(cmdID), nil
	}); err != nil {
		return err
	}

	if err := r.registerFuncLocked("__go_cdp_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, nil
		}
		id := int(args[0].(float64))
		mgr.mu.Lock()
		inst, ok := mgr.instances[id]
		if ok {
			delete(mgr.instances, id)
		}
		mgr.mu.Unlock()
		if ok {
			inst.close()
		}
		return nil, nil
	}); err != nil {
		return err
	}

	return r.execLocked(cdpJSSource())
}

func cdpJSSource() string {
	return `(function() {
	var __cdpPending = {};
	var __cdpInstances = {};

	function WebViewCDP(opts) {
		this._id = __go_cdp_create(JSON.stringify(opts || {}));
		this._closed = false;
		this._url = '';
		this._title = '';
		this._loading = false;
		this.onNavigated = null;
		this.onNavigationFailed = null;
		__cdpInstances[String(this._id)] = this;
	}

	WebViewCDP.prototype._send = function(method, params) {
		if (this._closed) return Promise.reject(new Error('WebView is closed'));
		var cmdId = __go_cdp_send(this._id, method, params ? JSON.stringify(params) : '{}');
		return new Promise(function(resolve, reject) {
			__cdpPending[String(cmdId)] = {resolve: resolve, reject: reject};
		});
	};

	WebViewCDP.prototype.navigate = function(url) {
		var self = this;
		self._loading = true;
		return self._send('Page.navigate', {url: url}).then(function(result) {
			if (result && result.errorText) {
				self._loading = false;
				var err = new Error(result.errorText);
				if (self.onNavigationFailed) self.onNavigationFailed(err);
				throw err;
			}
			self._url = url;
			// Wait for load event
			return new Promise(function(resolve) {
				self._onLoadResolve = resolve;
				// Timeout fallback
				setTimeout(function() {
					if (self._onLoadResolve) {
						self._onLoadResolve();
						self._onLoadResolve = null;
					}
				}, 30000);
			});
		});
	};

	WebViewCDP.prototype.evaluate = function(expression) {
		return this._send('Runtime.evaluate', {
			expression: String(expression),
			returnByValue: true,
			awaitPromise: true
		}).then(function(result) {
			if (result && result.exceptionDetails) {
				throw new Error(result.exceptionDetails.text || 'Evaluation failed');
			}
			return result && result.result ? result.result.value : undefined;
		});
	};

	WebViewCDP.prototype.screenshot = function(opts) {
		opts = opts || {};
		var params = {format: opts.format || 'png'};
		if (opts.quality) params.quality = opts.quality;
		if (opts.fullPage) params.captureBeyondViewport = true;
		return this._send('Page.captureScreenshot', params).then(function(result) {
			if (result && result.data) {
				return globalThis.Buffer ? globalThis.Buffer.from(result.data, 'base64')
					: Uint8Array.from(atob(result.data), function(c) { return c.charCodeAt(0); });
			}
			return null;
		});
	};

	WebViewCDP.prototype.click = function(x, y, opts) {
		var self = this;
		opts = opts || {};
		var button = opts.button || 'left';
		return self._send('Input.dispatchMouseEvent', {
			type: 'mousePressed', x: x, y: y, button: button, clickCount: 1
		}).then(function() {
			return self._send('Input.dispatchMouseEvent', {
				type: 'mouseReleased', x: x, y: y, button: button, clickCount: 1
			});
		});
	};

	WebViewCDP.prototype.type = function(text) {
		return this._send('Input.insertText', {text: String(text)});
	};

	WebViewCDP.prototype.press = function(key, opts) {
		var self = this;
		opts = opts || {};
		var params = {type: 'keyDown', key: key};
		if (opts.modifiers) params.modifiers = opts.modifiers;
		return self._send('Input.dispatchKeyEvent', params).then(function() {
			return self._send('Input.dispatchKeyEvent', {type: 'keyUp', key: key});
		});
	};

	WebViewCDP.prototype.scroll = function(dx, dy) {
		return this._send('Input.dispatchMouseEvent', {
			type: 'mouseWheel', x: 0, y: 0, deltaX: dx, deltaY: dy
		});
	};

	WebViewCDP.prototype.resize = function(w, h) {
		return this._send('Emulation.setDeviceMetricsOverride', {
			width: w, height: h, deviceScaleFactor: 1, mobile: false
		});
	};

	WebViewCDP.prototype.back = function() { return this._send('Page.navigateToHistoryEntry', {entryId: -1}).catch(function() { return this.evaluate('history.back()'); }.bind(this)); };
	WebViewCDP.prototype.forward = function() { return this._send('Page.navigateToHistoryEntry', {entryId: 1}).catch(function() { return this.evaluate('history.forward()'); }.bind(this)); };
	WebViewCDP.prototype.reload = function() { return this._send('Page.reload', {}); };

	WebViewCDP.prototype.cdp = function(method, params) {
		return this._send(method, params || {});
	};

	Object.defineProperty(WebViewCDP.prototype, 'url', { get: function() { return this._url; } });
	Object.defineProperty(WebViewCDP.prototype, 'title', { get: function() { return this._title; } });
	Object.defineProperty(WebViewCDP.prototype, 'loading', { get: function() { return this._loading; } });

	WebViewCDP.prototype.close = function() {
		if (!this._closed) {
			this._closed = true;
			try { __go_cdp_close(this._id); } catch(e) {}
			delete __cdpInstances[String(this._id)];
		}
	};
	WebViewCDP.prototype[Symbol.dispose] = function() { this.close(); };

	WebViewCDP.closeAll = function() {
		var keys = Object.keys(__cdpInstances);
		for (var i = 0; i < keys.length; i++) {
			__cdpInstances[keys[i]].close();
		}
	};

	globalThis.__cdpDeliverEvents = function(events) {
		for (var i = 0; i < events.length; i++) {
			var ev = events[i];
			if (ev.kind === 'response') {
				var pending = __cdpPending[String(ev.cmdId)];
				if (pending) {
					delete __cdpPending[String(ev.cmdId)];
					if (ev.error) pending.reject(new Error(ev.error));
					else pending.resolve(ev.data ? JSON.parse(ev.data) : null);
				}
			} else if (ev.kind === 'event') {
				var inst = __cdpInstances[String(ev.instanceId)];
				if (!inst) continue;
				var params = ev.params ? JSON.parse(ev.params) : {};
				if (ev.method === 'Page.loadEventFired') {
					inst._loading = false;
					if (inst._onLoadResolve) {
						inst._onLoadResolve();
						inst._onLoadResolve = null;
					}
				} else if (ev.method === 'Page.frameNavigated' && params.frame && !params.frame.parentId) {
					inst._url = params.frame.url || inst._url;
					inst._title = params.frame.name || inst._title;
					if (inst.onNavigated) inst.onNavigated(inst._url, inst._title);
				}
			} else if (ev.kind === 'close' || ev.kind === 'error') {
				var inst = __cdpInstances[String(ev.instanceId)];
				if (inst) {
					inst._closed = true;
					delete __cdpInstances[String(ev.instanceId)];
				}
				// Reject all pending commands
				var keys = Object.keys(__cdpPending);
				for (var j = 0; j < keys.length; j++) {
					var p = __cdpPending[keys[j]];
					delete __cdpPending[keys[j]];
					p.reject(new Error(ev.error || 'WebView closed'));
				}
			}
		}
	};

	// Register as Bun.WebView
	if (typeof globalThis.Bun === 'undefined') globalThis.Bun = {};
	globalThis.Bun.WebView = WebViewCDP;
})();`
}
