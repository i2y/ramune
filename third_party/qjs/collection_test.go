package qjs_test

import (
	"fmt"
	"testing"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRuntime creates a new runtime and context for testing
func setupRuntime(t *testing.T) (*qjs.Runtime, *qjs.Context) {
	t.Helper()
	rt := must(qjs.New())
	t.Cleanup(func() { rt.Close() })
	return rt, rt.Context()
}

// createTestValues creates common test values for collection testing
func createTestValues(ctx *qjs.Context) (string1, string2, string3, int1, bool1 *qjs.Value) {
	return ctx.NewString("hello"),
		ctx.NewString("world"),
		ctx.NewString("test"),
		ctx.NewInt32(42),
		ctx.NewBool(true)
}

// createThrowingFunction creates a function that throws an error when called
func createThrowingFunction(ctx *qjs.Context, errorMsg string) *qjs.Value {
	return ctx.Function(func(t *qjs.This) (*qjs.Value, error) {
		return nil, fmt.Errorf("%s", errorMsg)
	})
}

// setupMapWithFailingMethod creates a map with a method that throws an error
func setupMapWithFailingMethod(ctx *qjs.Context, methodName, errorMsg string) *qjs.Map {
	result := ctx.NewMap()
	throwingFn := createThrowingFunction(ctx, errorMsg)
	result.SetPropertyStr(methodName, throwingFn)
	return qjs.NewMap(result.Value)
}

// setupArrayWithFailingMethod creates an array with a method that throws an error
func setupArrayWithFailingMethod(ctx *qjs.Context, methodName, errorMsg string) *qjs.Array {
	result := ctx.NewArray()
	throwingFn := createThrowingFunction(ctx, errorMsg)
	result.SetPropertyStr(methodName, throwingFn)
	return qjs.NewArray(result.Value)
}

// setupSetWithFailingMethod creates a set with a method that throws an error
func setupSetWithFailingMethod(ctx *qjs.Context, methodName, errorMsg string) *qjs.Set {
	result := ctx.NewSet()
	throwingFn := createThrowingFunction(ctx, errorMsg)
	result.SetPropertyStr(methodName, throwingFn)
	return qjs.NewSet(result.Value)
}

// Array Tests
func TestArray(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("Nil_Array", func(t *testing.T) {
		var nilArray *qjs.Array
		assert.Nil(t, nilArray)
		nilArray.ForEach(nil)
		nilArray.Get(0)
		nilArray.Delete(0)
		nilArray.Set(0, nil)
	})

	t.Run("create_from_nil_value", func(t *testing.T) {
		assert.Nil(t, qjs.NewArray(nil))
	})

	t.Run("create_from_non_array_value", func(t *testing.T) {
		nonArray := ctx.NewString("not an array")
		defer nonArray.Free()
		assert.Nil(t, qjs.NewArray(nonArray))
	})

	t.Run("create_from_valid_array", func(t *testing.T) {
		arr := ctx.NewArray()
		defer arr.Free()
		wrappedArr := qjs.NewArray(arr.Value)
		assert.NotNil(t, wrappedArr)
		assert.Equal(t, int64(0), wrappedArr.Len())
	})

	t.Run("Basic_Operations", func(t *testing.T) {
		t.Run("push_and_get_elements", func(t *testing.T) {
			array := ctx.NewArray()
			defer array.Free()

			str1, _, _, int1, bool1 := createTestValues(ctx)

			// Push multiple elements
			length := array.Push(str1, int1, bool1)
			assert.Equal(t, int64(3), length)
			assert.Equal(t, int64(3), array.Len())

			// Get elements and verify values
			assert.Equal(t, "hello", array.Get(0).String())
			assert.Equal(t, int32(42), array.Get(1).Int32())
			assert.True(t, array.Get(2).Bool())
		})

		t.Run("has_index_check", func(t *testing.T) {
			array := ctx.NewArray()
			defer array.Free()

			str1, _, _, _, _ := createTestValues(ctx)
			array.Push(str1)

			assert.True(t, array.HasIndex(0))
			assert.False(t, array.HasIndex(1))
			assert.False(t, array.HasIndex(-1))
		})

		t.Run("set_and_update_elements", func(t *testing.T) {
			array := ctx.NewArray()
			defer array.Free()

			str1, str2, _, _, _ := createTestValues(ctx)
			array.Push(str1)

			array.Set(0, str2)
			assert.Equal(t, "world", array.Get(0).String())
		})

		t.Run("delete_elements", func(t *testing.T) {
			array := ctx.NewArray()
			defer array.Free()

			str1, str2, str3, _, _ := createTestValues(ctx)
			array.Push(str1, str2, str3)

			success := array.Delete(1)
			assert.True(t, success)
			assert.Equal(t, int64(2), array.Len())
			assert.Equal(t, "hello", array.Get(0).String())
			assert.Equal(t, "test", array.Get(1).String())
		})

		t.Run("forEach_iteration", func(t *testing.T) {
			array := ctx.NewArray()
			defer array.Free()

			str1, str2, str3, _, _ := createTestValues(ctx)
			array.Push(str1, str2, str3)

			values := make([]string, 0)
			array.ForEach(func(_, value *qjs.Value) {
				fmt.Printf("Foreach value: %s\n", value.String())
				values = append(values, value.String())
			})

			assert.Len(t, values, 3)
			assert.Contains(t, values, "hello")
			assert.Contains(t, values, "world")
			assert.Contains(t, values, "test")
		})
	})

	t.Run("JSON_Serialization", func(t *testing.T) {
		array := ctx.NewArray()
		defer array.Free()

		str1, _, _, int1, bool1 := createTestValues(ctx)
		array.Push(str1, int1, bool1)

		json := must(array.JSONStringify())
		assert.JSONEq(t, `["hello",42,true]`, json)
	})

	t.Run("Error_Handling", func(t *testing.T) {
		// Test push method error handling - create an array that will cause push to fail
		t.Run("push_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			array := setupArrayWithFailingMethod(ctx, "push", "push failed")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke push on Array")
					}
				}()
				array.Push(ctx.NewString("test"))
			}()
		})

		// Test delete method error handling - create an array that will cause splice to fail
		t.Run("delete_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)

			// Create an array with elements first
			result := ctx.NewArray()
			defer result.Free()
			str1, str2, _, _, _ := createTestValues(ctx)
			result.Push(str1, str2)

			// Setup array with failing splice
			result.SetPropertyStr("splice", createThrowingFunction(ctx, "splice failed"))
			array := qjs.NewArray(result.Value)

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke splice on Array")
					}
				}()

				array.Delete(0)
			}()
		})
	})
}

