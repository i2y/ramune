/*
 * ramune_shim.c - Ramune qjswasm backend bridge for QuickJS-NG.
 *
 * Compiled to wasm32-wasip1 together with QuickJS-NG. Go code running in
 * wazero calls the exported functions listed in ramune_shim.h by name.
 * The shim imports env.go_dispatch from Go for Go-function callbacks.
 *
 * Build-time requirement: -DJS_NAN_BOXING=1 (JSValue fits in uint64_t).
 */

#include "ramune_shim.h"
#include "quickjs.h"
#include <stdlib.h>
#include <string.h>

/* ---- export helpers ----------------------------------------------- */

#define RMN_EXPORT(name) __attribute__((export_name(#name))) \
                         __attribute__((visibility("default")))

/* Pack (ptr, len) into a single 64-bit return value. High 32 bits are the
 * wasm linear memory offset, low 32 bits are the byte length. */
static inline uint64_t pack_ptr_len(uint32_t ptr, uint32_t len) {
    return ((uint64_t)ptr << 32) | (uint64_t)len;
}

#define PACKED_ERR 0ULL

/* ---- memory ------------------------------------------------------- */

RMN_EXPORT(rmn_malloc)
void *rmn_malloc(uint32_t size) {
    return malloc((size_t)size);
}

RMN_EXPORT(rmn_free)
void rmn_free(void *ptr) {
    free(ptr);
}

/* ---- runtime / context -------------------------------------------- */

RMN_EXPORT(rt_new)
uint32_t rt_new(void) {
    JSRuntime *rt = JS_NewRuntime();
    return (uint32_t)(uintptr_t)rt;
}

RMN_EXPORT(rt_free)
void rt_free(uint32_t rt) {
    if (rt) JS_FreeRuntime((JSRuntime *)(uintptr_t)rt);
}

RMN_EXPORT(ctx_new)
uint32_t ctx_new(uint32_t rt) {
    if (!rt) return 0;
    JSContext *ctx = JS_NewContext((JSRuntime *)(uintptr_t)rt);
    return (uint32_t)(uintptr_t)ctx;
}

RMN_EXPORT(ctx_free)
void ctx_free(uint32_t ctx) {
    if (ctx) JS_FreeContext((JSContext *)(uintptr_t)ctx);
}

/* ---- evaluation --------------------------------------------------- */

RMN_EXPORT(eval)
uint64_t eval(uint32_t ctx_u, uint32_t code_ptr, uint32_t code_len,
              uint32_t fname_ptr, uint32_t fname_len, uint32_t flags) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx || !code_ptr) return JS_EXCEPTION;

    const char *code = (const char *)(uintptr_t)code_ptr;
    char namebuf[256];
    const char *fname = "<input>";
    if (fname_ptr && fname_len) {
        uint32_t n = fname_len < sizeof(namebuf) - 1 ? fname_len : sizeof(namebuf) - 1;
        memcpy(namebuf, (const char *)(uintptr_t)fname_ptr, n);
        namebuf[n] = '\0';
        fname = namebuf;
    }

    int eval_flags = JS_EVAL_TYPE_GLOBAL;
    if (flags & 1u) eval_flags = JS_EVAL_TYPE_MODULE;
    /* flags bit1 was reserved for strip-shebang in the shim API but
     * QuickJS-NG does not expose it as a JS_Eval flag; callers strip on
     * the Go side before passing source in. */
    return JS_Eval(ctx, code, (size_t)code_len, fname, eval_flags);
}

/* ---- value lifecycle ---------------------------------------------- */

RMN_EXPORT(val_free)
void val_free(uint32_t ctx_u, uint64_t v) {
    if (!ctx_u) return;
    JS_FreeValue((JSContext *)(uintptr_t)ctx_u, v);
}

