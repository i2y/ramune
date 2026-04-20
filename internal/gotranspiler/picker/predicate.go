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
// Only the v1 predicate is implemented:
//   - non-generic, non-generator, non-async
//   - every parameter is number/string/boolean
//   - return type is primitive or void
//   - body uses only the v1 AST allowlist
//   - no closure capture beyond params/locals and same-file functions
//   - no parameter mutation
//   - no built-in calls (Math.*, String(), etc.)
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
	if ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync) {
		return false, Reason{Code: reasonAsyncFunc, Detail: "async function not supported in v1"}
	}
	if fd.Body == nil {
		return false, Reason{Code: reasonMissingBody, Detail: "ambient/overload declaration"}
	}

	fnType := ck.GetTypeAtLocation(node)
	if fnType == nil {
		return false, Reason{Code: reasonEmptyReturn, Detail: "type checker returned nil for function"}
	}
	sigs := ck.GetSignaturesOfType(fnType, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return false, Reason{Code: reasonEmptyReturn, Detail: "no call signature"}
	}
	sig := sigs[0]

	if ck.HasEffectiveRestParameter(sig) {
		return false, Reason{Code: reasonRestParam, Detail: "rest parameter not supported in v1"}
	}

	// Default-value and optional parameters silently widen the JS-callable arity
	// without the emitter generating any compensating Go logic. JS callers
	// invoking f() against an extracted func F(x float64) get a missing-arg
	// error from the bridge. Reject up front rather than ship a latent bug.
	if fd.Parameters != nil {
		for _, p := range fd.Parameters.Nodes {
			pd := p.AsParameterDeclaration()
			if pd == nil {
				continue
			}
			if pd.Initializer != nil {
				return false, Reason{Code: reasonUnhandledKind, Detail: "default parameter value not supported in v1"}
			}
			if pd.QuestionToken != nil {
				return false, Reason{Code: reasonUnhandledKind, Detail: "optional parameter not supported in v1"}
			}
		}
	}

	paramNames := map[string]bool{}
	for i, paramSym := range sig.Parameters() {
		if paramSym == nil {
			continue
		}
		paramNames[paramSym.Name] = true
		if r := isExtractableType(ck, ck.GetTypeOfSymbol(paramSym)); r != nil {
			return false, Reason{Code: r.Code, Detail: fmt.Sprintf("param %d (%s): %s", i, paramSym.Name, r.Detail)}
		}
	}

	if r := isExtractableType(ck, ck.GetReturnTypeOfSignature(sig)); r != nil {
		return false, Reason{Code: r.Code, Detail: "return: " + r.Detail}
	}

	ctx := &bodyCtx{
		ck:            ck,
		paramNames:    paramNames,
		topLevelFuncs: topLevelFuncs,
		localNames:    map[string]bool{},
	}
	if reason := checkBody(fd.Body, ctx); reason != nil {
		return false, *reason
	}

	return true, Reason{}
}

// bodyCtx carries state threaded through the body walker.
type bodyCtx struct {
	ck            *checker.Checker
	paramNames    map[string]bool     // function parameters - readable, not writable
	topLevelFuncs map[string]struct{} // same-file top-level functions - callable
	localNames    map[string]bool     // locals declared in this body (let/const/var) - readable AND writable
}

// checkBody walks a block/statement subtree and returns a non-nil Reason if
// any node violates the v1 allowlist. It mutates ctx.localNames as it
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
		return &Reason{Code: reasonTry, Detail: "try/catch not supported in v1"}
	case ast.KindThrowStatement:
		return &Reason{Code: reasonThrow, Detail: "throw not supported in v1"}
	case ast.KindSwitchStatement:
		return checkSwitchStatement(node, ctx)
	case ast.KindForOfStatement:
		return checkForOfStatement(node, ctx)
	case ast.KindForInStatement,
		ast.KindWithStatement, ast.KindDebuggerStatement:
		return &Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("statement kind %v not supported in v1", node.Kind)}
	}
	return checkExpr(node, ctx)
}

