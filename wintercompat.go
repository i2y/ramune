package ramune

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

// WithWinterTC installs the WinterTC (ECMA-429) Minimum Common Web API
// surface. This includes CompressionStream, DecompressionStream,
// MessageChannel, MessagePort, MessageEvent, ErrorEvent,
// PromiseRejectionEvent, and URLPattern.
//
// When used with NodeCompat(), these APIs are installed automatically.
// Use WithWinterTC() for standalone WinterTC compliance without the
// full Node.js compatibility layer.
func WithWinterTC() Option {
	return func(c *config) { c.winterTC = true }
}

// installWinterTC registers the WinterTC gap APIs.
// Must be called with rt.mu held, after installWebStreams and installNodeCompat
// (requires TransformStream and Event).
func (r *Runtime) installWinterTC() error {
	// Streaming compression session management.
	if err := r.registerFuncLocked("__go_wtc_cs_create", goWTCCSCreate); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_wtc_cs_write", goWTCCSWrite); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_wtc_cs_close", goWTCCSClose); err != nil {
		return err
	}
	// Streaming decompression session management.
	if err := r.registerFuncLocked("__go_wtc_ds_create", goWTCDSCreate); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_wtc_ds_write", goWTCDSWrite); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_wtc_ds_close", goWTCDSClose); err != nil {
		return err
	}
	return r.execLocked(winterTCJSSource())
}

// --- Streaming compression session management ---

// compSession holds state for a streaming compression session.
type compSession struct {
	buf    bytes.Buffer
	writer io.WriteCloser
}

var compSessions = struct {
	mu       sync.Mutex
	sessions map[int]*compSession
	nextID   int
}{sessions: make(map[int]*compSession)}

// goWTCCSCreate: args [format] -> sessionID (float64)
func goWTCCSCreate(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("cs_create: format required")
	}
	format, _ := args[0].(string)

	s := &compSession{}
	switch format {
	case "gzip":
		s.writer = gzip.NewWriter(&s.buf)
	case "deflate":
		s.writer = zlib.NewWriter(&s.buf)
	case "deflate-raw":
		w, err := flate.NewWriter(&s.buf, flate.DefaultCompression)
		if err != nil {
			return nil, err
		}
		s.writer = w
	default:
		return nil, fmt.Errorf("unsupported compression format: %s", format)
	}

	compSessions.mu.Lock()
	id := compSessions.nextID
	compSessions.nextID++
	compSessions.sessions[id] = s
	compSessions.mu.Unlock()

	return float64(id), nil
}

// goWTCCSWrite: args [id, dataHex] -> hexCompressed (output available so far)
func goWTCCSWrite(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("cs_write: id and data required")
	}
	id := int(args[0].(float64))
	dataHex, _ := args[1].(string)
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, err
	}

	compSessions.mu.Lock()
	s, ok := compSessions.sessions[id]
	compSessions.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("cs_write: invalid session %d", id)
	}

	if _, err := s.writer.Write(data); err != nil {
		return nil, err
	}
	// Flush to produce per-chunk output (like Z_SYNC_FLUSH).
	if f, ok := s.writer.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			return nil, err
		}
	}

	out := make([]byte, s.buf.Len())
	copy(out, s.buf.Bytes())
	s.buf.Reset()
	return hex.EncodeToString(out), nil
}

// goWTCCSClose: args [id] -> hexRemaining (final flush + cleanup)
func goWTCCSClose(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("cs_close: id required")
	}
	id := int(args[0].(float64))

	compSessions.mu.Lock()
	s, ok := compSessions.sessions[id]
	if ok {
		delete(compSessions.sessions, id)
	}
	compSessions.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("cs_close: invalid session %d", id)
	}

	if err := s.writer.Close(); err != nil {
		return nil, err
	}
	return hex.EncodeToString(s.buf.Bytes()), nil
}

// --- Streaming decompression session management ---

// decompSession accumulates compressed data and decompresses on close.
type decompSession struct {
	format string
	buf    bytes.Buffer
}

