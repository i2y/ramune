package qjs_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test data structures for struct conversion tests
type SimpleStruct struct {
	Name string
	Age  int
}

type StructWithMethods struct {
	Value int
}

func (s StructWithMethods) GetValue() int {
	return s.Value
}

func (s StructWithMethods) GetDoubleValue() int {
	return s.Value * 2
}

type StructWithPointerMethods struct {
	Value int
}

func (s StructWithPointerMethods) GetValue() int {
	return s.Value
}

func (s *StructWithPointerMethods) SetValue(v int) {
	s.Value = v
}

func (s *StructWithPointerMethods) GetValuePlusOne() int {
	return s.Value + 1
}

type StructWithJSONTags struct {
	PublicField    string `json:"public_field"`
	RenamedField   int    `json:"renamed"`
	OmittedField   string `json:"-"`
	DefaultField   bool
	EmptyTagField  string `json:""`
	CommaOnlyField string `json:","`
}

type EmbeddedStructTest struct {
	EmbeddedStruct
	NewField string
}

type EmbeddedPointerStructTest struct {
	*EmbeddedStruct
	ExtraField string
}

type EmbeddedStructWithUnsupportedField struct {
	Name     string
	BadField unsafe.Pointer
}

type EmbeddedPointerWithErrorTest struct {
	*EmbeddedStructWithUnsupportedField
	ExtraField string
}

type CustomInt int
type CustomUint uint
type CustomUint64 uint64
type CustomFloat32 float32
type CustomBool bool
type CustomString string
type CustomIntPtr int

func (c CustomInt) GetValue() int {
	return int(c)
}

func (c CustomString) String() string {
	return string(c)
}

type StructWithEmbeddedPrimitive struct {
	CustomInt
	CustomUint
	CustomUint64
	CustomFloat32
	CustomBool
	CustomString
	*CustomIntPtr
	ExtraField string
}

type AliasInt = int
type AliasString = string
type DefInt int
type DefString string

func (d DefInt) GetValue() int {
	return int(d)
}

func (d DefString) Upper() string {
	return strings.ToUpper(string(d))
}

type StructWithAliasEmbedded struct {
	AliasInt
	AliasString
	ExtraField string
}

type StructWithDefEmbedded struct {
	DefInt
	DefString
	ExtraField string
}

type StructWithInvalidMethod struct {
	Value int
}

// InvalidMethod has an unsupported signature that will cause FuncToJS to fail
func (s StructWithInvalidMethod) InvalidMethod() unsafe.Pointer {
	return unsafe.Pointer(&[]byte{1}[0]) // unsafe.Pointer is not supported by FuncToJS
}

type NestedLevel struct {
	Level int
	Data  map[string]any
	Next  *NestedLevel
}

func createNestedStructure(depth int) any {
	root := &NestedLevel{
		Level: 0,
		Data:  map[string]any{"key0": "value0"},
	}

	current := root
	for i := 1; i < depth; i++ {
		current.Next = &NestedLevel{
			Level: i,
			Data:  map[string]any{fmt.Sprintf("key%d", i): fmt.Sprintf("value%d", i)},
		}
		current = current.Next
	}

	return root
}

func testValueConversion(t *testing.T, ctx *qjs.Context, input any, validator func(*qjs.Value)) {
	result, err := qjs.ToJsValue(ctx, input)
	require.NoError(t, err, "ToJSValue should not return error for input %T: %v", input, input)
	require.NotNil(t, result, "ToJSValue should not return nil for input %T: %v", input, input)
	defer result.Free()
	validator(result)
}

func testErrorCase(t *testing.T, ctx *qjs.Context, input any, expectedErrorSubstring string) {
	result, err := qjs.ToJsValue(ctx, input)
	if result != nil {
		result.Free()
	}
	require.Error(t, err, "ToJSValue should return error for input %T: %v", input, input)
	if expectedErrorSubstring != "" {
		assert.Contains(t, err.Error(), expectedErrorSubstring)
	}
}

func testNumberValue(t *testing.T, ctx *qjs.Context, input any, expected int64) {
	testValueConversion(t, ctx, input, func(result *qjs.Value) {
		assert.True(t, result.IsNumber(), "Expected number for input %T: %v", input, input)
		assert.Equal(t, expected, result.Int64(), "Number value mismatch for input %T: %v", input, input)
	})
}