func checkVarDecl(node *ast.Node, ctx *bodyCtx) *Reason {
	vd := node.AsVariableDeclaration()
	if vd == nil || vd.Name() == nil {
		return nil
	}
	if vd.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonUnhandledKind, Detail: "destructuring binding not supported in v1"}
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
		return &Reason{Code: reasonThis, Detail: "`this` not allowed outside methods (v1)"}

	case ast.KindParenthesizedExpression:
		return checkExpr(node.AsParenthesizedExpression().Expression, ctx)

	case ast.KindAsExpression:
		// `x as any` would widen the inner expression to `any` invisibly to
		// the rest of the walker, then the emitter forces a `.(float64)` (or
		// similar) type assertion that fails on the static-typed operand.
		// Reject any cast whose target widens; fall through for everything else.
		ae := node.AsAsExpression()
		if ctx.ck != nil && ae.Type != nil {
			if t := ctx.ck.GetTypeAtLocation(ae.Type); t != nil {
				if t.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsUnknown) != 0 {
					return &Reason{Code: reasonAnyType, Detail: "as-cast widens to any/unknown"}
				}
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
			// Bitwise NOT has the same int↔float64 problem as binary bitwise ops.
			if ctx.ck != nil {
				t := ctx.ck.GetTypeAtLocation(pu.Operand)
				if t != nil && t.Flags()&checker.TypeFlagsNumberLike != 0 {
					return &Reason{Code: reasonForbiddenOp, Detail: "bitwise NOT on number returns int in emitted Go"}
				}
			}
			return checkExpr(pu.Operand, ctx)
		case ast.KindPlusToken, ast.KindMinusToken:
			// JS unary + / - coerces strings/booleans to number; the emitter
			// just drops the operator and returns the original value, so the
			// extracted Go would carry a type mismatch (`return s` with
			// float64 return type). Require numeric operand.
			if r := checkExpr(pu.Operand, ctx); r != nil {
				return r
			}
			if ctx.ck != nil {
				t := ctx.ck.GetTypeAtLocation(pu.Operand)
				if t == nil || t.Flags()&checker.TypeFlagsNumberLike == 0 {
					return &Reason{Code: reasonObjectType, Detail: "unary +/- requires numeric operand"}
				}
			}
			return nil
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
		return &Reason{Code: reasonAwait, Detail: "await"}
	case ast.KindArrowFunction, ast.KindFunctionExpression, ast.KindClassExpression:
		return &Reason{Code: reasonFuncLiteral, Detail: "inline function/class literal"}
	case ast.KindRegularExpressionLiteral:
		return &Reason{Code: reasonRegex, Detail: "regex literal"}
	case ast.KindSpreadElement:
		return &Reason{Code: reasonSpread, Detail: "spread"}
	case ast.KindTemplateExpression:
		return checkTemplateExpression(node, ctx)
	case ast.KindTaggedTemplateExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "tagged template not supported in v1"}
	case ast.KindPropertyAccessExpression:
		return checkPropertyAccess(node, ctx)
	case ast.KindElementAccessExpression:
		return checkElementAccess(node, ctx)
	case ast.KindNewExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "new expression not supported in v1"}
	case ast.KindArrayLiteralExpression:
		return checkArrayLiteral(node, ctx)
	case ast.KindObjectLiteralExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "object literal not supported in v1"}
	case ast.KindTypeOfExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "typeof not supported in v1"}
	case ast.KindDeleteExpression, ast.KindVoidExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "delete/void expression"}
	}

	return &Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("expression kind %v not supported in v1", node.Kind)}
}

// checkIdentifierRef rejects free identifiers: anything not a parameter, a
// local, or another extractable top-level function in the same file.
func checkIdentifierRef(node *ast.Node, ctx *bodyCtx) *Reason {
	name := node.AsIdentifier().Text
	if name == "" || name == "undefined" {
		return nil
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
		if ctx.ck != nil {
			lt := ctx.ck.GetTypeAtLocation(be.Left)
			rt := ctx.ck.GetTypeAtLocation(be.Right)
			if (lt != nil && lt.Flags()&checker.TypeFlagsNumberLike != 0) ||
				(rt != nil && rt.Flags()&checker.TypeFlagsNumberLike != 0) {
				return &Reason{Code: reasonForbiddenOp, Detail: "bitwise op on number returns int in emitted Go but float64 was expected"}
			}
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

// checkPropertyAccess accepts `<arrayOrStringVar>.length` and the Math
// numeric constants listed in mathSafeConstants.
func checkPropertyAccess(node *ast.Node, ctx *bodyCtx) *Reason {
	pa := node.AsPropertyAccessExpression()
	if pa == nil || pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonUnhandledKind, Detail: "property access not supported in v1"}
	}
	propName := pa.Name().AsIdentifier().Text

	if pa.Expression.Kind == ast.KindIdentifier {
		recv := pa.Expression.AsIdentifier().Text
		if recv == "Math" && !ctx.paramNames[recv] && !ctx.localNames[recv] {
			if mathSafeConstants[propName] {
				return nil
			}
			return &Reason{Code: reasonBuiltinCall, Detail: "Math." + propName + " not in constant safelist"}
		}
	}

	if propName == "length" {
		return rejectNonLengthableReceiver(pa.Expression, ctx)
	}
	return &Reason{Code: reasonUnhandledKind, Detail: "only .length and Math.<const> supported (got ." + propName + ")"}
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
	if ctx.ck != nil {
		idx := ctx.ck.GetTypeAtLocation(ea.ArgumentExpression)
		if idx == nil || idx.Flags()&checker.TypeFlagsNumberLike == 0 {
			return &Reason{Code: reasonUnhandledKind, Detail: "array index must be number-typed"}
		}
	}
	return nil
}

// requireBoolOperand walks expr and requires its checker type be boolean.
// Used by `&&` / `||` / `!` operands.
func requireBoolOperand(expr *ast.Node, ctx *bodyCtx, where string) *Reason {
	if expr == nil {
		return nil
	}
	if r := checkExpr(expr, ctx); r != nil {
		return r
	}
	if ctx.ck == nil {
		return nil
	}
	t := ctx.ck.GetTypeAtLocation(expr)
	if t == nil || t.Flags()&checker.TypeFlagsBooleanLike == 0 {
		return &Reason{Code: reasonObjectType, Detail: where + " operand must be boolean-typed"}
	}
	return nil
}

// requireBoolCondition walks expr and requires its checker type be boolean
// (or `boolean | undefined` etc. via TypeFlagsBooleanLike). Without this,
// `if (n)` over a non-boolean drives the emitter to wrap with `jsrt.ToBool`,
// which drags ramune into otherwise-standalone Go output. JS truthy coercion
// over arbitrary types is not preserved by extracted code.
func requireBoolCondition(expr *ast.Node, ctx *bodyCtx) *Reason {
	if expr == nil {
		return nil
	}
	if r := checkExpr(expr, ctx); r != nil {
		return r
	}
	if ctx.ck == nil {
		return nil
	}
	t := ctx.ck.GetTypeAtLocation(expr)
	if t == nil || t.Flags()&checker.TypeFlagsBooleanLike == 0 {
		return &Reason{Code: reasonObjectType, Detail: "condition must be boolean-typed (no JS truthy coercion in v1)"}
	}
	return nil
}

// checkReceiverType walks expr (so closure capture, mutations, etc. are
// caught) and asserts the checker's type for expr satisfies typePred. Used
// for receivers of `.length`, `arr[i]`, string methods, and array methods -
// all of which share the same shape after the bare-identifier constraint
// was lifted to allow chained expressions.
func checkReceiverType(expr *ast.Node, ctx *bodyCtx, typePred func(*checker.Type) bool, desc string) *Reason {
	if expr == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "nil " + desc + " receiver"}
	}
	if r := checkExpr(expr, ctx); r != nil {
		return r
	}
	if ctx.ck == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "no checker for " + desc + " receiver"}
	}
	if !typePred(ctx.ck.GetTypeAtLocation(expr)) {
		return &Reason{Code: reasonObjectType, Detail: "receiver is not " + desc}
	}
	return nil
}

