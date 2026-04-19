package qjs

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCombineErrors(t *testing.T) {
	t.Run("empty slice returns nil", func(t *testing.T) {
		result := combineErrors()
		assert.Nil(t, result)
	})

	t.Run("single error", func(t *testing.T) {
		err := errors.New("test error")
		result := combineErrors(err)
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "test error")
	})

	t.Run("multiple errors", func(t *testing.T) {
		err1 := errors.New("error 1")
		err2 := errors.New("error 2")
		result := combineErrors(err1, err2)
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "error 1")
		assert.Contains(t, result.Error(), "error 2")
	})

	t.Run("mixed nil and non-nil errors", func(t *testing.T) {
		err1 := errors.New("error 1")
		var err2 error // nil
		err3 := errors.New("error 3")
		result := combineErrors(err1, err2, err3)
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "error 1")
		assert.Contains(t, result.Error(), "error 3")
	})

	t.Run("all nil errors", func(t *testing.T) {
		var err1, err2, err3 error // all nil
		result := combineErrors(err1, err2, err3)
		assert.Error(t, result)
		assert.Equal(t, "", result.Error()) // When all errors are nil, empty string
	})
}

func TestNewJsStringifyErr(t *testing.T) {
	t.Run("basic error wrapping", func(t *testing.T) {
		originalErr := errors.New("stringify failed")
		result := newJsStringifyErr("test", originalErr)

		assert.Error(t, result)
		assert.Contains(t, result.Error(), "js test:")
		assert.Contains(t, result.Error(), "stringify failed")
		assert.True(t, errors.Is(result, originalErr))
	})

	t.Run("different kinds", func(t *testing.T) {
		originalErr := errors.New("conversion error")

		result1 := newJsStringifyErr("array", originalErr)
		assert.Contains(t, result1.Error(), "js array:")

		result2 := newJsStringifyErr("object", originalErr)
		assert.Contains(t, result2.Error(), "js object:")
	})
}

func TestNewProxyErr(t *testing.T) {
	t.Run("error type input", func(t *testing.T) {
		originalErr := errors.New("original error")
		result := newProxyErr(123, originalErr)

		assert.Error(t, result)
		assert.Contains(t, result.Error(), "functionProxy [123]:")
		assert.Contains(t, result.Error(), "original error")
		assert.True(t, errors.Is(result, originalErr))
		// Should contain stack trace
		assert.Contains(t, result.Error(), "TestNewProxyErr")
	})

	t.Run("string type input", func(t *testing.T) {
		result := newProxyErr(456, "string error")

		assert.Error(t, result)
		assert.Contains(t, result.Error(), "functionProxy [456]: string error")
		// Should contain stack trace
		assert.Contains(t, result.Error(), "TestNewProxyErr")
	})

	t.Run("other type input", func(t *testing.T) {
		result := newProxyErr(789, 42)

		assert.Error(t, result)
		assert.Contains(t, result.Error(), "functionProxy [789]: 42")
		// Should contain stack trace
		assert.Contains(t, result.Error(), "TestNewProxyErr")
	})

	t.Run("contains debug stack", func(t *testing.T) {
		result := newProxyErr(999, "test")

		// Verify the stack trace is included
		stackLines := strings.Split(result.Error(), "\n")
		found := false
		for _, line := range stackLines {
			if strings.Contains(line, "TestNewProxyErr") {
				found = true
				break
			}
		}
		assert.True(t, found, "Stack trace should contain test function name")
	})
}

func TestNewJsToGoErr_JSONStringifyFailure(t *testing.T) {
	// This test requires a runtime to create a Value that fails JSONStringify
	rt, err := New()
	require.NoError(t, err)
	defer rt.Close()

	// Create a Value that will fail JSONStringify (circular reference)
	result, err := rt.Eval("test.js", Code(`
		const obj = {};
		obj.self = obj; // circular reference
		obj
	`))
	require.NoError(t, err)
	defer result.Free()

	// This should trigger the JSONStringify failure path
	jsErr := newJsToGoErr(result, errors.New("conversion failed"), "test details")

	assert.Error(t, jsErr)
	assert.Contains(t, jsErr.Error(), "conversion failed")
	assert.Contains(t, jsErr.Error(), "test details")
	// Should contain fallback string representation
	assert.Contains(t, jsErr.Error(), "[object Object]")
}

