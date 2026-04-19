package ramune_test

import (
	"bytes"
	"errors"
	"math"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/i2y/ramune"
)

func newOrSkip(t *testing.T) *ramune.Runtime {
	t.Helper()
	r, err := ramune.New()
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	return r
}

// --- Runtime lifecycle ---

func TestNewAndClose(t *testing.T) {
	r, err := ramune.New()
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestDoubleClose(t *testing.T) {
	r := newOrSkip(t)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExecAfterClose(t *testing.T) {
	r := newOrSkip(t)
	r.Close()
	err := r.Exec("1")
	if !errors.Is(err, ramune.ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got: %v", err)
	}
}

// --- Eval ---

func TestEvalSimple(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("1 + 2")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 3.0 {
		t.Fatalf("got %f, want 3.0", f)
	}
}

func TestEvalString(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("'hello'")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello" {
		t.Fatalf("got %q, want %q", s, "hello")
	}
}

func TestEvalStringJapanese(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("'こんにちは世界'")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "こんにちは世界" {
		t.Fatalf("got %q, want %q", s, "こんにちは世界")
	}
}

func TestEvalBoolean(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("true")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	b, err := v.Bool()
	if err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Fatal("got false, want true")
	}
}

func TestEvalNull(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("null")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if !v.IsNull() {
		t.Fatal("expected null")
	}
}

func TestEvalUndefined(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("undefined")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if !v.IsUndefined() {
		t.Fatal("expected undefined")
	}
}

func TestEvalError(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	_, err := r.Eval("throw new Error('test error')")
	if err == nil {
		t.Fatal("expected error")
	}
	var jsErr *ramune.JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("expected *JSError, got %T: %v", err, err)
	}
}

func TestEvalSyntaxError(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	_, err := r.Eval("if (")
	if err == nil {
		t.Fatal("expected error for syntax error")
	}
}

func TestErrorStack(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()
	if r.Engine() == "quickjs" || r.Engine() == "goja" || r.Engine() == "qjswasm" {
		t.Skip("this backend does not expose stack traces via Go bindings")
	}

	_, err := r.Eval(`
		function foo() { throw new Error('deep error'); }
		function bar() { foo(); }
		bar();
	`)
	if err == nil {
		t.Fatal("expected error")
	}
	var jsErr *ramune.JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("expected *JSError, got %T: %v", err, err)
	}
	if jsErr.Stack == "" {
		t.Fatal("expected non-empty stack trace")
	}
	if jsErr.Message == "" {
		t.Fatal("expected non-empty message")
	}
	// Stack should contain function names.
	if !strings.Contains(jsErr.Stack, "foo") {
		t.Fatalf("stack should contain 'foo': %s", jsErr.Stack)
	}
	if !strings.Contains(jsErr.Stack, "bar") {
		t.Fatalf("stack should contain 'bar': %s", jsErr.Stack)
	}
}

func TestErrorMessage(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	_, err := r.Eval(`throw new TypeError('custom type error')`)
	if err == nil {
		t.Fatal("expected error")
	}
	var jsErr *ramune.JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("expected *JSError, got %T: %v", err, err)
	}
	if !strings.Contains(jsErr.Message, "custom type error") {
		t.Fatalf("message should contain 'custom type error': %s", jsErr.Message)
	}
}

func TestCallErrorStack(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()
	if r.Engine() == "quickjs" || r.Engine() == "goja" || r.Engine() == "qjswasm" {
		t.Skip("this backend does not expose stack traces via Go bindings")
	}

	fn, err := r.Eval(`(function() { throw new Error('call error'); })`)
	if err != nil {
		t.Fatal(err)
	}
	defer fn.Close()

	_, err = fn.Call()
	if err == nil {
		t.Fatal("expected error")
	}
	var jsErr *ramune.JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("expected *JSError, got %T: %v", err, err)
	}
	if jsErr.Stack == "" {
		t.Fatal("expected non-empty stack trace from Call")
	}
}

// --- Exec ---

func TestExec(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec("var x = 42;"); err != nil {
		t.Fatal(err)
	}
}

// --- Value ---

