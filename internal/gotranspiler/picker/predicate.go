package picker

import (
	"fmt"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// IsFunctionExtractable classifies a FunctionDeclaration node.
//
// topLevelFuncs is the set of top-level function names in the same file,
// letting the body walker recognize self- and peer-calls instead of flagging
// them as free identifiers.
//
// Accepts:
//   - non-generic, non-generator (async is fine if the body stays within the
//     allowlist — sync-resolving Promise<T> returns are handled by the
//     emitter)
//   - every parameter is primitive / T[] / named interface / JSFunc callback
//   - return type is primitive, T[], void, or Promise<primitive>
//   - body uses only the allowlisted AST nodes (walked below)
//   - no closure capture beyond params/locals and same-file functions
//   - no parameter mutation
//   - calls only the built-in safelist (Math.*, Number.*, string/array
//     methods, same-file extractable functions, JSFunc params at call head)
func IsFunctionExtractable(node *ast.Node, ck *checker.Checker, topLevelFuncs map[string]struct{}) (bool, Reason) {
	if node == nil || node.Kind != ast.KindFunctionDeclaration {
		return false, Reason{Code: reasonUnhandledKind, Detail: "not a function declaration"}
	}
	fd := node.AsFunctionDeclaration()
	if fd == nil {
		return false, Reason{Code: reasonUnhandledKind, Detail: "nil function declaration"}
	}

	if fd.Name() == nil {
		return false, Reason{Code: reasonUnnamed, Detail: "function has no name"}
	}
	if fd.TypeParameters != nil && len(fd.TypeParameters.Nodes) > 0 {
		return false, Reason{Code: reasonGenericFunc, Detail: "function has type parameters"}
	}
	if fd.AsteriskToken != nil {
		return false, Reason{Code: reasonGeneratorFunc, Detail: "generator function"}
	}
	// `async` itself is fine - the body walker still rejects `await` and
	// `yield`, so what's left is sync-resolving Promise<T> functions like
	// `async function add(a, b): Promise<number> { return a + b }`. The
	// emitter wraps the body in promise.New[T].
	if fd.Body == nil {
		return false, Reason{Code: reasonMissingBody, Detail: "ambient/overload declaration"}
	}

	paramNames, jsFuncParams, reason := checkCallableSignature(ck, node, fd.Parameters, "")
	if reason != nil {
		return false, *reason
	}

	ctx := &bodyCtx{
		ck:               ck,
		paramNames:       paramNames,
		jsFuncParamNames: jsFuncParams,
		topLevelFuncs:    topLevelFuncs,
		localNames:       map[string]bool{},
		inAsync:          ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync),
	}
	if r := checkBody(fd.Body, ctx); r != nil {
		return false, *r
	}

	return true, Reason{}
}

// checkCallableSignature validates a FunctionDeclaration / MethodDeclaration /
// ConstructorDeclaration's call signature: no rest, no default/optional
// parameters, every parameter type and the return type must be extractable.
// Returns (paramNames, jsFuncParamNames) — the latter is the subset of
// paramNames whose TS type is a callable accepted by isJSFuncParamType.
//
// label is prefixed to error details so callers can distinguish a free
// function ("") from a method ("method `foo` ").
//
// Rationale for the per-parameter Initializer/QuestionToken guard: default
// and optional parameters silently widen the JS-callable arity without the
// emitter generating any compensating Go logic. JS callers invoking f()
// against an extracted `func F(x float64)` get a missing-arg error from the
// bridge. Reject up front rather than ship a latent bug.
func checkCallableSignature(ck *checker.Checker, node *ast.Node, params *ast.ParameterList, label string) (map[string]bool, map[string]bool, *Reason) {
	fnType := ck.GetTypeAtLocation(node)
	if fnType == nil {
		return nil, nil, &Reason{Code: reasonEmptyReturn, Detail: label + "type checker returned nil"}
	}
	sigs := ck.GetSignaturesOfType(fnType, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return nil, nil, &Reason{Code: reasonEmptyReturn, Detail: label + "no call signature"}
	}
	sig := sigs[0]
	if ck.HasEffectiveRestParameter(sig) {
		return nil, nil, &Reason{Code: reasonRestParam, Detail: label + "rest parameter not supported"}
	}
	if params != nil {
		for _, p := range params.Nodes {
			pd := p.AsParameterDeclaration()
			if pd == nil {
				continue
			}
			if pd.Initializer != nil {
				// Default-valued params widen JS-callable arity without
				// a corresponding Go default — emitting `f(a, b)` silently
				// changes runtime semantics. Suggest the body-side
				// alternative the picker can already extract.
				return nil, nil, &Reason{Code: reasonUnhandledKind, Detail: label + "default parameter value not supported (use `b ?? <default>` in the body with a nullable param instead)"}
			}
			// `b?: T` is accepted: tsgo widens the symbol's type to `T |
			// undefined`, which lowers via typemapper.go's nullable path
			// to `*T`. JS callers passing `undefined` (or omitting the
			// arg) bridge to a Go `nil` *T — the body walker dereferences
			// where the program actually reads `b`, and `b ?? default` /
			// `b === undefined` paths use the existing nullable emit.
		}
	}
	paramNames := map[string]bool{}
	jsFuncParams := map[string]bool{}
	for i, paramSym := range sig.Parameters() {
		if paramSym == nil {
			continue
		}
		paramNames[paramSym.Name] = true
		pt := ck.GetTypeOfSymbol(paramSym)
		if r := isExtractableType(ck, pt); r != nil {
			// Callable params fall back to the *ramune.JSFunc bridge; the
			// walker enforces call-head-only use via jsFuncParamNames.
			if jr := isJSFuncParamType(ck, pt); jr == nil {
				jsFuncParams[paramSym.Name] = true
				continue
			}
			return nil, nil, &Reason{Code: r.Code, Detail: fmt.Sprintf("%sparam %d (%s): %s", label, i, paramSym.Name, r.Detail)}
		}
	}
	if r := isExtractableReturnType(ck, ck.GetReturnTypeOfSignature(sig)); r != nil {
		return nil, nil, &Reason{Code: r.Code, Detail: label + "return: " + r.Detail}
	}
	return paramNames, jsFuncParams, nil
}

