package qjs_test

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTimezone(t *testing.T) {
	t.Run("IANA_timezone_names", func(t *testing.T) {
		// Test valid IANA timezone names
		testCases := []struct {
			name     string
			timezone string
			expected string
		}{
			{"UTC", "UTC", "UTC"},
			{"America_New_York", "America/New_York", "America/New_York"},
			{"Asia_Tokyo", "Asia/Tokyo", "Asia/Tokyo"},
			{"Europe_London", "Europe/London", "Europe/London"},
			{"Australia_Sydney", "Australia/Sydney", "Australia/Sydney"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := qjs.ParseTimezone(tc.timezone)
				assert.NotNil(t, result)
				assert.Equal(t, tc.expected, result.String())
			})
		}
	})

	t.Run("UTC_offset_formats", func(t *testing.T) {
		testCases := []struct {
			name           string
			timezone       string
			expectedName   string
			expectedOffset int // in seconds
		}{
			// 5-character format with colon (+HH:MM)
			{"positive_offset_colon_format", "+05:30", "+05:30", 5*3600 + 30*60},
			{"negative_offset_colon_format", "-08:00", "-08:00", -(8 * 3600)},
			{"zero_offset_colon_format", "+00:00", "+00:00", 0},
			{"max_positive_colon", "+23:59", "+23:59", 23*3600 + 59*60},
			{"max_negative_colon", "-23:59", "-23:59", -(23*3600 + 59*60)},

			// 4-character format without colon (+HHMM)
			{"positive_offset_no_colon", "+0530", "+0530", 5*3600 + 30*60},
			{"negative_offset_no_colon", "-0800", "-0800", -(8 * 3600)},
			{"zero_offset_no_colon", "+0000", "+0000", 0},
			{"max_positive_no_colon", "+2359", "+2359", 23*3600 + 59*60},
			{"max_negative_no_colon", "-2359", "-2359", -(23*3600 + 59*60)},

			// 2-character format hours only (+HH)
			{"positive_hours_only", "+05", "+05", 5 * 3600},
			{"negative_hours_only", "-08", "-08", -(8 * 3600)},
			{"zero_hours_only", "+00", "+00", 0},
			{"max_positive_hours", "+23", "+23", 23 * 3600},
			{"max_negative_hours", "-23", "-23", -(23 * 3600)},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := qjs.ParseTimezone(tc.timezone)
				assert.NotNil(t, result)
				assert.Equal(t, tc.expectedName, result.String())

				// Test offset by checking time conversion
				testTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
				convertedTime := testTime.In(result)
				_, offset := convertedTime.Zone()
				assert.Equal(t, tc.expectedOffset, offset)
			})
		}
	})

	t.Run("unusual_but_valid_offset_formats", func(t *testing.T) {
		testCases := []struct {
			name     string
			timezone string
			expected string
		}{
			{"extra_characters_after_valid", "+05:30extra", "+05:30extra"},
			{"letters_that_parse_to_zero", "+ab:cd", "+ab:cd"}, // parses as 0:0
			{"incomplete_colon_format", "+05:", "+05:"},        // parses as 5:0
			{"invalid_colon_position", "+0:530", "+0:530"},     // parses as 0:530, but 530 > 59 so might be invalid
			{"three_digit_format", "+123", "+123"},             // treated as unknown format, parses as 0:0
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := qjs.ParseTimezone(tc.timezone)
				assert.NotNil(t, result)
				assert.Equal(t, tc.expected, result.String())
			})
		}
	})

	t.Run("invalid_UTC_offset_formats_fallback_to_UTC", func(t *testing.T) {
		// These are the actual conditions that cause fallback to UTC:
		// 1. Length < 3
		// 2. Doesn't start with + or -
		// 3. Hours > 23 or Minutes > 59 (after parsing)
		testCases := []struct {
			name     string
			timezone string
			reason   string
		}{
			// Length < 3 conditions
			{"too_short_with_sign", "+1", "length < 3"},
			{"too_short_with_minus", "-", "length < 3"},

			// No + or - prefix
			{"no_sign_prefix", "0530", "doesn't start with + or -"},
			{"invalid_sign", "*0530", "invalid sign character"},

			// These should actually fallback due to validation failure
			{"invalid_minutes_over_59_colon", "+05:60", "minutes > 59"},
			{"invalid_minutes_over_59_no_colon", "+0560", "minutes > 59"},
			{"invalid_hours_over_23_colon", "+24:00", "hours > 23"},
			{"invalid_hours_over_23_no_colon", "+2400", "hours > 23"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := qjs.ParseTimezone(tc.timezone)
				assert.NotNil(t, result)
				assert.Equal(t, "UTC", result.String(), "Should fallback to UTC for: %s", tc.reason)
			})
		}
	})
}

