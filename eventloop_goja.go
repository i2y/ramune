//go:build goja

package ramune

import (
	"time"

	"github.com/dop251/goja"
)

// drainMicrotasks is a no-op for goja (promise jobs run inline during RunString).
func (r *Runtime) drainMicrotasks() {}

// nextDelayLocked returns the duration until the next timer fires.
// Returns 0 if there are immediates ready. Returns -1 if no pending timers.
func (r *Runtime) nextDelayLocked() time.Duration {
	result, err := r.safeRunString("__eventLoop.nextDelay()")
	if err != nil || result == nil {
		return -1
	}
	ms := result.ToFloat()
	if ms < 0 {
		return -1
	}
	return time.Duration(ms) * time.Millisecond
}

// evalBoolLocked evaluates a JS expression and returns its boolean value.
func (r *Runtime) evalBoolLocked(code string) bool {
	result, err := r.safeRunString(code)
	if err != nil || result == nil {
		return false
	}
	return result.ToBoolean()
}

// evalIsUndefinedLocked evaluates a JS expression and checks if the result is undefined.
func (r *Runtime) evalIsUndefinedLocked(code string) bool {
	result, err := r.safeRunString(code)
	if err != nil || result == nil {
		return true
	}
	return goja.IsUndefined(result)
}

// evalStringLocked evaluates a JS expression and returns its string value.
func (r *Runtime) evalStringLocked(code string) string {
	result, err := r.safeRunString(code)
	if err != nil || result == nil {
		return ""
	}
	return result.String()
}