RMN_EXPORT(val_dup)
uint64_t val_dup(uint32_t ctx_u, uint64_t v) {
    if (!ctx_u) return JS_UNDEFINED;
    return JS_DupValue((JSContext *)(uintptr_t)ctx_u, v);
}

/* ---- value kind bitfield ------------------------------------------ */

RMN_EXPORT(val_kind)
uint32_t val_kind(uint32_t ctx_u, uint64_t v) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    uint32_t k = 0;
    if (JS_IsException(v))    return VAL_KIND_EXCEPTION;
    if (JS_IsUndefined(v))    k |= VAL_KIND_UNDEFINED;
    if (JS_IsNull(v))         k |= VAL_KIND_NULL;
    if (JS_IsBool(v))         k |= VAL_KIND_BOOL;
    if (JS_IsNumber(v))       k |= VAL_KIND_NUMBER;
    if (JS_IsString(v))       k |= VAL_KIND_STRING;
    if (JS_IsArray(v))        k |= VAL_KIND_ARRAY | VAL_KIND_OBJECT;
    else if (JS_IsObject(v))  k |= VAL_KIND_OBJECT;
    if (ctx && JS_IsFunction(ctx, v)) k |= VAL_KIND_FUNCTION;
    if (JS_IsPromise(v))      k |= VAL_KIND_PROMISE;
    return k;
}

/* ---- primitive extraction ----------------------------------------- */

RMN_EXPORT(val_to_bool)
int32_t val_to_bool(uint32_t ctx_u, uint64_t v) {
    if (!ctx_u) return 0;
    int r = JS_ToBool((JSContext *)(uintptr_t)ctx_u, v);
    return r < 0 ? 0 : r;
}

RMN_EXPORT(val_to_int64)
int64_t val_to_int64(uint32_t ctx_u, uint64_t v) {
    if (!ctx_u) return 0;
    int64_t out = 0;
    JS_ToInt64((JSContext *)(uintptr_t)ctx_u, &out, v);
    return out;
}

RMN_EXPORT(val_to_float64)
double val_to_float64(uint32_t ctx_u, uint64_t v) {
    if (!ctx_u) return 0.0;
    double out = 0.0;
    JS_ToFloat64((JSContext *)(uintptr_t)ctx_u, &out, v);
    return out;
}

RMN_EXPORT(val_to_string)
uint64_t val_to_string(uint32_t ctx_u, uint64_t v) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return PACKED_ERR;
    size_t len = 0;
    const char *s = JS_ToCStringLen(ctx, &len, v);
    if (!s) return PACKED_ERR;
    /* Copy into a freshly-malloc'd buffer owned by the Go caller.
     * QuickJS owns the original cstring; we must release it after copy. */
    char *buf = (char *)malloc(len + 1);
    if (!buf) {
        JS_FreeCString(ctx, s);
        return PACKED_ERR;
    }
    memcpy(buf, s, len);
    buf[len] = '\0';
    JS_FreeCString(ctx, s);
    return pack_ptr_len((uint32_t)(uintptr_t)buf, (uint32_t)len);
}

/* ---- primitive construction --------------------------------------- */

RMN_EXPORT(val_undefined)
uint64_t val_undefined(uint32_t ctx_u) { (void)ctx_u; return JS_UNDEFINED; }

RMN_EXPORT(val_null)
uint64_t val_null(uint32_t ctx_u) { (void)ctx_u; return JS_NULL; }

RMN_EXPORT(val_from_bool)
uint64_t val_from_bool(uint32_t ctx_u, int32_t b) {
    (void)ctx_u;
    return b ? JS_TRUE : JS_FALSE;
}

RMN_EXPORT(val_from_int64)
uint64_t val_from_int64(uint32_t ctx_u, int64_t n) {
    if (!ctx_u) return JS_UNDEFINED;
    return JS_NewInt64((JSContext *)(uintptr_t)ctx_u, n);
}