// Test struct types for FieldMapper concurrency testing
type ConcurrencyTestStruct struct {
	Name   string
	Value  int
	Active bool
}

type ComplexConcurrencyStruct struct {
	ID          int            `json:"id"`
	Data        map[string]any `json:"data"`
	Timestamp   time.Time      `json:"timestamp"`
	PrivateData string         `json:"-"`
	Nested      struct {
		Field1 string `json:"field1"`
		Field2 int    `json:"field2"`
	} `json:"nested"`
}

type EmbeddedConcurrencyStruct struct {
	ConcurrencyTestStruct
	ExtraField string
}

// TestFieldMapperDoubleCheckLocking tests the double-check locking pattern.
func TestFieldMapperDoubleCheckLocking(t *testing.T) {
	const numAttempts = 100
	for range numAttempts {
		fm := qjs.NewFieldMapper()
		structType := reflect.TypeOf(ConcurrencyTestStruct{})

		const numGoroutines = 200 // Very high contention
		var wg sync.WaitGroup
		var startBarrier sync.WaitGroup
		var results sync.Map

		startBarrier.Add(1)
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func(id int) {
				defer wg.Done()

				// All goroutines wait at the barrier
				startBarrier.Wait()
				// Immediate call to GetFieldMap to maximize race condition
				fieldMap := fm.GetFieldMap(structType)
				// Store result for verification
				results.Store(id, len(fieldMap))
			}(i)
		}

		// Release all goroutines at exactly the same time
		startBarrier.Done()
		wg.Wait()

		// Verify all results are consistent
		var count int
		results.Range(func(key, value any) bool {
			count++
			assert.Equal(t, 3, value.(int), "All goroutines should get same field count")
			return true
		})

		assert.Equal(t, numGoroutines, count, "Should have results from all goroutines")
	}
}

