//go:build qjswasm && !quickjs && !goja

package ramune

import "time"

// drainMicrotasks runs JS_ExecutePendingJob until the queue is empty. The
// shim export execute_pending_jobs returns 1 if any job ran, 0 when the
// queue is drained, and -1 on error.
func (r *Runtime) drainMicrotasks() {
	if r.closed.Load() {
		return
	}
	for {
		res, err := r.wzExp.executePendingJobs.Call(r.wzCtx, uint64(r.qjsRT))
		if err != nil {
			return
		}
		if int32(res[0]) <= 0 {
			return
		}
	}
}

// drainUnprotectQueue is a no-op on qjswasm (mirrors engine_quickjs.go).
// Values live until Runtime.Close(); adding a queue here lands with M7
// along with its JSC-parity implementation.
func (r *Runtime) drainUnprotectQueue() {}

// nextDelayLocked reports how long until the next timer fires. -1 means
// no pending timers.
func (r *Runtime) nextDelayLocked() time.Duration {
	s := r.evalStringLocked("String(__eventLoop && __eventLoop.nextDelay ? __eventLoop.nextDelay() : -1)")
	if s == "" {
		return -1
	}
	// Expect integer ms as string.
	var ms int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' && i == 0 {
			continue
		}
		if c < '0' || c > '9' {
			return -1
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' {
			continue
		}
		ms = ms*10 + int64(c-'0')
	}
	if s[0] == '-' {
		ms = -ms
	}
	if ms < 0 {
		return -1
	}
	return time.Duration(ms) * time.Millisecond
}

// hasPendingLocked is defined in eventloop.go (backend-agnostic).

// -----------------------------------------------------------------------
// Scalar eval helpers used by the event loop and backend-agnostic code.
// -----------------------------------------------------------------------

func (r *Runtime) evalBoolLocked(code string) bool {
	h, err := r.rawEvalLocked(code, "<bool>", 0)
	if err != nil || isExceptionHandle(h) {
		return false
	}
	defer r.freeValueLocked(h)
	res, e := r.wzExp.valToBool.Call(r.wzCtx, uint64(r.qjsCtx), h)
	if e != nil {
		return false
	}
	return int32(res[0]) != 0
}

func (r *Runtime) evalStringLocked(code string) string {
	h, err := r.rawEvalLocked(code, "<string>", 0)
	if err != nil || isExceptionHandle(h) {
		return ""
	}
	defer r.freeValueLocked(h)
	s, _ := r.valToStringLocked(h)
	return s
}

func (r *Runtime) evalIsUndefinedLocked(code string) bool {
	h, err := r.rawEvalLocked(code, "<isundef>", 0)
	if err != nil {
		return true
	}
	if isExceptionHandle(h) {
		return true
	}
	defer r.freeValueLocked(h)
	res, e := r.wzExp.valKind.Call(r.wzCtx, uint64(r.qjsCtx), h)
	if e != nil {
		return false
	}
	return uint32(res[0])&valKindUndefined != 0
}

// newObjectLocked creates an empty object. Used by goja/quickjs parity
// helpers.
func (r *Runtime) newObjectLocked() (*Value, error) {
	res, err := r.wzExp.newObject.Call(r.wzCtx, uint64(r.qjsCtx))
	if err != nil {
		return nil, err
	}
	if isExceptionHandle(res[0]) {
		return nil, r.pullExceptionLocked()
	}
	return r.wrapValue(res[0]), nil
}

// newJSError constructs a JSError from the current pending exception.
func (r *Runtime) newJSError(context string) error {
	err := r.pullExceptionLocked()
	if jsErr, ok := err.(*JSError); ok {
		jsErr.Context = context
		return jsErr
	}
	return err
}
