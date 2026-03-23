package bench_test

import (
	"testing"

	"github.com/dop251/goja"
	"github.com/i2y/ramune"
	"github.com/robertkrimen/otto"
)

const fibJS = `function fib(n) { return n <= 1 ? n : fib(n-1) + fib(n-2); }`
const jsonJS = `
var arr = [];
for (var i = 0; i < 10000; i++) {
	arr.push({id: i, name: "item" + i, value: Math.sqrt(i)});
}
JSON.stringify(arr).length;
`

func BenchmarkFib35_Ramune(b *testing.B) {
	rt, err := ramune.New()
	if err != nil {
		b.Skip("JSC not available")
	}
	defer rt.Close()
	rt.Exec(fibJS)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := rt.Eval("fib(35)")
		if v != nil {
			v.Close()
		}
	}
}

func BenchmarkFib35_Goja(b *testing.B) {
	vm := goja.New()
	vm.RunString(fibJS)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.RunString("fib(35)")
	}
}

func BenchmarkFib35_Otto(b *testing.B) {
	vm := otto.New()
	vm.Run(fibJS)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Run("fib(35)")
	}
}

func BenchmarkJSON10K_Ramune(b *testing.B) {
	rt, err := ramune.New()
	if err != nil {
		b.Skip("JSC not available")
	}
	defer rt.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := rt.Eval(jsonJS)
		if v != nil {
			v.Close()
		}
	}
}

func BenchmarkJSON10K_Goja(b *testing.B) {
	vm := goja.New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.RunString(jsonJS)
	}
}

func BenchmarkJSON10K_Otto(b *testing.B) {
	vm := otto.New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Run(jsonJS)
	}
}