func TestJsNumericToGoConverterErrorHandling(t *testing.T) {
	testCases := []struct {
		name       string
		targetType reflect.Type
		floatVal   float64
		expectErr  bool
		errMsg     string
	}{
		// Valid numeric types (should not error)
		{
			name:       "valid_int_type",
			targetType: reflect.TypeOf(int(0)),
			floatVal:   42.0,
			expectErr:  false,
		},
		{
			name:       "valid_float64_type",
			targetType: reflect.TypeOf(float64(0)),
			floatVal:   3.14,
			expectErr:  false,
		},

		// Invalid types that will trigger FloatToInt error
		{
			name:       "string_type_error",
			targetType: reflect.TypeOf(""),
			floatVal:   42.0,
			expectErr:  true,
			errMsg:     "unsupported numeric type: string",
		},
		{
			name:       "bool_type_error",
			targetType: reflect.TypeOf(true),
			floatVal:   1.0,
			expectErr:  true,
			errMsg:     "unsupported numeric type: bool",
		},
		{
			name:       "slice_type_error",
			targetType: reflect.TypeOf([]int{}),
			floatVal:   42.0,
			expectErr:  true,
			errMsg:     "unsupported numeric type: slice",
		},
		{
			name:       "map_type_error",
			targetType: reflect.TypeOf(map[string]int{}),
			floatVal:   42.0,
			expectErr:  true,
			errMsg:     "unsupported numeric type: map",
		},
		{
			name:       "struct_type_error",
			targetType: reflect.TypeOf(struct{}{}),
			floatVal:   42.0,
			expectErr:  true,
			errMsg:     "unsupported numeric type: struct",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			converter := qjs.NewJsNumericToGoConverter(tc.targetType)
			result, err := converter.Convert(tc.floatVal)

			if tc.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

// TestJsArrayToGoConverterErrorWrapping tests that errors are properly wrapped with newJsToGoErr
func TestJsArrayToGoConverterErrorWrapping(t *testing.T) {
	rt := must(qjs.New())
	defer rt.Close()

	// Create a non-array value
	jsValue, err := rt.Eval("test.js", qjs.Code("123"))
	assert.NoError(t, err)
	defer jsValue.Free()

	converter := qjs.NewJsArrayToGoConverter[[]int](jsValue)
	_, err = converter.Convert()

	assert.Error(t, err)

	// Check error wrapping structure from newJsToGoErr
	assert.Contains(t, err.Error(), "cannot convert JS")
	assert.Contains(t, err.Error(), "Array")
	assert.Contains(t, err.Error(), "expected JS array, got Number=123")
}

// TestJsArrayToGoConverterJSONStringifyError tests the JSONStringify error.
func TestJsArrayToGoConverterJSONStringifyError(t *testing.T) {
	rt := must(qjs.New())
	defer rt.Close()

	// Create a JS array with circular reference that will fail JSONStringify
	jsValue, err := rt.Eval("test.js", qjs.Code(`
		const obj = {};
		obj.self = obj; // circular reference
		[obj]  // array containing circular reference object
	`))
	assert.NoError(t, err)
	defer jsValue.Free()

	// CustomUnmarshaler is a struct type (not slice/array) so it will trigger convertViaJSON
	type TestStruct struct {
		Value string
	}

	// This should trigger the JSONStringify error path in convertViaJSON
	converter := qjs.NewJsArrayToGoConverter[TestStruct](jsValue)
	_, err = converter.Convert()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "js array:")
}

// TestFloatToIntUintptrConversion tests the specific uintptr case.
func TestFloatToIntUintptrConversion(t *testing.T) {
	testCases := []struct {
		name        string
		floatVal    float64
		targetKind  reflect.Kind
		expected    any
		expectError bool
	}{
		{
			name:       "uintptr_conversion_positive",
			floatVal:   42.0,
			targetKind: reflect.Uintptr,
			expected:   uintptr(42),
		},
		{
			name:       "uintptr_conversion_zero",
			floatVal:   0.0,
			targetKind: reflect.Uintptr,
			expected:   uintptr(0),
		},
		{
			name:       "uintptr_conversion_large_value",
			floatVal:   0xdeadbeef,
			targetKind: reflect.Uintptr,
			expected:   uintptr(0xdeadbeef),
		},
		{
			name:        "unsupported_type",
			floatVal:    42.0,
			targetKind:  reflect.String,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := qjs.FloatToInt(tc.floatVal, tc.targetKind)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported numeric type")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)

				// Verify the type is correct
				assert.Equal(t, tc.targetKind, reflect.TypeOf(result).Kind())
			}
		})
	}
}

