package qjs_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allocateMemoryPtr allocates memory and returns a uint32 pointer
func allocateMemoryPtr(t *testing.T, runtime *qjs.Runtime, size uint64) uint32 {
	ptr := runtime.Malloc(size)
	t.Cleanup(func() { runtime.FreeHandle(ptr) })
	return uint32(ptr)
}

// generateTestPattern creates a test data pattern of specified size
func generateTestPattern(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	return data
}

// createPackedPtr creates a packed pointer with given address and size
func createPackedPtr(t *testing.T, runtime *qjs.Runtime, addr, size uint32) uint64 {
	packedValue := uint64(addr)<<32 | uint64(size)
	packedPtr := allocateMemoryPtr(t, runtime, 8)
	err := runtime.Mem().WriteUint64(packedPtr, packedValue)
	require.NoError(t, err)
	return uint64(packedPtr)
}

// memTestUtil provides common memory testing utilities
type memTestUtil struct {
	t       *testing.T
	runtime *qjs.Runtime
}

func newMemTestUtil(t *testing.T, runtime *qjs.Runtime) *memTestUtil {
	return &memTestUtil{t: t, runtime: runtime}
}

func (m *memTestUtil) allocatePtr(size uint64) uint32 {
	return allocateMemoryPtr(m.t, m.runtime, size)
}

func (m *memTestUtil) assertWriteRead(ptr uint32, data []byte) {
	err := m.runtime.Mem().Write(ptr, data)
	require.NoError(m.t, err)

	readData, err := m.runtime.Mem().Read(ptr, uint64(len(data)))
	require.NoError(m.t, err)
	assert.Equal(m.t, data, readData)
}

func (m *memTestUtil) testErrorCondition(name string, testFunc func() error, expectedErr error) {
	m.t.Run(name, func(t *testing.T) {
		err := testFunc()
		assert.ErrorIs(t, err, expectedErr, "Test %s should fail with expected error", name)
	})
}

func (m *memTestUtil) testBoundaryCondition(typeName string, typeSize uint32, testFunc func(uint32) error) {

}

// Test data structures for table-driven tests

type memoryOperationTest struct {
	name        string
	dataSize    uint64
	testData    []byte
	shouldError bool
	errorType   error
	testFunc    func(*memTestUtil, memoryOperationTest)
}

type primitiveTypeTest struct {
	name         string
	size         uint64
	values       []any
	writeFunc    func(*qjs.Runtime, uint32, any) error
	readFunc     func(*qjs.Runtime, uint32) (any, error)
	specialTests func(*testing.T, *qjs.Runtime)
}

type stringTest struct {
	name            string
	testString      string
	maxLen          uint32
	expectTruncated string
	shouldError     bool
	errorType       error
	setupFunc       func(*testing.T, *qjs.Runtime) uint32
}

type pointerTest struct {
	name        string
	setupFunc   func(*testing.T, *qjs.Runtime) uint64
	expectAddr  uint32
	expectSize  uint32
	shouldPanic bool
	testString  string
}

// Main test functions with improved structure and names

