package ramune_test

import (
	"testing"

	"github.com/i2y/ramune"
)

func TestWithModule(t *testing.T) {
	mod := ramune.Module{
		Name: "mymath",
		Exports: map[string]ramune.GoFunc{
			"add": func(args []any) (any, error) {
				a, _ := args[0].(float64)
				b, _ := args[1].(float64)
				return a + b, nil
			},
			"mul": func(args []any) (any, error) {
				a, _ := args[0].(float64)
				b, _ := args[1].(float64)
				return a * b, nil
			},
		},
	}

	r, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.Eval(`var m = require('mymath'); m.add(3, 4)`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 7 {
		t.Fatalf("add: got %f, want 7", f)
	}

	v2, err := r.Eval(`require('mymath').mul(5, 6)`)
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	f2, _ := v2.Float64()
	if f2 != 30 {
		t.Fatalf("mul: got %f, want 30", f2)
	}
}

func TestLoadModule(t *testing.T) {
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	mod := ramune.Module{
		Name: "greeter",
		Exports: map[string]ramune.GoFunc{
			"hello": func(args []any) (any, error) {
				name, _ := args[0].(string)
				return "Hello, " + name + "!", nil
			},
		},
	}

	if err := r.LoadModule(mod); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval(`require('greeter').hello('World')`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "Hello, World!" {
		t.Fatalf("got %q, want %q", s, "Hello, World!")
	}
}

func TestModuleInit(t *testing.T) {
	initCalled := false
	mod := ramune.Module{
		Name: "inittest",
		Exports: map[string]ramune.GoFunc{
			"check": func(args []any) (any, error) { return initCalled, nil },
		},
		Init: func(rt *ramune.Runtime) error {
			initCalled = true
			return nil
		},
	}

	r, err := ramune.New(ramune.NodeCompat(), ramune.WithModule(mod))
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	if !initCalled {
		t.Fatal("Init was not called")
	}
}
