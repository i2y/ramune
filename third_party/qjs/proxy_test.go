package qjs_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type proxyFunctionTestCase struct {
	name     string
	setupFn  func(*qjs.Context)
	testCode string
	validate func(*testing.T, *qjs.Value, error)
}

type proxyErrorTestCase struct {
	name        string
	setupFn     func(*qjs.Context)
	testCode    string
	expectError bool
	errorCheck  func(*testing.T, error)
}

// setupTestRuntime creates a runtime with cleanup for proxy tests
func setupProxyTestRuntime(t *testing.T) *qjs.Runtime {
	runtime := must(qjs.New())
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

// TestBasicFunctionCall tests basic function calls (maintains original test)
func TestBasicFunctionCall(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.Context().SetFunc("add", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) != 2 {
			return this.NewUndefined(), errors.New("add requires 2 arguments")
		}

		a := this.Args()[0].Int32()
		b := this.Args()[1].Int32()
		return this.Context().NewInt32(a + b), nil
	})

	val, err := runtime.Eval("test.js", qjs.Code(`
        const result = add(5, 7);
        result;
    `))
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, int32(12), val.Int32())
}

// TestBasicFunctionCalls tests fundamental function calling functionality
func TestBasicFunctionCalls(t *testing.T) {
	testCases := []proxyFunctionTestCase{
		{
			name: "simple_addition_function",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("add", func(this *qjs.This) (*qjs.Value, error) {
					if len(this.Args()) != 2 {
						return this.NewUndefined(), errors.New("add requires 2 arguments")
					}
					a := this.Args()[0].Int32()
					b := this.Args()[1].Int32()
					return this.Context().NewInt32(a + b), nil
				})
			},
			testCode: "add(5, 7)",
			validate: func(t *testing.T, val *qjs.Value, err error) {
				require.NoError(t, err)
				assert.Equal(t, int32(12), val.Int32())
			},
		},
		{
			name: "string_number_concatenation",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("concat", func(this *qjs.This) (*qjs.Value, error) {
					if len(this.Args()) != 2 {
						return this.NewUndefined(), errors.New("concat requires 2 arguments")
					}
					str := this.Args()[0].String()
					num := this.Args()[1].Int32()
					result := fmt.Sprintf("%s-%d", str, num)
					return this.Context().NewString(result), nil
				})
			},
			testCode: `concat("test", 42)`,
			validate: func(t *testing.T, val *qjs.Value, err error) {
				require.NoError(t, err)
				assert.Equal(t, "test-42", val.String())
			},
		},
		{
			name: "no_arguments_function",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getConstant", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewString("constant value"), nil
				})
			},
			testCode: "getConstant()",
			validate: func(t *testing.T, val *qjs.Value, err error) {
				require.NoError(t, err)
				assert.Equal(t, "constant value", val.String())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := setupProxyTestRuntime(t)
			tc.setupFn(runtime.Context())

			val, err := runtime.Eval("test.js", qjs.Code(tc.testCode))
			defer func() {
				if val != nil {
					val.Free()
				}
			}()

			tc.validate(t, val, err)
		})
	}
}