// bodyCtx carries state threaded through the body walker.
type bodyCtx struct {
	ck            *checker.Checker
	paramNames    map[string]bool     // function parameters - readable, not writable
	topLevelFuncs map[string]struct{} // same-file top-level functions - callable
	localNames    map[string]bool     // locals declared in this body (let/const/var) - readable AND writable
	inAsync       bool                // function is `async` - permits `await` on Promise<T>
	// jsFuncParamNames is the subset of paramNames whose declared TS type is
	// a callable accepted by isJSFuncParamType. The emitter lowers these to
	// `*ramune.JSFunc`. The walker allows a reference only in call-head
	// position (checkBareCallee); other uses (value operand, assignment RHS,
	// argument to another call) would require materialising a JS function
	// back into a new *JSFunc, which the bridge doesn't support.
	jsFuncParamNames map[string]bool
	// Class-method / constructor context. When inMethod is true, `this` is
	// permitted and `this.<field>` / `this.<method>` resolve against these
	// maps. Constructor bodies enforce the `this.<field> = <expr>` shape
	// syntactically (checkConstructorStatement), so no separate flag needed.
	inMethod    bool
	thisFields  map[string]bool
	thisMethods map[string]bool
}

// checkBody walks a block/statement subtree and returns a non-nil Reason if
// any node violates the allowlist. It mutates ctx.localNames as it
// encounters VariableDeclarations.
func checkBody(node *ast.Node, ctx *bodyCtx) *Reason {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case ast.KindBlock:
		blk := node.AsBlock()
		if blk == nil || blk.Statements == nil {
			return nil
		}
		for _, s := range blk.Statements.Nodes {
			if r := checkBody(s, ctx); r != nil {
				return r
			}
		}
		return nil

	case ast.KindVariableStatement:
		vs := node.AsVariableStatement()
		if vs == nil || vs.DeclarationList == nil {
			return nil
		}
		return checkBody(vs.DeclarationList, ctx)

	case ast.KindVariableDeclarationList:
		// Used by `for (let i = 0; ...)` initializers and plain var statements.
		decls := node.AsVariableDeclarationList()
		if decls == nil || decls.Declarations == nil {
			return nil
		}
		for _, d := range decls.Declarations.Nodes {
			if r := checkVarDecl(d, ctx); r != nil {
				return r
			}
		}
		return nil

	case ast.KindExpressionStatement:
		es := node.AsExpressionStatement()
		if es == nil {
			return nil
		}
		return checkExpr(es.Expression, ctx)

	case ast.KindReturnStatement:
		rs := node.AsReturnStatement()
		if rs == nil || rs.Expression == nil {
			return nil
		}
		return checkExpr(rs.Expression, ctx)

	case ast.KindIfStatement:
		is := node.AsIfStatement()
		if is == nil {
			return nil
		}
		if r := requireBoolCondition(is.Expression, ctx); r != nil {
			return r
		}
		if r := checkBody(is.ThenStatement, ctx); r != nil {
			return r
		}
		if is.ElseStatement != nil {
			if r := checkBody(is.ElseStatement, ctx); r != nil {
				return r
			}
		}
		return nil

	case ast.KindForStatement:
		fs := node.AsForStatement()
		if fs == nil {
			return nil
		}
		if fs.Initializer != nil {
			if r := checkBody(fs.Initializer, ctx); r != nil {
				return r
			}
		}
		if fs.Condition != nil {
			if r := requireBoolCondition(fs.Condition, ctx); r != nil {
				return r
			}
		}
		if fs.Incrementor != nil {
			if r := checkExpr(fs.Incrementor, ctx); r != nil {
				return r
			}
		}
		return checkBody(fs.Statement, ctx)

	case ast.KindWhileStatement:
		ws := node.AsWhileStatement()
		if ws == nil {
			return nil
		}
		if r := requireBoolCondition(ws.Expression, ctx); r != nil {
			return r
		}
		return checkBody(ws.Statement, ctx)

	case ast.KindDoStatement:
		ds := node.AsDoStatement()
		if ds == nil {
			return nil
		}
		if r := checkBody(ds.Statement, ctx); r != nil {
			return r
		}
		return requireBoolCondition(ds.Expression, ctx)

	case ast.KindBreakStatement, ast.KindContinueStatement, ast.KindEmptyStatement:
		if node.AsBreakStatement() != nil && node.AsBreakStatement().Label != nil {
			return &Reason{Code: reasonLabeledStmt, Detail: "labeled break"}
		}
		if node.AsContinueStatement() != nil && node.AsContinueStatement().Label != nil {
			return &Reason{Code: reasonLabeledStmt, Detail: "labeled continue"}
		}
		return nil

	case ast.KindVariableDeclaration:
		return checkVarDecl(node, ctx)

	case ast.KindLabeledStatement:
		return &Reason{Code: reasonLabeledStmt, Detail: "labeled statement"}
	case ast.KindTryStatement:
		return &Reason{Code: reasonTry, Detail: "try/catch not supported"}
	case ast.KindThrowStatement:
		return &Reason{Code: reasonThrow, Detail: "throw not supported"}
	case ast.KindSwitchStatement:
		return checkSwitchStatement(node, ctx)
	case ast.KindForOfStatement:
		return checkForOfStatement(node, ctx)
	case ast.KindForInStatement,
		ast.KindWithStatement, ast.KindDebuggerStatement:
		return &Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("statement kind %v not supported", node.Kind)}
	}
	return checkExpr(node, ctx)
}

func checkVarDecl(node *ast.Node, ctx *bodyCtx) *Reason {
	vd := node.AsVariableDeclaration()
	if vd == nil || vd.Name() == nil {
		return nil
	}
	if vd.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonUnhandledKind, Detail: "destructuring binding not supported"}
	}
	name := vd.Name().AsIdentifier().Text
	// Register as local even if the initializer fails - the whole function is
	// rejected anyway, and this keeps the walker from misclassifying later refs
	// as captures.
	ctx.localNames[name] = true

	if ctx.ck != nil {
		if t := ctx.ck.GetTypeAtLocation(vd.Name()); t != nil {
			if r := isExtractableType(ctx.ck, t); r != nil {
				return &Reason{Code: r.Code, Detail: "local `" + name + "`: " + r.Detail}
			}
		}
	}
	if vd.Initializer != nil {
		return checkExpr(vd.Initializer, ctx)
	}
	return nil
}

