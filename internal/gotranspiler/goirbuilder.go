package gotranspiler

import (
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// IRBuilder constructs GOTIR nodes from the TypeScript AST.
// It is self-contained: no dependency on the old Transpiler emit functions.
// All type-driven decisions are made here; the emitter just formats Go source.
type IRBuilder struct {
	ck *checker.Checker
	tm *typeMapper

	// Import tracking — populated during build, consumed by emitter
	imports        map[string]string // path → alias
	pendingImports map[string]string // alias → path

	// Identifier context
	thisReceiver          string
	importedNames         map[string]string
	importedOriginalNames map[string]string
	packageRefs           map[string]string
	samePackageExports    map[string]bool
	goNativeImports       map[string]string
	classNames            map[string]bool
	privateFields         map[string]string

	// Variable type tracking (unified — replaces goAnyVars, concreteVarTypes, etc.)
	varTypes map[string]GoTypeInfo

	// Narrowed types in current scope (from typeof/instanceof checks)
	narrowedTypes map[string]GoTypeInfo

	// Function context
	inAsyncBody    bool
	currentRetType string
	declContext    string // expected type from variable declaration context
	returnContext  string // struct type name during return expression
}

// NewIRBuilderFromChecker creates a standalone builder.
func NewIRBuilderFromChecker(ck *checker.Checker, tm *typeMapper) *IRBuilder {
	return &IRBuilder{
		ck:             ck,
		tm:             tm,
		imports:        make(map[string]string),
		pendingImports: make(map[string]string),
		varTypes:       make(map[string]GoTypeInfo),
		narrowedTypes:  make(map[string]GoTypeInfo),
		classNames:     make(map[string]bool),
	}
}

// addImport registers a Go import.
func (b *IRBuilder) addImport(path, alias string) {
	b.imports[path] = alias
}

// resolvePendingImport moves a pending import (by alias) into the active imports map.
func (b *IRBuilder) resolvePendingImport(alias string) {
	if path, ok := b.pendingImports[alias]; ok {
		b.imports[path] = alias
		delete(b.pendingImports, alias)
	}
}

// getGoType returns the Go type for an AST node using the checker's flow-narrowed type.
func (b *IRBuilder) getGoType(node *ast.Node) GoTypeInfo {
	if b.ck == nil || node == nil {
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	}
	if node.Kind == ast.KindThisKeyword {
		return GoTypeInfo{Category: GoTypePointer, GoStr: "*this", Name: "this"}
	}
	if b.isPackageRef(node) {
		return GoTypeInfo{Category: GoTypePointer, GoStr: "pkg"}
	}
	typ := b.ck.GetTypeAtLocation(node)
	if typ == nil {
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	}
	return b.tm.goTypeInfo(typ)
}

// getVarGoType returns the tracked Go type for a variable.
func (b *IRBuilder) getVarGoType(name string) GoTypeInfo {
	if ti, ok := b.varTypes[name]; ok {
		return ti
	}
	return GoTypeInfo{}
}

// trackVar records the Go type of a variable.
func (b *IRBuilder) trackVar(name string, typ GoTypeInfo) {
	b.varTypes[name] = typ
}

// isPackageRef checks if a node is a package reference.
func (b *IRBuilder) isPackageRef(node *ast.Node) bool {
	if node == nil || node.Kind != ast.KindIdentifier {
		return false
	}
	name := node.AsIdentifier().Text
	if _, ok := b.packageRefs[name]; ok {
		return true
	}
	return false
}

// dispatchMethod determines the dispatch target based on Go type category.
func (b *IRBuilder) dispatchMethod(objType GoTypeInfo, declType GoTypeInfo) DispatchTarget {
	for _, ty := range []GoTypeInfo{objType, declType} {
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
	return DispatchJSRTRuntime
}

// getNarrowedType returns the narrowed type for a variable, if any.
func (b *IRBuilder) getNarrowedType(name string) (GoTypeInfo, bool) {
	ti, ok := b.narrowedTypes[name]
	return ti, ok
}

// withNarrowing creates a copy of narrowedTypes with an additional entry.
func (b *IRBuilder) withNarrowing(varName string, typ GoTypeInfo) map[string]GoTypeInfo {
	m := make(map[string]GoTypeInfo, len(b.narrowedTypes)+1)
	for k, v := range b.narrowedTypes {
		m[k] = v
	}
	m[varName] = typ
	return m
}

// resolveReturnType gets the Go return type for a function node.
func (b *IRBuilder) resolveReturnType(node *ast.Node) string {
	if b.ck == nil {
		return ""
	}
	sig := b.ck.GetSignatureFromDeclaration(node)
	if sig == nil {
		return ""
	}
	retType := b.ck.GetReturnTypeOfSignature(sig)
	if retType == nil {
		return ""
	}
	return b.tm.goReturnType(retType)
}

// --------------------------------------------------------------------
// Expression building
// --------------------------------------------------------------------

// BuildExpr converts a TypeScript AST expression to a GOTIR expression node.
func (b *IRBuilder) BuildExpr(node *ast.Node) GoExpr {
	if node == nil {
		return irNil()
	}

	switch node.Kind {
	case ast.KindIdentifier:
		return b.buildIdentifier(node)

	case ast.KindNumericLiteral:
		return irFloat64(node.AsNumericLiteral().Text)

	case ast.KindStringLiteral:
		return irString(fmt.Sprintf("%q", node.AsStringLiteral().Text))

	case ast.KindNoSubstitutionTemplateLiteral:
		return irString(fmt.Sprintf("%q", node.AsNoSubstitutionTemplateLiteral().Text))

	case ast.KindTrueKeyword:
		return irBool("true")

	case ast.KindFalseKeyword:
		return irBool("false")

	case ast.KindNullKeyword, ast.KindUndefinedKeyword:
		return irNil()

	case ast.KindThisKeyword:
		name := "this"
		if b.thisReceiver != "" {
			name = b.thisReceiver
		}
		return &IRIdent{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePointer, GoStr: "*this", Name: "this"}},
			Name:     name,
		}

	case ast.KindBinaryExpression:
		return b.buildBinaryExpr(node)

	case ast.KindPrefixUnaryExpression:
		return b.buildPrefixUnary(node)

	case ast.KindPostfixUnaryExpression:
		return b.buildPostfixUnary(node)

	case ast.KindCallExpression:
		return b.buildCallExpr(node)

	case ast.KindPropertyAccessExpression:
		return b.buildPropertyAccess(node)

	case ast.KindElementAccessExpression:
		return b.buildElementAccess(node)

	case ast.KindTemplateExpression:
		return b.buildTemplateExpr(node)

	case ast.KindParenthesizedExpression:
		paren := node.AsParenthesizedExpression()
		inner := b.BuildExpr(paren.Expression)
		// Preserve parentheses in IR by wrapping
		return &IRUnaryOp{
			exprBase: exprBase{Typ: inner.ExprType()},
			Op:       "()",
			Operand:  inner,
		}

	case ast.KindConditionalExpression:
		return b.buildConditionalExpr(node)

	case ast.KindArrowFunction:
		return b.buildArrowFunction(node)

	case ast.KindFunctionExpression:
		return b.buildFunctionExpr(node)

	case ast.KindArrayLiteralExpression:
		return b.buildArrayLiteral(node)

	case ast.KindObjectLiteralExpression:
		return b.buildObjectLiteral(node)

	case ast.KindNewExpression:
		return b.buildNewExpr(node)

	case ast.KindAwaitExpression:
		return b.buildAwaitExpr(node)

	case ast.KindAsExpression:
		return b.buildAsExpr(node)

	case ast.KindTypeAssertionExpression:
		ta := node.AsTypeAssertion()
		return b.BuildExpr(ta.Expression)

	case ast.KindNonNullExpression:
		nn := node.AsNonNullExpression()
		return b.BuildExpr(nn.Expression)

	case ast.KindTypeOfExpression:
		typeOf := node.AsTypeOfExpression()
		return &IRTypeOf{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}},
			Expr:     b.BuildExpr(typeOf.Expression),
		}

	case ast.KindDeleteExpression:
		return b.buildDeleteExpr(node)

	case ast.KindRegularExpressionLiteral:
		return b.buildRegExpLiteral(node)

	default:
		return &IRRawExpr{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
			Code:     fmt.Sprintf("/* unsupported expr kind: %s */", node.Kind.String()),
		}
	}
}

// --- Identifier ---

func (b *IRBuilder) buildIdentifier(node *ast.Node) GoExpr {
	name := node.AsIdentifier().Text
	typ := b.getGoType(node)

	// Global identifier mappings
	switch name {
	case "undefined":
		return irNil()
	case "NaN":
		b.addImport("math", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
			Package:  "math", Func: "NaN",
		}
	case "Infinity":
		b.addImport("math", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
			Package:  "math", Func: "Inf",
			Args: []GoExpr{irLiteral("1", "int")},
		}
	case "parseInt":
		b.addImport("strconv", "")
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: "Atoi", PkgName: "strconv"}
	case "parseFloat":
		b.addImport("strconv", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(string) float64"}},
			Params:   []IRParam{{Name: "s", Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}}},
			RetType:  GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"},
			Body:     []GoStmt{&IRRawStmt{Code: "f, _ := strconv.ParseFloat(s, 64)\nreturn f"}},
		}
	case "isNaN":
		b.addImport("math", "")
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: "IsNaN", PkgName: "math"}
	case "isFinite":
		b.addImport("math", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(float64) bool"}},
			Params:   []IRParam{{Name: "f", Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}}},
			RetType:  GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"},
			Body:     []GoStmt{&IRRawStmt{Code: "return !math.IsInf(f, 0) && !math.IsNaN(f)"}},
		}
	case "decodeURIComponent", "decodeURI":
		b.addImport("net/url", "")
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: "QueryUnescape", PkgName: "url"}
	case "encodeURIComponent", "encodeURI":
		b.addImport("net/url", "")
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: "QueryEscape", PkgName: "url"}
	case "btoa":
		b.addImport("encoding/base64", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(string) string"}},
			Params:   []IRParam{{Name: "s", Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}}},
			RetType:  GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"},
			Body:     []GoStmt{&IRRawStmt{Code: "return base64.StdEncoding.EncodeToString([]byte(s))"}},
		}
	case "atob":
		b.addImport("encoding/base64", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(string) string"}},
			Params:   []IRParam{{Name: "s", Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}}},
			RetType:  GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"},
			Body:     []GoStmt{&IRRawStmt{Code: "b, _ := base64.StdEncoding.DecodeString(s)\nreturn string(b)"}},
		}
	case "crypto":
		return irBool("true")
	case "String":
		b.addImport("fmt", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(any) string"}},
			Params:   []IRParam{{Name: "v", Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}}},
			RetType:  GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"},
			Body:     []GoStmt{&IRRawStmt{Code: `if v == nil { return "" }; return fmt.Sprint(v)`}},
		}
	case "Boolean":
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(any, int) bool"}},
			Params: []IRParam{
				{Name: "v", Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
				{Name: "_", Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "int"}},
			},
			RetType: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"},
			Body:    []GoStmt{&IRRawStmt{Code: "return jsrt.ToBool(v)"}},
		}
	case "setTimeout":
		b.addImport("time", "")
		return &IRFuncLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func(any, float64)"}},
			Params: []IRParam{
				{Name: "fn", Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
				{Name: "ms", Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
			},
			Body: []GoStmt{&IRRawStmt{Code: "time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { fn.(func(any))(nil) })"}},
		}
	}

	// Package reference
	if pkg, ok := b.packageRefs[name]; ok {
		b.resolvePendingImport(pkg)
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: pkg}
	}

	// Imported name
	if pkg, ok := b.importedNames[name]; ok {
		b.resolvePendingImport(pkg)
		exportName := name
		if orig, ok := b.importedOriginalNames[name]; ok {
			exportName = orig
		}
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: goExportedName(exportName), PkgName: pkg}
	}

	// Same-package export
	if b.samePackageExports != nil && b.samePackageExports[name] {
		return &IRIdent{exprBase: exprBase{Typ: typ}, Name: goExportedName(name)}
	}

	// Tracked variable type
	goName := goVarName(name)
	if trackedType, ok := b.varTypes[goName]; ok {
		return &IRIdent{exprBase: exprBase{Typ: trackedType}, Name: goName}
	}

	return &IRIdent{exprBase: exprBase{Typ: typ}, Name: goName}
}

