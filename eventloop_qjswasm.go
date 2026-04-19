//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"strconv"
	"time"
)

// drainMicrotasks runs JS_ExecutePendingJob via the fastschema runtime.
// Promise microtasks queued during an Eval are drained here.
func (r *Runtime) drainMicrotasks() {
	if r.closed.Load() {
		return
	}
	// fastschema doesn't expose a direct pending-job drain, but its
	// Eval / Invoke paths drain internally. A no-op here is acceptable
	// because callers that care (Tick, awaitAsyncResult) will re-invoke
	// eval which drains.
}

// drainUnprotectQueue is a no-op: fastschema refcounts JSValues and we
// Free() each wrapper on Close(), so there's nothing to batch.
func (r *Runtime) drainUnprotectQueue() {}

// nextDelayLocked reports how long until the next timer fires. -1 means
// no pending timers.
func (r *Runtime) nextDelayLocked() time.Duration {
	s := r.evalStringLocked("(__eventLoop && __eventLoop.nextDelay ? String(__eventLoop.nextDelay()) : '-1')")
	if s == "" {
		return -1
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms < 0 {
		return -1
	}
	return time.Duration(ms) * time.Millisecond
}

// Scalar eval helpers used by the event loop and shared code.

func (r *Runtime) evalBoolLocked(code string) bool {
	v, err := r.qjsCtx.Eval("<bool>", codeOpt(code))
	if err != nil {
		return false
	}
	defer v.Free()
	return v.Bool()
}

func (r *Runtime) evalStringLocked(code string) string {
	v, err := r.qjsCtx.Eval("<string>", codeOpt(code))
	if err != nil {
		return ""
	}
	defer v.Free()
	return v.String()
}

func (r *Runtime) evalIsUndefinedLocked(code string) bool {
	v, err := r.qjsCtx.Eval("<isundef>", codeOpt(code))
	if err != nil {
		return true
	}
	defer v.Free()
	return v.IsUndefined()
}

// newObjectLocked creates an empty JS object wrapped in our Value type.
func (r *Runtime) newObjectLocked() (*Value, error) {
	return r.wrapValue(r.qjsCtx.NewObject()), nil
}

// newJSError attaches the given context string to the most recent
// pending exception. qjswasm-specific variant of what other backends
// do — fastschema surfaces exceptions through eval's error return, so
// we don't have a separate "pending exception" to pull.
func (r *Runtime) newJSError(context string) error {
	return &JSError{Context: context, Message: "qjswasm: no pending exception captured"}
}