// checkExpr classifies an expression node. Returns nil on accept, a Reason on
// reject.
func checkExpr(node *ast.Node, ctx *bodyCtx) *Reason {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return nil

	case ast.KindIdentifier:
		return checkIdentifierRef(node, ctx)

	case ast.KindThisKeyword:
		if ctx.inMethod {
			return nil
		}
		return &Reason{Code: reasonThis, Detail: "`this` not allowed outside methods"}

	case ast.KindParenthesizedExpression:
		return checkExpr(node.AsParenthesizedExpression().Expression, ctx)

	case ast.KindAsExpression:
		// `x as any` would widen the inner expression to `any` invisibly to
		// the rest of the walker, then the emitter forces a `.(float64)` (or
		// similar) type assertion that fails on the static-typed operand.
		// Delegate to isExtractableType so we also catch as-bigint/as-union/
		// as-symbol with their precise reason codes.
		ae := node.AsAsExpression()
		if ctx.ck != nil && ae.Type != nil {
			if r := isExtractableType(ctx.ck, ctx.ck.GetTypeAtLocation(ae.Type)); r != nil {
				return &Reason{Code: r.Code, Detail: "as-cast target: " + r.Detail}
			}
		}
		return checkExpr(ae.Expression, ctx)

	case ast.KindNonNullExpression:
		return checkExpr(node.AsNonNullExpression().Expression, ctx)

	case ast.KindPrefixUnaryExpression:
		pu := node.AsPrefixUnaryExpression()
		switch pu.Operator {
		case ast.KindExclamationToken:
			// Same reason as logical && / ||: emitted Go's `!` requires bool.
			return requireBoolOperand(pu.Operand, ctx, "logical not")
		case ast.KindTildeToken:
			if isNumberLikeNode(ctx.ck, pu.Operand) {
				return &Reason{Code: reasonForbiddenOp, Detail: "bitwise NOT on number returns int in emitted Go"}
			}
			return checkExpr(pu.Operand, ctx)
		case ast.KindPlusToken, ast.KindMinusToken:
			// Emitter drops unary +/- so a non-numeric operand would leak through
			// (`return s` with a float64 return type). Require numeric operand.
			return checkExprWithType(pu.Operand, ctx, isNumberLikeType, Reason{Code: reasonObjectType, Detail: "unary +/- requires numeric operand"})
		case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
			if r := rejectParamMutation(pu.Operand, ctx); r != nil {
				return r
			}
			return checkExpr(pu.Operand, ctx)
		default:
			return &Reason{Code: reasonForbiddenOp, Detail: fmt.Sprintf("prefix op %v", pu.Operator)}
		}

	case ast.KindPostfixUnaryExpression:
		pu := node.AsPostfixUnaryExpression()
		switch pu.Operator {
		case ast.KindPlusPlusToken, ast.KindMinusMinusToken:
			if r := rejectParamMutation(pu.Operand, ctx); r != nil {
				return r
			}
			return checkExpr(pu.Operand, ctx)
		default:
			return &Reason{Code: reasonForbiddenOp, Detail: fmt.Sprintf("postfix op %v", pu.Operator)}
		}

	case ast.KindBinaryExpression:
		return checkBinaryExpr(node, ctx)

	case ast.KindConditionalExpression:
		ce := node.AsConditionalExpression()
		if r := requireBoolCondition(ce.Condition, ctx); r != nil {
			return r
		}
		if r := checkExpr(ce.WhenTrue, ctx); r != nil {
			return r
		}
		return checkExpr(ce.WhenFalse, ctx)

	case ast.KindCallExpression:
		return checkCallExpr(node, ctx)

	case ast.KindYieldExpression:
		return &Reason{Code: reasonYield, Detail: "yield"}
	case ast.KindAwaitExpression:
		return checkAwaitExpr(node, ctx)
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindClassExpression:
		return &Reason{Code: reasonFuncLiteral, Detail: "inline function/class literal"}
	case ast.KindRegularExpressionLiteral:
		return &Reason{Code: reasonRegex, Detail: "regex literal"}
	case ast.KindSpreadElement:
		return &Reason{Code: reasonSpread, Detail: "spread"}
	case ast.KindTemplateExpression:
		return checkTemplateExpression(node, ctx)
	case ast.KindTaggedTemplateExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "tagged template not supported"}
	case ast.KindPropertyAccessExpression:
		return checkPropertyAccess(node, ctx)
	case ast.KindElementAccessExpression:
		return checkElementAccess(node, ctx)
	case ast.KindNewExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "new expression not supported"}
	case ast.KindArrayLiteralExpression:
		return checkArrayLiteral(node, ctx)
	case ast.KindObjectLiteralExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "object literal not supported"}
	case ast.KindTypeOfExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "typeof not supported"}
	case ast.KindDeleteExpression, ast.KindVoidExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "delete/void expression"}
	}

	return &Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("expression kind %v not supported", node.Kind)}
}

// checkIdentifierRef rejects free identifiers: anything not a parameter, a
// local, or another extractable top-level function in the same file. A
// JSFunc-param identifier reached here is always a value-use (call-head
// position is routed through checkBareCallee, which short-circuits before
// this walker sees the identifier) — reject so the emitter doesn't have to
// synthesize a callable value out of a *ramune.JSFunc.
func checkIdentifierRef(node *ast.Node, ctx *bodyCtx) *Reason {
	name := node.AsIdentifier().Text
	if name == "" || name == "undefined" {
		return nil
	}
	if ctx.jsFuncParamNames[name] {
		return &Reason{Code: reasonDynamicCallee, Detail: "callback param `" + name + "` may only appear in call-head position"}
	}
	if ctx.paramNames[name] || ctx.localNames[name] {
		return nil
	}
	if _, ok := ctx.topLevelFuncs[name]; ok {
		return nil
	}
	return &Reason{Code: reasonClosureCapture, Detail: "free identifier `" + name + "`"}
}

