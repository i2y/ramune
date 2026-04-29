package gotranspiler

import (
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// jsGlobalObjects lists JavaScript global objects whose static methods need
// special handling (Object.keys, Array.isArray, JSON.parse, etc.).
// These bypass type-driven dispatch and are handled by the global static method section.
var jsGlobalObjects = map[string]bool{
	"Object": true, "Array": true, "ArrayBuffer": true,
	"JSON": true, "Math": true, "Promise": true, "Date": true,
	"String": true, "Boolean": true, "Number": true,
	"console": true, "crypto": true,
}

// DispatchTarget determines how a method call should be emitted in Go.
// The target is determined by the Go type category of the object, not by method name matching.
type DispatchTarget int

const (
	DispatchArrayHelper    DispatchTarget = iota // jsarray.* helpers (for []T types)
	DispatchStringStdlib                         // strings.* stdlib or inline (for string types)
	DispatchPromiseMethod                        // promise.Promise.Then/Await (for *promise.Promise[T])
	DispatchMapOperation                         // Go map operations (for map[K]V types)
	DispatchConcreteMethod                       // obj.Method() direct call (for *Struct types)
	DispatchJSRTRuntime                          // jsrt.Obj().Get().Call() (for any/unknown types)
)

// dispatchMethodCall determines the dispatch target based purely on Go type category.
// No method name matching — the Go compiler will catch invalid method calls.
func (t *Transpiler) dispatchMethodCall(objType GoTypeInfo, objDeclType GoTypeInfo) DispatchTarget {
	// Priority: checkerType (narrowed) > declaredType
	// Check narrowed type first — it's more specific
	for _, ty := range []GoTypeInfo{objType, objDeclType} {
		if ty.GoStr == "" || ty.GoStr == "any" {
			continue
		}
		switch {
		case ty.IsString():
			return DispatchStringStdlib
		case ty.IsSlice():
			return DispatchArrayHelper
		case ty.IsPromise():
			return DispatchPromiseMethod
		case ty.IsMap():
			return DispatchMapOperation
		case ty.IsPointer() || isGenericType(ty.GoStr):
			return DispatchConcreteMethod
		}
	}
	// Both are any → runtime dispatch
	return DispatchJSRTRuntime
}

// emitExpr generates Go source code for a TypeScript expression node.
func (t *Transpiler) emitExpr(node *ast.Node) {
	if node == nil {
		t.w.write("nil")
		return
	}

	switch node.Kind {
	case ast.KindIdentifier:
		t.emitIdentifier(node)

	case ast.KindNumericLiteral:
		t.w.write(node.AsNumericLiteral().Text)

	case ast.KindStringLiteral:
		t.w.writef("%q", node.AsStringLiteral().Text)

	case ast.KindNoSubstitutionTemplateLiteral:
		t.w.writef("%q", node.AsNoSubstitutionTemplateLiteral().Text)

	case ast.KindRegularExpressionLiteral:
		t.emitRegExpLiteral(node)

	case ast.KindTrueKeyword:
		t.w.write("true")

	case ast.KindFalseKeyword:
		t.w.write("false")

	case ast.KindNullKeyword, ast.KindUndefinedKeyword:
		t.w.write("nil")

	case ast.KindThisKeyword:
		if t.thisReceiver != "" {
			t.w.write(t.thisReceiver)
		} else {
			t.w.write("this")
		}

	case ast.KindBinaryExpression:
		t.emitBinaryExpr(node)

	case ast.KindPrefixUnaryExpression:
		t.emitPrefixUnary(node)

	case ast.KindPostfixUnaryExpression:
		t.emitPostfixUnary(node)

	case ast.KindCallExpression:
		t.emitCallExpr(node)

	case ast.KindPropertyAccessExpression:
		t.emitPropertyAccess(node)

	case ast.KindElementAccessExpression:
		t.emitElementAccess(node)

	case ast.KindTemplateExpression:
		t.emitTemplateExpr(node)

	case ast.KindParenthesizedExpression:
		paren := node.AsParenthesizedExpression()
		t.w.write("(")
		t.emitExpr(paren.Expression)
		t.w.write(")")

	case ast.KindConditionalExpression:
		t.emitConditionalExpr(node)

	case ast.KindArrowFunction:
		t.emitArrowFunction(node)

	case ast.KindFunctionExpression:
		t.emitFunctionExpression(node)

	case ast.KindArrayLiteralExpression:
		t.emitArrayLiteral(node)

	case ast.KindObjectLiteralExpression:
		t.emitObjectLiteral(node)

	case ast.KindNewExpression:
		t.emitNewExpr(node)

	case ast.KindAwaitExpression:
		t.emitAwaitExpr(node)

	case ast.KindAsExpression:
		as := node.AsAsExpression()
		// If target type is a named struct/interface, emit Go type assertion or conversion
		if as.Type != nil && t.ck != nil {
			targetType := t.ck.GetTypeAtLocation(as.Type)
			if targetType != nil {
				goTarget := t.tm.goType(targetType)
				// Only assert for named types (not primitives or any)
				if goTarget != "" && goTarget != "any" && goTarget != "string" && goTarget != "float64" &&
					goTarget != "bool" && goTarget != "int" && !strings.HasPrefix(goTarget, "func(") {
					// Check if expression produces concrete type → use conversion instead of assertion
					if t.exprProducesConcreteGoType(as.Expression) {
						if t.isTypeParam(goTarget) {
							// Concrete → type parameter: type conversion T(expr)
							t.w.writef("%s(", goTarget)
							t.emitExpr(as.Expression)
							t.w.write(")")
						} else if targetType.Flags()&checker.TypeFlagsIntersection != 0 {
							// Primitive → intersection struct: wrap in struct with Value field
							// Use the TS alias name, not the resolved underlying type
							structName := goTarget
							if as.Type.Kind == ast.KindTypeReference {
								ref := as.Type.AsTypeReferenceNode()
								if ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
									structName = goExportedName(ref.TypeName.AsIdentifier().Text)
								}
							}
							t.w.writef("%s{Value: ", structName)
							t.emitExpr(as.Expression)
							t.w.write("}")
						} else {
							// Concrete → concrete: just emit (Go compiler handles compatibility)
							t.emitExpr(as.Expression)
						}
					} else {
						// Interface → concrete: type assertion .(T)
						t.emitExpr(as.Expression)
						t.writeTypeAssertion(goTarget)
					}
					break
				}
			}
		}
		// Default: erase type assertion
		t.emitExpr(as.Expression)

	case ast.KindTypeAssertionExpression:
		ta := node.AsTypeAssertion()
		t.emitExpr(ta.Expression)

	case ast.KindNonNullExpression:
		nn := node.AsNonNullExpression()
		t.emitExpr(nn.Expression)

	case ast.KindTypeOfExpression:
		t.emitTypeOfExpr(node)

	case ast.KindDeleteExpression:
		t.emitDeleteExpr(node)

	default:
		t.w.writef("/* unsupported expr kind: %s */", node.Kind.String())
	}
}

// emitIdentifier emits a Go identifier, mapping known globals.
func (t *Transpiler) emitIdentifier(node *ast.Node) {
	name := node.AsIdentifier().Text
	switch name {
	case "undefined":
		t.w.write("nil")
	case "NaN":
		t.w.addImport("math", "")
		t.w.write("math.NaN()")
	case "Infinity":
		t.w.addImport("math", "")
		t.w.write("math.Inf(1)")
	case "decodeURIComponent", "decodeURI":
		t.w.addImport("net/url", "")
		t.w.write("url.QueryUnescape")
	case "encodeURIComponent", "encodeURI":
		t.w.addImport("net/url", "")
		t.w.write("url.QueryEscape")
	case "parseInt":
		t.w.addImport("strconv", "")
		t.w.write("strconv.Atoi")
	case "parseFloat":
		t.w.addImport("strconv", "")
		t.w.write("func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }")
	case "isNaN":
		t.w.addImport("math", "")
		t.w.write("math.IsNaN")
	case "isFinite":
		t.w.addImport("math", "")
		t.w.write("func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }")
	case "btoa":
		t.w.addImport("encoding/base64", "")
		t.w.write("func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }")
	case "atob":
		t.w.addImport("encoding/base64", "")
		t.w.write("func(s string) string { b, _ := base64.StdEncoding.DecodeString(s); return string(b) }")
	case "crypto":
		t.w.write("true")
	case "String":
		t.w.addImport("fmt", "")
		t.w.write("func(v any) string { if v == nil { return \"\" }; return fmt.Sprint(v) }")
	case "Boolean":
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.write("func(v any, _ int) bool { return jsrt.ToBool(v) }")
	case "setTimeout":
		t.w.addImport("time", "")
		t.w.write("func(fn any, ms float64) { time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { fn.(func(any))(nil) }) }")
	default:
		// Check if this is a package reference (e.g., z from zod) → emit package name directly
		if pkg, ok := t.packageRefs[name]; ok {
			t.w.write(pkg)
			return
		}
		// Check if this is an imported name → emit as pkg.ExportedName
		if pkg, ok := t.importedNames[name]; ok {
			// Use original export name for renamed imports (e.g., { v4 as uuidv4 } → uuid.V4)
			exportName := name
			if orig, ok := t.importedOriginalNames[name]; ok {
				exportName = orig
			}
			// Lazily resolve the import — only add to Go imports when actually used in code
			t.resolvePendingImport(pkg)
			t.w.writef("%s.%s", pkg, goExportedName(exportName))
			return
		}
		// Same-package exports need PascalCase (they were exported in another file)
		if t.samePackageExports != nil && t.samePackageExports[name] {
			t.w.write(goExportedName(name))
		} else {
			t.w.write(goVarName(name))
		}
	}
}

// emitBinaryExpr handles binary expressions (a + b, a === b, etc.)
func (t *Transpiler) emitBinaryExpr(node *ast.Node) {
	bin := node.AsBinaryExpression()
	op := bin.OperatorToken.Kind

	// typeof x === "string" / "string" === typeof x pattern
	if t.emitTypeOfComparison(bin) {
		return
	}

	// instanceof: x instanceof Foo → type assertion
	if op == ast.KindInstanceOfKeyword {
		t.emitInstanceOf(bin)
		return
	}

	// in: "key" in obj → map key check
	if op == ast.KindInKeyword {
		t.emitInOperator(bin)
		return
	}

	// Nullish coalescing: a ?? b → func() T { if a != nil { return a.(T) }; return b }()
	if op == ast.KindQuestionQuestionToken {
		left := t.captureExpr(bin.Left)
		retType := "any"
		if t.ck != nil {
			typ := t.ck.GetTypeAtLocation(node)
			if typ != nil {
				goType := t.tm.goType(typ)
				if goType != "" && goType != "any" {
					retType = goType
				}
			}
		}
		// Non-nilable left side (e.g., Headers.Get() returns string):
		// use zero-value comparison instead of nil check.
		// The checker may report *string (string|null) but the Go method returns string.
		// Detect by checking if the captured code is a concrete method call (no jsrt/Unwrap).
		leftGoType := t.getGoType(bin.Left)
		// Detect concrete types from captured code (struct field access, etc.)
		leftIsConcrete := codeProducesConcreteType(left)
		goLevelString := leftGoType.GoStr == "string" && !leftGoType.IsPointer()
		if !goLevelString && leftGoType.GoStr == "*string" &&
			!strings.Contains(left, "jsrt.") && !strings.Contains(left, ".Unwrap()") &&
			strings.HasSuffix(left, ")") {
			goLevelString = true
		}
		// Non-nilable bool: use false comparison instead of nil
		if leftIsConcrete || leftGoType.GoStr == "bool" {
			zeroVal := "false"
			if leftGoType.GoStr == "string" || goLevelString {
				zeroVal = `""`
			} else if leftGoType.GoStr == "float64" || leftGoType.GoStr == "int" {
				zeroVal = "0"
			}
			goLevelRetType := retType
			if goLevelRetType == "" || goLevelRetType == "any" {
				goLevelRetType = leftGoType.GoStr
			}
			if goLevelRetType == "" {
				goLevelRetType = "any"
			}
			t.w.writef("func() %s { __v := %s; if __v != %s { return __v }; return ", goLevelRetType, left, zeroVal)
			t.emitExpr(bin.Right)
			t.w.write(" }()")
			return
		}
		if goLevelString {
			if retType == "*string" {
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				t.w.writef("func() *string { __v := %s; if __v != \"\" { return jsrt.Ptr(__v) }; return ", left)
				t.emitExpr(bin.Right)
				t.w.write(" }()")
			} else {
				t.w.writef("func() string { __v := %s; if __v != \"\" { return __v }; return ", left)
				t.emitExpr(bin.Right)
				t.w.write(" }()")
			}
			return
		}
		// Strip trailing type assertion from jsrt.Obj().Get().Unwrap().(Type) chains
		// since ?? needs a nil-checkable (any) value, not a concrete type
		if retType != "any" && strings.Contains(left, ".Unwrap().") {
			suffix := ".(" + retType + ")"
			if strings.HasSuffix(left, suffix) {
				left = strings.TrimSuffix(left, suffix)
			}
		}
		// Pointer-typed left (`*T` from `T | null` lowering): the ??
		// branch reads `*left` rather than asserting an interface.
		// `if left != nil { return *left }` keeps the test concise and
		// works for `*string`, `*float64`, `*bool`, `*int`, *Point, …
		if leftGoType.IsPointer() {
			deref := strings.TrimPrefix(leftGoType.GoStr, "*")
			if retType == leftGoType.GoStr {
				t.w.writef("func() %s { if %s != nil { return %s }; return ", retType, left, left)
			} else {
				t.w.writef("func() %s { if %s != nil { return *%s }; return ", deref, left, left)
			}
			t.emitExpr(bin.Right)
			t.w.write(" }()")
			return
		}
		if retType != "any" {
			t.w.writef("func() %s { if %s != nil { return %s.(%s) }; return ", retType, left, left, retType)
		} else {
			t.w.writef("func() any { if %s != nil { return %s }; return ", left, left)
		}
		t.emitExpr(bin.Right)
		t.w.write(" }()")
		return
	}

	// Exponentiation: a ** b → math.Pow(a, b)
	if op == ast.KindAsteriskAsteriskToken {
		t.w.addImport("math", "")
		t.w.write("math.Pow(")
		t.emitExpr(bin.Left)
		t.w.write(", ")
		t.emitExpr(bin.Right)
		t.w.write(")")
		return
	}

	// Nullish coalescing assignment: a ??= b → func() T { if a == nil { a = b }; return a }()
	if op == ast.KindQuestionQuestionEqualsToken {
		// For element access on map, use direct map assignment instead of jsrt.Index
		if bin.Left.Kind == ast.KindElementAccessExpression {
			ea := bin.Left.AsElementAccessExpression()
			mapType := t.getGoType(ea.Expression)
			if mapType.IsMap() || mapType.IsAny() {
				obj := t.captureExpr(ea.Expression)
				key := t.captureExpr(ea.ArgumentExpression)
				t.w.writef("func() any { if %s[%s] == nil { %s[%s] = ", obj, key, obj, key)
				t.emitExpr(bin.Right)
				t.w.writef(" }; return %s[%s] }()", obj, key)
				return
			}
		}
		left := t.captureExpr(bin.Left)
		t.w.writef("func() any { if %s == nil { %s = ", left, left)
		t.emitExpr(bin.Right)
		t.w.writef(" }; return %s }()", left)
		return
	}

	// Logical AND assignment: a &&= b → func() any { if ToBool(a) { a = b }; return a }()
	if op == ast.KindAmpersandAmpersandEqualsToken {
		left := t.captureExpr(bin.Left)
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.writef("func() any { if jsrt.ToBool(%s) { %s = ", left, left)
		t.emitExpr(bin.Right)
		t.w.writef(" }; return %s }()", left)
		return
	}

	// Logical OR assignment: a ||= b → func() any { if !ToBool(a) { a = b }; return a }()
	if op == ast.KindBarBarEqualsToken {
		left := t.captureExpr(bin.Left)
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.writef("func() any { if !jsrt.ToBool(%s) { %s = ", left, left)
		t.emitExpr(bin.Right)
		t.w.writef(" }; return %s }()", left)
		return
	}

	// Exponentiation assignment: a **= b → a = math.Pow(a, b)
	if op == ast.KindAsteriskAsteriskEqualsToken {
		t.w.addImport("math", "")
		t.emitExpr(bin.Left)
		t.w.write(" = math.Pow(")
		t.emitExpr(bin.Left)
		t.w.write(", ")
		t.emitExpr(bin.Right)
		t.w.write(")")
		return
	}

	// Unsigned right shift: a >>> b → int(uint(a) >> b)
	if op == ast.KindGreaterThanGreaterThanGreaterThanToken {
		t.w.write("int(uint(")
		t.emitExpr(bin.Left)
		t.w.write(") >> uint(")
		t.emitExpr(bin.Right)
		t.w.write("))")
		return
	}

	// Unsigned right shift assignment: a >>>= b → a = int(uint(a) >> b)
	if op == ast.KindGreaterThanGreaterThanGreaterThanEqualsToken {
		t.emitExpr(bin.Left)
		t.w.write(" = int(uint(")
		t.emitExpr(bin.Left)
		t.w.write(") >> uint(")
		t.emitExpr(bin.Right)
		t.w.write("))")
		return
	}

	// && / || with string left operand as truthy check: str && expr → str != "" && expr
	if op == ast.KindAmpersandAmpersandToken || op == ast.KindBarBarToken {
		leftType := t.getDeclaredGoType(bin.Left)
		if leftType.GoStr == "string" && !t.isBooleanCondition(bin.Left) {
			t.emitExpr(bin.Left)
			t.w.write(" != \"\"")
			t.w.writef(" %s ", tsOperatorToGo(op))
			t.emitExpr(bin.Right)
			return
		}
	}

	// && and || with any-typed or pointer-typed operands
	if op == ast.KindAmpersandAmpersandToken || op == ast.KindBarBarToken {
		leftDeclType := t.getDeclaredGoType(bin.Left)
		rightDeclType := t.getDeclaredGoType(bin.Right)
		leftAny := t.getGoType(bin.Left).IsAny() || leftDeclType.IsAny()
		rightAny := t.getGoType(bin.Right).IsAny() || rightDeclType.IsAny()
		// Property access on primitive-declared var emits jsrt.Obj().Get().Unwrap() → any at Go level
		if !leftAny && bin.Left.Kind == ast.KindPropertyAccessExpression {
			pa := bin.Left.AsPropertyAccessExpression()
			baseDeclType := t.getDeclaredGoType(pa.Expression)
			if baseDeclType.Category == GoTypePrimitive {
				leftAny = true
			}
			// Also check goAnyVars through as-expression unwrap
			inner := pa.Expression
			for inner.Kind == ast.KindParenthesizedExpression {
				inner = inner.AsParenthesizedExpression().Expression
			}
			if inner.Kind == ast.KindAsExpression {
				inner = inner.AsAsExpression().Expression
			}
			if inner.Kind == ast.KindIdentifier {
				vn := goVarName(inner.AsIdentifier().Text)
				if t.goAnyVars != nil && t.goAnyVars[vn] {
					leftAny = true
				}
			}
		}
		if !rightAny && bin.Right.Kind == ast.KindPropertyAccessExpression {
			pa := bin.Right.AsPropertyAccessExpression()
			baseDeclType := t.getDeclaredGoType(pa.Expression)
			if baseDeclType.Category == GoTypePrimitive {
				rightAny = true
			}
		}
		leftPtr := !leftAny && strings.HasPrefix(leftDeclType.GoStr, "*")
		rightPtr := !rightAny && strings.HasPrefix(rightDeclType.GoStr, "*")

		// Value-returning ||: any_left || concrete_right → IIFE fallback
		// (Go's || only works between bools, but TS || returns the value)
		if op == ast.KindBarBarToken && (leftAny || leftPtr) && !rightAny && !rightPtr {
			rightType := t.getGoType(bin.Right)
			if !rightType.IsBool() {
				// Use the full expression type (more reliable than right-side type for empty literals)
				retType := t.getGoType(node).GoStr
				if retType == "" || retType == "[]" || retType == "any" {
					retType = rightType.GoStr
				}
				if retType == "" || retType == "[]" {
					retType = "any"
				}
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				left := t.captureExpr(bin.Left)
				// Strip trailing type assertion if present (e.g., .Unwrap().([]any))
				// so __v is any-typed and the return assertion works
				assertSuffix := ".(" + retType + ")"
				if strings.HasSuffix(left, assertSuffix) {
					left = strings.TrimSuffix(left, assertSuffix)
				}
				// Check if left expression produces a concrete type (e.g., struct field access)
				// In that case, __v already has the right type — no assertion needed
				leftIsConcrete := codeProducesConcreteType(left)
				if leftIsConcrete {
					t.w.writef("func() %s { __v := %s; if jsrt.ToBool(__v) { return __v }; return ", retType, left)
				} else {
					t.w.writef("func() %s { __v := %s; if jsrt.ToBool(__v) { return __v.(%s) }; return ", retType, left, retType)
				}
				t.emitExpr(bin.Right)
				t.w.write(" }()")
				return
			}
		}

		if leftAny || rightAny || leftPtr || rightPtr {
			goOp := tsOperatorToGo(op)
			// Left operand
			if leftPtr {
				t.emitExpr(bin.Left)
				t.w.write(" != nil")
			} else if leftAny && !t.isBooleanCondition(bin.Left) {
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				t.w.write("jsrt.ToBool(")
				t.emitExpr(bin.Left)
				t.w.write(")")
			} else {
				t.emitExpr(bin.Left)
			}
			t.w.writef(" %s ", goOp)
			// Right operand
			if rightPtr {
				t.emitExpr(bin.Right)
				t.w.write(" != nil")
			} else if rightAny && !t.isBooleanCondition(bin.Right) {
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				t.w.write("jsrt.ToBool(")
				t.emitExpr(bin.Right)
				t.w.write(")")
			} else {
				t.emitExpr(bin.Right)
			}
			return
		}
	}

	// Handle string concatenation: if either operand is string-typed, use +
	goOp := tsOperatorToGo(op)

	// Assignment to property on any-typed object: obj.foo = x → jsrt.Obj(obj).Set("Foo", x)
	if op == ast.KindEqualsToken && bin.Left.Kind == ast.KindPropertyAccessExpression {
		pa := bin.Left.AsPropertyAccessExpression()
		objDeclType := t.getDeclaredGoType(pa.Expression)
		if objDeclType.IsAny() {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			obj := t.captureExpr(pa.Expression)
			propName := goExportedName(nodeText(pa.Name()))
			t.w.writef("jsrt.Obj(%s).Set(%q, ", obj, propName)
			t.emitExpr(bin.Right)
			t.w.write(")")
			return
		}
	}

	// Assignment to setter property: obj.foo = x → obj.SetFoo(x)
	if op == ast.KindEqualsToken && bin.Left.Kind == ast.KindPropertyAccessExpression && t.ck != nil {
		sym := t.ck.GetSymbolAtLocation(bin.Left)
		if sym != nil && sym.Flags&ast.SymbolFlagsSetAccessor != 0 {
			pa := bin.Left.AsPropertyAccessExpression()
			propName := nodeText(pa.Name())
			t.emitExpr(pa.Expression)
			t.w.writef(".Set%s(", goExportedName(propName))
			t.emitExpr(bin.Right)
			t.w.write(")")
			return
		}
	}

	// Compound assignment on []any element or any-typed element access:
	// buffer[0] += str → buffer[0] = buffer[0].(string) + str
	if op == ast.KindPlusEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
		ea := bin.Left.AsElementAccessExpression()
		arrType := t.getDeclaredGoType(ea.Expression)
		if arrType.IsSlice() && arrType.ElemType == "any" {
			left := t.captureExpr(bin.Left)
			t.w.writef("%s = %s.(string) + ", left, left)
			right := t.captureExpr(bin.Right)
			if t.rightSideProducesGoAny(bin.Right) || goCodeProducesAny(right) {
				t.w.writef("%s.(string)", right)
			} else {
				t.w.write(right)
			}
			return
		}
		if arrType.IsAny() {
			// any-typed array: use jsrt.SetIndex since jsrt.Index returns are not assignable
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			arrObj := t.captureExpr(ea.Expression)
			key := t.captureExpr(ea.ArgumentExpression)
			right := t.captureExpr(bin.Right)
			rightStr := right
			if t.rightSideProducesGoAny(bin.Right) || goCodeProducesAny(right) {
				rightStr = right + ".(string)"
			}
			t.w.writef("jsrt.SetIndex(%s, %s, jsrt.Index(%s, %s).(string) + %s)", arrObj, key, arrObj, key, rightStr)
			return
		}
	}

	// Compound string += any: str += expr where right produces any at Go level
	if op == ast.KindPlusEqualsToken {
		leftType := t.getDeclaredGoType(bin.Left)
		if leftType.IsString() && t.rightSideProducesGoAny(bin.Right) {
			t.emitExpr(bin.Left)
			t.w.write(" += ")
			t.emitExpr(bin.Right)
			t.w.write(".(string)")
			return
		}
	}

	// Assignment operators
	if isAssignment(op) {
		// Simple assignment to any-typed element access → use jsrt.SetIndex or direct map assign
		if op == ast.KindEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
			ea := bin.Left.AsElementAccessExpression()
			arrType := t.getGoType(ea.Expression)
			if arrType.IsAny() {
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				arrObj := t.captureExpr(ea.Expression)
				key := t.captureExpr(ea.ArgumentExpression)
				t.w.writef("jsrt.SetIndex(%s, %s, ", arrObj, key)
				t.emitExpr(bin.Right)
				t.w.write(")")
				return
			}
			// map[string]any → direct map assignment
			if arrType.IsMap() && arrType.ElemType == "any" {
				t.emitExpr(ea.Expression)
				t.w.write("[")
				t.emitExpr(ea.ArgumentExpression)
				t.w.write("] = ")
				t.emitExpr(bin.Right)
				return
			}
		}

		// Check if assigning to []byte element — need byte() cast on right side
		needsByteCast := false
		if op == ast.KindEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
			ea := bin.Left.AsElementAccessExpression()
			if ea.Expression.Kind == ast.KindIdentifier {
				varName := goVarName(ea.Expression.AsIdentifier().Text)
				if t.concreteVarTypes != nil && t.concreteVarTypes[varName] == "[]byte" {
					needsByteCast = true
				}
			}
		}

		t.emitExpr(bin.Left)
		t.w.writef(" %s ", goOp)
		if needsByteCast {
			t.w.write("byte(")
			t.emitExpr(bin.Right)
			t.w.write(")")
			return
		}
		// Compound assignment (+=, *=, etc.): coerce int right-hand side to float64
		if op != ast.KindEqualsToken && t.isFloatExpr(bin.Left) && t.isIntExpr(bin.Right) {
			t.w.write("float64(")
			t.emitExpr(bin.Right)
			t.w.write(")")
		} else if op == ast.KindEqualsToken {
			// Type-driven: check if left is concrete and right is any → type assertion
			leftInfo := t.getGoType(bin.Left)
			rightInfo := t.getGoType(bin.Right)
			// Check if right side is any at Go level (even if checker narrows it)
			rightIsGoAny := rightInfo.IsAny()
			if !rightIsGoAny {
				rightCode := t.captureExpr(bin.Right)
				if goCodeProducesAny(rightCode) {
					rightIsGoAny = true
				}
			}
			// Map index expressions return any at Go level when the map value type is any.
			// Unwrap type assertions (as X) to find the underlying expression.
			rightExpr := bin.Right
			for rightExpr.Kind == ast.KindAsExpression {
				rightExpr = rightExpr.AsAsExpression().Expression
			}
			for rightExpr.Kind == ast.KindNonNullExpression {
				rightExpr = rightExpr.AsNonNullExpression().Expression
			}
			if !rightIsGoAny && rightExpr.Kind == ast.KindElementAccessExpression {
				ea := rightExpr.AsElementAccessExpression()
				mapType := t.getGoType(ea.Expression)
				declMapType := t.getDeclaredGoType(ea.Expression)
				if (mapType.IsMap() && mapType.ElemType == "any") ||
					(declMapType.IsMap() && declMapType.ElemType == "any") {
					rightIsGoAny = true
				}
				// If left is a map type, element access on map[K]any returns any
				if !rightIsGoAny && leftInfo.IsMap() {
					rightIsGoAny = true
				}
				// If both left and right have the same non-primitive alias type (e.g., BodyData),
				// and left is not a Go primitive or slice, the index result is any
				if !rightIsGoAny && !leftInfo.IsAny() && !leftInfo.IsSlice() &&
					leftInfo.Category != GoTypePrimitive &&
					leftInfo.GoStr == rightInfo.GoStr {
					rightIsGoAny = true
				}
			}
			if !leftInfo.IsAny() && leftInfo.GoStr != "" && rightIsGoAny {
				code := t.captureExpr(bin.Right)
				t.w.write(code)
				t.writeTypeAssertion(leftInfo.GoStr)
				return
			}
			// *T = T wrapping (e.g., *string = string from function call)
			if leftInfo.IsPointer() && !rightInfo.IsPointer() && rightInfo.GoStr != "" &&
				rightInfo.GoStr != "any" && leftInfo.GoStr == "*"+rightInfo.GoStr {
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				t.w.write("jsrt.Ptr(")
				t.emitExpr(bin.Right)
				t.w.write(")")
				return
			}
			t.emitExpr(bin.Right)
		} else {
			t.emitExpr(bin.Right)
		}
		return
	}

	// Bitwise operators (|, &, ^) require int operands in Go.
	// JS idiom: x | 0 floors to int. Wrap float/any operands with int().
	if op == ast.KindBarToken || op == ast.KindAmpersandToken || op == ast.KindCaretToken {
		t.emitBitwiseOperand(bin.Left)
		t.w.writef(" %s ", goOp)
		t.emitBitwiseOperand(bin.Right)
		return
	}

	// Detect int/float64 mismatch in comparisons and arithmetic.
	// If one side is int (loop counter) and the other is float64, insert int() conversion.
	leftIsInt := t.isIntExpr(bin.Left)
	rightIsInt := t.isIntExpr(bin.Right)
	leftIsFloat := t.isFloatExpr(bin.Left)
	rightIsFloat := t.isFloatExpr(bin.Right)

	// Check if comparing with null/nil — don't deref in that case
	isNullComparison := (bin.Right.Kind == ast.KindNullKeyword || bin.Right.Kind == ast.KindUndefinedKeyword ||
		bin.Left.Kind == ast.KindNullKeyword || bin.Left.Kind == ast.KindUndefinedKeyword)

	isComparison := op == ast.KindLessThanToken || op == ast.KindGreaterThanToken ||
		op == ast.KindLessThanEqualsToken || op == ast.KindGreaterThanEqualsToken ||
		op == ast.KindEqualsEqualsEqualsToken || op == ast.KindExclamationEqualsEqualsToken ||
		op == ast.KindEqualsEqualsToken || op == ast.KindExclamationEqualsToken

	if isNullComparison {
		// Type-driven null comparison: string → "" , bool → false, numeric → 0, any/pointer → nil
		nonNullSide := bin.Left
		if bin.Left.Kind == ast.KindNullKeyword || bin.Left.Kind == ast.KindUndefinedKeyword {
			nonNullSide = bin.Right
		}
		nonNullType := t.getGoType(nonNullSide)
		t.emitExpr(nonNullSide)
		if nonNullType.GoStr == "string" {
			// Non-pointer string → compare with empty string
			t.w.writef(" %s \"\"", goOp)
		} else if nonNullType.IsFloat64() || nonNullType.IsInt() {
			t.w.writef(" %s 0", goOp)
		} else if nonNullType.IsBool() {
			if goOp == "==" {
				t.w.write(" == false")
			} else {
				t.w.write(" != false")
			}
		} else {
			t.w.writef(" %s nil", goOp)
		}
	} else if leftIsInt && rightIsFloat && isComparison {
		// JS compares numerically as float; widen int to float64 instead of
		// truncating float to int (which diverges for fractional values —
		// `int(-0.5) < 0` is false in Go but `-0.5 < 0` is true in JS).
		t.emitFloatOperand(bin.Left, true)
		t.w.writef(" %s ", goOp)
		t.emitFloatOperand(bin.Right, false)
	} else if leftIsFloat && rightIsInt && isComparison {
		t.emitFloatOperand(bin.Left, false)
		t.w.writef(" %s ", goOp)
		t.emitFloatOperand(bin.Right, true)
	} else if (leftIsInt && rightIsFloat || leftIsFloat && rightIsInt) && op == ast.KindPercentToken {
		// JS `%` is IEEE 754 remainder on floats (5.5 % 2 = 1.5); Go's `%`
		// is int-only, so use math.Mod for float-correct semantics.
		t.w.addImport("math", "")
		t.w.write("math.Mod(")
		t.emitFloatOperand(bin.Left, leftIsInt)
		t.w.write(", ")
		t.emitFloatOperand(bin.Right, rightIsInt)
		t.w.write(")")
	} else if leftIsInt && rightIsFloat {
		// Arithmetic: convert int to float64 (e.g., 2 + x → float64(2) + x)
		t.w.write("float64(")
		t.emitExprDeref(bin.Left)
		t.w.writef(") %s ", goOp)
		t.emitExprDeref(bin.Right)
	} else if leftIsFloat && rightIsInt {
		if t.rightSideProducesGoAny(bin.Left) || t.getDeclaredGoType(bin.Left).IsAny() {
			t.emitExprDeref(bin.Left)
			t.w.write(".(float64)")
		} else {
			t.emitExprDeref(bin.Left)
		}
		t.w.writef(" %s float64(", goOp)
		t.emitExprDeref(bin.Right)
		t.w.write(")")
	} else if op == ast.KindPlusToken && t.isStringConcatContext(bin) {
		// String concatenation with non-string operand → wrap with fmt.Sprint
		t.w.addImport("fmt", "")
		t.emitStringConcatOperand(bin.Left)
		t.w.write(" + ")
		t.emitStringConcatOperand(bin.Right)
	} else {
		// Check for any vs concrete numeric in arithmetic operations.
		// Use code-capture to detect Go-level any (JSObject chains produce any even when checker says concrete).
		isArithmetic := op == ast.KindMinusToken || op == ast.KindAsteriskToken ||
			op == ast.KindSlashToken || op == ast.KindPlusToken
		if isArithmetic {
			leftCode := t.captureExpr(bin.Left)
			rightCode := t.captureExpr(bin.Right)
			leftGoAny := goCodeProducesAny(leftCode) || t.getGoType(bin.Left).IsAny() || t.rightSideProducesGoAny(bin.Left)
			rightGoAny := goCodeProducesAny(rightCode) || t.getGoType(bin.Right).IsAny() || t.rightSideProducesGoAny(bin.Right)
			if leftGoAny && !rightGoAny {
				t.w.writef("%s.(float64) %s %s", leftCode, goOp, rightCode)
			} else if rightGoAny && !leftGoAny {
				t.w.writef("%s %s %s.(float64)", leftCode, goOp, rightCode)
			} else {
				t.w.writef("%s %s %s", leftCode, goOp, rightCode)
			}
		} else if isComparison {
			t.emitExprDerefUnlessNil(bin.Left, bin.Right)
			t.w.writef(" %s ", goOp)
			t.emitExprDerefUnlessNil(bin.Right, bin.Left)
		} else {
			t.emitExprDeref(bin.Left)
			t.w.writef(" %s ", goOp)
			t.emitExprDeref(bin.Right)
		}
	}
}