// --- Binary expressions ---

func (b *IRBuilder) buildBinaryExpr(node *ast.Node) GoExpr {
	bin := node.AsBinaryExpression()
	op := bin.OperatorToken.Kind
	resultType := b.getGoType(node)

	// instanceof
	if op == ast.KindInstanceOfKeyword {
		return b.buildInstanceOf(bin)
	}

	// in operator: "key" in obj → map check
	if op == ast.KindInKeyword {
		return b.buildInOperator(bin)
	}

	// Nullish coalescing: a ?? b
	if op == ast.KindQuestionQuestionToken {
		return b.buildNullishCoalescing(node, bin)
	}

	// Exponentiation: a ** b → math.Pow(a, b)
	if op == ast.KindAsteriskAsteriskToken {
		b.addImport("math", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
			Package:  "math", Func: "Pow",
			Args: []GoExpr{b.BuildExpr(bin.Left), b.BuildExpr(bin.Right)},
		}
	}

	// Compound assignment ops that need special handling
	switch op {
	case ast.KindQuestionQuestionEqualsToken,
		ast.KindAmpersandAmpersandEqualsToken,
		ast.KindBarBarEqualsToken,
		ast.KindAsteriskAsteriskEqualsToken:
		return b.buildCompoundAssignment(node, bin, op)

	case ast.KindGreaterThanGreaterThanGreaterThanToken:
		// Unsigned right shift: a >>> b → int(uint(a) >> uint(b))
		return &IRRawExpr{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "int"}},
			Code: fmt.Sprintf("int(uint(%s) >> uint(%s))",
				irExprPlaceholder(b.BuildExpr(bin.Left)),
				irExprPlaceholder(b.BuildExpr(bin.Right))),
		}
	}

	// typeof comparison: typeof x === "string"
	if b.isTypeOfComparison(bin) {
		return b.buildTypeOfComparison(bin)
	}

	// Standard binary operators
	goOp := tsOpToGoOp(op)
	if goOp == "" {
		return &IRRawExpr{
			exprBase: exprBase{Typ: resultType},
			Code:     fmt.Sprintf("/* unsupported binary op: %s */", op.String()),
		}
	}

	left := b.BuildExpr(bin.Left)
	right := b.BuildExpr(bin.Right)

	return &IRBinaryOp{
		exprBase: exprBase{Typ: resultType},
		Op:       goOp,
		Left:     left,
		Right:    right,
	}
}

func (b *IRBuilder) buildInstanceOf(bin *ast.BinaryExpression) GoExpr {
	typeName := ""
	if bin.Right.Kind == ast.KindIdentifier {
		typeName = goTypeName(bin.Right.AsIdentifier().Text)
	}
	return &IRInstanceOf{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
		Expr:     b.BuildExpr(bin.Left),
		TypeName: typeName,
	}
}

func (b *IRBuilder) buildInOperator(bin *ast.BinaryExpression) GoExpr {
	// "key" in obj → _, ok := obj["key"]; ok
	return &IRRawExpr{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
		Code: fmt.Sprintf("func() bool { _, __ok := %s[%s]; return __ok }()",
			irExprPlaceholder(b.BuildExpr(bin.Right)),
			irExprPlaceholder(b.BuildExpr(bin.Left))),
	}
}

func (b *IRBuilder) buildNullishCoalescing(node *ast.Node, bin *ast.BinaryExpression) GoExpr {
	left := b.BuildExpr(bin.Left)
	right := b.BuildExpr(bin.Right)
	leftType := left.ExprType()

	resultType := b.getGoType(node)
	if resultType.GoStr == "" || resultType.GoStr == "any" {
		resultType = leftType
	}

	return &IRNilCheck{
		exprBase: exprBase{Typ: resultType},
		Expr:     left,
		Then:     left,
		Else:     right,
	}
}

func (b *IRBuilder) buildCompoundAssignment(node *ast.Node, bin *ast.BinaryExpression, op ast.Kind) GoExpr {
	left := b.BuildExpr(bin.Left)
	right := b.BuildExpr(bin.Right)

	switch op {
	case ast.KindAsteriskAsteriskEqualsToken:
		// a **= b → a = math.Pow(a, b)
		b.addImport("math", "")
		return &IRBinaryOp{
			exprBase: exprBase{Typ: left.ExprType()},
			Op:       "=",
			Left:     left,
			Right: &IRStdlibCall{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
				Package:  "math", Func: "Pow",
				Args: []GoExpr{left, right},
			},
		}
	case ast.KindQuestionQuestionEqualsToken:
		// a ??= b → if a == nil { a = b }
		return &IRNilCheck{
			exprBase: exprBase{Typ: left.ExprType()},
			Expr:     left,
			Then:     left,
			Else: &IRBinaryOp{
				exprBase: exprBase{Typ: left.ExprType()},
				Op:       "=",
				Left:     left,
				Right:    right,
			},
		}
	case ast.KindAmpersandAmpersandEqualsToken:
		// a &&= b → if ToBool(a) { a = b }
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRRawExpr{
			exprBase: exprBase{Typ: left.ExprType()},
			Code: fmt.Sprintf("func() any { if jsrt.ToBool(%s) { %s = %s }; return %s }()",
				irExprPlaceholder(left), irExprPlaceholder(left),
				irExprPlaceholder(right), irExprPlaceholder(left)),
		}
	case ast.KindBarBarEqualsToken:
		// a ||= b → if !ToBool(a) { a = b }
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRRawExpr{
			exprBase: exprBase{Typ: left.ExprType()},
			Code: fmt.Sprintf("func() any { if !jsrt.ToBool(%s) { %s = %s }; return %s }()",
				irExprPlaceholder(left), irExprPlaceholder(left),
				irExprPlaceholder(right), irExprPlaceholder(left)),
		}
	}
	return &IRRawExpr{exprBase: exprBase{Typ: left.ExprType()}, Code: "/* unsupported compound assignment */"}
}

// --- Typeof comparison ---

func (b *IRBuilder) isTypeOfComparison(bin *ast.BinaryExpression) bool {
	op := bin.OperatorToken.Kind
	if op != ast.KindEqualsEqualsEqualsToken && op != ast.KindExclamationEqualsEqualsToken &&
		op != ast.KindEqualsEqualsToken && op != ast.KindExclamationEqualsToken {
		return false
	}
	if bin.Left.Kind == ast.KindTypeOfExpression || bin.Right.Kind == ast.KindTypeOfExpression {
		return true
	}
	return false
}

func (b *IRBuilder) buildTypeOfComparison(bin *ast.BinaryExpression) GoExpr {
	var typeOfNode, valueNode *ast.Node
	if bin.Left.Kind == ast.KindTypeOfExpression {
		typeOfNode = bin.Left
		valueNode = bin.Right
	} else {
		typeOfNode = bin.Right
		valueNode = bin.Left
	}

	typeOfExpr := typeOfNode.AsTypeOfExpression()
	inner := b.BuildExpr(typeOfExpr.Expression)
	innerType := b.getGoType(typeOfExpr.Expression)

	isNeg := bin.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken ||
		bin.OperatorToken.Kind == ast.KindExclamationEqualsToken

	typeStr := ""
	if valueNode.Kind == ast.KindStringLiteral {
		typeStr = valueNode.AsStringLiteral().Text
	}

	// For concrete types, typeof comparison can be resolved statically or as type assertion
	if !innerType.IsAny() && typeStr != "" {
		var matches bool
		switch typeStr {
		case "string":
			matches = innerType.IsString()
		case "number":
			matches = innerType.IsFloat64() || innerType.IsInt()
		case "boolean":
			matches = innerType.IsBool()
		case "object":
			matches = innerType.IsPointer() || innerType.IsMap() || innerType.IsSlice()
		case "function":
			matches = innerType.Category == GoTypeFunc
		}
		val := matches
		if isNeg {
			val = !val
		}
		if val {
			return irBool("true")
		}
		return irBool("false")
	}

	// For any-typed, generate idiomatic Go type checks
	boolType := GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}

	switch typeStr {
	case "undefined":
		// typeof x === "undefined" → x == nil
		op := "=="
		if isNeg {
			op = "!="
		}
		return &IRBinaryOp{
			exprBase: exprBase{Typ: boolType},
			Op:       op,
			Left:     inner,
			Right:    irNil(),
		}

	case "string":
		// typeof x === "string" → func() bool { _, ok := x.(string); return ok }()
		return b.buildTypeAssertionCheck(inner, "string", isNeg)

	case "number":
		// typeof x === "number" → type switch with float64, int
		return b.buildTypeAssertionCheckMulti(inner, []string{"float64", "int"}, isNeg)

	case "boolean":
		return b.buildTypeAssertionCheck(inner, "bool", isNeg)

	case "function":
		b.addImport("reflect", "")
		operand := EmitExprToString(inner)
		op := "=="
		if isNeg {
			op = "!="
		}
		code := fmt.Sprintf("reflect.TypeOf(%s).Kind() %s reflect.Func", operand, op)
		return &IRRawExpr{
			exprBase: exprBase{Typ: boolType},
			Code:     code,
		}

	default:
		// Fallback: runtime typeof check
		b.addImport("github.com/i2y/ramune/jsrt", "")
		cmpOp := "=="
		if isNeg {
			cmpOp = "!="
		}
		return &IRBinaryOp{
			exprBase: exprBase{Typ: boolType},
			Op:       cmpOp,
			Left: &IRStdlibCall{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}},
				Package:  "jsrt", Func: "TypeOf",
				Args: []GoExpr{inner},
			},
			Right: irString(fmt.Sprintf("%q", typeStr)),
		}
	}
}