// Map Tests
func TestMap(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("Nil_Map", func(t *testing.T) {
		var nilMap *qjs.Map
		assert.Nil(t, nilMap)
		assert.Nil(t, nilMap.CreateObject())
		assert.Nil(t, nilMap.Get(nil))
		assert.False(t, nilMap.Has(nil))
		nilMap.ForEach(nil)
		nilMap.Set(nil, nil)
		nilMap.Delete(nil)
	})

	t.Run("create_from_nil_value", func(t *testing.T) {
		assert.Nil(t, qjs.NewMap(nil))
	})

	t.Run("create_from_non_map_value", func(t *testing.T) {
		nonMap := ctx.NewString("not a map")
		defer nonMap.Free()
		assert.Nil(t, qjs.NewMap(nonMap))
	})

	t.Run("create_from_valid_map", func(t *testing.T) {
		m := ctx.NewMap()
		defer m.Free()
		wrappedMap := qjs.NewMap(m.Value)
		assert.NotNil(t, wrappedMap)
		assert.True(t, wrappedMap.IsMap())
		assert.True(t, wrappedMap.IsObject())
	})

	t.Run("Basic_Operations", func(t *testing.T) {
		_, ctx := setupRuntime(t)

		t.Run("set_get_has_delete_operations", func(t *testing.T) {
			m := ctx.NewMap()
			defer m.Free()

			keyName := ctx.NewString("name")
			valueName := ctx.NewString("John")
			keyAge := ctx.NewString("age")
			valueAge := ctx.NewInt32(30)

			// Set values
			m.Set(keyName, valueName)
			m.Set(keyAge, valueAge)

			// Check existence
			assert.True(t, m.Has(keyName))
			assert.True(t, m.Has(keyAge))

			// Get values
			assert.Equal(t, "John", m.Get(keyName).String())
			assert.Equal(t, int32(30), m.Get(keyAge).Int32())

			// Delete value
			m.Delete(keyName)
			assert.False(t, m.Has(keyName))
			assert.True(t, m.Has(keyAge))
		})

		t.Run("to_map_returns_self", func(t *testing.T) {
			m := ctx.NewMap()
			defer m.Free()

			key := ctx.NewString("test")
			value := ctx.NewString("value")
			m.Set(key, value)

			sameMap := m.ToMap()
			assert.Equal(t, "value", sameMap.Get(key).String())
		})
	})

	t.Run("forEach_iteration", func(t *testing.T) {
		m := ctx.NewMap()
		defer m.Free()

		m.Set(ctx.NewString("name"), ctx.NewString("John"))
		m.Set(ctx.NewString("age"), ctx.NewInt32(30))

		entries := make(map[string]string)
		m.ForEach(func(key, value *qjs.Value) {
			entries[key.String()] = value.String()
		})

		assert.Len(t, entries, 2)
		assert.Equal(t, "John", entries["name"])
		assert.Equal(t, "30", entries["age"])
	})

	t.Run("create_object_conversion", func(t *testing.T) {
		m := ctx.NewMap()
		defer m.Free()

		key := ctx.NewString("test")
		value := ctx.NewString("value")
		m.Set(key, value)

		obj := m.CreateObject()
		defer obj.Free()
		assert.Equal(t, "value", obj.GetPropertyStr("test").String())
	})

	t.Run("json_stringify_output", func(t *testing.T) {
		m := ctx.NewMap()
		defer m.Free()

		m.Set(ctx.NewString("name"), ctx.NewString("John"))
		m.Set(ctx.NewString("age"), ctx.NewInt32(30))

		json := must(m.JSONStringify())
		assert.Contains(t, json, `"name":"John"`)
		assert.Contains(t, json, `"age":30`)
	})

	t.Run("Error_Handling", func(t *testing.T) {
		_, ctx := setupRuntime(t)
		key := ctx.NewString("key")
		defer key.Free()
		value := ctx.NewString("value")
		defer value.Free()

		// Test Map.CreateObject error when ForEach fails
		t.Run("createObject_forEach_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)

			// Create a map with values
			result := ctx.NewMap()
			defer result.Free()
			key := ctx.NewString("test")
			value := ctx.NewString("value")
			result.Set(key, value)

			// Setup map with failing forEach
			result.SetPropertyStr("forEach", createThrowingFunction(ctx, "forEach failed"))
			m := qjs.NewMap(result.Value)

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke forEach on Map")
					}
				}()
				m.CreateObject()
			}()
		})

		// Test Map.Get invoke error
		t.Run("get_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			m := setupMapWithFailingMethod(ctx, "get", "get failed")
			key := ctx.NewString("test")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke get on Map")
					}
				}()
				m.Get(key)
			}()
		})

		// Test Map.Set invoke error
		t.Run("set_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			m := setupMapWithFailingMethod(ctx, "set", "set failed")
			key := ctx.NewString("test")
			value := ctx.NewString("value")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke set on Map")
					}
				}()
				m.Set(key, value)
			}()
		})

		// Test Map.Delete invoke error
		t.Run("delete_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			m := setupMapWithFailingMethod(ctx, "delete", "delete failed")
			key := ctx.NewString("test")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke delete on Map")
					}
				}()
				m.Delete(key)
			}()
		})

		// Test Map.Has invoke error
		t.Run("has_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			m := setupMapWithFailingMethod(ctx, "has", "has failed")
			key := ctx.NewString("test")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke has on Map")
					}
				}()
				m.Has(key)
			}()
		})

		// Test Map.ForEach invoke error
		t.Run("forEach_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			m := setupMapWithFailingMethod(ctx, "forEach", "forEach failed")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke forEach on Map")
					}
				}()
				m.ForEach(func(key, value *qjs.Value) {})
			}()
		})

		// Test Map.JSONStringify error when CreateObject fails
		t.Run("jsonStringify_createObject_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)

			// Create a map with values
			result := ctx.NewMap()
			defer result.Free()
			key := ctx.NewString("test")
			value := ctx.NewString("value")
			result.Set(key, value)

			// Setup map with failing forEach
			result.SetPropertyStr("forEach", createThrowingFunction(ctx, "forEach failed"))
			m := qjs.NewMap(result.Value)

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke forEach on Map")
					}
				}()
				_, err := m.JSONStringify()
				require.Error(t, err)
				assert.Contains(t, err.Error(), "forEach failed")
			}()
		})
	})
}