// isStringConcatContext checks if a + binary expression involves string operands.
func (t *Transpiler) isStringConcatContext(bin *ast.BinaryExpression) bool {
	if t.ck == nil {
		return false
	}
	leftType := t.ck.GetTypeAtLocation(bin.Left)
	rightType := t.ck.GetTypeAtLocation(bin.Right)
	leftIsString := leftType != nil && leftType.Flags()&checker.TypeFlagsStringLike != 0
	rightIsString := rightType != nil && rightType.Flags()&checker.TypeFlagsStringLike != 0
	leftIsNum := leftType != nil && leftType.Flags()&checker.TypeFlagsNumberLike != 0
	rightIsNum := rightType != nil && rightType.Flags()&checker.TypeFlagsNumberLike != 0
	leftIsNonString := leftIsNum || t.getGoType(bin.Left).IsAny() || t.rightSideProducesGoAny(bin.Left)
	rightIsNonString := rightIsNum || t.getGoType(bin.Right).IsAny() || t.rightSideProducesGoAny(bin.Right)
	return (leftIsString && rightIsNonString) || (rightIsString && leftIsNonString)
}

// emitStringConcatOperand emits an operand in string concatenation, wrapping non-string with fmt.Sprint.
func (t *Transpiler) emitStringConcatOperand(node *ast.Node) {
	if t.ck != nil {
		nodeType := t.ck.GetTypeAtLocation(node)
		isNonString := (nodeType != nil && nodeType.Flags()&checker.TypeFlagsNumberLike != 0) ||
			t.getGoType(node).IsAny() || t.rightSideProducesGoAny(node)
		if isNonString {
			t.w.write("fmt.Sprint(")
			t.emitExprDeref(node)
			t.w.write(")")
			return
		}
	}
	t.emitExprDeref(node)
}

// emitBitwiseOperand emits an operand for bitwise operations, wrapping float64/any with int().
func (t *Transpiler) emitBitwiseOperand(node *ast.Node) {
	goType := t.getGoType(node)
	if goType.IsAny() {
		t.w.write("int(")
		t.emitExprDeref(node)
		t.w.write(".(float64))")
	} else if t.isIntExpr(node) {
		t.emitExprDeref(node)
	} else {
		// float64 or unknown → int() cast
		t.w.write("int(")
		t.emitExprDeref(node)
		t.w.write(")")
	}
}

// emitPrefixUnary handles prefix unary expressions (!x, -x, ++x, typeof x, etc.)
func (t *Transpiler) emitPrefixUnary(node *ast.Node) {
	prefix := node.AsPrefixUnaryExpression()
	switch prefix.Operator {
	case ast.KindExclamationToken:
		// Check if the operand is a known boolean expression
		if t.isBooleanExpr(prefix.Operand) {
			t.w.write("!")
			t.emitExpr(prefix.Operand)
		} else {
			// Non-boolean or unknown type → use jsrt.ToBool for safety
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("!jsrt.ToBool(")
			t.emitExpr(prefix.Operand)
			t.w.write(")")
		}
	case ast.KindMinusToken:
		t.w.write("-")
		t.emitExpr(prefix.Operand)
	case ast.KindPlusToken:
		// Unary plus — identity for numbers, type coercion in JS
		t.emitExpr(prefix.Operand)
	case ast.KindTildeToken:
		t.w.write("^")
		t.emitExpr(prefix.Operand)
	case ast.KindPlusPlusToken:
		t.emitExpr(prefix.Operand)
		t.w.write("++") // Go only has postfix, but this works as statement
	case ast.KindMinusMinusToken:
		t.emitExpr(prefix.Operand)
		t.w.write("--")
	default:
		t.w.writef("/* unsupported prefix operator: %s */", prefix.Operator.String())
	}
}

// emitPostfixUnary handles postfix unary expressions (x++, x--)
func (t *Transpiler) emitPostfixUnary(node *ast.Node) {
	postfix := node.AsPostfixUnaryExpression()
	// any-typed variables can't use ++ directly; expand to assignment
	declType := t.getDeclaredGoType(postfix.Operand)
	if declType.IsAny() {
		operand := t.captureExpr(postfix.Operand)
		switch postfix.Operator {
		case ast.KindPlusPlusToken:
			t.w.writef("%s = %s.(float64) + 1", operand, operand)
		case ast.KindMinusMinusToken:
			t.w.writef("%s = %s.(float64) - 1", operand, operand)
		}
		return
	}
	t.emitExpr(postfix.Operand)
	switch postfix.Operator {
	case ast.KindPlusPlusToken:
		t.w.write("++")
	case ast.KindMinusMinusToken:
		t.w.write("--")
	default:
		t.w.writef("/* unsupported postfix operator */")
	}
}