// buildTypeAssertionCheck generates func() bool { _, ok := x.(T); return ok }()
func (b *IRBuilder) buildTypeAssertionCheck(inner GoExpr, goType string, negate bool) GoExpr {
	operand := EmitExprToString(inner)
	retExpr := "ok"
	if negate {
		retExpr = "!ok"
	}
	code := fmt.Sprintf("func() bool { _, ok := %s.(%s); return %s }()", operand, goType, retExpr)
	return &IRRawExpr{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
		Code:     code,
	}
}

// buildTypeAssertionCheckMulti generates func() bool { switch x.(type) { case T1, T2: return true ... } }()
func (b *IRBuilder) buildTypeAssertionCheckMulti(inner GoExpr, goTypes []string, negate bool) GoExpr {
	operand := EmitExprToString(inner)
	cases := strings.Join(goTypes, ", ")
	var code string
	if negate {
		code = fmt.Sprintf("func() bool { switch %s.(type) { case %s: return false; default: return true } }()", operand, cases)
	} else {
		code = fmt.Sprintf("func() bool { switch %s.(type) { case %s: return true; default: return false } }()", operand, cases)
	}
	return &IRRawExpr{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
		Code:     code,
	}
}

// --- Unary ---

func (b *IRBuilder) buildPrefixUnary(node *ast.Node) GoExpr {
	prefix := node.AsPrefixUnaryExpression()
	typ := b.getGoType(node)

	switch prefix.Operator {
	case ast.KindExclamationToken:
		// Check if operand is any-typed → need jsrt.ToBool
		operandType := b.getGoType(prefix.Operand)
		if operandType.IsAny() {
			b.addImport("github.com/i2y/ramune/jsrt", "")
			return &IRUnaryOp{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
				Op:       "!",
				Operand: &IRStdlibCall{
					exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
					Package:  "jsrt", Func: "ToBool",
					Args: []GoExpr{b.BuildExpr(prefix.Operand)},
				},
			}
		}
		return &IRUnaryOp{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
			Op:       "!",
			Operand:  b.BuildExpr(prefix.Operand),
		}
	case ast.KindMinusToken:
		return &IRUnaryOp{exprBase: exprBase{Typ: typ}, Op: "-", Operand: b.BuildExpr(prefix.Operand)}
	case ast.KindPlusToken:
		return b.BuildExpr(prefix.Operand)
	case ast.KindTildeToken:
		return &IRUnaryOp{exprBase: exprBase{Typ: typ}, Op: "^", Operand: b.BuildExpr(prefix.Operand)}
	case ast.KindPlusPlusToken:
		return &IRUnaryOp{exprBase: exprBase{Typ: typ}, Op: "++", Operand: b.BuildExpr(prefix.Operand)}
	case ast.KindMinusMinusToken:
		return &IRUnaryOp{exprBase: exprBase{Typ: typ}, Op: "--", Operand: b.BuildExpr(prefix.Operand)}
	default:
		return &IRRawExpr{exprBase: exprBase{Typ: typ}, Code: "/* unsupported prefix op */"}
	}
}

func (b *IRBuilder) buildPostfixUnary(node *ast.Node) GoExpr {
	postfix := node.AsPostfixUnaryExpression()
	typ := b.getGoType(postfix.Operand)

	// If operand is any-typed, special handling
	if typ.IsAny() {
		operand := b.BuildExpr(postfix.Operand)
		switch postfix.Operator {
		case ast.KindPlusPlusToken:
			return &IRBinaryOp{
				exprBase: exprBase{Typ: typ},
				Op:       "=",
				Left:     operand,
				Right: &IRBinaryOp{
					exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
					Op:       "+",
					Left:     &IRTypeAssertion{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}}, Expr: operand, TargetType: GoTypeInfo{GoStr: "float64"}},
					Right:    irFloat64("1"),
				},
			}
		case ast.KindMinusMinusToken:
			return &IRBinaryOp{
				exprBase: exprBase{Typ: typ},
				Op:       "=",
				Left:     operand,
				Right: &IRBinaryOp{
					exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
					Op:       "-",
					Left:     &IRTypeAssertion{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}}, Expr: operand, TargetType: GoTypeInfo{GoStr: "float64"}},
					Right:    irFloat64("1"),
				},
			}
		}
	}

	var goOp string
	switch postfix.Operator {
	case ast.KindPlusPlusToken:
		goOp = "++"
	case ast.KindMinusMinusToken:
		goOp = "--"
	default:
		return &IRRawExpr{exprBase: exprBase{Typ: typ}, Code: "/* unsupported postfix op */"}
	}
	return &IRUnaryOp{exprBase: exprBase{Typ: typ}, Op: goOp, Operand: b.BuildExpr(postfix.Operand), Postfix: true}
}

// --- Call expression ---

func (b *IRBuilder) buildCallExpr(node *ast.Node) GoExpr {
	call := node.AsCallExpression()
	resultType := b.getGoType(node)

	// Console calls
	if b.isConsoleCall(call) {
		return b.buildConsoleCall(call)
	}

	// Property access method calls: obj.method(args)
	if call.Expression.Kind == ast.KindPropertyAccessExpression {
		return b.buildMethodCallExpr(call, resultType)
	}

	// Direct function call: func(args)
	args := b.buildArgList(call.Arguments)
	fn := b.BuildExpr(call.Expression)

	return &IRCall{
		exprBase: exprBase{Typ: resultType},
		Func:     fn,
		Args:     args,
	}
}

func (b *IRBuilder) isConsoleCall(call *ast.CallExpression) bool {
	if call.Expression.Kind != ast.KindPropertyAccessExpression {
		return false
	}
	prop := call.Expression.AsPropertyAccessExpression()
	if prop.Expression.Kind == ast.KindIdentifier {
		return prop.Expression.AsIdentifier().Text == "console"
	}
	return false
}

func (b *IRBuilder) buildConsoleCall(call *ast.CallExpression) GoExpr {
	prop := call.Expression.AsPropertyAccessExpression()
	method := nodeText(prop.Name())
	b.addImport("github.com/i2y/ramune/jsrt/console", "")

	goMethod := "Log"
	switch method {
	case "error":
		goMethod = "Error"
	case "warn":
		goMethod = "Warn"
	case "info":
		goMethod = "Info"
	case "debug":
		goMethod = "Debug"
	}

	return &IRCall{
		exprBase: exprBase{Typ: GoTypeInfo{}},
		Func:     &IRIdent{exprBase: exprBase{}, Name: goMethod, PkgName: "console"},
		Args:     b.buildArgList(call.Arguments),
	}
}

// arrayCallbackMethods is the set of array methods that take a callback as their first argument.
var arrayCallbackMethods = map[string]bool{
	"map": true, "filter": true, "forEach": true,
	"find": true, "findIndex": true, "some": true,
	"every": true, "reduce": true, "flatMap": true,
}

func (b *IRBuilder) buildMethodCallExpr(call *ast.CallExpression, resultType GoTypeInfo) GoExpr {
	prop := call.Expression.AsPropertyAccessExpression()
	methodName := nodeText(prop.Name())

	// Determine dispatch strategy early (before building args) to intercept array callback methods.
	objType := b.getGoType(prop.Expression)
	declType := objType
	if prop.Expression.Kind == ast.KindIdentifier {
		goName := goVarName(prop.Expression.AsIdentifier().Text)
		if tracked, ok := b.varTypes[goName]; ok {
			declType = tracked
		}
	}

	// Array callback methods: build IRArrayMethodCall with typed callback
	if prop.QuestionDotToken == nil &&
		!(prop.Expression.Kind == ast.KindIdentifier && jsGlobalObjects[prop.Expression.AsIdentifier().Text]) &&
		prop.Expression.Kind != ast.KindThisKeyword &&
		arrayCallbackMethods[methodName] {
		dispatch := b.dispatchMethod(objType, declType)
		if dispatch == DispatchArrayHelper {
			return b.buildArrayMethodCall(call, objType, methodName, resultType)
		}
	}

	args := b.buildArgList(call.Arguments)

	// Optional chaining call: obj?.method() → nil check
	if prop.QuestionDotToken != nil {
		obj := b.BuildExpr(prop.Expression)
		if objType.IsAny() {
			// any-typed: fall through to normal dispatch which uses jsrt.Obj (nil-safe)
			b.addImport("github.com/i2y/ramune/jsrt", "")
		} else {
			// Concrete type → nil check wrapping
			callExpr := &IRMethodCall{
				exprBase: exprBase{Typ: resultType},
				Strategy: DispatchConcreteMethod,
				Object:   obj,
				Method:   goExportedName(methodName),
				Args:     args,
			}
			return &IRNilCheck{
				exprBase: exprBase{Typ: resultType},
				Expr:     obj,
				Then:     callExpr,
				Else:     irNil(),
			}
		}
	}

	// JS global static method calls: Object.keys, JSON.parse, Math.floor, etc.
	if prop.Expression.Kind == ast.KindIdentifier {
		objName := prop.Expression.AsIdentifier().Text
		if jsGlobalObjects[objName] {
			return b.buildGlobalStaticCall(objName, methodName, args, resultType)
		}
	}

	// this.method() → always concrete dispatch
	if prop.Expression.Kind == ast.KindThisKeyword {
		return &IRMethodCall{
			exprBase: exprBase{Typ: resultType},
			Strategy: DispatchConcreteMethod,
			Object:   b.BuildExpr(prop.Expression),
			Method:   goExportedName(methodName),
			Args:     args,
		}
	}

	dispatch := b.dispatchMethod(objType, declType)

	return &IRMethodCall{
		exprBase:   exprBase{Typ: resultType},
		Strategy:   dispatch,
		Object:     b.BuildExpr(prop.Expression),
		Method:     methodName,
		Args:       args,
		ElemGoType: objType.ElemType,
	}
}

func (b *IRBuilder) buildGlobalStaticCall(objName, method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	switch objName {
	case "Math":
		return b.buildMathCall(method, args)
	case "JSON":
		return b.buildJSONCall(method, args, resultType)
	case "Object":
		return b.buildObjectCall(method, args, resultType)
	case "Array":
		return b.buildArrayStaticCall(method, args, resultType)
	case "Promise":
		return b.buildPromiseStaticCall(method, args, resultType)
	case "Date":
		return b.buildDateCall(method, args, resultType)
	case "Number":
		return b.buildNumberCall(method, args, resultType)
	}
	// Default: pkg.Method(args)
	return &IRCall{
		exprBase: exprBase{Typ: resultType},
		Func: &IRIdent{
			exprBase: exprBase{Typ: resultType},
			Name:     goExportedName(method),
			PkgName:  goVarName(objName),
		},
		Args: args,
	}
}

func (b *IRBuilder) buildMathCall(method string, args []GoExpr) GoExpr {
	b.addImport("math", "")
	f64Type := GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}
	switch method {
	case "floor":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Floor", Args: args}
	case "ceil":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Ceil", Args: args}
	case "round":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Round", Args: args}
	case "abs":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Abs", Args: args}
	case "sqrt":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Sqrt", Args: args}
	case "pow":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Pow", Args: args}
	case "log":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Log", Args: args}
	case "min":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "", Func: "min", Args: args}
	case "max":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "", Func: "max", Args: args}
	case "random":
		b.addImport("math/rand", "")
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "rand", Func: "Float64"}
	case "sign":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Copysign",
			Args: []GoExpr{irFloat64("1"), args[0]}}
	case "trunc":
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: "Trunc", Args: args}
	default:
		return &IRStdlibCall{exprBase: exprBase{Typ: f64Type}, Package: "math", Func: goExportedName(method), Args: args}
	}
}

