//go:build !quickjs

package ramune

import (
	"time"
	"unsafe"
)

// drainMicrotasks is a no-op for JSC (microtasks run inline during eval).
func (r *Runtime) drainMicrotasks() {}

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
