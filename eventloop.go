package ramune

import (
	"context"
	"fmt"
	"time"
	"unsafe"
)

// installEventLoop sets up the JavaScript event loop infrastructure.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) installEventLoop() error {
	return r.execLocked(eventLoopJSSource())
}

// Wake signals the event loop to process events immediately.
// Safe to call from any goroutine. Non-blocking.
func (r *Runtime) Wake() {
	select {
	case r.wakeCh <- struct{}{}:
	default:
	}
}

// Tick processes one round of the event loop (immediates + ready timers).
// Returns true if there are still pending timers or immediates.
func (r *Runtime) Tick() (bool, error) {
	if r.closed.Load() {
		return false, ErrAlreadyClosed
	}
	var pending bool
	var err error
	r.dispatch(func() {
		r.tickManagers()
		if e := r.execLocked("__eventLoop.tick()"); e != nil {
			err = e
			return
		}
		pending = r.hasPendingLocked()
	})
	return pending, err
}

// tickManagers drains events from all async I/O managers.
// Must be called on the dedicated JSC goroutine.
func (r *Runtime) tickManagers() {
	if r.bunSrv != nil {
		r.bunSrv.processRequests(r)
		if r.bunSrv.wsEnabled {
			r.bunSrv.processWSEvents(r)
		}
	}
	if r.fsMgr != nil {
		r.fsMgr.processEvents(r)
	}
	if r.fswatchMgr != nil {
		r.fswatchMgr.processEvents(r)
	}
	if r.procMgr != nil {
		r.procMgr.processEvents(r)
	}
	if r.sockMgr != nil {
		r.sockMgr.processEvents(r)
	}
	if r.workerMgr != nil {
		r.workerMgr.processEvents(r)
	}
}

// RunEventLoop processes the event loop until all timers complete.
// Default timeout is 30 seconds.
func (r *Runtime) RunEventLoop() error {
	return r.RunEventLoopFor(30 * time.Second)
}