func (b *IRBuilder) buildJSONCall(method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	switch method {
	case "stringify":
		b.addImport("encoding/json", "")
		fn := &IRRawExpr{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func"}},
			Code:     "func(v any) string { b, _ := json.Marshal(v); return string(b) }",
		}
		return &IRCall{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}}, Func: fn, Args: args}
	case "parse":
		b.addImport("encoding/json", "")
		fn := &IRRawExpr{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func"}},
			Code:     "func(s string) any { var v any; json.Unmarshal([]byte(s), &v); return v }",
		}
		return &IRCall{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}}, Func: fn, Args: args}
	}
	return &IRRawExpr{exprBase: exprBase{Typ: resultType}, Code: fmt.Sprintf("/* JSON.%s */", method)}
}

func (b *IRBuilder) buildObjectCall(method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	switch method {
	case "create":
		return &IRCompositeLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeMap, GoStr: "map[string]any"}},
			TypeStr:  "map[string]any",
		}
	case "assign":
		if len(args) > 0 {
			return args[0]
		}
		return irNil()
	case "keys":
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeSlice, GoStr: "[]string", ElemType: "string"}},
			Package:  "jsrt", Func: "Keys", Args: args,
		}
	case "values":
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeSlice, GoStr: "[]any", ElemType: "any"}},
			Package:  "jsrt", Func: "Values", Args: args,
		}
	case "entries":
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeSlice, GoStr: "[]any", ElemType: "any"}},
			Package:  "jsrt", Func: "Entries", Args: args,
		}
	case "fromEntries":
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeMap, GoStr: "map[string]any"}},
			Package:  "jsrt", Func: "FromEntries", Args: args,
		}
	}
	return &IRRawExpr{exprBase: exprBase{Typ: resultType}, Code: fmt.Sprintf("/* Object.%s */", method)}
}

func (b *IRBuilder) buildArrayStaticCall(method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	switch method {
	case "isArray":
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
			Package:  "jsrt", Func: "IsArray", Args: args,
		}
	case "from":
		if len(args) > 0 {
			return args[0] // simplified: Array.from(x) → x
		}
	}
	return &IRRawExpr{exprBase: exprBase{Typ: resultType}, Code: fmt.Sprintf("/* Array.%s */", method)}
}

func (b *IRBuilder) buildPromiseStaticCall(method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	b.addImport("github.com/i2y/ramune/jsrt/promise", "")
	switch method {
	case "all":
		return &IRStdlibCall{
			exprBase: exprBase{Typ: resultType},
			Package:  "promise", Func: "All", Args: args,
		}
	case "resolve":
		return &IRStdlibCall{
			exprBase: exprBase{Typ: resultType},
			Package:  "promise", Func: "Resolve", Args: args,
		}
	case "reject":
		return &IRStdlibCall{
			exprBase: exprBase{Typ: resultType},
			Package:  "promise", Func: "Reject", Args: args,
		}
	}
	return &IRStdlibCall{
		exprBase: exprBase{Typ: resultType},
		Package:  "promise", Func: goExportedName(method), Args: args,
	}
}

func (b *IRBuilder) buildDateCall(method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	if method == "now" {
		b.addImport("time", "")
		return &IRRawExpr{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}},
			Code:     "float64(time.Now().UnixMilli())",
		}
	}
	return &IRRawExpr{exprBase: exprBase{Typ: resultType}, Code: fmt.Sprintf("/* Date.%s */", method)}
}

func (b *IRBuilder) buildNumberCall(method string, args []GoExpr, resultType GoTypeInfo) GoExpr {
	if method == "isInteger" && len(args) > 0 {
		b.addImport("math", "")
		return &IRRawExpr{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
			Code:     fmt.Sprintf("func(f float64) bool { return f == math.Floor(f) }(%s)", irExprPlaceholder(args[0])),
		}
	}
	return &IRRawExpr{exprBase: exprBase{Typ: resultType}, Code: fmt.Sprintf("/* Number.%s */", method)}
}

// --- Property access ---

func (b *IRBuilder) buildPropertyAccess(node *ast.Node) GoExpr {
	prop := node.AsPropertyAccessExpression()
	propName := nodeText(prop.Name())
	isPrivate := strings.HasPrefix(propName, "#") || ast.IsPrivateIdentifier(prop.Name())
	if strings.HasPrefix(propName, "#") {
		propName = propName[1:]
	}
	resultType := b.getGoType(node)

	// Optional chaining
	if prop.QuestionDotToken != nil {
		return b.buildOptionalPropertyAccess(prop, propName, resultType)
	}

	// Math.xxx
	if prop.Expression.Kind == ast.KindIdentifier {
		objName := prop.Expression.AsIdentifier().Text
		if objName == "Math" {
			return b.buildMathPropertyAccess(propName)
		}
		if objName == "crypto" && propName == "subtle" {
			b.addImport("github.com/i2y/ramune/jsrt/web", "web")
			return &IRIdent{exprBase: exprBase{Typ: resultType}, Name: "Subtle", PkgName: "web"}
		}
		// Enum/class static member
		if b.ck != nil {
			sym := b.ck.GetSymbolAtLocation(prop.Expression)
			if sym != nil {
				if sym.Flags&ast.SymbolFlagsEnum != 0 {
					return &IRIdent{exprBase: exprBase{Typ: resultType}, Name: goTypeName(objName) + goExportedName(propName)}
				}
				if sym.Flags&ast.SymbolFlagsClass != 0 {
					return &IRIdent{exprBase: exprBase{Typ: resultType}, Name: goTypeName(objName) + "_" + goExportedName(propName)}
				}
			}
		}
	}

	// .length
	if propName == "length" {
		objType := b.getGoType(prop.Expression)
		if objType.IsAny() {
			b.addImport("github.com/i2y/ramune/jsrt", "")
			return &IRStdlibCall{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "int"}},
				Package:  "jsrt", Func: "Len",
				Args: []GoExpr{b.BuildExpr(prop.Expression)},
			}
		}
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "int"}},
			Package:  "", Func: "len",
			Args: []GoExpr{b.BuildExpr(prop.Expression)},
		}
	}

	// Type-driven dispatch for field access
	objType := b.getGoType(prop.Expression)
	declType := objType
	if prop.Expression.Kind == ast.KindIdentifier {
		goName := goVarName(prop.Expression.AsIdentifier().Text)
		if tracked, ok := b.varTypes[goName]; ok {
			declType = tracked
		}
	}

	goField := goExportedName(propName)
	if isPrivate {
		goField = goVarName(toCamelCase(propName))
	}
	if b.privateFields != nil {
		if pf, ok := b.privateFields[propName]; ok {
			goField = pf
		}
	}

	obj := b.BuildExpr(prop.Expression)

	// Narrowed types
	if prop.Expression.Kind == ast.KindIdentifier {
		varName := prop.Expression.AsIdentifier().Text
		if narrowed, ok := b.getNarrowedType(varName); ok {
			return &IRFieldAccess{
				exprBase:       exprBase{Typ: resultType},
				Object:         &IRTypeAssertion{exprBase: exprBase{Typ: narrowed}, Expr: obj, TargetType: narrowed},
				Field:          goExportedName(propName),
				NeedsAssertion: false,
			}
		}
	}

	// Any-typed → jsrt.Obj().Get()
	if declType.IsAny() && !b.isPackageRef(prop.Expression) {
		if !objType.IsAny() {
			// Checker narrowed → type assertion + field
			return &IRFieldAccess{
				exprBase:       exprBase{Typ: resultType},
				Object:         obj,
				Field:          goField,
				NeedsAssertion: true,
				AssertType:     objType,
			}
		}
		// No narrowing → runtime dispatch
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRJSRTCall{
			exprBase: exprBase{Typ: resultType},
			Pattern:  "Get",
			Object:   obj,
			Field:    goExportedName(propName),
		}
	}

	// Discriminated union interface → getter method
	if declType.Category == GoTypeInterface {
		return &IRCall{
			exprBase: exprBase{Typ: resultType},
			Func: &IRFieldAccess{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}},
				Object:   obj,
				Field:    "Get" + goExportedName(propName),
			},
		}
	}

	// Getter accessor check
	if b.ck != nil {
		sym := b.ck.GetSymbolAtLocation(node)
		if sym != nil && sym.Flags&ast.SymbolFlagsGetAccessor != 0 {
			return &IRCall{
				exprBase: exprBase{Typ: resultType},
				Func: &IRFieldAccess{
					exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}},
					Object:   obj,
					Field:    goExportedName(propName),
				},
			}
		}
	}

	// Direct field access
	return &IRFieldAccess{
		exprBase: exprBase{Typ: resultType},
		Object:   obj,
		Field:    goField,
	}
}

func (b *IRBuilder) buildOptionalPropertyAccess(prop *ast.PropertyAccessExpression, propName string, resultType GoTypeInfo) GoExpr {
	obj := b.BuildExpr(prop.Expression)
	goField := goExportedName(propName)

	if propName == "length" {
		return &IRNilCheck{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
			Expr:     obj,
			Then: &IRStdlibCall{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "int"}},
				Package:  "", Func: "len",
				Args: []GoExpr{obj},
			},
			Else: irNil(),
		}
	}

	objType := b.getGoType(prop.Expression)
	if objType.IsAny() {
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRJSRTCall{
			exprBase: exprBase{Typ: resultType},
			Pattern:  "Get",
			Object:   obj,
			Field:    goField,
		}
	}

	return &IRNilCheck{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
		Expr:     obj,
		Then: &IRFieldAccess{
			exprBase: exprBase{Typ: resultType},
			Object:   obj,
			Field:    goField,
		},
		Else: irNil(),
	}
}

func (b *IRBuilder) buildMathPropertyAccess(prop string) GoExpr {
	b.addImport("math", "")
	f64Type := GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}
	switch prop {
	case "PI":
		return &IRIdent{exprBase: exprBase{Typ: f64Type}, Name: "Pi", PkgName: "math"}
	case "E":
		return &IRIdent{exprBase: exprBase{Typ: f64Type}, Name: "E", PkgName: "math"}
	case "floor":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Floor", PkgName: "math"}
	case "ceil":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Ceil", PkgName: "math"}
	case "round":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Round", PkgName: "math"}
	case "abs":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Abs", PkgName: "math"}
	case "sqrt":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Sqrt", PkgName: "math"}
	case "pow":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Pow", PkgName: "math"}
	case "log":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Log", PkgName: "math"}
	case "min":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "min"}
	case "max":
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "max"}
	case "random":
		b.addImport("math/rand", "")
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: "Float64", PkgName: "rand"}
	default:
		return &IRIdent{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc}}, Name: goExportedName(prop), PkgName: "math"}
	}
}

// --- Element access ---