// emitCallExpr handles function calls using type-driven dispatch.
func (t *Transpiler) emitCallExpr(node *ast.Node) {
	call := node.AsCallExpression()

	// DEBUG: trace

	// `Class.method(args)` where Class is an extracted class lowers to
	// the package-level form emitStaticMethod produces (`Class_Method`).
	// Emitter-side guard: instance methods can't be called this way in
	// valid TS, so the picker accepts the form only when method is in
	// the static registry.
	if t.emitStaticMethodCallIfApplicable(call) {
		return
	}

	// --- 1. Console calls (namespace-driven) ---
	if t.isConsoleCall(call) {
		t.emitConsoleCall(call)
		return
	}

	// Bare identifier callee bound to a *ramune.JSFunc parameter must
	// intercept before the generic "callee is any → func(any...) any"
	// cast path below, which would produce a Go type error.
	if t.emitJSFuncCallIfApplicable(call) {
		return
	}

	// --- 2. Property access method calls: obj.method(args) ---
	if call.Expression.Kind == ast.KindPropertyAccessExpression {
		prop := call.Expression.AsPropertyAccessExpression()

		// Array.prototype.map.call(arr, fn) → inline map
		if nodeText(prop.Name()) == "call" && prop.Expression.Kind == ast.KindPropertyAccessExpression {
			inner := prop.Expression.AsPropertyAccessExpression()
			arrMethod := nodeText(inner.Name())
			if inner.Expression.Kind == ast.KindPropertyAccessExpression {
				innerInner := inner.Expression.AsPropertyAccessExpression()
				if nodeText(innerInner.Name()) == "prototype" &&
					innerInner.Expression.Kind == ast.KindIdentifier &&
					innerInner.Expression.AsIdentifier().Text == "Array" {
					if arrMethod == "map" && call.Arguments != nil && len(call.Arguments.Nodes) >= 2 {
						// Array.prototype.map.call(arr, fn) → func(a []byte, fn func(any) string) []string {...}(arr, fn)
						t.w.write("func() []string { var __r []string; for _, __x := range ")
						t.emitExpr(call.Arguments.Nodes[0])
						t.w.write(" { __r = append(__r, (")
						t.emitExpr(call.Arguments.Nodes[1])
						t.w.write(")(any(__x))) }; return __r }()")
						return
					}
				}
			}
		}

		// 2-level nested static calls: crypto.subtle.importKey(...) → web.Subtle.ImportKey(...)
		if prop.Expression.Kind == ast.KindPropertyAccessExpression {
			innerProp := prop.Expression.AsPropertyAccessExpression()
			if innerProp.Expression.Kind == ast.KindIdentifier {
				outerObj := innerProp.Expression.AsIdentifier().Text
				middleProp := nodeText(innerProp.Name())
				method := nodeText(prop.Name())
				if outerObj == "crypto" && middleProp == "subtle" {
					t.w.addImport("github.com/i2y/ramune/jsrt/web", "web")
					t.w.writef("web.Subtle.%s(", goExportedName(method))
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				}
			}
		}

		// .then() on Promise or any: force any types in callback
		{
			methodName := nodeText(prop.Name())
			if methodName == "then" || methodName == "Then" {
				objType := t.getGoType(prop.Expression)
				isPromise := objType.IsPromise() || strings.Contains(objType.GoStr, "Promise")
				if isPromise || objType.IsAny() {
					objCode := t.captureExpr(prop.Expression)
					declType := t.getDeclaredGoType(prop.Expression)
					needsAssert := objType.IsAny() || declType.IsAny() || goCodeProducesAny(objCode)
					// Don't assert if code already returns a *promise.Promise[...] type
					if needsAssert && (strings.HasPrefix(objCode, "promise.") || strings.Contains(objCode, "*promise.Promise[")) {
						needsAssert = false
					}
					if needsAssert {
						t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
						t.w.writef("%s.(*promise.Promise[any]).Then(", objCode)
					} else {
						t.emitExpr(prop.Expression)
						t.w.write(".Then(")
					}
					saved := t.inThenCallback
					t.inThenCallback = true
					t.emitCallArgs(call.Arguments)
					t.inThenCallback = saved
					t.w.write(")")
					return
				}
			}
		}

		isOptional := call.QuestionDotToken != nil || prop.QuestionDotToken != nil

		// 2a. Optional chaining call — type-driven
		if isOptional {
			objType := t.getGoType(prop.Expression)
			methodName := nodeText(prop.Name())

			// If object is a concrete type (e.g., string), dispatch to proper method handler
			if objType.IsString() {
				// Check if the object is actually *string at Go level (tracked param)
				isPtrString := false
				if prop.Expression.Kind == ast.KindIdentifier {
					emitted := t.getEmittedGoType(prop.Expression)
					if emitted.GoStr == "*string" {
						isPtrString = true
					} else {
						vn := goVarName(prop.Expression.AsIdentifier().Text)
						if t.goPtrStringVars != nil && t.goPtrStringVars[vn] {
							isPtrString = true
						}
					}
				}
				if isPtrString {
					// *string?.method() → nil check + dereference + method call
					obj := t.captureExpr(prop.Expression)
					t.w.writef("func() any { if %s != nil { __s := *%s; ", obj, obj)
					t.stringMethodObjOverride = "__s"
					t.w.write("return ")
					t.emitStringMethodCall(call)
					t.w.write(" }; return nil }()")
				} else {
					t.emitStringMethodCall(call)
				}
				return
			}
			if objType.IsSlice() {
				t.emitArrayMethodCall(call)
				return
			}

			obj := t.captureExpr(prop.Expression)
			goMethodName := goExportedName(methodName)
			args := t.captureCallArgs(call)

			if objType.IsAny() || objType.GoStr == "" {
				// any-typed → nil check IIFE
				t.w.writef("func() any { if %s != nil { return %s.%s(%s) }; return nil }()", obj, obj, goMethodName, args)
			} else if objType.IsString() {
				// string → empty check
				t.w.writef("func() any { if %s != \"\" { return %s.%s(%s) }; return nil }()", obj, obj, goMethodName, args)
			} else {
				// pointer/interface → nil check
				t.w.writef("func() any { if %s != nil { return %s.%s(%s) }; return nil }()", obj, obj, goMethodName, args)
			}
			return
		}

		// 2b. TYPE-DRIVEN method dispatch
		objType := t.getGoType(prop.Expression)
		objDeclType := t.getDeclaredGoType(prop.Expression)
		// Override with emitted Go type for more accurate dispatch
		emittedObjType := t.getEmittedGoType(prop.Expression)
		if emittedObjType.GoStr != "" {
			objDeclType = emittedObjType
		}
		methodName := nodeText(prop.Name())

		// Regex methods (check first — regex type maps to "any" so must come before any-dispatch)
		switch methodName {
		case "test":
			t.emitExpr(prop.Expression)
			t.w.write(".MatchString(")
			if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
				arg := call.Arguments.Nodes[0]
				argCode := t.captureExpr(arg)
				if goCodeProducesAny(argCode) {
					t.w.writef("%s.(string)", argCode)
				} else if arg.Kind == ast.KindIdentifier && t.goPtrStringVars != nil &&
					t.goPtrStringVars[goVarName(arg.AsIdentifier().Text)] {
					t.w.writef("*%s", argCode)
				} else {
					t.w.write(argCode)
				}
			}
			t.w.write(")")
			return
		case "exec":
			t.emitExpr(prop.Expression)
			t.w.write(".FindStringSubmatch(")
			t.emitCallArgs(call.Arguments)
			t.w.write(")")
			return
		}

		// Skip type-driven dispatch for JS global static calls (Object, Array, JSON, etc.)
		// These are handled in the global static method section below.
		isJSGlobal := false
		if prop.Expression.Kind == ast.KindIdentifier {
			isJSGlobal = jsGlobalObjects[prop.Expression.AsIdentifier().Text]
		}
		if t.isPackageRef(prop.Expression) || isJSGlobal {
			// Fall through to global static method section
		} else {
			// Type-driven dispatch: determine strategy from Go type category, not method name
			dispatch := t.dispatchMethodCall(objType, objDeclType)
			if prop.Expression.Kind == ast.KindThisKeyword {
				dispatch = DispatchConcreteMethod
			}
			// Override: emitted type may provide better info
			emittedType := t.getEmittedGoType(prop.Expression)
			if emittedType.GoStr != "" {
				dispatch = t.dispatchMethodCall(emittedType, objDeclType)
			}

			switch dispatch {
			case DispatchStringStdlib:
				t.emitStringMethodCall(call)
				return

			case DispatchArrayHelper:
				t.emitArrayMethodCall(call)
				return

			case DispatchConcreteMethod:
				// Direct method call: obj.Method(args)
				obj := t.captureExpr(prop.Expression)
				args := t.captureCallArgs(call)
				goMethodName := goExportedName(methodName)
				t.w.writef("%s.%s(%s)", obj, goMethodName, args)
				return

			case DispatchPromiseMethod:
				// Promise methods handled by concrete method path
				obj := t.captureExpr(prop.Expression)
				args := t.captureCallArgs(call)
				goMethodName := goExportedName(methodName)
				t.w.writef("%s.%s(%s)", obj, goMethodName, args)
				return

			case DispatchMapOperation:
				// Map methods: direct Go map operations
				obj := t.captureExpr(prop.Expression)
				args := t.captureCallArgs(call)
				goMethodName := goExportedName(methodName)
				t.w.writef("%s.%s(%s)", obj, goMethodName, args)
				return

			case DispatchJSRTRuntime:
				// Any/unknown type → runtime dispatch via jsrt.Obj
				goMethodName := goExportedName(methodName)
				if !objType.IsAny() && !objDeclType.IsAny() {
					// Checker narrowed to concrete → type assertion + method call
					obj := t.captureExpr(prop.Expression)
					args := t.captureCallArgs(call)
					t.w.writef("%s.(%s).%s(%s)", obj, objType.GoStr, goMethodName, args)
				} else {
					obj := t.captureExpr(prop.Expression)
					t.w.addImport("github.com/i2y/ramune/jsrt", "")
					args := t.captureCallArgs(call)
					t.w.writef("jsrt.Obj(%s).Get(%q).Call(%s).Unwrap()", obj, goMethodName, args)
				}
				return
			}
		} // end of non-packageRef dispatch block

	}

	// JS global static method calls: Object.create, Array.isArray, etc.
	if call.Expression.Kind == ast.KindPropertyAccessExpression {
		prop := call.Expression.AsPropertyAccessExpression()
		if prop.Expression.Kind == ast.KindIdentifier {
			objName := prop.Expression.AsIdentifier().Text
			method := nodeText(prop.Name())
			switch objName {
			case "Object":
				switch method {
				case "create":
					t.w.write("map[string]any{}")
					return
				case "assign":
					// Object.assign(target, ...sources) — emit target; ignore sources for now
					if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
						t.emitExpr(call.Arguments.Nodes[0])
					}
					return
				case "entries":
					// Determine the map type of the argument
					argMapType := "map[string]any"
					if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
						argType := t.getGoType(call.Arguments.Nodes[0])
						if argType.IsMap() {
							argMapType = argType.GoStr
						}
					}
					t.w.writef("func(m %s) [][]any { var r [][]any; for k, v := range m { r = append(r, []any{k, v}) }; return r }(", argMapType)
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				case "fromEntries":
					t.w.write("func(entries any) map[string]any { return map[string]any{} }(")
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				case "keys":
					t.w.write("func(m map[string]any) []string { var r []string; for k := range m { r = append(r, k) }; return r }(")
					if call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
						argCode := t.captureExpr(call.Arguments.Nodes[0])
						if goCodeProducesAny(argCode) || t.getGoType(call.Arguments.Nodes[0]).IsAny() {
							t.w.writef("%s.(map[string]any)", argCode)
						} else {
							t.w.write(argCode)
						}
					} else {
						t.emitCallArgs(call.Arguments)
					}
					t.w.write(")")
					return
				}
			case "Array":
				if method == "isArray" {
					t.w.addImport("reflect", "")
					t.w.write("func(v any) bool { return v != nil && reflect.TypeOf(v).Kind() == reflect.Slice }(")
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				}
			case "ArrayBuffer":
				if method == "isView" {
					t.w.addImport("reflect", "")
					t.w.write("func(v any) bool { return v != nil && reflect.TypeOf(v).Kind() == reflect.Slice }(")
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				}
			case "String":
				switch method {
				case "fromCharCode":
					if call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
						call.Arguments.Nodes[0].Kind == ast.KindSpreadElement {
						// String.fromCharCode(...bytes) → string(bytes)
						t.w.write("string(")
						t.emitExpr(call.Arguments.Nodes[0].AsSpreadElement().Expression)
						t.w.write(")")
					} else {
						// String.fromCharCode(c1, c2, ...) → string([]byte{byte(c1), ...})
						t.w.write("string([]byte{")
						if call.Arguments != nil {
							for i, arg := range call.Arguments.Nodes {
								if i > 0 {
									t.w.write(", ")
								}
								t.w.write("byte(")
								t.emitExpr(arg)
								t.w.write(")")
							}
						}
						t.w.write("})")
					}
					return
				}
			case "JSON":
				switch method {
				case "stringify":
					t.w.addImport("encoding/json", "")
					t.w.write("func(v any) string { b, _ := json.Marshal(v); return string(b) }(")
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				case "parse":
					t.w.addImport("encoding/json", "")
					t.w.write("func(s string) any { var v any; json.Unmarshal([]byte(s), &v); return v }(")
					if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
						arg := call.Arguments.Nodes[0]
						code := t.captureExpr(arg)
						if t.isGoAnyVar(arg) || t.getDeclaredGoType(arg).IsAny() || goCodeProducesAny(code) {
							t.w.writef("%s.(string)", code)
						} else {
							t.w.write(code)
						}
					}
					t.w.write(")")
					return
				}
			case "Date":
				if method == "now" || method == "Now" {
					t.w.addImport("time", "")
					t.w.write("float64(time.Now().UnixMilli())")
					return
				}
			case "Promise":
				if method == "resolve" || method == "Resolve" {
					t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
					t.w.write("promise.Resolve[any](")
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					return
				}
				if method == "all" || method == "All" {
					t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
					// Array literal: spread elements as variadic args with promise assertion
					if call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
						call.Arguments.Nodes[0].Kind == ast.KindArrayLiteralExpression {
						t.w.write("promise.All[any](")
						elems := call.Arguments.Nodes[0].AsArrayLiteralExpression().Elements
						if elems != nil {
							for i, elem := range elems.Nodes {
								if i > 0 {
									t.w.write(", ")
								}
								t.emitExpr(elem)
								t.w.write(".(*promise.Promise[any])")
							}
						}
						t.w.write(")")
					} else {
						// Variable/non-literal: use AllSlice for mixed []any
						t.w.write("promise.AllSlice(")
						t.emitCallArgs(call.Arguments)
						t.w.write(")")
					}
					return
				}
			}
		}
	}

	// If the callee is any-typed (e.g., handler(args)), cast to func.
	// Skip if the declared type is a function (concrete func var — no assertion needed).
	// Also check goAnyVars for parameters from []any callbacks (TS says func, Go says any).
	calleeIsGoAny := t.getGoType(call.Expression).IsAny()
	calleeIsGoAnyVar := false
	if call.Expression.Kind == ast.KindIdentifier {
		vn := goVarName(call.Expression.AsIdentifier().Text)
		if t.goAnyVars != nil && t.goAnyVars[vn] {
			calleeIsGoAnyVar = true
		}
	}
	// Use func assertion for: (1) checker says any AND declared type is not func, OR (2) goAnyVar
	if call.Expression.Kind == ast.KindIdentifier &&
		((calleeIsGoAny && t.getDeclaredGoType(call.Expression).Category != GoTypeFunc) || calleeIsGoAnyVar) {
		t.w.write("(")
		t.emitExpr(call.Expression)
		t.w.write(".(func(")
		if call.Arguments != nil {
			for i := range call.Arguments.Nodes {
				if i > 0 {
					t.w.write(", ")
				}
				t.w.write("any")
			}
		}
		t.w.write(") any))(")
		t.emitCallArgs(call.Arguments)
		t.w.write(")")
		return
	}

	// setTimeout/setInterval: emit as plain call without coercion (inline function)
	if call.Expression.Kind == ast.KindIdentifier {
		name := call.Expression.AsIdentifier().Text
		if name == "setTimeout" || name == "setInterval" {
			t.emitExpr(call.Expression)
			t.w.write("(")
			t.emitCallArgs(call.Arguments)
			t.w.write(")")
			return
		}
	}

	// Dynamic method call: obj[key](args) on struct/pointer → jsrt.CallMethod(obj, key, args...)
	if call.Expression.Kind == ast.KindElementAccessExpression {
		ea := call.Expression.AsElementAccessExpression()
		exprCode := t.captureExpr(ea.Expression)
		exprType := t.getGoType(ea.Expression)
		// Also check if expression is a known class instance (generic classes may resolve to any)
		useCallMethod := false
		if !exprType.IsAny() && !exprType.IsMap() && !exprType.IsSlice() &&
			(exprType.IsPointer() || (exprType.GoStr != "" && !strings.HasPrefix(exprType.GoStr, "any"))) {
			useCallMethod = true
		}
		// For identifiers with any type, check if the captured code names a known class instance
		if !useCallMethod && exprType.IsAny() && ea.Expression.Kind == ast.KindIdentifier {
			// Check if the expression has a class type via checker symbol
			if t.ck != nil {
				sym := t.ck.GetSymbolAtLocation(ea.Expression)
				if sym != nil {
					symType := t.ck.GetTypeOfSymbol(sym)
					if symType != nil {
						symTypeName := symType.Symbol().Name
						if t.classNames != nil && t.classNames[goExportedName(symTypeName)] {
							useCallMethod = true
						}
					}
				}
			}
		}
		if useCallMethod {
			key := t.captureExpr(ea.ArgumentExpression)

			// Try Union → Switch expansion: if the key's type is a union of string literals,
			// generate a compile-time switch instead of reflect-based CallMethod.
			if t.ck != nil {
				keyType := t.ck.GetTypeAtLocation(ea.ArgumentExpression)
				if keyType != nil && keyType.Flags()&checker.TypeFlagsUnion != 0 {
					members := keyType.Types()
					var literals []string
					for _, m := range members {
						if m.IsStringLiteral() {
							if v, ok := m.AsLiteralType().Value().(string); ok {
								literals = append(literals, v)
							}
						}
					}
					if len(literals) > 0 && len(literals) == len(members) {
						// All union members are string literals → emit switch
						t.w.writef("func() any { switch %s {", key)
						t.w.newline()
						for _, lit := range literals {
							goMethod := goExportedName(lit)
							t.w.writef("case %q:", lit)
							t.w.newline()
							t.w.writef("return %s.%s(", exprCode, goMethod)
							if call.Arguments != nil {
								for ai, arg := range call.Arguments.Nodes {
									if ai > 0 {
										t.w.write(", ")
									}
									t.emitExpr(arg)
								}
							}
							t.w.write(")")
							t.w.newline()
						}
						t.w.write("default:")
						t.w.newline()
						t.w.write("return nil")
						t.w.newline()
						t.w.write("} }()")
						return
					}
				}
			}

			// Fallback: reflect-based dynamic method call
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.writef("jsrt.CallMethod(%s, jsrt.GoExportedName(%s)", exprCode, key)
			if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
				for _, arg := range call.Arguments.Nodes {
					t.w.write(", ")
					t.emitExpr(arg)
				}
			}
			t.w.write(")")
			return
		}
	}

	// General function/method call
	t.emitExpr(call.Expression)
	t.w.write("(")
	t.emitCallArgsWithCoercion(call)
	t.w.write(")")
}

// isConsoleCall checks if a call is console.log/error/warn/info.
func (t *Transpiler) isConsoleCall(call *ast.CallExpression) bool {
	if call.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	prop := call.Expression.AsPropertyAccessExpression()
	if prop.Expression.Kind != ast.KindIdentifier {
		return false
	}
	return prop.Expression.AsIdentifier().Text == "console"
}

// emitConsoleCall generates console.Log/Error/Warn calls.
func (t *Transpiler) emitConsoleCall(call *ast.CallExpression) {
	prop := call.Expression.AsPropertyAccessExpression()
	method := nodeText(prop.Name())

	t.w.addImport("github.com/i2y/ramune/jsrt/console", "")

	goMethod := "Log"
	switch method {
	case "log":
		goMethod = "Log"
	case "error":
		goMethod = "Error"
	case "warn":
		goMethod = "Warn"
	case "info":
		goMethod = "Info"
	case "debug":
		goMethod = "Debug"
	}

	t.w.writef("console.%s(", goMethod)
	t.emitCallArgs(call.Arguments)
	t.w.write(")")
}

// emitPropertyAccess handles obj.prop expressions using type-driven dispatch.
func (t *Transpiler) emitPropertyAccess(node *ast.Node) {
	prop := node.AsPropertyAccessExpression()
	propName := nodeText(prop.Name())
	isPrivateProp := strings.HasPrefix(propName, "#") || ast.IsPrivateIdentifier(prop.Name())
	if strings.HasPrefix(propName, "#") {
		propName = propName[1:]
	}

	// --- 1. Optional chaining (structural, not type-driven) ---
	if prop.QuestionDotToken != nil {
		t.emitOptionalPropertyAccess(prop, propName)
		return
	}

	// --- 2. Symbol-driven dispatch (enum, class static, Math) ---
	if prop.Expression.Kind == ast.KindIdentifier {
		objName := prop.Expression.AsIdentifier().Text
		if objName == "Math" {
			t.emitMathAccess(propName)
			return
		}
		if objName == "Number" {
			t.emitNumberAccess(propName)
			return
		}
		if objName == "crypto" && propName == "subtle" {
			t.w.addImport("github.com/i2y/ramune/jsrt/web", "web")
			t.w.write("web.Subtle")
			return
		}
		if t.ck != nil {
			sym := t.ck.GetSymbolAtLocation(prop.Expression)
			if sym != nil {
				if sym.Flags&ast.SymbolFlagsEnum != 0 {
					t.w.writef("%s%s", goTypeName(objName), goExportedName(propName))
					return
				}
				if sym.Flags&ast.SymbolFlagsClass != 0 {
					t.w.writef("%s_%s", goTypeName(objName), goExportedName(propName))
					return
				}
			}
		}
	}

	// --- 3. .length (special property) ---
	if propName == "length" {
		declType := t.getDeclaredGoType(prop.Expression)
		if declType.IsAny() {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("jsrt.Len(")
			t.emitExpr(prop.Expression)
			t.w.write(")")
		} else {
			t.w.write("len(")
			// Dereference *string params
			if prop.Expression.Kind == ast.KindIdentifier && t.goPtrStringVars != nil {
				vn := goVarName(prop.Expression.AsIdentifier().Text)
				if t.goPtrStringVars[vn] {
					t.w.write("*")
				}
			}
			t.emitExpr(prop.Expression)
			t.w.write(")")
		}
		return
	}

	// --- 4. Narrowed types (discriminant check) ---
	if prop.Expression.Kind == ast.KindIdentifier && t.narrowedTypes != nil {
		varName := prop.Expression.AsIdentifier().Text
		if concreteType, ok := t.narrowedTypes[varName]; ok {
			t.emitExpr(prop.Expression)
			t.writeTypeAssertion(concreteType)
			t.w.writef(".%s", goExportedName(propName))
			return
		}
	}

	// --- 5. TYPE-DRIVEN DISPATCH ---
	exprType := t.getGoType(prop.Expression)         // narrowed type from checker
	declType := t.getDeclaredGoType(prop.Expression) // declared type for Go static type
	// Override with emitted Go type when available (more accurate than checker)
	emittedType := t.getEmittedGoType(prop.Expression)
	if emittedType.GoStr != "" {
		declType = emittedType
	}

	goField := goExportedName(propName)
	if isPrivateProp {
		goField = goVarName(toCamelCase(propName))
	}
	if t.privateFields != nil {
		if pf, ok := t.privateFields[propName]; ok {
			goField = pf
		}
	}

	switch {
	// 5a. Declared type is any/JSObject at Go level (or Go-level any via goAnyVars)
	case (declType.IsAny() || t.isGoAnyExpression(prop.Expression)) && !t.isPackageRef(prop.Expression):
		if !exprType.IsAny() {
			// Checker narrowed to concrete type → type assertion + field
			obj := t.captureExpr(prop.Expression)
			if codeProducesConcreteType(obj) {
				t.w.writef("%s.%s", obj, goField)
			} else {
				t.w.writef("%s.(%s).%s", obj, exprType.GoStr, goField)
			}
		} else {
			// No narrowing → JSObject chain
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			obj := t.captureExpr(prop.Expression)
			// Check if the result type is concrete (from checker)
			resultType := t.getGoType(node)
			if !resultType.IsAny() && resultType.GoStr != "" && !t.suppressTypeAssertion {
				t.w.writef("jsrt.Obj(%s).Get(%q).Unwrap().(%s)", obj, goExportedName(propName), resultType.GoStr)
			} else {
				t.w.writef("jsrt.Obj(%s).Get(%q).Unwrap()", obj, goExportedName(propName))
			}
		}

	// 5b. Discriminated union interface → getter method
	case declType.Category == GoTypeInterface:
		t.emitExpr(prop.Expression)
		t.w.writef(".Get%s()", goExportedName(propName))

	// 5c. Primitive type cannot have fields — use jsrt.Obj (intersection type assertions like string & HtmlEscaped)
	case declType.Category == GoTypePrimitive:
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		obj := t.captureExpr(prop.Expression)
		t.w.writef("jsrt.Obj(%s).Get(%q).Unwrap()", obj, goExportedName(propName))

	// 5d. Concrete type → direct field access (with getter check)
	default:
		if t.ck != nil {
			sym := t.ck.GetSymbolAtLocation(node)
			if sym != nil && sym.Flags&ast.SymbolFlagsGetAccessor != 0 {
				t.emitExpr(prop.Expression)
				t.w.writef(".%s()", goExportedName(propName))
				return
			}
		}
		t.emitExpr(prop.Expression)
		t.w.writef(".%s", goField)
	}
}

// emitOptionalPropertyAccess handles obj?.prop
func (t *Transpiler) emitOptionalPropertyAccess(prop *ast.PropertyAccessExpression, propName string) {
	obj := t.captureExpr(prop.Expression)

	// ?.length → len() with nil check
	if propName == "length" {
		t.w.writef("func() any { if %s != nil { return len(%s) }; return nil }()", obj, obj)
		return
	}

	declType := t.getDeclaredGoType(prop.Expression)
	if declType.IsAny() && !t.isPackageRef(prop.Expression) {
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.writef("jsrt.Obj(%s).Get(%q).Unwrap()", obj, goExportedName(propName))
	} else {
		goField := goExportedName(propName)
		if t.privateFields != nil {
			if pf, ok := t.privateFields[propName]; ok {
				goField = pf
			}
		}
		t.w.writef("func() any { if %s != nil { return %s.%s }; return nil }()", obj, obj, goField)
	}
}

// emitMathAccess handles Math.* property access.
func (t *Transpiler) emitMathAccess(prop string) {
	t.w.addImport("math", "")
	switch prop {
	case "PI":
		t.w.write("math.Pi")
	case "E":
		t.w.write("math.E")
	case "floor":
		t.w.write("math.Floor")
	case "ceil":
		t.w.write("math.Ceil")
	case "round":
		t.w.write("math.Round")
	case "abs":
		t.w.write("math.Abs")
	case "sqrt":
		t.w.write("math.Sqrt")
	case "pow":
		t.w.write("math.Pow")
	case "min":
		t.w.write("min") // Go 1.21+ built-in — works with int and float64
	case "max":
		t.w.write("max") // Go 1.21+ built-in — works with int and float64
	case "log":
		t.w.write("math.Log")
	case "random":
		t.w.addImport("math/rand", "")
		t.w.write("rand.Float64")
	default:
		t.w.writef("math.%s", goExportedName(prop))
	}
}