func TestStringToNumeric(t *testing.T) {
	t.Run("empty_string_error", func(t *testing.T) {
		result, err := qjs.StringToNumeric("", reflect.TypeOf(int(0)))
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "empty string cannot be converted to number")
	})

	t.Run("pointer_type_error_propagation", func(t *testing.T) {
		ptrType := reflect.TypeOf((*int)(nil))
		result, err := qjs.StringToNumeric("invalid_number", ptrType)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "cannot convert JS string")
	})

	t.Run("unsupported_type_error", func(t *testing.T) {
		result, err := qjs.StringToNumeric("42", reflect.TypeOf("string"))
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "cannot convert JS string")
		assert.Contains(t, err.Error(), "string")
	})

	t.Run("invalid_string_parsing_errors", func(t *testing.T) {
		// Test invalid strings that can't be parsed as numbers
		invalidStrings := []string{
			"not_a_number",
			"42abc",
			"3.14.15",
			"--42",
			"++42",
			"4.2e",
			"abc123",
			"12.34.56",
		}

		targetTypes := []reflect.Type{
			reflect.TypeOf(int(0)),
			reflect.TypeOf(float32(0)),
			reflect.TypeOf(float64(0)),
		}

		for _, invalidStr := range invalidStrings {
			for _, targetType := range targetTypes {
				t.Run(fmt.Sprintf("invalid_%s_to_%s", invalidStr, targetType.Kind()), func(t *testing.T) {
					result, err := qjs.StringToNumeric(invalidStr, targetType)
					assert.Error(t, err)
					assert.Nil(t, result)
					assert.Contains(t, err.Error(), "cannot convert JS string")
				})
			}
		}
	})
}

// TestAnyToError tests the AnyToError function for panic recovery.
func TestAnyToError(t *testing.T) {
	t.Run("nil_input_returns_nil", func(t *testing.T) {
		result := qjs.AnyToError(nil)
		assert.Nil(t, result)
	})

	t.Run("error_input_returns_same_error", func(t *testing.T) {
		originalErr := fmt.Errorf("original error message")
		result := qjs.AnyToError(originalErr)

		assert.NotNil(t, result)
		assert.Equal(t, originalErr, result)
		assert.Same(t, originalErr, result) // Should be the exact same error instance
	})

	t.Run("other_input_wrapped_with_panic_prefix", func(t *testing.T) {
		testCases := []struct {
			name     string
			input    any
			expected string
		}{
			{
				name:     "simple_string",
				input:    "panic message",
				expected: "recovered from panic: panic message",
			},
			{
				name:     "multiline_string",
				input:    "line1\nline2\nline3",
				expected: "recovered from panic: line1\nline2\nline3",
			},
			{
				name:     "integer_value",
				input:    42,
				expected: "recovered from panic: 42",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := qjs.AnyToError(tc.input)

				assert.NotNil(t, result)
				assert.Equal(t, tc.expected, result.Error())
			})
		}
	})
}

func TestCreateGoBindFuncType(t *testing.T) {
	t.Run("valid_functions", func(t *testing.T) {
		t.Run("nil_function_is_valid", func(t *testing.T) {
			var nilFunc func()
			fnType, err := qjs.CreateGoBindFuncType(nilFunc)
			assert.NoError(t, err)
			assert.NotNil(t, fnType)
			assert.Equal(t, reflect.Func, fnType.Kind())
		})

		t.Run("no_return_values", func(t *testing.T) {
			fn := func() {}
			fnType, err := qjs.CreateGoBindFuncType(fn)
			assert.NoError(t, err)
			assert.NotNil(t, fnType)
			assert.Equal(t, reflect.Func, fnType.Kind())
		})

		t.Run("single_return_value", func(t *testing.T) {
			fn := func() string { return "" }
			fnType, err := qjs.CreateGoBindFuncType(fn)
			assert.NoError(t, err)
			assert.NotNil(t, fnType)
			assert.Equal(t, reflect.Func, fnType.Kind())
		})

		t.Run("two_return_values_with_error", func(t *testing.T) {
			fn := func() (string, error) { return "", nil }
			fnType, err := qjs.CreateGoBindFuncType(fn)
			assert.NoError(t, err)
			assert.NotNil(t, fnType)
			assert.Equal(t, reflect.Func, fnType.Kind())
		})

		t.Run("single_error_return", func(t *testing.T) {
			fn := func() error { return nil }
			fnType, err := qjs.CreateGoBindFuncType(fn)
			assert.NoError(t, err)
			assert.NotNil(t, fnType)
			assert.Equal(t, reflect.Func, fnType.Kind())
		})
	})

	t.Run("invalid_samples", func(t *testing.T) {
		t.Run("non_function", func(t *testing.T) {
			_, err := qjs.CreateGoBindFuncType("not a function")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "expected GO type")
			assert.Contains(t, err.Error(), "function")
		})

		t.Run("integer", func(t *testing.T) {
			_, err := qjs.CreateGoBindFuncType(42)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "expected GO type")
			assert.Contains(t, err.Error(), "function")
		})
	})

	t.Run("unsafe_pointer_return", func(t *testing.T) {
		fn := func() unsafe.Pointer { return nil }
		_, err := qjs.CreateGoBindFuncType(fn)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "func return")
	})

	t.Run("unsafe_pointer_param", func(t *testing.T) {
		fn := func(unsafe.Pointer) {}
		_, err := qjs.CreateGoBindFuncType(fn)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parameter 0 error")
		assert.Contains(t, err.Error(), "func param")
	})
}

