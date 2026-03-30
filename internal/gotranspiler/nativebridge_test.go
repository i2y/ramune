package gotranspiler

import (
	"testing"
)

func TestDiscoverExportedFuncs(t *testing.T) {
	src := `package math

func Fibonacci(n float64) float64 { return n }
func IsPrime(n float64) bool { return true }
func add(a, b float64) float64 { return a + b }
func NewCounter(name string) *Counter { return nil }
func init() {}

type Counter struct{ Count float64 }
func (c *Counter) Increment() float64 { return 0 }
`

	funcs, err := DiscoverExportedFuncs(src)
	if err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.GoName] = true
	}

	// Exported top-level functions should be found
	if !names["Fibonacci"] {
		t.Error("expected Fibonacci")
	}
	if !names["IsPrime"] {
		t.Error("expected IsPrime")
	}
	if !names["NewCounter"] {
		t.Error("expected NewCounter")
	}

	// Unexported, init, and methods should be excluded
	if names["add"] {
		t.Error("unexported 'add' should not be discovered")
	}
	if names["init"] {
		t.Error("init should not be discovered")
	}
	if names["Increment"] {
		t.Error("methods should not be discovered")
	}
}

func TestDiscoverExportedFuncsGeneric(t *testing.T) {
	src := `package utils

func Identity[T any](x T) T { return x }
func Filter[T any](a []T, fn func(T, int) bool) []T { return nil }
func Add(a, b float64) float64 { return a + b }
`

	funcs, err := DiscoverExportedFuncs(src)
	if err != nil {
		t.Fatal(err)
	}

	generic := 0
	nonGeneric := 0
	for _, f := range funcs {
		if f.Generic {
			generic++
		} else {
			nonGeneric++
		}
	}

	if generic != 2 {
		t.Errorf("expected 2 generic funcs, got %d", generic)
	}
	if nonGeneric != 1 {
		t.Errorf("expected 1 non-generic func, got %d", nonGeneric)
	}
}

func TestGoNameToJS(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Fibonacci", "fibonacci"},
		{"IsPrime", "isPrime"},
		{"NewCounter", "newCounter"},
		{"URL", "url"},
		{"HTTPServer", "httpServer"},
		{"X", "x"},
		{"", ""},
	}

	for _, tt := range tests {
		got := goNameToJS(tt.input)
		if got != tt.want {
			t.Errorf("goNameToJS(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateBridgeCode(t *testing.T) {
	funcs := []ExportedFunc{
		{GoName: "Add", JSName: "add"},
		{GoName: "Multiply", JSName: "multiply"},
		{GoName: "Filter", JSName: "filter", Generic: true},
	}

	code := GenerateBridgeCode("native:math", "nativemath", funcs)

	// Non-generic functions should be included
	if !contains(code, `"add": nativemath.Add`) {
		t.Error("expected Add in bridge code")
	}
	if !contains(code, `"multiply": nativemath.Multiply`) {
		t.Error("expected Multiply in bridge code")
	}

	// Generic functions should be skipped
	if contains(code, `nativemath.Filter`) {
		t.Error("generic Filter should not be directly referenced")
	}
}

func TestGenericWarnings(t *testing.T) {
	funcs := []ExportedFunc{
		{GoName: "Add", JSName: "add"},
		{GoName: "Filter", JSName: "filter", Generic: true},
		{GoName: "Map", JSName: "map", Generic: true},
	}

	warnings := GenericWarnings(funcs)
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(warnings))
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