func TestMem_ReadWrite(t *testing.T) {
	runtime, _ := setupRuntime(t)
	util := newMemTestUtil(t, runtime)

	tests := []memoryOperationTest{
		{
			name:     "basic_byte_operations",
			dataSize: 100,
			testData: []byte("Hello World"),
		},
		{
			name:     "empty_data_handling",
			dataSize: 10,
			testData: []byte{},
		},
		{
			name:     "large_data_operations",
			dataSize: 1024,
			testData: generateTestPattern(1024),
		},
		{
			name:        "null_pointer_read_error",
			dataSize:    0,
			testData:    []byte{1, 2, 3},
			shouldError: true,
			errorType:   qjs.ErrInvalidPointer,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				_, err := util.runtime.Mem().Read(0, uint64(len(tc.testData)))
				assert.ErrorIs(t, err, tc.errorType)
			},
		},
		{
			name:        "null_pointer_write_error",
			dataSize:    0,
			testData:    []byte{1, 2, 3},
			shouldError: true,
			errorType:   qjs.ErrInvalidPointer,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				err := util.runtime.Mem().Write(0, tc.testData)
				assert.ErrorIs(t, err, tc.errorType)
			},
		},
		{
			name:        "read_uint8_at_boundary",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()
				_, err := mem.ReadUint8(memSize)
				assert.ErrorIs(t, err, tc.errorType)
			},
		},
		{
			name:        "read_uint32_insufficient_bytes",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()
				if memSize >= 2 {
					_, err := mem.ReadUint32(memSize - 2) // Only 2 bytes available, needs 4
					assert.ErrorIs(t, err, tc.errorType)
				}
			},
		},
		{
			name:        "write_uint32_insufficient_space",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()
				if memSize >= 2 {
					err := mem.WriteUint32(memSize-2, 42) // Only 2 bytes available, needs 4
					assert.ErrorIs(t, err, tc.errorType)
				}
			},
		},
		{
			name:        "read_uint64_insufficient_bytes",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()
				if memSize >= 4 {
					_, err := mem.ReadUint64(memSize - 4) // Only 4 bytes available, needs 8
					assert.ErrorIs(t, err, tc.errorType)
				}
			},
		},
		{
			name:        "write_uint64_insufficient_space",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()
				if memSize >= 4 {
					err := mem.WriteUint64(memSize-4, 42) // Only 4 bytes available, needs 8
					assert.ErrorIs(t, err, tc.errorType)
				}
			},
		},
		{
			name:        "size_exceeds_max_uint32",
			dataSize:    100,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				ptr := util.allocatePtr(tc.dataSize)
				oversizedRead := uint64(math.MaxUint32) + 1
				_, err := util.runtime.Mem().Read(ptr, oversizedRead)
				assert.ErrorIs(t, err, tc.errorType)
			},
		},
		{
			name:        "read_string_beyond_memory_bounds",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()

				// Test ReadString with a pointer beyond memory bounds
				if memSize < math.MaxUint32-1000 {
					beyondMemPtr := memSize + 100
					_, err := mem.ReadString(beyondMemPtr, 10)
					assert.ErrorIs(t, err, tc.errorType, "ReadString beyond memory should fail")
				}

				// Test ReadString with pointer definitely invalid
				if memSize > 1 {
					invalidPtr := memSize + 1
					_, err := mem.ReadString(invalidPtr, 1)
					assert.ErrorIs(t, err, tc.errorType, "ReadString with invalid pointer should fail")
				}
			},
		},
		{
			name:        "large_pointer_with_small_size",
			dataSize:    0,
			testData:    []byte{},
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			testFunc: func(util *memTestUtil, tc memoryOperationTest) {
				mem := util.runtime.Mem()
				memSize := mem.Size()

				// Use a large pointer value that's definitely out of bounds
				var largePtr uint32
				if memSize > 1000 {
					largePtr = memSize + 1000
				} else {
					largePtr = memSize * 2 // Ensure it's beyond current memory
				}

				// Test Read with small size at large pointer
				_, err := mem.Read(largePtr, 1)
				assert.Error(t, err, "Read with large pointer should fail")

				// Test WriteUint8 at large pointer
				err = mem.WriteUint8(largePtr, 42)
				assert.Error(t, err, "WriteUint8 with large pointer should fail")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.testFunc != nil {
				tc.testFunc(util, tc)
				return
			}

			ptr := util.allocatePtr(tc.dataSize)
			util.assertWriteRead(ptr, tc.testData)
		})
	}

	t.Run("must_operations", func(t *testing.T) {
		ptr := util.allocatePtr(10)
		testData := []byte{1, 2, 3, 4, 5}

		util.runtime.Mem().MustWrite(ptr, testData)
		readData := util.runtime.Mem().MustRead(ptr, uint64(len(testData)))
		assert.Equal(t, testData, readData)

		// Test panic conditions
		assert.Panics(t, func() {
			util.runtime.Mem().MustRead(0, 5)
		})
		assert.Panics(t, func() {
			util.runtime.Mem().MustWrite(0, []byte{1, 2, 3})
		})
	})
}