RMN_EXPORT(val_from_float64)
uint64_t val_from_float64(uint32_t ctx_u, double n) {
    if (!ctx_u) return JS_UNDEFINED;
    return JS_NewFloat64((JSContext *)(uintptr_t)ctx_u, n);
}

RMN_EXPORT(val_from_string)
uint64_t val_from_string(uint32_t ctx_u, uint32_t ptr, uint32_t len) {
    if (!ctx_u) return JS_UNDEFINED;
    return JS_NewStringLen((JSContext *)(uintptr_t)ctx_u,
                           (const char *)(uintptr_t)ptr, (size_t)len);
}

/* ---- JSON round-trip ---------------------------------------------- */

RMN_EXPORT(val_to_json)
uint64_t val_to_json(uint32_t ctx_u, uint64_t v) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return PACKED_ERR;
    JSValue s = JS_JSONStringify(ctx, v, JS_UNDEFINED, JS_UNDEFINED);
    if (JS_IsException(s)) return PACKED_ERR;
    size_t len = 0;
    const char *cs = JS_ToCStringLen(ctx, &len, s);
    if (!cs) {
        JS_FreeValue(ctx, s);
        return PACKED_ERR;
    }
    char *buf = (char *)malloc(len + 1);
    if (!buf) {
        JS_FreeCString(ctx, cs);
        JS_FreeValue(ctx, s);
        return PACKED_ERR;
    }
    memcpy(buf, cs, len);
    buf[len] = '\0';
    JS_FreeCString(ctx, cs);
    JS_FreeValue(ctx, s);
    return pack_ptr_len((uint32_t)(uintptr_t)buf, (uint32_t)len);
}

RMN_EXPORT(val_from_json)
uint64_t val_from_json(uint32_t ctx_u, uint32_t ptr, uint32_t len) {
    if (!ctx_u) return JS_EXCEPTION;
    return JS_ParseJSON((JSContext *)(uintptr_t)ctx_u,
                        (const char *)(uintptr_t)ptr, (size_t)len,
                        "<json>");
}

/* ---- object / property -------------------------------------------- */

RMN_EXPORT(new_object)
uint64_t new_object(uint32_t ctx_u) {
    if (!ctx_u) return JS_EXCEPTION;
    return JS_NewObject((JSContext *)(uintptr_t)ctx_u);
}

RMN_EXPORT(new_array)
uint64_t new_array(uint32_t ctx_u) {
    if (!ctx_u) return JS_EXCEPTION;
    return JS_NewArray((JSContext *)(uintptr_t)ctx_u);
}

RMN_EXPORT(new_uint8array)
uint64_t new_uint8array(uint32_t ctx_u, uint32_t ptr, uint32_t len) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return JS_EXCEPTION;

    JSValue buffer = JS_NewArrayBufferCopy(ctx,
                                           (const uint8_t *)(uintptr_t)ptr,
                                           (size_t)len);
    if (JS_IsException(buffer)) return buffer;

    /* Construct `new Uint8Array(buffer)`. We get the constructor via
     * globalThis.Uint8Array and call it with the buffer as its single arg. */
    JSValue global = JS_GetGlobalObject(ctx);
    JSValue ctor = JS_GetPropertyStr(ctx, global, "Uint8Array");
    JS_FreeValue(ctx, global);
    if (JS_IsException(ctor)) {
        JS_FreeValue(ctx, buffer);
        return ctor;
    }
    JSValue argv[1] = { buffer };
    JSValue ret = JS_CallConstructor(ctx, ctor, 1, (JSValueConst *)argv);
    JS_FreeValue(ctx, ctor);
    JS_FreeValue(ctx, buffer);
    return ret;
}

/* Helper: build a NUL-terminated name on the stack. 512 bytes covers
 * every JS property name we care about; longer names fall through to
 * JS_NewAtomLen which takes a length. */
static JSAtom make_atom(JSContext *ctx, uint32_t ptr, uint32_t len) {
    return JS_NewAtomLen(ctx, (const char *)(uintptr_t)ptr, (size_t)len);
}

