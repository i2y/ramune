package ramune

// OnReady registers fn to be invoked once when the event loop first
// observes no pending work during a [Runtime.RunEventLoop] or
// [Runtime.RunEventLoopFor] call. Intended for platforms that need a
// deterministic "runtime has finished top-level initialization"
// signal — notably openworkers' Firecracker guest, which forwards a
// pending HTTP request only after the worker module's top-level
// awaits have resolved and event loop returns to idle.
//
// Calling OnReady again before the first fire replaces the callback.
// Calls after the first fire are no-ops. fn panics are recovered so
// they do not propagate into the event loop driver.
//
// OnReady does NOT fire at registration time or from [Runtime.Tick];
// only from the RunEventLoop[For] paths. This avoids accidental fire
// when a caller registers before any JS has been evaluated and the
// runtime happens to be idle.
func (r *Runtime) OnReady(fn func()) {
	r.onReadyMu.Lock()
	defer r.onReadyMu.Unlock()
	if r.onReadyDone {
		return
	}
	r.onReadyFn = fn
}

// fireOnReady runs the registered OnReady callback once, then clears
// it so subsequent calls are no-ops. Safe to call from the event
// loop driver on an idle-detection tick.
func (r *Runtime) fireOnReady() {
	r.onReadyMu.Lock()
	fn := r.onReadyFn
	if r.onReadyDone || fn == nil {
		r.onReadyMu.Unlock()
		return
	}
	r.onReadyDone = true
	r.onReadyFn = nil
	r.onReadyMu.Unlock()
	defer func() {
		_ = recover()
	}()
	fn()
}