// emitNumberAccess handles Number.* property access and static method refs.
// Numeric constants (MAX_VALUE, EPSILON, POSITIVE_INFINITY, NaN, ...) are
// written as Go literals or `math` package calls. Static methods return a
// callable expression; the surrounding CallExpression then appends the args.
func (t *Transpiler) emitNumberAccess(prop string) {
	switch prop {
	case "MAX_VALUE":
		t.w.addImport("math", "")
		t.w.write("math.MaxFloat64")
	case "MIN_VALUE":
		t.w.addImport("math", "")
		t.w.write("math.SmallestNonzeroFloat64")
	case "MAX_SAFE_INTEGER":
		t.w.write("float64(9007199254740991)")
	case "MIN_SAFE_INTEGER":
		t.w.write("float64(-9007199254740991)")
	case "EPSILON":
		t.w.write("2.220446049250313e-16")
	case "POSITIVE_INFINITY":
		t.w.addImport("math", "")
		t.w.write("math.Inf(1)")
	case "NEGATIVE_INFINITY":
		t.w.addImport("math", "")
		t.w.write("math.Inf(-1)")
	case "NaN":
		t.w.addImport("math", "")
		t.w.write("math.NaN()")
	case "isNaN":
		// Number.isNaN is the strict form: only actual NaN values, never
		// coerces. For statically-typed float64 inputs this is math.IsNaN.
		t.w.addImport("math", "")
		t.w.write("math.IsNaN")
	case "isFinite":
		t.w.addImport("math", "")
		t.w.write("func(n float64) bool { return !math.IsInf(n, 0) && !math.IsNaN(n) }")
	case "isInteger":
		t.w.addImport("math", "")
		t.w.write("func(n float64) bool { return !math.IsInf(n, 0) && !math.IsNaN(n) && math.Trunc(n) == n }")
	case "isSafeInteger":
		t.w.addImport("math", "")
		t.w.write("func(n float64) bool { return !math.IsInf(n, 0) && !math.IsNaN(n) && math.Trunc(n) == n && math.Abs(n) <= 9007199254740991 }")
	case "parseFloat":
		t.w.addImport("strconv", "")
		t.w.write("func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }")
	default:
		t.w.writef("/* unsupported Number.%s */", prop)
	}
}

// emitElementAccess handles obj[expr] expressions.
func (t *Transpiler) emitElementAccess(node *ast.Node) {
	ea := node.AsElementAccessExpression()

	// Optional chaining: obj?.[key] → func() any { if obj != nil { return obj[key] }; return nil }()
	if ea.QuestionDotToken != nil {
		obj := t.captureExpr(ea.Expression)
		key := t.captureExpr(ea.ArgumentExpression)
		// Check if obj is *string at Go level (tracked param)
		isPtrString := false
		if ea.Expression.Kind == ast.KindIdentifier {
			vn := goVarName(ea.Expression.AsIdentifier().Text)
			if t.goPtrStringVars != nil && t.goPtrStringVars[vn] {
				isPtrString = true
			}
		}
		if isPtrString {
			// *string?.[key] → dereference before indexing
			t.w.writef("func() any { if %s != nil { return string((*%s)[%s]) }; return nil }()", obj, obj, key)
		} else {
			t.w.writef("func() any { if %s != nil { return %s[%s] }; return nil }()", obj, obj, key)
		}
		return
	}

	// If expression type is any at Go level, use runtime indexing
	exprCode := t.captureExpr(ea.Expression)
	goLevelAny := t.getGoType(ea.Expression).IsAny() || goCodeProducesAny(exprCode)
	if goLevelAny {
		// Check if the variable has a known concrete type (e.g., []byte from new Uint8Array)
		if ea.Expression.Kind == ast.KindIdentifier {
			varName := goVarName(ea.Expression.AsIdentifier().Text)
			if t.concreteVarTypes != nil {
				if _, ok := t.concreteVarTypes[varName]; ok {
					// Use direct indexing for known concrete slice types
					t.emitExpr(ea.Expression)
					t.w.write("[")
					t.emitExpr(ea.ArgumentExpression)
					t.w.write("]")
					return
				}
			}
		}
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		key := t.captureExpr(ea.ArgumentExpression)
		t.w.writef("jsrt.Index(%s, %s)", exprCode, key)
		return
	}

	// Deref pointer types for indexing: (*str)[i]
	declType := t.getDeclaredGoType(ea.Expression)
	isStringIndex := declType.IsString()
	if isStringIndex {
		t.w.write("string(")
	}
	if strings.HasPrefix(declType.GoStr, "*") {
		t.w.write("(*")
		t.emitExpr(ea.Expression)
		t.w.write(")")
	} else {
		t.emitExpr(ea.Expression)
	}
	t.w.write("[")
	t.emitExpr(ea.ArgumentExpression)
	t.w.write("]")
	if isStringIndex {
		t.w.write(")")
	}
}

// emitTemplateExpr handles template literals: `Hello, ${name}!`
func (t *Transpiler) emitTemplateExpr(node *ast.Node) {
	tmpl := node.AsTemplateExpression()
	t.w.addImport("fmt", "")

	// Collect format parts and arguments
	var formatParts []string
	var args []string

	// Head text
	headText := tmpl.Head.AsTemplateHead().Text
	formatParts = append(formatParts, escapePercent(headText))

	// Template spans
	if tmpl.TemplateSpans != nil {
		for _, span := range tmpl.TemplateSpans.Nodes {
			ts := span.AsTemplateSpan()

			// Get the type of the expression to choose format specifier
			spec := "%v"
			if t.ck != nil {
				exprType := t.ck.GetTypeAtLocation(ts.Expression)
				if exprType != nil {
					goType := t.tm.goType(exprType)
					spec = formatSpecifier(goType)
				}
			}

			formatParts = append(formatParts, spec)

			args = append(args, t.captureExpr(ts.Expression))

			// Tail/middle text
			var literalText string
			if ts.Literal.Kind == ast.KindTemplateMiddle {
				literalText = ts.Literal.AsTemplateMiddle().Text
			} else {
				literalText = ts.Literal.AsTemplateTail().Text
			}
			formatParts = append(formatParts, escapePercent(literalText))
		}
	}

	formatStr := strings.Join(formatParts, "")
	if len(args) == 0 {
		t.w.writef("%q", formatStr)
	} else {
		t.w.writef("fmt.Sprintf(%q, %s)", formatStr, strings.Join(args, ", "))
	}
}

// emitConditionalExpr handles ternary: cond ? a : b
// Emits as: func() any { if cond { return a } else { return b } }()
// With instanceof narrowing support for the then-branch.
// exprProducesInt returns true if the expression produces an int value in Go,
// even if the TypeScript checker types it as number (float64).
func (t *Transpiler) exprProducesInt(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindCallExpression:
		call := node.AsCallExpression()
		if call.Expression.Kind == ast.KindPropertyAccessExpression {
			prop := call.Expression.AsPropertyAccessExpression()
			methodName := nodeText(prop.Name())
			if methodName == "charCodeAt" || methodName == "indexOf" || methodName == "lastIndexOf" {
				return true
			}
			// Math.min/max with all-int args produces int in Go
			if (methodName == "min" || methodName == "max") &&
				prop.Expression.Kind == ast.KindIdentifier &&
				prop.Expression.AsIdentifier().Text == "Math" &&
				call.Arguments != nil {
				allInt := true
				for _, arg := range call.Arguments.Nodes {
					if !t.isIntExpr(arg) {
						allInt = false
						break
					}
				}
				if allInt {
					return true
				}
			}
		}
	case ast.KindNumericLiteral:
		text := node.AsNumericLiteral().Text
		if !strings.Contains(text, ".") {
			return true
		}
	case ast.KindPropertyAccessExpression:
		prop := node.AsPropertyAccessExpression()
		if nodeText(prop.Name()) == "length" {
			return true
		}
	}
	return false
}

func (t *Transpiler) emitConditionalExpr(node *ast.Node) {
	cond := node.AsConditionalExpression()

	// Detect instanceof narrowing: x instanceof Foo ? x.field : other
	varName, concreteType := t.detectInstanceofNarrowing(cond.Condition)

	// Use the checker's result type for the IIFE return type (instead of always `any`)
	retType := "any"
	if t.ck != nil {
		typ := t.ck.GetTypeAtLocation(node)
		if typ != nil {
			goType := t.tm.goType(typ)
			if goType != "" && goType != "any" {
				retType = goType
			}
		}
	}
	// Override to int when both branches produce int (e.g., charCodeAt vs 0)
	if retType == "float64" && t.exprProducesInt(cond.WhenTrue) && t.exprProducesInt(cond.WhenFalse) {
		retType = "int"
	}
	t.w.writef("func() %s { if ", retType)
	t.emitCondition(cond.Condition)
	t.w.write(" { ")

	// Apply narrowing for the then-branch
	savedNarrowed := t.narrowedTypes
	if varName != "" && concreteType != "" {
		newMap := make(map[string]string)
		for k, v := range savedNarrowed {
			newMap[k] = v
		}
		newMap[varName] = concreteType
		t.narrowedTypes = newMap
	}

	t.w.write("return ")
	t.emitConditionalBranch(cond.WhenTrue, retType)
	t.narrowedTypes = savedNarrowed

	t.w.write(" } else { return ")
	t.emitConditionalBranch(cond.WhenFalse, retType)
	t.w.write(" } }()")
}

// emitConditionalBranch emits a ternary branch expression, adding type assertion if needed.
func (t *Transpiler) emitConditionalBranch(node *ast.Node, retType string) {
	// any → concrete assertion
	if retType != "any" && retType != "" && t.rightSideProducesGoAny(node) {
		t.emitExpr(node)
		t.writeTypeAssertionChecked(retType, node)
		return
	}
	// int → float64 conversion
	if retType == "float64" && t.isIntExpr(node) {
		t.w.write("float64(")
		t.emitExpr(node)
		t.w.write(")")
		return
	}
	// pointer return type conversions
	if strings.HasPrefix(retType, "*") {
		inner := retType[1:]
		// int → *float64
		if inner == "float64" && t.isIntExpr(node) {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("jsrt.Ptr(float64(")
			t.emitExpr(node)
			t.w.write("))")
			return
		}
		// nested ternary T → *T (e.g., float64 IIFE → *float64)
		if node.Kind == ast.KindConditionalExpression && t.ck != nil {
			typ := t.ck.GetTypeAtLocation(node)
			if typ != nil && t.tm.goType(typ) == inner {
				t.w.addImport("github.com/i2y/ramune/jsrt", "")
				t.w.write("jsrt.Ptr(")
				t.emitExpr(node)
				t.w.write(")")
				return
			}
		}
		// General T → *T: branch produces inner type, outer expects pointer
		branchType := t.getGoType(node)
		if branchType.GoStr == inner && !branchType.IsPointer() {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("jsrt.Ptr(")
			t.emitExpr(node)
			t.w.write(")")
			return
		}
	}
	// *T → T dereference: branch produces pointer but return type is non-pointer
	if !strings.HasPrefix(retType, "*") && retType != "any" && retType != "" {
		if node.Kind == ast.KindIdentifier {
			varName := goVarName(node.AsIdentifier().Text)
			if t.goPtrStringVars != nil && t.goPtrStringVars[varName] && retType == "string" {
				t.w.write("*")
				t.emitExpr(node)
				return
			}
		}
		branchType := t.getGoType(node)
		if branchType.GoStr == "*"+retType {
			t.w.write("*")
			t.emitExpr(node)
			return
		}
	}
	t.emitExpr(node)
}

// emitArrowFunction handles arrow functions: (x) => x + 1
func (t *Transpiler) emitArrowFunction(node *ast.Node) {
	arrow := node.AsArrowFunction()
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	// In .then() callback: force all param types and return type to any
	if t.inThenCallback {
		t.inThenCallback = false // consume the flag
		params := node.Parameters()
		if len(params) > 0 {
			t.w.write("func(")
			for i, p := range params {
				if i > 0 {
					t.w.write(", ")
				}
				pName := "_"
				if p.Name() != nil && p.Name().Kind == ast.KindIdentifier {
					pName = goVarName(p.Name().AsIdentifier().Text)
					if t.goAnyVars == nil {
						t.goAnyVars = make(map[string]bool)
					}
					t.goAnyVars[pName] = true
				}
				t.w.writef("%s any", pName)
			}
			t.w.write(") any")
		} else {
			t.w.write("func(_ any) any")
		}
		savedRetType := t.currentRetType
		t.currentRetType = "any"
		savedAsync := t.inAsyncBody
		t.inAsyncBody = false
		body := arrow.Body
		if body == nil {
			t.w.write(" {}")
		} else if body.Kind == ast.KindBlock {
			t.emitBlock(body)
		} else {
			t.w.write(" { return ")
			t.emitExpr(body)
			t.w.write(" }")
		}
		t.currentRetType = savedRetType
		t.inAsyncBody = savedAsync
		return
	}

	// Save/reset *string param tracking before parameter emission
	savedPtrStringVars := t.goPtrStringVars
	t.goPtrStringVars = nil

	// Check if any param uses destructuring (ArrayBindingPattern/ObjectBindingPattern)
	var destructuringParams []int // indices of params that need destructuring
	params := node.Parameters()
	if params != nil {
		for pi, p := range params {
			if p.Name() != nil && (p.Name().Kind == ast.KindArrayBindingPattern || p.Name().Kind == ast.KindObjectBindingPattern) {
				destructuringParams = append(destructuringParams, pi)
			}
		}
	}

	if t.pendingFuncName != "" {
		t.w.writef("func %s(", t.pendingFuncName)
		t.pendingFuncName = ""
	} else {
		t.w.write("func(")
	}

	if len(destructuringParams) > 0 {
		// Emit with placeholder names for destructuring params
		for pi, p := range params {
			if pi > 0 {
				t.w.write(", ")
			}
			hasDestructuring := false
			for _, di := range destructuringParams {
				if di == pi {
					hasDestructuring = true
					break
				}
			}
			if hasDestructuring {
				t.w.writef("__p%d", pi)
			} else if p.Name() != nil && p.Name().Kind == ast.KindIdentifier {
				t.w.write(goParamName(p.Name().AsIdentifier().Text))
			} else {
				t.w.writef("p%d", pi)
			}
			// Param type
			goType := "any"
			if t.ck != nil {
				pt := t.ck.GetTypeAtLocation(p)
				if pt != nil {
					goType = t.tm.goType(pt)
				}
			}
			if goType == "" {
				goType = "any"
			}
			t.w.writef(" %s", goType)
		}
	} else {
		t.emitParameterList(node)
	}
	t.w.write(")")

	// Return type
	retType := t.getFuncReturnType(node)
	if isAsync {
		t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
		innerType := retType
		if strings.HasPrefix(innerType, "*promise.Promise[") {
			innerType = innerType[len("*promise.Promise[") : len(innerType)-1]
		}
		if innerType == "" {
			innerType = "any"
		}
		retType = innerType
		t.w.writef(" *promise.Promise[%s]", innerType)
	} else if retType != "" {
		t.w.writef(" %s", retType)
	}

	savedRetType := t.currentRetType
	t.currentRetType = retType

	// Save/restore inAsyncBody for all functions — non-async resets it
	savedAsync := t.inAsyncBody
	if !isAsync {
		t.inAsyncBody = false
	}

	body := arrow.Body
	if body == nil {
		t.w.write(" {}")
		t.currentRetType = savedRetType
		t.inAsyncBody = savedAsync
		t.goPtrStringVars = savedPtrStringVars
		return
	}

	if isAsync {
		innerType := retType
		if innerType == "" {
			innerType = "any"
		}
		t.w.openBlock()
		t.w.writef("return promise.New[%s](func(__resolve func(%s), __reject func(error))", innerType, innerType)
		t.inAsyncBody = true
		if body.Kind == ast.KindBlock {
			t.emitBlock(body)
		} else {
			t.w.openBlock()
			t.w.write("__resolve(")
			t.emitExpr(body)
			t.w.write(")")
			t.w.newline()
			t.w.write("return")
			t.w.newline()
			t.w.closeBlock()
		}
		t.inAsyncBody = savedAsync
		t.w.writeln(")")
		if t.inCallArg {
			t.w.closeBlockInline()
		} else {
			t.w.closeBlock()
		}
	} else if body.Kind == ast.KindBlock {
		block := body.AsBlock()
		t.w.openBlock()
		// Emit destructuring for params with ArrayBindingPattern/ObjectBindingPattern
		for _, di := range destructuringParams {
			if di < len(params) && params[di].Name() != nil {
				t.emitDestructureFromParam(params[di].Name(), fmt.Sprintf("__p%d", di))
			}
		}
		t.emitParamDestructuring(node)
		if block.Statements != nil {
			// Hoist function declarations
			for _, stmt := range block.Statements.Nodes {
				if stmt.Kind == ast.KindFunctionDeclaration && stmt.Name() != nil {
					funcName := goVarName(nodeText(stmt.Name()))
					t.w.writef("var %s func(", funcName)
					t.emitParameterList(stmt)
					t.w.write(")")
					retType := t.getFuncReturnType(stmt)
					if retType != "" {
						t.w.writef(" %s", retType)
					}
					t.w.newline()
				}
			}
			for _, stmt := range block.Statements.Nodes {
				if stmt.Kind == ast.KindFunctionDeclaration {
					t.emitFunctionDeclAssignment(stmt)
				} else {
					t.emitStatement(stmt)
				}
			}
			// Add default return for arrow function body if needed
			if retType != "" && !isAsync {
				lastIsReturn := false
				if len(block.Statements.Nodes) > 0 {
					last := block.Statements.Nodes[len(block.Statements.Nodes)-1]
					lastIsReturn = last.Kind == ast.KindReturnStatement
				}
				if !lastIsReturn {
					t.w.writeln(t.defaultReturn())
				}
			}
		}
		if t.inCallArg {
			t.w.closeBlockInline()
		} else {
			t.w.closeBlock()
		}
	} else {
		// Expression body: (x) => x + 1 → func(x float64) float64 { return x + 1 }
		if len(destructuringParams) > 0 {
			t.w.write(" { ")
			for _, di := range destructuringParams {
				if di < len(params) && params[di].Name() != nil {
					t.emitDestructureFromParam(params[di].Name(), fmt.Sprintf("__p%d", di))
				}
			}
			t.w.write("return ")
		} else {
			t.w.write(" { return ")
		}
		if retType != "" && retType != "any" {
			code := t.captureExpr(body)
			t.w.write(code)
			// Add assertion only when the TOP-LEVEL expression produces any
			// (HasPrefix for jsrt.Index, HasSuffix for .Unwrap(), full match for func-call on any)
			if strings.HasPrefix(code, "jsrt.Index(") ||
				strings.HasSuffix(code, ".Unwrap()") ||
				(strings.Contains(code, ".(func(") && strings.HasSuffix(code, ")")) {
				t.writeTypeAssertion(retType)
			}
		} else {
			t.emitExpr(body)
		}
		t.w.write(" }")
	}
	t.currentRetType = savedRetType
	t.inAsyncBody = savedAsync
	t.goPtrStringVars = savedPtrStringVars
}

// emitFunctionExpression handles function expressions: function(x) { ... }
func (t *Transpiler) emitFunctionExpression(node *ast.Node) {
	if t.pendingFuncName != "" {
		t.w.writef("func %s(", t.pendingFuncName)
		t.pendingFuncName = ""
	} else {
		t.w.write("func(")
	}
	t.emitParameterList(node)
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	if retType != "" {
		t.w.writef(" %s", retType)
	}

	savedRetType := t.currentRetType
	t.currentRetType = retType

	body := node.Body()
	if body != nil {
		t.emitBlock(body)
	} else {
		t.w.write(" {}")
	}
	t.currentRetType = savedRetType
}

// emitArrayLiteral handles [1, 2, 3].
func (t *Transpiler) emitArrayLiteral(node *ast.Node) {
	arr := node.AsArrayLiteralExpression()

	// Try to determine element type from the checker
	elemType := "any"
	if t.ck != nil {
		arrType := t.ck.GetTypeAtLocation(node)
		if arrType != nil {
			goType := t.tm.goType(arrType)
			if et, ok := sliceElemType(goType); ok {
				elemType = et
			}
		}
	}
	// Fallback: use declaration context (e.g., const users: User[] = [...])
	if elemType == "any" {
		if et, ok := sliceElemType(t.declContext); ok {
			elemType = et
		}
	}

	// Check for spread elements
	hasSpread := false
	if arr.Elements != nil {
		for _, elem := range arr.Elements.Nodes {
			if elem.Kind == ast.KindSpreadElement {
				hasSpread = true
				break
			}
		}
	}

	if hasSpread && arr.Elements != nil {
		// Chain append calls: append(append([]T{literal_elems...}, spread1...), spread2...)
		// Group consecutive non-spread elements into a literal, then append spreads
		t.w.writef("func() []%s {\n", elemType)
		t.w.writef("__s := []%s{}\n", elemType)
		for _, elem := range arr.Elements.Nodes {
			if elem.Kind == ast.KindSpreadElement {
				spread := elem.AsSpreadElement()
				t.w.write("__s = append(__s, ")
				t.emitExpr(spread.Expression)
				t.w.writeln("...)")
			} else {
				t.w.write("__s = append(__s, ")
				t.emitExpr(elem)
				t.w.writeln(")")
			}
		}
		t.w.write("return __s }()")
	} else {
		t.w.writef("[]%s{", elemType)
		if arr.Elements != nil {
			for i, elem := range arr.Elements.Nodes {
				if i > 0 {
					t.w.write(", ")
				}
				t.emitExpr(elem)
			}
		}
		t.w.write("}")
	}
}

