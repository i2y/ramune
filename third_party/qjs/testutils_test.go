package qjs_test

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fastschema/qjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestContext(_ *testing.T) (*qjs.Runtime, *qjs.Context) {
	runtime := must(qjs.New())
	ctx := runtime.Context()
	// t.Cleanup(runtime.Close)
	return runtime, ctx
}

type CustomUnmarshaler struct {
	Value string
}

func (c *CustomUnmarshaler) UnmarshalJSON(data []byte) error {
	// Fail for specific input to test error handling
	if strings.Contains(string(data), "invalid") {
		return errors.New("custom unmarshaling error")
	}

	// Strip quotes for string
	s := string(data)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}

	c.Value = s
	return nil
}

type EmbeddedStruct struct {
	Name string
	Age  int
}

type unExportedFieldStruct struct {
	Anything string
}

type NestedEmbeddedStruct struct {
	EmbeddedStruct
	unExportedFieldStruct
	Label string
}

func must[T any](val T, err error) T {
	if err != nil {
		panic(err)
	}
	return val
}

func extractErr[T any](_ T, err error) error {
	return err
}

func fileContent(file string) string {
	content := must(os.ReadFile(file))
	return string(content)
}

func glob(path string, exts ...string) ([]string, error) {
	var files []string
	must[any](nil, filepath.Walk(path,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				if slices.Contains(exts, filepath.Ext(path)) {
					files = append(files, path)
				}
			}

			return nil
		}),
	)

	return files, nil
}

type modeGlobalTest struct {
	file      string
	wantError bool
	expect    func(val *qjs.Value, err error)
}

type modeModuleTest struct {
	moduleDir string
	wantError bool
	expect    func(val *qjs.Value, err error)
}

func genModeGlobalTests(t *testing.T) []modeGlobalTest {
	tests := []modeGlobalTest{
		{
			file:      "01_invalid_syntax.js",
			wantError: true,
			expect: func(val *qjs.Value, err error) {
				assert.Nil(t, val)
				assert.Error(t, err)
			},
		},
		{
			wantError: true,
			file:      "02_throw_error.js",
			expect: func(val *qjs.Value, err error) {
				assert.Nil(t, val)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "script error")
			},
		},
		{
			file: "03_result_no_value.js",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsUndefined())
			},
		},
		{
			file: "04_result_number.js",
			expect: func(val *qjs.Value, err error) {
				assert.Equal(t, int32(3), val.Int32())
			},
		},
		{
			file: "05_result_string.js",
			expect: func(val *qjs.Value, err error) {
				assert.Equal(t, "Hello, World!", val.String())
			},
		},
		{
			file: "06_result_boolean.js",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.Bool())
			},
		},
		{
			file: "07_result_null.js",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsNull())
			},
		},
		{
			file: "08_result_undefined.js",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsUndefined())
			},
		},
		{
			file: "09_result_object.js",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsObject())
				obj := val.Object()
				defer obj.Free()
				assert.Equal(t, int32(1), obj.GetPropertyStr("a").Int32())
				assert.Equal(t, "2", obj.GetPropertyStr("b").String())
				assert.True(t, obj.GetPropertyStr("c").Bool())
				assert.True(t, obj.GetPropertyStr("d").IsUndefined())
			},
		},
		{
			wantError: true,
			file:      "10_top_level_await_not_supported.js",
			expect: func(val *qjs.Value, err error) {
				assert.Nil(t, val)
				assert.Error(t, err)
			},
		},
		{
			file: "11_empty_script.js",
			expect: func(val *qjs.Value, err error) {
				// An empty script should return undefined without error.
				assert.True(t, val.IsUndefined())
				assert.NoError(t, err)
			},
		},
		{
			file: "12_function.js",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsFunction())
			},
		},
		{
			file: "13_array.js",
			expect: func(val *qjs.Value, err error) {
				// Assuming arrays are returned as objects with numeric keys.
				assert.True(t, val.IsArray())
				arrObj := val.Object()
				defer arrObj.Free()
				assert.Equal(t, int32(1), arrObj.GetPropertyStr("0").Int32())
				assert.Equal(t, int32(2), arrObj.GetPropertyStr("1").Int32())
				assert.Equal(t, int32(3), arrObj.GetPropertyStr("2").Int32())
			},
		},
	}

	return tests
}

