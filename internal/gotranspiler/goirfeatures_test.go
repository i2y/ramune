package gotranspiler

import (
	"strings"
	"testing"
)

// This file mirrors features_test.go and goimport_test.go tests
// using the IR pipeline (TranspileFileIR) instead of the old transpiler.

// --- typeof / instanceof / in / delete ---

func TestIRTypeofComparison(t *testing.T) {
	path := writeTempTS(t, `
function check(x: any): boolean {
  if (typeof x === "string") {
    return true
  }
  return false
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, ".(string)") {
		t.Errorf("expected type assertion for typeof string check")
	}
}

func TestIRTypeofNotEquals(t *testing.T) {
	path := writeTempTS(t, `
function check(x: any): boolean {
  return typeof x !== "number"
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "float64") && !strings.Contains(result.GoSource, "int") {
		t.Errorf("expected float64/int type check for typeof number")
	}
}

func TestIRTypeofUndefined(t *testing.T) {
	path := writeTempTS(t, `
function isNil(x: any): boolean {
  return typeof x === "undefined"
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "== nil") {
		t.Errorf("expected nil check for typeof undefined")
	}
}

func TestIRTypeofStandalone(t *testing.T) {
	path := writeTempTS(t, `
function getType(x: any): string {
  return typeof x
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "jsrt.TypeOf(") {
		t.Errorf("expected jsrt.TypeOf call for standalone typeof")
	}
}

func TestIRInstanceof(t *testing.T) {
	path := writeTempTS(t, `
class Dog {
  name: string
  constructor(name: string) { this.name = name }
}
function isDog(x: any): boolean {
  return x instanceof Dog
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Dog") {
		t.Errorf("expected Dog type check for instanceof")
	}
}

func TestIRInOperator(t *testing.T) {
	path := writeTempTS(t, `
function hasKey(obj: Record<string, any>, key: string): boolean {
  return key in obj
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "func() bool") {
		t.Errorf("expected IIFE for in operator")
	}
}

func TestIRDeleteElement(t *testing.T) {
	path := writeTempTS(t, `
const m: Record<string, number> = { a: 1 }
delete m["a"]
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "delete(") {
		t.Errorf("expected Go delete() call for delete operator")
	}
}

// --- static class members ---

func TestIRStaticField(t *testing.T) {
	path := writeTempTS(t, `
class Counter {
  static count: number = 0
  value: number
  constructor() {
    this.value = 0
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Counter_Count") {
		t.Errorf("expected package-level var Counter_Count for static field")
	}
	if !strings.Contains(result.GoSource, "Value float64") {
		t.Errorf("expected Value field in struct")
	}
}

func TestIRStaticMethod(t *testing.T) {
	path := writeTempTS(t, `
class MathUtil {
  static add(a: number, b: number): number {
    return a + b
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "func MathUtil_Add(") {
		t.Errorf("expected package-level func MathUtil_Add for static method")
	}
}

func TestIRStaticAccess(t *testing.T) {
	path := writeTempTS(t, `
class Config {
  static defaultName: string = "world"
}
const name = Config.defaultName
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Config_DefaultName") {
		t.Errorf("expected Config_DefaultName for static access")
	}
}

// --- getter / setter ---

func TestIRClassGetter(t *testing.T) {
	path := writeTempTS(t, `
class Person {
  private _name: string
  constructor(name: string) {
    this._name = name
  }
  get name(): string {
    return this._name
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Name()") {
		t.Errorf("expected getter method Name()")
	}
}

func TestIRClassSetter(t *testing.T) {
	path := writeTempTS(t, `
class Person {
  private _age: number = 0
  set age(value: number) {
    this._age = value
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "SetAge(") {
		t.Errorf("expected setter method SetAge()")
	}
}

func TestIRGetterSetterPair(t *testing.T) {
	path := writeTempTS(t, `
class Temperature {
  private _celsius: number = 0
  get celsius(): number {
    return this._celsius
  }
  set celsius(value: number) {
    this._celsius = value
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Celsius()") {
		t.Errorf("expected getter method Celsius()")
	}
	if !strings.Contains(result.GoSource, "SetCelsius(") {
		t.Errorf("expected setter method SetCelsius()")
	}
}

// --- optional chaining, nullish coalescing, exponentiation ---

func TestIROptionalChainingProperty(t *testing.T) {
	path := writeTempTS(t, `
function getName(obj: any): any {
  return obj?.name
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "jsrt.Obj(") {
		t.Errorf("expected jsrt.Obj for optional chaining on any type")
	}
}

func TestIROptionalChainingCall(t *testing.T) {
	path := writeTempTS(t, `
function callMethod(obj: any): any {
  return obj?.toString()
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	// For any-typed, jsrt.Obj provides nil-safe access; explicit nil check also acceptable
	if !strings.Contains(result.GoSource, "!= nil") && !strings.Contains(result.GoSource, "jsrt.Obj(") {
		t.Errorf("expected nil-safe access for optional call")
	}
}

func TestIRNullishCoalescing(t *testing.T) {
	path := writeTempTS(t, `
function fallback(x: any): any {
  return x ?? "default"
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "!= nil") {
		t.Errorf("expected nil check for nullish coalescing")
	}
	if !strings.Contains(result.GoSource, `"default"`) {
		t.Errorf("expected default value in nullish coalescing")
	}
}

func TestIRExponentiation(t *testing.T) {
	path := writeTempTS(t, `
function power(base: number, exp: number): number {
  return base ** exp
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "math.Pow(") {
		t.Errorf("expected math.Pow for exponentiation")
	}
}

// --- destructuring defaults, logical assignment ---

func TestIRDestructuringObjectDefault(t *testing.T) {
	path := writeTempTS(t, `
interface Config {
  host: string;
  port: number;
}
function parse(config: Config): void {
  const { host = "localhost", port = 8080 } = config
  console.log(host, port)
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, `"localhost"`) {
		t.Errorf("expected default value 'localhost' in output")
	}
	if !strings.Contains(result.GoSource, "8080") {
		t.Errorf("expected default value 8080 in output")
	}
}

func TestIRDestructuringArrayDefault(t *testing.T) {
	path := writeTempTS(t, `
const arr: any[] = [1]
const [a = 10, b = 20] = arr
console.log(a, b)
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "10") && !strings.Contains(result.GoSource, "20") {
		t.Errorf("expected default values in array destructuring")
	}
}

func TestIRLogicalAndAssignment(t *testing.T) {
	path := writeTempTS(t, `
let x: any = true
x &&= "hello"
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "jsrt.ToBool(x)") {
		t.Errorf("expected jsrt.ToBool check for &&=")
	}
}

func TestIRLogicalOrAssignment(t *testing.T) {
	path := writeTempTS(t, `
let x: any = null
x ||= "default"
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "if !") {
		t.Errorf("expected negated conditional for ||=")
	}
}

// --- export default ---

func TestIRExportDefaultExpression(t *testing.T) {
	path := writeTempTS(t, `
export default 42
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Default") {
		t.Errorf("expected Default for export default expression")
	}
}

func TestIRExportDefaultFunction(t *testing.T) {
	path := writeTempTS(t, `
export default function add(a: number, b: number): number {
  return a + b
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "func") && !strings.Contains(result.GoSource, "Add") {
		t.Errorf("expected exported function for export default function")
	}
}

func TestIRExportDefaultIdentifier(t *testing.T) {
	path := writeTempTS(t, `
const msg = "hello"
export default msg
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Default") && !strings.Contains(result.GoSource, "msg") {
		t.Errorf("expected Default with msg reference")
	}
}

// --- abstract classes, >>>, labeled statements ---

func TestIRAbstractClass(t *testing.T) {
	path := writeTempTS(t, `
abstract class Shape {
  abstract area(): number
  describe(): string {
    return "a shape"
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, `panic("abstract method not implemented")`) {
		t.Errorf("expected panic stub for abstract method")
	}
	if !strings.Contains(result.GoSource, "Describe()") {
		t.Errorf("expected concrete method Describe to be emitted")
	}
}

func TestIRUnsignedRightShift(t *testing.T) {
	path := writeTempTS(t, `
function ushr(a: number, b: number): number {
  return a >>> b
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "uint(") {
		t.Errorf("expected uint cast for >>>")
	}
}

func TestIRLabeledStatement(t *testing.T) {
	path := writeTempTS(t, `
function find(matrix: number[][]): number {
  let result: number = -1
  outer:
  for (let i = 0; i < matrix.length; i++) {
    for (let j = 0; j < matrix[i].length; j++) {
      if (matrix[i][j] === 42) {
        result = matrix[i][j]
        break outer
      }
    }
  }
  return result
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "outer:") {
		t.Errorf("expected label 'outer:'")
	}
	if !strings.Contains(result.GoSource, "break outer") {
		t.Errorf("expected 'break outer'")
	}
}

// --- for await...of ---

func TestIRForAwaitOf(t *testing.T) {
	path := writeTempTS(t, `
async function processAll(promises: Promise<string>[]): Promise<void> {
  for await (const result of promises) {
    console.log(result)
  }
}
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "Await()") {
		t.Errorf("expected .Await() call for for-await-of")
	}
}

// --- Go imports ---

func TestIRGoImportStdlib(t *testing.T) {
	path := writeTempTS(t, `
import { Println } from "go:fmt"
Println("hello")
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "fmt") && !strings.Contains(result.GoSource, "Println") {
		t.Errorf("expected fmt.Println call")
	}
}

func TestIRGoImportNamespace(t *testing.T) {
	path := writeTempTS(t, `
import * as http from "go:net/http"
http.ListenAndServe(":8080", null)
`)
	result, err := TranspileFileIR(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("IR output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "http") && !strings.Contains(result.GoSource, "ListenAndServe") {
		t.Errorf("expected http.ListenAndServe call")
	}
}
