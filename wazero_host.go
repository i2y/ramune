//go:build qjswasm && !quickjs && !goja

package ramune

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// wasmExports caches the api.Function handles we pull from the wasm
// module at instantiate time. Calling api.Module.ExportedFunction on a
// hot path rebuilds the lookup each time; caching shaves that off.
type wasmExports struct {
	// Runtime / context
	rtNew   api.Function
	rtFree  api.Function
	ctxNew  api.Function
	ctxFree api.Function

	// Memory
	rmnMalloc api.Function
	rmnFree   api.Function

	// Eval / value lifecycle
	eval    api.Function
	valFree api.Function
	valDup  api.Function
	valKind api.Function

	// Primitive extraction
	valToBool    api.Function
	valToInt64   api.Function
	valToFloat64 api.Function
	valToString  api.Function

	// Primitive construction
	valUndefined   api.Function
	valNull        api.Function
	valFromBool    api.Function
	valFromInt64   api.Function
	valFromFloat64 api.Function
	valFromString  api.Function

	// JSON round-trip
	valToJSON   api.Function
	valFromJSON api.Function

	// Object / property ops
	newObject     api.Function
	newArray      api.Function
	newUint8Array api.Function
	objGetProp    api.Function
	objSetProp    api.Function
	objHasProp    api.Function
	objDeleteProp api.Function

	// Function call / Go registration
	valCall        api.Function
	registerGoFunc api.Function

	// Exceptions / microtasks
	getException       api.Function
	exceptionToJson    api.Function
	executePendingJobs api.Function

	// globalThis shortcuts (avoid eval re-entry from Go callbacks)
	globalGetProp    api.Function
	globalDeleteProp api.Function
}

func (e *wasmExports) resolve(mod api.Module) error {
	type lookup struct {
		name string
		dst  *api.Function
	}
	table := []lookup{
		{"rt_new", &e.rtNew},
		{"rt_free", &e.rtFree},
		{"ctx_new", &e.ctxNew},
		{"ctx_free", &e.ctxFree},
		{"rmn_malloc", &e.rmnMalloc},
		{"rmn_free", &e.rmnFree},
		{"eval", &e.eval},
		{"val_free", &e.valFree},
		{"val_dup", &e.valDup},
		{"val_kind", &e.valKind},
		{"val_to_bool", &e.valToBool},
		{"val_to_int64", &e.valToInt64},
		{"val_to_float64", &e.valToFloat64},
		{"val_to_string", &e.valToString},
		{"val_undefined", &e.valUndefined},
		{"val_null", &e.valNull},
		{"val_from_bool", &e.valFromBool},
		{"val_from_int64", &e.valFromInt64},
		{"val_from_float64", &e.valFromFloat64},
		{"val_from_string", &e.valFromString},
		{"val_to_json", &e.valToJSON},
		{"val_from_json", &e.valFromJSON},
		{"new_object", &e.newObject},
		{"new_array", &e.newArray},
		{"new_uint8array", &e.newUint8Array},
		{"obj_get_prop", &e.objGetProp},
		{"obj_set_prop", &e.objSetProp},
		{"obj_has_prop", &e.objHasProp},
		{"obj_delete_prop", &e.objDeleteProp},
		{"val_call", &e.valCall},
		{"register_go_func", &e.registerGoFunc},
		{"get_exception", &e.getException},
		{"exception_to_json", &e.exceptionToJson},
		{"execute_pending_jobs", &e.executePendingJobs},
		{"global_get_prop", &e.globalGetProp},
		{"global_delete_prop", &e.globalDeleteProp},
	}
	for _, l := range table {
		f := mod.ExportedFunction(l.name)
		if f == nil {
			return fmt.Errorf("wasm export %q missing (was quickjs.wasm rebuilt?)", l.name)
		}
		*l.dst = f
	}
	return nil
}

// installWazeroHost registers env.go_dispatch, env.host_log, env.host_panic.
// These are imported by the shim in ramune_shim.c.
func (r *Runtime) installWazeroHost(ctx context.Context) error {
	_, err := r.wzRt.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(r.hostGoDispatch).Export("go_dispatch").
		NewFunctionBuilder().WithFunc(r.hostLog).Export("host_log").
		NewFunctionBuilder().WithFunc(r.hostPanic).Export("host_panic").
		Instantiate(ctx)
	return err
}

// hostGoDispatch is the callback invoked from the wasm shim when a JS
// function registered via register_go_func is called. The shim has
// already packed argv into JSON. We invoke the matching Go handler,
// JSON-encode the result, malloc a buffer in wasm memory, write the
// JSON, and return packed (ptr, len).
//
// The dummyU64 parameter is an ABI workaround — without a 64-bit input
// parameter the wazero compiler mode corrupts the outer trampoline's
// uint64 return after this callback re-enters wasm.
func (r *Runtime) hostGoDispatch(ctx context.Context, mod api.Module,
	id uint32, dummyU64 uint64, argsPtr, argsLen uint32) uint64 {
	_ = dummyU64
	return r.dispatchGoFunc(ctx, mod, id, argsPtr, argsLen)
}

func (r *Runtime) hostLog(ctx context.Context, mod api.Module,
	level, ptr, length uint32) {
	buf, _ := mod.Memory().Read(ptr, length)
	switch level {
	case 0:
		fmt.Fprintf(r.stderr, "[qjswasm DEBUG] %s\n", buf)
	case 1:
		fmt.Fprintf(r.stderr, "[qjswasm INFO] %s\n", buf)
	case 2:
		fmt.Fprintf(r.stderr, "[qjswasm WARN] %s\n", buf)
	default:
		fmt.Fprintf(r.stderr, "[qjswasm ERROR] %s\n", buf)
	}
}

func (r *Runtime) hostPanic(ctx context.Context, mod api.Module, ptr, length uint32) {
	buf, _ := mod.Memory().Read(ptr, length)
	panic(fmt.Sprintf("qjswasm wasm-side panic: %s", buf))
}

// Small safety belt to keep wazero import tidy.
var _ = wazero.NewRuntimeConfigCompiler