func TestValueClose(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("42")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValueNilClose(t *testing.T) {
	var v *ramune.Value
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValueString(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("42")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	if got := v.String(); got != "42" {
		t.Fatalf("got %q, want %q", got, "42")
	}
}

func TestValueInt64(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("42")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	n, err := v.Int64()
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("got %d, want 42", n)
	}
}

// --- Properties ---

func TestGetProperty(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec("var testVal = 99;"); err != nil {
		t.Fatal(err)
	}

	global := r.GlobalObject()
	defer global.Close()

	v := global.Attr("testVal")
	if v == nil {
		t.Fatal("testVal property is nil")
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 99.0 {
		t.Fatalf("got %f, want 99.0", f)
	}
}

// --- Function calls ---

func TestCallFunction(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	global := r.GlobalObject()
	defer global.Close()

	mathObj := global.Attr("Math")
	if mathObj == nil {
		t.Fatal("Math is nil")
	}
	defer mathObj.Close()

	maxFn := mathObj.Attr("max")
	if maxFn == nil {
		t.Fatal("Math.max is nil")
	}
	defer maxFn.Close()

	result, err := maxFn.Call(3.0, 5.0)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()

	f, err := result.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 5.0 {
		t.Fatalf("got %f, want 5.0", f)
	}
}

func TestCallFunctionWithStringArg(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("(function(x) { return x.toUpperCase(); })")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	result, err := v.Call("hello")
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()

	s, err := result.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "HELLO" {
		t.Fatalf("got %q, want %q", s, "HELLO")
	}
}

func TestEvalNaN(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("NaN")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(f) {
		t.Fatalf("got %f, want NaN", f)
	}
}

// --- Object construction ---

func TestNewObject(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.NewObject(map[string]any{
		"name": "Alice",
		"age":  30.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	name := obj.Attr("name")
	if name == nil {
		t.Fatal("name property is nil")
	}
	defer name.Close()

	s, err := name.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "Alice" {
		t.Fatalf("got %q, want %q", s, "Alice")
	}

	age := obj.Attr("age")
	if age == nil {
		t.Fatal("age property is nil")
	}
	defer age.Close()

	f, err := age.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 30.0 {
		t.Fatalf("got %f, want 30.0", f)
	}
}

func TestNewArray(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	arr, err := r.NewArray(1.0, "two", true)
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	// Use JSON.stringify to verify the array contents.
	if err := r.Exec("var __testArr = null;"); err != nil {
		t.Fatal(err)
	}
	global := r.GlobalObject()
	defer global.Close()
	if err := global.SetAttr("__testArr", arr); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("JSON.stringify(__testArr)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	want := `[1,"two",true]`
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}

func TestSetAttr(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.Eval("({})")
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	if err := obj.SetAttr("x", 42.0); err != nil {
		t.Fatal(err)
	}

	x := obj.Attr("x")
	if x == nil {
		t.Fatal("x property is nil")
	}
	defer x.Close()

	f, err := x.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 42.0 {
		t.Fatalf("got %f, want 42.0", f)
	}
}

func TestNewObjectNested(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.NewObject(map[string]any{
		"user": map[string]any{
			"name": "Bob",
		},
		"tags": []any{"go", "js"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	// Assign to global and stringify to verify structure.
	global := r.GlobalObject()
	defer global.Close()
	if err := global.SetAttr("__nested", obj); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("__nested.user.name")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "Bob" {
		t.Fatalf("got %q, want %q", s, "Bob")
	}

	v2, err := r.Eval("JSON.stringify(__nested.tags)")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()

	s2, err := v2.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s2 != `["go","js"]` {
		t.Fatalf("got %q, want %q", s2, `["go","js"]`)
	}
}

func TestNewObjectClosedRuntime(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	_, err := r.NewObject(nil)
	if !errors.Is(err, ramune.ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got: %v", err)
	}

	_, err = r.NewArray(1)
	if !errors.Is(err, ramune.ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got: %v", err)
	}
}

func TestSetAttrNilValue(t *testing.T) {
	var v *ramune.Value
	err := v.SetAttr("x", 1)
	if !errors.Is(err, ramune.ErrNilValue) {
		t.Fatalf("expected ErrNilValue, got: %v", err)
	}
}

// --- Object enumeration & manipulation ---

func TestKeys(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.NewObject(map[string]any{"a": 1.0, "b": 2.0, "c": 3.0})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	keys, err := obj.Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("got %d keys, want 3", len(keys))
	}
	// Keys may be in any order; check all are present.
	found := map[string]bool{}
	for _, k := range keys {
		found[k] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !found[want] {
			t.Fatalf("missing key %q", want)
		}
	}
}

func TestLen(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	arr, err := r.NewArray(1, 2, 3, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	n, err := arr.Len()
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("got %d, want 5", n)
	}
}

func TestHas(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.NewObject(map[string]any{"x": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	if !obj.Has("x") {
		t.Fatal("expected Has('x') to be true")
	}
	if obj.Has("y") {
		t.Fatal("expected Has('y') to be false")
	}
}

func TestDelete(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.NewObject(map[string]any{"a": 1.0, "b": 2.0})
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	if err := obj.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if obj.Has("a") {
		t.Fatal("'a' should have been deleted")
	}
	if !obj.Has("b") {
		t.Fatal("'b' should still exist")
	}
}

func TestIndex(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	arr, err := r.NewArray(10.0, 20.0, 30.0)
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	v := arr.Index(1)
	if v == nil {
		t.Fatal("Index(1) returned nil")
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 20.0 {
		t.Fatalf("got %f, want 20.0", f)
	}

	// Out of bounds.
	if arr.Index(10) != nil {
		t.Fatal("Index(10) should be nil for out-of-bounds")
	}
}

func TestIsArrayAndIsFunction(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	arr, err := r.Eval("[1,2,3]")
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	fn, err := r.Eval("(function(){})")
	if err != nil {
		t.Fatal(err)
	}
	defer fn.Close()

	num, err := r.Eval("42")
	if err != nil {
		t.Fatal(err)
	}
	defer num.Close()

	if !arr.IsArray() {
		t.Fatal("array should be IsArray")
	}
	if arr.IsFunction() {
		t.Fatal("array should not be IsFunction")
	}
	if !fn.IsFunction() {
		t.Fatal("function should be IsFunction")
	}
	if fn.IsArray() {
		t.Fatal("function should not be IsArray")
	}
	if num.IsArray() || num.IsFunction() {
		t.Fatal("number should not be array or function")
	}
}

func TestToMap(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	obj, err := r.Eval(`({name: "Alice", age: 30, tags: ["go", "js"]})`)
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()

	m, err := obj.ToMap()
	if err != nil {
		t.Fatal(err)
	}
	if m["name"] != "Alice" {
		t.Fatalf("name: got %v, want Alice", m["name"])
	}
	if m["age"] != 30.0 {
		t.Fatalf("age: got %v, want 30", m["age"])
	}
	tags, ok := m["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("tags: got %v", m["tags"])
	}
}

func TestToSlice(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	arr, err := r.Eval(`[1, "two", true, null]`)
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	s, err := arr.ToSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 4 {
		t.Fatalf("got %d items, want 4", len(s))
	}
	if s[0] != 1.0 {
		t.Fatalf("s[0]: got %v", s[0])
	}
	if s[1] != "two" {
		t.Fatalf("s[1]: got %v", s[1])
	}
}

// --- TypedArray / ArrayBuffer ---

func TestUint8ArrayRoundTrip(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	data := []byte{0, 1, 2, 255, 128, 64}
	arr, err := r.NewUint8Array(data)
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	got, err := arr.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("got %v, want %v", got, data)
	}
}

func TestUint8ArrayEmpty(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	arr, err := r.NewUint8Array([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	defer arr.Close()

	got, err := arr.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestArrayBufferFromJS(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	v, err := r.Eval("new Uint8Array([10, 20, 30])")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	got, err := v.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 20, 30}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUint8ArrayGoToJS(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()
	if r.Engine() == "quickjs" {
		t.Skip("QuickJS Call() does not yet convert []byte to Uint8Array")
	}

	// Pass []byte through goToJS via Call.
	fn, err := r.Eval("(function(arr) { return arr.length; })")
	if err != nil {
		t.Fatal(err)
	}
	defer fn.Close()

	result, err := fn.Call([]byte{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()

	f, err := result.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 5.0 {
		t.Fatalf("got %f, want 5.0", f)
	}
}

// --- Dependencies (npm bundling) ---

func skipIfNoJSCOrPM(t *testing.T) *ramune.Runtime {
	t.Helper()
	if _, err := exec.LookPath("bun"); err != nil {
		if _, err := exec.LookPath("npm"); err != nil {
			t.Skip("npm or bun not available")
		}
	}
	// Clear cache to ensure a clean test.
	ramune.ClearCache()

	// Force GC to finalize any lingering Runtimes from previous tests.
	// Their finalizers call JSValueUnprotect which can SIGTRAP if
	// they run concurrently with a new Runtime's JSC initialization.
	runtime.GC()
	runtime.GC()

	r, err := ramune.New(ramune.Dependencies("lodash@4"))
	if err != nil {
		t.Skipf("JSC or npm not available: %v", err)
	}
	return r
}

func TestDependenciesLodash(t *testing.T) {
	r := skipIfNoJSCOrPM(t)
	defer r.Close()

	v, err := r.Eval(`JSON.stringify(lodash.chunk([1,2,3,4,5,6], 2))`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	want := "[[1,2],[3,4],[5,6]]"
	if s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}

func TestDependenciesDayjs(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		if _, err := exec.LookPath("npm"); err != nil {
			t.Skip("npm or bun not available")
		}
	}

	r, err := ramune.New(ramune.Dependencies("dayjs"))
	if err != nil {
		t.Skipf("JSC or npm not available: %v", err)
	}
	defer r.Close()

	v, err := r.Eval(`dayjs("2025-01-15").format("YYYY-MM-DD")`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "2025-01-15" {
		t.Fatalf("got %q, want %q", s, "2025-01-15")
	}
}

func TestDependenciesCache(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		if _, err := exec.LookPath("npm"); err != nil {
			t.Skip("npm or bun not available")
		}
	}
	ramune.ClearCache()

	// First call: installs and bundles.
	r1, err := ramune.New(ramune.Dependencies("lodash@4"))
	if err != nil {
		t.Skipf("JSC or npm not available: %v", err)
	}
	r1.Close()

	// Second call: should use cache (just verify it works).
	r2, err := ramune.New(ramune.Dependencies("lodash@4"))
	if err != nil {
		t.Fatalf("cached run failed: %v", err)
	}
	defer r2.Close()

	v, err := r2.Eval(`JSON.stringify(lodash.flatten([1, [2, [3]]]))`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "[1,2,[3]]" {
		t.Fatalf("got %q, want %q", s, "[1,2,[3]]")
	}
}

// --- Multi-Runtime tests ---

func TestMultipleRuntimes(t *testing.T) {
	r1 := newOrSkip(t)
	defer r1.Close()

	r2, err := ramune.New()
	if err != nil {
		t.Fatalf("second runtime: %v", err)
	}
	defer r2.Close()

	// Each runtime has its own independent global state.
	if err := r1.Exec("globalThis.x = 10"); err != nil {
		t.Fatal(err)
	}
	if err := r2.Exec("globalThis.x = 20"); err != nil {
		t.Fatal(err)
	}

	v1, err := r1.Eval("globalThis.x")
	if err != nil {
		t.Fatal(err)
	}
	defer v1.Close()

	v2, err := r2.Eval("globalThis.x")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()

	f1, _ := v1.Float64()
	f2, _ := v2.Float64()
	if f1 != 10 {
		t.Fatalf("r1: got %f, want 10", f1)
	}
	if f2 != 20 {
		t.Fatalf("r2: got %f, want 20", f2)
	}
}

func TestConcurrentRuntimeEval(t *testing.T) {
	const N = 4
	runtimes := make([]*ramune.Runtime, N)
	for i := range runtimes {
		r, err := ramune.New()
		if err != nil {
			if i == 0 {
				t.Skipf("JSC not available: %v", err)
			}
			t.Fatalf("runtime %d: %v", i, err)
		}
		defer r.Close()
		runtimes[i] = r
	}

	// Run eval concurrently on all runtimes.
	errs := make(chan error, N)
	for i, r := range runtimes {
		go func(idx int, rt *ramune.Runtime) {
			v, err := rt.Eval("1 + " + strings.Repeat("1+", idx) + "0")
			if err != nil {
				errs <- err
				return
			}
			defer v.Close()
			f, _ := v.Float64()
			if int(f) != idx+1 {
				errs <- errors.New("wrong result")
				return
			}
			errs <- nil
		}(i, r)
	}

	for i := 0; i < N; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
