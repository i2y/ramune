//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// RegisterFunc exposes a Go function to JS as globalThis[name]. The JSON
// contract on the Go↔wasm boundary matches the QuickJS (modernc) backend
// (`{e: msg}` / `{r: value}` / `{__native_ref: name}`), so downstream JS
// polyfills that were written for the existing __goDispatch protocol work
// unchanged. The trampoline that JSON-encodes argv lives in C
// (go_func_trampoline in ramune_shim.c) rather than in a Go-emitted JS
// wrapper.
func (r *Runtime) RegisterFunc(name string, fn GoFunc) error {
	if r.closed.Load() {
		return ErrAlreadyClosed
	}
	var err error
	r.dispatch(func() {
		err = r.registerFuncLocked(name, fn)
	})
	return err
}

func (r *Runtime) registerFuncLocked(name string, fn GoFunc) error {
	id := uint32(len(r.goFuncs))
	r.goFuncs = append(r.goFuncs, fn)

	// Register the trampoline via the shim; the wasm side will pack argv
	// as JSON and invoke env.go_dispatch(id, ...) with it.
	namePtr, nameLen, err := r.writeStringLocked(name)
	if err != nil {
		return err
	}
	defer r.wasmFreeLocked(namePtr)
	res, err := r.wzExp.registerGoFunc.Call(r.wzCtx,
		uint64(r.qjsCtx),
		uint64(namePtr), uint64(nameLen),
		uint64(id))
	if err != nil {
		return err
	}
	if int32(res[0]) < 0 {
		return r.pullExceptionLocked()
	}
	return nil
}

// RegisterFuncWithContext registers a GoFunc that receives a
// CallbackContext as its first arg. Mirrors engine_goja.go's equivalent.
func (r *Runtime) RegisterFuncWithContext(name string, fn GoFuncWithContext) error {
	cc := &CallbackContext{rt: r}
	return r.RegisterFunc(name, func(args []any) (any, error) {
		return fn(cc, args)
	})
}

// dispatchGoFunc is the Go side of env.go_dispatch. The wasm trampoline
// already packed argv into a JSON array and malloc'd it in wasm memory.
// We parse, invoke the matching Go handler, JSON-encode the response, and
// return (ptr, len) packed into one i64 — the trampoline parses that
// result and either throws (on `e`) or returns the `r` value.
func (r *Runtime) dispatchGoFunc(ctx context.Context, mod api.Module,
	id, argsPtr, argsLen uint32) uint64 {
	if id >= uint32(len(r.goFuncs)) {
		return r.packErrorResult(fmt.Sprintf("invalid function id %d", id))
	}
	fn := r.goFuncs[id]

	argsBytes, ok := mod.Memory().Read(argsPtr, argsLen)
	if !ok {
		return r.packErrorResult("args memory read failed")
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(argsBytes, &raw); err != nil {
		return r.packErrorResult("argv parse: " + err.Error())
	}

	goArgs := make([]any, len(raw))
	for i, msg := range raw {
		var v any
		if err := json.Unmarshal(msg, &v); err != nil {
			return r.packErrorResult(fmt.Sprintf("arg %d: %s", i, err))
		}
		// The C trampoline replaces JS-function args with a marker object
		// { "__jsfunc_ref": "<name>" } and stashes the original under
		// globalThis[<name>]. Decode the marker back into *JSFunc here so
		// Go handlers can keep calling JS callbacks.
		if m, isMap := v.(map[string]any); isMap {
			if ref, rok := m["__jsfunc_ref"].(string); rok && len(m) == 1 {
				goArgs[i] = &JSFunc{rt: r, refName: ref}
				continue
			}
		}
		goArgs[i] = v
	}

	result, err := fn(goArgs)
	if err != nil {
		return r.packErrorResult(err.Error())
	}

	payload := map[string]any{"r": result}
	buf, merr := json.Marshal(payload)
	if merr != nil {
		return r.packErrorResult("result marshal: " + merr.Error())
	}
	return r.packJSONResult(buf)
}

// packErrorResult encodes { "e": msg } into wasm memory and returns the
// packed (ptr, len) for env.go_dispatch return value.
func (r *Runtime) packErrorResult(msg string) uint64 {
	b, _ := json.Marshal(map[string]any{"e": msg})
	return r.packJSONResult(b)
}

// packJSONResult allocates space in wasm memory via rmn_malloc, writes
// JSON bytes, NUL-terminates (same reasoning as writeStringLocked —
// QuickJS-NG's JSON parser peeks past the end in some tokenizer paths
// and uninitialized bytes trip UTF-8 validation). Returns packed
// (ptr, len). The wasm shim frees the buffer after parsing.
func (r *Runtime) packJSONResult(b []byte) uint64 {
	if len(b) == 0 {
		b = []byte("null")
	}
	ptr, err := r.wasmMallocLocked(uint32(len(b)) + 1)
	if err != nil {
		return 0
	}
	if !r.wzMem.Write(ptr, b) {
		r.wasmFreeLocked(ptr)
		return 0
	}
	if !r.wzMem.WriteByte(ptr+uint32(len(b)), 0) {
		r.wasmFreeLocked(ptr)
		return 0
	}
	return (uint64(ptr) << 32) | uint64(uint32(len(b)))
}
