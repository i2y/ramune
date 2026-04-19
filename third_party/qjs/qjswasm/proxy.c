#include "qjs.h"

// toString method for QJS_PROXY_VALUE class
static JSValue qjs_proxy_value_toString(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
  JSValue proxy_id = JS_GetPropertyStr(ctx, this_val, "proxyId");
  if (JS_IsException(proxy_id))
    return proxy_id;

  const char *proxy_id_str = JS_ToCString(ctx, proxy_id);
  JS_FreeValue(ctx, proxy_id);

  if (!proxy_id_str)
    return JS_EXCEPTION;

  char buffer[256];
  snprintf(buffer, sizeof(buffer), "[object QJS_PROXY_VALUE(proxyId: %s)]", proxy_id_str);
  JS_FreeCString(ctx, proxy_id_str);

  return JS_NewString(ctx, buffer);
}

// Constructor function for QJS_PROXY_VALUE class
static JSValue qjs_proxy_value_constructor(JSContext *ctx, JSValueConst new_target, int argc, JSValueConst *argv)
{
  JSValue obj;
  JSValue proto;

  if (JS_IsUndefined(new_target))
  {
    // Called as function, not constructor
    return JS_ThrowTypeError(ctx, "QJS_PROXY_VALUE must be called with new");
  }

  // Get prototype from new_target
  proto = JS_GetPropertyStr(ctx, new_target, "prototype");
  if (JS_IsException(proto))
    return proto;

  // Create object with proper prototype
  obj = JS_NewObjectProto(ctx, proto);
  JS_FreeValue(ctx, proto);

  if (JS_IsException(obj))
    return obj;

  // Set the proxyId property
  if (argc > 0)
  {
    if (JS_SetPropertyStr(ctx, obj, "proxyId", JS_DupValue(ctx, argv[0])) < 0)
    {
      JS_FreeValue(ctx, obj);
      return JS_EXCEPTION;
    }
  }
  else
  {
    if (JS_SetPropertyStr(ctx, obj, "proxyId", JS_UNDEFINED) < 0)
    {
      JS_FreeValue(ctx, obj);
      return JS_EXCEPTION;
    }
  }

  return obj;
}

// Initialize QJS_PROXY_VALUE class and add it to global object
int init_qjs_proxy_value_class(JSContext *ctx)
{
  JSValue global_obj = JS_GetGlobalObject(ctx);

  // Create prototype object with toString method
  JSValue proto = JS_NewObject(ctx);
  JSValue toString_func = JS_NewCFunction(ctx, qjs_proxy_value_toString, "toString", 0);
  if (JS_SetPropertyStr(ctx, proto, "toString", toString_func) < 0)
  {
    JS_FreeValue(ctx, proto);
    JS_FreeValue(ctx, global_obj);
    return -1;
  }

  // Create the constructor function
  JSValue ctor = JS_NewCFunction2(ctx, qjs_proxy_value_constructor, "QJS_PROXY_VALUE", 1, JS_CFUNC_constructor, 0);

  // Set proto.constructor and ctor.prototype using QuickJS helper
  JS_SetConstructor(ctx, ctor, proto);

  // Add the constructor to the global object
  if (JS_SetPropertyStr(ctx, global_obj, "QJS_PROXY_VALUE", ctor) < 0)
  {
    JS_FreeValue(ctx, global_obj);
    return -1;
  }

  JS_FreeValue(ctx, global_obj);
  return 0;
}

// Create a new QJS_PROXY_VALUE instance directly in C for better performance
JSValue QJS_NewProxyValue(JSContext *ctx, int64_t proxyId)
{
  // Get the QJS_PROXY_VALUE constructor from global object
  JSValue global_obj = JS_GetGlobalObject(ctx);
  JSValue ctor = JS_GetPropertyStr(ctx, global_obj, "QJS_PROXY_VALUE");
  JS_FreeValue(ctx, global_obj);

  if (JS_IsException(ctor) || JS_IsUndefined(ctor))
  {
    JS_FreeValue(ctx, ctor);
    return JS_ThrowReferenceError(ctx, "QJS_PROXY_VALUE is not defined");
  }

  // Create argument for the constructor (proxyId)
  JSValue arg = JS_NewInt64(ctx, proxyId);
  JSValue args[1] = {arg};

  // Call the constructor with 'new'
  JSValue result = JS_CallConstructor(ctx, ctor, 1, args);

  // Clean up
  JS_FreeValue(ctx, ctor);
  JS_FreeValue(ctx, arg);

  return result;
}