// checkBinaryExpr validates operator + both operands, with assignment
// targeting param-mutation detection.
func checkBinaryExpr(node *ast.Node, ctx *bodyCtx) *Reason {
	be := node.AsBinaryExpression()
	op := be.OperatorToken.Kind
	switch op {
	case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindAsteriskAsteriskToken,
		ast.KindSlashToken, ast.KindPercentToken,
		ast.KindLessThanToken, ast.KindLessThanEqualsToken,
		ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken,
		ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken,
		ast.KindQuestionQuestionToken:
		// arithmetic, comparison, nullish coalescing - safe on primitives

	case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken:
		// JS short-circuit returns the last/first operand value; preserving
		// that under static typing requires both sides bool. Otherwise the
		// emitter would need jsrt.ToBool wrapping (ramune dep) or the Go
		// `&&`/`||` would be a type error on float64 / string operands.
		if r := requireBoolOperand(be.Left, ctx, "logical operator"); r != nil {
			return r
		}
		if r := requireBoolOperand(be.Right, ctx, "logical operator"); r != nil {
			return r
		}

	case ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
		ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken,
		ast.KindGreaterThanGreaterThanGreaterThanToken:
		// Bitwise ops produce `int` in the emitted Go (`int(n) & 1`), which
		// the surrounding float64 context cannot consume without a back-cast
		// the emitter doesn't currently insert. Pure-bool bitwise is fine
		// (rare in practice); reject the float case.
		if isNumberLikeNode(ctx.ck, be.Left) || isNumberLikeNode(ctx.ck, be.Right) {
			return &Reason{Code: reasonForbiddenOp, Detail: "bitwise op on number returns int in emitted Go but float64 was expected"}
		}

	case ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
		return &Reason{Code: reasonForbiddenOp, Detail: "== and != (use === / !==)"}

	case ast.KindEqualsToken,
		ast.KindPlusEqualsToken, ast.KindMinusEqualsToken,
		ast.KindAsteriskEqualsToken, ast.KindSlashEqualsToken, ast.KindPercentEqualsToken,
		ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken, ast.KindCaretEqualsToken,
		ast.KindLessThanLessThanEqualsToken, ast.KindGreaterThanGreaterThanEqualsToken,
		ast.KindGreaterThanGreaterThanGreaterThanEqualsToken,
		ast.KindAsteriskAsteriskEqualsToken,
		ast.KindAmpersandAmpersandEqualsToken, ast.KindBarBarEqualsToken, ast.KindQuestionQuestionEqualsToken:
		if r := rejectParamMutation(be.Left, ctx); r != nil {
			return r
		}

	case ast.KindInstanceOfKeyword, ast.KindInKeyword, ast.KindCommaToken:
		return &Reason{Code: reasonForbiddenOp, Detail: fmt.Sprintf("operator %v", op)}

	default:
		return &Reason{Code: reasonForbiddenOp, Detail: fmt.Sprintf("operator %v", op)}
	}
	if r := checkExpr(be.Left, ctx); r != nil {
		return r
	}
	return checkExpr(be.Right, ctx)
}

// mathSafeConstants lists `Math.<name>` numeric constants the emitter maps
// to Go (`math.Pi`, `math.E`). Adding here requires emitMathAccess at
// expr.go:1783 to handle the matching Go symbol.
var mathSafeConstants = map[string]bool{
	"PI": true, "E": true,
}

// numberSafeConstants lists `Number.<name>` numeric constants the emitter
// maps to Go literals or math package calls. Adding here requires
// emitNumberAccess in expr.go to handle the matching Go symbol.
var numberSafeConstants = map[string]bool{
	"MAX_VALUE": true, "MIN_VALUE": true,
	"MAX_SAFE_INTEGER": true, "MIN_SAFE_INTEGER": true,
	"EPSILON":           true,
	"POSITIVE_INFINITY": true, "NEGATIVE_INFINITY": true,
	"NaN": true,
}

// numberSafeMethods lists `Number.<method>` static calls the emitter lowers
// to pure-primitive Go. `parseInt` is excluded for the same reason as the
// global parseInt: the emitter would return `(int, error)`.
var numberSafeMethods = map[string]bool{
	"isNaN": true, "isFinite": true,
	"isInteger": true, "isSafeInteger": true,
	"parseFloat": true,
}

// checkPropertyAccess accepts the Math numeric constants, `.length` on
// arrays/strings, and named-interface field access (`r.width`).
func checkPropertyAccess(node *ast.Node, ctx *bodyCtx) *Reason {
	pa := node.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonUnhandledKind, Detail: "property access not supported"}
	}
	propName := pa.Name().AsIdentifier().Text

	// `this.<field>` or `this.<method>` reference inside a class method.
	// Field is readable AND writable (constructor / method); method refs must
	// sit in call head position — that's enforced by checkCallExpr, which
	// short-circuits before reaching here.
	if ctx.inMethod && pa.Expression != nil && pa.Expression.Kind == ast.KindThisKeyword {
		if ctx.thisFields[propName] {
			return nil
		}
		if ctx.thisMethods[propName] {
			return nil
		}
		return &Reason{Code: reasonUnhandledKind, Detail: "`this." + propName + "` references unknown field/method"}
	}

	if pa.Expression.Kind == ast.KindIdentifier {
		recv := pa.Expression.AsIdentifier().Text
		if !ctx.paramNames[recv] && !ctx.localNames[recv] {
			if r, handled := checkNamespacedConstant(recv, propName, "Math", mathSafeConstants); handled {
				return r
			}
			if r, handled := checkNamespacedConstant(recv, propName, "Number", numberSafeConstants); handled {
				return r
			}
		}
	}

	if propName == "length" {
		return rejectNonLengthableReceiver(pa.Expression, ctx)
	}

	// Named-interface field access: receiver type must be an extractable
	// object (named, all-primitive props). The picker accepting `r: Rect`
	// at the param gate doesn't help by itself - the body walker still
	// needs to admit `r.width` and reject `obj.foo` on anonymous inline
	// types (which the emitter would lower via jsrt.Obj reflection).
	return checkReceiverType(pa.Expression, ctx, func(t *checker.Type) bool {
		return isExtractableObjectType(ctx.ck, t)
	}, "a named-interface field receiver")
}

// checkElementAccess accepts `arrayVar[i]` where the index is number-typed
// and the base is an array-typed identifier.
func checkElementAccess(node *ast.Node, ctx *bodyCtx) *Reason {
	ea := node.AsElementAccessExpression()
	if ea == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "nil element access"}
	}
	if r := rejectNonArrayReceiver(ea.Expression, ctx); r != nil {
		return r
	}
	if r := checkExpr(ea.ArgumentExpression, ctx); r != nil {
		return r
	}
	if ctx.ck != nil && !isNumberLikeNode(ctx.ck, ea.ArgumentExpression) {
		return &Reason{Code: reasonUnhandledKind, Detail: "array index must be number-typed"}
	}
	return nil
}