func TestNewJsToGoErr_EmptyErrorConditions(t *testing.T) {
	rt, err := New()
	require.NoError(t, err)
	defer rt.Close()

	ctx := rt.Context()

	t.Run("with nil error", func(t *testing.T) {
		value := ctx.NewString("test")
		defer value.Free()

		result := newJsToGoErr(value, nil, "detail")
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "cannot convert JS detail")
		assert.NotContains(t, result.Error(), ":")
	})

	t.Run("with undefined value", func(t *testing.T) {
		value := ctx.NewUndefined()
		defer value.Free()

		result := newJsToGoErr(value, errors.New("test"), "detail")
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "Undefined")
	})

	t.Run("with null value", func(t *testing.T) {
		value := ctx.NewNull()
		defer value.Free()

		result := newJsToGoErr(value, errors.New("test"), "detail")
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "Null")
	})
}

func createCircularValue(ctx *Context) *Value {
	result, err := ctx.Eval("test.js", Code(`
		const obj = {};
		obj.self = obj;
		obj
	`))
	if err != nil {
		panic(err)
	}
	return result
}

func TestNewJsToGoErr(t *testing.T) {
	rt, err := New()
	require.NoError(t, err)
	defer rt.Close()

	ctx := rt.Context()

	t.Run("JSONStringifyFailure", func(t *testing.T) {
		// Create a value that will cause JSONStringify to fail
		circularValue := createCircularValue(ctx)
		defer circularValue.Free()

		// This should hit the JSONStringify error path and use fallback
		result := newJsToGoErr(circularValue, errors.New("test error"))
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "test error")
		// Should use String() as fallback when JSONStringify fails
		assert.Contains(t, result.Error(), "[object Object]")
	})

	t.Run("JSONStringifySuccess", func(t *testing.T) {
		value := ctx.NewString("hello")
		defer value.Free()

		result := newJsToGoErr(value, errors.New("test error"))
		assert.Error(t, result)
		assert.Contains(t, result.Error(), `"hello"`)
		assert.Contains(t, result.Error(), "test error")
	})
}

func TestNewInvalidJsInputErr_JSONStringifyFailure(t *testing.T) {
	rt, err := New()
	require.NoError(t, err)
	defer rt.Close()

	// Create a Value that will fail JSONStringify (circular reference)
	circularValue, err := rt.Eval("test.js", Code(`
		const obj = {};
		obj.self = obj; // circular reference
		obj
	`))
	require.NoError(t, err)
	defer circularValue.Free()

	// This should trigger the JSONStringify failure path in newInvalidJsInputErr
	result := newInvalidJsInputErr("array", circularValue)

	assert.Error(t, result)
	assert.Contains(t, result.Error(), "expected JS array")
	assert.Contains(t, result.Error(), "JSONStringify failed:")
	assert.Contains(t, result.Error(), "[object Object]") // Fallback String() representation
}

func TestNewInvalidJsInputErr_SuccessfulJSONStringify(t *testing.T) {
	rt, err := New()
	require.NoError(t, err)
	defer rt.Close()

	ctx := rt.Context()

	// Create a simple value that JSONStringify will work on
	value := ctx.NewString("test string")
	defer value.Free()

	result := newInvalidJsInputErr("number", value)

	assert.Error(t, result)
	assert.Contains(t, result.Error(), "expected JS number")
	assert.Contains(t, result.Error(), `"test string"`) // Successful JSONStringify
	assert.NotContains(t, result.Error(), "JSONStringify failed")
}
