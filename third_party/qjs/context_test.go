package qjs_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
)

// Basic Properties Tests
func TestContextBasicProperties(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("string_representation", func(t *testing.T) {
		assert.Contains(t, ctx.String(), "Context(")
	})

	t.Run("global_object_access", func(t *testing.T) {
		global := ctx.Global()
		assert.True(t, global.IsObject())

		// Check for standard JavaScript global objects
		console := global.GetPropertyStr("console")
		assert.True(t, console.IsObject())
		console.Free()

		object := global.GetPropertyStr("Object")
		assert.True(t, object.IsFunction())
		object.Free()
	})
}

// Value Creation Tests
func TestContextValueCreation(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("proxy_values", func(t *testing.T) {
		goFuncWithContext := func(ctx context.Context, num int) int {
			fmt.Printf("context check: is nil=%t, value=%v\n", ctx == nil, ctx)
			return num * 2
		}
		jsFuncWithContext := must(qjs.ToJsValue(ctx, goFuncWithContext))
		defer jsFuncWithContext.Free()
		ctx.Global().SetPropertyStr("funcWithContext", jsFuncWithContext)

		ctx.SetFunc("$context", func(this *qjs.This) (*qjs.Value, error) {
			val := ctx.NewProxyValue(context.Background())
			return val, nil
		})

		result, err := ctx.Eval("test.js", qjs.Code(`
			funcWithContext($context(), 10);
		`))
		assert.NoError(t, err)
		defer result.Free()
		assert.Equal(t, int32(20), result.Int32())
	})

	t.Run("primitive_values", func(t *testing.T) {
		testCases := []struct {
			name     string
			creator  func() *qjs.Value
			verifier func(*testing.T, *qjs.Value)
		}{
			{
				name:    "uninitialized",
				creator: func() *qjs.Value { return ctx.NewUninitialized() },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.True(t, val.IsUninitialized())
				},
			},
			{
				name:    "null",
				creator: func() *qjs.Value { return ctx.NewNull() },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.True(t, val.IsNull())
				},
			},
			{
				name:    "undefined",
				creator: func() *qjs.Value { return ctx.NewUndefined() },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.True(t, val.IsUndefined())
				},
			},
			{
				name:    "boolean_true",
				creator: func() *qjs.Value { return ctx.NewBool(true) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.True(t, val.Bool())
				},
			},
			{
				name:    "boolean_false",
				creator: func() *qjs.Value { return ctx.NewBool(false) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.False(t, val.Bool())
				},
			},
			{
				name:    "string",
				creator: func() *qjs.Value { return ctx.NewString("Hello, World!") },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.Equal(t, "Hello, World!", val.String())
				},
			},
			{
				name:    "int32",
				creator: func() *qjs.Value { return ctx.NewInt32(42) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.Equal(t, int32(42), val.Int32())
				},
			},
			{
				name:    "int64",
				creator: func() *qjs.Value { return ctx.NewInt64(9876543210) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.Equal(t, int64(9876543210), val.Int64())
				},
			},
			{
				name:    "uint32",
				creator: func() *qjs.Value { return ctx.NewUint32(42) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.Equal(t, uint32(42), val.Uint32())
				},
			},
			{
				name:    "float64",
				creator: func() *qjs.Value { return ctx.NewFloat64(3.14159) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.InEpsilon(t, 3.14159, val.Float64(), 0.00001)
				},
			},
			{
				name:    "bigint64",
				creator: func() *qjs.Value { return ctx.NewBigInt64(9876543210) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.True(t, val.IsBigInt())
				},
			},
			{
				name:    "biguint64",
				creator: func() *qjs.Value { return ctx.NewBigUint64(9876543210) },
				verifier: func(t *testing.T, val *qjs.Value) {
					assert.True(t, val.IsBigInt())
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				val := tc.creator()
				defer val.Free()
				tc.verifier(t, val)
			})
		}
	})

	t.Run("date_values", func(t *testing.T) {
		tm := time.Date(2023, 10, 1, 12, 0, 0, 0, time.UTC)
		val := ctx.NewDate(&tm)
		defer val.Free()
		assert.True(t, val.IsDate())
		// Compare milliseconds due to JavaScript Date precision
		assert.Equal(t, tm.UnixNano()/1e6, val.DateTime().UnixNano()/1e6)
	})
}