// RunEventLoopFor processes the event loop until all timers complete
// or the timeout is reached.
func (r *Runtime) RunEventLoopFor(timeout time.Duration) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}
	deadline := time.Now().Add(timeout)

	for {
		var pending bool
		var delay time.Duration
		var err error

		r.dispatch(func() {
			r.tickManagers()
			if e := r.execLocked("__eventLoop.tick()"); e != nil {
				err = e
				return
			}
			pending = r.hasPendingLocked()
			if pending {
				delay = r.nextDelayLocked()
			}
		})

		if err != nil {
			return err
		}
		if !pending {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ramune: event loop timeout after %v", timeout)
		}

		if delay > 0 {
			remaining := time.Until(deadline)
			if delay > remaining {
				delay = remaining
			}
			timer := time.NewTimer(delay)
			select {
			case <-r.wakeCh:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

// EvalAsync evaluates JavaScript code that may return a Promise,
// runs the event loop until the Promise resolves, and returns the result.
func (r *Runtime) EvalAsync(code string) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}

	// Set up promise resolution tracking and wrap in Promise.resolve
	// so both sync values and Promises are handled uniformly.
	setup := fmt.Sprintf(
		`globalThis.__asyncDone=false;globalThis.__asyncResult=undefined;globalThis.__asyncError=undefined;`+
			`Promise.resolve(%s).then(`+
			`function(v){globalThis.__asyncResult=v;globalThis.__asyncDone=true;},`+
			`function(e){globalThis.__asyncError=String(e);globalThis.__asyncDone=true;});`,
		code)
	if err := r.Exec(setup); err != nil {
		return nil, err
	}

	return r.awaitAsyncResult(30 * time.Second)
}

// awaitAsyncResult polls the event loop until __asyncDone is true.
func (r *Runtime) awaitAsyncResult(timeout time.Duration) (*Value, error) {
	deadline := time.Now().Add(timeout)

	for {
		var done bool
		var hasErr bool
		var errMsg string
		var result *Value
		var evalErr error
		var pending bool
		var delay time.Duration
		var needRecheck bool

		r.dispatch(func() {
			r.tickManagers()
			done = r.evalBoolLocked("globalThis.__asyncDone")
			if done {
				hasErr = !r.evalIsUndefinedLocked("globalThis.__asyncError")
				if hasErr {
					errMsg = r.evalStringLocked("globalThis.__asyncError")
				} else {
					result, evalErr = r.evalLocked("globalThis.__asyncResult")
				}
				return
			}

			if e := r.execLocked("__eventLoop.tick()"); e != nil {
				evalErr = e
				return
			}

			done = r.evalBoolLocked("globalThis.__asyncDone")
			if done {
				needRecheck = true
				return
			}

			pending = r.hasPendingLocked()
			if pending {
				delay = r.nextDelayLocked()
			}
		})

		if evalErr != nil {
			return nil, evalErr
		}
		if done && !needRecheck {
			if hasErr {
				return nil, &JSError{Context: "EvalAsync", Message: errMsg}
			}
			return result, evalErr
		}
		if needRecheck {
			continue
		}
		if !pending {
			return nil, &JSError{Context: "EvalAsync", Message: "Promise did not resolve and no pending timers"}
		}

		if time.Now().After(deadline) {
			return nil, &JSError{Context: "EvalAsync", Message: "timeout waiting for Promise"}
		}

		if delay > 0 {
			remaining := time.Until(deadline)
			if delay > remaining {
				delay = remaining
			}
			timer := time.NewTimer(delay)
			select {
			case <-r.wakeCh:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

// RunEventLoopWithContext processes the event loop until all timers complete
// or the context is cancelled/expired.
func (r *Runtime) RunEventLoopWithContext(ctx context.Context) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var pending bool
		var delay time.Duration
		var err error

		r.dispatch(func() {
			r.tickManagers()
			if e := r.execLocked("__eventLoop.tick()"); e != nil {
				err = e
				return
			}
			pending = r.hasPendingLocked()
			if pending {
				delay = r.nextDelayLocked()
			}
		})

		if err != nil {
			return err
		}
		if !pending {
			return nil
		}

		if time.Now().After(deadline) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("ramune: event loop timeout")
		}

		if delay > 0 {
			remaining := time.Until(deadline)
			if delay > remaining {
				delay = remaining
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-r.wakeCh:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

// EvalAsyncWithContext evaluates JavaScript code that may return a Promise,
// runs the event loop until the Promise resolves or the context is
// cancelled/expired, and returns the result.
func (r *Runtime) EvalAsyncWithContext(ctx context.Context, code string) (*Value, error) {
	if r.closed.Load() {
		return nil, ErrAlreadyClosed
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Set up promise resolution tracking and wrap in Promise.resolve
	// so both sync values and Promises are handled uniformly.
	setup := fmt.Sprintf(
		`globalThis.__asyncDone=false;globalThis.__asyncResult=undefined;globalThis.__asyncError=undefined;`+
			`Promise.resolve(%s).then(`+
			`function(v){globalThis.__asyncResult=v;globalThis.__asyncDone=true;},`+
			`function(e){globalThis.__asyncError=String(e);globalThis.__asyncDone=true;});`,
		code)
	if err := r.Exec(setup); err != nil {
		return nil, err
	}

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(30 * time.Second)
	}
	timeout := time.Until(deadline)

	return r.awaitAsyncResultWithContext(ctx, timeout)
}

// awaitAsyncResultWithContext polls the event loop until __asyncDone is true
// or the context is cancelled/expired.
func (r *Runtime) awaitAsyncResultWithContext(ctx context.Context, timeout time.Duration) (*Value, error) {
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var done bool
		var hasErr bool
		var errMsg string
		var result *Value
		var evalErr error
		var pending bool
		var delay time.Duration
		var needRecheck bool

		r.dispatch(func() {
			r.tickManagers()
			done = r.evalBoolLocked("globalThis.__asyncDone")
			if done {
				hasErr = !r.evalIsUndefinedLocked("globalThis.__asyncError")
				if hasErr {
					errMsg = r.evalStringLocked("globalThis.__asyncError")
				} else {
					result, evalErr = r.evalLocked("globalThis.__asyncResult")
				}
				return
			}

			if e := r.execLocked("__eventLoop.tick()"); e != nil {
				evalErr = e
				return
			}

			done = r.evalBoolLocked("globalThis.__asyncDone")
			if done {
				needRecheck = true
				return
			}

			pending = r.hasPendingLocked()
			if pending {
				delay = r.nextDelayLocked()
			}
		})

		if evalErr != nil {
			return nil, evalErr
		}
		if done && !needRecheck {
			if hasErr {
				return nil, &JSError{Context: "EvalAsyncWithContext", Message: errMsg}
			}
			return result, evalErr
		}
		if needRecheck {
			continue
		}
		if !pending {
			return nil, &JSError{Context: "EvalAsyncWithContext", Message: "Promise did not resolve and no pending timers"}
		}

		if time.Now().After(deadline) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &JSError{Context: "EvalAsyncWithContext", Message: "timeout waiting for Promise"}
		}

		if delay > 0 {
			remaining := time.Until(deadline)
			if delay > remaining {
				delay = remaining
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-r.wakeCh:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

// --- internal helpers (must be called on the dedicated JSC goroutine) ---

// hasPendingLocked returns true if there are pending timers, immediates,
// or active async processes.
func (r *Runtime) hasPendingLocked() bool {
	if r.evalBoolLocked("__eventLoop.hasPending()") {
		return true
	}
	// Check for active async processes.
	if r.procMgr != nil && r.procMgr.hasActive() {
		return true
	}
	// Check for active async sockets.
	if r.sockMgr != nil && r.sockMgr.hasActive() {
		return true
	}
	// Check for active Bun server.
	if r.bunSrv != nil && r.bunSrv.hasActive() {
		return true
	}
	// Check for active workers.
	if r.workerMgr != nil && r.workerMgr.hasActive() {
		return true
	}
	// Check for pending async fs operations.
	if r.fsMgr != nil && r.evalBoolLocked("typeof __fsPendingCount === 'function' && __fsPendingCount() > 0") {
		return true
	}
	return false
}

// nextDelayLocked returns the duration until the next timer fires.
// Returns 0 if there are immediates ready. Returns -1 if no pending timers.
func (r *Runtime) nextDelayLocked() time.Duration {
	jsStr := r.jsStringCreateWithUTF8CString("__eventLoop.nextDelay()")
	defer r.jsStringRelease(jsStr)
	result := r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, 0)
	if result == 0 {
		return -1
	}
	ms := r.jsValueToNumber(r.ctx, result, 0)
	if ms < 0 {
		return -1
	}
	return time.Duration(ms) * time.Millisecond
}

// evalBoolLocked evaluates a JS expression and returns its boolean value.
func (r *Runtime) evalBoolLocked(code string) bool {
	jsStr := r.jsStringCreateWithUTF8CString(code)
	defer r.jsStringRelease(jsStr)
	result := r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, 0)
	if result == 0 {
		return false
	}
	return r.jsValueToBoolean(r.ctx, result)
}

// evalIsUndefinedLocked evaluates a JS expression and checks if the result is undefined.
func (r *Runtime) evalIsUndefinedLocked(code string) bool {
	jsStr := r.jsStringCreateWithUTF8CString(code)
	defer r.jsStringRelease(jsStr)
	result := r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, 0)
	if result == 0 {
		return true
	}
	return r.jsValueIsUndefined(r.ctx, result)
}

// evalStringLocked evaluates a JS expression and returns its string value.
func (r *Runtime) evalStringLocked(code string) string {
	jsStr := r.jsStringCreateWithUTF8CString(code)
	defer r.jsStringRelease(jsStr)
	var exc uintptr
	result := r.jsEvaluateScript(r.ctx, jsStr, 0, 0, 0, uintptr(unsafe.Pointer(&exc)))
	if result == 0 {
		return ""
	}
	return r.jsValueToGoString(result)
}

func eventLoopJSSource() string {
	return `
(function() {
	var __timers = {};
	var __nextId = 1;
	var __immediates = [];

	globalThis.__eventLoop = {
		tick: function() {
			// Process immediates first (like Node.js setImmediate).
			var imms = __immediates.slice();
			__immediates = [];
			for (var i = 0; i < imms.length; i++) {
				try { imms[i](); } catch(e) {}
			}

			// Process ready timers.
			var now = Date.now();
			var ids = Object.keys(__timers);
			for (var i = 0; i < ids.length; i++) {
				var id = ids[i];
				var t = __timers[id];
				if (t && now >= t.fireAt) {
					if (t.interval) {
						t.fireAt = now + t.delay;
					} else {
						delete __timers[id];
					}
					try { t.fn(); } catch(e) {}
				}
			}
		},
		hasPending: function() {
			return Object.keys(__timers).length > 0 || __immediates.length > 0;
		},
		nextDelay: function() {
			if (__immediates.length > 0) return 0;
			var now = Date.now();
			var min = Infinity;
			var ids = Object.keys(__timers);
			for (var i = 0; i < ids.length; i++) {
				var t = __timers[ids[i]];
				if (t) {
					var d = t.fireAt - now;
					if (d < min) min = d;
				}
			}
			return min === Infinity ? -1 : Math.max(0, min);
		}
	};

	globalThis.setTimeout = function(fn, delay) {
		if (typeof fn !== 'function') return 0;
		var id = __nextId++;
		__timers[id] = { fn: fn, fireAt: Date.now() + (delay || 0), interval: false };
		return id;
	};
	globalThis.clearTimeout = function(id) { delete __timers[id]; };

	globalThis.setInterval = function(fn, delay) {
		if (typeof fn !== 'function') return 0;
		var id = __nextId++;
		__timers[id] = { fn: fn, fireAt: Date.now() + (delay || 0), delay: delay || 0, interval: true };
		return id;
	};
	globalThis.clearInterval = function(id) { delete __timers[id]; };

	globalThis.setImmediate = function(fn) {
		if (typeof fn === 'function') __immediates.push(fn);
		return __nextId++;
	};
	globalThis.clearImmediate = function() {};

	if (typeof globalThis.queueMicrotask === 'undefined') {
		globalThis.queueMicrotask = function(fn) { Promise.resolve().then(fn); };
	}
})();
`
}