func (b *IRBuilder) buildElementAccess(node *ast.Node) GoExpr {
	ea := node.AsElementAccessExpression()
	resultType := b.getGoType(node)
	obj := b.BuildExpr(ea.Expression)
	index := b.BuildExpr(ea.ArgumentExpression)
	objType := b.getGoType(ea.Expression)

	// Optional chaining: obj?.[key]
	if ea.QuestionDotToken != nil {
		return &IRNilCheck{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
			Expr:     obj,
			Then:     &IRIndexAccess{exprBase: exprBase{Typ: resultType}, Object: obj, Index: index},
			Else:     irNil(),
		}
	}

	// Any-typed → jsrt.Index
	if objType.IsAny() {
		b.addImport("github.com/i2y/ramune/jsrt", "")
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}},
			Package:  "jsrt", Func: "Index",
			Args: []GoExpr{obj, index},
		}
	}

	// String indexing: s[i] → string(s[i])
	if objType.IsString() {
		return &IRTypeConversion{
			exprBase:   exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}},
			Expr:       &IRIndexAccess{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "byte"}}, Object: obj, Index: index},
			TargetType: "string",
		}
	}

	return &IRIndexAccess{exprBase: exprBase{Typ: resultType}, Object: obj, Index: index}
}

// --- Template expression ---

func (b *IRBuilder) buildTemplateExpr(node *ast.Node) GoExpr {
	tmpl := node.AsTemplateExpression()
	var formatParts []string
	var args []GoExpr

	if tmpl.Head != nil {
		formatParts = append(formatParts, escapeFormatString(tmpl.Head.AsTemplateHead().Text))
	}

	if tmpl.TemplateSpans != nil {
		for _, span := range tmpl.TemplateSpans.Nodes {
			ts := span.AsTemplateSpan()
			exprType := b.getGoType(ts.Expression)
			formatParts = append(formatParts, formatSpecifier(exprType.GoStr))
			args = append(args, b.BuildExpr(ts.Expression))
			if ts.Literal != nil {
				switch ts.Literal.Kind {
				case ast.KindTemplateMiddle:
					formatParts = append(formatParts, escapeFormatString(ts.Literal.AsTemplateMiddle().Text))
				case ast.KindTemplateTail:
					formatParts = append(formatParts, escapeFormatString(ts.Literal.AsTemplateTail().Text))
				}
			}
		}
	}

	format := strings.Join(formatParts, "")
	if len(args) == 0 {
		return irString(fmt.Sprintf("%q", format))
	}
	if format == "%s" && len(args) == 1 {
		return args[0]
	}

	b.addImport("fmt", "")
	return &IRSprintfCall{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}},
		Format:   format,
		Args:     args,
	}
}

// --- Conditional expression (ternary) ---

func (b *IRBuilder) buildConditionalExpr(node *ast.Node) GoExpr {
	cond := node.AsConditionalExpression()
	resultType := b.getGoType(node)
	if resultType.GoStr == "" {
		resultType = GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	}

	condExpr := b.BuildExpr(cond.Condition)
	thenExpr := b.BuildExpr(cond.WhenTrue)
	elseExpr := b.BuildExpr(cond.WhenFalse)

	return &IRTernary{
		exprBase: exprBase{Typ: resultType},
		Cond:     condExpr,
		Then:     thenExpr,
		Else:     elseExpr,
	}
}

// --- Arrow function ---

func (b *IRBuilder) buildArrowFunction(node *ast.Node) GoExpr {
	arrow := node.AsArrowFunction()
	retType := b.resolveReturnType(node)
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	params := b.buildParamList(node)
	retTypeInfo := goTypeInfoFromString(retType)

	// Build body
	savedAsync := b.inAsyncBody
	savedRetType := b.currentRetType
	b.inAsyncBody = isAsync
	b.currentRetType = retType

	var body []GoStmt
	if arrow.Body.Kind == ast.KindBlock {
		body = b.buildStmtList(arrow.Body)
	} else {
		// Expression body: () => expr → return expr
		body = []GoStmt{&IRReturn{Values: []GoExpr{b.BuildExpr(arrow.Body)}}}
	}

	b.inAsyncBody = savedAsync
	b.currentRetType = savedRetType

	funcType := GoTypeInfo{Category: GoTypeFunc, GoStr: "func"}
	return &IRFuncLit{
		exprBase: exprBase{Typ: funcType},
		Params:   params,
		RetType:  retTypeInfo,
		Body:     body,
		IsAsync:  isAsync,
	}
}

// --- Function expression ---

func (b *IRBuilder) buildFunctionExpr(node *ast.Node) GoExpr {
	retType := b.resolveReturnType(node)
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	params := b.buildParamList(node)
	retTypeInfo := goTypeInfoFromString(retType)

	savedAsync := b.inAsyncBody
	savedRetType := b.currentRetType
	b.inAsyncBody = isAsync
	b.currentRetType = retType

	body := b.buildStmtList(node.Body())

	b.inAsyncBody = savedAsync
	b.currentRetType = savedRetType

	funcType := GoTypeInfo{Category: GoTypeFunc, GoStr: "func"}
	return &IRFuncLit{
		exprBase: exprBase{Typ: funcType},
		Params:   params,
		RetType:  retTypeInfo,
		Body:     body,
		IsAsync:  isAsync,
	}
}

// --- Array literal ---

func (b *IRBuilder) buildArrayLiteral(node *ast.Node) GoExpr {
	arr := node.AsArrayLiteralExpression()
	resultType := b.getGoType(node)
	elemType := "any"
	if resultType.IsSlice() && resultType.ElemType != "" {
		elemType = resultType.ElemType
	}

	var elements []GoExpr
	hasSpread := false
	if arr.Elements != nil {
		for _, elem := range arr.Elements.Nodes {
			if elem.Kind == ast.KindSpreadElement {
				hasSpread = true
			}
			elements = append(elements, b.BuildExpr(elem))
		}
	}

	if hasSpread {
		// [...a, b, ...c] → append(append(a, b), c...)
		// Build a chain of append calls
		sliceType := GoTypeInfo{Category: GoTypeSlice, GoStr: "[]" + elemType, ElemType: elemType}
		var result GoExpr
		var pending []GoExpr // non-spread elements to batch
		flush := func() {
			if len(pending) == 0 {
				return
			}
			if result == nil {
				result = &IRCompositeLit{
					exprBase: exprBase{Typ: sliceType},
					TypeStr:  "[]" + elemType,
					Elements: make([]IRKeyValue, len(pending)),
				}
				for i, p := range pending {
					result.(*IRCompositeLit).Elements[i] = IRKeyValue{Value: p}
				}
			} else {
				args := []GoExpr{result}
				args = append(args, pending...)
				result = &IRCall{
					exprBase: exprBase{Typ: sliceType},
					Func:     &IRIdent{Name: "append"},
					Args:     args,
				}
			}
			pending = nil
		}
		for _, elem := range arr.Elements.Nodes {
			if elem.Kind == ast.KindSpreadElement {
				flush()
				spread := b.BuildExpr(elem.AsSpreadElement().Expression)
				if result == nil {
					result = spread
				} else {
					result = &IRCall{
						exprBase: exprBase{Typ: sliceType},
						Func:     &IRIdent{Name: "append"},
						Args:     []GoExpr{result, &IRUnaryOp{exprBase: exprBase{Typ: sliceType}, Op: "...", Operand: spread, Postfix: true}},
					}
				}
			} else {
				pending = append(pending, b.BuildExpr(elem))
			}
		}
		flush()
		if result == nil {
			result = &IRCompositeLit{exprBase: exprBase{Typ: sliceType}, TypeStr: "[]" + elemType}
		}
		return result
	}

	return &IRCompositeLit{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeSlice, GoStr: "[]" + elemType, ElemType: elemType}},
		TypeStr:  "[]" + elemType,
		Elements: b.buildArrayElements(arr.Elements),
	}
}

func (b *IRBuilder) buildArrayElements(nodes *ast.NodeList) []IRKeyValue {
	if nodes == nil {
		return nil
	}
	var elems []IRKeyValue
	for _, node := range nodes.Nodes {
		elems = append(elems, IRKeyValue{Value: b.BuildExpr(node)})
	}
	return elems
}

// --- Object literal ---

func (b *IRBuilder) buildObjectLiteral(node *ast.Node) GoExpr {
	obj := node.AsObjectLiteralExpression()
	resultType := b.getGoType(node)

	// Determine struct type name from checker
	typeName := ""
	if b.ck != nil {
		objType := b.ck.GetTypeAtLocation(node)
		if objType != nil {
			sym := objType.Symbol()
			if sym != nil && sym.Name != "" && !strings.HasPrefix(sym.Name, "__") && isValidGoIdentifier(sym.Name) {
				typeName = goTypeName(sym.Name)
			}
		}
	}
	if typeName == "" && b.returnContext != "" && isValidGoIdentifier(b.returnContext) {
		switch b.returnContext {
		case "float64", "string", "bool", "int", "any":
		default:
			typeName = b.returnContext
		}
	}
	if typeName == "" && b.declContext != "" {
		ctx := b.declContext
		if et, ok := sliceElemType(ctx); ok {
			ctx = et
		}
		if isValidGoIdentifier(ctx) {
			switch ctx {
			case "float64", "string", "bool", "int", "any":
			default:
				typeName = ctx
			}
		}
	}

	var elements []IRKeyValue
	if obj.Properties != nil {
		for _, prop := range obj.Properties.Nodes {
			switch prop.Kind {
			case ast.KindPropertyAssignment:
				pa := prop.AsPropertyAssignment()
				key := ""
				name := prop.Name()
				if name != nil && name.Kind == ast.KindIdentifier {
					if typeName != "" {
						key = goExportedName(name.AsIdentifier().Text)
					} else {
						key = name.AsIdentifier().Text
					}
				} else if name != nil && name.Kind == ast.KindStringLiteral {
					key = name.AsStringLiteral().Text
				}
				elements = append(elements, IRKeyValue{Key: key, Value: b.BuildExpr(pa.Initializer)})
			case ast.KindShorthandPropertyAssignment:
				name := prop.Name()
				if name != nil && name.Kind == ast.KindIdentifier {
					id := name.AsIdentifier().Text
					key := id
					if typeName != "" {
						key = goExportedName(id)
					}
					elements = append(elements, IRKeyValue{
						Key:   key,
						Value: &IRIdent{exprBase: exprBase{Typ: b.getGoType(name)}, Name: goVarName(id)},
					})
				}
			}
		}
	}

	goTypeStr := typeName
	if goTypeStr == "" {
		goTypeStr = "map[string]any"
		if resultType.IsMap() {
			goTypeStr = resultType.GoStr
		}
	}

	return &IRCompositeLit{
		exprBase: exprBase{Typ: resultType},
		TypeStr:  goTypeStr,
		Elements: elements,
	}
}

// --- New expression ---

