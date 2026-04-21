package picker_test

import (
	"strings"
	"testing"

	"github.com/i2y/ramune/internal/gotranspiler/picker"
)

func TestPicker_Class_Accept_MinimalCounter(t *testing.T) {
	src := `
export class Counter {
  value: number;
  constructor(initial: number) { this.value = initial; }
  increment(): void { this.value = this.value + 1; }
  total(x: number): number { return this.value + x; }
}`
	res := pickOne(t, src)
	c, ok := byName(res, "Counter")
	if !ok {
		t.Fatalf("candidate `Counter` not found; got %+v", res.Candidates)
	}
	if !c.Extracted {
		t.Fatalf("expected Counter extracted, got reason %+v", c.Reason)
	}
	if c.Kind != picker.KindClass {
		t.Fatalf("expected KindClass, got %v", c.Kind)
	}
}

func TestPicker_Class_Accept_NoConstructor(t *testing.T) {
	src := `
export class Box {
  a: number;
  b: number;
  area(): number { return this.a * this.b; }
}`
	res := pickOne(t, src)
	c, ok := byName(res, "Box")
	if !ok || !c.Extracted {
		t.Fatalf("expected Box extracted, got %+v", res.Candidates)
	}
}

func TestPicker_Class_Accept_FieldMutationAndMethodCall(t *testing.T) {
	src := `
export class Adder {
  total: number;
  constructor() { this.total = 0; }
  add(x: number): void { this.total = this.total + x; }
  reset(): void { this.add(0); this.total = 0; }
}`
	res := pickOne(t, src)
	c, ok := byName(res, "Adder")
	if !ok || !c.Extracted {
		t.Fatalf("expected Adder extracted, got %+v", res.Candidates)
	}
}

func TestPicker_Class_Reject_Generic(t *testing.T) {
	src := `
export class Box<T> {
  v: T;
  constructor(x: T) { this.v = x; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "Box")
	if c.Extracted {
		t.Fatalf("expected Box rejected (generic), got extracted")
	}
	if c.Reason.Code != "generic-func" {
		t.Fatalf("want generic-func reason, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_Extends(t *testing.T) {
	src := `
export class Base { v: number; constructor() { this.v = 0; } }
export class Sub extends Base { w: number; constructor() { super(); this.w = 0; } }`
	res := pickOne(t, src)
	c, _ := byName(res, "Sub")
	if c.Extracted {
		t.Fatalf("expected Sub rejected (extends)")
	}
	if c.Reason.Code != "class-heritage" {
		t.Fatalf("want class-heritage reason, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_StaticMember(t *testing.T) {
	src := `
export class K {
  v: number;
  static PI: number;
  constructor() { this.v = 0; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "K")
	if c.Extracted {
		t.Fatalf("expected K rejected (static)")
	}
	if c.Reason.Code != "class-static" {
		t.Fatalf("want class-static reason, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_PrivateField(t *testing.T) {
	src := `
export class P {
  #secret: number;
  constructor() { this.#secret = 0; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "P")
	if c.Extracted {
		t.Fatalf("expected P rejected (# private)")
	}
	if c.Reason.Code != "class-private" {
		t.Fatalf("want class-private reason, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_Getter(t *testing.T) {
	src := `
export class G {
  v: number;
  constructor() { this.v = 0; }
  get doubled(): number { return this.v * 2; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "G")
	if c.Extracted {
		t.Fatalf("expected G rejected (getter)")
	}
	if c.Reason.Code != "class-accessor" {
		t.Fatalf("want class-accessor reason, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_FieldInitializer(t *testing.T) {
	src := `
export class F {
  v: number = 10;
  constructor() {}
}`
	res := pickOne(t, src)
	c, _ := byName(res, "F")
	if c.Extracted {
		t.Fatalf("expected F rejected (field initializer)")
	}
	if !strings.Contains(c.Reason.Detail, "initializer") {
		t.Fatalf("want initializer reason, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_ObjectField(t *testing.T) {
	src := `
export class O {
  data: { a: number };
  constructor() { this.data = { a: 0 }; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "O")
	if c.Extracted {
		t.Fatalf("expected O rejected (anonymous-object field)")
	}
}

func TestPicker_Class_Reject_NonAssignmentConstructorStmt(t *testing.T) {
	src := `
export class A {
  v: number;
  constructor(n: number) {
    let k = n + 1;
    this.v = k;
  }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "A")
	if c.Extracted {
		t.Fatalf("expected A rejected (local var in constructor)")
	}
}

func TestPicker_Class_Reject_UnknownThisField(t *testing.T) {
	src := `
export class U {
  v: number;
  constructor() { this.other = 1; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "U")
	if c.Extracted {
		t.Fatalf("expected U rejected (unknown this field)")
	}
}

func TestPicker_Class_Reject_ParameterProperty(t *testing.T) {
	src := `
export class PP {
  constructor(public v: number) {}
}`
	res := pickOne(t, src)
	c, _ := byName(res, "PP")
	if c.Extracted {
		t.Fatalf("expected PP rejected (parameter property)")
	}
	if c.Reason.Code != "class-param-property" {
		t.Fatalf("want class-param-property, got %+v", c.Reason)
	}
}

func TestPicker_Class_Reject_MethodWithClosureCapture(t *testing.T) {
	src := `
const BONUS = 5;
export class R {
  v: number;
  constructor() { this.v = 0; }
  apply(): number { return this.v + BONUS; }
}`
	res := pickOne(t, src)
	c, _ := byName(res, "R")
	if c.Extracted {
		t.Fatalf("expected R rejected (closure capture)")
	}
}
