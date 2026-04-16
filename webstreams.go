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
		this._underlyingSource = underlyingSource || {};
		this._byteSource = this._underlyingSource.type === 'bytes';

		if (this._byteSource) {
			this._highWaterMark = (strategy && strategy.highWaterMark !== undefined) ? strategy.highWaterMark : 0;
			this._controller = new ReadableByteStreamController(this);
		} else {
			this._highWaterMark = (strategy && strategy.highWaterMark !== undefined) ? strategy.highWaterMark : 1;
			this._controller = new ReadableStreamDefaultController(this);
		}

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
		var hasPendingReads = this._pendingReads.length > 0 || (this._byobPendingReads && this._byobPendingReads.length > 0);
		if (!hasPendingReads && this._queue.length >= this._highWaterMark) return;
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
		if (this._byobPendingReads) {
			while (this._byobPendingReads.length > 0) {
				var entry = this._byobPendingReads.shift();
				entry.resolve({ value: new Uint8Array(entry.view.buffer, entry.view.byteOffset, 0), done: true });
			}
		}
		if (this._reader && this._reader._closedResolve) this._reader._closedResolve(undefined);
	};

	ReadableStream.prototype._error = function(e) {
		this._state = 'errored';
		this._storedError = e;
		while (this._pendingReads.length > 0) {
			this._pendingReads.shift().reject(e);
		}
		if (this._byobPendingReads) {
			while (this._byobPendingReads.length > 0) {
				this._byobPendingReads.shift().reject(e);
			}
		}
		if (this._reader && this._reader._closedReject) this._reader._closedReject(e);
	};

	Object.defineProperty(ReadableStream.prototype, 'locked', {
		get: function() { return this._locked; }
	});
	ReadableStream.prototype.getReader = function(opts) {
		if (opts && opts.mode === 'byob') {
			if (!this._byteSource) throw new TypeError('This readable stream does not support BYOB readers');
			return new ReadableStreamBYOBReader(this);
		}
		if (opts && opts.mode !== undefined && opts.mode !== 'byob') throw new RangeError('Invalid mode');
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
		if (this._locked) return Promise.reject(new TypeError('ReadableStream is locked'));
		if (dest._locked) return Promise.reject(new TypeError('WritableStream is locked'));
		var reader = this.getReader();
		var writer = dest.getWriter();
		options = options || {};
		var signal = options.signal;
		var shuttingDown = false;

		if (signal && signal.aborted) {
			var abortErr = signal.reason || new DOMException('The operation was aborted.', 'AbortError');
			var actions = [];
			if (!options.preventAbort) actions.push(function() { return writer.abort(abortErr); });
			if (!options.preventCancel) actions.push(function() { return reader.cancel(abortErr); });
			return Promise.all(actions.map(function(a) { return a(); })).then(function() {
				reader.releaseLock(); writer.releaseLock();
			}, function() {
				reader.releaseLock(); writer.releaseLock();
			}).then(function() { throw abortErr; });
		}

		function releaseBoth() { reader.releaseLock(); writer.releaseLock(); }

		return new Promise(function(resolve, reject) {
			var abortHandler;
			function shutdown(action, originalErr) {
				if (shuttingDown) return;
				shuttingDown = true;
				if (signal && abortHandler) signal.removeEventListener('abort', abortHandler);
				var p = action ? action() : Promise.resolve();
				p.then(function() {
					releaseBoth();
					if (originalErr) reject(originalErr); else resolve();
				}, function(e) {
					releaseBoth();
					reject(originalErr || e);
				});
			}

			function pump() {
				if (shuttingDown) return;
				writer.ready.then(function() {
					if (shuttingDown) return;
					reader.read().then(function(result) {
						if (shuttingDown) return;
						if (result.done) {
							if (!options.preventClose) {
								shutdown(function() { return writer.close(); });
							} else {
								shutdown();
							}
							return;
						}
						writer.write(result.value).then(pump, function(err) {
							if (!options.preventCancel) {
								shutdown(function() { return reader.cancel(err); }, err);
							} else {
								shutdown(null, err);
							}
						});
					}, function(err) {
						if (!options.preventAbort) {
							shutdown(function() { return writer.abort(err); }, err);
						} else {
							shutdown(null, err);
						}
					});
				});
			}

			if (signal) {
				abortHandler = function() {
					var abortErr = signal.reason || new DOMException('The operation was aborted.', 'AbortError');
					var actions = [];
					if (!options.preventAbort) actions.push(function() { return writer.abort(abortErr); });
					if (!options.preventCancel) actions.push(function() { return reader.cancel(abortErr); });
					shutdown(function() { return Promise.all(actions.map(function(a) { return a(); })); }, abortErr);
				};
				signal.addEventListener('abort', abortHandler);
			}
			pump();
		});
	};

	ReadableStream.prototype.pipeThrough = function(transform, options) {
		if (!transform || typeof transform !== 'object') throw new TypeError('parameter 1 is not of type object');
		if (!(transform.readable instanceof ReadableStream)) throw new TypeError('readable is not a ReadableStream');
		if (!(transform.writable instanceof WritableStream)) throw new TypeError('writable is not a WritableStream');
		if (this._locked) throw new TypeError('ReadableStream is locked');
		if (transform.writable._locked) throw new TypeError('WritableStream is locked');
		this.pipeTo(transform.writable, options);
		return transform.readable;
	};

	// Async iterator: for await (const chunk of stream)
	ReadableStream.prototype.values = function(opts) {
		var reader = this.getReader();
		var preventCancel = opts && opts.preventCancel;
		return {
			next: function() { return reader.read(); },
			return: function(value) {
				if (!preventCancel) reader.cancel(value);
				else reader.releaseLock();
				return Promise.resolve({ value: value, done: true });
			},
			[Symbol.asyncIterator]: function() { return this; }
		};
	};
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

	Object.defineProperty(WritableStream.prototype, 'locked', {
		get: function() { return this._locked; }
	});
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
		if (this._writing || this._writeQueue.length === 0) return;
		if (this._state !== 'writable' && !this._closeRequested) return;
		this._writing = true;
		var entry = this._writeQueue.shift();
		var self = this;
		function afterWrite() {
			self._writing = false;
			entry.resolve();
			if (self._writeQueue.length > 0) {
				self._processWrite();
			} else if (self._closePending) {
				var resolve = self._closePending;
				self._closePending = null;
				resolve();
			}
		}
		try {
			var result = this._underlyingSink.write(entry.chunk, this._controller);
			if (result && typeof result.then === 'function') {
				result.then(afterWrite, function(e) {
					self._writing = false;
					entry.reject(e);
					self._error(e);
				});
			} else {
				afterWrite();
			}
		} catch(e) {
			this._writing = false;
			entry.reject(e);
			this._error(e);
		}
	};

	WritableStream.prototype._close = function() {
		var self = this;
		function doClose() {
			self._state = 'closed';
			if (self._underlyingSink.close) {
				try {
					var result = self._underlyingSink.close();
					if (result && typeof result.then === 'function') {
						return result.then(function() {
							if (self._writer && self._writer._closedResolve) self._writer._closedResolve(undefined);
						});
					}
				} catch(e) { /* ignore close errors */ }
			}
			if (self._writer && self._writer._closedResolve) self._writer._closedResolve(undefined);
			return Promise.resolve();
		}
		// Drain pending writes before closing.
		if (this._writing || this._writeQueue.length > 0) {
			this._closeRequested = true;
			return new Promise(function(resolve) {
				self._closePending = resolve;
			}).then(doClose);
		}
		return doClose();
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

	// --- ReadableByteStreamController ---
	function ReadableByteStreamController(stream) {
		this._stream = stream;
		this._closeRequested = false;
		this._byobRequest = null;
	}
	ReadableByteStreamController.prototype.enqueue = function(chunk) {
		if (this._closeRequested) throw new TypeError('Cannot enqueue after close');
		var u8 = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk.buffer || chunk);
		if (this._stream._byobPendingReads && this._stream._byobPendingReads.length > 0) {
			var entry = this._stream._byobPendingReads.shift();
			var dest = entry.view;
			var n = Math.min(u8.length, dest.byteLength);
			new Uint8Array(dest.buffer, dest.byteOffset, n).set(u8.subarray(0, n));
			entry.resolve({ value: new Uint8Array(dest.buffer, dest.byteOffset, n), done: false });
		} else {
			this._stream._queue.push(u8);
			this._stream._drainQueue();
		}
	};
	ReadableByteStreamController.prototype.close = function() {
		if (this._closeRequested) return;
		this._closeRequested = true;
		this._stream._closeRequested = true;
		if (this._stream._queue.length === 0) {
			this._stream._finishClose();
		}
	};
	ReadableByteStreamController.prototype.error = function(e) {
		this._stream._error(e);
	};
	Object.defineProperty(ReadableByteStreamController.prototype, 'byobRequest', {
		get: function() { return this._byobRequest; }
	});
	Object.defineProperty(ReadableByteStreamController.prototype, 'desiredSize', {
		get: function() {
			if (this._stream._state === 'errored') return null;
			if (this._stream._state === 'closed') return 0;
			return this._stream._highWaterMark - this._stream._queue.length;
		}
	});

	// --- ReadableStreamBYOBRequest ---
	function ReadableStreamBYOBRequest(controller, view) {
		this._controller = controller;
		this.view = view;
	}
	ReadableStreamBYOBRequest.prototype.respond = function(bytesWritten) {
		var u8 = new Uint8Array(this.view.buffer, this.view.byteOffset, bytesWritten);
		this._controller.enqueue(u8);
		this.view = null;
	};
	ReadableStreamBYOBRequest.prototype.respondWithNewView = function(view) {
		this._controller.enqueue(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
		this.view = null;
	};

	// --- ReadableStreamBYOBReader ---
	function ReadableStreamBYOBReader(stream) {
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
	ReadableStreamBYOBReader.prototype.read = function(view) {
		var stream = this._stream;
		if (!stream) return Promise.reject(new TypeError('Reader has been released'));
		if (!(view instanceof ArrayBuffer) && !ArrayBuffer.isView(view)) {
			return Promise.reject(new TypeError('view must be a TypedArray or DataView'));
		}
		var u8View = view instanceof Uint8Array ? view : new Uint8Array(view.buffer || view, view.byteOffset || 0, view.byteLength);
		// If data is queued, copy directly
		if (stream._queue.length > 0) {
			var chunk = stream._queue.shift();
			var n = Math.min(chunk.length, u8View.byteLength);
			u8View.set(chunk.subarray(0, n));
			if (stream._closeRequested && stream._queue.length === 0) stream._finishClose();
			return Promise.resolve({ value: new Uint8Array(u8View.buffer, u8View.byteOffset, n), done: false });
		}
		if (stream._state === 'closed') return Promise.resolve({ value: new Uint8Array(u8View.buffer, u8View.byteOffset, 0), done: true });
		if (stream._state === 'errored') return Promise.reject(stream._storedError);
		// Wait for data via pull
		if (!stream._byobPendingReads) stream._byobPendingReads = [];
		return new Promise(function(resolve, reject) {
			stream._byobPendingReads.push({ view: u8View, resolve: resolve, reject: reject });
			stream._pullIfNeeded();
		});
	};
	ReadableStreamBYOBReader.prototype.releaseLock = function() {
		if (!this._stream) return;
		this._stream._reader = null;
		this._stream._locked = false;
		this._stream = null;
	};
	ReadableStreamBYOBReader.prototype.cancel = function(reason) {
		if (!this._stream) return Promise.reject(new TypeError('Reader has been released'));
		return this._stream.cancel(reason);
	};

	// --- TransformStreamDefaultController ---
	function TransformStreamDefaultController(readableController) {
		this._readableController = readableController;
	}
	TransformStreamDefaultController.prototype.enqueue = function(chunk) {
		this._readableController.enqueue(chunk);
	};
	TransformStreamDefaultController.prototype.error = function(reason) {
		this._readableController.error(reason);
	};
	TransformStreamDefaultController.prototype.terminate = function() {
		this._readableController.close();
	};
	Object.defineProperty(TransformStreamDefaultController.prototype, 'desiredSize', {
		get: function() { return this._readableController.desiredSize; }
	});

	// --- TransformStream ---
	function TransformStream(transformer, writableStrategy, readableStrategy) {
		transformer = transformer || {};
		var ctrl;
		var self = this;
		this.readable = new ReadableStream({
			start: function(c) { ctrl = new TransformStreamDefaultController(c); }
		}, readableStrategy);
		this.writable = new WritableStream({
			write: function(chunk) {
				if (transformer.transform) {
					return new Promise(function(resolve, reject) {
						try {
							var result = transformer.transform(chunk, ctrl);
							if (result && typeof result.then === 'function') {
								result.then(resolve, reject);
							} else { resolve(); }
						} catch(e) { reject(e); }
					});
				}
				ctrl.enqueue(chunk);
				return Promise.resolve();
			},
			close: function() {
				if (transformer.flush) {
					try {
						transformer.flush(ctrl);
					} catch(e) { ctrl.error(e); return; }
				}
				ctrl.terminate();
			},
			abort: function(reason) {
				ctrl.error(reason);
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

	// --- CountQueuingStrategy ---
	function CountQueuingStrategy(init) {
		this.highWaterMark = (init && init.highWaterMark !== undefined) ? init.highWaterMark : 1;
	}
	CountQueuingStrategy.prototype.size = function() { return 1; };

	// --- ByteLengthQueuingStrategy ---
	function ByteLengthQueuingStrategy(init) {
		this.highWaterMark = (init && init.highWaterMark !== undefined) ? init.highWaterMark : 0;
	}
	ByteLengthQueuingStrategy.prototype.size = function(chunk) {
		return chunk.byteLength !== undefined ? chunk.byteLength : chunk.length || 0;
	};

	ReadableByteStreamController.prototype[Symbol.toStringTag] = 'ReadableByteStreamController';
	ReadableStreamBYOBReader.prototype[Symbol.toStringTag] = 'ReadableStreamBYOBReader';
	ReadableStreamBYOBRequest.prototype[Symbol.toStringTag] = 'ReadableStreamBYOBRequest';
	ReadableStream.prototype[Symbol.toStringTag] = 'ReadableStream';
	WritableStream.prototype[Symbol.toStringTag] = 'WritableStream';
	TransformStream.prototype[Symbol.toStringTag] = 'TransformStream';
	ReadableStreamDefaultReader.prototype[Symbol.toStringTag] = 'ReadableStreamDefaultReader';
	ReadableStreamDefaultController.prototype[Symbol.toStringTag] = 'ReadableStreamDefaultController';
	WritableStreamDefaultWriter.prototype[Symbol.toStringTag] = 'WritableStreamDefaultWriter';
	WritableStreamDefaultController.prototype[Symbol.toStringTag] = 'WritableStreamDefaultController';
	CountQueuingStrategy.prototype[Symbol.toStringTag] = 'CountQueuingStrategy';
	ByteLengthQueuingStrategy.prototype[Symbol.toStringTag] = 'ByteLengthQueuingStrategy';

	globalThis.ReadableStream = ReadableStream;
	globalThis.ReadableStreamDefaultReader = ReadableStreamDefaultReader;
	globalThis.ReadableStreamDefaultController = ReadableStreamDefaultController;
	globalThis.ReadableByteStreamController = ReadableByteStreamController;
	globalThis.ReadableStreamBYOBReader = ReadableStreamBYOBReader;
	globalThis.ReadableStreamBYOBRequest = ReadableStreamBYOBRequest;
	globalThis.WritableStream = WritableStream;
	globalThis.WritableStreamDefaultWriter = WritableStreamDefaultWriter;
	globalThis.WritableStreamDefaultController = WritableStreamDefaultController;
	globalThis.TransformStream = TransformStream;
	globalThis.TransformStreamDefaultController = TransformStreamDefaultController;
	globalThis.CountQueuingStrategy = CountQueuingStrategy;
	globalThis.ByteLengthQueuingStrategy = ByteLengthQueuingStrategy;
})();
`
}