var decompSessions = struct {
	mu       sync.Mutex
	sessions map[int]*decompSession
	nextID   int
}{sessions: make(map[int]*decompSession)}

// goWTCDSCreate: args [format] -> sessionID
func goWTCDSCreate(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("ds_create: format required")
	}
	format, _ := args[0].(string)

	s := &decompSession{format: format}

	decompSessions.mu.Lock()
	id := decompSessions.nextID
	decompSessions.nextID++
	decompSessions.sessions[id] = s
	decompSessions.mu.Unlock()

	return float64(id), nil
}

// goWTCDSWrite: args [id, dataHex] -> "" (accumulate compressed data)
func goWTCDSWrite(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("ds_write: id and data required")
	}
	id := int(args[0].(float64))
	dataHex, _ := args[1].(string)
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, err
	}

	decompSessions.mu.Lock()
	s, ok := decompSessions.sessions[id]
	decompSessions.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("ds_write: invalid session %d", id)
	}

	s.buf.Write(data)
	return "", nil
}

// goWTCDSClose: args [id] -> hexDecompressed (decompress all accumulated data)
func goWTCDSClose(args []any) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("ds_close: id required")
	}
	id := int(args[0].(float64))

	decompSessions.mu.Lock()
	s, ok := decompSessions.sessions[id]
	if ok {
		delete(decompSessions.sessions, id)
	}
	decompSessions.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("ds_close: invalid session %d", id)
	}

	var reader io.ReadCloser
	switch s.format {
	case "gzip":
		r, err := gzip.NewReader(&s.buf)
		if err != nil {
			return nil, err
		}
		reader = r
	case "deflate":
		r, err := zlib.NewReader(&s.buf)
		if err != nil {
			return nil, err
		}
		reader = r
	case "deflate-raw":
		reader = flate.NewReader(&s.buf)
	default:
		return nil, fmt.Errorf("unsupported format: %s", s.format)
	}
	defer reader.Close()
	out, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return hex.EncodeToString(out), nil
}

