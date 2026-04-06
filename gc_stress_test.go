package ramune_test

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"

	"github.com/i2y/ramune"
)

// TestGCStress_ObjectAllocation creates many JS objects with GC enabled.
func TestGCStress_ObjectAllocation(t *testing.T) {
	rt, err := ramune.New()
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	debug.SetGCPercent(50)
	defer debug.SetGCPercent(100)

	const iterations = 50000
	failures := 0
	for i := 0; i < iterations; i++ {
		v, err := rt.Eval(fmt.Sprintf(`({x: %d, y: "hello", z: [1,2,3], nested: {a: true}})`, i))
		if err != nil {
			failures++
			if failures <= 3 {
				t.Logf("iteration %d: %v", i, err)
			}
			continue
		}
		if v != nil {
			v.Close()
		}
		if i%1000 == 0 {
			runtime.GC()
		}
	}
	if failures > 0 {
		t.Fatalf("failures: %d/%d (%.1f%%)", failures, iterations, float64(failures)/float64(iterations)*100)
	}
}

// TestGCStress_CallbackHeavy exercises Go↔JS callbacks that return maps.
func TestGCStress_CallbackHeavy(t *testing.T) {
	rt, err := ramune.New()
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	rt.RegisterFunc("goEcho", func(args []any) (any, error) {
		if len(args) > 0 {
			return args[0], nil
		}
		return nil, nil
	})

	const iterations = 30000
	failures := 0
	for i := 0; i < iterations; i++ {
		v, err := rt.Eval(fmt.Sprintf(`goEcho({id: %d, data: "test".repeat(100)})`, i))
		if err != nil {
			failures++
			if failures <= 3 {
				t.Logf("iteration %d: %v", i, err)
			}
			continue
		}
		if v != nil {
			v.Close()
		}
	}
	if failures > 0 {
		t.Fatalf("failures: %d/%d (%.1f%%)", failures, iterations, float64(failures)/float64(iterations)*100)
	}
}

// TestGCStress_ConcurrentAllocGoroutines creates GC pressure from other
// goroutines while JSC operations are running.
func TestGCStress_ConcurrentAllocGoroutines(t *testing.T) {
	rt, err := ramune.New()
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	debug.SetGCPercent(30)
	defer debug.SetGCPercent(100)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := make([]byte, 1<<16)
					_ = s
				}
			}
		}()
	}

	const iterations = 20000
	failures := 0
	for i := 0; i < iterations; i++ {
		v, err := rt.Eval(fmt.Sprintf(`({id: %d, ts: Date.now(), arr: new Array(10).fill(%d)})`, i, i))
		if err != nil {
			failures++
			if failures <= 5 {
				t.Logf("iteration %d: %v", i, err)
			}
			continue
		}
		if v != nil {
			v.Close()
		}
	}

	close(stop)
	wg.Wait()

	if failures > 0 {
		t.Fatalf("failures: %d/%d (%.1f%%)", failures, iterations, float64(failures)/float64(iterations)*100)
	}
}

// BenchmarkGCStress_ObjectCreation benchmarks rapid JS object creation.
func BenchmarkGCStress_ObjectCreation(b *testing.B) {
	rt, err := ramune.New()
	if err != nil {
		b.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := rt.Eval(`({x: 1, y: "hello", z: [1,2,3]})`)
		if err != nil {
			b.Fatal(err)
		}
		v.Close()
	}
}

// BenchmarkGCStress_WithCallbacks benchmarks callback-heavy workload.
func BenchmarkGCStress_WithCallbacks(b *testing.B) {
	rt, err := ramune.New()
	if err != nil {
		b.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	rt.RegisterFunc("noop", func(args []any) (any, error) {
		return "ok", nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := rt.Eval(`noop({x: 1})`)
		if err != nil {
			b.Fatal(err)
		}
		v.Close()
	}
}