// emitObjectLiteral handles { key: value, ... }.
func (t *Transpiler) emitObjectLiteral(node *ast.Node) {
	obj := node.AsObjectLiteralExpression()

	// Determine the target struct type for this object literal.
	typeName := ""

	// Try 1: Use the checker's type for this node
	if t.ck != nil {
		objType := t.ck.GetTypeAtLocation(node)
		if objType != nil {
			sym := objType.Symbol()
			if sym != nil && sym.Name != "" && !strings.HasPrefix(sym.Name, "__") && isValidGoIdentifier(sym.Name) {
				typeName = goTypeName(sym.Name)
			}
		}
	}

	// Try 2: Use return context (only set during return statement emission)
	if typeName == "" && t.returnContext != "" && isValidGoIdentifier(t.returnContext) {
		switch t.returnContext {
		case "float64", "string", "bool", "int", "any":
		default:
			typeName = t.returnContext
		}
	}

	// Try 3: Use declaration context (e.g., element of User[] array)
	if typeName == "" && t.declContext != "" {
		elemType := t.declContext
		if et, ok := sliceElemType(elemType); ok {
			elemType = et
		}
		if isValidGoIdentifier(elemType) {
			switch elemType {
			case "float64", "string", "bool", "int", "any":
			default:
				typeName = elemType
			}
		}
	}

	// If typeName is a discriminated union interface, resolve to the concrete type
	// by inspecting the discriminant property (e.g., kind: "circle" → Circle)
	if typeName != "" {
		typeName = t.resolveDiscriminatedType(typeName, obj)
	}

	if typeName != "" {
		t.w.writef("%s{", typeName)
		if obj.Properties != nil {
			for i, prop := range obj.Properties.Nodes {
				if i > 0 {
					t.w.write(", ")
				}
				if prop.Kind == ast.KindPropertyAssignment {
					pa := prop.AsPropertyAssignment()
					name := prop.Name()
					if name != nil && name.Kind == ast.KindIdentifier {
						t.w.writef("%s: ", goExportedName(name.AsIdentifier().Text))
					}
					t.emitExpr(pa.Initializer)
				} else if prop.Kind == ast.KindShorthandPropertyAssignment {
					name := prop.Name()
					if name != nil && name.Kind == ast.KindIdentifier {
						id := name.AsIdentifier().Text
						t.w.writef("%s: %s", goExportedName(id), goVarName(id))
					}
				}
			}
		}
		t.w.write("}")
	} else {
		// Fallback: emit as map — use checker type, returnContext, or declContext for typed maps
		mapType := "map[string]any"
		if t.ck != nil {
			objType := t.ck.GetTypeAtLocation(node)
			if objType != nil {
				goStr := t.tm.goType(objType)
				if strings.HasPrefix(goStr, "map[") {
					mapType = goStr
				}
			}
		}
		// Use return context or decl context for concrete map types
		if mapType == "map[string]any" {
			if strings.HasPrefix(t.returnContext, "map[") {
				mapType = t.returnContext
			} else if strings.HasPrefix(t.declContext, "map[") {
				mapType = t.declContext
			}
		}
		t.w.writef("%s{", mapType)
		if obj.Properties != nil {
			first := true
			for _, prop := range obj.Properties.Nodes {
				switch prop.Kind {
				case ast.KindPropertyAssignment:
					if !first {
						t.w.write(", ")
					}
					first = false
					pa := prop.AsPropertyAssignment()
					name := prop.Name()
					if name != nil && name.Kind == ast.KindIdentifier {
						t.w.writef("%q: ", name.AsIdentifier().Text)
					} else if name != nil && name.Kind == ast.KindStringLiteral {
						t.w.writef("%q: ", name.AsStringLiteral().Text)
					} else if name != nil && name.Kind == ast.KindComputedPropertyName {
						t.w.write("/* computed */\"_\": ")
					}
					t.emitExpr(pa.Initializer)
				case ast.KindShorthandPropertyAssignment:
					if !first {
						t.w.write(", ")
					}
					first = false
					name := prop.Name()
					if name != nil && name.Kind == ast.KindIdentifier {
						id := name.AsIdentifier().Text
						t.w.writef("%q: %s", id, goVarName(id))
					}
				case ast.KindMethodDeclaration:
					if !first {
						t.w.write(", ")
					}
					first = false
					name := prop.Name()
					if name != nil {
						t.w.writef("%q: ", nodeText(name))
					}
					isMethodAsync := ast.HasSyntacticModifier(prop, ast.ModifierFlagsAsync)
					// Emit method as anonymous function
					t.w.write("func(")
					t.emitParameterList(prop)
					t.w.write(")")
					retType := t.getFuncReturnType(prop)
					if isMethodAsync {
						t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
						innerType := retType
						if strings.HasPrefix(innerType, "*promise.Promise[") {
							innerType = innerType[len("*promise.Promise[") : len(innerType)-1]
						}
						if innerType == "" {
							innerType = "any"
						}
						if !isPrimitiveOrCollectionType(innerType) {
							innerType = "any"
						}
						retType = innerType
						t.w.writef(" *promise.Promise[%s]", innerType)
					} else if retType != "" {
						t.w.writef(" %s", retType)
					}
					savedRetType := t.currentRetType
					t.currentRetType = retType
					savedAsync := t.inAsyncBody
					body := prop.Body()
					if body == nil {
						t.w.write(" {}")
					} else if isMethodAsync {
						innerType := retType
						if innerType == "" {
							innerType = "any"
						}
						t.w.openBlock()
						t.w.writef("return promise.New[%s](func(__resolve func(%s), __reject func(error))", innerType, innerType)
						t.inAsyncBody = true
						t.emitBlock(body)
						t.w.writeln(")")
						t.w.closeBlock()
					} else {
						savedNeedsDefault := t.needsDefaultReturn
						t.needsDefaultReturn = retType != ""
						t.emitBlock(body)
						t.needsDefaultReturn = savedNeedsDefault
					}
					t.currentRetType = savedRetType
					t.inAsyncBody = savedAsync
				case ast.KindSpreadAssignment:
					// Spread in object literal — skip (Go maps can't spread)
				default:
					// Skip unknown property kinds
				}
			}
			// Trailing comma for Go multi-line composite literals
			if !first {
				t.w.write(",")
			}
		}
		t.w.write("}")
	}
}

// emitNewExpr handles new MyClass(args).
func (t *Transpiler) emitNewExpr(node *ast.Node) {
	newExpr := node.AsNewExpression()
	if newExpr.Expression.Kind == ast.KindIdentifier {
		className := newExpr.Expression.AsIdentifier().Text

		// Special case: new String(x) → fmt.Sprint(x)
		if className == "String" {
			t.w.addImport("fmt", "")
			t.w.write("fmt.Sprint(")
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				t.emitExpr(newExpr.Arguments.Nodes[0])
			} else {
				t.w.write("\"\"")
			}
			t.w.write(")")
			return
		}

		// Special case: new Error("msg") → &jsrt.JSError{Message: "msg"}
		switch className {
		case "Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError":
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("&jsrt.JSError{Message: ")
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				t.emitExpr(newExpr.Arguments.Nodes[0])
			} else {
				t.w.writef("%q", className)
			}
			t.w.write("}")
			return
		}

		// Web API constructors: new Response(...) → web.NewResponse(...)
		switch className {
		case "Uint8Array":
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				arg := newExpr.Arguments.Nodes[0]
				if t.exprProducesInt(arg) || t.isIntExpr(arg) {
					// new Uint8Array(length) → make([]byte, length)
					t.w.write("make([]byte, ")
					t.emitExpr(arg)
					t.w.write(")")
				} else {
					// new Uint8Array(buffer) → convert to []byte
					t.w.write("func(v any) []byte { switch b := v.(type) { case []byte: return b; case string: return []byte(b); default: return nil } }(")
					t.emitExpr(arg)
					t.w.write(")")
				}
			} else {
				t.w.write("[]byte{}")
			}
			return
		case "ArrayBuffer":
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				t.w.write("make([]byte, int(")
				t.emitExpr(newExpr.Arguments.Nodes[0])
				t.w.write("))")
			} else {
				t.w.write("[]byte{}")
			}
			return
		case "DataView":
			// DataView is a view over ArrayBuffer — in Go, just pass through the value
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				t.emitExpr(newExpr.Arguments.Nodes[0])
			} else {
				t.w.write("nil")
			}
			return
		case "RegExp":
			// new RegExp(pattern) → regexp.MustCompile(pattern)
			t.w.addImport("regexp", "")
			t.w.write("regexp.MustCompile(")
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				t.emitExpr(newExpr.Arguments.Nodes[0])
			} else {
				t.w.write("\"\"")
			}
			t.w.write(")")
			return
		case "Promise":
			// new Promise((resolve, reject?) => ...) → promise.New[any](func(resolve func(any), reject func(error)) { ... })
			t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				cb := newExpr.Arguments.Nodes[0]
				if cb.Kind == ast.KindArrowFunction || cb.Kind == ast.KindFunctionExpression {
					params := cb.Parameters()
					// Emit custom executor signature
					t.w.write("promise.New[any](func(")
					if len(params) >= 1 && params[0].Name() != nil {
						t.w.writef("%s func(any)", goVarName(params[0].Name().AsIdentifier().Text))
					} else {
						t.w.write("__resolve func(any)")
					}
					if len(params) >= 2 && params[1].Name() != nil {
						t.w.writef(", %s func(error)", goVarName(params[1].Name().AsIdentifier().Text))
					} else {
						t.w.write(", __reject func(error)")
					}
					t.w.write(")")
					var body *ast.Node
					if cb.Kind == ast.KindArrowFunction {
						body = cb.AsArrowFunction().Body
					} else {
						body = cb.Body()
					}
					if body != nil && body.Kind == ast.KindBlock {
						t.emitBlock(body)
					} else if body != nil {
						t.w.write(" { ")
						t.emitExpr(body)
						t.w.write(" }")
					} else {
						t.w.write(" {}")
					}
					t.w.write(")")
				} else {
					t.w.write("promise.New[any](")
					t.emitCallArgs(newExpr.Arguments)
					t.w.write(")")
				}
			} else {
				t.w.write("promise.New[any](func(_ func(any), _ func(error)) {})")
			}
			return
		case "ReadableStream":
			// new ReadableStream({pull, cancel}) → pass options map directly (stub)
			if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
				t.emitExpr(newExpr.Arguments.Nodes[0])
			} else {
				t.w.write("nil")
			}
			return
		case "Response", "Request", "Headers", "URL", "TextEncoder", "TextDecoder", "FormData":
			t.w.addImport("github.com/i2y/ramune/jsrt/web", "web")
			t.w.writef("web.New%s(", className)
			if newExpr.Arguments != nil {
				for i, arg := range newExpr.Arguments.Nodes {
					if i > 0 {
						t.w.write(", ")
					}
					t.emitExpr(arg)
				}
			}
			t.w.write(")")
			return
		}

		// Check if class is imported from another package
		if pkg, ok := t.importedNames[className]; ok {
			t.w.writef("%s.New%s(", pkg, goTypeName(className))
		} else {
			t.w.writef("New%s(", goTypeName(className))
		}
	} else {
		t.w.write("/* unsupported new expression */New(")
	}
	if newExpr.Arguments != nil {
		for i, arg := range newExpr.Arguments.Nodes {
			if i > 0 {
				t.w.write(", ")
			}
			t.emitExpr(arg)
		}
	}
	t.w.write(")")
}

// emitParameterList emits the Go parameter list for a function-like node.
// emitParamDestructuring emits destructuring assignments for parameters that use
// ArrayBindingPattern (e.g., `([key, value]) => {...}` → `key := p0.([]any)[0]; value := p0.([]any)[1]`).
// Call this at the start of the function body.
func (t *Transpiler) emitParamDestructuring(node *ast.Node) {
	params := node.Parameters()
	if params == nil {
		return
	}
	for i, param := range params {
		name := param.Name()
		if name == nil || name.Kind != ast.KindArrayBindingPattern {
			continue
		}
		bp := name.AsBindingPattern()
		if bp.Elements == nil {
			continue
		}
		paramName := fmt.Sprintf("p%d", i)
		for j, elem := range bp.Elements.Nodes {
			if elem.Kind == ast.KindOmittedExpression {
				continue
			}
			elemName := elem.Name()
			if elemName == nil || elemName.Kind != ast.KindIdentifier {
				continue
			}
			localName := goVarName(elemName.AsIdentifier().Text)
			// Get the type of this destructured element from the checker
			elemTypeInfo := t.getGoType(elem)
			// Check param's Go type to decide if .([]any) assertion is needed
			paramGoType := "any"
			if t.arrayCallbackElemType != "" {
				paramGoType = t.arrayCallbackElemType
			}
			if paramGoType != "any" && strings.HasPrefix(paramGoType, "[]") {
				// Param is already a concrete slice (e.g., []any) — direct index access
				if !elemTypeInfo.IsAny() && elemTypeInfo.GoStr != "" {
					t.w.writef("var %s %s = %s[%d].(%s)", localName, elemTypeInfo.GoStr, paramName, j, elemTypeInfo.GoStr)
				} else {
					t.w.writef("%s := %s[%d]", localName, paramName, j)
				}
			} else {
				// Param is any — need type assertion to []any first
				if !elemTypeInfo.IsAny() && elemTypeInfo.GoStr != "" {
					t.w.writef("var %s %s = %s.([]any)[%d].(%s)", localName, elemTypeInfo.GoStr, paramName, j, elemTypeInfo.GoStr)
				} else {
					t.w.writef("%s := %s.([]any)[%d]", localName, paramName, j)
				}
			}
			t.w.newline()
		}
	}
}

func (t *Transpiler) emitParameterList(node *ast.Node) {
	params := node.Parameters()
	if params == nil {
		return
	}
	for i, param := range params {
		if i > 0 {
			t.w.write(", ")
		}
		name := param.Name()
		if name != nil && name.Kind == ast.KindIdentifier {
			t.w.write(goParamName(name.AsIdentifier().Text))
		} else {
			t.w.writef("p%d", i)
		}

		// Rest parameter: ...args → args ...T
		paramDecl := param.AsParameterDeclaration()
		isRest := paramDecl.DotDotDotToken != nil

		// Parameter type
		goType := "any"
		if t.ck != nil {
			paramType := t.ck.GetTypeAtLocation(param)
			if paramType != nil {
				goType = t.tm.goType(paramType)
			}
		}
		// In array callback context, override param types from the caller's array element type:
		// - Position 0: use the array's element type (from checker)
		// - Position 1 (index): always int (TS number → Go int for array indices)
		if t.arrayCallbackElemType != "" && i == 0 {
			goType = t.arrayCallbackElemType
		}
		if t.arrayCallbackIdx >= 0 && i == t.arrayCallbackIdx && goType == "float64" {
			goType = "int"
		}

		if isRest {
			// ...args: T[] → args ...T
			elemType := goType
			if et, ok := sliceElemType(goType); ok {
				elemType = et
			}
			t.w.writef(" ...%s", elemType)
		} else if goType != "" {
			t.w.writef(" %s", goType)
		}

		// Track parameter Go types in unified tracker
		if !isRest && name != nil && name.Kind == ast.KindIdentifier {
			pn := goVarName(name.AsIdentifier().Text)
			if pn != "_" {
				t.trackGoVarType(pn, goType)
			}
		}
		// Legacy: Track *string parameters
		if goType == "*string" && !isRest && name != nil && name.Kind == ast.KindIdentifier {
			pn := goVarName(name.AsIdentifier().Text)
			if t.goPtrStringVars == nil {
				t.goPtrStringVars = make(map[string]bool)
			}
			t.goPtrStringVars[pn] = true
		}
		// Legacy: Track any-typed parameters in goAnyVars
		if goType == "any" && !isRest && name != nil && name.Kind == ast.KindIdentifier {
			pn := goVarName(name.AsIdentifier().Text)
			if pn != "_" {
				if t.goAnyVars == nil {
					t.goAnyVars = make(map[string]bool)
				}
				t.goAnyVars[pn] = true
			}
		} else if goType != "any" && name != nil && name.Kind == ast.KindIdentifier {
			// Typed parameter shadows any goAnyVar with same name
			pn := goVarName(name.AsIdentifier().Text)
			if t.goAnyVars != nil {
				delete(t.goAnyVars, pn)
			}
		}
	}
}

// getFuncReturnType gets the Go return type string for a function-like node.
func (t *Transpiler) getFuncReturnType(node *ast.Node) string {
	if t.ck == nil {
		return ""
	}
	funcType := t.ck.GetTypeAtLocation(node)
	if funcType == nil {
		return ""
	}
	sigs := t.ck.GetSignaturesOfType(funcType, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return ""
	}
	retType := t.ck.GetReturnTypeOfSignature(sigs[0])
	return t.tm.goReturnType(retType)
}

// tsOperatorToGo converts a TypeScript binary operator token to a Go operator string.
func tsOperatorToGo(kind ast.Kind) string {
	switch kind {
	case ast.KindPlusToken:
		return "+"
	case ast.KindMinusToken:
		return "-"
	case ast.KindAsteriskToken:
		return "*"
	case ast.KindSlashToken:
		return "/"
	case ast.KindPercentToken:
		return "%"
	case ast.KindEqualsEqualsEqualsToken:
		return "=="
	case ast.KindExclamationEqualsEqualsToken:
		return "!="
	case ast.KindEqualsEqualsToken: // == (loose equality) — treat as ==
		return "=="
	case ast.KindExclamationEqualsToken:
		return "!="
	case ast.KindLessThanToken:
		return "<"
	case ast.KindGreaterThanToken:
		return ">"
	case ast.KindLessThanEqualsToken:
		return "<="
	case ast.KindGreaterThanEqualsToken:
		return ">="
	case ast.KindAmpersandAmpersandToken:
		return "&&"
	case ast.KindBarBarToken:
		return "||"
	case ast.KindEqualsToken:
		return "="
	case ast.KindPlusEqualsToken:
		return "+="
	case ast.KindMinusEqualsToken:
		return "-="
	case ast.KindAsteriskEqualsToken:
		return "*="
	case ast.KindSlashEqualsToken:
		return "/="
	case ast.KindPercentEqualsToken:
		return "%="
	case ast.KindAmpersandToken:
		return "&"
	case ast.KindBarToken:
		return "|"
	case ast.KindCaretToken:
		return "^"
	case ast.KindLessThanLessThanToken:
		return "<<"
	case ast.KindGreaterThanGreaterThanToken:
		return ">>"
	case ast.KindBarEqualsToken:
		return "|="
	case ast.KindAmpersandEqualsToken:
		return "&="
	case ast.KindCaretEqualsToken:
		return "^="
	case ast.KindLessThanLessThanEqualsToken:
		return "<<="
	case ast.KindGreaterThanGreaterThanEqualsToken:
		return ">>="
	default:
		return fmt.Sprintf("/* unknown op %s */", kind.String())
	}
}

// isAssignment returns true if the operator is an assignment operator.
func isAssignment(kind ast.Kind) bool {
	return ast.IsAssignmentOperator(kind)
}

// Method name lists removed — dispatch is purely type-driven via dispatchMethodCall.