func (b *IRBuilder) buildNewExpr(node *ast.Node) GoExpr {
	newExpr := node.AsNewExpression()
	resultType := b.getGoType(node)

	typeName := ""
	if newExpr.Expression.Kind == ast.KindIdentifier {
		typeName = goTypeName(newExpr.Expression.AsIdentifier().Text)
	}

	// Special cases
	switch typeName {
	case "Error", "TypeError", "RangeError":
		b.addImport("errors", "")
		msg := irString(`"error"`)
		if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
			msg = b.BuildExpr(newExpr.Arguments.Nodes[0])
		}
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePointer, GoStr: "*jsrt.JSError"}},
			Package:  "errors", Func: "New", Args: []GoExpr{msg},
		}
	case "RegExp":
		b.addImport("regexp", "")
		if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
			return &IRStdlibCall{
				exprBase: exprBase{Typ: resultType},
				Package:  "regexp", Func: "MustCompile",
				Args: []GoExpr{b.BuildExpr(newExpr.Arguments.Nodes[0])},
			}
		}
	case "Map":
		return &IRMakeCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeMap, GoStr: "map[string]any"}},
			TypeStr:  "map[string]any",
		}
	case "Set":
		return &IRMakeCall{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeMap, GoStr: "map[any]struct{}"}},
			TypeStr:  "map[any]struct{}",
		}
	case "Uint8Array":
		if newExpr.Arguments != nil && len(newExpr.Arguments.Nodes) > 0 {
			return &IRMakeCall{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeSlice, GoStr: "[]byte", ElemType: "byte"}},
				TypeStr:  "[]byte",
				Len:      b.BuildExpr(newExpr.Arguments.Nodes[0]),
			}
		}
		return &IRCompositeLit{
			exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeSlice, GoStr: "[]byte", ElemType: "byte"}},
			TypeStr:  "[]byte",
		}
	}

	// General case: &TypeName{fields...}
	args := b.buildArgList(newExpr.Arguments)
	return &IRNewExpr{
		exprBase: exprBase{Typ: resultType},
		TypeName: typeName,
		Args:     b.argsToKeyValues(args),
	}
}

// argsToKeyValues converts positional args to IRKeyValue for struct initialization.
func (b *IRBuilder) argsToKeyValues(args []GoExpr) []IRKeyValue {
	var kvs []IRKeyValue
	for _, arg := range args {
		kvs = append(kvs, IRKeyValue{Value: arg})
	}
	return kvs
}

// --- Await ---

func (b *IRBuilder) buildAwaitExpr(node *ast.Node) GoExpr {
	await := node.AsAwaitExpression()
	inner := b.BuildExpr(await.Expression)
	innerType := inner.ExprType()

	resultType := GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	if innerType.IsPromise() && innerType.ElemType != "" {
		resultType = goTypeInfoFromString(innerType.ElemType)
	}

	return &IRAwait{exprBase: exprBase{Typ: resultType}, Expr: inner}
}

// --- As expression ---

func (b *IRBuilder) buildAsExpr(node *ast.Node) GoExpr {
	as := node.AsAsExpression()
	inner := b.BuildExpr(as.Expression)

	if as.Type != nil && b.ck != nil {
		targetType := b.ck.GetTypeAtLocation(as.Type)
		if targetType != nil {
			goTarget := b.tm.goType(targetType)
			if goTarget != "" && goTarget != "any" {
				targetInfo := goTypeInfoFromString(goTarget)
				return &IRTypeAssertion{
					exprBase:   exprBase{Typ: targetInfo},
					Expr:       inner,
					TargetType: targetInfo,
				}
			}
		}
	}
	return inner
}

// --- Delete expression ---

func (b *IRBuilder) buildDeleteExpr(node *ast.Node) GoExpr {
	del := node.AsDeleteExpression()
	if del.Expression.Kind == ast.KindElementAccessExpression {
		ea := del.Expression.AsElementAccessExpression()
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{}},
			Package:  "", Func: "delete",
			Args: []GoExpr{b.BuildExpr(ea.Expression), b.BuildExpr(ea.ArgumentExpression)},
		}
	}
	if del.Expression.Kind == ast.KindPropertyAccessExpression {
		prop := del.Expression.AsPropertyAccessExpression()
		propName := nodeText(prop.Name())
		return &IRStdlibCall{
			exprBase: exprBase{Typ: GoTypeInfo{}},
			Package:  "", Func: "delete",
			Args: []GoExpr{b.BuildExpr(prop.Expression), irString(fmt.Sprintf("%q", propName))},
		}
	}
	return &IRRawExpr{exprBase: exprBase{}, Code: "/* unsupported delete target */"}
}

// --- RegExp literal ---

func (b *IRBuilder) buildRegExpLiteral(node *ast.Node) GoExpr {
	text := node.AsRegularExpressionLiteral().Text
	b.addImport("regexp", "")
	// Parse /pattern/flags
	if len(text) > 1 && text[0] == '/' {
		lastSlash := strings.LastIndex(text[1:], "/") + 1
		if lastSlash > 0 {
			pattern := text[1:lastSlash]
			// Use quoted string if pattern contains backtick (can't use raw string)
			var patternLit string
			if strings.Contains(pattern, "`") {
				patternLit = fmt.Sprintf("%q", pattern)
			} else {
				patternLit = fmt.Sprintf("`%s`", pattern)
			}
			return &IRStdlibCall{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePointer, GoStr: "*regexp.Regexp"}},
				Package:  "regexp", Func: "MustCompile",
				Args: []GoExpr{irString(patternLit)},
			}
		}
	}
	return &IRStdlibCall{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePointer, GoStr: "*regexp.Regexp"}},
		Package:  "regexp", Func: "MustCompile",
		Args: []GoExpr{irString(fmt.Sprintf("%q", text))},
	}
}

// --------------------------------------------------------------------
// Statement building
// --------------------------------------------------------------------

// BuildStmt converts a TypeScript AST statement to a GOTIR statement node.
func (b *IRBuilder) BuildStmt(node *ast.Node) GoStmt {
	if node == nil {
		return nil
	}

	switch node.Kind {
	case ast.KindExpressionStatement:
		exprStmt := node.AsExpressionStatement()
		return &IRExprStmt{Expr: b.BuildExpr(exprStmt.Expression)}

	case ast.KindReturnStatement:
		return b.buildReturnStmt(node)

	case ast.KindIfStatement:
		return b.buildIfStmt(node)

	case ast.KindForStatement:
		return b.buildForStmt(node)

	case ast.KindForInStatement, ast.KindForOfStatement:
		return b.buildForInOfStmt(node)

	case ast.KindWhileStatement:
		return b.buildWhileStmt(node)

	case ast.KindDoStatement:
		return b.buildDoWhileStmt(node)

	case ast.KindSwitchStatement:
		return b.buildSwitchStmt(node)

	case ast.KindTryStatement:
		return b.buildTryStmt(node)

	case ast.KindBlock:
		return b.buildBlock(node)

	case ast.KindVariableStatement:
		return b.buildVariableStmt(node)

	case ast.KindBreakStatement:
		label := ""
		bs := node.AsBreakStatement()
		if bs.Label != nil {
			label = nodeText(bs.Label)
		}
		return &IRBreak{Label: label}

	case ast.KindContinueStatement:
		label := ""
		cs := node.AsContinueStatement()
		if cs.Label != nil {
			label = nodeText(cs.Label)
		}
		return &IRContinue{Label: label}

	case ast.KindLabeledStatement:
		ls := node.AsLabeledStatement()
		return &IRLabeled{
			Label: nodeText(ls.Label),
			Stmt:  b.BuildStmt(ls.Statement),
		}

	case ast.KindThrowStatement:
		ts := node.AsThrowStatement()
		return &IRExprStmt{Expr: &IRStdlibCall{
			exprBase: exprBase{},
			Package:  "", Func: "panic",
			Args: []GoExpr{b.BuildExpr(ts.Expression)},
		}}

	case ast.KindEmptyStatement:
		return nil

	default:
		return &IRRawStmt{Code: fmt.Sprintf("/* unsupported stmt kind: %s */", node.Kind.String())}
	}
}

func (b *IRBuilder) buildReturnStmt(node *ast.Node) GoStmt {
	ret := node.AsReturnStatement()

	if b.inAsyncBody && ret.Expression != nil {
		return &IRResolveCall{Value: b.BuildExpr(ret.Expression)}
	}

	if ret.Expression == nil {
		return &IRReturn{}
	}
	return &IRReturn{Values: []GoExpr{b.BuildExpr(ret.Expression)}}
}

func (b *IRBuilder) buildIfStmt(node *ast.Node) GoStmt {
	ifStmt := node.AsIfStatement()

	cond := b.BuildExpr(ifStmt.Expression)
	body := b.buildStmtList(ifStmt.ThenStatement)

	var elseBody []GoStmt
	if ifStmt.ElseStatement != nil {
		if ifStmt.ElseStatement.Kind == ast.KindIfStatement {
			elseBody = []GoStmt{b.buildIfStmt(ifStmt.ElseStatement)}
		} else {
			elseBody = b.buildStmtList(ifStmt.ElseStatement)
		}
	}

	return &IRIf{Cond: cond, Body: body, Else: elseBody}
}

func (b *IRBuilder) buildForStmt(node *ast.Node) GoStmt {
	forStmt := node.AsForStatement()
	var init GoStmt
	var preStmts []GoStmt // declarations hoisted before the for loop
	if forStmt.Initializer != nil {
		if forStmt.Initializer.Kind == ast.KindVariableDeclarationList {
			initStmt := b.buildVarDeclList(forStmt.Initializer)
			// Go for-init supports only one simple statement.
			// Hoist extra declarations from comma-separated initializers.
			if blk, ok := initStmt.(*IRBlock); ok && len(blk.Stmts) > 1 {
				init = blk.Stmts[0]
				preStmts = blk.Stmts[1:]
			} else {
				init = initStmt
			}
		} else {
			init = &IRExprStmt{Expr: b.BuildExpr(forStmt.Initializer)}
		}
	}
	var cond GoExpr
	if forStmt.Condition != nil {
		cond = b.BuildExpr(forStmt.Condition)
	}
	var post GoStmt
	if forStmt.Incrementor != nil {
		post = &IRExprStmt{Expr: b.BuildExpr(forStmt.Incrementor)}
	}
	body := b.buildStmtList(forStmt.Statement)
	forStmtIR := &IRFor{Init: init, Cond: cond, Post: post, Body: body}
	if len(preStmts) > 0 {
		return &IRBlock{Stmts: append(preStmts, forStmtIR)}
	}
	return forStmtIR
}

