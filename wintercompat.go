package ramune

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"io"
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
	if err := r.registerFuncLocked("__go_wtc_compress", goWTCCompress); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_wtc_decompress", goWTCDecompress); err != nil {
		return err
	}
	return r.execLocked(winterTCJSSource())
}

// goWTCCompress: args [format, dataHex] -> hexCompressed
// Formats: "gzip" (RFC 1952), "deflate" (RFC 1950 zlib), "deflate-raw" (RFC 1951).
func goWTCCompress(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("compress: format and data required")
	}
	format, _ := args[0].(string)
	dataHex, _ := args[1].(string)
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	var w io.WriteCloser
	switch format {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "deflate":
		w = zlib.NewWriter(&buf)
	case "deflate-raw":
		var err2 error
		w, err2 = flate.NewWriter(&buf, flate.DefaultCompression)
		if err2 != nil {
			return nil, err2
		}
	default:
		return nil, fmt.Errorf("unsupported compression format: %s", format)
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return hex.EncodeToString(buf.Bytes()), nil
}

// goWTCDecompress: args [format, dataHex] -> hexDecompressed
func goWTCDecompress(args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("decompress: format and data required")
	}
	format, _ := args[0].(string)
	dataHex, _ := args[1].(string)
	compressed, err := hex.DecodeString(dataHex)
	if err != nil {
		return nil, err
	}

	var reader io.ReadCloser
	switch format {
	case "gzip":
		r, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, err
		}
		reader = r
	case "deflate":
		r, err := zlib.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, err
		}
		reader = r
	case "deflate-raw":
		reader = flate.NewReader(bytes.NewReader(compressed))
	default:
		return nil, fmt.Errorf("unsupported compression format: %s", format)
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

	// --- CompressionStream / DecompressionStream (Web Compression API) ---
	function __wtcCodecStream(name, goFn) {
		return function(format) {
			if (['gzip', 'deflate', 'deflate-raw'].indexOf(format) < 0) {
				throw new TypeError("Failed to construct '" + name + "': Unsupported compression format: '" + format + "'");
			}
			var chunks = [];
			var ts = new TransformStream({
				transform: function(chunk, controller) {
					var u8 = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk.buffer ? chunk.buffer : chunk);
					chunks.push(u8);
				},
				flush: function(controller) {
					var merged = __wtcMergeChunks(chunks);
					var hexResult = goFn(format, __wtcU8ToHex(merged));
					controller.enqueue(__wtcHexToU8(hexResult));
				}
			});
			this.readable = ts.readable;
			this.writable = ts.writable;
		};
	}
	if (typeof globalThis.CompressionStream === 'undefined') {
		globalThis.CompressionStream = __wtcCodecStream('CompressionStream', __go_wtc_compress);
	}
	if (typeof globalThis.DecompressionStream === 'undefined') {
		globalThis.DecompressionStream = __wtcCodecStream('DecompressionStream', __go_wtc_decompress);
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
