package picker

import (
	"fmt"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// IsFunctionExtractable classifies a FunctionDeclaration node.
//
// topLevelFuncs maps top-level function names in the same file to their
// declaration node. This lets the body walker recognize self- and peer-calls
// without flagging them as free identifiers.
//
// Only the v1 predicate is implemented:
//   - non-generic, non-generator, non-async
//   - every parameter is number/string/boolean
//   - return type is primitive or void
//   - body uses only the v1 AST allowlist
//   - no closure capture beyond params/locals and same-file functions
//   - no parameter mutation
//   - no built-in calls (Math.*, String(), etc.)
func IsFunctionExtractable(node *ast.Node, ck *checker.Checker, topLevelFuncs map[string]*ast.Node) (bool, Reason) {
	if node == nil || node.Kind != ast.KindFunctionDeclaration {
		return false, Reason{Code: reasonUnhandledKind, Detail: "not a function declaration"}
	}
	fd := node.AsFunctionDeclaration()
	if fd == nil {
		return false, Reason{Code: reasonUnhandledKind, Detail: "nil function declaration"}
	}

	// 1. Declaration shape.
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

	// 2. Signature checks.
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

	// Collect parameter names so the body walker can recognize them as locals.
	paramNames := map[string]bool{}
	for i, paramSym := range sig.Parameters() {
		if paramSym == nil {
			continue
		}
		paramNames[paramSym.Name] = true
		pt := ck.GetTypeOfSymbol(paramSym)
		if ok, r := isExtractableType(pt); !ok {
			return false, Reason{Code: r.Code, Detail: fmt.Sprintf("param %d (%s): %s", i, paramSym.Name, r.Detail)}
		}
	}

	retType := ck.GetReturnTypeOfSignature(sig)
	if ok, r := isExtractableType(retType); !ok {
		return false, Reason{Code: r.Code, Detail: "return: " + r.Detail}
	}

	// 3. Body walk.
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
	paramNames    map[string]bool      // function parameters — readable, not writable
	topLevelFuncs map[string]*ast.Node // same-file top-level functions — callable
	localNames    map[string]bool      // locals declared in this body (let/const/var) — readable AND writable
}

// checkBody walks a block/statement subtree and returns a non-nil Reason if
// any node violates the v1 allowlist. It mutates ctx.localNames as it
// encounters VariableDeclarations.
func checkBody(node *ast.Node, ctx *bodyCtx) *Reason {
	if node == nil {
		return nil
	}
	switch node.Kind {
	// ── Declarations & statements ─────────────────────────────────────
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
		decls := vs.DeclarationList.AsVariableDeclarationList()
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
		if r := checkExpr(is.Expression, ctx); r != nil {
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
			if r := checkExpr(fs.Condition, ctx); r != nil {
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
		if r := checkExpr(ws.Expression, ctx); r != nil {
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
		return checkExpr(ds.Expression, ctx)

	case ast.KindBreakStatement, ast.KindContinueStatement, ast.KindEmptyStatement:
		// Allow unlabeled only. Labeled break/continue have a Label field.
		if node.AsBreakStatement() != nil && node.AsBreakStatement().Label != nil {
			return &Reason{Code: reasonLabeledStmt, Detail: "labeled break"}
		}
		if node.AsContinueStatement() != nil && node.AsContinueStatement().Label != nil {
			return &Reason{Code: reasonLabeledStmt, Detail: "labeled continue"}
		}
		return nil

	case ast.KindVariableDeclaration:
		return checkVarDecl(node, ctx)

	// ── Disallowed statement kinds (explicit) ─────────────────────────
	case ast.KindLabeledStatement:
		return &Reason{Code: reasonLabeledStmt, Detail: "labeled statement"}
	case ast.KindTryStatement:
		return &Reason{Code: reasonTry, Detail: "try/catch not supported in v1"}
	case ast.KindThrowStatement:
		return &Reason{Code: reasonThrow, Detail: "throw not supported in v1"}
	case ast.KindSwitchStatement, ast.KindForInStatement, ast.KindForOfStatement,
		ast.KindWithStatement, ast.KindDebuggerStatement:
		return &Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("statement kind %v not supported in v1", node.Kind)}
	}

	// Expression at statement position shouldn't normally reach here — already
	// covered by KindExpressionStatement — but be defensive.
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
	// Registered as a local even if the initializer fails — simpler, and the
	// whole function is rejected anyway.
	ctx.localNames[name] = true

	// Type of the declared variable must be extractable.
	if ctx.ck != nil {
		if t := ctx.ck.GetTypeAtLocation(vd.Name()); t != nil {
			if ok, r := isExtractableType(t); !ok {
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
	// ── Accepted literals & leaf nodes ────────────────────────────────
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return nil

	case ast.KindIdentifier:
		return checkIdentifierRef(node, ctx)

	case ast.KindThisKeyword:
		return &Reason{Code: reasonThis, Detail: "`this` not allowed outside methods (v1)"}

	// ── Accepted composite expressions ────────────────────────────────
	case ast.KindParenthesizedExpression:
		return checkExpr(node.AsParenthesizedExpression().Expression, ctx)

	case ast.KindAsExpression:
		return checkExpr(node.AsAsExpression().Expression, ctx)

	case ast.KindNonNullExpression:
		return checkExpr(node.AsNonNullExpression().Expression, ctx)

	case ast.KindPrefixUnaryExpression:
		pu := node.AsPrefixUnaryExpression()
		switch pu.Operator {
		case ast.KindPlusToken, ast.KindMinusToken, ast.KindExclamationToken, ast.KindTildeToken:
			return checkExpr(pu.Operand, ctx)
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
		if r := checkExpr(ce.Condition, ctx); r != nil {
			return r
		}
		if r := checkExpr(ce.WhenTrue, ctx); r != nil {
			return r
		}
		return checkExpr(ce.WhenFalse, ctx)

	case ast.KindCallExpression:
		return checkCallExpr(node, ctx)

	// ── Hard rejects with named reasons ───────────────────────────────
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
	case ast.KindTemplateExpression, ast.KindTaggedTemplateExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "template literal not supported in v1"}
	case ast.KindPropertyAccessExpression, ast.KindElementAccessExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "property/element access not supported in v1"}
	case ast.KindNewExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "new expression not supported in v1"}
	case ast.KindArrayLiteralExpression, ast.KindObjectLiteralExpression:
		return &Reason{Code: reasonUnhandledKind, Detail: "array/object literal not supported in v1"}
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
	// Arithmetic, comparison (strict only), logical, bitwise.
	case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindAsteriskAsteriskToken,
		ast.KindSlashToken, ast.KindPercentToken,
		ast.KindLessThanToken, ast.KindLessThanEqualsToken,
		ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken,
		ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken,
		ast.KindAmpersandAmpersandToken, ast.KindBarBarToken, ast.KindQuestionQuestionToken,
		ast.KindAmpersandToken, ast.KindBarToken, ast.KindCaretToken,
		ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken,
		ast.KindGreaterThanGreaterThanGreaterThanToken:
		// ok

	case ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken:
		return &Reason{Code: reasonForbiddenOp, Detail: "== and != (use === / !==)"}

	// Assignments mutate — allowed only on locals, never on parameters.
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

// checkCallExpr allows only calls to same-file extractable functions.
// v1 rejects all property-access callees (Math.floor, arr.map, etc.) — any
// callee that isn't a bare Identifier is a dynamic callee.
func checkCallExpr(node *ast.Node, ctx *bodyCtx) *Reason {
	ce := node.AsCallExpression()
	if ce.TypeArguments != nil && len(ce.TypeArguments.Nodes) > 0 {
		return &Reason{Code: reasonGenericType, Detail: "generic call"}
	}
	callee := ce.Expression
	if callee == nil {
		return &Reason{Code: reasonDynamicCallee, Detail: "missing callee"}
	}
	if callee.Kind != ast.KindIdentifier {
		return &Reason{Code: reasonDynamicCallee, Detail: "callee is not a bare identifier"}
	}
	name := callee.AsIdentifier().Text
	if ctx.paramNames[name] || ctx.localNames[name] {
		return &Reason{Code: reasonDynamicCallee, Detail: "callee `" + name + "` is a local/param (function-typed value)"}
	}
	if _, ok := ctx.topLevelFuncs[name]; !ok {
		return &Reason{Code: reasonBuiltinCall, Detail: "callee `" + name + "` is not a same-file function"}
	}
	// Check every argument.
	if ce.Arguments != nil {
		for _, arg := range ce.Arguments.Nodes {
			if r := checkExpr(arg, ctx); r != nil {
				return r
			}
		}
	}
	return nil
}
