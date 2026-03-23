package ramune_test

import (
	"testing"
)

type testUser struct {
	Name  string `js:"name"`
	Age   int    `js:"age"`
	Email string // no js tag → "email"
}

func (u *testUser) Greet() string {
	return "Hello, " + u.Name
}

func (u *testUser) SetAge(age int) {
	u.Age = age
}

func (u *testUser) Add(a, b int) int {
	return a + b
}

func TestBindBasic(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	user := &testUser{Name: "Alice", Age: 30, Email: "alice@example.com"}
	if err := r.Bind("user", user); err != nil {
		t.Fatal(err)
	}

	// Read name.
	v, err := r.Eval("user.name")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "Alice" {
		t.Fatalf("user.name = %q, want %q", s, "Alice")
	}

	// Read age.
	v2, err := r.Eval("user.age")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	f, _ := v2.Float64()
	if f != 30.0 {
		t.Fatalf("user.age = %f, want 30", f)
	}

	// Read email (no js tag → lowercased first letter).
	v3, err := r.Eval("user.email")
	if err != nil {
		t.Fatal(err)
	}
	defer v3.Close()
	s3, _ := v3.GoString()
	if s3 != "alice@example.com" {
		t.Fatalf("user.email = %q, want %q", s3, "alice@example.com")
	}
}

func TestBindMethod(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	user := &testUser{Name: "Bob", Age: 25}
	if err := r.Bind("user", user); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("user.greet()")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "Hello, Bob" {
		t.Fatalf("user.greet() = %q, want %q", s, "Hello, Bob")
	}
}

func TestBindMethodMutate(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	user := &testUser{Name: "Charlie", Age: 20}
	if err := r.Bind("user", user); err != nil {
		t.Fatal(err)
	}

	// Call setAge to mutate the struct.
	if err := r.Exec("user.setAge(35)"); err != nil {
		t.Fatal(err)
	}

	// Verify Go struct was updated.
	if user.Age != 35 {
		t.Fatalf("Go struct Age = %d, want 35", user.Age)
	}

	// Verify JS reads updated value.
	v, err := r.Eval("user.age")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 35.0 {
		t.Fatalf("user.age after setAge = %f, want 35", f)
	}
}

func TestBindMethodWithArgs(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	user := &testUser{Name: "Dave", Age: 40}
	if err := r.Bind("calc", user); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("calc.add(3, 4)")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	f, _ := v.Float64()
	if f != 7.0 {
		t.Fatalf("calc.add(3,4) = %f, want 7", f)
	}
}

func TestBindFieldSetter(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	user := &testUser{Name: "Eve", Age: 28}
	if err := r.Bind("user", user); err != nil {
		t.Fatal(err)
	}

	// Set name via JS.
	if err := r.Exec("user.name = 'Fiona'"); err != nil {
		t.Fatal(err)
	}

	// Verify Go struct was updated.
	if user.Name != "Fiona" {
		t.Fatalf("Go struct Name = %q, want %q", user.Name, "Fiona")
	}

	// Verify the method sees the updated name.
	v, err := r.Eval("user.greet()")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "Hello, Fiona" {
		t.Fatalf("user.greet() = %q, want %q", s, "Hello, Fiona")
	}
}

func TestBindNonPointer(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	err := r.Bind("x", testUser{Name: "fail"})
	if err == nil {
		t.Fatal("expected error for non-pointer value")
	}
}