RMN_EXPORT(obj_get_prop)
uint64_t obj_get_prop(uint32_t ctx_u, uint64_t obj,
                      uint32_t name_ptr, uint32_t name_len) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return JS_EXCEPTION;
    JSAtom a = make_atom(ctx, name_ptr, name_len);
    if (a == JS_ATOM_NULL) return JS_EXCEPTION;
    JSValue r = JS_GetProperty(ctx, obj, a);
    JS_FreeAtom(ctx, a);
    return r;
}

RMN_EXPORT(obj_set_prop)
int32_t obj_set_prop(uint32_t ctx_u, uint64_t obj,
                     uint32_t name_ptr, uint32_t name_len, uint64_t val) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return -1;
    JSAtom a = make_atom(ctx, name_ptr, name_len);
    if (a == JS_ATOM_NULL) return -1;
    /* JS_SetProperty takes ownership of `val` on success. Dup to keep it. */
    JSValue held = JS_DupValue(ctx, val);
    int r = JS_SetProperty(ctx, obj, a, held);
    JS_FreeAtom(ctx, a);
    return r < 0 ? -1 : 0;
}

RMN_EXPORT(obj_has_prop)
int32_t obj_has_prop(uint32_t ctx_u, uint64_t obj,
                     uint32_t name_ptr, uint32_t name_len) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return 0;
    JSAtom a = make_atom(ctx, name_ptr, name_len);
    if (a == JS_ATOM_NULL) return 0;
    int r = JS_HasProperty(ctx, obj, a);
    JS_FreeAtom(ctx, a);
    return r < 0 ? 0 : (r ? 1 : 0);
}

RMN_EXPORT(obj_delete_prop)
int32_t obj_delete_prop(uint32_t ctx_u, uint64_t obj,
                        uint32_t name_ptr, uint32_t name_len) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return -1;
    JSAtom a = make_atom(ctx, name_ptr, name_len);
    if (a == JS_ATOM_NULL) return -1;
    int r = JS_DeleteProperty(ctx, obj, a, 0);
    JS_FreeAtom(ctx, a);
    return r < 0 ? -1 : 0;
}

/* ---- function call ------------------------------------------------ */

RMN_EXPORT(val_call)
uint64_t val_call(uint32_t ctx_u, uint64_t fn, uint64_t this_val,
                  uint32_t args_json_ptr, uint32_t args_json_len) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return JS_EXCEPTION;

    /* Parse args JSON (must be an Array). */
    JSValue args_val = JS_ParseJSON(ctx,
                                    (const char *)(uintptr_t)args_json_ptr,
                                    (size_t)args_json_len,
                                    "<args>");
    if (JS_IsException(args_val)) return args_val;

    int argc = 0;
    JSValueConst *argv = NULL;
    JSValue length_v = JS_UNDEFINED;

    if (!JS_IsArray(args_val)) {
        JS_FreeValue(ctx, args_val);
        return JS_ThrowTypeError(ctx, "val_call args must be a JSON array");
    }
    length_v = JS_GetPropertyStr(ctx, args_val, "length");
    int32_t len32 = 0;
    JS_ToInt32(ctx, &len32, length_v);
    JS_FreeValue(ctx, length_v);
    argc = len32 < 0 ? 0 : len32;

    JSValue *argvs = NULL;
    if (argc > 0) {
        argvs = (JSValue *)malloc(sizeof(JSValue) * (size_t)argc);
        if (!argvs) {
            JS_FreeValue(ctx, args_val);
            return JS_ThrowOutOfMemory(ctx);
        }
        for (int i = 0; i < argc; i++) {
            argvs[i] = JS_GetPropertyUint32(ctx, args_val, (uint32_t)i);
        }
        argv = (JSValueConst *)argvs;
    }

    JSValue this_v = this_val;
    /* A zero-tag value means "no this"; fall back to undefined. */
    if (this_val == JS_UNINITIALIZED || this_val == 0) {
        this_v = JS_UNDEFINED;
    }

    JSValue result = JS_Call(ctx, fn, this_v, argc, argv);

    if (argvs) {
        for (int i = 0; i < argc; i++) JS_FreeValue(ctx, argvs[i]);
        free(argvs);
    }
    JS_FreeValue(ctx, args_val);
    return result;
}