// TestArgumentValidation tests function argument validation and type checking
func TestArgumentValidation(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	// Setup validation function
	runtime.Context().SetFunc("validateArgs", func(this *qjs.This) (*qjs.Value, error) {
		args := this.Args()

		if len(args) < 2 {
			return this.NewUndefined(), errors.New("requires at least 2 arguments")
		}

		if !args[0].IsString() {
			return this.NewUndefined(), errors.New("first argument must be a string")
		}

		if !args[1].IsNumber() {
			return this.NewUndefined(), errors.New("second argument must be a number")
		}

		return this.Context().NewBool(true), nil
	})

	t.Run("valid_arguments", func(t *testing.T) {
		val, err := runtime.Eval("test.js", qjs.Code(`validateArgs("test", 123)`))
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.Bool())
	})

	t.Run("insufficient_arguments", func(t *testing.T) {
		_, err := runtime.Eval("test.js", qjs.Code(`validateArgs("test")`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requires at least 2 arguments")
	})

	t.Run("wrong_argument_type", func(t *testing.T) {
		_, err := runtime.Eval("test.js", qjs.Code(`validateArgs("test", "not a number")`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "second argument must be a number")
	})

	t.Run("missing_first_argument", func(t *testing.T) {
		_, err := runtime.Eval("test.js", qjs.Code(`validateArgs()`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requires at least 2 arguments")
	})
}

// TestReturnValueTypes tests different return value types from Go functions
func TestReturnValueTypes(t *testing.T) {
	testCases := []struct {
		name     string
		setupFn  func(*qjs.Context)
		testCode string
		validate func(*testing.T, *qjs.Value)
	}{
		{
			name: "return_string",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getString", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewString("hello world"), nil
				})
			},
			testCode: "getString()",
			validate: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, "hello world", val.String())
			},
		},
		{
			name: "return_number",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getNumber", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewInt32(42), nil
				})
			},
			testCode: "getNumber()",
			validate: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, int32(42), val.Int32())
			},
		},
		{
			name: "return_boolean_true",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getBoolTrue", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewBool(true), nil
				})
			},
			testCode: "getBoolTrue()",
			validate: func(t *testing.T, val *qjs.Value) {
				assert.True(t, val.Bool())
			},
		},
		{
			name: "return_boolean_false",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getBoolFalse", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewBool(false), nil
				})
			},
			testCode: "getBoolFalse()",
			validate: func(t *testing.T, val *qjs.Value) {
				assert.False(t, val.Bool())
			},
		},
		{
			name: "return_null",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getNull", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewNull(), nil
				})
			},
			testCode: "getNull()",
			validate: func(t *testing.T, val *qjs.Value) {
				assert.True(t, val.IsNull())
			},
		},
		{
			name: "return_undefined",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("getUndefined", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().NewUndefined(), nil
				})
			},
			testCode: "getUndefined()",
			validate: func(t *testing.T, val *qjs.Value) {
				assert.True(t, val.IsUndefined())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := setupProxyTestRuntime(t)
			tc.setupFn(runtime.Context())

			val, err := runtime.Eval("test.js", qjs.Code(tc.testCode))
			defer val.Free()

			require.NoError(t, err)
			tc.validate(t, val)
		})
	}
}

// TestComplexReturnTypes tests complex JavaScript return types (objects, arrays)
func TestComplexReturnTypes(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("return_object", func(t *testing.T) {
		_, err := runtime.Eval("setup.js", qjs.Code(`
			function returnObject() {
				return {name: "test", id: 123};
			}
		`))
		require.NoError(t, err)

		val, err := runtime.Eval("test.js", qjs.Code("returnObject()"))
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.IsObject())

		obj := val.Object()
		defer obj.Free()
		assert.Equal(t, "test", obj.GetPropertyStr("name").String())
		assert.Equal(t, int32(123), obj.GetPropertyStr("id").Int32())
	})

	t.Run("return_array", func(t *testing.T) {
		_, err := runtime.Eval("setup.js", qjs.Code(`
			function returnArray() {
				return [1, 2, 3];
			}
		`))
		require.NoError(t, err)

		val, err := runtime.Eval("test.js", qjs.Code("returnArray()"))
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.IsArray())

		arr := val.Object()
		defer arr.Free()
		assert.Equal(t, int32(1), arr.GetPropertyStr("0").Int32())
		assert.Equal(t, int32(2), arr.GetPropertyStr("1").Int32())
		assert.Equal(t, int32(3), arr.GetPropertyStr("2").Int32())
	})

	t.Run("return_nested_object", func(t *testing.T) {
		_, err := runtime.Eval("setup.js", qjs.Code(`
			function returnNestedObject() {
				return {
					user: {name: "john", age: 30},
					meta: {created: true, version: 1}
				};
			}
		`))
		require.NoError(t, err)

		val, err := runtime.Eval("test.js", qjs.Code("returnNestedObject()"))
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.IsObject())

		obj := val.Object()
		defer obj.Free()

		user := obj.GetPropertyStr("user").Object()
		defer user.Free()
		assert.Equal(t, "john", user.GetPropertyStr("name").String())
		assert.Equal(t, int32(30), user.GetPropertyStr("age").Int32())

		meta := obj.GetPropertyStr("meta").Object()
		defer meta.Free()
		assert.True(t, meta.GetPropertyStr("created").Bool())
		assert.Equal(t, int32(1), meta.GetPropertyStr("version").Int32())
	})
}

