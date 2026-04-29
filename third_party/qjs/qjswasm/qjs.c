#include "qjs.h"

JSContext *New_QJSContext(JSRuntime *rt)
{
  JSContext *ctx;
  ctx = JS_NewContext(rt);
  js_init_module_std(ctx, "qjs:std");
  js_init_module_os(ctx, "qjs:os");
  js_init_module_bjson(ctx, "qjs:bjson");
  js_set_global_objs(ctx);

  return ctx;
}

QJSRuntime *New_QJS(
    size_t memory_limit,
    size_t max_stack_size,
    size_t max_execution_time,
    size_t gc_threshold)
{
  JSRuntime *runtime = NULL;
  JSContext *ctx = NULL;
  TimeoutArgs *timeout_args = NULL;
  QJSRuntime *qjs = NULL;

#ifdef QJS_DEBUG_RUNTIME_ADDRESS
  randomize_address_space();
#endif

  runtime = JS_NewRuntime();
  if (!runtime)
    return NULL;

  if (memory_limit > 0)
    JS_SetMemoryLimit(runtime, memory_limit);

  if (gc_threshold > 0)
    JS_SetGCThreshold(runtime, gc_threshold);

  if (max_stack_size > 0)
    JS_SetMaxStackSize(runtime, max_stack_size);

  if (max_execution_time > 0)
  {
    timeout_args = (TimeoutArgs *)malloc(sizeof(TimeoutArgs));
    if (!timeout_args)
      goto fail;
    timeout_args->start = time(NULL);
    timeout_args->timeout = (time_t)max_execution_time;
    JS_SetInterruptHandler(runtime, QJS_TimeoutHandler, timeout_args);
  }

  /* setup the the worker context */
  js_std_set_worker_new_context_func(New_QJSContext);
  /* initialize the standard objects */
  js_std_init_handlers(runtime);
  /* loader for ES6 modules */
  JS_SetModuleLoaderFunc(runtime, NULL, QJS_ModuleLoader, NULL);
  /* exit on unhandled promise rejections */
  // JS_SetHostPromiseRejectionTracker(runtime, js_std_promise_rejection_tracker, NULL);

  ctx = New_QJSContext(runtime);
  if (!ctx)
    goto fail;

  // Initialize QJS_PROXY_VALUE class
  if (init_qjs_proxy_value_class(ctx) < 0)
    goto fail;

  qjs = (QJSRuntime *)malloc(sizeof(QJSRuntime));
  if (!qjs)
    goto fail;

  qjs->runtime = runtime;
  qjs->context = ctx;
  qjs->timeout_args = timeout_args;
  return qjs;

fail:
  if (timeout_args) free(timeout_args);
  if (ctx) JS_FreeContext(ctx);
  JS_FreeRuntime(runtime);
  return NULL;
}

void QJS_FreeValue(JSContext *ctx, JSValue val)
{
  JS_FreeValue(ctx, val);
}

void QJS_Free(QJSRuntime *qjs)
{
  /*
   * Disable the interrupt handler before tearing down the runtime so any
   * cleanup-time JS_RunGC / finalizer triggered call does not dereference
   * the timeout args we are about to free.
   */
  if (qjs->timeout_args)
  {
    JS_SetInterruptHandler(qjs->runtime, NULL, NULL);
  }
  JS_FreeContext(qjs->context);
  JS_FreeRuntime(qjs->runtime);
  if (qjs->timeout_args)
  {
    free(qjs->timeout_args);
  }
  free(qjs);
}

JSValue QJS_CloneValue(JSContext *ctx, JSValue val)
{
  return JS_DupValue(ctx, val);
}

JSContext *QJS_GetContext(QJSRuntime *qjs)
{
  return qjs->context;
}

void QJS_UpdateStackTop(QJSRuntime *qjs)
{
  JS_UpdateStackTop(qjs->runtime);
}

QJSRuntime *qjs = NULL;

QJSRuntime *QJS_GetRuntime()
{
  return qjs;
}

void initialize()
{
  if (qjs != NULL)
    return;
  size_t memory_limit = 0;
  size_t gc_threshold = 0;
  size_t max_stack_size = 0;
  qjs = New_QJS(memory_limit, max_stack_size, 0, gc_threshold);
}