// emitArrayMethodCall generates Go code for array method calls.
func (t *Transpiler) emitArrayMethodCall(call *ast.CallExpression) {
	prop := call.Expression.AsPropertyAccessExpression()
	method := nodeText(prop.Name())

	t.w.addImport("github.com/i2y/ramune/jsrt/array", "jsarray")

	// Check if the array expression is any at Go level (goAnyVar or declared any)
	// If so, we need to assert to []any and use any as element type
	arrayIsGoAny := t.isGoAnyExpression(prop.Expression) || t.rightSideProducesGoAny(prop.Expression)
	// Also check chained array method calls: res.filter(Boolean).map(...) — if inner is goAny, outer is too
	if !arrayIsGoAny && prop.Expression.Kind == ast.KindCallExpression {
		innerCall := prop.Expression.AsCallExpression()
		if innerCall.Expression.Kind == ast.KindPropertyAccessExpression {
			innerProp := innerCall.Expression.AsPropertyAccessExpression()
			if t.isGoAnyExpression(innerProp.Expression) || t.rightSideProducesGoAny(innerProp.Expression) {
				arrayIsGoAny = true
			}
		}
	}

	// Get the array's element type from the checker for callback parameter typing.
	// Use GoTypeInfo.ElemType which correctly reflects the Go-level slice element type.
	arrayTypeInfo := t.getGoType(prop.Expression)
	arrayElemGoType := arrayTypeInfo.ElemType
	if arrayIsGoAny {
		arrayElemGoType = "any"
	}
	if arrayElemGoType == "" && arrayTypeInfo.IsSlice() {
		// Fallback: parse from GoStr
		if et, ok := sliceElemType(arrayTypeInfo.GoStr); ok {
			arrayElemGoType = et
		}
	}
	// For checker-provided element type via GetTypeArguments (skip if Go-level any)
	if (arrayElemGoType == "" || arrayElemGoType == "any") && !arrayIsGoAny {
		if t.ck != nil {
			arrType := t.ck.GetTypeAtLocation(prop.Expression)
			if arrType != nil && arrType.Flags()&checker.TypeFlagsObject != 0 &&
				arrType.ObjectFlags()&checker.ObjectFlagsReference != 0 {
				typeArgs := t.ck.GetTypeArguments(arrType)
				if len(typeArgs) > 0 {
					elemGoType := t.tm.goType(typeArgs[0])
					if elemGoType != "" && elemGoType != "any" {
						arrayElemGoType = elemGoType
					}
				}
			}
		}
	}
	// Fallback: check if the array expression is a known JS global that returns a typed array
	// e.g., Object.entries() → [][]any, Object.keys() → []string
	if arrayElemGoType == "" || arrayElemGoType == "any" {
		if prop.Expression.Kind == ast.KindCallExpression {
			innerCall := prop.Expression.AsCallExpression()
			if innerCall.Expression.Kind == ast.KindPropertyAccessExpression {
				innerProp := innerCall.Expression.AsPropertyAccessExpression()
				if innerProp.Expression.Kind == ast.KindIdentifier {
					objName := innerProp.Expression.AsIdentifier().Text
					methodName := nodeText(innerProp.Name())
					switch objName {
					case "Object":
						switch methodName {
						case "entries":
							arrayElemGoType = "[]any" // Object.entries returns [key, value][] → [][]any
						case "keys":
							arrayElemGoType = "string" // Object.keys returns string[]
						case "values":
							arrayElemGoType = "any" // Object.values returns any[]
						}
					}
				}
			}
		}
	}

	// Only capture for mutating methods that need &obj
	needsCapture := method == "push" || method == "pop" || method == "shift" || method == "unshift" || method == "splice"
	var obj string
	if needsCapture {
		obj = t.captureExpr(prop.Expression)
	}

	args := call.Arguments
	switch method {
	case "push":
		// Check if obj is a map index expression (not addressable in Go).
		// Unwrap parenthesized and type assertion expressions.
		innerExpr := prop.Expression
		for innerExpr.Kind == ast.KindParenthesizedExpression {
			innerExpr = innerExpr.AsParenthesizedExpression().Expression
		}
		if innerExpr.Kind == ast.KindAsExpression {
			innerExpr = innerExpr.AsAsExpression().Expression
		}
		if innerExpr.Kind == ast.KindElementAccessExpression {
			ea := innerExpr.AsElementAccessExpression()
			mapObj := t.captureExpr(ea.Expression)
			key := t.captureExpr(ea.ArgumentExpression)
			t.w.writef("func() { __tmp := %s[%s].([]any); ", mapObj, key)
			t.w.write("jsarray.Push(&__tmp, ")
			t.emitCallArgs(args)
			t.w.writef("); %s[%s] = __tmp }()", mapObj, key)
		} else {
			t.w.writef("jsarray.Push(&%s, ", obj)
			// Wrap typed function arguments with any() for []any Push
			if arrayElemGoType == "any" && args != nil && len(args.Nodes) > 0 {
				for i, arg := range args.Nodes {
					if i > 0 {
						t.w.write(", ")
					}
					if arg.Kind == ast.KindSpreadElement {
						spread := arg.AsSpreadElement()
						t.emitExpr(spread.Expression)
						t.w.write("...")
					} else {
						argType := t.getGoType(arg)
						needsAnyWrap := argType.Category == GoTypeFunc || argType.IsPromise() ||
							arg.Kind == ast.KindArrowFunction || arg.Kind == ast.KindFunctionExpression ||
							argType.IsSlice() || arg.Kind == ast.KindArrayLiteralExpression
						if needsAnyWrap {
							t.w.write("any(")
							t.emitExpr(arg)
							t.w.write(")")
						} else {
							t.emitExpr(arg)
						}
					}
				}
			} else {
				t.emitCallArgs(args)
			}
			t.w.write(")")
		}

	case "pop":
		t.w.writef("jsarray.Pop(&%s)", obj)

	case "shift":
		t.w.writef("jsarray.Shift(&%s)", obj)

	case "unshift":
		t.w.writef("jsarray.Unshift(&%s, ", obj)
		t.emitCallArgs(args)
		t.w.write(")")

	case "map":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.Map(")
		t.emitExpr(prop.Expression)
		// Only add .([]any) for identifiers, not for chained calls that already return []any
		if arrayIsGoAny && prop.Expression.Kind != ast.KindCallExpression {
			t.w.write(".([]any)")
		}
		t.w.write(", ")
		// Force any elem type for chained calls from goAny arrays
		mapElemType := arrayElemGoType
		if arrayIsGoAny {
			mapElemType = "any"
		}
		t.emitArrayCallback(args, 2, mapElemType)
		t.w.write(")")

	case "filter":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.Filter(")
		t.emitExpr(prop.Expression)
		if arrayIsGoAny {
			t.w.write(".([]any)")
		}
		t.w.write(", ")
		t.emitArrayCallback(args, 2, arrayElemGoType)
		t.w.write(")")

	case "forEach":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.ForEach(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		// ForEach callback must not return a value — emit void wrapper
		hasDestructuringParam := false
		if args != nil && len(args.Nodes) > 0 {
			cb := args.Nodes[0]
			if cb.Kind == ast.KindArrowFunction || cb.Kind == ast.KindFunctionExpression {
				params := cb.Parameters()
				if params != nil && len(params) > 0 && params[0].Name() != nil &&
					(params[0].Name().Kind == ast.KindArrayBindingPattern || params[0].Name().Kind == ast.KindObjectBindingPattern) {
					hasDestructuringParam = true
				}
			}
		}
		if args != nil && len(args.Nodes) > 0 && (arrayElemGoType == "any" || hasDestructuringParam) {
			cb := args.Nodes[0]
			if cb.Kind == ast.KindArrowFunction || cb.Kind == ast.KindFunctionExpression {
				params := cb.Parameters()
				paramName := "__v"
				hasDestructuring := false
				if params != nil && len(params) > 0 && params[0].Name() != nil {
					pn := params[0].Name()
					if pn.Kind == ast.KindIdentifier {
						paramName = goVarName(pn.AsIdentifier().Text)
					} else if pn.Kind == ast.KindArrayBindingPattern || pn.Kind == ast.KindObjectBindingPattern {
						hasDestructuring = true
					}
				}
				elemType := arrayElemGoType
				if elemType == "" {
					elemType = "any"
				}
				t.w.writef("func(%s %s, _ int) { ", paramName, elemType)
				// Track callback param as Go-level any
				if t.goAnyVars == nil {
					t.goAnyVars = make(map[string]bool)
				}
				t.goAnyVars[paramName] = true
				// Emit destructuring from param if needed
				if hasDestructuring && params[0].Name() != nil {
					t.emitDestructureFromParam(params[0].Name(), paramName)
				}
				// Emit body — for arrow functions with expression body, just emit the expression
				var body *ast.Node
				if cb.Kind == ast.KindArrowFunction {
					body = cb.AsArrowFunction().Body
				} else {
					body = cb.AsFunctionExpression().Body
				}
				if body != nil && body.Kind != ast.KindBlock {
					// Expression body — emit as statement (discard return value)
					t.emitExpr(body)
				} else if body != nil {
					// Block body — emit statements only (wrapper already provides braces)
					block := body.AsBlock()
					if block.Statements != nil {
						for _, s := range block.Statements.Nodes {
							t.emitStatement(s)
						}
					}
				}
				t.w.write(" }")
			} else {
				t.emitArrayCallback(args, 2, arrayElemGoType)
			}
		} else {
			t.emitArrayCallback(args, 2, arrayElemGoType)
		}
		t.w.write(")")

	case "reduce":
		t.w.write("jsarray.Reduce(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitCallArgs(args)
		t.w.write(")")

	case "find":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.Find(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitArrayCallback(args, 2, arrayElemGoType)
		t.w.write(")")

	case "findIndex":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.FindIndex(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitArrayCallback(args, 2, arrayElemGoType)
		t.w.write(")")

	case "some":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.Some(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitArrayCallback(args, 2, arrayElemGoType)
		t.w.write(")")

	case "every":
		if t.emitArrayJSFuncCallbackMethod(call, prop, method, arrayElemGoType) {
			return
		}
		t.w.write("jsarray.Every(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitArrayCallback(args, 2, arrayElemGoType)
		t.w.write(")")

	case "includes":
		t.w.write("jsarray.Includes(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitCallArgs(args)
		t.w.write(")")

	case "indexOf":
		t.w.write("jsarray.IndexOf(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitCallArgs(args)
		t.w.write(")")

	case "lastIndexOf":
		t.w.write("jsarray.LastIndexOf(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitCallArgs(args)
		t.w.write(")")

	case "reverse":
		t.w.write("jsarray.Reverse(")
		t.emitExpr(prop.Expression)
		t.w.write(")")

	case "flat":
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.write("jsrt.Flat(")
		t.emitExpr(prop.Expression)
		t.w.write(")")

	case "slice":
		nArgs := 0
		if args != nil {
			nArgs = len(args.Nodes)
		}
		objStr := t.captureExpr(prop.Expression)
		if nArgs <= 1 {
			t.w.writef("jsarray.Slice(%s, ", objStr)
			if nArgs == 1 {
				t.emitExpr(call.Arguments.Nodes[0])
			} else {
				t.w.write("0")
			}
			t.w.writef(", len(%s))", objStr)
		} else {
			t.w.writef("jsarray.Slice(%s, ", objStr)
			t.emitCallArgs(args)
			t.w.write(")")
		}

	case "splice":
		t.w.writef("jsarray.Splice(&%s, ", obj)
		t.emitCallArgs(args)
		t.w.write(")")

	case "concat":
		t.w.write("jsarray.Concat(")
		t.emitExpr(prop.Expression)
		t.w.write(", ")
		t.emitCallArgs(args)
		t.w.write(")")

	case "join":
		t.w.addImport("fmt", "")
		arrCode := t.captureExpr(prop.Expression)
		t.w.writef("jsarray.Join(%s, ", arrCode)
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.emitExpr(call.Arguments.Nodes[0])
		} else {
			t.w.write("\",\"")
		}
		// Determine element type: check generated Go code, then fall back to checker
		elemType := "any"
		if strings.HasPrefix(arrCode, "func() []") {
			rest := arrCode[len("func() []"):]
			if j := strings.IndexAny(rest, " {"); j > 0 {
				elemType = rest[:j]
			}
		}
		if elemType == "any" {
			arrType := t.getGoType(prop.Expression)
			if arrType.IsSlice() && arrType.ElemType != "" {
				elemType = arrType.ElemType
			}
		}
		t.w.writef(", func(v %s) string { return fmt.Sprint(v) })", elemType)

	default:
		// Fallback
		t.emitExpr(call.Expression)
		t.w.write("(")
		t.emitCallArgs(args)
		t.w.write(")")
	}
}

// emitStringMethodCall generates Go code for string method calls.
func (t *Transpiler) emitStringMethodCall(call *ast.CallExpression) {
	prop := call.Expression.AsPropertyAccessExpression()
	method := nodeText(prop.Name())
	obj := t.captureExpr(prop.Expression)
	if t.stringMethodObjOverride != "" {
		obj = t.stringMethodObjOverride
		t.stringMethodObjOverride = ""
	} else if prop.Expression.Kind == ast.KindIdentifier {
		// Dereference *string params for string method calls
		vn := goVarName(prop.Expression.AsIdentifier().Text)
		if t.goPtrStringVars != nil && t.goPtrStringVars[vn] {
			obj = "*" + obj
		}
	}

	t.w.addImport("strings", "")

	switch method {
	case "split":
		t.w.writef("strings.Split(%s, ", obj)
		t.emitCallArgs(call.Arguments)
		t.w.write(")")

	case "includes":
		t.w.writef("strings.Contains(%s, ", obj)
		t.emitCallArgs(call.Arguments)
		t.w.write(")")

	case "startsWith":
		if call.Arguments != nil && len(call.Arguments.Nodes) >= 2 {
			// str.startsWith(search, position) → strings.HasPrefix(str[position:], search)
			t.w.write("strings.HasPrefix(")
			t.w.writef("%s[", obj)
			t.emitIntConversion(call.Arguments.Nodes[1])
			t.w.write(":], ")
			t.emitExprDerefPtrString(call.Arguments.Nodes[0])
			t.w.write(")")
		} else {
			t.w.writef("strings.HasPrefix(%s, ", obj)
			if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
				t.emitExprDerefPtrString(call.Arguments.Nodes[0])
			}
			t.w.write(")")
		}

	case "endsWith":
		t.w.writef("strings.HasSuffix(%s, ", obj)
		t.emitCallArgs(call.Arguments)
		t.w.write(")")

	case "indexOf":
		if call.Arguments != nil && len(call.Arguments.Nodes) >= 2 {
			// str.indexOf(substr, startPos) → func(s, sub string, start int) int { i := strings.Index(s[start:], sub); if i == -1 { return -1 }; return i + start }(str, substr, startPos)
			t.w.writef("func(s, sub string, start int) int { i := strings.Index(s[start:], sub); if i == -1 { return -1 }; return i + start }(%s, ", obj)
			t.emitExpr(call.Arguments.Nodes[0])
			t.w.write(", ")
			t.emitIntConversion(call.Arguments.Nodes[1])
			t.w.write(")")
		} else {
			t.w.writef("strings.Index(%s, ", obj)
			t.emitCallArgsDeref(call.Arguments)
			t.w.write(")")
		}

	case "lastIndexOf":
		t.w.writef("strings.LastIndex(%s, ", obj)
		t.emitCallArgsDeref(call.Arguments)
		t.w.write(")")

	case "trim":
		t.w.writef("strings.TrimSpace(%s)", obj)

	case "trimStart":
		t.w.writef("strings.TrimLeft(%s, \" \\t\\n\\r\")", obj)

	case "trimEnd":
		t.w.writef("strings.TrimRight(%s, \" \\t\\n\\r\")", obj)

	case "toUpperCase":
		t.w.writef("strings.ToUpper(%s)", obj)

	case "toLowerCase":
		t.w.writef("strings.ToLower(%s)", obj)

	case "replace":
		// Check if second arg is a function (callback replace)
		if call.Arguments != nil && len(call.Arguments.Nodes) >= 2 {
			arg2 := call.Arguments.Nodes[1]
			if arg2.Kind == ast.KindArrowFunction || arg2.Kind == ast.KindFunctionExpression {
				// str.replace(regex, fn) → regex.ReplaceAllStringFunc(str, fn)
				// Go's ReplaceAllStringFunc only passes match string (1 param), not index
				t.emitExpr(call.Arguments.Nodes[0])
				t.w.writef(".ReplaceAllStringFunc(%s, ", obj)
				params := arg2.Parameters()
				if len(params) > 1 {
					// Emit 1-param wrapper, declaring dropped params as zero values
					paramName := "match"
					if params[0].Name() != nil && params[0].Name().Kind == ast.KindIdentifier {
						paramName = goVarName(params[0].Name().AsIdentifier().Text)
					}
					var body *ast.Node
					if arg2.Kind == ast.KindArrowFunction {
						body = arg2.AsArrowFunction().Body
					} else {
						body = arg2.Body()
					}
					t.w.writef("func(%s string) string { ", paramName)
					// Declare dropped params as zero values so body can reference them
					for k := 1; k < len(params); k++ {
						if params[k].Name() != nil && params[k].Name().Kind == ast.KindIdentifier {
							pn := goVarName(params[k].Name().AsIdentifier().Text)
							t.w.writef("var %s any; _ = %s; ", pn, pn)
						}
					}
					if body != nil && body.Kind == ast.KindBlock {
						block := body.AsBlock()
						if block.Statements != nil {
							for _, stmt := range block.Statements.Nodes {
								t.emitStatement(stmt)
							}
						}
					} else if body != nil {
						t.w.write("return ")
						t.emitExpr(body)
					}
					t.w.write(" }")
				} else {
					t.emitExpr(arg2)
				}
				t.w.write(")")
				return
			}
		}
		// Check if first arg is a regex literal → use regex methods
		if call.Arguments != nil && len(call.Arguments.Nodes) >= 2 &&
			call.Arguments.Nodes[0].Kind == ast.KindRegularExpressionLiteral {
			// regex.replace(str, replacement) → regex.ReplaceAllLiteralString(str, replacement)
			t.emitExpr(call.Arguments.Nodes[0])
			t.w.writef(".ReplaceAllLiteralString(%s, ", obj)
			t.emitExpr(call.Arguments.Nodes[1])
			t.w.write(")")
		} else {
			t.w.writef("strings.Replace(%s, ", obj)
			if call.Arguments != nil {
				for i, arg := range call.Arguments.Nodes {
					if i > 0 {
						t.w.write(", ")
					}
					code := t.captureExpr(arg)
					t.w.write(code)
					if goCodeProducesAny(code) || t.rightSideProducesGoAny(arg) {
						t.w.write(".(string)")
					}
				}
			}
			t.w.write(", 1)")
		}

	case "replaceAll":
		t.w.writef("strings.ReplaceAll(%s, ", obj)
		t.emitCallArgs(call.Arguments)
		t.w.write(")")

	case "repeat":
		t.w.writef("strings.Repeat(%s, ", obj)
		// repeat takes a count — need int conversion
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.w.write("int(")
			t.emitExpr(call.Arguments.Nodes[0])
			t.w.write(")")
		}
		t.w.write(")")

	case "slice", "substring":
		// Use a helper to handle negative indices safely
		nArgs := 0
		if call.Arguments != nil {
			nArgs = len(call.Arguments.Nodes)
		}
		if nArgs == 0 {
			t.w.write(obj)
		} else if nArgs == 1 {
			t.w.writef("func(s string, start int) string { if start < 0 { start = len(s) + start }; if start < 0 { start = 0 }; return s[start:] }(%s, ", obj)
			t.emitIntConversion(call.Arguments.Nodes[0])
			t.w.write(")")
		} else {
			t.w.writef("func(s string, start, end int) string { if start < 0 { start = len(s) + start }; if end < 0 { end = len(s) + end }; if start < 0 { start = 0 }; if end > len(s) { end = len(s) }; return s[start:end] }(%s, ", obj)
			t.emitIntConversion(call.Arguments.Nodes[0])
			t.w.write(", ")
			t.emitIntConversion(call.Arguments.Nodes[1])
			t.w.write(")")
		}

	case "padStart", "padEnd":
		// Emit as inline helper since fmt.Sprintf width requires a runtime format string
		t.w.writef("func(s string, targetLen int, pad string) string { for len(s) < targetLen { ")
		if method == "padStart" {
			t.w.write("s = pad + s")
		} else {
			t.w.write("s = s + pad")
		}
		t.w.writef(" }; return s[:targetLen] }(%s, int(", obj)
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.emitExpr(call.Arguments.Nodes[0])
		} else {
			t.w.write("0")
		}
		t.w.write("), ")
		if call.Arguments != nil && len(call.Arguments.Nodes) > 1 {
			t.emitExpr(call.Arguments.Nodes[1])
		} else {
			t.w.write("\" \"")
		}
		t.w.write(")")

	case "toString":
		// str.toString() → fmt.Sprint(str) — works for any-typed values too
		t.w.addImport("fmt", "")
		t.w.writef("fmt.Sprint(%s)", obj)

	case "match":
		// str.match(re) → re.FindStringSubmatch(str)
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.emitExpr(call.Arguments.Nodes[0])
			t.w.writef(".FindStringSubmatch(%s)", obj)
		}

	case "search":
		// str.search(regexp) → index of first match or -1
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			arg := t.captureExpr(call.Arguments.Nodes[0])
			t.w.writef("func() int { loc := %s.FindStringIndex(%s); if loc == nil { return -1 }; return loc[0] }()", arg, obj)
		}

	case "charCodeAt":
		// str.charCodeAt(i) → int(str[i]) — returns byte value as int (for bitwise ops compatibility)
		t.w.writef("int(%s[", obj)
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.emitIntConversion(call.Arguments.Nodes[0])
		} else {
			t.w.write("0")
		}
		t.w.write("])")

	case "charAt":
		// str.charAt(i) → string(str[i])
		t.w.writef("string(%s[", obj)
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.emitIntConversion(call.Arguments.Nodes[0])
		} else {
			t.w.write("0")
		}
		t.w.write("])")

	case "at":
		// str.at(i) → *string (nil for out of bounds, matches TS string|undefined)
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.writef("func(s string, i int) *string { if i < 0 { i = len(s) + i }; if i < 0 || i >= len(s) { return nil }; return jsrt.Ptr(string(s[i])) }(%s, ", obj)
		if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
			t.emitIntConversion(call.Arguments.Nodes[0])
		} else {
			t.w.write("0")
		}
		t.w.write(")")

	default:
		t.emitExpr(call.Expression)
		t.w.write("(")
		t.emitCallArgs(call.Arguments)
		t.w.write(")")
	}
}

// emitArrayCallback emits a callback argument for array methods like map/filter/find.
// elemGoType is the Go type of the array's elements (from checker), used for the callback's first param.
func (t *Transpiler) emitArrayCallback(args *ast.NodeList, expectedParams int, elemGoType string) {
	if args == nil || len(args.Nodes) == 0 {
		return
	}
	// Set inCallArg so function literals use closeBlockInline
	savedInCallArg := t.inCallArg
	t.inCallArg = true
	defer func() { t.inCallArg = savedInCallArg }()
	cb := args.Nodes[0]
	if cb.Kind == ast.KindArrowFunction || cb.Kind == ast.KindFunctionExpression {
		params := cb.Parameters()
		// Handle destructuring params: ([[, route]]) => route
		if params != nil && len(params) > 0 && params[0].Name() != nil &&
			params[0].Name().Kind == ast.KindArrayBindingPattern {
			retType := t.getFuncReturnType(cb)
			if retType == "" {
				retType = "any"
			}
			t.w.writef("func(__item %s, _ int) %s { ", elemGoType, retType)
			// Emit destructuring from __item
			t.emitDestructureFromParam(params[0].Name(), "__item")
			// Emit body
			var body *ast.Node
			if cb.Kind == ast.KindArrowFunction {
				body = cb.AsArrowFunction().Body
			} else {
				body = cb.AsFunctionExpression().Body
			}
			savedRetType := t.currentRetType
			t.currentRetType = retType
			if body != nil && body.Kind != ast.KindBlock {
				t.w.write("return ")
				t.emitExpr(body)
			} else if body != nil {
				block := body.AsBlock()
				if block.Statements != nil {
					for _, stmt := range block.Statements.Nodes {
						t.emitStatement(stmt)
					}
				}
			}
			t.currentRetType = savedRetType
			t.w.write(" }")
			return
		}
		if params != nil && len(params) < expectedParams {
			saved := t.arrayCallbackElemType
			t.arrayCallbackElemType = elemGoType
			t.emitCallbackWithExtraParams(cb, expectedParams-len(params))
			t.arrayCallbackElemType = saved
			return
		}
		// Callback has more params than Go expects → emit wrapper dropping extra params
		if params != nil && len(params) > expectedParams {
			t.emitArrayCallbackDropExtraParams(cb, expectedParams, elemGoType)
			return
		}
	}
	// Set array callback context for emitParameterList:
	// - arrayCallbackIdx=1 maps index param to int
	// - arrayCallbackElemType overrides the first param type
	savedIdx := t.arrayCallbackIdx
	savedElem := t.arrayCallbackElemType
	t.arrayCallbackIdx = 1
	t.arrayCallbackElemType = elemGoType
	t.emitExpr(cb)
	t.arrayCallbackIdx = savedIdx
	t.arrayCallbackElemType = savedElem
}

// emitCallbackWithExtraParams emits an arrow/function expression with additional `_ int` parameters.
func (t *Transpiler) emitCallbackWithExtraParams(node *ast.Node, extraCount int) {
	// Emit signature: func(params..., _ int, ...) retType
	t.w.write("func(")
	saved := t.arrayCallbackIdx
	t.arrayCallbackIdx = 1
	t.emitParameterList(node)
	t.arrayCallbackIdx = saved
	for range extraCount {
		t.w.write(", _ int")
	}
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	if retType != "" {
		t.w.writef(" %s", retType)
	}

	// Save/restore currentRetType like emitArrowFunction does
	savedRetType := t.currentRetType
	t.currentRetType = retType
	defer func() { t.currentRetType = savedRetType }()

	// Emit body
	var body *ast.Node
	if node.Kind == ast.KindArrowFunction {
		body = node.AsArrowFunction().Body
	} else {
		body = node.Body()
	}

	if body == nil {
		t.w.write(" {}")
		return
	}

	if body.Kind == ast.KindBlock {
		block := body.AsBlock()
		t.w.openBlock()
		t.emitParamDestructuring(node)
		if block.Statements != nil {
			for _, stmt := range block.Statements.Nodes {
				t.emitStatement(stmt)
			}
		}
		t.w.closeBlock()
	} else {
		// Expression body (arrow only): void → no return, non-void → return
		if retType == "" {
			t.w.write(" { ")
			t.emitExpr(body)
			t.w.write(" }")
		} else {
			t.w.write(" { return ")
			if retType != "" && retType != "any" {
				code := t.captureExpr(body)
				t.w.write(code)
				if goCodeProducesAny(code) {
					t.writeTypeAssertion(retType)
				}
			} else {
				t.emitExpr(body)
			}
			t.w.write(" }")
		}
	}
}

// emitArrayCallbackDropExtraParams emits a wrapper callback that drops extra params.
// e.g., (v, i, a) => ... becomes func(v T, i int) bool { a := arr; return ... }
func (t *Transpiler) emitArrayCallbackDropExtraParams(node *ast.Node, expectedParams int, elemGoType string) {
	params := node.Parameters()
	retType := t.getFuncReturnType(node)

	// Emit wrapper signature with only the expected params
	t.w.write("func(")
	savedIdx := t.arrayCallbackIdx
	savedElem := t.arrayCallbackElemType
	t.arrayCallbackIdx = 1
	t.arrayCallbackElemType = elemGoType
	for i := 0; i < expectedParams && i < len(params); i++ {
		if i > 0 {
			t.w.write(", ")
		}
		name := params[i].Name()
		if name != nil && name.Kind == ast.KindIdentifier {
			t.w.write(goVarName(name.AsIdentifier().Text))
		} else {
			t.w.writef("p%d", i)
		}
		if i == 0 && elemGoType != "" {
			t.w.writef(" %s", elemGoType)
		} else if i == 1 {
			t.w.write(" int")
		} else {
			t.w.write(" any")
		}
	}
	t.w.write(")")
	t.arrayCallbackIdx = savedIdx
	t.arrayCallbackElemType = savedElem

	if retType != "" {
		t.w.writef(" %s", retType)
	}

	savedRetType := t.currentRetType
	t.currentRetType = retType
	defer func() { t.currentRetType = savedRetType }()

	var body *ast.Node
	if node.Kind == ast.KindArrowFunction {
		body = node.AsArrowFunction().Body
	} else {
		body = node.Body()
	}
	if body == nil {
		t.w.write(" {}")
		return
	}

	if body.Kind == ast.KindBlock {
		block := body.AsBlock()
		t.w.openBlock()
		// Declare dropped params with checker types
		for i := expectedParams; i < len(params); i++ {
			pname := params[i].Name()
			if pname != nil && pname.Kind == ast.KindIdentifier {
				pn := goVarName(pname.AsIdentifier().Text)
				paramGoType := "any"
				if t.ck != nil {
					pt := t.ck.GetTypeAtLocation(params[i])
					if pt != nil {
						gt := t.tm.goType(pt)
						if gt != "" && gt != "any" {
							paramGoType = gt
						}
					}
				}
				t.w.writef("var %s %s; _ = %s", pn, paramGoType, pn)
				t.w.newline()
			}
		}
		if block.Statements != nil {
			for _, stmt := range block.Statements.Nodes {
				t.emitStatement(stmt)
			}
		}
		t.w.closeBlock()
	} else {
		// Expression body — also declare dropped params
		t.w.write(" { ")
		for i := expectedParams; i < len(params); i++ {
			pname := params[i].Name()
			if pname != nil && pname.Kind == ast.KindIdentifier {
				pn := goVarName(pname.AsIdentifier().Text)
				paramGoType := "any"
				if t.ck != nil {
					pt := t.ck.GetTypeAtLocation(params[i])
					if pt != nil {
						gt := t.tm.goType(pt)
						if gt != "" && gt != "any" {
							paramGoType = gt
						}
					}
				}
				t.w.writef("var %s %s; _ = %s; ", pn, paramGoType, pn)
			}
		}
		if retType == "" {
			t.emitExpr(body)
		} else {
			t.w.write("return ")
			t.emitExpr(body)
		}
		t.w.write(" }")
	}
}

// emitCallArgsWithCoercion emits arguments for a general function call,
// inserting float64() conversions for int-typed arguments when the function
// expects float64 parameters.
func (t *Transpiler) emitCallArgsWithCoercion(call *ast.CallExpression) {
	if call.Arguments == nil {
		return
	}

	// Try to get parameter types from the call signature
	var paramTypes []string
	// For self-recursive calls, use the implementation's param types
	if t.currentFuncName != "" && call.Expression.Kind == ast.KindIdentifier {
		callName := goVarName(call.Expression.AsIdentifier().Text)
		if t.samePackageExports != nil && t.samePackageExports[call.Expression.AsIdentifier().Text] {
			callName = goExportedName(call.Expression.AsIdentifier().Text)
		}
		if callName == t.currentFuncName && t.currentFuncParamTypes != nil {
			paramTypes = t.currentFuncParamTypes
		}
	}
	if paramTypes == nil && t.ck != nil {
		funcType := t.ck.GetTypeAtLocation(call.Expression)
		if funcType != nil {
			sigs := t.ck.GetSignaturesOfType(funcType, checker.SignatureKindCall)
			if len(sigs) > 0 {
				params := sigs[0].Parameters()
				for _, p := range params {
					pt := t.ck.GetTypeOfSymbol(p)
					paramTypes = append(paramTypes, t.tm.goType(pt))
				}
			}
		}
	}

	// Set inCallArg so function literals use closeBlockInline (no trailing newline)
	savedInCallArg := t.inCallArg
	t.inCallArg = true
	defer func() { t.inCallArg = savedInCallArg }()

	for i, arg := range call.Arguments.Nodes {
		if i > 0 {
			t.w.write(", ")
		}

		needsFloat64 := false
		if i < len(paramTypes) && paramTypes[i] == "float64" {
			needsFloat64 = t.isIntExpr(arg)
		}

		// Check if param is pointer type and arg is non-pointer → wrap with jsrt.Ptr()
		needsPtr := false
		if i < len(paramTypes) && len(paramTypes[i]) > 0 && paramTypes[i][0] == '*' {
			// Don't wrap null/undefined — just pass nil directly
			if arg.Kind != ast.KindNullKeyword && arg.Kind != ast.KindUndefinedKeyword {
				needsPtr = !t.getDeclaredGoType(arg).IsPointer()
			}
		}

		// Check if arg is *string but param expects string → dereference
		// Only for tracked *string params (not checker-inferred, which may differ from Go type)
		needsDerefPtr := false
		if i < len(paramTypes) && paramTypes[i] == "string" && arg.Kind == ast.KindIdentifier {
			varName := goVarName(arg.AsIdentifier().Text)
			if t.goPtrStringVars != nil && t.goPtrStringVars[varName] {
				needsDerefPtr = true
			}
		}

		// Check if arg is any-typed but param expects a concrete type → type assertion
		needsTypeAssert := false
		assertType := ""
		if i < len(paramTypes) {
			pt := paramTypes[i]
			if pt == "string" || pt == "int" || pt == "bool" || pt == "float64" ||
				strings.HasPrefix(pt, "[]") || strings.HasPrefix(pt, "map[") {
				argDeclType := t.getDeclaredGoType(arg)
				isGoAny := argDeclType.IsAny()
				if !isGoAny && arg.Kind == ast.KindIdentifier {
					varName := goVarName(arg.AsIdentifier().Text)
					if t.goAnyVars != nil && t.goAnyVars[varName] {
						isGoAny = true
					}
				}
				if isGoAny {
					needsTypeAssert = true
					assertType = pt
				}
			}
		}

		// Check if param is []any but arg is []T (concrete) → convert with jsrt.ToAnySlice
		needsSliceConvert := false
		if i < len(paramTypes) && paramTypes[i] == "[]any" {
			argType := t.getDeclaredGoType(arg)
			if argType.IsSlice() && argType.ElemType != "any" && argType.ElemType != "" {
				needsSliceConvert = true
			}
		}

		if arg.Kind == ast.KindSpreadElement {
			spread := arg.AsSpreadElement()
			// When spread needs to fill remaining named params before the variadic
			remainingNamed := 0
			if len(paramTypes) > i+1 {
				remainingNamed = len(paramTypes) - i - 1
			}
			if remainingNamed > 0 {
				spreadVar := t.captureExpr(spread.Expression)
				for j := 0; j <= remainingNamed; j++ {
					if j > 0 {
						t.w.write(", ")
					}
					paramIdx := i + j
					if paramIdx >= len(paramTypes)-1 {
						t.w.writef("%s[%d:]...", spreadVar, j)
					} else {
						pt := paramTypes[paramIdx]
						if len(pt) > 0 && pt[0] == '*' {
							t.w.addImport("github.com/i2y/ramune/jsrt", "")
							t.w.writef("jsrt.Ptr(%s[%d])", spreadVar, j)
						} else {
							t.w.writef("%s[%d]", spreadVar, j)
						}
					}
				}
			} else {
				t.emitExpr(spread.Expression)
				t.w.write("...")
			}
		} else if needsPtr {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("jsrt.Ptr(")
			t.emitExpr(arg)
			t.w.write(")")
		} else if needsDerefPtr {
			t.w.write("*")
			t.emitExpr(arg)
		} else if needsFloat64 {
			t.w.write("float64(")
			t.emitExpr(arg)
			t.w.write(")")
		} else if needsTypeAssert {
			t.emitExpr(arg)
			t.writeTypeAssertionChecked(assertType, arg)
		} else if needsSliceConvert {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.write("jsrt.ToAnySlice(")
			t.emitExpr(arg)
			t.w.write(")")
		} else {
			t.emitExpr(arg)
		}
	}

	// Pad missing optional args with nil (only for local function calls, not method calls)
	// Skip when spread args were distributed across named params
	hasSpreadArg := false
	if call.Arguments != nil {
		for _, arg := range call.Arguments.Nodes {
			if arg.Kind == ast.KindSpreadElement {
				hasSpreadArg = true
				break
			}
		}
	}
	isPackageCall := false
	if call.Expression.Kind == ast.KindPropertyAccessExpression {
		prop := call.Expression.AsPropertyAccessExpression()
		if prop.Expression.Kind == ast.KindIdentifier {
			pkgName := prop.Expression.AsIdentifier().Text
			isPackageCall = t.isPackageRef(prop.Expression) ||
				(t.samePackageExports != nil && t.samePackageExports[pkgName])
		}
	}
	// Also pad for concrete method calls on same-package types (not external types like FormData)
	isConcreteMethodCall := false
	if !isPackageCall && call.Expression.Kind == ast.KindPropertyAccessExpression {
		prop := call.Expression.AsPropertyAccessExpression()
		objType := t.getGoType(prop.Expression)
		if !objType.IsAny() && (objType.IsPointer() || isGenericType(objType.GoStr)) {
			// Only pad for types declared in the same package (classNames)
			typeName := objType.Name
			if typeName == "" && isGenericType(objType.GoStr) {
				bracketIdx := strings.Index(objType.GoStr, "[")
				if bracketIdx > 0 {
					typeName = objType.GoStr[:bracketIdx]
				}
			}
			if t.classNames != nil && t.classNames[typeName] {
				isConcreteMethodCall = true
			}
		}
	}
	if (call.Expression.Kind == ast.KindIdentifier || isPackageCall || isConcreteMethodCall) && !hasSpreadArg {
		nArgs := 0
		if call.Arguments != nil {
			nArgs = len(call.Arguments.Nodes)
		}
		for i := nArgs; i < len(paramTypes); i++ {
			if i > 0 {
				t.w.write(", ")
			}
			// For same-package method calls, missing args are optional params
			// which become *T in Go → use nil
			if isConcreteMethodCall {
				t.w.write("nil")
			} else {
				switch paramTypes[i] {
				case "bool":
					t.w.write("false")
				case "int":
					t.w.write("0")
				case "float64":
					t.w.write("0")
				case "string":
					t.w.write(`""`)
				default:
					t.w.write("nil")
				}
			}
		}
	}
}

// isIntExpr checks if an expression will produce an int value in Go.
func (t *Transpiler) isIntExpr(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindNumericLiteral:
		return isIntegerLiteral(node)
	case ast.KindIdentifier:
		if t.intVars != nil {
			return t.intVars[goVarName(node.AsIdentifier().Text)]
		}
	case ast.KindCallExpression:
		return t.exprProducesInt(node)
	case ast.KindPropertyAccessExpression:
		// .length → len() which returns int in Go
		prop := node.AsPropertyAccessExpression()
		if nodeText(prop.Name()) == "length" {
			return true
		}
	case ast.KindBinaryExpression:
		bin := node.AsBinaryExpression()
		op := bin.OperatorToken.Kind
		// Arithmetic operators on two int operands produce int
		if op == ast.KindPlusToken || op == ast.KindMinusToken ||
			op == ast.KindAsteriskToken || op == ast.KindPercentToken {
			return t.isIntExpr(bin.Left) && t.isIntExpr(bin.Right)
		}
	case ast.KindParenthesizedExpression:
		return t.isIntExpr(node.AsParenthesizedExpression().Expression)
	case ast.KindPrefixUnaryExpression:
		prefix := node.AsPrefixUnaryExpression()
		if prefix.Operator == ast.KindMinusToken || prefix.Operator == ast.KindPlusToken {
			return t.isIntExpr(prefix.Operand)
		}
	}
	return false
}

// isFloatExpr checks if an expression will produce a float64 value in Go.
func (t *Transpiler) isFloatExpr(node *ast.Node) bool {
	if node == nil {
		return false
	}
	// Check via type checker
	if t.ck != nil {
		exprType := t.ck.GetTypeAtLocation(node)
		if exprType != nil && exprType.Flags()&checker.TypeFlagsNumberLike != 0 {
			// It's a number type — but is it a Go float64 or int?
			// If it's NOT an int expression, it's float64
			return !t.isIntExpr(node)
		}
	}
	// Float literal (has decimal point)
	if node.Kind == ast.KindNumericLiteral && !isIntegerLiteral(node) {
		return true
	}
	return false
}

// resolveDiscriminatedType checks if typeName is a discriminated union interface,
// and if so, inspects the object literal's properties to find the concrete variant type.
func (t *Transpiler) resolveDiscriminatedType(typeName string, obj *ast.ObjectLiteralExpression) string {
	if t.ck == nil || obj.Properties == nil {
		return typeName
	}

	// Find discriminant field values in the object literal (e.g., kind: "circle")
	discriminantFields := discriminantFieldNames
	var discriminantValue string
	var discriminantField string
	for _, prop := range obj.Properties.Nodes {
		if prop.Kind != ast.KindPropertyAssignment {
			continue
		}
		pa := prop.AsPropertyAssignment()
		name := prop.Name()
		if name == nil || name.Kind != ast.KindIdentifier {
			continue
		}
		fieldName := name.AsIdentifier().Text
		for _, df := range discriminantFields {
			if fieldName == df && pa.Initializer != nil && pa.Initializer.Kind == ast.KindStringLiteral {
				discriminantField = fieldName
				discriminantValue = pa.Initializer.AsStringLiteral().Text
				break
			}
		}
		if discriminantValue != "" {
			break
		}
	}

	if discriminantValue == "" || discriminantField == "" {
		return typeName
	}

	// Use the checker to find the object literal's actual type — it may resolve
	// to the correct variant directly.
	objType := t.ck.GetTypeAtLocation(obj.AsNode())
	if objType != nil {
		sym := objType.Symbol()
		if sym != nil && sym.Name != "" && !strings.HasPrefix(sym.Name, "__") && isValidGoIdentifier(sym.Name) {
			return goTypeName(sym.Name)
		}
	}

	// Fallback: map discriminant value to PascalCase type name
	return goTypeName(discriminantValue)
}

// emitExprDeref emits an expression, automatically dereferencing if it's a pointer type.
// Used in binary expressions where a value (not pointer) is needed.
func (t *Transpiler) emitExprDeref(node *ast.Node) {
	if t.getDeclaredGoType(node).IsPointer() && !t.isOptionalChainExpr(node) {
		t.w.write("*")
	}
	t.emitExpr(node)
}

// emitExprDerefUnlessNil emits with deref, but skips deref when comparing pointer with nil.
func (t *Transpiler) emitExprDerefUnlessNil(node *ast.Node, otherSide *ast.Node) {
	isNilComparison := otherSide.Kind == ast.KindNullKeyword || otherSide.Kind == ast.KindUndefinedKeyword ||
		(otherSide.Kind == ast.KindIdentifier && otherSide.AsIdentifier().Text == "undefined")
	if isNilComparison {
		t.emitExpr(node)
	} else {
		t.emitExprDeref(node)
	}
}

// emitExprDerefPtrString emits an expression, dereferencing *string params to string.
func (t *Transpiler) emitExprDerefPtrString(node *ast.Node) {
	if node.Kind == ast.KindIdentifier {
		vn := goVarName(node.AsIdentifier().Text)
		if t.goPtrStringVars != nil && t.goPtrStringVars[vn] {
			t.w.write("*")
			t.emitExpr(node)
			return
		}
	}
	t.emitExpr(node)
}

// emitFloatOperand emits a numeric operand in a context that requires Go
// float64 — comparisons and math.Mod over mixed int/float. When isInt, wraps
// with float64(...). Otherwise, if the operand is a Go-any value (declared
// any or JSObject-chain anytype), appends .(float64) to satisfy the consuming
// position. Mirrors the any-unwrap predicate used by the arithmetic branch so
// comparisons and math.Mod are not narrower than `+` / `-` / `*` / `/`.
func (t *Transpiler) emitFloatOperand(node *ast.Node, isInt bool) {
	if isInt {
		t.w.write("float64(")
		t.emitExprDeref(node)
		t.w.write(")")
		return
	}
	if t.rightSideProducesGoAny(node) || t.getDeclaredGoType(node).IsAny() {
		t.emitExprDeref(node)
		t.w.write(".(float64)")
		return
	}
	t.emitExprDeref(node)
}

// isOptionalChainExpr checks if an expression uses optional chaining (?.).
func (t *Transpiler) isOptionalChainExpr(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindElementAccessExpression:
		return node.AsElementAccessExpression().QuestionDotToken != nil
	case ast.KindCallExpression:
		call := node.AsCallExpression()
		if call.QuestionDotToken != nil {
			return true
		}
		if call.Expression.Kind == ast.KindPropertyAccessExpression {
			return call.Expression.AsPropertyAccessExpression().QuestionDotToken != nil
		}
	case ast.KindPropertyAccessExpression:
		return node.AsPropertyAccessExpression().QuestionDotToken != nil
	}
	return false
}