func runModeGlobalTests(t *testing.T, tests []modeGlobalTest, isScript bool) {
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			runtime := must(qjs.New(qjs.Option{MaxStackSize: 512 * 1024}))
			defer runtime.Close()
			var val *qjs.Value
			var err error

			fileName := path.Join("./testdata/01_global", test.file)
			if isScript {
				script := fileContent(fileName)
				val, err = runtime.Eval(
					fileName,
					qjs.Code(script),
				)
			} else {
				val, err = runtime.Eval(fileName)
			}

			defer val.Free()
			if !test.wantError {
				assert.NoError(t, err)
			}
			test.expect(val, err)
		})
	}
}

func genModeModuleTests(t *testing.T) []modeModuleTest {
	tests := []modeModuleTest{
		{
			moduleDir: "01_invalid_syntax",
			wantError: true,
			expect: func(val *qjs.Value, err error) {
				assert.Nil(t, val)
				assert.Error(t, err)
			},
		},
		{
			moduleDir: "02_throw_error",
			wantError: true,
			expect: func(val *qjs.Value, err error) {
				assert.Nil(t, val)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "module error")
			},
		},
		{
			moduleDir: "03_default_export_return_value",
			expect: func(val *qjs.Value, err error) {
				assert.Equal(t, int32(3), val.Int32())
			},
		},
		{
			moduleDir: "04_without_default_export_return_undefined",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsUndefined())
			},
		},
		{
			moduleDir: "05_import_not_exist",
			wantError: true,
			expect: func(val *qjs.Value, err error) {
				assert.Nil(t, val)
				assert.Error(t, err)
			},
		},
		{
			moduleDir: "06_import_export",
			expect: func(val *qjs.Value, err error) {
				assert.Equal(t, int32(6), val.Int32())
			},
		},
		{
			moduleDir: "07_export_fn_call",
			expect: func(val *qjs.Value, err error) {
				assert.Equal(t, int32(20), val.Int32())
			},
		},
		{
			moduleDir: "08_export_empty",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsUndefined())
			},
		},
		{
			moduleDir: "09_side_effect",
			expect: func(val *qjs.Value, err error) {
				assert.True(t, val.IsUndefined())
			},
		},
	}

	return tests
}

func runModeModuleTests(t *testing.T, tests []modeModuleTest, isScript bool) {
	for _, test := range tests {
		t.Run(test.moduleDir, func(t *testing.T) {
			runtime := must(qjs.New())
			defer runtime.Close()

			if !isScript {
				moduleMainFile := filepath.Join("./testdata/02_module", test.moduleDir, "999_index.js")
				val, err := runtime.Eval(
					moduleMainFile,
					qjs.TypeModule(),
				)
				defer val.Free()
				if !test.wantError {
					require.NoError(t, err)
				}
				test.expect(val, err)
				return
			}

			moduleDir := filepath.Join("./testdata/02_module", test.moduleDir)
			moduleFiles, err := glob(moduleDir, ".js")
			assert.NoError(t, err)

			// main file is the last file in the list.
			mainPath := moduleFiles[len(moduleFiles)-1]
			// Evaluate all dependencies first (all modules except the last one)
			depPaths := moduleFiles[:len(moduleFiles)-1]
			for _, depPath := range depPaths {
				depScript := fileContent(depPath)
				val, err := runtime.Load(
					depPath,
					qjs.Code(depScript),
					qjs.TypeModule(),
				)
				defer val.Free()
				assert.NoError(t, err)
			}

			// Evaluate the main module.
			mainScript := fileContent(mainPath)
			val, err := runtime.Eval(
				mainPath,
				qjs.Code(mainScript),
				qjs.TypeModule(),
			)
			defer val.Free()

			// Check error expectations.
			if !test.wantError {
				assert.NoError(t, err)
			}
			test.expect(val, err)
		})
	}
}