// checkExprWithType is the shared core for "walk expr, then assert its
// checker type satisfies pred". Returns reject when the predicate fails.
// nil expr or nil ctx.ck both short-circuit to nil so callers can decide
// whether those edge cases are acceptable (condition gates: yes; receiver
// gates: no — they handle them before delegating here).
func checkExprWithType(expr *ast.Node, ctx *bodyCtx, pred func(*checker.Type) bool, reject Reason) *Reason {
	if expr == nil {
		return nil
	}
	if r := checkExpr(expr, ctx); r != nil {
		return r
	}
	if ctx.ck == nil {
		return nil
	}
	if !pred(ctx.ck.GetTypeAtLocation(expr)) {
		return &reject
	}
	return nil
}

// requireBoolExpr is a thin wrapper enforcing boolean type. Without this,
// `if (n)` and `n && b` drive the emitter to wrap with `jsrt.ToBool` or to
// emit Go `&&`/`!` against a non-bool — both wrong.
func requireBoolExpr(expr *ast.Node, ctx *bodyCtx, detail string) *Reason {
	return checkExprWithType(expr, ctx, isBoolLikeType, Reason{Code: reasonObjectType, Detail: detail})
}

func requireBoolOperand(expr *ast.Node, ctx *bodyCtx, where string) *Reason {
	return requireBoolExpr(expr, ctx, where+" operand must be boolean-typed")
}

func requireBoolCondition(expr *ast.Node, ctx *bodyCtx) *Reason {
	return requireBoolExpr(expr, ctx, "condition must be boolean-typed (no JS truthy coercion)")
}

// checkReceiverType is the receiver-position counterpart to requireBoolExpr.
// Both share the walker + type-query core via checkExprWithType but differ on
// nil-edge semantics: receivers reject up front (a nil receiver is a malformed
// call), while bool conditions accept (a missing condition is fine).
func checkReceiverType(expr *ast.Node, ctx *bodyCtx, typePred func(*checker.Type) bool, desc string) *Reason {
	if expr == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "nil " + desc + " receiver"}
	}
	if ctx.ck == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "no checker for " + desc + " receiver"}
	}
	return checkExprWithType(expr, ctx, typePred, Reason{Code: reasonObjectType, Detail: "receiver is not " + desc})
}

func rejectNonArrayReceiver(expr *ast.Node, ctx *bodyCtx) *Reason {
	return checkReceiverType(expr, ctx, func(t *checker.Type) bool {
		return arrayElementType(ctx.ck, t) != nil
	}, "an array")
}

func rejectNonStringReceiver(expr *ast.Node, ctx *bodyCtx) *Reason {
	return checkReceiverType(expr, ctx, isStringLikeType, "a string")
}

// checkForOfStatement accepts `for (const x of xs)` over a walker-safe
// primitive-array iterable. Destructuring patterns and `for await` are both
// rejected. Registers the loop variable as a local so the body walker treats
// it as readable/writable.
func checkForOfStatement(node *ast.Node, ctx *bodyCtx) *Reason {
	fio := node.AsForInOrOfStatement()
	if fio == nil {
		return nil
	}
	if fio.AwaitModifier != nil {
		return &Reason{Code: reasonAwait, Detail: "for-await-of"}
	}
	if fio.Initializer == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "for-of without initializer"}
	}
	var loopVar string
	switch fio.Initializer.Kind {
	case ast.KindVariableDeclarationList:
		decls := fio.Initializer.AsVariableDeclarationList()
		if decls == nil || decls.Declarations == nil || len(decls.Declarations.Nodes) != 1 {
			return &Reason{Code: reasonUnhandledKind, Detail: "for-of declaration list must bind one variable"}
		}
		vd := decls.Declarations.Nodes[0].AsVariableDeclaration()
		if vd == nil || vd.Name() == nil || vd.Name().Kind != ast.KindIdentifier {
			return &Reason{Code: reasonUnhandledKind, Detail: "for-of destructuring not supported"}
		}
		loopVar = vd.Name().AsIdentifier().Text
	case ast.KindIdentifier:
		loopVar = fio.Initializer.AsIdentifier().Text
	default:
		return &Reason{Code: reasonUnhandledKind, Detail: "for-of initializer must declare a bare identifier"}
	}

	if fio.Expression == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "for-of without iterable"}
	}
	if r := checkExpr(fio.Expression, ctx); r != nil {
		return r
	}
	if ctx.ck != nil {
		t := ctx.ck.GetTypeAtLocation(fio.Expression)
		if t == nil || arrayElementType(ctx.ck, t) == nil {
			return &Reason{Code: reasonObjectType, Detail: "for-of iterable must be a primitive array"}
		}
	}
	ctx.localNames[loopVar] = true
	if fio.Statement != nil {
		if r := checkBody(fio.Statement, ctx); r != nil {
			return r
		}
	}
	return nil
}

// checkSwitchStatement accepts switches whose discriminant is a walker-safe,
// primitive-typed expression. Every case-clause expression and every case
// body is walked individually; fall-through `break` statements are already
// handled by the emitter.
func checkSwitchStatement(node *ast.Node, ctx *bodyCtx) *Reason {
	sw := node.AsSwitchStatement()
	if sw == nil {
		return nil
	}
	if sw.Expression == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "switch with no discriminant"}
	}
	if r := checkExpr(sw.Expression, ctx); r != nil {
		return r
	}
	if ctx.ck != nil {
		t := ctx.ck.GetTypeAtLocation(sw.Expression)
		if t == nil || !isPrimitiveOrVoid(t.Flags()) {
			return &Reason{Code: reasonObjectType, Detail: "switch discriminant must be primitive"}
		}
	}
	if sw.CaseBlock == nil {
		return nil
	}
	block := sw.CaseBlock.AsCaseBlock()
	if block == nil || block.Clauses == nil {
		return nil
	}
	for _, clause := range block.Clauses.Nodes {
		cc := clause.AsCaseOrDefaultClause()
		if cc == nil {
			continue
		}
		if cc.Expression != nil {
			if r := checkExpr(cc.Expression, ctx); r != nil {
				return r
			}
		}
		if cc.Statements != nil {
			for _, stmt := range cc.Statements.Nodes {
				if r := checkBody(stmt, ctx); r != nil {
					return r
				}
			}
		}
	}
	return nil
}