func TestInvalid32BitValue(t *testing.T) {
	t.Run("reflect_Int", func(t *testing.T) {
		// Within bounds - should not error
		err := qjs.IsValid32BitFloat(0, reflect.Int)
		assert.NoError(t, err)

		err = qjs.IsValid32BitFloat(2147483647, reflect.Int) // math.MaxInt32
		assert.NoError(t, err)

		err = qjs.IsValid32BitFloat(-2147483648, reflect.Int) // math.MinInt32
		assert.NoError(t, err)

		// Overflow - should error
		err = qjs.IsValid32BitFloat(2147483648, reflect.Int) // math.MaxInt32 + 1
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "overflows")

		err = qjs.IsValid32BitFloat(-2147483649, reflect.Int) // math.MinInt32 - 1
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "overflows")
	})

	t.Run("reflect_Uint", func(t *testing.T) {
		// Within bounds - should not error
		err := qjs.IsValid32BitFloat(0, reflect.Uint)
		assert.NoError(t, err)

		err = qjs.IsValid32BitFloat(4294967295, reflect.Uint) // math.MaxUint32
		assert.NoError(t, err)

		// Overflow - should error
		err = qjs.IsValid32BitFloat(-1, reflect.Uint)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "overflows")

		err = qjs.IsValid32BitFloat(4294967296, reflect.Uint) // math.MaxUint32 + 1
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "overflows")
	})

	t.Run("other_kinds_use_uint_bounds", func(t *testing.T) {
		err := qjs.IsValid32BitFloat(1000, reflect.String) // any non-Int kind
		assert.NoError(t, err)

		err = qjs.IsValid32BitFloat(-1, reflect.String) // should fail with uint bounds
		assert.Error(t, err)
	})
}

func createChannelValue(t *testing.T, ctx *qjs.Context) (
	waitChan chan struct{},
	goChan chan int,
	jsChan *qjs.Value,
) {
	waitChan = make(chan struct{}, 1)
	goChan = make(chan int)
	chType := reflect.TypeOf(goChan)
	chValue := reflect.ValueOf(goChan)
	jsChan = must(qjs.ChannelToJSObjectValue(ctx, chType, chValue))
	assert.True(t, jsChan.IsObject())
	ctx.Global().SetPropertyStr("goChan", jsChan)

	return waitChan, goChan, jsChan
}