func testFloatValue(t *testing.T, ctx *qjs.Context, input any, expected float64, tolerance float64) {
	testValueConversion(t, ctx, input, func(result *qjs.Value) {
		assert.True(t, result.IsNumber(), "Expected number for input %T: %v", input, input)
		actual := result.Float64()
		if math.IsNaN(expected) {
			assert.True(t, math.IsNaN(actual), "Expected NaN for input %T: %v", input, input)
		} else if math.IsInf(expected, 0) {
			assert.True(t, math.IsInf(actual, int(math.Copysign(1, expected))), "Expected infinity for input %T: %v", input, input)
		} else {
			assert.InDelta(t, expected, actual, tolerance, "Float value mismatch for input %T: %v", input, input)
		}
	})
}

func TestJSValueConversion(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()
	ctx := runtime.Context()

	t.Run("NumericTypes", func(t *testing.T) {
		t.Run("SignedIntegers", func(t *testing.T) {
			// Consolidated test for all signed integer types
			integerTests := []struct {
				name   string
				values any
			}{
				{"Int8", []int8{0, 1, -1, 42, -42, math.MaxInt8, math.MinInt8}},
				{"Int16", []int16{0, 1, -1, 42, -42, math.MaxInt16, math.MinInt16}},
				{"Int", []int{0, 1, -1, 42, -42}},
				{"Int64Standard", []int64{0, 1, -1, 42, -42, 1<<53 - 1, -(1<<53 - 1)}},
			}

			for _, test := range integerTests {
				t.Run(test.name, func(t *testing.T) {
					switch values := test.values.(type) {
					case []int8:
						for _, v := range values {
							testNumberValue(t, ctx, v, int64(v))
						}
					case []int16:
						for _, v := range values {
							testNumberValue(t, ctx, v, int64(v))
						}
					case []int:
						for _, v := range values {
							testNumberValue(t, ctx, v, int64(v))
						}
					case []int64:
						for _, v := range values {
							testNumberValue(t, ctx, v, v)
						}
					}
				})
			}

			t.Run("ByteRuneComplexTypes", func(t *testing.T) {
				jsValue, err := qjs.ToJsValue(runtime.Context(), []byte("hello world"))
				require.NoError(t, err)
				defer jsValue.Free()

				assert.NotNil(t, jsValue)
				assert.True(t, jsValue.IsByteArray())
			})

			// Special handling for int32 (uses Int32() method)
			t.Run("Int32", func(t *testing.T) {
				testValues := []int32{0, 1, -1, 42, -42, math.MaxInt32, math.MinInt32}
				for _, v := range testValues {
					testValueConversion(t, ctx, v, func(result *qjs.Value) {
						assert.True(t, result.IsNumber())
						assert.Equal(t, v, result.Int32())
					})
				}
			})

			t.Run("Uintptr", func(t *testing.T) {
				val := qjs.GoNumberToJs[uintptr](ctx, 42)
				assert.True(t, val.IsNumber(), "Uintptr should be converted to number")
				retrieved := val.Int64()
				assert.Equal(t, int64(42), retrieved, "Uintptr value should match")
			})

			// Test precision limits with very large int64 values
			t.Run("Int64LargeValues", func(t *testing.T) {
				largeValues := []int64{math.MaxInt64, math.MinInt64}
				for _, v := range largeValues {
					testValueConversion(t, ctx, v, func(result *qjs.Value) {
						assert.True(t, result.IsNumber())
						retrieved := result.Int64()
						assert.NotEqual(t, 0, retrieved, "Large int64 should not become 0")
					})
				}
			})

			t.Run("MaxUint64", func(t *testing.T) {
				val := qjs.GoNumberToJs[uint64](ctx, math.MaxUint64)
				assert.True(t, val.IsBigInt())
				retrieved := val.BigInt().Uint64()
				var expected uint64 = math.MaxUint64
				assert.Equal(t, expected, retrieved, "MaxUint64 should match")
			})
		})

		t.Run("UnsignedIntegers", func(t *testing.T) {
			// Consolidated test for unsigned integer types
			unsignedTests := []struct {
				name   string
				values any
			}{
				{"Uint8", []uint8{0, 1, 42, math.MaxUint8}},
				{"Uint16", []uint16{0, 1, 42, math.MaxUint16}},
				{"Uint", []uint{0, 1, 42}},
			}

			for _, test := range unsignedTests {
				t.Run(test.name, func(t *testing.T) {
					switch values := test.values.(type) {
					case []uint8:
						for _, v := range values {
							testNumberValue(t, ctx, v, int64(v))
						}
					case []uint16:
						for _, v := range values {
							testNumberValue(t, ctx, v, int64(v))
						}
					case []uint:
						for _, v := range values {
							testNumberValue(t, ctx, v, int64(v))
						}
					}
				})
			}

			// Special handling for uint32 (uses Uint32() method)
			t.Run("Uint32", func(t *testing.T) {
				testValues := []uint32{0, 1, 42, math.MaxUint32}
				for _, v := range testValues {
					testValueConversion(t, ctx, v, func(result *qjs.Value) {
						assert.True(t, result.IsNumber())
						assert.Equal(t, v, result.Uint32())
					})
				}
			})

			t.Run("Uint64", func(t *testing.T) {
				// Values that fit safely in int64 range
				t.Run("SmallValues", func(t *testing.T) {
					smallValues := []uint64{0, 1, 42, 1<<53 - 1}
					for _, v := range smallValues {
						testNumberValue(t, ctx, v, int64(v))
					}
				})

				// Large values requiring float64 representation
				t.Run("LargeValues", func(t *testing.T) {
					largeValues := []uint64{uint64(math.MaxInt64) + 1, math.MaxUint64}
					for _, v := range largeValues {
						testValueConversion(t, ctx, v, func(result *qjs.Value) {
							assert.True(t, result.IsNumber())
							retrieved := result.Float64()
							assert.NotEqual(t, 0.0, retrieved, "Large uint64 should not become 0")
						})
					}
				})
			})

			t.Run("Uintptr", func(t *testing.T) {
				testValues := []uintptr{0, 1, 42, 0xdeadbeef}
				for _, v := range testValues {
					testNumberValue(t, ctx, v, int64(v))
				}
			})
		})

		t.Run("FloatingPoint", func(t *testing.T) {
			t.Run("Float32", func(t *testing.T) {
				testValues := []float32{0.0, 1.0, -1.0, 42.5, -42.5, math.MaxFloat32, math.SmallestNonzeroFloat32}
				for _, v := range testValues {
					testFloatValue(t, ctx, v, float64(v), 1e-6)
				}
			})

			t.Run("Float64", func(t *testing.T) {
				testValues := []float64{0.0, 1.0, -1.0, 42.5, -42.5, math.MaxFloat64, math.SmallestNonzeroFloat64}
				for _, v := range testValues {
					testFloatValue(t, ctx, v, v, 1e-15)
				}
			})

			t.Run("SpecialFloatingPointValues", func(t *testing.T) {
				specialValues := []struct {
					name  string
					value float64
				}{
					{"PositiveInfinity", math.Inf(1)},
					{"NegativeInfinity", math.Inf(-1)},
					{"NaN", math.NaN()},
					{"NegativeZero", math.Copysign(0, -1)},
				}

				for _, test := range specialValues {
					t.Run(test.name, func(t *testing.T) {
						testFloatValue(t, ctx, test.value, test.value, 0)
					})
				}
			})
		})

		t.Run("ComplexNumbers", func(t *testing.T) {
			complexTests := []struct {
				name  string
				input any
				real  float64
				imag  float64
			}{
				{"Complex64Zero", complex64(0), 0, 0},
				{"Complex64Simple", complex64(3 + 4i), 3, 4},
				{"Complex128Zero", complex128(0), 0, 0},
				{"Complex128Simple", complex128(3.14 + 2.71i), 3.14, 2.71},
			}

			for _, test := range complexTests {
				t.Run(test.name, func(t *testing.T) {
					testValueConversion(t, ctx, test.input, func(result *qjs.Value) {
						assert.True(t, result.IsObject(), "Complex number should be object")
						obj := result.Object()
						defer obj.Free()

						realProp := obj.GetPropertyStr("real")
						defer realProp.Free()
						assert.True(t, realProp.IsNumber(), "Real part should be number")
						assert.InDelta(t, test.real, realProp.Float64(), 1e-10)

						imagProp := obj.GetPropertyStr("imag")
						defer imagProp.Free()
						assert.True(t, imagProp.IsNumber(), "Imaginary part should be number")
						assert.InDelta(t, test.imag, imagProp.Float64(), 1e-10)
					})
				})
			}
		})
	})

	t.Run("SpecializedTypes", func(t *testing.T) {
		t.Run("TimeValues", func(t *testing.T) {
			now := time.Now()
			epoch := time.Unix(0, 0)

			t.Run("TimeByValue", func(t *testing.T) {
				testValueConversion(t, ctx, now, func(result *qjs.Value) {
					assert.True(t, result.IsDate(), "Time should convert to Date")
				})
			})

			t.Run("TimeByPointer", func(t *testing.T) {
				testValueConversion(t, ctx, &now, func(result *qjs.Value) {
					assert.True(t, result.IsDate(), "Time pointer should convert to Date")
				})
			})

			t.Run("NilTimePointer", func(t *testing.T) {
				var nilTime *time.Time
				testValueConversion(t, ctx, nilTime, func(result *qjs.Value) {
					assert.True(t, result.IsNull(), "nil time pointer should be null")
				})
			})

			t.Run("EpochTime", func(t *testing.T) {
				testValueConversion(t, ctx, epoch, func(result *qjs.Value) {
					assert.True(t, result.IsDate(), "Epoch time should convert to Date")
				})
			})
		})

		t.Run("NilBytes", func(t *testing.T) {
			var nilBytes []byte
			testValueConversion(t, ctx, nilBytes, func(result *qjs.Value) {
				assert.True(t, result.IsNull(), "nil byte slice should be null")
			})
		})

		t.Run("Channel", func(t *testing.T) {
			ch := make(chan string, 5)
			testValueConversion(t, ctx, ch, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Channel should be object")
			})
		})
	})

	t.Run("CollectionTypes", func(t *testing.T) {
		t.Run("SliceTypes", func(t *testing.T) {
			t.Run("IntSlice", func(t *testing.T) {
				slice := []int{1, 2, 3, 4, 5}
				testValueConversion(t, ctx, slice, func(result *qjs.Value) {
					assert.True(t, result.IsArray(), "Int slice should be array")
					arr := result.Object()
					defer arr.Free()

					for i, expected := range slice {
						elem := arr.GetPropertyIndex(int64(i))
						defer elem.Free()
						assert.True(t, elem.IsNumber(), "Array element should be number")
						assert.Equal(t, int64(expected), elem.Int64())
					}
				})
			})

			t.Run("StringSlice", func(t *testing.T) {
				slice := []string{"hello", "world", "test"}
				testValueConversion(t, ctx, slice, func(result *qjs.Value) {
					assert.True(t, result.IsArray(), "String slice should be array")
					arr := result.Object()
					defer arr.Free()

					for i, expected := range slice {
						elem := arr.GetPropertyIndex(int64(i))
						defer elem.Free()
						assert.True(t, elem.IsString(), "Array element should be string")
						assert.Equal(t, expected, elem.String())
					}
				})
			})

			t.Run("EmptySlice", func(t *testing.T) {
				var emptySlice []int
				testValueConversion(t, ctx, emptySlice, func(result *qjs.Value) {
					assert.True(t, result.IsNull(), "Empty slice should be null")
				})
			})

			t.Run("NilSlice", func(t *testing.T) {
				var nilSlice []string
				testValueConversion(t, ctx, nilSlice, func(result *qjs.Value) {
					assert.True(t, result.IsNull(), "nil slice should be null")
				})
			})
		})

		t.Run("ArrayTypes", func(t *testing.T) {
			t.Run("IntArray", func(t *testing.T) {
				arr := [3]int{1, 2, 3}
				testValueConversion(t, ctx, arr, func(result *qjs.Value) {
					assert.True(t, result.IsArray(), "Int array should be array")
					jsArr := result.Object()
					defer jsArr.Free()

					for i, expected := range arr {
						elem := jsArr.GetPropertyIndex(int64(i))
						defer elem.Free()
						assert.True(t, elem.IsNumber(), "Array element should be number")
						assert.Equal(t, int64(expected), elem.Int64())
					}
				})
			})

			t.Run("StringArray", func(t *testing.T) {
				arr := [2]string{"hello", "world"}
				testValueConversion(t, ctx, arr, func(result *qjs.Value) {
					assert.True(t, result.IsArray(), "String array should be array")
					jsArr := result.Object()
					defer jsArr.Free()

					for i, expected := range arr {
						elem := jsArr.GetPropertyIndex(int64(i))
						defer elem.Free()
						assert.True(t, elem.IsString(), "Array element should be string")
						assert.Equal(t, expected, elem.String())
					}
				})
			})

			t.Run("EmptyArray", func(t *testing.T) {
				arr := [0]int{}
				testValueConversion(t, ctx, arr, func(result *qjs.Value) {
					assert.True(t, result.IsArray(), "Empty array should still be array")
				})
			})
		})

		t.Run("MapTypes", func(t *testing.T) {
			t.Run("StringKeyMap", func(t *testing.T) {
				m := map[string]int{
					"one":   1,
					"two":   2,
					"three": 3,
				}
				testValueConversion(t, ctx, m, func(result *qjs.Value) {
					assert.True(t, result.IsObject(), "Map should be object")
					obj := result.Object()
					defer obj.Free()

					for key, expectedValue := range m {
						prop := obj.GetPropertyStr(key)
						defer prop.Free()
						assert.True(t, prop.IsNumber(), "Map value should be number")
						assert.Equal(t, int64(expectedValue), prop.Int64())
					}
				})
			})

			t.Run("NonStringKeyMap", func(t *testing.T) {
				m := map[int]string{
					1: "one",
					2: "two",
					3: "three",
				}
				testValueConversion(t, ctx, m, func(result *qjs.Value) {
					assert.True(t, result.IsObject(), "Map should be object")
					obj := result.Object()
					defer obj.Free()

					for key, expectedValue := range m {
						keyStr := fmt.Sprintf("%d", key)
						prop := obj.GetPropertyStr(keyStr)
						defer prop.Free()
						assert.True(t, prop.IsString(), "Map value should be string")
						assert.Equal(t, expectedValue, prop.String())
					}
				})
			})

			t.Run("EmptyMap", func(t *testing.T) {
				m := make(map[string]int)
				testValueConversion(t, ctx, m, func(result *qjs.Value) {
					assert.True(t, result.IsObject(), "Empty map should be object")
				})
			})

			t.Run("NilMap", func(t *testing.T) {
				var nilMap map[string]int
				testValueConversion(t, ctx, nilMap, func(result *qjs.Value) {
					assert.True(t, result.IsNull(), "nil map should be null")
				})
			})
		})
	})

	t.Run("StructTypes", func(t *testing.T) {
		t.Run("SimpleStruct", func(t *testing.T) {
			s := SimpleStruct{Name: "John", Age: 30}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				nameProp := obj.GetPropertyStr("Name")
				defer nameProp.Free()
				assert.True(t, nameProp.IsString(), "Name field should be string")
				assert.Equal(t, "John", nameProp.String())

				ageProp := obj.GetPropertyStr("Age")
				defer ageProp.Free()
				assert.True(t, ageProp.IsNumber(), "Age field should be number")
				assert.Equal(t, int64(30), ageProp.Int64())
			})
		})

		t.Run("StructWithMethods", func(t *testing.T) {
			s := StructWithMethods{Value: 10}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Check field
				valueProp := obj.GetPropertyStr("Value")
				defer valueProp.Free()
				assert.True(t, valueProp.IsNumber(), "Value field should be number")
				assert.Equal(t, int64(10), valueProp.Int64())

				// Check methods
				getValueProp := obj.GetPropertyStr("GetValue")
				defer getValueProp.Free()
				assert.True(t, getValueProp.IsFunction(), "GetValue should be function")

				getDoubleValueProp := obj.GetPropertyStr("GetDoubleValue")
				defer getDoubleValueProp.Free()
				assert.True(t, getDoubleValueProp.IsFunction(), "GetDoubleValue should be function")
			})
		})

		t.Run("StructWithPointerMethods", func(t *testing.T) {
			// Adding another level of indirection to ensure pointer resolution works correctly
			s1 := &StructWithPointerMethods{Value: 20}
			s2 := &s1
			testValueConversion(t, ctx, s2, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Check field
				valueProp := obj.GetPropertyStr("Value")
				defer valueProp.Free()
				assert.True(t, valueProp.IsNumber(), "Value field should be number")
				assert.Equal(t, int64(20), valueProp.Int64())

				// Check value receiver method
				getValueProp := obj.GetPropertyStr("GetValue")
				defer getValueProp.Free()
				assert.True(t, getValueProp.IsFunction(), "GetValue should be function")

				// Check pointer receiver methods
				setValueProp := obj.GetPropertyStr("SetValue")
				defer setValueProp.Free()
				assert.True(t, setValueProp.IsFunction(), "SetValue should be function")

				getValuePlusOneProp := obj.GetPropertyStr("GetValuePlusOne")
				defer getValuePlusOneProp.Free()
				assert.True(t, getValuePlusOneProp.IsFunction(), "GetValuePlusOne should be function")
			})
		})

		t.Run("StructWithJSONTags", func(t *testing.T) {
			s := StructWithJSONTags{
				PublicField:    "public",
				RenamedField:   42,
				OmittedField:   "omitted",
				DefaultField:   true,
				EmptyTagField:  "empty_tag",
				CommaOnlyField: "comma_only",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Check renamed field
				renamedProp := obj.GetPropertyStr("renamed")
				defer renamedProp.Free()
				assert.True(t, renamedProp.IsNumber(), "Renamed field should be accessible")
				assert.Equal(t, int64(42), renamedProp.Int64())

				// Check public field
				publicProp := obj.GetPropertyStr("public_field")
				defer publicProp.Free()
				assert.True(t, publicProp.IsString(), "Public field should be accessible")
				assert.Equal(t, "public", publicProp.String())

				// Check omitted field (should not exist)
				omittedProp := obj.GetPropertyStr("OmittedField")
				defer omittedProp.Free()
				assert.True(t, omittedProp.IsUndefined(), "Omitted field should not exist")

				// Check default field (uses struct field name)
				defaultProp := obj.GetPropertyStr("DefaultField")
				defer defaultProp.Free()
				assert.True(t, defaultProp.IsBool(), "Default field should be accessible")
				assert.True(t, defaultProp.Bool())
			})
		})

		t.Run("EmbeddedStruct", func(t *testing.T) {
			s := EmbeddedStructTest{
				EmbeddedStruct: EmbeddedStruct{Name: "embedded", Age: 25},
				NewField:       "new",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Check embedded fields are accessible at top level
				nameProp := obj.GetPropertyStr("Name")
				defer nameProp.Free()
				assert.True(t, nameProp.IsString(), "Embedded Name field should be accessible")
				assert.Equal(t, "embedded", nameProp.String())

				ageProp := obj.GetPropertyStr("Age")
				defer ageProp.Free()
				assert.True(t, ageProp.IsNumber(), "Embedded Age field should be accessible")
				assert.Equal(t, int64(25), ageProp.Int64())

				// Check new field
				newFieldProp := obj.GetPropertyStr("NewField")
				defer newFieldProp.Free()
				assert.True(t, newFieldProp.IsString(), "New field should be accessible")
				assert.Equal(t, "new", newFieldProp.String())
			})
		})

		t.Run("EmbeddedPointerStructNil", func(t *testing.T) {
			s := EmbeddedPointerStructTest{
				EmbeddedStruct: nil,
				ExtraField:     "extra",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Check embedded pointer fields are not accessible at top level
				nameProp := obj.GetPropertyStr("Name")
				defer nameProp.Free()
				assert.True(t, nameProp.IsUndefined(), "Embedded pointer Name field should not be accessible")

				// Check extra field
				extraFieldProp := obj.GetPropertyStr("ExtraField")
				defer extraFieldProp.Free()
				assert.True(t, extraFieldProp.IsString(), "Extra field should be accessible")
				assert.Equal(t, "extra", extraFieldProp.String())
			})
		})

		t.Run("EmbeddedPointerStruct", func(t *testing.T) {
			s := EmbeddedPointerStructTest{
				EmbeddedStruct: &EmbeddedStruct{Name: "pointer_embedded", Age: 30},
				ExtraField:     "extra",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Check embedded pointer fields are accessible at top level
				nameProp := obj.GetPropertyStr("Name")
				defer nameProp.Free()
				assert.True(t, nameProp.IsString(), "Embedded pointer Name field should be accessible")
				assert.Equal(t, "pointer_embedded", nameProp.String())

				ageProp := obj.GetPropertyStr("Age")
				defer ageProp.Free()
				assert.True(t, ageProp.IsNumber(), "Embedded pointer Age field should be accessible")
				assert.Equal(t, int64(30), ageProp.Int64())

				// Check extra field
				extraFieldProp := obj.GetPropertyStr("ExtraField")
				defer extraFieldProp.Free()
				assert.True(t, extraFieldProp.IsString(), "Extra field should be accessible")
				assert.Equal(t, "extra", extraFieldProp.String())
			})
		})

		t.Run("EmbeddedPrimitiveTypes", func(t *testing.T) {
			s := StructWithEmbeddedPrimitive{
				CustomInt:     42,
				CustomUint:    100,
				CustomUint64:  math.MaxUint64,
				CustomFloat32: 3.14159,
				CustomBool:    true,
				CustomString:  "embedded_string",
				CustomIntPtr:  new(CustomIntPtr),
				ExtraField:    "extra",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				customIntProp := obj.GetPropertyStr("CustomInt")
				defer customIntProp.Free()
				assert.True(t, customIntProp.IsNumber(), "Embedded CustomInt field should be accessible")
				assert.Equal(t, int64(42), customIntProp.Int64())

				customUintProp := obj.GetPropertyStr("CustomUint")
				defer customUintProp.Free()
				assert.True(t, customUintProp.IsNumber(), "Embedded CustomUint field should be accessible")
				assert.Equal(t, int64(100), customUintProp.Int64())

				customUint64Prop := obj.GetPropertyStr("CustomUint64")
				defer customUint64Prop.Free()
				assert.True(t, customUint64Prop.IsNumber(), "Embedded CustomUint64 field should be accessible")
				assert.Equal(t, float64(math.MaxUint64), customUint64Prop.Float64())

				customFloat32Prop := obj.GetPropertyStr("CustomFloat32")
				defer customFloat32Prop.Free()
				assert.True(t, customFloat32Prop.IsNumber(), "Embedded CustomFloat32 field should be accessible")
				assert.InDelta(t, 3.14159, customFloat32Prop.Float64(), 0.0001)

				customBoolProp := obj.GetPropertyStr("CustomBool")
				defer customBoolProp.Free()
				assert.True(t, customBoolProp.IsBool(), "Embedded CustomBool field should be accessible")
				assert.True(t, customBoolProp.Bool())

				customStringProp := obj.GetPropertyStr("CustomString")
				defer customStringProp.Free()
				assert.True(t, customStringProp.IsString(), "Embedded CustomString field should be accessible")
				assert.Equal(t, "embedded_string", customStringProp.String())

				getValueProp := obj.GetPropertyStr("GetValue")
				defer getValueProp.Free()
				assert.True(t, getValueProp.IsFunction(), "GetValue method should be accessible")

				stringMethodProp := obj.GetPropertyStr("String")
				defer stringMethodProp.Free()
				assert.True(t, stringMethodProp.IsFunction(), "String method should be accessible")

				customIntPtrProp := obj.GetPropertyStr("CustomIntPtr")
				defer customIntPtrProp.Free()
				assert.True(t, customIntPtrProp.IsNumber(), "Embedded CustomIntPtr field should be accessible")
				assert.Equal(t, int64(0), customIntPtrProp.Int64())

				extraFieldProp := obj.GetPropertyStr("ExtraField")
				defer extraFieldProp.Free()
				assert.True(t, extraFieldProp.IsString(), "Extra field should be accessible")
				assert.Equal(t, "extra", extraFieldProp.String())
			})
		})

		t.Run("EmbeddedTypeAlias", func(t *testing.T) {
			s := StructWithAliasEmbedded{
				AliasInt:    100,
				AliasString: "alias_test",
				ExtraField:  "extra",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				aliasIntProp := obj.GetPropertyStr("AliasInt")
				defer aliasIntProp.Free()
				assert.True(t, aliasIntProp.IsNumber(), "Embedded AliasInt field should be accessible")
				assert.Equal(t, int64(100), aliasIntProp.Int64())

				aliasStringProp := obj.GetPropertyStr("AliasString")
				defer aliasStringProp.Free()
				assert.True(t, aliasStringProp.IsString(), "Embedded AliasString field should be accessible")
				assert.Equal(t, "alias_test", aliasStringProp.String())

				extraFieldProp := obj.GetPropertyStr("ExtraField")
				defer extraFieldProp.Free()
				assert.True(t, extraFieldProp.IsString(), "Extra field should be accessible")
				assert.Equal(t, "extra", extraFieldProp.String())
			})
		})

		t.Run("EmbeddedTypeDefinition", func(t *testing.T) {
			s := StructWithDefEmbedded{
				DefInt:     200,
				DefString:  "def_test",
				ExtraField: "extra",
			}
			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				defIntProp := obj.GetPropertyStr("DefInt")
				defer defIntProp.Free()
				assert.True(t, defIntProp.IsNumber(), "Embedded DefInt field should be accessible")
				assert.Equal(t, int64(200), defIntProp.Int64())

				defStringProp := obj.GetPropertyStr("DefString")
				defer defStringProp.Free()
				assert.True(t, defStringProp.IsString(), "Embedded DefString field should be accessible")
				assert.Equal(t, "def_test", defStringProp.String())

				getValueProp := obj.GetPropertyStr("GetValue")
				defer getValueProp.Free()
				assert.True(t, getValueProp.IsFunction(), "GetValue method should be accessible")

				upperProp := obj.GetPropertyStr("Upper")
				defer upperProp.Free()
				assert.True(t, upperProp.IsFunction(), "Upper method should be accessible")

				extraFieldProp := obj.GetPropertyStr("ExtraField")
				defer extraFieldProp.Free()
				assert.True(t, extraFieldProp.IsString(), "Extra field should be accessible")
				assert.Equal(t, "extra", extraFieldProp.String())
			})
		})
	})

	// Test the standalone backward compatibility functions
	t.Run("StructToJSObjectValue", func(t *testing.T) {
		s := SimpleStruct{Name: "test", Age: 25}
		rtype := reflect.TypeOf(s)
		rval := reflect.ValueOf(s)

		result, err := qjs.GoStructToJs(ctx, rtype, rval)
		require.NoError(t, err)
		defer result.Free()

		assert.True(t, result.IsObject(), "StructToJSObjectValue should return object")
	})

	t.Run("SliceToArrayValue", func(t *testing.T) {
		slice := []int{1, 2, 3}
		rval := reflect.ValueOf(slice)

		result, err := qjs.GoSliceToJs(ctx, rval)
		require.NoError(t, err)
		defer result.Free()

		assert.True(t, result.IsArray(), "SliceToArrayValue should return array")
	})

	t.Run("MapToObjectValue", func(t *testing.T) {
		m := map[string]int{"key": 42}
		rval := reflect.ValueOf(m)

		result, err := qjs.GoMapToJs(ctx, rval)
		require.NoError(t, err)
		defer result.Free()

		assert.True(t, result.IsObject(), "MapToObjectValue should return object")
	})

	t.Run("StructFieldAndMethodProcessing", func(t *testing.T) {
		// Test struct with unexported fields
		t.Run("UnexportedFields", func(t *testing.T) {
			type StructWithUnexported struct {
				PublicField  string
				privateField string // This should be skipped
			}

			s := StructWithUnexported{
				PublicField:  "public",
				privateField: "private",
			}

			testValueConversion(t, ctx, s, func(result *qjs.Value) {
				assert.True(t, result.IsObject(), "Struct should be object")
				obj := result.Object()
				defer obj.Free()

				// Public field should exist
				publicProp := obj.GetPropertyStr("PublicField")
				defer publicProp.Free()
				assert.True(t, publicProp.IsString(), "Public field should exist")

				// Private field should not exist
				privateProp := obj.GetPropertyStr("privateField")
				defer privateProp.Free()
				assert.True(t, privateProp.IsUndefined(), "Private field should not exist")
			})
		})

		t.Run("EmbeddedStructFieldError", func(t *testing.T) {
			// Test embedded struct with field that causes conversion error
			type BadEmbedded struct {
				UnsafePtr unsafe.Pointer
			}
			type StructWithBadEmbedded struct {
				BadEmbedded
				GoodField string
			}

			s := StructWithBadEmbedded{
				BadEmbedded: BadEmbedded{UnsafePtr: unsafe.Pointer(&[]byte{1}[0])},
				GoodField:   "good",
			}

			testErrorCase(t, ctx, s, "unsafe.Pointer")
		})
	})
}