// Set Tests
func TestSet(t *testing.T) {
	_, ctx := setupRuntime(t)

	t.Run("Nil_Set", func(t *testing.T) {
		var nilSet *qjs.Set
		nilSet.ForEach(nil)
		assert.Nil(t, nilSet)
		assert.Nil(t, nilSet.ToArray())
		assert.False(t, nilSet.Has(nil))
		nilSet.Add(nil)
		nilSet.Delete(nil)
	})

	t.Run("create_from_nil_value", func(t *testing.T) {
		assert.Nil(t, qjs.NewSet(nil))
	})

	t.Run("create_from_non_set_value", func(t *testing.T) {
		nonSet := ctx.NewString("not a set")
		defer nonSet.Free()
		assert.Nil(t, qjs.NewSet(nonSet))
	})

	t.Run("create_from_valid_set", func(t *testing.T) {
		s := ctx.NewSet()
		defer s.Free()
		wrappedSet := qjs.NewSet(s.Value)
		assert.NotNil(t, wrappedSet)
	})

	t.Run("add_has_delete_operations", func(t *testing.T) {
		set := ctx.NewSet()
		defer set.Free()

		str1, _, _, int1, bool1 := createTestValues(ctx)

		// Add values
		set.Add(str1)
		set.Add(int1)
		set.Add(bool1)

		// Duplicate addition should not error
		set.Add(str1)

		// Check existence
		assert.True(t, set.Has(str1))
		assert.False(t, set.Has(ctx.NewString("missing")))

		// Delete value
		set.Delete(str1)
		assert.False(t, set.Has(str1))
	})

	t.Run("forEach_iteration", func(t *testing.T) {
		set := ctx.NewSet()
		defer set.Free()

		str1, str2, str3, _, _ := createTestValues(ctx)
		set.Add(str1)
		set.Add(str2)
		set.Add(str3)

		values := make([]string, 0)
		set.ForEach(func(value *qjs.Value) {
			values = append(values, value.String())
		})

		assert.Len(t, values, 3)
		assert.Contains(t, values, "hello")
		assert.Contains(t, values, "world")
		assert.Contains(t, values, "test")
	})

	t.Run("to_array_conversion", func(t *testing.T) {
		set := ctx.NewSet()
		defer set.Free()

		str1, _, _, int1, bool1 := createTestValues(ctx)
		set.Add(str1)
		set.Add(int1)
		set.Add(bool1)

		array := set.ToArray()
		defer array.Free()
		assert.Equal(t, int64(3), array.Len())
	})

	t.Run("json_stringify_output", func(t *testing.T) {
		set := ctx.NewSet()
		defer set.Free()

		str1, _, _, int1, bool1 := createTestValues(ctx)
		set.Add(str1)
		set.Add(int1)
		set.Add(bool1)

		json := must(set.JSONStringify())
		assert.Contains(t, json, "hello")
		assert.Contains(t, json, "42")
		assert.Contains(t, json, "true")
	})

	t.Run("Error_Handling", func(t *testing.T) {
		_, ctx := setupRuntime(t)
		value := ctx.NewString("test")
		defer value.Free()

		// Test Set.Add invoke error
		t.Run("add_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			s := setupSetWithFailingMethod(ctx, "add", "add failed")
			value := ctx.NewString("test")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke add on Set")
					}
				}()
				s.Add(value)
			}()
		})

		// Test Set.Delete invoke error
		t.Run("delete_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			s := setupSetWithFailingMethod(ctx, "delete", "delete failed")
			value := ctx.NewString("test")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke delete on Set")
					}
				}()
				s.Delete(value)
			}()
		})

		// Test Set.Has invoke error
		t.Run("has_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			s := setupSetWithFailingMethod(ctx, "has", "has failed")
			value := ctx.NewString("test")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke has on Set")
					}
				}()
				s.Has(value)
			}()
		})

		// Test Set.ForEach invoke error
		t.Run("forEach_invoke_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)
			s := setupSetWithFailingMethod(ctx, "forEach", "forEach failed")

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke forEach on Set")
					}
				}()
				s.ForEach(func(value *qjs.Value) {})
			}()
		})

		// Test Set.ToArray error when ForEach fails
		t.Run("toArray_forEach_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)

			// Create a set with values
			result := ctx.NewSet()
			defer result.Free()
			value := ctx.NewString("test")
			result.Add(value)

			// Setup set with failing forEach
			result.SetPropertyStr("forEach", createThrowingFunction(ctx, "forEach failed"))
			s := qjs.NewSet(result.Value)

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke forEach on Set")
					}
				}()
				s.ToArray()
			}()
		})

		// Test Set.ToArray error when Push fails inside ForEach callback
		t.Run("toArray_push_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)

			// Create a set with values
			s := ctx.NewSet()
			defer s.Free()

			value := ctx.NewString("test")
			s.Add(value)

			// Get the Array prototype and modify its push method
			globalThis := ctx.Global()
			defer globalThis.Free()

			arrayConstructor := globalThis.GetPropertyStr("Array")
			defer arrayConstructor.Free()

			arrayPrototype := arrayConstructor.GetPropertyStr("prototype")
			defer arrayPrototype.Free()

			// Store the original push method
			originalPush := arrayPrototype.GetPropertyStr("push")
			defer originalPush.Free()

			// Replace push with a failing version
			failingPush := createThrowingFunction(ctx, "push failed")
			arrayPrototype.SetPropertyStr("push", failingPush)

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke push on Array")
					}
				}()

				array := s.ToArray()
				defer array.Free()
			}()
		})

		// Test Set.JSONStringify error when ToArray fails
		t.Run("jsonStringify_toArray_error", func(t *testing.T) {
			_, ctx := setupRuntime(t)

			// Create a set with values
			result := ctx.NewSet()
			defer result.Free()
			value := ctx.NewString("test")
			result.Add(value)

			// Setup set with failing forEach
			result.SetPropertyStr("forEach", createThrowingFunction(ctx, "forEach failed"))
			s := qjs.NewSet(result.Value)

			func() {
				defer func() {
					if r := recover(); r != nil {
						assert.Contains(t, r.(error).Error(), "failed to invoke forEach on Set")
					}
				}()
				_, err := s.JSONStringify()
				require.Error(t, err)
				assert.Contains(t, err.Error(), "forEach failed")
			}()
		})
	})
}
