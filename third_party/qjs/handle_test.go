package qjs_test

import (
	"math"
	"testing"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
)

func TestHandle_Basic(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Raw", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		assert.Equal(t, uint64(42), handle.Raw())
	})

	t.Run("Free", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		// Test that Free() is safe to call and doesn't panic
		handle.Free()
		// Test that double free is safe and doesn't cause issues
		handle.Free()
	})
}

func TestHandle_BoolConversion(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	tests := []struct {
		name     string
		value    uint64
		expected bool
	}{
		{"Zero", 0, false},
		{"One", 1, true},
		{"MaxUint64", math.MaxUint64, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handle := qjs.NewHandle(runtime, test.value)
			assert.Equal(t, test.expected, handle.Bool())
		})
	}
}

func TestHandle_IntegerConversions(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Int", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		assert.Equal(t, int(42), handle.Int())
	})

	t.Run("Int8", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 127)
		assert.Equal(t, int8(127), handle.Int8())
	})

	t.Run("Int16", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 32767)
		assert.Equal(t, int16(32767), handle.Int16())
	})

	t.Run("Int32", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 2147483647)
		assert.Equal(t, int32(2147483647), handle.Int32())
	})

	t.Run("Int64", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 9223372036854775807)
		assert.Equal(t, int64(9223372036854775807), handle.Int64())
	})
}

func TestHandle_UintegerConversions(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Uint", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		assert.Equal(t, uint(42), handle.Uint())
	})

	t.Run("Uint8", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 255)
		assert.Equal(t, uint8(255), handle.Uint8())
	})

	t.Run("Uint16", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 65535)
		assert.Equal(t, uint16(65535), handle.Uint16())
	})

	t.Run("Uint32", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 4294967295)
		assert.Equal(t, uint32(4294967295), handle.Uint32())
	})

	t.Run("Uint64", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 18446744073709551615)
		assert.Equal(t, uint64(18446744073709551615), handle.Uint64())
	})

	t.Run("Uintptr", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		assert.Equal(t, uintptr(42), handle.Uintptr())
	})
}

func TestHandle_FloatConversions(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Float32", func(t *testing.T) {
		// Test IEEE 754 bit pattern interpretation for float32
		bits := math.Float32bits(3.14159)
		handle := qjs.NewHandle(runtime, uint64(bits))
		assert.InDelta(t, float32(3.14159), handle.Float32(), 0.0001)
	})

	t.Run("Float64", func(t *testing.T) {
		// Test IEEE 754 bit pattern interpretation for float64
		bits := math.Float64bits(3.14159265359)
		handle := qjs.NewHandle(runtime, bits)
		assert.InDelta(t, 3.14159265359, handle.Float64(), 0.0000000001)
	})
}

func TestHandle_String(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	v := runtime.Context().NewString("hello world")
	v2 := runtime.Context().Call(
		"QJS_ToCString",
		runtime.Context().Raw(),
		v.Raw(),
	)

	assert.Equal(t, "hello world", v2.Handle().String())
}

func TestHandle_Bytes(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("empty_handle", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 0)
		bytes := handle.Bytes()

		assert.Empty(t, bytes)
		assert.Nil(t, bytes)
	})

	t.Run("non_empty_handle", func(t *testing.T) {
		value, err := runtime.Context().Eval("test.js", qjs.Code(`({
			hello: "hello world",
		})`))
		assert.NoError(t, err)
		json := runtime.Context().Call(
			"QJS_JSONStringify",
			runtime.Context().Raw(),
			value.Raw(),
		)
		bytes := json.Handle().Bytes()

		assert.NotEmpty(t, bytes)
		assert.JSONEq(t, `{"hello":"hello world"}`, string(bytes))
	})
}

// Test coverage for uncovered areas in handle.go
func TestHandle_NilChecks(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("NewHandle_NilRuntime", func(t *testing.T) {
		assert.Panics(t, func() {
			qjs.NewHandle(nil, 42)
		}, "NewHandle should panic with nil runtime")
	})

	t.Run("Free_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		// Should not panic
		handle.Free()
	})

	t.Run("IsFreed_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.True(t, handle.IsFreed(), "nil handle should be considered freed")
	})

	t.Run("Raw_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.Equal(t, uint64(0), handle.Raw(), "nil handle should return 0")
	})

	t.Run("Bool_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.False(t, handle.Bool(), "nil handle should return false")
	})

	t.Run("Float32_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.Equal(t, float32(0.0), handle.Float32(), "nil handle should return 0.0")
	})

	t.Run("Float64_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.Equal(t, 0.0, handle.Float64(), "nil handle should return 0.0")
	})

	t.Run("String_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.Equal(t, "", handle.String(), "nil handle should return empty string")
	})

	t.Run("Bytes_NilHandle", func(t *testing.T) {
		var handle *qjs.Handle
		assert.Nil(t, handle.Bytes(), "nil handle should return nil bytes")
	})
}