// checkAwaitExpr accepts `await <Promise<T>>` inside async functions where T
// is an extractable type. The emitter lowers this to an IIFE around
// `.Await()` (with `jsrt.Throw` on error), which only compiles when the
// awaited value is a *promise.Promise[T].
func checkAwaitExpr(node *ast.Node, ctx *bodyCtx) *Reason {
	if !ctx.inAsync {
		return &Reason{Code: reasonAwait, Detail: "await outside async function"}
	}
	ae := node.AsAwaitExpression()
	if ae == nil || ae.Expression == nil {
		return &Reason{Code: reasonAwait, Detail: "await with no operand"}
	}
	if r := checkExpr(ae.Expression, ctx); r != nil {
		return r
	}
	if ctx.ck == nil {
		return &Reason{Code: reasonAwait, Detail: "no checker for await operand"}
	}
	t := ctx.ck.GetTypeAtLocation(ae.Expression)
	inner := ctx.ck.GetPromisedTypeOfPromise(t)
	if inner == nil {
		return &Reason{Code: reasonAwait, Detail: "await operand must be a Promise<T>"}
	}
	if r := isExtractableType(ctx.ck, inner); r != nil {
		return &Reason{Code: r.Code, Detail: "await result: " + r.Detail}
	}
	return nil
}

// checkArrayLiteral accepts primitive-element array literals without spread.
// The literal's contextual type must itself be extractable (Array<primitive>)
// so the emitter writes a well-formed `[]T{...}`.
func checkArrayLiteral(node *ast.Node, ctx *bodyCtx) *Reason {
	arr := node.AsArrayLiteralExpression()
	if arr == nil || arr.Elements == nil {
		return nil
	}
	for _, elem := range arr.Elements.Nodes {
		if elem.Kind == ast.KindSpreadElement {
			// Spread of an extractable expression is fine — expr.go's
			// array-literal lowering already chains append calls. Walk
			// the inner expression for the same checks any other element
			// gets so a `...someJunk` doesn't slip through.
			inner := elem.AsSpreadElement()
			if inner == nil || inner.Expression == nil {
				return &Reason{Code: reasonSpread, Detail: "spread missing operand"}
			}
			if r := checkExpr(inner.Expression, ctx); r != nil {
				return r
			}
			continue
		}
		if r := checkExpr(elem, ctx); r != nil {
			return r
		}
	}
	if ctx.ck != nil {
		t := ctx.ck.GetTypeAtLocation(node)
		if r := isExtractableType(ctx.ck, t); r != nil {
			return &Reason{Code: r.Code, Detail: "array literal: " + r.Detail}
		}
	}
	return nil
}

// checkTemplateExpression walks each interpolated span's expression. The
// emitter lowers the whole template into one `fmt.Sprintf` call with a
// format specifier chosen per-span from the expression's Go type; both paths
// need the span expression to be walker-safe AND primitive-typed so the
// format specifier is deterministic.
func checkTemplateExpression(node *ast.Node, ctx *bodyCtx) *Reason {
	tmpl := node.AsTemplateExpression()
	if tmpl == nil || tmpl.TemplateSpans == nil {
		return nil
	}
	for _, span := range tmpl.TemplateSpans.Nodes {
		ts := span.AsTemplateSpan()
		if ts == nil || ts.Expression == nil {
			continue
		}
		if r := checkExpr(ts.Expression, ctx); r != nil {
			return r
		}
		if ctx.ck != nil {
			t := ctx.ck.GetTypeAtLocation(ts.Expression)
			if t == nil || !isPrimitiveOrVoid(t.Flags()) {
				return &Reason{Code: reasonObjectType, Detail: "template interpolation must be primitive-typed"}
			}
		}
	}
	return nil
}

func rejectNonLengthableReceiver(expr *ast.Node, ctx *bodyCtx) *Reason {
	return checkReceiverType(expr, ctx, func(t *checker.Type) bool {
		return isStringLikeType(t) || arrayElementType(ctx.ck, t) != nil
	}, "an array or string")
}

// rejectParamMutation returns a Reason when target is an Identifier that
// refers to a function parameter. Mutations of locals are fine.
func rejectParamMutation(target *ast.Node, ctx *bodyCtx) *Reason {
	if target == nil || target.Kind != ast.KindIdentifier {
		return nil
	}
	name := target.AsIdentifier().Text
	if ctx.paramNames[name] && !ctx.localNames[name] {
		return &Reason{Code: reasonMutParam, Detail: "assigns to parameter `" + name + "`"}
	}
	return nil
}

// checkCallExpr allows calls to same-file extractable functions and to a
// small safelist of built-in namespaces (`Math.<method>`, `Number.<method>`,
// string/array methods). All other property-access callees (arr.map via a
// non-callback path, user.toString, etc.) are dynamic and get rejected.
func checkCallExpr(node *ast.Node, ctx *bodyCtx) *Reason {
	ce := node.AsCallExpression()
	if ce.TypeArguments != nil && len(ce.TypeArguments.Nodes) > 0 {
		return &Reason{Code: reasonGenericType, Detail: "generic call"}
	}
	callee := ce.Expression
	if callee == nil {
		return &Reason{Code: reasonDynamicCallee, Detail: "missing callee"}
	}
	switch callee.Kind {
	case ast.KindIdentifier:
		if r := checkBareCallee(callee.AsIdentifier().Text, ctx); r != nil {
			return r
		}
	case ast.KindPropertyAccessExpression:
		if r := checkThisMethodCall(callee, ctx); r != nil {
			// Not a `this.<method>` call; fall through to namespace/instance
			// builtin handling which has its own error codes.
			if r2 := checkBuiltinCallee(callee, ctx); r2 != nil {
				return r2
			}
		}
	default:
		return &Reason{Code: reasonDynamicCallee, Detail: "callee is not a bare identifier"}
	}
	// Callback-taking array methods (map/filter/forEach/some/every) take a
	// bare *JSFunc parameter as their callback — the identifier reaches the
	// arg walker below where checkIdentifierRef would otherwise reject it
	// as a value-use. Validate the shape here and skip generic arg walking
	// for these specific calls.
	if callee.Kind == ast.KindPropertyAccessExpression {
		pa := callee.AsPropertyAccessExpression()
		if pa != nil && pa.Name() != nil && pa.Name().Kind == ast.KindIdentifier {
			if _, isCb := arrayCallbackSafeMethods[pa.Name().AsIdentifier().Text]; isCb {
				return checkArrayCallbackMethodCall(ce, pa, ctx)
			}
		}
	}
	if ce.Arguments != nil {
		for _, arg := range ce.Arguments.Nodes {
			if r := checkExpr(arg, ctx); r != nil {
				return r
			}
		}
	}
	return nil
}

