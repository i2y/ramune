package ramune

// installWebStreams sets up ReadableStream, WritableStream, and TransformStream
// polyfills. Must be called with rt.mu held, after installNodeCompat and before
// installBunCompat (so Response/Request can use ReadableStream bodies).
func (r *Runtime) installWebStreams() error {
	return r.execLocked(webStreamsJSSource())
}

func webStreamsJSSource() string {
	return `
(function() {
	if (typeof globalThis.ReadableStream !== 'undefined') return;

	// --- ReadableStreamDefaultController ---
	function ReadableStreamDefaultController(stream) {
		this._stream = stream;
		this._closeRequested = false;
	}
	ReadableStreamDefaultController.prototype.enqueue = function(chunk) {
		if (this._closeRequested) throw new TypeError('Cannot enqueue after close');
		this._stream._queue.push(chunk);
		this._stream._drainQueue();
	};
	ReadableStreamDefaultController.prototype.close = function() {
		if (this._closeRequested) return;
		this._closeRequested = true;
		this._stream._closeRequested = true;
		if (this._stream._queue.length === 0) {
			this._stream._finishClose();
		}
	};
	ReadableStreamDefaultController.prototype.error = function(e) {
		this._stream._error(e);
	};
	Object.defineProperty(ReadableStreamDefaultController.prototype, 'desiredSize', {
		get: function() {
			if (this._stream._state === 'errored') return null;
			if (this._stream._state === 'closed') return 0;
			return this._stream._highWaterMark - this._stream._queue.length;
		}
	});

	// --- ReadableStreamDefaultReader ---
	function ReadableStreamDefaultReader(stream) {
		if (stream._locked) throw new TypeError('ReadableStream is locked');
		this._stream = stream;
		stream._reader = this;
		stream._locked = true;
		this._closedResolve = null;
		this._closedReject = null;
		var self = this;
		this.closed = new Promise(function(resolve, reject) {
			self._closedResolve = resolve;
			self._closedReject = reject;
		});
		if (stream._state === 'closed') this._closedResolve(undefined);
		else if (stream._state === 'errored') this._closedReject(stream._storedError);
	}
	ReadableStreamDefaultReader.prototype.read = function() {
		var stream = this._stream;
		if (!stream) return Promise.reject(new TypeError('Reader has been released'));
		if (stream._queue.length > 0) {
			var chunk = stream._queue.shift();
			if (stream._closeRequested && stream._queue.length === 0) {
				stream._finishClose();
			}
			return Promise.resolve({ value: chunk, done: false });
		}
		if (stream._state === 'closed') return Promise.resolve({ value: undefined, done: true });
		if (stream._state === 'errored') return Promise.reject(stream._storedError);
		return new Promise(function(resolve, reject) {
			stream._pendingReads.push({ resolve: resolve, reject: reject });
			stream._pullIfNeeded();
		});
	};
	ReadableStreamDefaultReader.prototype.releaseLock = function() {
		if (!this._stream) return;
		this._stream._reader = null;
		this._stream._locked = false;
		this._stream = null;
	};
	ReadableStreamDefaultReader.prototype.cancel = function(reason) {
		if (!this._stream) return Promise.reject(new TypeError('Reader has been released'));
		return this._stream.cancel(reason);
	};

	// --- ReadableStream ---
	function ReadableStream(underlyingSource, strategy) {
		this._queue = [];
		this._pendingReads = [];
		this._locked = false;
		this._reader = null;
		this._state = 'readable'; // 'readable' | 'closed' | 'errored'
		this._storedError = undefined;
		this._closeRequested = false;
		this._pulling = false;
		this._pullAgain = false;
		this._highWaterMark = (strategy && strategy.highWaterMark !== undefined) ? strategy.highWaterMark : 1;
		this._controller = new ReadableStreamDefaultController(this);
		this._underlyingSource = underlyingSource || {};

		var self = this;
		if (this._underlyingSource.start) {
			try {
				var result = this._underlyingSource.start(this._controller);
				if (result && typeof result.then === 'function') {
					result.then(function() { self._pullIfNeeded(); }, function(e) { self._error(e); });
				}
			} catch(e) { this._error(e); }
		}
	}

	ReadableStream.prototype._pullIfNeeded = function() {
		if (this._pulling || this._closeRequested || this._state !== 'readable') return;
		if (this._pendingReads.length === 0 && this._queue.length >= this._highWaterMark) return;
		if (!this._underlyingSource.pull) return;
		this._pulling = true;
		var self = this;
		try {
			var result = this._underlyingSource.pull(this._controller);
			if (result && typeof result.then === 'function') {
				result.then(function() {
					self._pulling = false;
					self._drainQueue();
					if (self._pullAgain) { self._pullAgain = false; self._pullIfNeeded(); }
				}, function(e) { self._error(e); });
			} else {
				this._pulling = false;
				this._drainQueue();
			}
		} catch(e) { this._error(e); }
	};

	ReadableStream.prototype._drainQueue = function() {
		while (this._pendingReads.length > 0 && this._queue.length > 0) {
			var reader = this._pendingReads.shift();
			var chunk = this._queue.shift();
			reader.resolve({ value: chunk, done: false });
		}
		if (this._closeRequested && this._queue.length === 0) {
			this._finishClose();
		}
	};

	ReadableStream.prototype._finishClose = function() {
		this._state = 'closed';
		while (this._pendingReads.length > 0) {
			this._pendingReads.shift().resolve({ value: undefined, done: true });
		}
		if (this._reader && this._reader._closedResolve) this._reader._closedResolve(undefined);
	};

	ReadableStream.prototype._error = function(e) {
		this._state = 'errored';
		this._storedError = e;
		while (this._pendingReads.length > 0) {
			this._pendingReads.shift().reject(e);
		}
		if (this._reader && this._reader._closedReject) this._reader._closedReject(e);
	};

	ReadableStream.prototype.getReader = function() {
		return new ReadableStreamDefaultReader(this);
	};

	ReadableStream.prototype.cancel = function(reason) {
		if (this._state === 'closed') return Promise.resolve();
		if (this._state === 'errored') return Promise.reject(this._storedError);
		this._state = 'closed';
		this._queue = [];
		while (this._pendingReads.length > 0) {
			this._pendingReads.shift().resolve({ value: undefined, done: true });
		}
		if (this._reader && this._reader._closedResolve) this._reader._closedResolve(undefined);
		if (this._underlyingSource.cancel) {
			try { return Promise.resolve(this._underlyingSource.cancel(reason)); } catch(e) { return Promise.reject(e); }
		}
		return Promise.resolve();
	};

	ReadableStream.prototype.tee = function() {
		var reader = this.getReader();
		var canceled1 = false, canceled2 = false;
		var done = false;
		var branch1Controller, branch2Controller;
		// Buffer for each branch: chunks read from source are pushed to both.
		var buf1 = [], buf2 = [];

		function pullSource() {
			if (done) return Promise.resolve();
			return reader.read().then(function(result) {
				if (result.done) {
					done = true;
					if (!canceled1) branch1Controller.close();
					if (!canceled2) branch2Controller.close();
					return;
				}
				if (!canceled1) buf1.push(result.value);
				if (!canceled2) buf2.push(result.value);
			});
		}
		var branch1 = new ReadableStream({
			start: function(c) { branch1Controller = c; },
			pull: function(c) {
				if (buf1.length > 0) { c.enqueue(buf1.shift()); return; }
				return pullSource().then(function() { if (buf1.length > 0) c.enqueue(buf1.shift()); });
			},
			cancel: function() { canceled1 = true; if (canceled2) reader.cancel(); }
		});
		var branch2 = new ReadableStream({
			start: function(c) { branch2Controller = c; },
			pull: function(c) {
				if (buf2.length > 0) { c.enqueue(buf2.shift()); return; }
				return pullSource().then(function() { if (buf2.length > 0) c.enqueue(buf2.shift()); });
			},
			cancel: function() { canceled2 = true; if (canceled1) reader.cancel(); }
		});
		return [branch1, branch2];
	};

	ReadableStream.prototype.pipeTo = function(dest, options) {
		var reader = this.getReader();
		var writer = dest.getWriter();
		options = options || {};
		function pump() {
			return reader.read().then(function(result) {
				if (result.done) {
					if (!options.preventClose) return writer.close();
					writer.releaseLock();
					return;
				}
				return writer.write(result.value).then(pump);
			});
		}
		return pump().catch(function(err) {
			if (!options.preventAbort) writer.abort(err);
			throw err;
		});
	};

	ReadableStream.prototype.pipeThrough = function(transform, options) {
		this.pipeTo(transform.writable, options);
		return transform.readable;
	};

	// Async iterator support: for await (const chunk of stream)
	ReadableStream.prototype[Symbol.asyncIterator] = function() {
		var reader = this.getReader();
		return {
			next: function() { return reader.read(); },
			return: function() {
				reader.releaseLock();
				return Promise.resolve({ value: undefined, done: true });
			}
		};
	};

	// --- WritableStreamDefaultController ---
	function WritableStreamDefaultController(stream) {
		this._stream = stream;
	}
	WritableStreamDefaultController.prototype.error = function(e) {
		this._stream._error(e);
	};

	// --- WritableStreamDefaultWriter ---
	function WritableStreamDefaultWriter(stream) {
		if (stream._locked) throw new TypeError('WritableStream is locked');
		this._stream = stream;
		stream._writer = this;
		stream._locked = true;
		var self = this;
		this._readyResolve = null;
		this.ready = new Promise(function(resolve) { self._readyResolve = resolve; });
		if (stream._state === 'writable') this._readyResolve();
		this._closedResolve = null;
		this._closedReject = null;
		this.closed = new Promise(function(resolve, reject) {
			self._closedResolve = resolve;
			self._closedReject = reject;
		});
		if (stream._state === 'closed') this._closedResolve(undefined);
		else if (stream._state === 'errored') this._closedReject(stream._storedError);
	}
	Object.defineProperty(WritableStreamDefaultWriter.prototype, 'desiredSize', {
		get: function() {
			if (!this._stream) return null;
			return this._stream._highWaterMark - this._stream._writeQueue.length;
		}
	});
	WritableStreamDefaultWriter.prototype.write = function(chunk) {
		var stream = this._stream;
		if (!stream) return Promise.reject(new TypeError('Writer has been released'));
		if (stream._state !== 'writable') return Promise.reject(new TypeError('Stream is not writable'));
		return stream._write(chunk);
	};
	WritableStreamDefaultWriter.prototype.close = function() {
		var stream = this._stream;
		if (!stream) return Promise.reject(new TypeError('Writer has been released'));
		return stream._close();
	};
	WritableStreamDefaultWriter.prototype.abort = function(reason) {
		var stream = this._stream;
		if (!stream) return Promise.reject(new TypeError('Writer has been released'));
		return stream.abort(reason);
	};
	WritableStreamDefaultWriter.prototype.releaseLock = function() {
		if (!this._stream) return;
		this._stream._writer = null;
		this._stream._locked = false;
		this._stream = null;
	};

	// --- WritableStream ---
	function WritableStream(underlyingSink, strategy) {
		this._writeQueue = [];
		this._locked = false;
		this._writer = null;
		this._state = 'writable'; // 'writable' | 'closed' | 'errored'
		this._storedError = undefined;
		this._highWaterMark = (strategy && strategy.highWaterMark !== undefined) ? strategy.highWaterMark : 1;
		this._controller = new WritableStreamDefaultController(this);
		this._underlyingSink = underlyingSink || {};
		this._writing = false;

		if (this._underlyingSink.start) {
			try { this._underlyingSink.start(this._controller); } catch(e) { this._error(e); }
		}
	}

	WritableStream.prototype.getWriter = function() {
		return new WritableStreamDefaultWriter(this);
	};

	WritableStream.prototype._write = function(chunk) {
		var self = this;
		if (!this._underlyingSink.write) return Promise.resolve();
		return new Promise(function(resolve, reject) {
			self._writeQueue.push({ chunk: chunk, resolve: resolve, reject: reject });
			self._processWrite();
		});
	};

	WritableStream.prototype._processWrite = function() {
		if (this._writing || this._writeQueue.length === 0 || this._state !== 'writable') return;
		this._writing = true;
		var entry = this._writeQueue.shift();
		var self = this;
		try {
			var result = this._underlyingSink.write(entry.chunk, this._controller);
			if (result && typeof result.then === 'function') {
				result.then(function() {
					self._writing = false;
					entry.resolve();
					self._processWrite();
				}, function(e) {
					self._writing = false;
					entry.reject(e);
					self._error(e);
				});
			} else {
				this._writing = false;
				entry.resolve();
				this._processWrite();
			}
		} catch(e) {
			this._writing = false;
			entry.reject(e);
			this._error(e);
		}
	};

	WritableStream.prototype._close = function() {
		var self = this;
		this._state = 'closed';
		if (this._underlyingSink.close) {
			try {
				var result = this._underlyingSink.close();
				if (result && typeof result.then === 'function') {
					return result.then(function() {
						if (self._writer && self._writer._closedResolve) self._writer._closedResolve(undefined);
					});
				}
			} catch(e) { /* ignore close errors */ }
		}
		if (this._writer && this._writer._closedResolve) this._writer._closedResolve(undefined);
		return Promise.resolve();
	};

	WritableStream.prototype._error = function(e) {
		this._state = 'errored';
		this._storedError = e;
		while (this._writeQueue.length > 0) {
			this._writeQueue.shift().reject(e);
		}
		if (this._writer && this._writer._closedReject) this._writer._closedReject(e);
	};

	WritableStream.prototype.abort = function(reason) {
		if (this._state === 'closed' || this._state === 'errored') return Promise.resolve();
		this._error(reason || new Error('Aborted'));
		if (this._underlyingSink.abort) {
			try { return Promise.resolve(this._underlyingSink.abort(reason)); } catch(e) { return Promise.reject(e); }
		}
		return Promise.resolve();
	};

	// --- TransformStream ---
	function TransformStream(transformer, writableStrategy, readableStrategy) {
		transformer = transformer || {};
		var readableController;
		var self = this;
		this.readable = new ReadableStream({
			start: function(c) { readableController = c; }
		}, readableStrategy);
		this.writable = new WritableStream({
			write: function(chunk) {
				if (transformer.transform) {
					return new Promise(function(resolve, reject) {
						try {
							var result = transformer.transform(chunk, {
								enqueue: function(c) { readableController.enqueue(c); },
								error: function(e) { readableController.error(e); },
								terminate: function() { readableController.close(); }
							});
							if (result && typeof result.then === 'function') {
								result.then(resolve, reject);
							} else { resolve(); }
						} catch(e) { reject(e); }
					});
				}
				readableController.enqueue(chunk);
				return Promise.resolve();
			},
			close: function() {
				if (transformer.flush) {
					try {
						transformer.flush({
							enqueue: function(c) { readableController.enqueue(c); },
							error: function(e) { readableController.error(e); },
							terminate: function() { readableController.close(); }
						});
					} catch(e) { readableController.error(e); return; }
				}
				readableController.close();
			},
			abort: function(reason) {
				readableController.error(reason);
			}
		}, writableStrategy);
	}

	// --- ReadableStream.from() static method ---
	ReadableStream.from = function(iterable) {
		if (iterable && typeof iterable[Symbol.asyncIterator] === 'function') {
			var iter = iterable[Symbol.asyncIterator]();
			return new ReadableStream({
				pull: function(controller) {
					return iter.next().then(function(result) {
						if (result.done) controller.close();
						else controller.enqueue(result.value);
					});
				},
				cancel: function() { if (iter.return) iter.return(); }
			});
		}
		if (iterable && typeof iterable[Symbol.iterator] === 'function') {
			var iter = iterable[Symbol.iterator]();
			return new ReadableStream({
				pull: function(controller) {
					var result = iter.next();
					if (result.done) controller.close();
					else controller.enqueue(result.value);
				},
				cancel: function() { if (iter.return) iter.return(); }
			});
		}
		throw new TypeError('ReadableStream.from requires an iterable');
	};

	globalThis.ReadableStream = ReadableStream;
	globalThis.WritableStream = WritableStream;
	globalThis.TransformStream = TransformStream;
	globalThis.ReadableStreamDefaultReader = ReadableStreamDefaultReader;
	globalThis.WritableStreamDefaultWriter = WritableStreamDefaultWriter;
})();
`
}
