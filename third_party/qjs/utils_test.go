package qjs_test

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
)

func TestIsConvertibleToJs(t *testing.T) {
	t.Run("BasicTypes", func(t *testing.T) {
		basicTypes := []struct {
			name     string
			value    any
			expected bool
		}{
			{"int", 42, true},
			{"string", "hello", true},
			{"bool", true, true},
			{"float64", 3.14, true},
			{"[]int", []int{1, 2, 3}, true},
			{"[]unsafe.Pointer", []unsafe.Pointer{}, false},
			{"map[string]int", map[string]int{"key": 1}, true},
			{"map[unsafe.Pointer]string", map[unsafe.Pointer]string{}, false},
			{"map[string]unsafe.Pointer", map[string]unsafe.Pointer{}, false},
			{"chan int", make(chan int), true},
		}

		for _, tt := range basicTypes {
			t.Run(tt.name, func(t *testing.T) {
				err := qjs.IsConvertibleToJs(reflect.TypeOf(tt.value), make(map[reflect.Type]bool), "test")
				if tt.expected {
					assert.NoError(t, err, "Expected %s to be convertible", tt.name)
				} else {
					assert.Error(t, err, "Expected %s to not be convertible", tt.name)
				}
			})
		}
	})

	t.Run("UnsupportedTypes", func(t *testing.T) {
		t.Run("unsafe_pointer", func(t *testing.T) {
			unsafePtr := unsafe.Pointer(&[]byte{1, 2, 3}[0])
			err := qjs.IsConvertibleToJs(reflect.TypeOf(unsafePtr), make(map[reflect.Type]bool), "test")
			assert.Error(t, err, "unsafe.Pointer should not be convertible")
			assert.Contains(t, err.Error(), "unsafe.Pointer", "Error should mention unsafe.Pointer")
		})
	})

	t.Run("StructWithFields", func(t *testing.T) {
		t.Run("ExportedFields", func(t *testing.T) {
			type TestStruct struct {
				PublicField  string
				AnotherField int
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct with exported fields should be convertible")
		})

		t.Run("UnexportedFieldsIgnored", func(t *testing.T) {
			type TestStruct struct {
				PublicField  string
				privateField string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct with unexported fields should be convertible (unexported fields ignored)")
		})

		t.Run("MixedFieldsWithUnsupportedPrivate", func(t *testing.T) {
			type TestStruct struct {
				PublicField  string
				privateField unsafe.Pointer
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct should be convertible when unsupported types are in unexported fields")
		})
	})

	t.Run("StructWithJSONTags", func(t *testing.T) {
		t.Run("JSONOmitTag", func(t *testing.T) {
			type TestStruct struct {
				PublicField  string
				OmittedField unsafe.Pointer `json:"-"`
				AnotherField int
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct should be convertible when unsupported types have json:\"-\" tag")
		})

		t.Run("JSONRenameTag", func(t *testing.T) {
			type TestStruct struct {
				Field            string         `json:"renamed_field"`
				UnsupportedField unsafe.Pointer `json:"-"`
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct should be convertible with JSON rename tags")
		})

		t.Run("UnsupportedFieldWithoutOmitTag", func(t *testing.T) {
			type TestStruct struct {
				PublicField      string
				UnsupportedField unsafe.Pointer
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.Error(t, err, "Struct should not be convertible with exported unsupported fields")
			assert.Contains(t, err.Error(), "TestStruct.UnsupportedField", "Error should mention the problematic field")
		})

		t.Run("ComplexJSONTags", func(t *testing.T) {
			type TestStruct struct {
				Field1         string         `json:"field1,omitempty"`
				Field2         int            `json:"field2"`
				OmittedField   unsafe.Pointer `json:"-"`
				AnotherOmitted unsafe.Pointer `json:"-,"`
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(TestStruct{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct should handle complex JSON tags correctly")
		})
	})

	t.Run("RecursiveTypes", func(t *testing.T) {
		type RecursiveStruct struct {
			Name string
			Self *RecursiveStruct
		}

		err := qjs.IsConvertibleToJs(reflect.TypeOf(RecursiveStruct{}), make(map[reflect.Type]bool), "test")
		assert.NoError(t, err, "Recursive struct should be convertible")
	})

	t.Run("NestedStructs", func(t *testing.T) {
		t.Run("ValidNested", func(t *testing.T) {
			type Inner struct {
				Value int
			}
			type Outer struct {
				Inner Inner
				Name  string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(Outer{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Nested convertible structs should be convertible")
		})

		t.Run("InvalidNested", func(t *testing.T) {
			type Inner struct {
				BadField unsafe.Pointer
			}
			type Outer struct {
				Inner Inner
				Name  string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(Outer{}), make(map[reflect.Type]bool), "test")
			assert.Error(t, err, "Nested struct with unsupported fields should not be convertible")
		})

		t.Run("NestedWithOmittedField", func(t *testing.T) {
			type Inner struct {
				GoodField string
				BadField  chan int `json:"-"`
			}
			type Outer struct {
				Inner Inner
				Name  string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(Outer{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Nested struct with omitted unsupported fields should be convertible")
		})

		t.Run("EmbeddedPointerStruct", func(t *testing.T) {
			type Inner struct {
				GoodField string
			}
			type Outer struct {
				*Inner
				Name string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(Outer{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct with embedded pointer to struct should be convertible")
		})

		t.Run("EmbeddedPointerWithUnsupportedField", func(t *testing.T) {
			type Inner struct {
				BadField unsafe.Pointer
			}
			type Outer struct {
				*Inner
				Name string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(Outer{}), make(map[reflect.Type]bool), "test")
			assert.Error(t, err, "Struct with embedded pointer containing unsupported fields should not be convertible")
		})

		t.Run("EmbeddedPointerWithOmittedField", func(t *testing.T) {
			type Inner struct {
				GoodField string
				BadField  chan int `json:"-"`
			}
			type Outer struct {
				*Inner
				Name string
			}

			err := qjs.IsConvertibleToJs(reflect.TypeOf(Outer{}), make(map[reflect.Type]bool), "test")
			assert.NoError(t, err, "Struct with embedded pointer containing omitted unsupported fields should be convertible")
		})
	})

	t.Run("Pointers", func(t *testing.T) {
		type TestStruct struct {
			Field        string
			OmittedField unsafe.Pointer `json:"-"`
		}

		err := qjs.IsConvertibleToJs(reflect.TypeOf(&TestStruct{}), make(map[reflect.Type]bool), "test")
		assert.NoError(t, err, "Pointer to convertible struct should be convertible")
	})
}

func TestGetGoTypeName(t *testing.T) {
	// Test input handling - reflect.Type vs value
	assert.Equal(t, "int", qjs.GetGoTypeName(reflect.TypeOf(42)))
	assert.Equal(t, "int", qjs.GetGoTypeName(42))

	// Test nil input panics
	assert.Panics(t, func() { qjs.GetGoTypeName(nil) })

	// Test pointer branch
	var intPtr *int
	assert.Equal(t, "*int", qjs.GetGoTypeName(intPtr))

	// Test slice branch
	var intSlice []int
	assert.Equal(t, "[]int", qjs.GetGoTypeName(intSlice))

	// Test array branch
	var intArray [5]int
	assert.Equal(t, "[5]int", qjs.GetGoTypeName(intArray))

	// Test map branch
	var stringIntMap map[string]int
	assert.Equal(t, "map[string]int", qjs.GetGoTypeName(stringIntMap))

	// Test channel branch
	var intChan chan int
	assert.Equal(t, "chan int", qjs.GetGoTypeName(intChan))

	// Test function branch
	var simpleFunc func()
	assert.Equal(t, "func()", qjs.GetGoTypeName(simpleFunc))

	// Test default case (basic types)
	assert.Equal(t, "string", qjs.GetGoTypeName("hello"))
	assert.Equal(t, "bool", qjs.GetGoTypeName(true))
	assert.Equal(t, "float64", qjs.GetGoTypeName(3.14))

	var goFunc func(int, ...int) (int, error)
	assert.Equal(t, "func(int, ...int) (int, error)", qjs.GetGoTypeName(goFunc))
}