func winterTCJSSource() string {
	return `
(function() {
	// --- Hex encoding helpers for binary data exchange with Go ---
	function __wtcU8ToHex(u8) {
		var h = '', b;
		for (var i = 0; i < u8.length; i++) {
			b = u8[i].toString(16);
			if (b.length < 2) h += '0';
			h += b;
		}
		return h;
	}
	function __wtcHexToU8(hex) {
		var u8 = new Uint8Array(hex.length / 2);
		for (var i = 0; i < u8.length; i++) {
			u8[i] = parseInt(hex.substr(i * 2, 2), 16);
		}
		return u8;
	}
	function __wtcMergeChunks(chunks) {
		var total = 0;
		for (var i = 0; i < chunks.length; i++) total += chunks[i].length;
		var merged = new Uint8Array(total);
		var offset = 0;
		for (var i = 0; i < chunks.length; i++) {
			merged.set(chunks[i], offset);
			offset += chunks[i].length;
		}
		return merged;
	}

	// --- DOMException (full polyfill with legacy error codes) ---
	if (typeof globalThis.DOMException === 'undefined' || !globalThis.DOMException.ABORT_ERR) {
		var _domExCodes = {
			IndexSizeError: 1, HierarchyRequestError: 3, WrongDocumentError: 4,
			InvalidCharacterError: 5, NoModificationAllowedError: 7, NotFoundError: 8,
			NotSupportedError: 9, InUseAttributeError: 10, InvalidStateError: 11,
			SyntaxError: 12, InvalidModificationError: 13, NamespaceError: 14,
			InvalidAccessError: 15, SecurityError: 18, NetworkError: 19,
			AbortError: 20, URLMismatchError: 21, QuotaExceededError: 22,
			TimeoutError: 23, InvalidNodeTypeError: 24, DataCloneError: 25
		};
		var _domExConsts = {
			INDEX_SIZE_ERR: 1, HIERARCHY_REQUEST_ERR: 3, WRONG_DOCUMENT_ERR: 4,
			INVALID_CHARACTER_ERR: 5, NO_MODIFICATION_ALLOWED_ERR: 7, NOT_FOUND_ERR: 8,
			NOT_SUPPORTED_ERR: 9, INUSE_ATTRIBUTE_ERR: 10, INVALID_STATE_ERR: 11,
			SYNTAX_ERR: 12, INVALID_MODIFICATION_ERR: 13, NAMESPACE_ERR: 14,
			INVALID_ACCESS_ERR: 15, SECURITY_ERR: 18, NETWORK_ERR: 19,
			ABORT_ERR: 20, URL_MISMATCH_ERR: 21, QUOTA_EXCEEDED_ERR: 22,
			TIMEOUT_ERR: 23, INVALID_NODE_TYPE_ERR: 24, DATA_CLONE_ERR: 25
		};
		globalThis.DOMException = function DOMException(message, name) {
			var err = new Error(message || '');
			Object.setPrototypeOf(err, DOMException.prototype);
			Object.defineProperty(err, 'name', { value: name || 'Error', enumerable: false, configurable: true });
			Object.defineProperty(err, 'code', { value: _domExCodes[name] || 0, enumerable: false, configurable: true });
			return err;
		};
		globalThis.DOMException.prototype = Object.create(Error.prototype);
		globalThis.DOMException.prototype.constructor = globalThis.DOMException;
		var _ck = Object.keys(_domExConsts);
		for (var _i = 0; _i < _ck.length; _i++) {
			globalThis.DOMException[_ck[_i]] = _domExConsts[_ck[_i]];
			globalThis.DOMException.prototype[_ck[_i]] = _domExConsts[_ck[_i]];
		}
	}

	// --- atob / btoa (WPT spec-compliant) ---
	if (typeof globalThis.btoa === 'undefined') {
		var _b64chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
		globalThis.btoa = function btoa(str) {
			if (str === undefined) throw new DOMException('The string to be encoded contains characters outside of the Latin1 range.', 'InvalidCharacterError');
			str = String(str);
			for (var i = 0; i < str.length; i++) {
				if (str.charCodeAt(i) > 255) throw new DOMException('The string to be encoded contains characters outside of the Latin1 range.', 'InvalidCharacterError');
			}
			var out = '', i = 0;
			while (i < str.length) {
				var a = str.charCodeAt(i++), b = i < str.length ? str.charCodeAt(i++) : NaN, c = i < str.length ? str.charCodeAt(i++) : NaN;
				var n = (a << 16) | ((isNaN(b) ? 0 : b) << 8) | (isNaN(c) ? 0 : c);
				out += _b64chars[(n >> 18) & 63] + _b64chars[(n >> 12) & 63];
				out += isNaN(b) ? '=' : _b64chars[(n >> 6) & 63];
				out += isNaN(c) ? '=' : _b64chars[n & 63];
			}
			return out;
		};
	}
	if (typeof globalThis.atob === 'undefined') {
		globalThis.atob = function atob(str) {
			str = String(str).replace(/[\t\n\f\r ]/g, '');
			if (str.length % 4 === 1) throw new DOMException("The string to be decoded is not correctly encoded.", 'InvalidCharacterError');
			var out = '', i = 0, lookup = {};
			var chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=';
			for (var j = 0; j < chars.length; j++) lookup[chars[j]] = j;
			while (i < str.length) {
				var a = lookup[str[i++]], b = lookup[str[i++]], c = lookup[str[i++]], d = lookup[str[i++]];
				if (a === undefined || b === undefined) throw new DOMException("The string to be decoded is not correctly encoded.", 'InvalidCharacterError');
				out += String.fromCharCode(((a & 63) << 2) | (b >> 4));
				if (c !== 64 && c !== undefined) out += String.fromCharCode(((b & 15) << 4) | (c >> 2));
				if (d !== 64 && d !== undefined) out += String.fromCharCode(((c & 3) << 6) | d);
			}
			return out;
		};
	}

	// --- Minimal Event/EventTarget (if not already provided by NodeCompat) ---
	if (typeof globalThis.Event === 'undefined') {
		globalThis.Event = function Event(type, opts) {
			this.type = type;
			this.bubbles = !!(opts && opts.bubbles);
			this.cancelable = !!(opts && opts.cancelable);
			this.composed = !!(opts && opts.composed);
			this.defaultPrevented = false;
			this._stopImmediate = false;
			this.target = null;
			this.currentTarget = null;
		};
		globalThis.Event.prototype.preventDefault = function() {
			if (this.cancelable) this.defaultPrevented = true;
		};
		globalThis.Event.prototype.stopPropagation = function() {};
		globalThis.Event.prototype.stopImmediatePropagation = function() { this._stopImmediate = true; };
	}
	if (typeof globalThis.EventTarget === 'undefined') {
		globalThis.EventTarget = function EventTarget() {
			this._listeners = {};
		};
		globalThis.EventTarget.prototype.addEventListener = function(type, fn, opts) {
			if (!this._listeners[type]) this._listeners[type] = [];
			if (this._listeners[type].indexOf(fn) >= 0) return;
			if (opts && opts.once) {
				var self = this;
				var wrapped = function(e) { self.removeEventListener(type, wrapped); fn.call(self, e); };
				wrapped._orig = fn;
				this._listeners[type].push(wrapped);
			} else {
				this._listeners[type].push(fn);
			}
		};
		globalThis.EventTarget.prototype.removeEventListener = function(type, fn) {
			if (!this._listeners[type]) return;
			this._listeners[type] = this._listeners[type].filter(function(f) { return f !== fn && f._orig !== fn; });
		};
		globalThis.EventTarget.prototype.dispatchEvent = function(event) {
			event.target = this;
			event.currentTarget = this;
			var fns = (this._listeners[event.type] || []).slice();
			for (var i = 0; i < fns.length; i++) {
				if (event._stopImmediate) break;
				var f = fns[i];
				if (typeof f === 'object' && f.handleEvent) f.handleEvent(event);
				else f.call(this, event);
			}
			return !event.defaultPrevented;
		};
	}
	if (typeof globalThis.CustomEvent === 'undefined') {
		globalThis.CustomEvent = function CustomEvent(type, opts) {
			globalThis.Event.call(this, type, opts);
			this.detail = (opts && opts.detail !== undefined) ? opts.detail : null;
		};
		globalThis.CustomEvent.prototype = Object.create(globalThis.Event.prototype);
		globalThis.CustomEvent.prototype.constructor = globalThis.CustomEvent;
	}

	// --- CompressionStream (streaming, per-chunk output via Go sessions) ---
	if (typeof globalThis.CompressionStream === 'undefined') {
		globalThis.CompressionStream = function CompressionStream(format) {
			if (['gzip', 'deflate', 'deflate-raw'].indexOf(format) < 0) {
				throw new TypeError("Failed to construct 'CompressionStream': Unsupported compression format: '" + format + "'");
			}
			var id = __go_wtc_cs_create(format);
			var ts = new TransformStream({
				transform: function(chunk, controller) {
					var u8 = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk.buffer ? chunk.buffer : chunk);
					var hexResult = __go_wtc_cs_write(id, __wtcU8ToHex(u8));
					if (hexResult.length > 0) controller.enqueue(__wtcHexToU8(hexResult));
				},
				flush: function(controller) {
					var hexResult = __go_wtc_cs_close(id);
					if (hexResult.length > 0) controller.enqueue(__wtcHexToU8(hexResult));
				}
			});
			this.readable = ts.readable;
			this.writable = ts.writable;
		};
	}

	// --- DecompressionStream (streaming via Go sessions) ---
	if (typeof globalThis.DecompressionStream === 'undefined') {
		globalThis.DecompressionStream = function DecompressionStream(format) {
			if (['gzip', 'deflate', 'deflate-raw'].indexOf(format) < 0) {
				throw new TypeError("Failed to construct 'DecompressionStream': Unsupported compression format: '" + format + "'");
			}
			var id = __go_wtc_ds_create(format);
			var ts = new TransformStream({
				transform: function(chunk, controller) {
					var u8 = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk.buffer ? chunk.buffer : chunk);
					__go_wtc_ds_write(id, __wtcU8ToHex(u8));
				},
				flush: function(controller) {
					var hexResult = __go_wtc_ds_close(id);
					if (hexResult.length > 0) controller.enqueue(__wtcHexToU8(hexResult));
				}
			});
			this.readable = ts.readable;
			this.writable = ts.writable;
		};
	}

	// --- MessageEvent ---
	if (typeof globalThis.MessageEvent === 'undefined') {
		globalThis.MessageEvent = function MessageEvent(type, opts) {
			globalThis.Event.call(this, type, opts);
			this.data = (opts && opts.data !== undefined) ? opts.data : null;
			this.origin = (opts && opts.origin) || '';
			this.lastEventId = (opts && opts.lastEventId) || '';
			this.source = (opts && opts.source) || null;
			this.ports = (opts && opts.ports) || [];
		};
		globalThis.MessageEvent.prototype = Object.create(globalThis.Event.prototype);
		globalThis.MessageEvent.prototype.constructor = globalThis.MessageEvent;
	}

	// --- MessagePort ---
	if (typeof globalThis.MessagePort === 'undefined') {
		var _etAEL = EventTarget.prototype.addEventListener;
		function MessagePort() {
			EventTarget.call(this);
			this._otherPort = null;
			this.onmessage = null;
			this.onmessageerror = null;
			this._started = false;
			this._queue = [];
		}
		MessagePort.prototype = Object.create(EventTarget.prototype);
		MessagePort.prototype.constructor = MessagePort;
		MessagePort.prototype._deliver = function(event) {
			this.dispatchEvent(event);
			if (this.onmessage) this.onmessage(event);
		};
		MessagePort.prototype.postMessage = function(data) {
			var target = this._otherPort;
			if (!target) return;
			var event = new MessageEvent('message', { data: data });
			if (target._started) {
				var t = target;
				queueMicrotask(function() { t._deliver(event); });
			} else {
				target._queue.push(event);
			}
		};
		MessagePort.prototype.start = function() {
			this._started = true;
			var self = this;
			while (this._queue.length > 0) {
				var event = this._queue.shift();
				(function(e) {
					queueMicrotask(function() { self._deliver(e); });
				})(event);
			}
		};
		MessagePort.prototype.close = function() {
			this._started = false;
			this._otherPort = null;
		};
		MessagePort.prototype.addEventListener = function(type, fn, opts) {
			_etAEL.call(this, type, fn, opts);
			if (type === 'message') this.start();
		};
		globalThis.MessagePort = MessagePort;
	}

	// --- MessageChannel ---
	if (typeof globalThis.MessageChannel === 'undefined') {
		globalThis.MessageChannel = function MessageChannel() {
			this.port1 = new MessagePort();
			this.port2 = new MessagePort();
			this.port1._otherPort = this.port2;
			this.port2._otherPort = this.port1;
		};
	}

	// --- ErrorEvent ---
	if (typeof globalThis.ErrorEvent === 'undefined') {
		globalThis.ErrorEvent = function ErrorEvent(type, opts) {
			globalThis.Event.call(this, type, opts);
			this.message = (opts && opts.message) || '';
			this.filename = (opts && opts.filename) || '';
			this.lineno = (opts && opts.lineno) || 0;
			this.colno = (opts && opts.colno) || 0;
			this.error = (opts && opts.error) || null;
		};
		globalThis.ErrorEvent.prototype = Object.create(globalThis.Event.prototype);
		globalThis.ErrorEvent.prototype.constructor = globalThis.ErrorEvent;
	}

	// --- PromiseRejectionEvent ---
	if (typeof globalThis.PromiseRejectionEvent === 'undefined') {
		globalThis.PromiseRejectionEvent = function PromiseRejectionEvent(type, opts) {
			globalThis.Event.call(this, type, opts);
			this.promise = (opts && opts.promise) || null;
			this.reason = (opts && opts.reason !== undefined) ? opts.reason : undefined;
		};
		globalThis.PromiseRejectionEvent.prototype = Object.create(globalThis.Event.prototype);
		globalThis.PromiseRejectionEvent.prototype.constructor = globalThis.PromiseRejectionEvent;
	}

	// --- URLPattern (Web Standard) ---
	if (typeof globalThis.URLPattern === 'undefined') {
		function _patternToRegex(pat, isPath) {
			if (!pat || pat === '*') return { re: /^.*$/, names: [] };
			var names = [], regex = '^', i = 0;
			while (i < pat.length) {
				var ch = pat[i];
				if (ch === ':') {
					var name = '', j = i + 1;
					while (j < pat.length && /\w/.test(pat[j])) name += pat[j++];
					names.push(name);
					regex += '([^' + (isPath ? '/' : '') + ']+)';
					i = j;
				} else if (ch === '*') {
					names.push('0');
					regex += '(.*)';
					i++;
				} else {
					regex += ch.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
					i++;
				}
			}
			regex += '$';
			return { re: new RegExp(regex), names: names };
		}
		function _parsePattern(input, baseURL) {
			var p = { protocol: '*', hostname: '*', port: '*', pathname: '*', search: '*', hash: '*' };
			if (typeof input === 'string') {
				if (baseURL) {
					var base = new URL(baseURL);
					p.protocol = base.protocol.replace(/:$/, '');
					p.hostname = base.hostname;
					p.port = base.port;
				}
				p.pathname = input;
			} else if (input && typeof input === 'object') {
				if (input.protocol !== undefined) p.protocol = input.protocol.replace(/:$/, '');
				if (input.hostname !== undefined) p.hostname = input.hostname;
				if (input.port !== undefined) p.port = String(input.port);
				if (input.pathname !== undefined) p.pathname = input.pathname;
				if (input.search !== undefined) p.search = input.search.replace(/^\?/, '');
				if (input.hash !== undefined) p.hash = input.hash.replace(/^#/, '');
			}
			return p;
		}
		function _matchCompiled(compiled, value) {
			var m = compiled.re.exec(value || '');
			if (!m) return null;
			var groups = {};
			for (var i = 0; i < compiled.names.length; i++) groups[compiled.names[i]] = m[i + 1] || '';
			return { input: value || '', groups: groups };
		}
		globalThis.URLPattern = function URLPattern(input, baseURL) {
			var p = _parsePattern(input, baseURL);
			this.protocol = p.protocol;
			this.hostname = p.hostname;
			this.port = p.port;
			this.pathname = p.pathname;
			this.search = p.search;
			this.hash = p.hash;
			this._compiled = {
				protocol: _patternToRegex(p.protocol, false),
				hostname: _patternToRegex(p.hostname, false),
				port: _patternToRegex(p.port, false),
				pathname: _patternToRegex(p.pathname, true),
				search: _patternToRegex(p.search, false),
				hash: _patternToRegex(p.hash, false)
			};
		};
		globalThis.URLPattern.prototype.test = function(input) {
			return this.exec(input) !== null;
		};
		globalThis.URLPattern.prototype.exec = function(input) {
			var url;
			if (typeof input === 'string') {
				try { url = new URL(input); } catch(e) { return null; }
			} else if (input && typeof input === 'object') {
				try { url = new URL(input.pathname || '/', 'http://' + (input.hostname || 'localhost')); } catch(e) { return null; }
			} else { return null; }
			var result = {}, c = this._compiled;
			var components = [
				['protocol', url.protocol.replace(/:$/, '')],
				['hostname', url.hostname],
				['port', url.port],
				['pathname', url.pathname],
				['search', url.search.replace(/^\?/, '')],
				['hash', url.hash.replace(/^#/, '')]
			];
			for (var i = 0; i < components.length; i++) {
				var name = components[i][0], val = components[i][1];
				var m = _matchCompiled(c[name], val);
				if (!m) return null;
				result[name] = m;
			}
			result.inputs = [typeof input === 'string' ? input : input];
			return result;
		};
	}
})();
`
}