func TestChannelToJSObjectValue(t *testing.T) {
	rt := must(qjs.New())
	defer rt.Close()
	ctx := rt.Context()

	t.Run("NilChannelHandling", func(t *testing.T) {
		var nilChan chan int
		chType := reflect.TypeOf(nilChan)
		chValue := reflect.ValueOf(nilChan)
		result, err := qjs.ChannelToJSObjectValue(ctx, chType, chValue)
		require.NoError(t, err)
		defer result.Free()
		assert.True(t, result.IsNull())
	})

	t.Run("BidirectionalChannelProperties", func(t *testing.T) {
		ch := make(chan string, 5)
		chRType := reflect.TypeOf(ch)
		chRValue := reflect.ValueOf(ch)
		chValue, err := qjs.ChannelToJSObjectValue(ctx, chRType, chRValue)
		require.NoError(t, err)
		defer chValue.Free()

		assert.True(t, chValue.IsObject())
		ctx.Global().SetPropertyStr("ch", chValue)

		result, err := ctx.Eval("test.js", qjs.Code(`
			([
				ch.type,
				ch.elementType,
				ch.canSend,
				ch.canReceive,
				ch.length(),
				ch.capacity(),
				typeof ch.send,
				typeof ch.receive,
				typeof ch.close,
				typeof ch.length,
				typeof ch.capacity,
			])
		`))

		require.NoError(t, err)
		defer result.Free()
		assert.True(t, result.IsArray())
		assert.Equal(t, int64(11), result.Len())
		resultJson := must(result.JSONStringify())
		assert.Equal(t, "[\"channel\",\"string\",true,true,0,5,\"function\",\"function\",\"function\",\"function\",\"function\"]", resultJson)
	})
}