// emitCallArgs emits arguments from a NodeList, handling spread elements.
func (t *Transpiler) emitCallArgs(args *ast.NodeList) {
	if args == nil {
		return
	}
	savedInCallArg := t.inCallArg
	t.inCallArg = true
	defer func() { t.inCallArg = savedInCallArg }()
	for i, arg := range args.Nodes {
		if i > 0 {
			t.w.write(", ")
		}
		if arg.Kind == ast.KindSpreadElement {
			spread := arg.AsSpreadElement()
			t.emitExpr(spread.Expression)
			t.w.write("...")
		} else {
			t.emitExpr(arg)
		}
	}
}

// emitCallArgsDeref emits call args, dereferencing pointer-typed arguments.
// Used for Go stdlib calls (strings.Index, etc.) that expect value types.
func (t *Transpiler) emitCallArgsDeref(args *ast.NodeList) {
	if args == nil {
		return
	}
	for i, arg := range args.Nodes {
		if i > 0 {
			t.w.write(", ")
		}
		declType := t.getDeclaredGoType(arg)
		if strings.HasPrefix(declType.GoStr, "*") {
			t.w.write("*")
		}
		t.emitExpr(arg)
	}
}

// emitRegExpLiteral handles /pattern/flags → regexp.MustCompile("(?flags)pattern").
func (t *Transpiler) emitRegExpLiteral(node *ast.Node) {
	text := node.AsRegularExpressionLiteral().Text
	// Parse /pattern/flags
	if len(text) < 2 || text[0] != '/' {
		t.w.writef("/* invalid regex: %s */nil", text)
		return
	}
	lastSlash := strings.LastIndex(text, "/")
	if lastSlash <= 0 {
		t.w.writef("/* invalid regex: %s */nil", text)
		return
	}
	pattern := text[1:lastSlash]
	flags := text[lastSlash+1:]

	t.w.addImport("regexp", "")

	// Convert JS flags to Go inline flags
	goFlags := ""
	for _, f := range flags {
		switch f {
		case 'i':
			goFlags += "i"
		case 's':
			goFlags += "s"
		case 'm':
			goFlags += "m"
		}
	}

	// Use backtick for raw strings unless pattern contains backtick
	if strings.Contains(pattern, "`") {
		// Escape backslashes and quotes for double-quoted string
		escaped := strings.ReplaceAll(pattern, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		if goFlags != "" {
			t.w.writef("regexp.MustCompile(\"(?%s)%s\")", goFlags, escaped)
		} else {
			t.w.writef("regexp.MustCompile(\"%s\")", escaped)
		}
	} else {
		if goFlags != "" {
			t.w.writef("regexp.MustCompile(`(?%s)%s`)", goFlags, pattern)
		} else {
			t.w.writef("regexp.MustCompile(`%s`)", pattern)
		}
	}
}

// emitArrayJSFuncCallbackMethod lowers an array callback-method call whose
// callback argument is a bare *ramune.JSFunc parameter — `xs.map(cb)` into
// an inline range+Call loop with jsrt.Throw error propagation. Returns
// true when it handled the call, false for the caller to fall back to the
// jsarray.* helper path. The picker's checkArrayCallbackMethodCall gate
// guarantees arg[0] is a JSFunc param with a per-method-valid return shape.
func (t *Transpiler) emitArrayJSFuncCallbackMethod(call *ast.CallExpression, prop *ast.PropertyAccessExpression, method, elemGoType string) bool {
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return false
	}
	cb := call.Arguments.Nodes[0]
	if cb.Kind != ast.KindIdentifier {
		return false
	}
	pn := goVarName(cb.AsIdentifier().Text)
	if !t.isJSFuncParam(pn) {
		return false
	}
	if elemGoType == "" {
		elemGoType = "any"
	}

	t.w.addImport("github.com/i2y/ramune/jsrt", "")
	arrCode := t.captureExpr(prop.Expression)

	switch method {
	case "forEach":
		t.w.writef("func() { for _, __x := range %s { __v, __err := %s.Call(__x); _ = __v; if __err != nil { jsrt.Throw(__err) } } }()", arrCode, pn)
	case "map":
		// Picker rejects void-returning map callbacks, so an empty retType
		// here would be a gate regression — fall back to "any" rather than
		// emitting `[]`.
		retType := t.jsFuncCallbackReturnType(cb)
		if retType == "" {
			retType = "any"
		}
		t.w.writef("func() []%s { var __out []%s; for _, __x := range %s { __v, __err := %s.Call(__x); if __err != nil { jsrt.Throw(__err) }; __out = append(__out, __v.(%s)) }; return __out }()", retType, retType, arrCode, pn, retType)
	case "filter":
		t.w.writef("func() []%s { var __out []%s; for _, __x := range %s { __v, __err := %s.Call(__x); if __err != nil { jsrt.Throw(__err) }; if __v.(bool) { __out = append(__out, __x) } }; return __out }()", elemGoType, elemGoType, arrCode, pn)
	case "some":
		t.w.writef("func() bool { for _, __x := range %s { __v, __err := %s.Call(__x); if __err != nil { jsrt.Throw(__err) }; if __v.(bool) { return true } }; return false }()", arrCode, pn)
	case "every":
		t.w.writef("func() bool { for _, __x := range %s { __v, __err := %s.Call(__x); if __err != nil { jsrt.Throw(__err) }; if !__v.(bool) { return false } }; return true }()", arrCode, pn)
	case "findIndex":
		t.w.writef("func() float64 { for __i, __x := range %s { __v, __err := %s.Call(__x); if __err != nil { jsrt.Throw(__err) }; if __v.(bool) { return float64(__i) } }; return float64(-1) }()", arrCode, pn)
	case "find":
		t.w.writef("func() *%s { for _, __x := range %s { __v, __err := %s.Call(__x); if __err != nil { jsrt.Throw(__err) }; if __v.(bool) { __r := __x; return &__r } }; return nil }()", elemGoType, arrCode, pn)
	default:
		return false
	}
	return true
}

