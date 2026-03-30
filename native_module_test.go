package ramune_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/i2y/ramune"
)

// --- Test structs ---

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Counter struct {
	Count float64
	Name  string
}

func NewCounter(name string) *Counter {
	return &Counter{Name: name, Count: 0}
}

func (c *Counter) Increment() float64 {
	c.Count++
	return c.Count
}

func (c *Counter) Decrement() float64 {
	c.Count--
	return c.Count
}

func (c *Counter) Describe() string {
	return c.Name + ": active"
}

// --- NativeModuleFromFuncs tests ---

func TestNativeModuleFromFuncsBasic(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:math", map[string]any{
		"add": func(a, b float64) float64 { return a + b },
		"mul": func(a, b float64) float64 { return a * b },
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`require('native:math').add(3, 4)`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, _ := v.Float64()
	if f != 7 {
		t.Errorf("expected 7, got %v", f)
	}
}

func TestNativeModuleFromFuncsStringReturn(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:greet", map[string]any{
		"hello": func(name string) string { return "Hello, " + name + "!" },
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`require('native:greet').hello("World")`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", s)
	}
}

func TestNativeModuleFromFuncsBoolReturn(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:check", map[string]any{
		"isPositive": func(n float64) bool { return n > 0 },
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`require('native:check').isPositive(5)`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	b, _ := v.Bool()
	if !b {
		t.Error("expected true")
	}
}

func TestNativeModuleFromFuncsErrorReturn(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:div", map[string]any{
		"divide": func(a, b float64) (float64, error) {
			if b == 0 {
				return 0, &ramune.JSError{Message: "division by zero"}
			}
			return a / b, nil
		},
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Success case
	v, err := rt.Eval(`require('native:div').divide(10, 2)`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 5 {
		t.Errorf("expected 5, got %v", f)
	}
}

// --- Struct return/param tests ---

func TestNativeModuleStructReturn(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:geo", map[string]any{
		"makePoint": func(x, y float64) Point { return Point{X: x, Y: y} },
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`JSON.stringify(require('native:geo').makePoint(3, 4))`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != `{"x":3,"y":4}` {
		t.Errorf("expected {\"x\":3,\"y\":4}, got %q", s)
	}
}

func TestNativeModuleStructParam(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:geo", map[string]any{
		"distance": func(a, b Point) float64 {
			dx := a.X - b.X
			dy := a.Y - b.Y
			return math.Sqrt(dx*dx + dy*dy)
		},
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`require('native:geo').distance({x:0, y:0}, {x:3, y:4})`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, _ := v.Float64()
	if f != 5 {
		t.Errorf("expected 5, got %v", f)
	}
}

func TestNativeModuleTypedSliceParam(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:arr", map[string]any{
		"sum": func(nums []float64) float64 {
			total := 0.0
			for _, n := range nums {
				total += n
			}
			return total
		},
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`require('native:arr').sum([1, 2, 3, 4, 5])`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, _ := v.Float64()
	if f != 15 {
		t.Errorf("expected 15, got %v", f)
	}
}

// --- Class method + live property tests ---

func TestNativeModuleClassMethods(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:counter", map[string]any{
		"newCounter": NewCounter,
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Test method call
	v, err := rt.Eval(`
		var c = require('native:counter').newCounter("hits");
		c.increment();
		c.increment();
		c.increment();
		c.decrement();
		c.describe();
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "hits: active" {
		t.Errorf("expected 'hits: active', got %q", s)
	}
}

func TestNativeModuleLiveProperties(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:counter", map[string]any{
		"newCounter": NewCounter,
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Test that properties reflect state changes after method calls
	v, err := rt.Eval(`
		var c = require('native:counter').newCounter("hits");
		var r1 = c.count;      // 0
		c.increment();
		c.increment();
		var r2 = c.count;      // 2
		c.count = 100;
		c.increment();
		var r3 = c.count;      // 101
		r1 + "," + r2 + "," + r3;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "0,2,101" {
		t.Errorf("expected '0,2,101', got %q", s)
	}
}

// --- FinalizationRegistry / instance lifecycle test ---

func TestNativeModuleFinalizationRegistry(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:counter", map[string]any{
		"newCounter": NewCounter,
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("not available: %v", err)
	}
	defer rt.Close()

	// Verify FinalizationRegistry bridge was installed via Init hook
	v, err := rt.Eval(`typeof globalThis.__nativeInstanceRegistry`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	regType, _ := v.GoString()
	t.Logf("__nativeInstanceRegistry: %s", regType)

	// Verify release function exists
	rv, err := rt.Eval(`typeof __nativeRelease`)
	if err != nil {
		t.Fatal(err)
	}
	defer rv.Close()
	rvType, _ := rv.GoString()
	if rvType != "function" {
		t.Fatalf("expected __nativeRelease to be function, got %s", rvType)
	}

	// Verify instance counting works
	_, err = rt.Eval(`
		(function() {
			var m = require('native:counter');
			for (var i = 0; i < 10; i++) {
				m.newCounter("tmp" + i);
			}
		})();
	`)
	if err != nil {
		t.Fatal(err)
	}
	count := rt.NativeInstanceCount()
	if count < 10 {
		t.Fatalf("expected at least 10 instances, got %d", count)
	}

	// Verify manual release via __nativeRelease works (simulating what FinalizationRegistry does).
	// Release any valid instance ID and check the count decreases.
	countBefore := count
	if err := rt.Exec(fmt.Sprintf(`__nativeRelease(%d)`, countBefore)); err != nil {
		t.Fatal(err)
	}
	countAfter := rt.NativeInstanceCount()
	if countAfter != countBefore-1 {
		t.Fatalf("expected %d instances after manual release, got %d", countBefore-1, countAfter)
	}
	t.Logf("manual __nativeRelease works: %d → %d", countBefore, countAfter)
}

// --- Panic recovery test ---

func TestNativeModulePanicRecovery(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	mod := ramune.NativeModuleFromFuncs("native:panic", map[string]any{
		"crash": func() { panic("intentional crash") },
	})

	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// Should not crash the runtime — panic is caught and converted to JS exception
	_, err = rt.Eval(`
		try {
			require('native:panic').crash();
		} catch(e) {
			"caught: " + e;
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
}
