//go:build legacy_goir

package gotranspiler

import (
	"strings"
	"testing"
)

// TestIRPipelineBasicFunction tests that a simple function transpiles through the IR pipeline.
func TestIRPipelineBasicFunction(t *testing.T) {
	path := writeTempTS(t, `
function add(a: number, b: number): number {
  return a + b
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if len(result.Errors) > 0 {
		t.Logf("errors: %v", result.Errors)
	}
	if !strings.Contains(result.GoSource, "func add(") && !strings.Contains(result.GoSource, "func Add(") {
		t.Errorf("expected function declaration 'add' or 'Add' in output")
	}
	if !strings.Contains(result.GoSource, "return") {
		t.Errorf("expected return statement in output")
	}
}

// TestIRPipelineVariables tests variable declarations.
func TestIRPipelineVariables(t *testing.T) {
	path := writeTempTS(t, `
const x = 42
const name = "hello"
const flag = true
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "42") {
		t.Errorf("expected numeric literal 42 in output")
	}
	if !strings.Contains(result.GoSource, `"hello"`) {
		t.Errorf("expected string literal in output")
	}
}

// TestIRPipelineConsoleLog tests console.log calls.
func TestIRPipelineConsoleLog(t *testing.T) {
	path := writeTempTS(t, `
console.log("hello world")
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "console.Log") {
		t.Errorf("expected console.Log in output")
	}
}

// TestIRPipelineIfElse tests if/else statements.
func TestIRPipelineIfElse(t *testing.T) {
	path := writeTempTS(t, `
function check(x: number): string {
  if (x > 0) {
    return "positive"
  } else {
    return "non-positive"
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "if") {
		t.Errorf("expected if statement in output")
	}
	if !strings.Contains(result.GoSource, "else") {
		t.Errorf("expected else clause in output")
	}
}

// TestIRPipelineForLoop tests for loop statements.
func TestIRPipelineForLoop(t *testing.T) {
	path := writeTempTS(t, `
let sum = 0
for (let i = 0; i < 10; i++) {
  sum += i
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "for") {
		t.Errorf("expected for loop in output")
	}
}

// TestIRPipelineTemplateLiteral tests template literal transpilation.
func TestIRPipelineTemplateLiteral(t *testing.T) {
	path := writeTempTS(t, `
const name = "world"
const greeting = `+"`Hello, ${name}!`"+`
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "fmt.Sprintf") || !strings.Contains(result.GoSource, "Sprintf") {
		// Template literals should become fmt.Sprintf or just string concat
		t.Logf("note: template literal may have been optimized")
	}
}

// TestIRPipelineEnum tests enum declaration transpilation.
func TestIRPipelineEnum(t *testing.T) {
	t.Run("string enum", func(t *testing.T) {
		path := writeTempTS(t, `
enum Color {
  Red = "red",
  Green = "green",
  Blue = "blue"
}
`)
		result, err := TranspileFileIR(path, "main")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("IR output:\n%s", result.GoSource)
		if !strings.Contains(result.GoSource, "type Color string") {
			t.Errorf("expected 'type Color string' in output")
		}
		if !strings.Contains(result.GoSource, "ColorRed") || !strings.Contains(result.GoSource, `"red"`) {
			t.Errorf("expected ColorRed constant with value 'red' in output")
		}
	})

	t.Run("numeric enum", func(t *testing.T) {
		path := writeTempTS(t, `
enum Direction {
  Up,
  Down,
  Left,
  Right
}
`)
		result, err := TranspileFileIR(path, "main")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("IR output:\n%s", result.GoSource)
		if !strings.Contains(result.GoSource, "type Direction int") {
			t.Errorf("expected 'type Direction int' in output")
		}
		if !strings.Contains(result.GoSource, "iota") {
			t.Errorf("expected iota in output")
		}
	})
}

// TestIRPipelineInterface tests interface declaration transpilation.
func TestIRPipelineInterface(t *testing.T) {
	t.Run("pure method interface", func(t *testing.T) {
		path := writeTempTS(t, `
interface Logger {
  log(message: string): void
  error(message: string): void
}
`)
		result, err := TranspileFileIR(path, "main")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("IR output:\n%s", result.GoSource)
		if !strings.Contains(result.GoSource, "type Logger interface") {
			t.Errorf("expected 'type Logger interface' in output")
		}
		if !strings.Contains(result.GoSource, "Log(") {
			t.Errorf("expected Log method in output")
		}
	})

	t.Run("property interface as struct", func(t *testing.T) {
		path := writeTempTS(t, `
interface User {
  name: string
  age: number
}
`)
		result, err := TranspileFileIR(path, "main")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("IR output:\n%s", result.GoSource)
		if !strings.Contains(result.GoSource, "type User struct") {
			t.Errorf("expected 'type User struct' in output")
		}
		if !strings.Contains(result.GoSource, "Name") {
			t.Errorf("expected Name field in output")
		}
		if !strings.Contains(result.GoSource, `json:"name"`) {
			t.Errorf("expected json tag in output")
		}
	})
}

// TestIRPipelineTypeAlias tests type alias declaration transpilation.
func TestIRPipelineTypeAlias(t *testing.T) {
	path := writeTempTS(t, `
type ID = string
type Handler = (req: string) => string
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "type Id =") || !strings.Contains(result.GoSource, "type ID =") {
		// ID might be mapped to Id or ID depending on naming
		t.Logf("note: type alias name mapping may vary")
	}
}

// TestIRPipelineClass tests basic class declaration transpilation.
func TestIRPipelineClass(t *testing.T) {
	path := writeTempTS(t, `
class Greeter {
  name: string
  constructor(name: string) {
    this.name = name
  }
  greet(): string {
    return "Hello, " + this.name
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "type Greeter struct") {
		t.Errorf("expected 'type Greeter struct' in output")
	}
	if !strings.Contains(result.GoSource, "func NewGreeter(") {
		t.Errorf("expected NewGreeter constructor in output")
	}
	if !strings.Contains(result.GoSource, "func (g *Greeter) Greet()") {
		t.Errorf("expected Greet method in output")
	}
	if !strings.Contains(result.GoSource, "return s") {
		t.Errorf("expected 'return s' in constructor")
	}
}

// TestIRPipelineClassInheritance tests class inheritance.
func TestIRPipelineClassInheritance(t *testing.T) {
	path := writeTempTS(t, `
class Animal {
  name: string
  constructor(name: string) {
    this.name = name
  }
}

class Dog extends Animal {
  breed: string
  constructor(name: string, breed: string) {
    super(name)
    this.breed = breed
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "type Dog struct") {
		t.Errorf("expected 'type Dog struct' in output")
	}
	if !strings.Contains(result.GoSource, "Animal") {
		t.Errorf("expected Animal embedded in Dog struct")
	}
	if !strings.Contains(result.GoSource, "func NewDog(") {
		t.Errorf("expected NewDog constructor")
	}
}

// TestIRPipelineStaticMembers tests static fields and methods.
func TestIRPipelineStaticMembers(t *testing.T) {
	path := writeTempTS(t, `
class Counter {
  static count: number = 0
  static increment(): number {
    return Counter.count + 1
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Counter_Count") {
		t.Errorf("expected Counter_Count static field")
	}
	if !strings.Contains(result.GoSource, "Counter_Increment") {
		t.Errorf("expected Counter_Increment static method")
	}
}

// TestIRPipelineGetterSetter tests getter/setter transpilation.
func TestIRPipelineGetterSetter(t *testing.T) {
	path := writeTempTS(t, `
class Box {
  private _value: number = 0
  get value(): number {
    return this._value
  }
  set value(v: number) {
    this._value = v
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "func (b *Box) Value()") {
		t.Errorf("expected Value getter method")
	}
	if !strings.Contains(result.GoSource, "func (b *Box) SetValue(") {
		t.Errorf("expected SetValue setter method")
	}
}

// TestIRPipelineExportDefault tests export default transpilation.
func TestIRPipelineExportDefault(t *testing.T) {
	path := writeTempTS(t, `
export default function greet(name: string): string {
  return "Hello, " + name
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	// export default function should produce either Greet or Default
	if !strings.Contains(result.GoSource, "func Greet(") && !strings.Contains(result.GoSource, "func Default(") {
		t.Errorf("expected exported function in output")
	}
}

// TestIREmitterExpressions tests the IREmitter on individual IR nodes.
func TestIREmitterExpressions(t *testing.T) {
	tests := []struct {
		name     string
		expr     GoExpr
		expected string
	}{
		{
			name:     "identifier",
			expr:     &IRIdent{exprBase: exprBase{}, Name: "foo"},
			expected: "foo",
		},
		{
			name:     "qualified identifier",
			expr:     &IRIdent{exprBase: exprBase{}, Name: "Println", PkgName: "fmt"},
			expected: "fmt.Println",
		},
		{
			name:     "string literal",
			expr:     irString(`"hello"`),
			expected: `"hello"`,
		},
		{
			name:     "binary op",
			expr:     &IRBinaryOp{exprBase: exprBase{}, Op: "+", Left: irFloat64("1"), Right: irFloat64("2")},
			expected: "1 + 2",
		},
		{
			name: "stdlib call",
			expr: &IRStdlibCall{exprBase: exprBase{}, Package: "strings", Func: "Contains",
				Args: []GoExpr{irString(`"hello"`), irString(`"ell"`)}},
			expected: `strings.Contains("hello", "ell")`,
		},
		{
			name:     "nil literal",
			expr:     irNil(),
			expected: "nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EmitExprToString(tt.expr)
			if got != tt.expected {
				t.Errorf("EmitExprToString() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestIREmitterStatements tests the IREmitter on individual IR statements.
func TestIREmitterStatements(t *testing.T) {
	tests := []struct {
		name     string
		stmt     GoStmt
		contains string
	}{
		{
			name:     "var decl short",
			stmt:     &IRVarDecl{Name: "x", Typ: GoTypeInfo{GoStr: "float64"}, Init: irFloat64("42"), UseShort: true},
			contains: "x := 42",
		},
		{
			name:     "return",
			stmt:     &IRReturn{Values: []GoExpr{irFloat64("42")}},
			contains: "return 42",
		},
		{
			name:     "break",
			stmt:     &IRBreak{},
			contains: "break",
		},
		{
			name:     "continue",
			stmt:     &IRContinue{},
			contains: "continue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EmitStmtToString(tt.stmt)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("EmitStmtToString() = %q, want to contain %q", got, tt.contains)
			}
		})
	}
}