// jsFuncCallbackReturnType queries the checker for the Go type of a *JSFunc
// callback's return. Empty means void. "any" passes the value through
// without assertion. Concrete Go types get `__v.(T)` in the caller.
func (t *Transpiler) jsFuncCallbackReturnType(cb *ast.Node) string {
	if t.ck == nil {
		return ""
	}
	cbType := t.ck.GetTypeAtLocation(cb)
	if cbType == nil {
		return ""
	}
	sigs := t.ck.GetSignaturesOfType(cbType, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return ""
	}
	return t.tm.goReturnType(t.ck.GetReturnTypeOfSignature(sigs[0]))
}

// emitStaticMethodCallIfApplicable lowers `Class.method(args...)` to
// `Class_Method(args...)` when Class is a known extracted class. Returns
// true when handled. The caller's existing dispatch covers everything
// else (Math, Number, instance methods, dynamic fallbacks).
func (t *Transpiler) emitStaticMethodCallIfApplicable(call *ast.CallExpression) bool {
	if call == nil || call.Expression == nil {
		return false
	}
	if call.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindIdentifier {
		return false
	}
	className := pa.Expression.AsIdentifier().Text
	goClassName := goTypeName(className)
	if t.classNames == nil || !t.classNames[goClassName] {
		return false
	}
	if pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return false
	}
	methodName := pa.Name().AsIdentifier().Text
	t.w.writef("%s_%s(", goClassName, goExportedName(methodName))
	t.emitCallArgs(call.Arguments)
	t.w.write(")")
	return true
}

// emitJSFuncCallIfApplicable lowers `cb(args...)` where `cb` is a parameter
// whose emitted Go type is *ramune.JSFunc. Mirrors emitAwaitExpr's IIFE
// shape — callback errors propagate via jsrt.Throw the same way Promise
// rejections do. Void-returning callbacks emit `func() any { ... return
// nil }` so the call works in both expression and statement positions.
func (t *Transpiler) emitJSFuncCallIfApplicable(call *ast.CallExpression) bool {
	if call == nil || call.Expression == nil || call.Expression.Kind != ast.KindIdentifier {
		return false
	}
	pn := goVarName(call.Expression.AsIdentifier().Text)
	if !t.isJSFuncParam(pn) {
		return false
	}
	t.w.addImport("github.com/i2y/ramune/jsrt", "")
	retType := t.jsFuncCallbackReturnType(call.Expression)
	if retType == "" {
		t.w.writef("func() any { __v, __err := %s.Call(", pn)
		t.emitCallArgs(call.Arguments)
		t.w.write("); _ = __v; if __err != nil { jsrt.Throw(__err) }; return nil }()")
		return true
	}
	t.w.writef("func() %s { __v, __err := %s.Call(", retType, pn)
	t.emitCallArgs(call.Arguments)
	t.w.write("); if __err != nil { jsrt.Throw(__err) }; ")
	if retType == "any" {
		t.w.write("return __v }()")
	} else {
		t.w.writef("return __v.(%s) }()", retType)
	}
	return true
}

// emitAwaitExpr handles await expressions: await p → p.Await() value extraction.
// For simplicity, we use a pattern that extracts the value and panics on error.
func (t *Transpiler) emitAwaitExpr(node *ast.Node) {
	awaitExpr := node.AsAwaitExpression()
	// Check if the awaited expression is actually a Promise
	exprType := t.getGoType(awaitExpr.Expression)
	if exprType.IsPromise() {
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		// Use the promise's inner type for the IIFE return type
		retType := "any"
		if exprType.ElemType != "" && exprType.ElemType != "any" {
			retType = exprType.ElemType
		}
		// Promise.all returns *promise.Promise[[]any] — force []any return type
		if isPromiseAllCall(awaitExpr.Expression) {
			retType = "[]any"
		}
		t.w.writef("func() %s { __v, __err := (", retType)
		// If the expression is any at Go level, assert to *promise.Promise[any]
		exprCode := t.captureExpr(awaitExpr.Expression)
		declType := t.getDeclaredGoType(awaitExpr.Expression)
		// Only assert to *Promise[any] when the expression is genuinely any (not already a promise)
		needsAnyPromise := ((declType.IsAny() || goCodeProducesAny(exprCode)) &&
			!strings.HasPrefix(exprCode, "promise."))
		// Also need return assertion (not promise assertion) when awaiting a goAnyVars variable (e.g., .Then() result)
		// The variable is already *Promise[any], so we DON'T wrap it, but we DO assert __v
		awaitGoAnyVar := false
		if awaitExpr.Expression.Kind == ast.KindIdentifier {
			vn := goVarName(awaitExpr.Expression.AsIdentifier().Text)
			if t.goAnyVars != nil && t.goAnyVars[vn] {
				awaitGoAnyVar = true
			}
		}
		if needsAnyPromise {
			t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
			t.w.writef("%s.(*promise.Promise[any])", exprCode)
		} else {
			t.w.write(exprCode)
		}
		// Add type assertion on __v when promise was asserted to Promise[any] but retType is concrete
		retAssert := ""
		if (needsAnyPromise || awaitGoAnyVar) && retType != "any" && retType != "[]any" {
			retAssert = ".(" + retType + ")"
		}
		// Struct return types are erased to any in Promise type params (isPrimitiveOrCollectionType),
		// so the Go promise is Promise[any] even though the checker reports a concrete type
		if retAssert == "" && retType != "any" && retType != "[]any" {
			if !isPrimitiveOrCollectionType(retType) {
				retAssert = ".(" + retType + ")"
			}
		}
		t.w.writef(").Await(); if __err != nil { jsrt.Throw(__err) }; return __v%s }()", retAssert)
	} else {
		// Not a Promise — just emit the expression (await is a no-op for non-Promises)
		t.emitExpr(awaitExpr.Expression)
	}
}

// isPromiseAllCall checks if an expression is Promise.all(...).
func isPromiseAllCall(node *ast.Node) bool {
	if node == nil || node.Kind != ast.KindCallExpression {
		return false
	}
	call := node.AsCallExpression()
	if call.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	prop := call.Expression.AsPropertyAccessExpression()
	method := nodeText(prop.Name())
	if method != "all" && method != "All" {
		return false
	}
	if prop.Expression.Kind == ast.KindIdentifier {
		name := prop.Expression.AsIdentifier().Text
		return name == "Promise"
	}
	return false
}

// captureExpr emits an expression into a fresh writer and returns the result as a string.
func (t *Transpiler) captureExpr(node *ast.Node) string {
	saved := t.w
	t.w = newGoWriter()
	t.emitExpr(node)
	result := t.w.buf.String()
	// Merge imports back to the original writer
	for path, alias := range t.w.imports {
		saved.addImport(path, alias)
	}
	t.w = saved
	return result
}

// captureCallArgs emits call arguments into a fresh writer and returns the result as a string.
func (t *Transpiler) captureCallArgs(call *ast.CallExpression) string {
	saved := t.w
	t.w = newGoWriter()
	t.emitCallArgs(call.Arguments)
	result := t.w.buf.String()
	for path, alias := range t.w.imports {
		saved.addImport(path, alias)
	}
	t.w = saved
	return result
}

// escapePercent escapes % characters for fmt.Sprintf format strings.
func escapePercent(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

// isBooleanExpr checks if an expression is structurally guaranteed to be a Go bool.
// Does NOT trust checker type info because Go variable types may differ from TS types
// (e.g., destructuring from any-typed objects).
func (t *Transpiler) isBooleanExpr(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return true
	case ast.KindBinaryExpression:
		op := node.AsBinaryExpression().OperatorToken.Kind
		switch op {
		case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken,
			ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken,
			ast.KindLessThanToken, ast.KindGreaterThanToken,
			ast.KindLessThanEqualsToken, ast.KindGreaterThanEqualsToken,
			ast.KindInstanceOfKeyword, ast.KindInKeyword:
			return true
		}
	case ast.KindPrefixUnaryExpression:
		if node.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
			return true
		}
	}
	return false
}

// isGoAnyParam checks if an expression will be `any` at Go runtime level.
// Catches cases where:
// isPackageRef checks if a node is an identifier that refers to a package/module reference.
func (t *Transpiler) isPackageRef(node *ast.Node) bool {
	if node.Kind != ast.KindIdentifier {
		return false
	}
	name := node.AsIdentifier().Text
	if t.packageRefs != nil {
		if _, ok := t.packageRefs[name]; ok {
			return true
		}
	}
	if t.importedNames != nil {
		if _, ok := t.importedNames[name]; ok {
			return true
		}
	}
	if t.goNativeImports != nil {
		if _, ok := t.goNativeImports[name]; ok {
			return true
		}
	}
	return false
}

// emitTypeOfExpr handles standalone typeof expressions: typeof x → jsrt.TypeOf(x).
func (t *Transpiler) emitTypeOfExpr(node *ast.Node) {
	expr := node.AsTypeOfExpression().Expression
	t.w.addImport("github.com/i2y/ramune/jsrt", "")
	t.w.write("jsrt.TypeOf(")
	t.emitExpr(expr)
	t.w.write(")")
}

// emitTypeOfComparison detects typeof x === "type" patterns and emits Go type assertions.
// Returns true if the pattern was handled.
func (t *Transpiler) emitTypeOfComparison(bin *ast.BinaryExpression) bool {
	op := bin.OperatorToken.Kind
	if op != ast.KindEqualsEqualsEqualsToken && op != ast.KindExclamationEqualsEqualsToken &&
		op != ast.KindEqualsEqualsToken && op != ast.KindExclamationEqualsToken {
		return false
	}

	var typeofExpr *ast.Node
	var typeStr string

	// typeof x === "string" or "string" === typeof x
	if bin.Left.Kind == ast.KindTypeOfExpression && bin.Right.Kind == ast.KindStringLiteral {
		typeofExpr = bin.Left.AsTypeOfExpression().Expression
		typeStr = bin.Right.AsStringLiteral().Text
	} else if bin.Right.Kind == ast.KindTypeOfExpression && bin.Left.Kind == ast.KindStringLiteral {
		typeofExpr = bin.Right.AsTypeOfExpression().Expression
		typeStr = bin.Left.AsStringLiteral().Text
	} else {
		return false
	}

	negate := op == ast.KindExclamationEqualsEqualsToken || op == ast.KindExclamationEqualsToken

	// Suppress type assertions in property access so typeof gets the raw any from .Unwrap()
	t.suppressTypeAssertion = true
	operand := t.captureExpr(typeofExpr)
	t.suppressTypeAssertion = false

	switch typeStr {
	case "string":
		t.emitTypeCheck(operand, "string", negate)
	case "number":
		t.emitTypeCheckMulti(operand, []string{"float64", "int"}, negate)
	case "boolean":
		t.emitTypeCheck(operand, "bool", negate)
	case "undefined":
		if negate {
			t.w.writef("%s != nil", operand)
		} else {
			t.w.writef("%s == nil", operand)
		}
	case "function":
		t.w.addImport("reflect", "")
		if negate {
			t.w.writef("reflect.TypeOf(%s).Kind() != reflect.Func", operand)
		} else {
			t.w.writef("reflect.TypeOf(%s).Kind() == reflect.Func", operand)
		}
	case "object":
		// object means: not nil, not primitive, not function
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		if negate {
			t.w.writef("jsrt.TypeOf(%s) != \"object\"", operand)
		} else {
			t.w.writef("jsrt.TypeOf(%s) == \"object\"", operand)
		}
	default:
		// Unknown type string — fall back to runtime check
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		if negate {
			t.w.writef("jsrt.TypeOf(%s) != %q", operand, typeStr)
		} else {
			t.w.writef("jsrt.TypeOf(%s) == %q", operand, typeStr)
		}
	}
	return true
}

// emitTypeCheck emits a Go type assertion check: func() bool { _, ok := x.(T); return ok }()
func (t *Transpiler) emitTypeCheck(operand, goType string, negate bool) {
	if negate {
		t.w.writef("func() bool { _, ok := %s.(%s); return !ok }()", operand, goType)
	} else {
		t.w.writef("func() bool { _, ok := %s.(%s); return ok }()", operand, goType)
	}
}

// emitTypeCheckMulti emits a type check against multiple Go types using a type switch.
func (t *Transpiler) emitTypeCheckMulti(operand string, goTypes []string, negate bool) {
	cases := strings.Join(goTypes, ", ")
	if negate {
		t.w.writef("func() bool { switch %s.(type) { case %s: return false; default: return true } }()", operand, cases)
	} else {
		t.w.writef("func() bool { switch %s.(type) { case %s: return true; default: return false } }()", operand, cases)
	}
}

// goCodeProducesAny returns true if the generated Go code produces an any-typed value,
// e.g., jsrt.Index() calls or JSObject .Unwrap() chains or func() any IIFEs.
// emitDestructureFromParam emits variable bindings from an ArrayBindingPattern,
// extracting values from a source variable via indexing.
// e.g., [[, route]] from __item → route := jsrt.Index(jsrt.Index(__item, 0), 1)
func (t *Transpiler) emitDestructureFromParam(pattern *ast.Node, source string) {
	if pattern.Kind != ast.KindArrayBindingPattern {
		return
	}
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil {
		return
	}
	t.w.addImport("github.com/i2y/ramune/jsrt", "")
	for i, elem := range bp.Elements.Nodes {
		if elem.Kind == ast.KindOmittedExpression {
			continue
		}
		be := elem.AsBindingElement()
		name := be.Name()
		if name == nil {
			continue
		}
		idx := fmt.Sprintf("jsrt.Index(%s, %d)", source, i)
		if name.Kind == ast.KindArrayBindingPattern {
			// Nested destructuring — recurse
			t.emitDestructureFromParam(name, idx)
		} else if name.Kind == ast.KindIdentifier {
			varName := goVarName(name.AsIdentifier().Text)
			// Try to get concrete type from checker for typed destructuring
			goType := ""
			if t.ck != nil {
				typ := t.ck.GetTypeAtLocation(name)
				if typ != nil {
					goType = t.tm.goType(typ)
				}
			}
			if goType != "" && goType != "any" {
				t.w.writef("%s := %s.(%s)", varName, idx, goType)
			} else {
				t.w.writef("%s := %s", varName, idx)
				if t.goAnyVars == nil {
					t.goAnyVars = make(map[string]bool)
				}
				t.goAnyVars[varName] = true
			}
			t.w.newline()
		}
	}
}

// isGoAnyVar checks if an AST node is an identifier tracked in goAnyVars.
func (t *Transpiler) isGoAnyVar(node *ast.Node) bool {
	if node.Kind == ast.KindIdentifier && t.goAnyVars != nil {
		return t.goAnyVars[goVarName(node.AsIdentifier().Text)]
	}
	return false
}

func goCodeProducesAny(code string) bool {
	return strings.HasSuffix(code, ".Unwrap()") ||
		strings.HasPrefix(code, "jsrt.Index(") ||
		strings.HasPrefix(code, "jsrt.GetField(") ||
		strings.Contains(code, ".(func(") || // any-func-call pattern: (x.(func(...) any))(...)
		strings.HasSuffix(code, ").Await()") || // *promise.Promise[any].Await() in nested context
		(strings.HasSuffix(code, "}()") && strings.Contains(code, "func() any")) ||
		isMapAnyAccess(code)
}

// isMapAnyAccess checks if code is a map[string]any access like "bodyCache[key]"
// where the key is a string variable (not a numeric index like buf[0]).
func isMapAnyAccess(code string) bool {
	// Strip outer parentheses
	for strings.HasPrefix(code, "(") && strings.HasSuffix(code, ")") {
		code = code[1 : len(code)-1]
	}
	idx := strings.Index(code, "[")
	if idx <= 0 || !strings.HasSuffix(code, "]") {
		return false
	}
	prefix := code[:idx]
	key := code[idx+1 : len(code)-1]
	// Must be a simple identifier (no dots, no parens, no jsrt.)
	if strings.Contains(prefix, ".") || strings.Contains(prefix, "(") || strings.Contains(prefix, "jsrt") {
		return false
	}
	// Key must not be a numeric literal (that would be slice access, not map access)
	if len(key) > 0 && key[0] >= '0' && key[0] <= '9' {
		return false
	}
	return true
}

// emitIntConversion emits int(expr), handling any-typed expressions with .(float64) assertion.
func (t *Transpiler) emitIntConversion(node *ast.Node) {
	if t.rightSideProducesGoAny(node) || t.getDeclaredGoType(node).IsAny() {
		t.w.write("int(")
		t.emitExpr(node)
		t.w.write(".(float64))")
	} else if t.getDeclaredGoType(node).IsPointer() {
		// *float64 → int: dereference then convert
		t.w.write("int(*")
		t.emitExpr(node)
		t.w.write(")")
	} else {
		t.w.write("int(")
		t.emitExpr(node)
		t.w.write(")")
	}
}

// isGoAnyExpression checks if an expression resolves to any at Go level,
// considering goAnyVars, as-expression unwrap, and primitive property access.
func (t *Transpiler) isGoAnyExpression(node *ast.Node) bool {
	if node == nil {
		return false
	}
	inner := node
	for {
		switch inner.Kind {
		case ast.KindParenthesizedExpression:
			inner = inner.AsParenthesizedExpression().Expression
			continue
		case ast.KindAsExpression:
			inner = inner.AsAsExpression().Expression
			continue
		case ast.KindNonNullExpression:
			inner = inner.AsNonNullExpression().Expression
			continue
		}
		break
	}
	if inner.Kind == ast.KindIdentifier {
		vn := goVarName(inner.AsIdentifier().Text)
		if t.goAnyVars != nil && t.goAnyVars[vn] {
			return true
		}
	}
	return false
}

// rightSideProducesGoAny checks if an expression node will produce any at Go level,
// considering goAnyVars, []any element access, and declared any-typed identifiers.
func (t *Transpiler) rightSideProducesGoAny(node *ast.Node) bool {
	if node == nil {
		return false
	}
	// Direct identifier in goAnyVars
	if node.Kind == ast.KindIdentifier {
		vn := goVarName(node.AsIdentifier().Text)
		if t.goAnyVars != nil && t.goAnyVars[vn] {
			return true
		}
	}
	// Element access on []any variable (concreteVarTypes)
	if node.Kind == ast.KindElementAccessExpression {
		ea := node.AsElementAccessExpression()
		if ea.Expression.Kind == ast.KindIdentifier {
			arrVarName := goVarName(ea.Expression.AsIdentifier().Text)
			if t.concreteVarTypes != nil && t.concreteVarTypes[arrVarName] == "[]any" {
				return true
			}
		}
	}
	return false
}

// emitInstanceOf handles instanceof: x instanceof Foo → Go type assertion.
func (t *Transpiler) emitInstanceOf(bin *ast.BinaryExpression) {
	operand := t.captureExpr(bin.Left)

	// Right side is the class/constructor name
	var typeName string
	if bin.Right.Kind == ast.KindIdentifier {
		name := bin.Right.AsIdentifier().Text
		typeName = goTypeName(name)

		// Special cases: types that don't exist in Go
		switch name {
		case "String":
			// JS boxed String doesn't exist in Go; check if value is a string
			t.w.writef("func() bool { _, ok := %s.(string); return ok }()", operand)
			return
		case "Promise":
			// Check if value is a *promise.Promise[any]
			t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
			t.w.writef("func() bool { _, ok := %s.(*promise.Promise[any]); return ok }()", operand)
			return
		}
	} else {
		typeName = t.captureExpr(bin.Right)
	}

	// If the operand is any-typed, use reflect-based type name check to avoid import cycles
	if t.getGoType(bin.Left).IsAny() {
		t.w.addImport("reflect", "")
		t.w.writef("func() bool { if %s == nil { return false }; return reflect.TypeOf(%s).Elem().Name() == %q }()", operand, operand, typeName)
	} else {
		// Try pointer type first (classes are emitted as pointer receivers)
		t.w.writef("func() bool { _, ok := %s.(*%s); return ok }()", operand, typeName)
	}
}

// emitInOperator handles in: "key" in obj → map key check.
func (t *Transpiler) emitInOperator(bin *ast.BinaryExpression) {
	key := t.captureExpr(bin.Left)
	obj := t.captureExpr(bin.Right)
	t.w.writef("func() bool { _, ok := %s[%s]; return ok }()", obj, key)
}

// emitDeleteExpr handles delete expressions: delete obj["key"] → delete(obj, "key").
func (t *Transpiler) emitDeleteExpr(node *ast.Node) {
	expr := node.AsDeleteExpression().Expression

	switch expr.Kind {
	case ast.KindElementAccessExpression:
		ea := expr.AsElementAccessExpression()
		obj := t.captureExpr(ea.Expression)
		key := t.captureExpr(ea.ArgumentExpression)
		t.w.writef("delete(%s, %s)", obj, key)
	case ast.KindPropertyAccessExpression:
		pa := expr.AsPropertyAccessExpression()
		obj := t.captureExpr(pa.Expression)
		propName := nodeText(pa.Name())
		t.w.writef("delete(%s, %q)", obj, propName)
	default:
		t.w.writef("/* unsupported delete target: %s */", expr.Kind.String())
	}
}