// TestProxyErrorHandling tests error propagation and handling
func TestProxyErrorHandling(t *testing.T) {
	testCases := []proxyErrorTestCase{
		{
			name: "function_returns_error",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("failingFunction", func(this *qjs.This) (*qjs.Value, error) {
					return this.NewUndefined(), errors.New("this function fails")
				})
			},
			testCode:    "failingFunction()",
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "this function fails")
			},
		},
		{
			name: "function_throws_error",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("throwingFunction", func(this *qjs.This) (*qjs.Value, error) {
					return this.Context().ThrowError(errors.New("thrown error")), nil
				})
			},
			testCode:    "throwingFunction()",
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "thrown error")
			},
		},
		{
			name: "custom_error_message",
			setupFn: func(ctx *qjs.Context) {
				ctx.SetFunc("customError", func(this *qjs.This) (*qjs.Value, error) {
					return this.NewUndefined(), fmt.Errorf("custom error with code: %d", 404)
				})
			},
			testCode:    "customError()",
			expectError: true,
			errorCheck: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "custom error with code: 404")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := setupProxyTestRuntime(t)
			tc.setupFn(runtime.Context())

			_, err := runtime.Eval("test.js", qjs.Code(tc.testCode))

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorCheck != nil {
					tc.errorCheck(t, err)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPanicRecovery(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("panic_with_string", func(t *testing.T) {
		runtime.Context().SetFunc("panicFunction", func(this *qjs.This) (*qjs.Value, error) {
			panic("this function panics")
		})

		_, err := runtime.Eval("test.js", qjs.Code(`
			try {
				panicFunction();
			} catch (e) {
				throw new Error("Caught: " + e.message);
			}
		`))

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "this function panics")
	})

	t.Run("panic_with_error", func(t *testing.T) {
		runtime.Context().SetFunc("panicWithError", func(this *qjs.This) (*qjs.Value, error) {
			panic(errors.New("panic error"))
		})

		_, err := runtime.Eval("test.js", qjs.Code("panicWithError()"))

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "panic error")
	})

	t.Run("panic_recovery_continues_execution", func(t *testing.T) {
		runtime.Context().SetFunc("sometimesPanics", func(this *qjs.This) (*qjs.Value, error) {
			if len(this.Args()) > 0 && this.Args()[0].Bool() {
				panic("intentional panic")
			}
			return this.Context().NewString("success"), nil
		})

		// Test that after a panic, we can still call functions
		_, err1 := runtime.Eval("test1.js", qjs.Code("sometimesPanics(true)"))
		assert.Error(t, err1)

		val2, err2 := runtime.Eval("test2.js", qjs.Code("sometimesPanics(false)"))
		defer val2.Free()
		require.NoError(t, err2)
		assert.Equal(t, "success", val2.String())
	})
}