func TestMem_Primitives(t *testing.T) {
	runtime, _ := setupRuntime(t)
	util := newMemTestUtil(t, runtime)

	tests := []primitiveTypeTest{
		{
			name: "uint8",
			size: 1,
			values: []any{
				uint8(0), uint8(127), uint8(255),
			},
			writeFunc: func(r *qjs.Runtime, ptr uint32, v any) error {
				return r.Mem().WriteUint8(ptr, v.(uint8))
			},
			readFunc: func(r *qjs.Runtime, ptr uint32) (any, error) {
				return r.Mem().ReadUint8(ptr)
			},
		},
		{
			name: "uint32",
			size: 4,
			values: []any{
				uint32(0), uint32(0x12345678), uint32(math.MaxUint32),
			},
			writeFunc: func(r *qjs.Runtime, ptr uint32, v any) error {
				return r.Mem().WriteUint32(ptr, v.(uint32))
			},
			readFunc: func(r *qjs.Runtime, ptr uint32) (any, error) {
				return r.Mem().ReadUint32(ptr)
			},
		},
		{
			name: "uint64",
			size: 8,
			values: []any{
				uint64(0), uint64(0x1234567890ABCDEF), uint64(math.MaxUint64),
			},
			writeFunc: func(r *qjs.Runtime, ptr uint32, v any) error {
				return r.Mem().WriteUint64(ptr, v.(uint64))
			},
			readFunc: func(r *qjs.Runtime, ptr uint32) (any, error) {
				return r.Mem().ReadUint64(ptr)
			},
		},
		{
			name: "float64",
			size: 8,
			values: []any{
				0.0, 3.14159265358979, math.MaxFloat64,
				math.SmallestNonzeroFloat64, -math.MaxFloat64,
				math.Inf(1), math.Inf(-1),
			},
			writeFunc: func(r *qjs.Runtime, ptr uint32, v any) error {
				return r.Mem().WriteFloat64(ptr, v.(float64))
			},
			readFunc: func(r *qjs.Runtime, ptr uint32) (any, error) {
				return r.Mem().ReadFloat64(ptr)
			},
			specialTests: func(t *testing.T, runtime *qjs.Runtime) {
				// NaN requires special handling since NaN != NaN
				ptr := allocateMemoryPtr(t, runtime, 8)
				err := runtime.Mem().WriteFloat64(ptr, math.NaN())
				require.NoError(t, err)
				readValue, err := runtime.Mem().ReadFloat64(ptr)
				require.NoError(t, err)
				assert.True(t, math.IsNaN(readValue), "NaN should be preserved")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Test normal read/write operations
			for _, value := range tc.values {
				t.Run(fmt.Sprintf("%s_value_%v", tc.name, value), func(t *testing.T) {
					ptr := util.allocatePtr(tc.size)

					err := tc.writeFunc(runtime, ptr, value)
					require.NoError(t, err, "Write operation should succeed")

					readValue, err := tc.readFunc(runtime, ptr)
					require.NoError(t, err, "Read operation should succeed")
					assert.Equal(t, value, readValue, "Read value should match written value")
				})
			}

			// Test error conditions using unified approach
			util.testErrorCondition(
				fmt.Sprintf("%s_null_pointer_read", tc.name),
				func() error {
					_, err := tc.readFunc(runtime, 0)
					return err
				},
				qjs.ErrInvalidPointer,
			)

			util.testErrorCondition(
				fmt.Sprintf("%s_null_pointer_write", tc.name),
				func() error {
					return tc.writeFunc(runtime, 0, tc.values[0])
				},
				qjs.ErrInvalidPointer,
			)

			// Test boundary conditions using utility
			util.testBoundaryCondition(tc.name, uint32(tc.size), func(ptr uint32) error {
				return tc.writeFunc(runtime, ptr, tc.values[0])
			})

			// Run special tests if provided
			if tc.specialTests != nil {
				t.Run(fmt.Sprintf("%s_special_cases", tc.name), func(t *testing.T) {
					tc.specialTests(t, runtime)
				})
			}
		})
	}
}