/* ---- Go-function registration ------------------------------------ */

typedef struct {
    uint32_t id;
} GoFuncData;

/*
 * JSCFunctionData trampoline. We stash the Go function id as an int32 in
 * `magic`. On invocation, JSON-encode the argv, call env.go_dispatch, parse
 * the result, and return it (or throw on { e: ... }).
 *
 * Function arguments: JSON.stringify drops function values (they serialize
 * to undefined and get omitted from the output). To let Go callbacks hold
 * references to JS callbacks, we pre-transform each function arg into a
 * marker object { "__jsfunc_ref": "<name>" } and stash the original under
 * globalThis[<name>]. The Go dispatcher decodes the marker into *JSFunc
 * which later reads globalThis[<name>] to invoke the JS function.
 */
static JSValue go_func_trampoline(JSContext *ctx, JSValueConst this_val,
                                  int argc, JSValueConst *argv, int magic,
                                  JSValue *func_data) {
    (void)this_val;
    (void)func_data;

    /* Build argv as a JS Array so we can stringify it. Function args are
     * replaced with marker objects (see note above). */
    static uint32_t fn_ref_seq = 0;
    JSValue global = JS_UNDEFINED;

    JSValue arr = JS_NewArray(ctx);
    if (JS_IsException(arr)) return arr;
    for (int i = 0; i < argc; i++) {
        if (JS_IsFunction(ctx, argv[i])) {
            if (JS_IsUndefined(global)) {
                global = JS_GetGlobalObject(ctx);
                if (JS_IsException(global)) {
                    JS_FreeValue(ctx, arr);
                    return global;
                }
            }
            char ref_name[48];
            fn_ref_seq++;
            int n = snprintf(ref_name, sizeof(ref_name),
                             "__jsfunc_wasm_%u", fn_ref_seq);
            if (n <= 0 || n >= (int)sizeof(ref_name)) {
                JS_FreeValue(ctx, global);
                JS_FreeValue(ctx, arr);
                return JS_ThrowInternalError(ctx, "jsfunc ref name overflow");
            }
            if (JS_SetPropertyStr(ctx, global, ref_name,
                                  JS_DupValue(ctx, argv[i])) < 0) {
                JS_FreeValue(ctx, global);
                JS_FreeValue(ctx, arr);
                return JS_EXCEPTION;
            }
            JSValue marker = JS_NewObject(ctx);
            if (JS_IsException(marker)) {
                JS_FreeValue(ctx, global);
                JS_FreeValue(ctx, arr);
                return marker;
            }
            if (JS_SetPropertyStr(ctx, marker, "__jsfunc_ref",
                                  JS_NewString(ctx, ref_name)) < 0) {
                JS_FreeValue(ctx, marker);
                JS_FreeValue(ctx, global);
                JS_FreeValue(ctx, arr);
                return JS_EXCEPTION;
            }
            if (JS_SetPropertyUint32(ctx, arr, (uint32_t)i, marker) < 0) {
                JS_FreeValue(ctx, global);
                JS_FreeValue(ctx, arr);
                return JS_EXCEPTION;
            }
        } else {
            JSValue dup = JS_DupValue(ctx, argv[i]);
            if (JS_SetPropertyUint32(ctx, arr, (uint32_t)i, dup) < 0) {
                if (!JS_IsUndefined(global)) JS_FreeValue(ctx, global);
                JS_FreeValue(ctx, arr);
                return JS_EXCEPTION;
            }
        }
    }
    if (!JS_IsUndefined(global)) JS_FreeValue(ctx, global);

    JSValue json = JS_JSONStringify(ctx, arr, JS_UNDEFINED, JS_UNDEFINED);
    JS_FreeValue(ctx, arr);
    if (JS_IsException(json)) return json;
    size_t jlen = 0;
    const char *jstr = JS_ToCStringLen(ctx, &jlen, json);
    JS_FreeValue(ctx, json);
    if (!jstr) return JS_EXCEPTION;

    /* jstr points into QuickJS-owned memory inside this wasm module's
     * linear memory. Since Go's wazero host function receives that
     * pointer in the same address space, it can read it without a copy.
     * The contract: Go must finish reading before we call JS_FreeCString.
     * env.go_dispatch is synchronous so the order below is sufficient. */
    uint64_t packed = go_dispatch((uint32_t)magic,
                                  (uint32_t)(uintptr_t)jstr,
                                  (uint32_t)jlen);
    JS_FreeCString(ctx, jstr);

    uint32_t rptr = (uint32_t)(packed >> 32);
    uint32_t rlen = (uint32_t)(packed & 0xffffffffu);
    if (rptr == 0 || rlen == 0) {
        return JS_ThrowInternalError(ctx, "go_dispatch returned empty result");
    }
    JSValue parsed = JS_ParseJSON(ctx, (const char *)(uintptr_t)rptr,
                                  (size_t)rlen, "<go_dispatch>");
    if (JS_IsException(parsed)) {
        free((void *)(uintptr_t)rptr);
        return parsed;
    }

    /* Defer freeing rptr until after we've finished using `parsed`. QuickJS
     * JSON parsing can leave internal aliases into the source buffer for
     * some value types; freeing early has been observed to corrupt the
     * returned JSValue on wasm. */
    JSValue result;

    /* Protocol: { "e": ... } -> throw ; { "__native_ref": "name" } -> pull
     * globalThis[name] ; { "r": value } -> return value directly. */
    JSValue e = JS_GetPropertyStr(ctx, parsed, "e");
    if (!JS_IsUndefined(e)) {
        JSValue msg = JS_ToString(ctx, e);
        JS_FreeValue(ctx, e);
        size_t mlen = 0;
        const char *m = JS_ToCStringLen(ctx, &mlen, msg);
        result = m ? JS_ThrowInternalError(ctx, "%s", m)
                   : JS_ThrowInternalError(ctx, "go dispatch error");
        if (m) JS_FreeCString(ctx, m);
        JS_FreeValue(ctx, msg);
        JS_FreeValue(ctx, parsed);
        free((void *)(uintptr_t)rptr);
        return result;
    }
    JS_FreeValue(ctx, e);

    JSValue nref = JS_GetPropertyStr(ctx, parsed, "__native_ref");
    if (JS_IsString(nref)) {
        JSValue global = JS_GetGlobalObject(ctx);
        size_t nlen = 0;
        const char *nname = JS_ToCStringLen(ctx, &nlen, nref);
        result = nname ? JS_GetPropertyStr(ctx, global, nname)
                       : JS_UNDEFINED;
        if (nname) {
            JSAtom a = JS_NewAtomLen(ctx, nname, nlen);
            JS_DeleteProperty(ctx, global, a, 0);
            JS_FreeAtom(ctx, a);
            JS_FreeCString(ctx, nname);
        }
        JS_FreeValue(ctx, global);
        JS_FreeValue(ctx, nref);
        JS_FreeValue(ctx, parsed);
        free((void *)(uintptr_t)rptr);
        return result;
    }
    JS_FreeValue(ctx, nref);

    result = JS_GetPropertyStr(ctx, parsed, "r");
    JS_FreeValue(ctx, parsed);
    free((void *)(uintptr_t)rptr);
    return result;
}