// Objects and Collections Tests
func TestContextObjectsAndCollections(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("object_creation_and_properties", func(t *testing.T) {
		obj := ctx.NewObject()
		defer obj.Free()
		assert.True(t, obj.IsObject())

		obj.SetPropertyStr("name", ctx.NewString("John"))
		obj.SetPropertyStr("age", ctx.NewInt32(30))

		assert.Equal(t, "John", obj.GetPropertyStr("name").String())
		assert.Equal(t, int32(30), obj.GetPropertyStr("age").Int32())
	})

	t.Run("json_parsing", func(t *testing.T) {
		jsonObj := ctx.ParseJSON(`{"name":"John","age":30,"isActive":true,"tags":["developer","golang"]}`)
		defer jsonObj.Free()

		assert.True(t, jsonObj.IsObject())
		assert.Equal(t, "John", jsonObj.GetPropertyStr("name").String())
		assert.Equal(t, int32(30), jsonObj.GetPropertyStr("age").Int32())
		assert.True(t, jsonObj.GetPropertyStr("isActive").Bool())

		tags := jsonObj.GetPropertyStr("tags")
		defer tags.Free()
		assert.True(t, tags.IsArray())

		tagsArray := must(tags.ToArray())
		assert.Equal(t, int64(2), tagsArray.Len())

		tag0 := tagsArray.Get(0)
		defer tag0.Free()
		assert.Equal(t, "developer", tag0.String())
	})
}

// Error Handling Tests
func TestContextErrorHandling(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("exception_throwing", func(t *testing.T) {
		testCases := []struct {
			name           string
			throwFunc      func() *qjs.Value
			expectedErrMsg string
		}{
			{
				name: "throw_error",
				throwFunc: func() *qjs.Value {
					return ctx.ThrowError(errors.New("custom error"))
				},
				expectedErrMsg: "custom error",
			},
			{
				name: "throw_syntax_error",
				throwFunc: func() *qjs.Value {
					return ctx.ThrowSyntaxError("syntax %s", "error")
				},
				expectedErrMsg: "syntax error",
			},
			{
				name: "throw_type_error",
				throwFunc: func() *qjs.Value {
					return ctx.ThrowTypeError("type %s", "error")
				},
				expectedErrMsg: "type error",
			},
			{
				name: "throw_reference_error",
				throwFunc: func() *qjs.Value {
					return ctx.ThrowReferenceError("reference %s", "error")
				},
				expectedErrMsg: "reference error",
			},
			{
				name: "throw_range_error",
				throwFunc: func() *qjs.Value {
					return ctx.ThrowRangeError("range %s", "error")
				},
				expectedErrMsg: "range error",
			},
			{
				name: "throw_internal_error",
				throwFunc: func() *qjs.Value {
					return ctx.ThrowInternalError("internal %s", "error")
				},
				expectedErrMsg: "internal error",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				// Clear any lingering exceptions from previous tests
				if ctx.HasException() {
					_ = ctx.Exception()
				}

				val := tc.throwFunc()
				defer val.Free()

				assert.True(t, ctx.HasException())
				err := ctx.Exception()
				assert.Contains(t, err.Error(), tc.expectedErrMsg)
			})
		}
	})
}

// Script Execution Tests
func TestContextScriptExecution(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("eval_basic", func(t *testing.T) {
		result := must(ctx.Eval("test_eval.js", qjs.Code(`1 + 2`)))
		defer result.Free()
		assert.Equal(t, int32(3), result.Int32())
	})

	t.Run("eval_with_module_type", func(t *testing.T) {
		result := must(ctx.Eval("test_module.js",
			qjs.Code(`export default 42;`),
			qjs.TypeModule()))
		defer result.Free()
		assert.Equal(t, int32(42), result.Int32())
	})

	t.Run("compile_and_execute", func(t *testing.T) {
		bytecode := must(ctx.Compile("test_compile.js", qjs.Code(`2 * 3`)))
		assert.NotEmpty(t, bytecode)

		result := must(ctx.Eval("test_bytecode.js", qjs.Bytecode(bytecode)))
		defer result.Free()
		assert.Equal(t, int32(6), result.Int32())
	})

	t.Run("load_and_import", func(t *testing.T) {
		moduleCode := `export const value = 42;`
		loaded := must(ctx.Load("test_load.js", qjs.Code(moduleCode)))
		defer loaded.Free()

		result := must(ctx.Eval("test_import.js",
			qjs.Code(`
			import { value } from 'test_load.js';
			export default value`),
			qjs.TypeModule()))
		defer result.Free()
		assert.Equal(t, int32(42), result.Int32())
	})
}