// TestAsyncFunctions tests asynchronous function handling
func TestAsyncFunctions(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("async_function_success", func(t *testing.T) {
		runtime.Context().SetAsyncFunc("asyncSuccess", func(this *qjs.This) {
			if this.IsAsync() {
				this.Promise().Resolve(this.Context().NewString("async success"))
			}
		})

		val, err := runtime.Eval("test.js", qjs.Code(`
			async function test() {
				const result = await asyncSuccess();
				return result;
			}
			await test();
		`), qjs.FlagAsync())
		defer val.Free()

		require.NoError(t, err)
		assert.Equal(t, "async success", val.String())
	})

	t.Run("async_function_failure", func(t *testing.T) {
		runtime.Context().SetAsyncFunc("asyncFailure", func(this *qjs.This) {
			if this.IsAsync() {
				this.Promise().Reject(this.Context().NewError(errors.New("async failure")))
			}
		})

		_, err := runtime.Eval("test.js", qjs.Code(`
			async function test() {
				const result = await asyncFailure();
				return result;
			}
			await test();
		`), qjs.FlagAsync())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "async failure")
	})

	t.Run("async_function_with_data", func(t *testing.T) {
		runtime.Context().SetAsyncFunc("asyncWithData", func(this *qjs.This) {
			if this.IsAsync() {
				if len(this.Args()) > 0 {
					data := this.Args()[0].String()
					result := fmt.Sprintf("processed: %s", data)
					this.Promise().Resolve(this.Context().NewString(result))
				} else {
					this.Promise().Reject(this.Context().NewError(errors.New("no data provided")))
				}
			}
		})

		val, err := runtime.Eval("test.js", qjs.Code(`
			await asyncWithData("test data");
		`), qjs.FlagAsync())
		defer val.Free()

		require.NoError(t, err)
		assert.Equal(t, "processed: test data", val.String())
	})

	t.Run("async_function_with_goroutine", func(t *testing.T) {
		_, ctx := setupTestContext(t)
		done := make(chan struct{})

		ctx.SetAsyncFunc("testAsyncFunc", func(this *qjs.This) {
			// Simulate async work and resolve with a value
			go func() {
				defer close(done)
				// Simulate some delay
				time.Sleep(10 * time.Millisecond)
				val := this.Context().NewString("async result")
				this.Promise().Resolve(val)
			}()
		})

		result := must(ctx.Eval("test.js", qjs.Code(`({
			promise: testAsyncFunc()
		})`)))
		defer result.Free()

		promise := result.GetPropertyStr("promise")
		assert.True(t, promise.IsPromise())

		// Wait for the goroutine to complete before awaiting the promise
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for async goroutine to complete")
		}

		a := must(promise.Await())
		assert.Equal(t, "async result", a.String())
	})
}

// TestThisContext tests "this" context handling
func TestThisContext(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	t.Run("this_object_property_access", func(t *testing.T) {
		runtime.Context().SetFunc("getThisProperty", func(this *qjs.This) (*qjs.Value, error) {
			thisValue := this.Value
			if !thisValue.IsObject() {
				return this.NewUndefined(), errors.New("'this' is not an object")
			}

			prop := thisValue.GetPropertyStr("name")
			return prop, nil
		})

		val, err := runtime.Eval("test.js", qjs.Code(`
			const obj = { name: "test object" };
			const result = getThisProperty.call(obj);
			result;
		`))
		defer val.Free()

		require.NoError(t, err)
		assert.Equal(t, "test object", val.String())
	})

	t.Run("this_context_modification", func(t *testing.T) {
		runtime.Context().SetFunc("modifyThis", func(this *qjs.This) (*qjs.Value, error) {
			thisValue := this.Value
			if !thisValue.IsObject() {
				return this.NewUndefined(), errors.New("'this' is not an object")
			}

			// Modify the this object
			thisValue.SetPropertyStr("modified", this.Context().NewBool(true))
			return this.Context().NewString("modified"), nil
		})

		val, err := runtime.Eval("test_modify.js", qjs.Code(`
			const testObj = { name: "test" };
			modifyThis.call(testObj);
			testObj.modified;
		`))
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.Bool())
	})

	t.Run("this_global_context", func(t *testing.T) {
		runtime.Context().SetFunc("checkGlobalThis", func(this *qjs.This) (*qjs.Value, error) {
			// When called without explicit this, should be global object
			return this.Context().NewBool(this.Value.IsObject()), nil
		})

		val, err := runtime.Eval("test.js", qjs.Code("checkGlobalThis()"))
		defer val.Free()

		require.NoError(t, err)
		assert.True(t, val.Bool())
	})
}