RMN_EXPORT(register_go_func)
int32_t register_go_func(uint32_t ctx_u, uint32_t name_ptr, uint32_t name_len,
                         uint32_t id) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return -1;
    JSValue fn = JS_NewCFunctionData(ctx, go_func_trampoline,
                                     /*length*/ 0, /*magic*/ (int)id,
                                     /*data_len*/ 0, NULL);
    if (JS_IsException(fn)) return -1;
    JSAtom a = JS_NewAtomLen(ctx, (const char *)(uintptr_t)name_ptr,
                             (size_t)name_len);
    if (a == JS_ATOM_NULL) {
        JS_FreeValue(ctx, fn);
        return -1;
    }
    JSValue global = JS_GetGlobalObject(ctx);
    int rc = JS_SetProperty(ctx, global, a, fn);
    JS_FreeAtom(ctx, a);
    JS_FreeValue(ctx, global);
    return rc < 0 ? -1 : 0;
}

/* ---- exceptions --------------------------------------------------- */

RMN_EXPORT(get_exception)
uint64_t get_exception(uint32_t ctx_u) {
    if (!ctx_u) return JS_NULL;
    return JS_GetException((JSContext *)(uintptr_t)ctx_u);
}

RMN_EXPORT(exception_to_json)
uint64_t exception_to_json(uint32_t ctx_u, uint64_t exc) {
    JSContext *ctx = (JSContext *)(uintptr_t)ctx_u;
    if (!ctx) return PACKED_ERR;

    /* Construct an object carrying message, stack, name. Stringify. */
    JSValue info = JS_NewObject(ctx);
    if (JS_IsException(info)) return PACKED_ERR;

    JSValue msg_v = JS_ToString(ctx, exc);
    if (!JS_IsException(msg_v)) {
        JS_SetPropertyStr(ctx, info, "message", msg_v);
    } else {
        JS_FreeValue(ctx, msg_v);
    }

    JSValue name_v = JS_GetPropertyStr(ctx, exc, "name");
    if (!JS_IsUndefined(name_v) && !JS_IsException(name_v)) {
        JS_SetPropertyStr(ctx, info, "name", name_v);
    } else {
        JS_FreeValue(ctx, name_v);
    }

    JSValue stack_v = JS_GetPropertyStr(ctx, exc, "stack");
    if (!JS_IsUndefined(stack_v) && !JS_IsException(stack_v)) {
        JS_SetPropertyStr(ctx, info, "stack", stack_v);
    } else {
        JS_FreeValue(ctx, stack_v);
    }

    JSValue json = JS_JSONStringify(ctx, info, JS_UNDEFINED, JS_UNDEFINED);
    JS_FreeValue(ctx, info);
    if (JS_IsException(json)) return PACKED_ERR;

    size_t len = 0;
    const char *s = JS_ToCStringLen(ctx, &len, json);
    if (!s) {
        JS_FreeValue(ctx, json);
        return PACKED_ERR;
    }
    char *buf = (char *)malloc(len + 1);
    if (!buf) {
        JS_FreeCString(ctx, s);
        JS_FreeValue(ctx, json);
        return PACKED_ERR;
    }
    memcpy(buf, s, len);
    buf[len] = '\0';
    JS_FreeCString(ctx, s);
    JS_FreeValue(ctx, json);
    return pack_ptr_len((uint32_t)(uintptr_t)buf, (uint32_t)len);
}

/* ---- microtasks --------------------------------------------------- */

RMN_EXPORT(execute_pending_jobs)
int32_t execute_pending_jobs(uint32_t rt_u) {
    if (!rt_u) return 0;
    int any = 0;
    JSContext *job_ctx = NULL;
    for (;;) {
        int r = JS_ExecutePendingJob((JSRuntime *)(uintptr_t)rt_u, &job_ctx);
        if (r == 0) break;
        if (r < 0) return -1;
        any = 1;
    }
    return any;
}
