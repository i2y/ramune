#include "qjs.h"

#ifdef __wasm__
// When compiling for WASM, declare the imported host function.
// The function is imported from the "env" module under the name "jsFunctionProxy".
__attribute__((import_module("env"), import_name("jsFunctionProxy"))) extern JSValue jsFunctionProxy(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv);
#endif

JSValue InvokeFunctionProxy(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
#ifdef __wasm__
  // In WASM, call the imported host function.
  // Validate that we have the expected number of arguments
  if (argc < 4)
  {
    return JS_ThrowInternalError(ctx, "InvokeFunctionProxy: insufficient arguments");
  }
  return jsFunctionProxy(ctx, this_val, argc, argv);
#else
  // For native builds, throw an internal error.
  return JS_ThrowInternalError(ctx, "proxy not implemented");
#endif
}

JSValue QJS_CreateFunctionProxy(JSContext *ctx, uint64_t func_id, uint64_t ctx_id, uint64_t is_async)
{
  // Create the C function binding that will be our proxy handler
  JSValue proxy = JS_NewCFunction(
      ctx,
      InvokeFunctionProxy,
      "proxyHandler", // for debugging purposes
      0);

  if (JS_IsException(proxy))
    return proxy;

  // Verify that the proxy is actually a function
  if (!JS_IsFunction(ctx, proxy))
  {
    JS_FreeValue(ctx, proxy);
    return JS_ThrowInternalError(ctx, "failed to create proxy function");
  }

  // This JavaScript creates a function that captures our proxy handler and context/handler IDs
  // When called, it invokes the proxy handler with the original 'this' context and arguments
  const char *proxy_content = "";

  if (is_async == 0)
  {
    proxy_content = "(proxy, fnHandler, ctx, is_async) => function QJS_FunctionProxy (...args) { "
                    "  if (typeof proxy !== 'function') throw new TypeError('proxy is not a function'); "
                    "  return proxy.call(this, fnHandler, ctx, is_async, undefined, ...args); "
                    "}";
  }
  else
  {
    proxy_content = "(proxy, fnHandler, ctx, is_async) => async function QJS_AsyncFunctionProxy (...args) {"
                    "	if (typeof proxy !== 'function') throw new TypeError('proxy is not a function'); "
                    "	let resolve, reject;"
                    "	const promise = new Promise((resolve_, reject_) => {"
                    "		resolve = resolve_;"
                    "		reject = reject_;"
                    "	});"
                    "	promise.resolve = resolve;"
                    "	promise.reject = reject;"
                    ""
                    "	proxy.call(this, fnHandler, ctx, is_async, promise,  ...args);"
                    "	return await promise;"
                    "}";
  }

  JSValue proxy_func = JS_Eval(
      ctx,
      proxy_content,
      strlen(proxy_content),
      "<proxy_factory>",
      JS_EVAL_TYPE_GLOBAL);
  if (JS_IsException(proxy_func))
  {
    JS_FreeValue(ctx, proxy);
    return proxy_func;
  }

  // Create arguments array for the call
  JSValue arg1 = JS_NewInt64(ctx, func_id);
  JSValue arg2 = JS_NewInt64(ctx, ctx_id);
  JSValue arg3 = JS_NewInt64(ctx, is_async);
  JSValueConst args[4] = {proxy, arg1, arg2, arg3};

  // Use JS_UNDEFINED for the 'this' context
  JSValue result = JS_Call(ctx, proxy_func, JS_UNDEFINED, 4, args);

  // Free all created values
  JS_FreeValue(ctx, arg1);
  JS_FreeValue(ctx, arg2);
  JS_FreeValue(ctx, arg3);
  JS_FreeValue(ctx, proxy_func);
  JS_FreeValue(ctx, proxy);

  return result;
}
