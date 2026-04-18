//go:build quickjs && !goja

package ramune

import (
	"time"
	"unsafe"

	libc "modernc.org/libc"
	lib "modernc.org/libquickjs"
	"modernc.org/quickjs"
)

// qjsRuntime mirrors the unexported quickjs.runtime struct layout.
type qjsRuntime struct {
	cRuntime uintptr
	tls      *libc.TLS
}

// qjsVM mirrors the unexported quickjs.VM struct layout for field access.
type qjsVM struct {
	cContext      uintptr
	goFuncs       map[string]int32
	int32_16      lib.TJSValue
	int32_2       lib.TJSValue
	runtime       *qjsRuntime
	timeout       time.Duration
	interruptData uintptr
	toStringArgv  uintptr
}

// executePendingJobs drains the QuickJS microtask/promise job queue.
// Must be called after Eval to allow Promises to resolve.
func (r *Runtime) executePendingJobs() {
	vm := (*qjsVM)(unsafe.Pointer(r.vm))
	rt := vm.runtime
	for {
		ret := lib.XJS_ExecutePendingJob(rt.tls, rt.cRuntime, 0)
		if ret <= 0 {
			break
		}
	}
}

// drainMicrotasks executes pending QuickJS promise jobs.
func (r *Runtime) drainMicrotasks() {
	r.executePendingJobs()
}

// nextDelayLocked returns the duration until the next timer fires.
func (r *Runtime) nextDelayLocked() time.Duration {
	result, err := r.vm.Eval("__eventLoop.nextDelay()", quickjs.EvalGlobal)
	if err != nil {
		return -1
	}
	switch ms := result.(type) {
	case float64:
		if ms < 0 {
			return -1
		}
		return time.Duration(ms) * time.Millisecond
	case int:
		if ms < 0 {
			return -1
		}
		return time.Duration(ms) * time.Millisecond
	}
	return -1
}

// evalBoolLocked evaluates a JS expression and returns its boolean value.
func (r *Runtime) evalBoolLocked(code string) bool {
	r.executePendingJobs()
	result, err := r.vm.Eval(code, quickjs.EvalGlobal)
	if err != nil {
		return false
	}
	if b, ok := result.(bool); ok {
		return b
	}
	return false
}

// evalIsUndefinedLocked evaluates a JS expression and checks if the result is undefined.
func (r *Runtime) evalIsUndefinedLocked(code string) bool {
	r.executePendingJobs()
	result, err := r.vm.EvalValue(code, quickjs.EvalGlobal)
	if err != nil {
		return true
	}
	isUndef := result.IsUndefined()
	result.Free()
	return isUndef
}

// evalStringLocked evaluates a JS expression and returns its string value.
func (r *Runtime) evalStringLocked(code string) string {
	r.executePendingJobs()
	result, err := r.vm.Eval(code, quickjs.EvalGlobal)
	if err != nil {
		return ""
	}
	if s, ok := result.(string); ok {
		return s
	}
	return ""
}