// Additional specific error path tests
func TestToJSValue_ErrorHandling(t *testing.T) {
	runtime := must(qjs.New())
	defer runtime.Close()
	ctx := runtime.Context()

	t.Run("ErrorValues", func(t *testing.T) {
		t.Run("StandardError", func(t *testing.T) {
			err := errors.New("test error")
			testValueConversion(t, ctx, err, func(result *qjs.Value) {
				assert.True(t, result.IsError(), "Expected error object")
			})
		})

		t.Run("NilError", func(t *testing.T) {
			var nilErr error
			testValueConversion(t, ctx, nilErr, func(result *qjs.Value) {
				assert.True(t, result.IsNull(), "nil error should be null")
			})
		})
	})

	type UnsupportedStruct struct {
		UnsafePtr unsafe.Pointer
	}

	// Test map with conversion error in value
	t.Run("ValueConversionError", func(t *testing.T) {
		badMap := map[string]UnsupportedStruct{
			"key1": {UnsafePtr: unsafe.Pointer(&[]byte{1}[0])},
		}
		testErrorCase(t, ctx, badMap, "unsafe.Pointer")
	})

	t.Run("ArrayElementConversionErrors", func(t *testing.T) {
		// Test array with element that causes conversion error
		t.Run("ArrayWithUnsupportedElements", func(t *testing.T) {
			unsafePtrs := [2]unsafe.Pointer{
				unsafe.Pointer(&[]byte{1}[0]),
				unsafe.Pointer(&[]byte{2}[0]),
			}
			testErrorCase(t, ctx, unsafePtrs, "unsafe.Pointer")
		})

		t.Run("SliceWithUnsupportedElements", func(t *testing.T) {
			unsafePtrSlice := []unsafe.Pointer{
				unsafe.Pointer(&[]byte{3}[0]),
				unsafe.Pointer(&[]byte{4}[0]),
			}
			testErrorCase(t, ctx, unsafePtrSlice, "unsafe.Pointer")
		})
	})

	t.Run("PointerCircularReference", func(t *testing.T) {
		// Test circular reference through addressable struct
		type AddressableStruct struct {
			Name string
			Self *AddressableStruct
		}

		root := AddressableStruct{Name: "root"}
		root.Self = &root

		testErrorCase(t, ctx, root, "recursive")
	})

	t.Run("TestDefaultReflectTypeError", func(t *testing.T) {
		type UnhandledType uintptr
		ut := UnhandledType(0x12345)
		testErrorCase(t, ctx, ut, "UnhandledType")
	})

	t.Run("StructMethodConversionError", func(t *testing.T) {
		s := StructWithInvalidMethod{Value: 42}
		testErrorCase(t, ctx, s, "struct method")
	})

	t.Run("UnsafePointerError", func(t *testing.T) {
		var unsafePtr unsafe.Pointer
		testErrorCase(t, ctx, unsafePtr, "cannot convert Go 'unsafe.Pointer'")
	})

	t.Run("DeeplyNestedStructures", func(t *testing.T) {
		const maxDepth = 10
		root := createNestedStructure(maxDepth)

		jsValue, err := qjs.ToJsValue(runtime.Context(), root)
		require.NoError(t, err)
		defer jsValue.Free()
		assert.True(t, jsValue.IsObject())
	})
}
