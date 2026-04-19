/*
 * ramune_shim.h - Host-side declarations documenting the WASM boundary
 *                 between Ramune's Go qjswasm backend and QuickJS-NG.
 *
 * This header is informational. The shim itself is implemented in
 * ramune_shim.c and compiled to WASI (wasm32-wasip1) together with
 * QuickJS-NG. Go code calls exported functions by name through wazero;
 * C-side callbacks reach Go via a single imported host function,
 * env.go_dispatch.
 *
 * Design notes
 * ------------
 * - Build with -DJS_NAN_BOXING=1 so JSValue is uint64_t. Values cross the
 *   wasm boundary as i64 without a handle table.
 * - All ptr/len pairs returned from the shim are packed into a single i64
 *   (hi32 = ptr, lo32 = len). The Go caller reads from wasm memory then
 *   frees the ptr via rmn_free.
 * - JSRuntime* and JSContext* pointers are wasm32 i32 values and pass
 *   across opaque; Go treats them as opaque tokens.
 * - Exception convention mirrors QuickJS: a JSValue-returning function
 *   returns a value whose tag is JS_TAG_EXCEPTION on failure. Go uses the
 *   val_is_exception check plus get_exception / exception_to_json to pull
 *   the message+stack.
 */
#ifndef RAMUNE_QJS_SHIM_H
#define RAMUNE_QJS_SHIM_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* --- Memory ---------------------------------------------------------- */
void *rmn_malloc(uint32_t size);
void rmn_free(void *ptr);

/* --- Runtime / context ----------------------------------------------- */
uint32_t rt_new(void);
void rt_free(uint32_t rt);
uint32_t ctx_new(uint32_t rt);
void ctx_free(uint32_t ctx);

/* --- Evaluation ------------------------------------------------------ */
/*
 * flags bit0: module mode
 * flags bit1: strip shebang
 * Returns JSValue (i64). If val_is_exception(r), call get_exception(ctx)
 * + exception_to_json(ctx, exc).
 */
uint64_t eval(uint32_t ctx, uint32_t code_ptr, uint32_t code_len,
              uint32_t fname_ptr, uint32_t fname_len, uint32_t flags);

/* --- Value lifecycle ------------------------------------------------- */
void     val_free(uint32_t ctx, uint64_t v);
uint64_t val_dup (uint32_t ctx, uint64_t v);

/* --- Value inspection (packed bitfield, see VAL_KIND_*) -------------- */
uint32_t val_kind(uint32_t ctx, uint64_t v);

#define VAL_KIND_UNDEFINED 0x001
#define VAL_KIND_NULL      0x002
#define VAL_KIND_BOOL      0x004
#define VAL_KIND_NUMBER    0x008
#define VAL_KIND_STRING    0x010
#define VAL_KIND_OBJECT    0x020
#define VAL_KIND_ARRAY     0x040
#define VAL_KIND_FUNCTION  0x080
#define VAL_KIND_PROMISE   0x100
#define VAL_KIND_EXCEPTION 0x200

/* --- Primitive extraction -------------------------------------------- */
int32_t val_to_bool   (uint32_t ctx, uint64_t v);
int64_t val_to_int64  (uint32_t ctx, uint64_t v);
double  val_to_float64(uint32_t ctx, uint64_t v);
uint64_t val_to_string(uint32_t ctx, uint64_t v); /* packed (ptr,len); caller frees ptr */

/* --- Primitive construction ----------------------------------------- */
uint64_t val_undefined  (uint32_t ctx);
uint64_t val_null       (uint32_t ctx);
uint64_t val_from_bool  (uint32_t ctx, int32_t b);
uint64_t val_from_int64 (uint32_t ctx, int64_t n);
uint64_t val_from_float64(uint32_t ctx, double n);
uint64_t val_from_string(uint32_t ctx, uint32_t ptr, uint32_t len);