func (b *IRBuilder) buildForInOfStmt(node *ast.Node) GoStmt {
	fio := node.AsForInOrOfStatement()
	isAwait := fio.AwaitModifier != nil

	key := "_"
	value := ""
	var varName string
	if fio.Initializer != nil && fio.Initializer.Kind == ast.KindVariableDeclarationList {
		declList := fio.Initializer.AsVariableDeclarationList()
		if declList.Declarations != nil && len(declList.Declarations.Nodes) > 0 {
			name := declList.Declarations.Nodes[0].Name()
			if name != nil && name.Kind == ast.KindIdentifier {
				varName = goVarName(name.AsIdentifier().Text)
				if node.Kind == ast.KindForInStatement {
					key = varName
				} else if !isAwait {
					value = varName
				}
			}
		}
	}

	over := b.BuildExpr(fio.Expression)

	if isAwait && varName != "" {
		// for await (const x of promises) → for _, __p := range promises { x := __p.Await(); ... }
		b.addImport("github.com/i2y/ramune/jsrt", "")
		innerBody := b.buildStmtList(fio.Statement)

		// Prepend: varName := func() any { __v, __err := __p.Await(); if __err != nil { jsrt.Throw(__err) }; return __v }()
		awaitStmt := &IRVarDecl{
			Name: varName,
			Typ:  GoTypeInfo{GoStr: "any"},
			Init: &IRRawExpr{
				exprBase: exprBase{},
				Code:     `func() any { __v, __err := __p.Await(); if __err != nil { jsrt.Throw(__err) }; return __v }()`,
			},
			UseShort: true,
		}

		body := make([]GoStmt, 0, 1+len(innerBody))
		body = append(body, awaitStmt)
		body = append(body, innerBody...)
		return &IRRange{Key: "_", Value: "__p", Over: over, Body: body}
	}

	body := b.buildStmtList(fio.Statement)
	return &IRRange{Key: key, Value: value, Over: over, Body: body}
}

func (b *IRBuilder) buildWhileStmt(node *ast.Node) GoStmt {
	ws := node.AsWhileStatement()

	// while(i--) / while(i++) — postfix decrement/increment as condition.
	// Go's i-- is a statement, not an expression. Transform to:
	//   for i > 0 { i--; body }  (for --)
	//   for { i++; if i == 0 { break }; body }  (for ++)
	if ws.Expression.Kind == ast.KindPostfixUnaryExpression {
		postfix := ws.Expression.AsPostfixUnaryExpression()
		if postfix.Operator == ast.KindMinusMinusToken {
			operand := b.BuildExpr(postfix.Operand)
			body := b.buildStmtList(ws.Statement)
			decr := &IRExprStmt{Expr: &IRUnaryOp{
				exprBase: exprBase{Typ: operand.ExprType()},
				Op:       "--",
				Operand:  operand,
				Postfix:  true,
			}}
			cond := &IRBinaryOp{
				exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}},
				Op:       ">",
				Left:     operand,
				Right:    irFloat64("0"),
			}
			return &IRFor{Cond: cond, Body: append([]GoStmt{decr}, body...)}
		}
	}

	cond := b.BuildExpr(ws.Expression)
	body := b.buildStmtList(ws.Statement)
	return &IRFor{Cond: cond, Body: body}
}

func (b *IRBuilder) buildDoWhileStmt(node *ast.Node) GoStmt {
	ds := node.AsDoStatement()
	body := b.buildStmtList(ds.Statement)
	cond := b.BuildExpr(ds.Expression)
	// do { body } while (cond) → for { body; if !cond { break } }
	body = append(body, &IRIf{
		Cond: &IRUnaryOp{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}}, Op: "!", Operand: cond},
		Body: []GoStmt{&IRBreak{}},
	})
	return &IRFor{Body: body}
}

func (b *IRBuilder) buildSwitchStmt(node *ast.Node) GoStmt {
	sw := node.AsSwitchStatement()
	tag := b.BuildExpr(sw.Expression)
	var cases []IRCase
	if sw.CaseBlock != nil {
		caseBlock := sw.CaseBlock.AsCaseBlock()
		if caseBlock.Clauses != nil {
			for _, clause := range caseBlock.Clauses.Nodes {
				cc := clause.AsCaseOrDefaultClause()
				var exprs []GoExpr
				if cc.Expression != nil {
					exprs = []GoExpr{b.BuildExpr(cc.Expression)}
				}
				var body []GoStmt
				if cc.Statements != nil {
					for _, stmt := range cc.Statements.Nodes {
						// Skip break statements — Go switches don't fall through
						if stmt.Kind == ast.KindBreakStatement {
							continue
						}
						s := b.BuildStmt(stmt)
						if s != nil {
							body = append(body, s)
						}
					}
				}
				cases = append(cases, IRCase{Exprs: exprs, Body: body})
			}
		}
	}
	return &IRSwitch{Tag: tag, Cases: cases}
}

func (b *IRBuilder) buildTryStmt(node *ast.Node) GoStmt {
	tryStmt := node.AsTryStatement()
	tryBody := b.buildStmtList(tryStmt.TryBlock)

	var catchVar string
	var catchBody []GoStmt
	if tryStmt.CatchClause != nil {
		cc := tryStmt.CatchClause.AsCatchClause()
		if cc.VariableDeclaration != nil {
			catchVar = goVarName(nodeText(cc.VariableDeclaration.Name()))
		}
		if cc.Block != nil {
			catchBody = b.buildStmtList(cc.Block)
		}
	}

	var finallyBody []GoStmt
	if tryStmt.FinallyBlock != nil {
		finallyBody = b.buildStmtList(tryStmt.FinallyBlock)
	}

	return &IRTryCatch{
		TryBody:     tryBody,
		CatchVar:    catchVar,
		CatchBody:   catchBody,
		FinallyBody: finallyBody,
	}
}

func (b *IRBuilder) buildBlock(node *ast.Node) GoStmt {
	block := node.AsBlock()
	var stmts []GoStmt
	if block.Statements != nil {
		for _, stmt := range block.Statements.Nodes {
			s := b.BuildStmt(stmt)
			if s != nil {
				stmts = append(stmts, s)
			}
		}
	}
	return &IRBlock{Stmts: stmts}
}

func (b *IRBuilder) buildStmtList(node *ast.Node) []GoStmt {
	if node == nil {
		return nil
	}
	if node.Kind == ast.KindBlock {
		block := node.AsBlock()
		var stmts []GoStmt
		if block.Statements != nil {
			for _, stmt := range block.Statements.Nodes {
				s := b.BuildStmt(stmt)
				if s != nil {
					stmts = append(stmts, s)
				}
			}
		}
		return stmts
	}
	s := b.BuildStmt(node)
	if s != nil {
		return []GoStmt{s}
	}
	return nil
}

func (b *IRBuilder) buildVariableStmt(node *ast.Node) GoStmt {
	varStmt := node.AsVariableStatement()
	if varStmt.DeclarationList == nil {
		return nil
	}
	return b.buildVarDeclList(varStmt.DeclarationList)
}

func (b *IRBuilder) buildVarDeclList(node *ast.Node) GoStmt {
	declList := node.AsVariableDeclarationList()
	if declList.Declarations == nil {
		return nil
	}

	var stmts []GoStmt
	for _, decl := range declList.Declarations.Nodes {
		d := decl.AsVariableDeclaration()
		nameNode := d.Name()

		// Object destructuring: const { a, b } = expr
		if nameNode != nil && nameNode.Kind == ast.KindObjectBindingPattern {
			stmts = append(stmts, b.buildObjectDestructuring(nameNode, d.Initializer)...)
			continue
		}
		// Array destructuring: const [a, b] = expr
		if nameNode != nil && nameNode.Kind == ast.KindArrayBindingPattern {
			stmts = append(stmts, b.buildArrayDestructuring(nameNode, d.Initializer)...)
			continue
		}

		name := nodeText(nameNode)
		goName := goVarName(name)
		typ := b.getGoType(decl)

		var init GoExpr
		if d.Initializer != nil {
			savedCtx := b.declContext
			b.declContext = typ.GoStr
			init = b.BuildExpr(d.Initializer)
			b.declContext = savedCtx
		}

		b.trackVar(goName, typ)
		stmts = append(stmts, &IRVarDecl{
			Name:     goName,
			Typ:      typ,
			Init:     init,
			UseShort: init != nil,
		})
	}

	if len(stmts) == 1 {
		return stmts[0]
	}
	return &IRBlock{Stmts: stmts, Bare: true}
}

// --------------------------------------------------------------------
// Destructuring
// --------------------------------------------------------------------

// buildObjectDestructuring handles: const { a = 1, b = 2 } = expr
func (b *IRBuilder) buildObjectDestructuring(pattern *ast.Node, initializer *ast.Node) []GoStmt {
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil || initializer == nil {
		return nil
	}

	initExpr := b.BuildExpr(initializer)
	initType := b.getGoType(initializer)
	initIsAny := initType.IsAny()

	tmpVar := "__obj"
	var stmts []GoStmt
	stmts = append(stmts, &IRVarDecl{
		Name: tmpVar, Typ: initType, Init: initExpr, UseShort: true,
	})

	var namedKeys []string // track extracted keys for rest element
	for _, elem := range bp.Elements.Nodes {
		be := elem.AsBindingElement()
		elemName := elem.Name()
		if elemName == nil || elemName.Kind != ast.KindIdentifier {
			continue
		}
		localName := goVarName(elemName.AsIdentifier().Text)

		propName := localName
		if be.PropertyName != nil && be.PropertyName.Kind == ast.KindIdentifier {
			propName = be.PropertyName.AsIdentifier().Text
		}

		// Rest element: const { a, ...rest } = obj
		if be.DotDotDotToken != nil {
			b.addImport("github.com/i2y/ramune/jsrt", "")
			var keyArgs []GoExpr
			keyArgs = append(keyArgs, &IRIdent{Name: tmpVar})
			for _, k := range namedKeys {
				keyArgs = append(keyArgs, irString(fmt.Sprintf("%q", k)))
			}
			stmts = append(stmts, &IRVarDecl{
				Name: localName, Typ: GoTypeInfo{GoStr: "any"},
				Init:     &IRStdlibCall{exprBase: exprBase{Typ: GoTypeInfo{GoStr: "any"}}, Package: "jsrt", Func: "OmitKeys", Args: keyArgs},
				UseShort: true,
			})
			continue
		}

		namedKeys = append(namedKeys, goExportedName(propName))

		var access GoExpr
		if initIsAny {
			b.addImport("github.com/i2y/ramune/jsrt", "")
			access = &IRStdlibCall{
				exprBase: exprBase{Typ: GoTypeInfo{GoStr: "any"}},
				Package:  "jsrt", Func: "GetField",
				Args: []GoExpr{
					&IRIdent{exprBase: exprBase{}, Name: tmpVar},
					irString(fmt.Sprintf("%q", goExportedName(propName))),
				},
			}
		} else {
			access = &IRFieldAccess{
				exprBase: exprBase{},
				Object:   &IRIdent{exprBase: exprBase{}, Name: tmpVar},
				Field:    goExportedName(propName),
			}
		}

		stmts = append(stmts, &IRVarDecl{
			Name: localName, Typ: GoTypeInfo{GoStr: "any"}, Init: access, UseShort: true,
		})

		// Default value: const { a = 1 } = obj → if a == zero { a = 1 }
		if be.Initializer != nil {
			defaultVal := b.BuildExpr(be.Initializer)
			stmts = append(stmts, b.buildDestructuringDefault(localName, defaultVal, initIsAny)...)
		}
	}
	return stmts
}