func rejectNonArrayReceiver(expr *ast.Node, ctx *bodyCtx) *Reason {
	return checkReceiverType(expr, ctx, func(t *checker.Type) bool {
		return t != nil && arrayElementType(ctx.ck, t) != nil
	}, "an array")
}

func rejectNonStringReceiver(expr *ast.Node, ctx *bodyCtx) *Reason {
	return checkReceiverType(expr, ctx, func(t *checker.Type) bool {
		return t != nil && t.Flags()&checker.TypeFlagsStringLike != 0
	}, "a string")
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
			return &Reason{Code: reasonSpread, Detail: "spread in array literal"}
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
		if t == nil {
			return false
		}
		return t.Flags()&checker.TypeFlagsStringLike != 0 || arrayElementType(ctx.ck, t) != nil
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
// small safelist of built-in namespaces (`Math.<method>` in v1.3). All other
// property-access callees (arr.map, user.toString, etc.) are dynamic.
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
		if r := checkBuiltinCallee(callee, ctx); r != nil {
			return r
		}
	default:
		return &Reason{Code: reasonDynamicCallee, Detail: "callee is not a bare identifier"}
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
// clear in v1.
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
// cover them. Callback-taking methods (map/filter/reduce/etc.) are excluded.
var arraySafeMethods = map[string]bool{
	"includes": true, "indexOf": true, "lastIndexOf": true,
	"join": true, "slice": true, "concat": true, "reverse": true,
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
	if r := checkStringMethodCall(pa, ctx); r == nil {
		return nil
	}
	if r := checkArrayMethodCall(pa, ctx); r == nil {
		return nil
	}
	return &Reason{Code: reasonBuiltinCall, Detail: "builtin call not in safelist"}
}

// checkMathCall returns nil iff callee is `Math.<method>` where Math is the
// global (not shadowed) and method is in the safelist.
func checkMathCall(pa *ast.PropertyAccessExpression, ctx *bodyCtx) *Reason {
	if pa.Expression.Kind != ast.KindIdentifier {
		return &Reason{Code: reasonBuiltinCall, Detail: "not a Math call"}
	}
	recv := pa.Expression.AsIdentifier().Text
	if recv != "Math" || ctx.paramNames[recv] || ctx.localNames[recv] {
		return &Reason{Code: reasonBuiltinCall, Detail: "not the global Math"}
	}
	method := pa.Name().AsIdentifier().Text
	if !mathSafeMethods[method] {
		return &Reason{Code: reasonBuiltinCall, Detail: "Math." + method + " not in safelist"}
	}
	return nil
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
	if !arraySafeMethods[method] {
		return &Reason{Code: reasonBuiltinCall, Detail: "." + method + " not in array safelist"}
	}
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