func TestGoChannelToJs(t *testing.T) {
	t.Run("SendErrorNoReceiverReady", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		_, _, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()
		_, err := ctx.Eval("test.js", qjs.Code(`
			setTimeout(() => { goChan.send(1) }, 100);
		`))
		assert.Error(t, err)
	})

	t.Run("SendErrorBufferFull", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()
		_, goChan, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()
		go func() {
			goChan <- 1
		}()

		// ensure the goroutine has sent the value
		time.Sleep(10 * time.Millisecond)
		_, err := ctx.Eval("test.js", qjs.Code(`
			setTimeout(() => { goChan.send(1) }, 100);
		`))
		assert.Error(t, err)
	})

	t.Run("SendErrorChannelClosed", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()
		_, goChan, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()
		close(goChan)
		_, err := ctx.Eval("test.js", qjs.Code(`
			setTimeout(() => { goChan.send(1) }, 100);
		`))
		assert.Error(t, err)
	})

	t.Run("SendSuccess", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		waitChan, goChan, jsChan := createChannelValue(t, ctx)
		go func() {
			value := <-goChan
			waitChan <- struct{}{}
			assert.Equal(t, 1, value)
		}()

		defer jsChan.Free()
		must(ctx.Eval("test.js", qjs.Code(`
			setTimeout(() => { goChan.send(1) }, 100);
		`)))
		<-waitChan
	})

	t.Run("ReceiveErrorNoData", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		_, _, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()
		_, err := ctx.Eval("test.js", qjs.Code(`
			goChan.receive();
		`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "channel receive would block: buffer empty or no sender ready")
	})

	t.Run("ReceiveErrorClosedChannel", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		_, goChan, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()

		close(goChan)

		_, err := ctx.Eval("test.js", qjs.Code(`
			goChan.receive();
		`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "channel is closed")
	})

	t.Run("ReceiveSuccess", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		_, goChan, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()

		go func() {
			goChan <- 42
		}()

		// ensure the goroutine has sent the value
		time.Sleep(10 * time.Millisecond)
		result := must(ctx.Eval("test.js", qjs.Code(`
			goChan.receive();
		`)))
		defer result.Free()

		assert.True(t, result.IsNumber())
		receivedValue := result.Int32()
		assert.Equal(t, int32(42), receivedValue)
	})

	t.Run("ReceiveMultipleValues", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		goChan := make(chan int, 3)
		chType := reflect.TypeOf(goChan)
		chValue := reflect.ValueOf(goChan)
		jsChan := must(qjs.ChannelToJSObjectValue(ctx, chType, chValue))
		defer jsChan.Free()
		assert.True(t, jsChan.IsObject())
		ctx.Global().SetPropertyStr("goChan", jsChan)

		for i := 1; i <= 3; i++ {
			goChan <- i * 10
		}

		for i := 1; i <= 3; i++ {
			result := must(ctx.Eval("test.js", qjs.Code(`
				goChan.receive();
			`)))
			assert.True(t, result.IsNumber())
			receivedValue := result.Int32()
			assert.Equal(t, int32(i*10), receivedValue)
			result.Free()
		}

		// Next receive should block (no more data)
		_, err := ctx.Eval("test.js", qjs.Code(`
			goChan.receive();
		`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "channel receive would block: buffer empty or no sender ready")
	})

	t.Run("ReceiveFromBufferedChannel", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		bufferedChan := make(chan int, 3)
		chType := reflect.TypeOf(bufferedChan)
		chValue := reflect.ValueOf(bufferedChan)
		jsChan := must(qjs.ChannelToJSObjectValue(ctx, chType, chValue))
		defer jsChan.Free()
		ctx.Global().SetPropertyStr("bufferedChan", jsChan)

		bufferedChan <- 100
		bufferedChan <- 200
		bufferedChan <- 300

		for i, expected := range []int{100, 200, 300} {
			result := must(ctx.Eval("test.js", qjs.Code(`
				bufferedChan.receive();
			`)))
			assert.True(t, result.IsNumber(), "Result %d should be a number", i)
			receivedValue := result.Int32()
			assert.Equal(t, int32(expected), receivedValue, "Received value %d should match expected", i)
			result.Free()
		}

		// Next receive should block since buffer is empty
		_, err := ctx.Eval("test.js", qjs.Code(`
			bufferedChan.receive();
		`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "channel receive would block: buffer empty or no sender ready")
	})

	t.Run("CloseSuccessBidirectionalChannel", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		_, _, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()

		result := must(ctx.Eval("test.js", qjs.Code(`
			goChan.close();
		`)))
		defer result.Free()

		assert.True(t, result.IsUndefined() || result.IsNull())
	})

	t.Run("CloseSuccessSendOnlyChannel", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		bidirectionalChan := make(chan int)
		sendOnlyChan := (chan<- int)(bidirectionalChan)

		chType := reflect.TypeOf(sendOnlyChan)
		chValue := reflect.ValueOf(sendOnlyChan)
		jsChan := must(qjs.ChannelToJSObjectValue(ctx, chType, chValue))
		defer jsChan.Free()
		assert.True(t, jsChan.IsObject())
		ctx.Global().SetPropertyStr("sendOnlyChan", jsChan)

		result := must(ctx.Eval("test.js", qjs.Code(`
			sendOnlyChan.close();
		`)))
		defer result.Free()

		assert.True(t, result.IsUndefined() || result.IsNull())
	})

	t.Run("CloseErrorReceiveOnlyChannel", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		bidirectionalChan := make(chan int)
		receiveOnlyChan := (<-chan int)(bidirectionalChan)

		chType := reflect.TypeOf(receiveOnlyChan)
		chValue := reflect.ValueOf(receiveOnlyChan)
		jsChan := must(qjs.ChannelToJSObjectValue(ctx, chType, chValue))
		defer jsChan.Free()
		assert.True(t, jsChan.IsObject())
		ctx.Global().SetPropertyStr("receiveOnlyChan", jsChan)

		_, err := ctx.Eval("test.js", qjs.Code(`
			receiveOnlyChan.close();
		`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot close receive-only channel")
	})

	t.Run("CloseChannelTwice", func(t *testing.T) {
		rt := must(qjs.New())
		defer rt.Close()
		ctx := rt.Context()

		_, _, jsChan := createChannelValue(t, ctx)
		defer jsChan.Free()

		result1 := must(ctx.Eval("test.js", qjs.Code(`
			goChan.close();
		`)))
		defer result1.Free()
		assert.True(t, result1.IsUndefined() || result1.IsNull())

		// Second close should fail
		_, err := ctx.Eval("test.js", qjs.Code(`
			goChan.close();
		`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "close of closed channel")
	})
}