// TestMultipleFunctionInteraction tests interaction between multiple registered functions
func TestMultipleFunctionInteraction(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	// Setup multiple interacting functions
	runtime.Context().SetFunc("setGlobalValue", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) != 1 {
			return this.NewUndefined(), errors.New("requires 1 argument")
		}

		value := this.Args()[0]
		global := this.Context().Global()
		global.SetPropertyStr("testValue", value)

		return this.NewUndefined(), nil
	})

	runtime.Context().SetFunc("getGlobalValue", func(this *qjs.This) (*qjs.Value, error) {
		global := this.Context().Global()
		return global.GetPropertyStr("testValue"), nil
	})

	runtime.Context().SetFunc("processGlobalValue", func(this *qjs.This) (*qjs.Value, error) {
		global := this.Context().Global()
		value := global.GetPropertyStr("testValue")

		if value.IsUndefined() {
			return this.Context().NewString("no value set"), nil
		}

		processed := fmt.Sprintf("processed: %s", value.String())
		return this.Context().NewString(processed), nil
	})

	t.Run("function_chain_interaction", func(t *testing.T) {
		_, err := runtime.Eval("test.js", qjs.Code(`setGlobalValue("global test value")`))
		require.NoError(t, err)

		val, err := runtime.Eval("test.js", qjs.Code(`getGlobalValue()`))
		defer val.Free()
		require.NoError(t, err)
		assert.Equal(t, "global test value", val.String())

		processedVal, err := runtime.Eval("test.js", qjs.Code(`processGlobalValue()`))
		defer processedVal.Free()
		require.NoError(t, err)
		assert.Equal(t, "processed: global test value", processedVal.String())
	})

	t.Run("function_state_persistence", func(t *testing.T) {
		// Set initial value
		_, err := runtime.Eval("test1.js", qjs.Code(`setGlobalValue("persistent value")`))
		require.NoError(t, err)

		// Access from different eval call
		val, err := runtime.Eval("test2.js", qjs.Code(`getGlobalValue()`))
		defer val.Free()
		require.NoError(t, err)
		assert.Equal(t, "persistent value", val.String())
	})
}

// TestProxyRegistryOperations tests the improved ProxyRegistry functionality
func TestProxyRegistryOperations(t *testing.T) {
	t.Run("registry_basic_operations", func(t *testing.T) {
		registry := qjs.NewProxyRegistry()

		// Test registration
		fn1 := func() string { return "test1" }
		fn2 := func() string { return "test2" }

		id1 := registry.Register(fn1)
		id2 := registry.Register(fn2)

		assert.NotEqual(t, id1, id2)
		assert.NotZero(t, id1)
		assert.NotZero(t, id2)

		// Test retrieval
		retrieved1, ok1 := registry.Get(id1)
		assert.True(t, ok1)
		assert.NotNil(t, retrieved1)

		retrieved2, ok2 := registry.Get(id2)
		assert.True(t, ok2)
		assert.NotNil(t, retrieved2)

		// Test length
		assert.Equal(t, 2, registry.Len())
	})

	t.Run("registry_nil_handling", func(t *testing.T) {
		registry := qjs.NewProxyRegistry()

		// Test nil registration
		id := registry.Register(nil)
		assert.Zero(t, id)

		// Test invalid ID retrieval
		_, ok := registry.Get(0)
		assert.False(t, ok)

		// Test non-existent ID
		_, ok = registry.Get(999)
		assert.False(t, ok)
	})

	t.Run("registry_unregister", func(t *testing.T) {
		registry := qjs.NewProxyRegistry()

		fn := func() string { return "test" }
		id := registry.Register(fn)

		assert.Equal(t, 1, registry.Len())

		// Test successful unregistration
		removed := registry.Unregister(id)
		assert.True(t, removed)
		assert.Equal(t, 0, registry.Len())

		// Test retrieval after unregistration
		_, ok := registry.Get(id)
		assert.False(t, ok)

		// Test duplicate unregistration
		removed = registry.Unregister(id)
		assert.False(t, removed)

		// Test unregistration zero ID
		removed = registry.Unregister(0)
		assert.False(t, removed)
	})

	t.Run("registry_clear", func(t *testing.T) {
		registry := qjs.NewProxyRegistry()

		// Register multiple functions
		for i := 0; i < 5; i++ {
			registry.Register(func() int { return i })
		}

		assert.Equal(t, 5, registry.Len())

		// Clear registry
		registry.Clear()
		assert.Equal(t, 0, registry.Len())
	})
}