func TestMem_Strings(t *testing.T) {
	runtime, _ := setupRuntime(t)
	util := newMemTestUtil(t, runtime)

	tests := []stringTest{
		{
			name:       "basic_string_operations",
			testString: "Hello, World!",
		},
		{
			name:       "empty_string_handling",
			testString: "",
		},
		{
			name:       "unicode_string_support",
			testString: "Hello 世界 🌍",
		},
		{
			name:       "string_with_control_chars",
			testString: "Line1\nLine2\rLine3\r\nCol1\tCol2",
		},
		{
			name:            "string_with_embedded_null",
			testString:      "Before\x00After",
			expectTruncated: "Before", // Truncated at embedded null
		},
		{
			name:        "null_pointer_read_error",
			testString:  "test",
			shouldError: true,
			errorType:   qjs.ErrInvalidPointer,
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint32 {
				return 0
			},
		},
		{
			name:        "null_pointer_write_error",
			testString:  "test",
			shouldError: true,
			errorType:   qjs.ErrInvalidPointer,
		},
		{
			name:        "insufficient_space_error",
			testString:  "test",
			shouldError: true,
			errorType:   qjs.ErrIndexOutOfRange,
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint32 {
				return runtime.Mem().Size()
			},
		},
		{
			name:       "zero_maxlen_handling",
			testString: "test",
			maxLen:     0,
		},
		{
			name:       "large_maxlen_handling",
			testString: "test",
			maxLen:     1000000,
		},
		{
			name:       "max_uint32_maxlen",
			testString: "test",
			maxLen:     math.MaxUint32,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ptr uint32

			if tc.setupFunc != nil {
				ptr = tc.setupFunc(t, runtime)
			} else if !tc.shouldError || tc.name == "null_pointer_write_error" {
				ptr = util.allocatePtr(uint64(len(tc.testString) + 1))
			}

			// Test error conditions
			if tc.shouldError {
				switch tc.name {
				case "null_pointer_read_error":
					_, err := runtime.Mem().ReadString(0, 10)
					assert.ErrorIs(t, err, tc.errorType)
					return
				case "null_pointer_write_error":
					err := runtime.Mem().WriteString(0, tc.testString)
					assert.ErrorIs(t, err, tc.errorType)
					return
				case "insufficient_space_error":
					err := runtime.Mem().WriteString(ptr, tc.testString)
					assert.ErrorIs(t, err, tc.errorType)
					return
				}
			}

			// Normal string operations
			err := runtime.Mem().WriteString(ptr, tc.testString)
			require.NoError(t, err)

			expectedStr := tc.testString
			if tc.expectTruncated != "" {
				expectedStr = tc.expectTruncated
			}

			maxLen := tc.maxLen
			if maxLen == 0 && tc.name != "zero_maxlen_handling" {
				maxLen = uint32(len(tc.testString) + 1)
			}

			if tc.name == "zero_maxlen_handling" {
				str, err := runtime.Mem().ReadString(ptr, maxLen)
				require.NoError(t, err)
				assert.Empty(t, str)
				return
			}

			readStr, err := runtime.Mem().ReadString(ptr, maxLen)
			require.NoError(t, err)
			assert.Equal(t, expectedStr, readStr)

			// Verify null terminator for basic strings
			if tc.name == "basic_string_operations" {
				rawBytes, err := runtime.Mem().Read(ptr, uint64(len(tc.testString)+1))
				require.NoError(t, err)
				assert.Equal(t, byte(0), rawBytes[len(tc.testString)])
			}
		})
	}

	t.Run("missing_null_terminator_error", func(t *testing.T) {
		ptr := util.allocatePtr(5)
		runtime.Mem().MustWrite(ptr, []byte{65, 66, 67, 68, 69})
		_, err := runtime.Mem().ReadString(ptr, 5)
		assert.ErrorIs(t, err, qjs.ErrNoNullTerminator)
	})
}

func TestMem_Pointers(t *testing.T) {
	runtime, _ := setupRuntime(t)

	tests := []pointerTest{
		{
			name: "valid_packed_data",
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint64 {
				return createPackedPtr(t, runtime, 0x12345678, 0x87654321)
			},
			expectAddr: 0x12345678,
			expectSize: 0x87654321,
		},
		{
			name: "zero_packed_ptr",
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint64 {
				return 0
			},
			expectAddr: 0,
			expectSize: 0,
		},
		{
			name: "little_endian_handling",
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint64 {
				ptr := allocateMemoryPtr(t, runtime, 8)
				err := runtime.Mem().Write(ptr, []byte{1, 2, 3, 4, 5, 6, 7, 8})
				require.NoError(t, err)
				return uint64(ptr)
			},
			expectAddr: 0x08070605,
			expectSize: 0x04030201,
		},
		{
			name: "out_of_bounds_panic",
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint64 {
				return uint64(math.MaxUint32 - 3)
			},
			shouldPanic: true,
		},
		{
			name: "valid_string_from_packed_ptr",
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint64 {
				testStr := "Test string for packed ptr"
				strPtr := allocateMemoryPtr(t, runtime, uint64(len(testStr)+1))
				err := runtime.Mem().WriteString(strPtr, testStr)
				require.NoError(t, err)
				return createPackedPtr(t, runtime, strPtr, uint32(len(testStr)))
			},
			testString: "Test string for packed ptr",
		},
		{
			name: "string_from_packed_ptr_zero_panic",
			setupFunc: func(t *testing.T, runtime *qjs.Runtime) uint64 {
				return 0
			},
			shouldPanic: true,
			testString:  "StringFromPackedPtr",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packedPtr := tc.setupFunc(t, runtime)

			if tc.shouldPanic {
				if tc.testString != "" {
					assert.Panics(t, func() {
						runtime.Mem().StringFromPackedPtr(packedPtr)
					})
				} else {
					assert.Panics(t, func() {
						runtime.Mem().UnpackPtr(packedPtr)
					})
				}
				return
			}

			if tc.testString != "" {
				str := runtime.Mem().StringFromPackedPtr(packedPtr)
				assert.Equal(t, tc.testString, str)
			} else {
				addr, size := runtime.Mem().UnpackPtr(packedPtr)
				assert.Equal(t, tc.expectAddr, addr)
				assert.Equal(t, tc.expectSize, size)
			}
		})
	}
}