// safeGlobalCallees lists JS global functions the emitter lowers into Go code
// that matches the picker's primitive-only contract. `parseInt` is excluded
// because the emitter writes a bare `strconv.Atoi` reference, producing
// `(int, error)` at the call site - a type mismatch against the `number`
// return. `String`/`Boolean` take `any` and return wrappers - unsafe.
var safeGlobalCallees = map[string]bool{
	"parseFloat": true,
	"isNaN":      true,
	"isFinite":   true,
}

func checkBareCallee(name string, ctx *bodyCtx) *Reason {
	if ctx.jsFuncParamNames[name] {
		return nil
	}
	if ctx.paramNames[name] || ctx.localNames[name] {
		return &Reason{Code: reasonDynamicCallee, Detail: "callee `" + name + "` is a local/param (function-typed value)"}
	}
	if _, ok := ctx.topLevelFuncs[name]; ok {
		return nil
	}
	if safeGlobalCallees[name] {
		return nil
	}
	return &Reason{Code: reasonBuiltinCall, Detail: "callee `" + name + "` is not a same-file function"}
}

// mathSafeMethods lists Math.<method> calls that round-trip through the
// emitter into a valid Go expression (`math.<Title>`, Go 1.21 `min`/`max`,
// or `rand.Float64`). Every entry must have a corresponding Go math symbol;
// `Math.sign` was dropped because Go's math package has no Sign function.
// The TestHybrid_MathSafelistCompiles smoke guards drift.
var mathSafeMethods = map[string]bool{
	"abs": true, "floor": true, "ceil": true, "round": true, "trunc": true,
	"sqrt": true, "cbrt": true, "pow": true, "exp": true,
	"log": true, "log2": true, "log10": true,
	"min": true, "max": true,
	"sin": true, "cos": true, "tan": true,
	"asin": true, "acos": true, "atan": true, "atan2": true,
	"sinh": true, "cosh": true, "tanh": true,
	"asinh": true, "acosh": true, "atanh": true,
	"hypot": true, "random": true,
}

// stringSafeMethods lists instance methods on `string` values that the
// emitter's emitStringMethodCall handles and that take + return either
// primitives or `string[]` (no callbacks, no regex). `replace` is excluded
// because its function-arg form produces JS callbacks the walker cannot
// clear soundly.
var stringSafeMethods = map[string]bool{
	"toUpperCase": true, "toLowerCase": true,
	"trim": true, "trimStart": true, "trimEnd": true,
	"includes": true, "startsWith": true, "endsWith": true,
	"indexOf": true, "lastIndexOf": true,
	"split":      true,
	"repeat":     true,
	"replaceAll": true,
	"slice":      true, "substring": true,
	"padStart": true, "padEnd": true,
}

// arraySafeMethods lists instance methods on arrays of primitives. These
// emit into `jsarray.*` calls (the ramune runtime helper package), so
// extracted code carries a ramune dependency - that is the norm for the
// generated native module but means standalone compile-only smokes cannot
// cover them. Callback-taking methods are gated separately in
// arrayCallbackSafeMethods so the walker can enforce per-method callback
// shape (return must be boolean for filter/some/every; return must be
// extractable for map; callback form must be a bare *JSFunc param — inline
// arrows are already rejected by the generic expr walker).
var arraySafeMethods = map[string]bool{
	"includes": true, "indexOf": true, "lastIndexOf": true,
	"join": true, "slice": true, "concat": true, "reverse": true,
}

// arrayCallbackSafeMethods is the admitted subset of callback-taking array
// instance methods. Membership check only; per-method return-type policy
// lives in callbackReturnPolicy so additions stay mechanical.
//
// `find` / `findIndex` joined the set after the JSFunc-bridge emit grew
// the corresponding IIFEs in expr.go's emitArrayJSFuncCallbackMethod.
// Inline-arrow callbacks remain rejected (the bare-*JSFunc-param rule),
// so the legacy `jsarray.Find{,Index}` int-return / `(T, bool)` shape
// never reaches the build.
//
// Deferred: `reduce` (accumulator type inference).
var arrayCallbackSafeMethods = map[string]bool{
	"map":       true,
	"filter":    true,
	"forEach":   true,
	"some":      true,
	"every":     true,
	"find":      true,
	"findIndex": true,
}

// checkThisMethodCall accepts `this.<method>(...)` when <method> is declared
// in the same class. Returns nil on accept; a Reason when the callee isn't a
// `this.<ident>` form or the method name isn't registered. Callers that get a
// non-nil result fall back to the namespaced/instance builtin handlers.
func checkThisMethodCall(callee *ast.Node, ctx *bodyCtx) *Reason {
	if !ctx.inMethod {
		return &Reason{Code: reasonDynamicCallee, Detail: "this-method call outside method"}
	}
	pa := callee.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword {
		return &Reason{Code: reasonDynamicCallee, Detail: "not a this.method call"}
	}
	if pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonDynamicCallee, Detail: "this.method must be identifier"}
	}
	name := pa.Name().AsIdentifier().Text
	if ctx.thisMethods[name] {
		return nil
	}
	return &Reason{Code: reasonDynamicCallee, Detail: "`this." + name + "` is not a same-class method"}
}

func checkBuiltinCallee(callee *ast.Node, ctx *bodyCtx) *Reason {
	pa := callee.AsPropertyAccessExpression()
	if pa == nil || pa.Expression == nil {
		return &Reason{Code: reasonDynamicCallee, Detail: "nil receiver"}
	}
	if pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonDynamicCallee, Detail: "non-identifier method"}
	}
	if r := checkMathCall(pa, ctx); r == nil {
		return nil
	}
	if r := checkNumberCall(pa, ctx); r == nil {
		return nil
	}
	if r := checkStringMethodCall(pa, ctx); r == nil {
		return nil
	}
	if r := checkArrayMethodCall(pa, ctx); r == nil {
		return nil
	}
	return &Reason{Code: reasonBuiltinCall, Detail: "builtin call not in safelist"}
}

