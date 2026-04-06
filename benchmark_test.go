package ramune_test

import (
	"testing"

	"github.com/i2y/ramune"
)

func benchRT(b *testing.B) *ramune.Runtime {
	b.Helper()
	rt, err := ramune.New()
	if err != nil {
		b.Skipf("JSC not available: %v", err)
	}
	b.Cleanup(func() { rt.Close() })
	return rt
}

func BenchmarkEval(b *testing.B) {
	rt := benchRT(b)
	b.ResetTimer()
	for b.Loop() {
		v, _ := rt.Eval("1 + 1")
		v.Close()
	}
}

func BenchmarkExec(b *testing.B) {
	rt := benchRT(b)
	b.ResetTimer()
	for b.Loop() {
		rt.Exec("var x = 1 + 1")
	}
}

func BenchmarkCallFunction(b *testing.B) {
	rt := benchRT(b)
	rt.Exec("function add(a, b) { return a + b; }")
	global := rt.GlobalObject()
	defer global.Close()
	addFn := global.Attr("add")
	defer addFn.Close()
	b.ResetTimer()
	for b.Loop() {
		result, _ := addFn.Call(3.0, 5.0)
		result.Close()
	}
}

func BenchmarkValueConvert(b *testing.B) {
	rt := benchRT(b)
	// Pre-evaluate once and read the value to avoid JSC GC pressure.
	rt.Exec("var __benchVal = 42")
	global := rt.GlobalObject()
	defer global.Close()
	b.ResetTimer()
	for b.Loop() {
		v := global.Attr("__benchVal")
		if v != nil {
			v.Float64()
			v.Close()
		}
	}
}

func BenchmarkEvalString(b *testing.B) {
	rt := benchRT(b)
	b.ResetTimer()
	for b.Loop() {
		v, _ := rt.Eval("'hello world'")
		v.GoString()
		v.Close()
	}
}

// BenchmarkNewObject and BenchmarkNewArray were previously disabled because
// JSC's internal property table became inconsistent when jsObjectMake +
// jsObjectSetProperty were called rapidly (rapid structure transitions).
// This was fixed by switching map/slice conversion to JSON.parse-based approach
// in goToJS, which avoids the C API structure creation path.

func BenchmarkRegisterFuncCall(b *testing.B) {
	rt := benchRT(b)
	rt.RegisterFunc("benchAdd", func(args []any) (any, error) {
		a, _ := args[0].(float64)
		b, _ := args[1].(float64)
		return a + b, nil
	})
	b.ResetTimer()
	for b.Loop() {
		v, _ := rt.Eval("benchAdd(3, 4)")
		v.Close()
	}
}

func BenchmarkAttrAccess(b *testing.B) {
	rt := benchRT(b)
	rt.Exec("var __benchObj = {x: 42, y: 'hello', z: true}")
	global := rt.GlobalObject()
	defer global.Close()
	obj := global.Attr("__benchObj")
	defer obj.Close()
	b.ResetTimer()
	for b.Loop() {
		v := obj.Attr("x")
		if v != nil {
			v.Close()
		}
	}
}

func BenchmarkBatchEval(b *testing.B) {
	rt := benchRT(b)
	b.ResetTimer()
	for b.Loop() {
		// Batch: do multiple operations in one Eval call
		v, _ := rt.Eval(`(function() {
			var obj = {a: 1, b: 2, c: 3};
			return obj.a + obj.b + obj.c;
		})()`)
		v.Close()
	}
}