// Function Binding Tests
func TestContextFunctionBinding(t *testing.T) {
	t.Run("Synchronous_Functions", func(t *testing.T) {
		testSynchronousFunctions(t)
	})

	t.Run("Asynchronous_Functions", func(t *testing.T) {
		testAsynchronousFunctions(t)
	})
}

// Helper structure for function test cases
type functionTestCase struct {
	name              string
	fn                qjs.Function
	asyncFn           qjs.AsyncFunction
	code              string
	expectErrorString string
	expectError       func(*testing.T, error)
	expectValue       func(*testing.T, *qjs.Value)
}

// Synchronous Functions Tests
func testSynchronousFunctions(t *testing.T) {
	t.Run("basic_function_registration", func(t *testing.T) {
		_, ctx := setupRuntime(t)

		ctx.SetFunc("testFunc", func(this *qjs.This) (*qjs.Value, error) {
			args := this.Args()
			if len(args) > 0 {
				return this.Context().NewString("Hello " + args[0].String()), nil
			}
			return this.Context().NewString("Hello World"), nil
		})

		result := must(ctx.Eval("test.js", qjs.Code(`testFunc("John")`)))
		defer result.Free()
		assert.Equal(t, "Hello John", result.String())

		result2 := must(ctx.Eval("test2.js", qjs.Code("testFunc()")))
		defer result2.Free()
		assert.Equal(t, "Hello World", result2.String())
	})

	t.Run("error_handling", func(t *testing.T) {
		tests := createErrorHandlingTests()
		runFunctionTests(t, tests, false)
	})

	t.Run("return_values", func(t *testing.T) {
		tests := createReturnValueTests()
		runFunctionTests(t, tests, false)
	})

	t.Run("argument_handling", func(t *testing.T) {
		tests := createArgumentHandlingTests()
		runFunctionTests(t, tests, false)
	})
}

// Asynchronous Functions Tests
func testAsynchronousFunctions(t *testing.T) {
	t.Run("basic_async_function", func(t *testing.T) {
		_, ctx := setupRuntime(t)

		ctx.SetAsyncFunc("asyncTestFunc", func(this *qjs.This) {
			this.Promise().Resolve(this.Context().NewString("async result"))
		})

		result := must(ctx.Eval(
			"test_async.js",
			qjs.Code(`async function run() { return await asyncTestFunc(); }; run()`),
		))
		defer result.Free()
		assert.Equal(t, "async result", result.String())
	})

	t.Run("async_error_handling", func(t *testing.T) {
		tests := createAsyncErrorHandlingTests()
		runFunctionTests(t, tests, true)
	})

	t.Run("async_return_values", func(t *testing.T) {
		tests := createAsyncReturnValueTests()
		runFunctionTests(t, tests, true)
	})

	t.Run("async_argument_handling", func(t *testing.T) {
		tests := createAsyncArgumentHandlingTests()
		runFunctionTests(t, tests, true)
	})
}

// Atoms Tests
func TestContextAtoms(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("atom_from_string", func(t *testing.T) {
		atom := ctx.NewAtom("test")
		defer atom.Free()

		str := atom.String()
		assert.Equal(t, "test", str)

		val := atom.ToValue()
		defer val.Free()
		assert.Equal(t, "test", val.String())
	})

	t.Run("atom_from_index", func(t *testing.T) {
		atom := ctx.NewAtomIndex(42)
		defer atom.Free()

		val := atom.ToValue()
		defer val.Free()
		assert.NotEmpty(t, val.String())
	})
}

func createErrorHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name:              "return_error",
			expectErrorString: "error string",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.NewUndefined(), errors.New("error string")
			},
		},
		{
			name:              "throw_string",
			expectErrorString: "throw string",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				this.Context().Throw(this.Context().NewString("throw string"))
				return this.NewUndefined(), nil
			},
		},
		{
			name:              "throw_bool",
			expectErrorString: "true",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				this.Context().Throw(this.Context().NewBool(true))
				return this.NewUndefined(), nil
			},
		},
		{
			name:              "throw_custom_error",
			expectErrorString: "custom error",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.Context().ThrowError(errors.New("custom error")), nil
			},
		},
	}
}

func createReturnValueTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "return_string",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.Context().NewString("Hello world"), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, "Hello world", val.String())
			},
		},
		{
			name: "return_number",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.Context().NewInt32(12345), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, int32(12345), val.Int32())
			},
		},
		{
			name: "return_bool",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.Context().NewBool(true), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.True(t, val.Bool())
			},
		},
		{
			name: "return_undefined",
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.NewUndefined(), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.True(t, val.IsUndefined())
			},
		},
	}
}

func createArgumentHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "string_argument",
			code: `goFunc('World')`,
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.Context().NewString(
					"Hello " + this.Args()[0].String(),
				), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, "Hello World", val.String())
			},
		},
		{
			name: "numeric_arguments",
			code: `goFunc(10, 20)`,
			fn: func(this *qjs.This) (*qjs.Value, error) {
				return this.Context().NewInt32(
					this.Args()[0].Int32() + this.Args()[1].Int32(),
				), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, int32(30), val.Int32())
			},
		},
		{
			name: "object_argument",
			code: `goFunc({"name": "John Doe", "age": 30})`,
			fn: func(this *qjs.This) (*qjs.Value, error) {
				obj := this.Args()[0].Object()
				defer obj.Free()
				return this.Context().NewString(
					"Hello " + obj.GetPropertyStr("name").String(),
				), nil
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, "Hello John Doe", val.String())
			},
		},
	}
}

func createAsyncErrorHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name:              "reject_string",
			expectErrorString: "throw string",
			asyncFn: func(this *qjs.This) {
				this.Promise().Reject(this.Context().NewString("throw string"))
			},
		},
		{
			name:              "reject_number",
			expectErrorString: "12345",
			asyncFn: func(this *qjs.This) {
				this.Promise().Reject(this.Context().NewInt32(12345))
			},
		},
		{
			name:              "throw_in_async",
			expectErrorString: "syntax error",
			asyncFn: func(this *qjs.This) {
				this.Context().ThrowSyntaxError("syntax error")
			},
		},
	}
}

func createAsyncReturnValueTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "resolve_string",
			asyncFn: func(this *qjs.This) {
				this.Promise().Resolve(this.Context().NewString("Hello world"))
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, "Hello world", val.String())
			},
		},
		{
			name: "resolve_number",
			asyncFn: func(this *qjs.This) {
				this.Promise().Resolve(this.Context().NewInt32(12345))
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, int32(12345), val.Int32())
			},
		},
	}
}

func createAsyncArgumentHandlingTests() []functionTestCase {
	return []functionTestCase{
		{
			name: "async_string_argument",
			code: `await goAsyncFunc('World')`,
			asyncFn: func(this *qjs.This) {
				this.Promise().Resolve(this.Context().NewString(
					"Hello " + this.Args()[0].String(),
				))
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, "Hello World", val.String())
			},
		},
		{
			name: "async_numeric_arguments",
			code: `await goAsyncFunc(10, 20)`,
			asyncFn: func(this *qjs.This) {
				this.Promise().Resolve(this.Context().NewInt32(
					this.Args()[0].Int32() + this.Args()[1].Int32(),
				))
			},
			expectValue: func(t *testing.T, val *qjs.Value) {
				assert.Equal(t, int32(30), val.Int32())
			},
		},
	}
}

// runFunctionTests executes a set of function test cases
func runFunctionTests(t *testing.T, tests []functionTestCase, isAsync bool) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := must(qjs.New())
			defer rt.Close()

			code := test.code
			if test.code == "" {
				if isAsync {
					code = `await goAsyncFunc()`
				} else {
					code = `goFunc()`
				}
			}

			funcName := "goFunc"
			if isAsync {
				funcName = "goAsyncFunc"
				rt.Context().SetAsyncFunc(funcName, test.asyncFn)
			} else {
				rt.Context().SetFunc(funcName, test.fn)
			}

			var val *qjs.Value
			var err error
			if isAsync {
				val, err = rt.Eval(
					test.name+".js",
					qjs.Code(code),
					qjs.FlagAsync(),
				)
			} else {
				val, err = rt.Eval(test.name+".js", qjs.Code(code))
			}
			defer val.Free()

			if test.expectErrorString != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), test.expectErrorString)
			}

			if test.expectError != nil {
				test.expectError(t, err)
				assert.True(t, val.IsUndefined())
			}

			if test.expectValue != nil {
				assert.NoError(t, err)
				test.expectValue(t, val)
			}
		})
	}
}