func TestMem_Boundaries(t *testing.T) {
	runtime, _ := setupRuntime(t)
	util := newMemTestUtil(t, runtime)

	t.Run("memory_size_properties", func(t *testing.T) {
		size := runtime.Mem().Size()
		assert.Positive(t, size, "Memory size should be greater than 0")

		runtime.Malloc(1024)
		newSize := runtime.Mem().Size()
		assert.GreaterOrEqual(t, newSize, size, "Memory size should not decrease after allocation")
	})

	// Consolidated boundary testing for all types
	boundaryTests := []struct {
		name     string
		typeSize uint32
		testFunc func(ptr uint32) error
	}{
		{
			name:     "uint8",
			typeSize: 1,
			testFunc: func(ptr uint32) error {
				return runtime.Mem().WriteUint8(ptr, 1)
			},
		},
		{
			name:     "uint32",
			typeSize: 4,
			testFunc: func(ptr uint32) error {
				return runtime.Mem().WriteUint32(ptr, 1)
			},
		},
		{
			name:     "uint64",
			typeSize: 8,
			testFunc: func(ptr uint32) error {
				return runtime.Mem().WriteUint64(ptr, 1)
			},
		},
		{
			name:     "float64",
			typeSize: 8,
			testFunc: func(ptr uint32) error {
				return runtime.Mem().WriteFloat64(ptr, 1.0)
			},
		},
	}

	for _, bt := range boundaryTests {
		util.testBoundaryCondition(bt.name, bt.typeSize, bt.testFunc)
	}
}

func TestMem_EdgeCases(t *testing.T) {
	runtime, _ := setupRuntime(t)
	util := newMemTestUtil(t, runtime)

	edgeCases := []struct {
		name     string
		testFunc func(*testing.T, *memTestUtil)
	}{
		{
			name: "string_at_memory_boundary",
			testFunc: func(t *testing.T, util *memTestUtil) {
				ptr := util.allocatePtr(10)
				testStr := "AB"

				err := util.runtime.Mem().WriteString(ptr, testStr)
				require.NoError(t, err)

				readStr, err := util.runtime.Mem().ReadString(ptr, uint32(len(testStr)+1))
				require.NoError(t, err)
				assert.Equal(t, testStr, readStr)

				// Test reading with large maxlen
				readStr, err = util.runtime.Mem().ReadString(ptr, 1000000)
				require.NoError(t, err)
				assert.Equal(t, testStr, readStr)
			},
		},
		{
			name: "large_data_operations",
			testFunc: func(t *testing.T, util *memTestUtil) {
				largeSize := uint64(65536) // 64KB test
				ptr := util.allocatePtr(largeSize)
				testData := generateTestPattern(int(largeSize))

				util.assertWriteRead(ptr, testData)
			},
		},
		{
			name: "multiple_allocations_integrity",
			testFunc: func(t *testing.T, util *memTestUtil) {
				const numAllocs = 100
				const allocSize = uint64(64)

				ptrs := make([]uint32, numAllocs)
				expectedData := make([][]byte, numAllocs)

				// Create multiple allocations with unique data
				for i := range numAllocs {
					ptrs[i] = util.allocatePtr(allocSize)

					testData := make([]byte, allocSize)
					for j := range testData {
						testData[j] = byte(i + j)
					}
					expectedData[i] = testData

					err := util.runtime.Mem().Write(ptrs[i], testData)
					require.NoError(t, err)
				}

				// Verify data integrity across all allocations
				for i := range numAllocs {
					readData, err := util.runtime.Mem().Read(ptrs[i], allocSize)
					require.NoError(t, err)
					assert.Equal(t, expectedData[i], readData)
				}
			},
		},
	}

	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.testFunc(t, util)
		})
	}
}