/* --- JSON round-trip ------------------------------------------------- */
uint64_t val_to_json  (uint32_t ctx, uint64_t v); /* packed (ptr,len) */
uint64_t val_from_json(uint32_t ctx, uint32_t ptr, uint32_t len);

/* --- Object / property ops ------------------------------------------ */
uint64_t new_object     (uint32_t ctx);
uint64_t new_array      (uint32_t ctx);
uint64_t new_uint8array (uint32_t ctx, uint32_t ptr, uint32_t len);

uint64_t obj_get_prop   (uint32_t ctx, uint64_t obj,
                         uint32_t name_ptr, uint32_t name_len);
int32_t  obj_set_prop   (uint32_t ctx, uint64_t obj,
                         uint32_t name_ptr, uint32_t name_len, uint64_t val);
int32_t  obj_has_prop   (uint32_t ctx, uint64_t obj,
                         uint32_t name_ptr, uint32_t name_len);
int32_t  obj_delete_prop(uint32_t ctx, uint64_t obj,
                         uint32_t name_ptr, uint32_t name_len);

/* --- Function call --------------------------------------------------- */
/*
 * args_json is a JSON array. `this_val` may be 0 (interpreted as
 * undefined).
 */
uint64_t val_call(uint32_t ctx, uint64_t fn, uint64_t this_val,
                  uint32_t args_json_ptr, uint32_t args_json_len);

/* --- Go function registration --------------------------------------- */
/*
 * Registers globalThis[name] = <function>. When invoked from JS, the
 * function JSON-encodes its argv, calls imported env.go_dispatch(id,
 * args_ptr, args_len) and parses the packed (result_ptr, result_len) JSON.
 * On { e: "..." } the JS side throws; on { __native_ref: "__name" } the JS
 * side pulls globalThis[__name] as a live handle; otherwise the r field is
 * returned directly.
 */
int32_t register_go_func(uint32_t ctx, uint32_t name_ptr, uint32_t name_len,
                         uint32_t id);

/* --- Exceptions ------------------------------------------------------ */
uint64_t get_exception     (uint32_t ctx);
uint64_t exception_to_json (uint32_t ctx, uint64_t exc); /* packed (ptr,len) JSON {message,stack,name} */

/* --- Microtasks / promises ------------------------------------------ */
int32_t  execute_pending_jobs(uint32_t rt);

/* --- globalThis shortcuts ------------------------------------------- */
/* Used by JSFunc so callers can pull/drop globalThis[refName] without
 * going through the `eval` export. Re-entering wasm through `eval` from
 * inside a Go host callback corrupts the outer JSCFunction trampoline's
 * uint64 return under wazero's compiler mode; re-entry through these
 * dedicated exports stays safe. */
uint64_t global_get_prop   (uint32_t ctx, uint32_t name_ptr, uint32_t name_len);
int32_t  global_delete_prop(uint32_t ctx, uint32_t name_ptr, uint32_t name_len);

/* Imported host function — from Go, through env.go_dispatch.
 *
 * The `dummy_u64` parameter is a wazero-compat workaround: without a
 * uint64 parameter, re-entering wasm from inside a Go callback (via
 * rt.Eval, JSFunc.Call, etc.) corrupts the outer JSCFunctionData
 * trampoline's uint64 return under compiler mode. Adding one 8-byte
 * parameter appears to stabilize the ABI. fastschema/qjs's proxy uses
 * the same pattern (thisVal is their uint64 param). */
__attribute__((import_module("env"), import_name("go_dispatch")))
extern uint64_t go_dispatch(uint32_t id, uint64_t dummy_u64,
                            uint32_t args_ptr, uint32_t args_len);

__attribute__((import_module("env"), import_name("host_log")))
extern void host_log(uint32_t level, uint32_t ptr, uint32_t len);

__attribute__((import_module("env"), import_name("host_panic")))
extern void host_panic(uint32_t ptr, uint32_t len);

#ifdef __cplusplus
}
#endif

#endif /* RAMUNE_QJS_SHIM_H */