// checkNamespacedConstant returns (nil, true) when recv matches namespace
// and propName is in the safelist; (reject Reason, true) when recv matches
// but propName is not safelisted; (nil, false) when recv doesn't match
// (caller should fall through to the next handler).
func checkNamespacedConstant(recv, propName, namespace string, constants map[string]bool) (*Reason, bool) {
	if recv != namespace {
		return nil, false
	}
	if constants[propName] {
		return nil, true
	}
	return &Reason{Code: reasonBuiltinCall, Detail: namespace + "." + propName + " not in constant safelist"}, true
}

// checkNamespacedCall returns nil iff callee is `<namespace>.<method>` where
// namespace is the global identifier (not locally shadowed) and method is in
// the safelist. Used for `Math.*` and `Number.*` style calls.
func checkNamespacedCall(pa *ast.PropertyAccessExpression, ctx *bodyCtx, namespace string, methods map[string]bool) *Reason {
	if pa.Expression.Kind != ast.KindIdentifier {
		return &Reason{Code: reasonBuiltinCall, Detail: "not a " + namespace + " call"}
	}
	recv := pa.Expression.AsIdentifier().Text
	if recv != namespace || ctx.paramNames[recv] || ctx.localNames[recv] {
		return &Reason{Code: reasonBuiltinCall, Detail: "not the global " + namespace}
	}
	method := pa.Name().AsIdentifier().Text
	if !methods[method] {
		return &Reason{Code: reasonBuiltinCall, Detail: namespace + "." + method + " not in safelist"}
	}
	return nil
}

func checkMathCall(pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	return checkNamespacedCall(pa, ctx, "Math", mathSafeMethods)
}

func checkNumberCall(pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	return checkNamespacedCall(pa, ctx, "Number", numberSafeMethods)
}

// checkStringMethodCall returns nil iff method is in the string safelist and
// the receiver is a walker-safe string-typed expression.
func checkStringMethodCall(pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	method := pa.Name().AsIdentifier().Text
	if !stringSafeMethods[method] {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " not in string safelist"}
	}
	return rejectNonStringReceiver(pa.Expression, ctx)
}

// checkArrayMethodCall returns nil iff method is in the array safelist and
// the receiver is a walker-safe primitive-array-typed expression.
func checkArrayMethodCall(pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	method := pa.Name().AsIdentifier().Text
	if _, isCallbackMethod := arrayCallbackSafeMethods[method]; isCallbackMethod {
		// Callback methods have their own argument-shape validation handled
		// by checkArrayCallbackMethodCall (invoked from the parent call
		// walker); the parent call walker can't fold method-name-specific
		// rules through the generic builtin path, so route here.
		return checkArrayMethodReceiver(pa, ctx)
	}
	if !arraySafeMethods[method] {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " not in array safelist"}
	}
	return checkArrayMethodReceiver(pa, ctx)
}

// checkArrayMethodReceiver walks the receiver expression and confirms its
// type is a primitive-element array. Shared between the no-callback and
// callback dispatch gates.
func checkArrayMethodReceiver(pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	expr := pa.Expression
	if r := checkExpr(expr, ctx); r != nil {
		return r
	}
	if ctx.ck == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "no checker for array receiver"}
	}
	t := ctx.ck.GetTypeAtLocation(expr)
	if t == nil || arrayElementType(ctx.ck, t) == nil {
		return &Reason{Code: reasonObjectType, Detail: "receiver is not an array"}
	}
	return nil
}

// checkArrayCallbackMethodCall validates the argument shape of a callback-
// taking array method: single bare *JSFunc parameter, one-param callback,
// per-method return-type policy. Assumes the receiver has already been
// vetted by checkArrayMethodCall.
func checkArrayCallbackMethodCall(ce *ast.CallExpression, pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	method := pa.Name().AsIdentifier().Text
	if !arrayCallbackSafeMethods[method] {
		return nil
	}
	if ce.Arguments == nil || len(ce.Arguments.Nodes) != 1 {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " requires exactly one callback argument"}
	}
	arg := ce.Arguments.Nodes[0]
	if arg.Kind != ast.KindIdentifier {
		return &Reason{Code: reasonFuncLiteral, Detail: "." + method + " callback must be a bare parameter identifier (inline functions not supported)"}
	}
	name := arg.AsIdentifier().Text
	if !ctx.jsFuncParamNames[name] {
		return &Reason{Code: reasonDynamicCallee, Detail: "." + method + " callback `" + name + "` is not a *JSFunc parameter of the enclosing function"}
	}
	if ctx.ck == nil {
		return nil
	}
	cbType := ctx.ck.GetTypeAtLocation(arg)
	if cbType == nil {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " callback has no checker type"}
	}
	sigs := ctx.ck.GetSignaturesOfType(cbType, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " callback has no call signature"}
	}
	ret := ctx.ck.GetReturnTypeOfSignature(sigs[0])
	// Per-method return-type policy. filter/some/every need a boolean to
	// decide inclusion or short-circuit. forEach tolerates void because
	// the emitter discards the result. map rejects void (the output slice
	// would be `[]void` — malformed).
	switch method {
	case "filter", "some", "every", "find", "findIndex":
		if ret == nil || !isBoolLikeType(ret) {
			return &Reason{Code: reasonObjectType, Detail: "." + method + " callback must return boolean"}
		}
	case "map":
		if ret != nil && ret.Flags()&checker.TypeFlagsVoidLike != 0 {
			return &Reason{Code: reasonObjectType, Detail: ".map callback must return a value"}
		}
	}
	arrT := ctx.ck.GetTypeAtLocation(pa.Expression)
	if arrT == nil || arrayElementType(ctx.ck, arrT) == nil {
		return nil // receiver gate will fail separately
	}
	// The emitter lowers `.map(cb)` as `cb.Call(x)` with only the element;
	// a 2+-param callback would see `undefined` for index/source at runtime.
	if len(sigs[0].Parameters()) != 1 {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " callback must declare exactly one parameter"}
	}
	return nil
}