// TestFunctionWithDifferentArgTypes maintains original test compatibility
func TestFunctionWithDifferentArgTypes(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.Context().SetFunc("concat", func(this *qjs.This) (*qjs.Value, error) {
		if len(this.Args()) != 2 {
			return this.NewUndefined(), errors.New("concat requires 2 arguments")
		}

		str := this.Args()[0].String()
		num := this.Args()[1].Int32()
		result := fmt.Sprintf("%s-%d", str, num)

		return this.Context().NewString(result), nil
	})

	val, err := runtime.Eval("test.js", qjs.Code(`
        const result = concat("test", 42);
        result;
    `))
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, "test-42", val.String())
}

// TestFunctionWithThis maintains original test compatibility
func TestFunctionWithThis(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	// Function that uses the "this" value
	runtime.Context().SetFunc("getThisProperty", func(this *qjs.This) (*qjs.Value, error) {
		thisValue := this.Value
		if !thisValue.IsObject() {
			return this.NewUndefined(), errors.New("'this' is not an object")
		}

		prop := thisValue.GetPropertyStr("name")
		return prop, nil
	})

	val, err := runtime.Eval("test.js", qjs.Code(`
        const obj = { name: "test object" };
        const result = getThisProperty.call(obj);
        result;
    `))
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, "test object", val.String())
}

// TestPanicHandling maintains original test compatibility
func TestPanicHandling(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.Context().SetFunc("panicFunction", func(this *qjs.This) (*qjs.Value, error) {
		panic("this function panics")
	})

	_, err := runtime.Eval("test.js", qjs.Code(`
			try {
					panicFunction();
			} catch (e) {
					throw new Error("Caught: " + e.message);
			}
	`))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "this function panics")
}

// TestMultipleFunctions maintains original test compatibility
func TestMultipleFunctions(t *testing.T) {
	runtime := setupProxyTestRuntime(t)

	runtime.Context().SetFunc(
		"setGlobalValue",
		func(this *qjs.This) (*qjs.Value, error) {
			if len(this.Args()) != 1 {
				return this.NewUndefined(), errors.New("requires 1 argument")
			}

			value := this.Args()[0]
			global := this.Context().Global()
			global.SetPropertyStr("testValue", value)

			return this.NewUndefined(), nil
		},
	)

	runtime.Context().SetFunc(
		"getGlobalValue",
		func(this *qjs.This) (*qjs.Value, error) {
			global := this.Context().Global()
			return global.GetPropertyStr("testValue"), nil
		},
	)

	_, err := runtime.Eval("test.js", qjs.Code(`
		setGlobalValue("global test value");
	`))
	require.NoError(t, err)

	val, err := runtime.Eval("test.js", qjs.Code(`
		getGlobalValue();
	`))
	defer val.Free()

	require.NoError(t, err)
	assert.Equal(t, "global test value", val.String())
}
