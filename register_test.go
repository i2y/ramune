package ramune_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/i2y/ramune"
)

func TestRegisterFloat(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := ramune.Register(r, "add", func(a, b float64) float64 {
		return a + b
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("add(3.5, 4.5)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 8.0 {
		t.Fatalf("got %f, want 8.0", f)
	}
}

func TestRegisterString(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := ramune.Register(r, "greet", func(name string) string {
		return "Hello, " + name
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`greet("World")`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "Hello, World" {
		t.Fatalf("got %q, want %q", s, "Hello, World")
	}
}

func TestRegisterError(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := ramune.Register(r, "divide", func(a, b float64) (float64, error) {
		if b == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return a / b, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test successful division.
	v, err := r.Eval("divide(10, 2)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 5.0 {
		t.Fatalf("got %f, want 5.0", f)
	}

	// Test division by zero — error should be catchable in JS.
	v2, err := r.Eval(`
		var result;
		try { divide(10, 0); result = "no error"; }
		catch(e) { result = "caught: " + e; }
		result;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()

	s, err := v2.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "division by zero") {
		t.Fatalf("got %q, want error containing 'division by zero'", s)
	}
}

func TestRegisterNoReturn(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	var captured string
	err := ramune.Register(r, "capture", func(msg string) {
		captured = msg
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.Eval(`capture("hello from JS")`)
	if err != nil {
		t.Fatal(err)
	}

	if captured != "hello from JS" {
		t.Fatalf("got %q, want %q", captured, "hello from JS")
	}
}

func TestRegisterIntConversion(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := ramune.Register(r, "doubleInt", func(n int) int {
		return n * 2
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("doubleInt(21)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 42.0 {
		t.Fatalf("got %f, want 42.0", f)
	}
}

func TestRegisterBoolParam(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ramune.Register(r, "not", func(b bool) bool { return !b })

	v, err := r.Eval("not(true)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	b, _ := v.Bool()
	if b != false {
		t.Fatal("expected false")
	}
}

func TestRegisterMapParam(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ramune.Register(r, "getKey", func(m map[string]any, key string) any {
		return m[key]
	})

	v, err := r.Eval(`getKey({x: 42}, "x")`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 42.0 {
		t.Fatalf("got %f, want 42", f)
	}
}

func TestRegisterArgCountError(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ramune.Register(r, "needsTwo", func(a, b float64) float64 { return a + b })

	_, err := r.Eval(`
		var result;
		try { needsTwo(1); result = "no error"; }
		catch(e) { result = "caught"; }
		result;
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegisterNotAFunction(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := ramune.Register(r, "bad", 42)
	if err == nil {
		t.Fatal("expected error for non-function")
	}
}