// buildArrayDestructuring handles: const [a = 10, b = 20] = expr
func (b *IRBuilder) buildArrayDestructuring(pattern *ast.Node, initializer *ast.Node) []GoStmt {
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil || initializer == nil {
		return nil
	}

	initExpr := b.BuildExpr(initializer)
	initType := b.getGoType(initializer)

	tmpVar := "__arr"
	var stmts []GoStmt
	stmts = append(stmts, &IRVarDecl{
		Name: tmpVar, Typ: initType, Init: initExpr, UseShort: true,
	})

	for i, elem := range bp.Elements.Nodes {
		if elem.Kind == ast.KindOmittedExpression {
			continue
		}
		be := elem.AsBindingElement()
		elemName := elem.Name()
		if elemName == nil || elemName.Kind != ast.KindIdentifier {
			continue
		}
		localName := goVarName(elemName.AsIdentifier().Text)

		access := &IRIndexAccess{
			exprBase: exprBase{Typ: GoTypeInfo{GoStr: "any"}},
			Object:   &IRIdent{exprBase: exprBase{}, Name: tmpVar},
			Index:    irFloat64(fmt.Sprintf("%d", i)),
		}

		stmts = append(stmts, &IRVarDecl{
			Name: localName, Typ: GoTypeInfo{GoStr: "any"}, Init: access, UseShort: true,
		})

		if be.Initializer != nil {
			defaultVal := b.BuildExpr(be.Initializer)
			stmts = append(stmts, b.buildDestructuringDefault(localName, defaultVal, true)...)
		}
	}
	return stmts
}

// buildDestructuringDefault generates: if localName == nil { localName = defaultVal }
// JS defaults trigger on undefined (nil in Go), so nil check is always correct.
func (b *IRBuilder) buildDestructuringDefault(localName string, defaultVal GoExpr, _ bool) []GoStmt {
	return []GoStmt{&IRIf{
		Cond: &IRBinaryOp{
			exprBase: exprBase{Typ: GoTypeInfo{GoStr: "bool"}},
			Op:       "==",
			Left:     &IRIdent{exprBase: exprBase{}, Name: localName},
			Right:    irNil(),
		},
		Body: []GoStmt{&IRAssign{
			Targets: []GoExpr{&IRIdent{exprBase: exprBase{}, Name: localName}},
			Op:      "=",
			Values:  []GoExpr{defaultVal},
		}},
	}}
}

// --------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------

func (b *IRBuilder) buildArgList(args *ast.NodeList) []GoExpr {
	if args == nil {
		return nil
	}
	var result []GoExpr
	for _, arg := range args.Nodes {
		if arg.Kind == ast.KindSpreadElement {
			spread := arg.AsSpreadElement()
			inner := b.BuildExpr(spread.Expression)
			// Mark as spread via a special wrapper
			result = append(result, &IRUnaryOp{
				exprBase: exprBase{Typ: inner.ExprType()},
				Op:       "...",
				Operand:  inner,
				Postfix:  true,
			})
		} else {
			result = append(result, b.BuildExpr(arg))
		}
	}
	return result
}

func (b *IRBuilder) buildParamList(node *ast.Node) []IRParam {
	params := node.Parameters()
	if params == nil {
		return nil
	}
	var result []IRParam
	for _, param := range params {
		p := param.AsParameterDeclaration()
		name := nodeText(p.Name())
		typ := b.getGoType(param.AsNode())
		isRest := p.DotDotDotToken != nil
		result = append(result, IRParam{
			Name:   goParamName(name),
			Typ:    typ,
			IsRest: isRest,
		})
	}
	return result
}

// --- Convenience constructors ---

func irNil() GoExpr {
	return &IRLiteral{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}}, Value: "nil"}
}

func irFloat64(v string) GoExpr {
	return &IRLiteral{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "float64"}}, Value: v}
}

func irString(v string) GoExpr {
	return &IRLiteral{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "string"}}, Value: v}
}

func irBool(v string) GoExpr {
	return &IRLiteral{exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypePrimitive, GoStr: "bool"}}, Value: v}
}

func irLiteral(v string, goType string) GoExpr {
	return &IRLiteral{exprBase: exprBase{Typ: goTypeInfoFromString(goType)}, Value: v}
}

// irExprPlaceholder returns a placeholder string for an IR expression.
// Used when building raw Go code strings that reference IR sub-expressions.
// The emitter will resolve these.
func irExprPlaceholder(expr GoExpr) string {
	switch e := expr.(type) {
	case *IRIdent:
		if e.PkgName != "" {
			return e.PkgName + "." + e.Name
		}
		return e.Name
	case *IRLiteral:
		return e.Value
	case *IRRawExpr:
		return e.Code
	default:
		return fmt.Sprintf("__ir_%p", expr)
	}
}

// tsOpToGoOp maps TypeScript binary operator tokens to Go operator strings.
func tsOpToGoOp(op ast.Kind) string {
	switch op {
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
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
		return "=="
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
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
	case ast.KindAmpersandEqualsToken:
		return "&="
	case ast.KindBarEqualsToken:
		return "|="
	case ast.KindCaretEqualsToken:
		return "^="
	case ast.KindLessThanLessThanEqualsToken:
		return "<<="
	case ast.KindGreaterThanGreaterThanEqualsToken:
		return ">>="
	default:
		return ""
	}
}

func escapeFormatString(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

// --------------------------------------------------------------------
// Array method callback typing
// --------------------------------------------------------------------

// buildArrayMethodCall builds an IRArrayMethodCall with properly typed callback parameters.
func (b *IRBuilder) buildArrayMethodCall(call *ast.CallExpression, objType GoTypeInfo, method string, resultType GoTypeInfo) GoExpr {
	prop := call.Expression.AsPropertyAccessExpression()
	arrayExpr := b.BuildExpr(prop.Expression)
	elemType := objType.ElemType
	if elemType == "" {
		elemType = "any"
	}

	cbParamTypes, cbRetType := b.arrayMethodSignature(method, elemType, call)

	var callback GoExpr
	var extraArgs []GoExpr

	args := call.Arguments
	if args != nil && len(args.Nodes) > 0 {
		cbNode := args.Nodes[0]
		if cbNode.Kind == ast.KindArrowFunction || cbNode.Kind == ast.KindFunctionExpression {
			callback = b.buildTypedCallbackFunc(cbNode, cbParamTypes, cbRetType)
		} else {
			callback = b.BuildExpr(cbNode)
		}
		// Extra args (e.g., initial value for reduce)
		for i := 1; i < len(args.Nodes); i++ {
			extraArgs = append(extraArgs, b.BuildExpr(args.Nodes[i]))
		}
	}

	return &IRArrayMethodCall{
		exprBase:   exprBase{Typ: resultType},
		HelperFunc: goExportedName(method),
		Array:      arrayExpr,
		Callback:   callback,
		ExtraArgs:  extraArgs,
		ElemType:   elemType,
	}
}

// arrayMethodSignature returns callback parameter types and return type for an array method.
func (b *IRBuilder) arrayMethodSignature(method, elemType string, call *ast.CallExpression) (cbParams []GoTypeInfo, cbRetType GoTypeInfo) {
	elem := goTypeInfoFromString(elemType)
	intType := goTypeInfoFromString("int")
	cbParams = []GoTypeInfo{elem, intType}

	switch method {
	case "map":
		cbRetType = goTypeInfoFromString(b.inferCallResultElemType(call))
	case "flatMap":
		cbRetType = goTypeInfoFromString("[]" + b.inferCallResultElemType(call))
	case "filter", "find", "findIndex", "some", "every":
		cbRetType = goTypeInfoFromString("bool")
	case "reduce":
		accType := b.inferCallResultType(call)
		cbParams = []GoTypeInfo{goTypeInfoFromString(accType), elem, intType}
		cbRetType = goTypeInfoFromString(accType)
	}
	return
}

// buildTypedCallbackFunc builds a function literal with overridden parameter types.
func (b *IRBuilder) buildTypedCallbackFunc(node *ast.Node, paramTypes []GoTypeInfo, retType GoTypeInfo) GoExpr {
	params := node.Parameters()
	var irParams []IRParam

	nUserParams := 0
	if params != nil {
		nUserParams = len(params)
	}

	// Build params: override types from context, limit to what the callback expects
	maxParams := len(paramTypes)
	if nUserParams < maxParams {
		maxParams = nUserParams
	}
	for i := 0; i < maxParams; i++ {
		p := params[i].AsParameterDeclaration()
		name := goParamName(nodeText(p.Name()))
		irParams = append(irParams, IRParam{Name: name, Typ: paramTypes[i]})
	}
	// Pad unused params (e.g., callback takes 1 param but Go expects 2)
	for i := nUserParams; i < len(paramTypes); i++ {
		irParams = append(irParams, IRParam{Name: "_", Typ: paramTypes[i]})
	}

	// Track callback param types for body resolution
	savedVarTypes := make(map[string]GoTypeInfo)
	for _, p := range irParams {
		if p.Name != "_" {
			if old, ok := b.varTypes[p.Name]; ok {
				savedVarTypes[p.Name] = old
			}
			b.trackVar(p.Name, p.Typ)
		}
	}

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)
	savedAsync := b.inAsyncBody
	savedRetType := b.currentRetType
	b.inAsyncBody = isAsync
	b.currentRetType = retType.GoStr

	var body []GoStmt
	if node.Kind == ast.KindArrowFunction {
		arrow := node.AsArrowFunction()
		if arrow.Body.Kind == ast.KindBlock {
			body = b.buildStmtList(arrow.Body)
		} else {
			body = []GoStmt{&IRReturn{Values: []GoExpr{b.BuildExpr(arrow.Body)}}}
		}
	} else {
		body = b.buildStmtList(node.Body())
	}

	b.inAsyncBody = savedAsync
	b.currentRetType = savedRetType

	// Restore previous var types
	for _, p := range irParams {
		if p.Name != "_" {
			if old, ok := savedVarTypes[p.Name]; ok {
				b.trackVar(p.Name, old)
			} else {
				delete(b.varTypes, p.Name)
			}
		}
	}

	return &IRFuncLit{
		exprBase: exprBase{Typ: GoTypeInfo{Category: GoTypeFunc, GoStr: "func"}},
		Params:   irParams,
		RetType:  retType,
		Body:     body,
		IsAsync:  isAsync,
	}
}

// inferCallResultElemType infers the element type of an array-returning call expression.
func (b *IRBuilder) inferCallResultElemType(call *ast.CallExpression) string {
	if b.ck == nil {
		return "any"
	}
	resultType := b.ck.GetTypeAtLocation(call.AsNode())
	if resultType == nil {
		return "any"
	}
	info := b.tm.goTypeInfo(resultType)
	if info.IsSlice() && info.ElemType != "" {
		return info.ElemType
	}
	return "any"
}

// inferCallResultType infers the Go type string of a call expression's result.
func (b *IRBuilder) inferCallResultType(call *ast.CallExpression) string {
	if b.ck == nil {
		return "any"
	}
	resultType := b.ck.GetTypeAtLocation(call.AsNode())
	if resultType == nil {
		return "any"
	}
	goStr := b.tm.goType(resultType)
	if goStr == "" {
		return "any"
	}
	return goStr
}