func TestHandle_FreedChecks(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("ConversionsAfterFree", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		handle.Free()

		// All conversions should return zero values after free
		assert.Equal(t, uint64(0), handle.Raw())
		assert.False(t, handle.Bool())
		assert.Equal(t, int(0), handle.Int())
		assert.Equal(t, int8(0), handle.Int8())
		assert.Equal(t, int16(0), handle.Int16())
		assert.Equal(t, int32(0), handle.Int32())
		assert.Equal(t, int64(0), handle.Int64())
		assert.Equal(t, uint(0), handle.Uint())
		assert.Equal(t, uint8(0), handle.Uint8())
		assert.Equal(t, uint16(0), handle.Uint16())
		assert.Equal(t, uint32(0), handle.Uint32())
		assert.Equal(t, uint64(0), handle.Uint64())
		assert.Equal(t, uintptr(0), handle.Uintptr())
		assert.Equal(t, float32(0.0), handle.Float32())
		assert.Equal(t, 0.0, handle.Float64())
		assert.Equal(t, "", handle.String())
		assert.Nil(t, handle.Bytes())
	})
}

func TestHandle_OverflowChecks(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Uint8_Overflow", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, math.MaxUint64) // Value too large for uint8
		assert.Panics(t, func() {
			handle.Uint8()
		}, "Should panic on uint8 overflow")
	})

	t.Run("Uint16_Overflow", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, math.MaxUint64) // Value too large for uint16
		assert.Panics(t, func() {
			handle.Uint16()
		}, "Should panic on uint16 overflow")
	})

	t.Run("Uint32_Overflow", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, math.MaxUint64) // Value too large for uint32
		assert.Panics(t, func() {
			handle.Uint32()
		}, "Should panic on uint32 overflow")
	})
}

func TestHandle_StringExceptionHandling(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("String_ZeroRawValue", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 0)
		// Should return empty string without panic when no exception
		assert.Equal(t, "", handle.String())
	})
}

func TestHandle_SignExtension(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Int8_SignExtension", func(t *testing.T) {
		// Test negative value sign extension for int8
		handle := qjs.NewHandle(runtime, 0xFF) // 255 as uint, should be -1 as int8
		assert.Equal(t, int8(-1), handle.Int8())
	})

	t.Run("Int16_SignExtension", func(t *testing.T) {
		// Test negative value sign extension for int16
		handle := qjs.NewHandle(runtime, 0xFFFF) // 65535 as uint, should be -1 as int16
		assert.Equal(t, int16(-1), handle.Int16())
	})

	t.Run("Int32_SignExtension", func(t *testing.T) {
		// Test negative value sign extension for int32
		handle := qjs.NewHandle(runtime, 0xFFFFFFFF) // MaxUint32 as uint, should be -1 as int32
		assert.Equal(t, int32(-1), handle.Int32())
	})
}

func TestHandle_FreeWithNilRuntime(t *testing.T) {
	t.Run("Free_WithNilRuntime", func(t *testing.T) {
		handle := &qjs.Handle{} // Handle with nil runtime
		// Should not panic
		handle.Free()
	})
}

func TestHandle_BytesFreeHandle(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	t.Run("Bytes_FreedHandle", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		handle.Free()
		bytes := handle.Bytes()
		assert.Nil(t, bytes, "freed handle should return nil bytes")
	})
}

func TestHandle_CustomSignedTypes(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()

	// Custom signed integer types that will hit the default case in ConvertToSigned
	type MyInt int
	type MyInt8 int8
	type MyInt16 int16
	type MyInt32 int32
	type MyInt64 int64

	t.Run("CustomInt", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 42)
		result := qjs.ConvertToSigned[MyInt](handle)
		assert.Equal(t, MyInt(42), result)
	})

	t.Run("CustomInt8", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 100)
		result := qjs.ConvertToSigned[MyInt8](handle)
		assert.Equal(t, MyInt8(100), result)
	})

	t.Run("CustomInt16", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 1000)
		result := qjs.ConvertToSigned[MyInt16](handle)
		assert.Equal(t, MyInt16(1000), result)
	})

	t.Run("CustomInt32", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 100000)
		result := qjs.ConvertToSigned[MyInt32](handle)
		assert.Equal(t, MyInt32(100000), result)
	})

	t.Run("CustomInt64", func(t *testing.T) {
		handle := qjs.NewHandle(runtime, 1000000000)
		result := qjs.ConvertToSigned[MyInt64](handle)
		assert.Equal(t, MyInt64(1000000000), result)
	})
}
