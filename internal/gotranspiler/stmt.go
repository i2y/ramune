package gotranspiler

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// emitStatement generates Go source code for a TypeScript statement node.
func (t *Transpiler) emitStatement(node *ast.Node) {
	if node == nil {
		return
	}

	switch node.Kind {
	case ast.KindVariableStatement:
		t.emitVariableStatement(node)

	case ast.KindExpressionStatement:
		exprStmt := node.AsExpressionStatement()
		t.emitExpr(exprStmt.Expression)
		t.w.newline()

	case ast.KindReturnStatement:
		ret := node.AsReturnStatement()
		if t.inAsyncBody {
			if ret.Expression != nil {
				// Check if the return expression is a Promise (needs .Await() unwrap)
				retTypeInfo := t.getGoType(ret.Expression)
				if retTypeInfo.IsPromise() {
					t.w.addImport("github.com/i2y/ramune/jsrt", "")
					t.w.write("{ __v, __err := (")
					exprCode := t.captureExpr(ret.Expression)
					t.w.write(exprCode)
					// Add type assertion on __v when Go-level promise produces any
					// Add type assertion on __v when the promise might resolve to any at Go level
					resolveAssert := ""
					if t.currentRetType != "" && t.currentRetType != "any" {
						needsAssert := retTypeInfo.ElemType != t.currentRetType ||
							strings.Contains(exprCode, "[any]") ||
							goCodeProducesAny(exprCode)
						if !needsAssert && ret.Expression.Kind == ast.KindIdentifier {
							vn := goVarName(ret.Expression.AsIdentifier().Text)
							if t.goAnyVars != nil && t.goAnyVars[vn] {
								needsAssert = true
							}
						}
						if needsAssert {
							resolveAssert = ".(" + t.currentRetType + ")"
						}
					}
					t.w.writef(").Await(); if __err != nil { __reject(__err); return }; __resolve(__v%s) }", resolveAssert)
				} else {
					t.w.write("__resolve(")
					// Auto-wrap with jsrt.Ptr() when __resolve expects pointer but value is non-pointer
					retExprType := t.getGoType(ret.Expression)
					if strings.HasPrefix(t.currentRetType, "*") &&
						!strings.HasPrefix(retExprType.GoStr, "*") &&
						retExprType.GoStr != "" && retExprType.GoStr != "any" {
						t.w.addImport("github.com/i2y/ramune/jsrt", "")
						t.w.write("jsrt.Ptr(")
						t.emitExpr(ret.Expression)
						t.w.write(")")
					} else {
						// Capture and check if expression produces any at Go level
						code := t.captureExpr(ret.Expression)
						t.w.write(code)
						if t.currentRetType != "" && t.currentRetType != "any" &&
							(goCodeProducesAny(code) ||
								(retExprType.IsAny() && ret.Expression.Kind == ast.KindCallExpression)) {
							t.w.writef(".(%s)", t.currentRetType)
						}
					}
					t.w.write(")")
				}
				t.w.newline()
			}
			t.w.writeln("return")
		} else if t.tryResultVar != "" && ret.Expression != nil {
			// Inside try/catch IIFE: assign to result var instead of returning from IIFE
			t.returnContext = t.currentRetType
			// Check if expression produces any at Go level
			needsTryAssert := false
			if t.currentRetType != "" && t.currentRetType != "any" {
				retExprType := t.getGoType(ret.Expression)
				declRetExprType := t.getDeclaredGoType(ret.Expression)
				if retExprType.IsAny() || declRetExprType.IsAny() {
					needsTryAssert = true
				}
				// Call on any-typed variable produces any
				if !needsTryAssert && ret.Expression.Kind == ast.KindCallExpression {
					call := ret.Expression.AsCallExpression()
					if call.Expression.Kind == ast.KindIdentifier {
						vn := goVarName(call.Expression.AsIdentifier().Text)
						if t.goAnyVars != nil && t.goAnyVars[vn] {
							needsTryAssert = true
						}
					}
				}
			}
			t.w.writef("%s = ", t.tryResultVar)
			t.emitExpr(ret.Expression)
			if needsTryAssert {
				t.w.writef(".(%s)", t.currentRetType)
			}
			t.returnContext = ""
			t.w.newline()
			t.w.writeln("return")
		} else if ret.Expression != nil {
			t.w.write("return ")
			t.returnContext = t.currentRetType
			// Wrap with promise.Resolve when returning non-promise where Promise is expected
			if strings.HasPrefix(t.currentRetType, "*promise.Promise[") {
				retExprT := t.getGoType(ret.Expression)
				if !retExprT.IsPromise() {
					t.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
					t.w.write("promise.Resolve[any](")
					t.emitExpr(ret.Expression)
					t.w.write(")")
					t.returnContext = ""
					t.w.newline()
					break
				}
			}
			// Wrap with & when returning string/float64/bool but function expects *T
			if strings.HasPrefix(t.currentRetType, "*") {
				inner := t.currentRetType[1:]
				retExprT := t.getGoType(ret.Expression)
				if retExprT.GoStr == inner {
					t.w.addImport("github.com/i2y/ramune/jsrt", "")
					t.w.write("jsrt.Ptr(")
					t.emitExpr(ret.Expression)
					t.w.write(")")
					t.returnContext = ""
					t.w.newline()
					break
				}
			}
			if t.currentRetType == "float64" && t.isIntExpr(ret.Expression) {
				t.w.write("float64(")
				t.emitExpr(ret.Expression)
				t.w.write(")")
			} else {
				retExprType := t.getGoType(ret.Expression)
				declRetExprType := t.getDeclaredGoType(ret.Expression)
				isRetExprAny := retExprType.IsAny() || declRetExprType.IsAny()
				// Check goAnyVars for identifiers
				if !isRetExprAny && ret.Expression.Kind == ast.KindIdentifier {
					vn := goVarName(ret.Expression.AsIdentifier().Text)
					if t.goAnyVars != nil && t.goAnyVars[vn] {
						isRetExprAny = true
					}
				}
				needsRetAssert := isRetExprAny && t.currentRetType != "" && t.currentRetType != "any"
				// Don't assert on object/array literals or nil — they're concrete values, not interfaces
				if needsRetAssert && (ret.Expression.Kind == ast.KindObjectLiteralExpression ||
					ret.Expression.Kind == ast.KindArrayLiteralExpression ||
					ret.Expression.Kind == ast.KindNullKeyword ||
					ret.Expression.Kind == ast.KindUndefinedKeyword) {
					needsRetAssert = false
				}
				if needsRetAssert {
					t.emitExpr(ret.Expression)
					t.w.writef(".(%s)", t.currentRetType)
				} else if t.currentRetType != "" && t.currentRetType != "any" {
					// Capture and check if expression produces any at Go level
					code := t.captureExpr(ret.Expression)
					t.w.write(code)
					if goCodeProducesAny(code) && !strings.HasPrefix(code, "promise.") {
						t.w.writef(".(%s)", t.currentRetType)
					}
				} else {
					// currentRetType is empty — still check if code produces any and
					// there's a returnContext from the enclosing scope
					code := t.captureExpr(ret.Expression)
					t.w.write(code)
					if t.returnContext != "" && t.returnContext != "any" && goCodeProducesAny(code) {
						t.w.writef(".(%s)", t.returnContext)
					}
				}
			}
			t.returnContext = ""
			t.w.newline()
		} else {
			// Bare return: if function returns pointer/interface, emit "return nil"
			if strings.HasPrefix(t.currentRetType, "*") || t.currentRetType == "any" || t.currentRetType == "error" {
				t.w.writeln("return nil")
			} else {
				t.w.writeln("return")
			}
		}

	case ast.KindIfStatement:
		t.emitIfStatement(node)

	case ast.KindWhileStatement:
		t.emitWhileStatement(node)

	case ast.KindForStatement:
		t.emitForStatement(node)

	case ast.KindForInStatement, ast.KindForOfStatement:
		t.emitForInOfStatement(node)

	case ast.KindBlock:
		t.emitBlock(node)

	case ast.KindBreakStatement:
		bs := node.AsBreakStatement()
		if bs.Label != nil {
			t.w.writelnf("break %s", bs.Label.AsIdentifier().Text)
		} else {
			t.w.writeln("break")
		}

	case ast.KindContinueStatement:
		cs := node.AsContinueStatement()
		if cs.Label != nil {
			t.w.writelnf("continue %s", cs.Label.AsIdentifier().Text)
		} else {
			t.w.writeln("continue")
		}

	case ast.KindLabeledStatement:
		ls := node.AsLabeledStatement()
		t.w.writelnf("%s:", ls.Label.AsIdentifier().Text)
		t.emitStatement(ls.Statement)

	case ast.KindThrowStatement:
		throw := node.AsThrowStatement()
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.write("jsrt.Throw(")
		t.emitExpr(throw.Expression)
		t.w.write(")")
		t.w.newline()

	case ast.KindTryStatement:
		t.emitTryStatement(node)

	case ast.KindSwitchStatement:
		t.emitSwitchStatement(node)

	case ast.KindFunctionDeclaration:
		t.emitFunctionDeclaration(node)

	case ast.KindClassDeclaration:
		t.emitClassDeclaration(node)

	case ast.KindInterfaceDeclaration:
		t.emitInterfaceDeclaration(node)

	case ast.KindTypeAliasDeclaration:
		t.emitTypeAliasDeclaration(node)

	case ast.KindImportDeclaration:
		t.emitImportDeclaration(node)

	case ast.KindExportDeclaration:
		t.emitExportDeclaration(node)
		return

	case ast.KindExportAssignment:
		t.emitExportAssignment(node)
		return

	case ast.KindEnumDeclaration:
		t.emitEnumDeclaration(node)

	default:
		t.w.writelnf("/* unsupported statement kind: %s */", node.Kind.String())
	}
}

// emitBlock emits a Go block { ... }.
// Function declarations are hoisted: a forward declaration (var name func(...)) is placed
// at the top of the block, and the actual assignment stays at the original position.
func (t *Transpiler) emitBlock(node *ast.Node) {
	block := node.AsBlock()
	t.w.openBlock()
	if block.Statements != nil {
		// Emit forward declarations for hoisted functions
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
		// Emit all statements in order (function declarations become assignments)
		for _, stmt := range block.Statements.Nodes {
			if stmt.Kind == ast.KindFunctionDeclaration {
				t.emitFunctionDeclAssignment(stmt)
			} else {
				t.emitStatement(stmt)
			}
		}
		// Add default return for function body blocks (top-level only, not inner blocks)
		if t.needsDefaultReturn && t.currentRetType != "" && !t.inAsyncBody {
			lastIsReturn := false
			if len(block.Statements.Nodes) > 0 {
				last := block.Statements.Nodes[len(block.Statements.Nodes)-1]
				lastIsReturn = last.Kind == ast.KindReturnStatement
			}
			if !lastIsReturn {
				t.w.writeln(t.defaultReturn())
			}
			t.needsDefaultReturn = false
		}
	}
	t.w.closeBlock()
}

// blockContainsReturn checks if a block contains a return statement (shallow check).
func (t *Transpiler) blockContainsReturn(node *ast.Node) bool {
	if node == nil || node.Kind != ast.KindBlock {
		return false
	}
	block := node.AsBlock()
	if block.Statements == nil {
		return false
	}
	for _, stmt := range block.Statements.Nodes {
		if stmt.Kind == ast.KindReturnStatement {
			return true
		}
	}
	return false
}

// defaultReturn returns the appropriate default return statement for the current return type.
func (t *Transpiler) defaultReturn() string {
	switch t.currentRetType {
	case "string":
		return "return \"\""
	case "float64":
		return "return 0"
	case "int":
		return "return 0"
	case "bool":
		return "return false"
	default:
		return "return nil"
	}
}

// emitFunctionDeclAssignment emits a function declaration as an assignment to a pre-declared variable.
// Used for hoisted function declarations inside blocks.
func (t *Transpiler) emitFunctionDeclAssignment(node *ast.Node) {
	name := node.Name()
	if name == nil {
		return
	}
	funcName := goVarName(nodeText(name))
	t.w.writef("%s = func(", funcName)
	t.emitParameterList(node)
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)
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

	body := node.Body()
	if body != nil {
		savedRetType := t.currentRetType
		t.currentRetType = retType
		t.emitBlock(body)
		t.currentRetType = savedRetType
	} else {
		t.w.writeln(" {}")
	}
	t.w.newline()
}

// emitPackageLevelVarStatement emits an exported variable statement as package-level var declarations.
func (t *Transpiler) emitPackageLevelVarStatement(node *ast.Node) {
	varStmt := node.AsVariableStatement()
	declList := varStmt.DeclarationList.AsVariableDeclarationList()
	if declList.Declarations == nil {
		return
	}
	for _, decl := range declList.Declarations.Nodes {
		varDecl := decl.AsVariableDeclaration()
		name := decl.Name()
		if name == nil || name.Kind != ast.KindIdentifier {
			continue
		}
		rawName := name.AsIdentifier().Text
		varName := goVarName(rawName)
		if isExported(node) {
			varName = goExportedName(rawName)
			// Track exported names so same-file references use exported Go casing
			if t.samePackageExports == nil {
				t.samePackageExports = make(map[string]bool)
			}
			t.samePackageExports[rawName] = true
		}

		if varDecl.Initializer != nil {
			// Function initializers → emit as func declaration to avoid init cycle
			if varDecl.Initializer.Kind == ast.KindArrowFunction || varDecl.Initializer.Kind == ast.KindFunctionExpression {
				// Inject name and track param types for self-call detection
				t.pendingFuncName = varName
				savedFuncName := t.currentFuncName
				savedFuncParams := t.currentFuncParamTypes
				t.currentFuncName = varName
				// Collect Go param types from the implementation
				t.currentFuncParamTypes = nil
				if t.ck != nil {
					funcType := t.ck.GetTypeAtLocation(varDecl.Initializer)
					if funcType != nil {
						sigs := t.ck.GetSignaturesOfType(funcType, checker.SignatureKindCall)
						if len(sigs) > 0 {
							for _, p := range sigs[0].Parameters() {
								pt := t.ck.GetTypeOfSymbol(p)
								t.currentFuncParamTypes = append(t.currentFuncParamTypes, t.tm.goType(pt))
							}
						}
					}
				}
				t.emitExpr(varDecl.Initializer)
				t.pendingFuncName = ""
				t.currentFuncName = savedFuncName
				t.currentFuncParamTypes = savedFuncParams
				t.w.newline()
			} else {
				t.w.writef("var %s = ", varName)
				t.emitExpr(varDecl.Initializer)
				t.w.newline()
			}
		} else {
			goType := "any"
			if t.ck != nil {
				typ := t.ck.GetTypeAtLocation(decl)
				if typ != nil {
					goType = t.tm.goType(typ)
				}
			}
			if goType == "" {
				goType = "any"
			}
			t.w.writelnf("var %s %s", varName, goType)
		}
	}
	t.w.newline()
}

// emitVariableStatement handles const/let/var declarations.
func (t *Transpiler) emitVariableStatement(node *ast.Node) {
	varStmt := node.AsVariableStatement()
	declList := varStmt.DeclarationList.AsVariableDeclarationList()

	if declList.Declarations == nil {
		return
	}

	for _, decl := range declList.Declarations.Nodes {
		varDecl := decl.AsVariableDeclaration()
		name := decl.Name()
		if name == nil {
			continue
		}

		if name.Kind == ast.KindObjectBindingPattern {
			t.emitObjectDestructuring(name, varDecl.Initializer)
			continue
		}
		if name.Kind == ast.KindArrayBindingPattern {
			t.emitArrayDestructuring(name, varDecl.Initializer)
			continue
		}

		varName := ""
		if name.Kind == ast.KindIdentifier {
			rawName := name.AsIdentifier().Text
			varName = goVarName(rawName)
			// Track exported names so same-file references use exported Go casing
			if isExported(node) {
				varName = goExportedName(rawName)
				if t.samePackageExports == nil {
					t.samePackageExports = make(map[string]bool)
				}
				t.samePackageExports[rawName] = true
			}
		} else {
			t.w.writelnf("/* unsupported binding pattern: %s */", name.Kind.String())
			continue
		}

		if varDecl.Initializer != nil {
			needsFloat := t.isNumberTypedDecl(decl, varDecl)

			// Get checker's type for the variable and the initializer
			varTypeInfo := t.getGoType(decl)
			initTypeInfo := t.getGoType(varDecl.Initializer)

			// If checker says variable is concrete but the Go expression produces any,
			// use typed variable declaration with type assertion.
			// Cases where Go produces any even though checker says concrete:
			// - Ternary IIFE: func() any{...}()
			// - JSObject chain: jsrt.Obj(...).Get(...).Unwrap()
			// - Conditional expressions, call chains on any objects
			initProducesAny := initTypeInfo.IsAny()
			if !initProducesAny {
				// Check if the initializer is emitted as an IIFE returning any
				if varDecl.Initializer.Kind == ast.KindConditionalExpression {
					// Only mark as any-producing if the branches don't both produce int
					cond := varDecl.Initializer.AsConditionalExpression()
					if !(t.exprProducesInt(cond.WhenTrue) && t.exprProducesInt(cond.WhenFalse)) {
						initProducesAny = true
					}
				} else if varDecl.Initializer.Kind == ast.KindAwaitExpression {
					// await Promise.all(...) → AllSlice returns []any, let type be inferred
					aw := varDecl.Initializer.AsAwaitExpression()
					if !isPromiseAllCall(aw.Expression) {
						initProducesAny = true
					}
				}
				// Check if initializer is a call on an any-declared object
				if varDecl.Initializer.Kind == ast.KindCallExpression {
					call := varDecl.Initializer.AsCallExpression()
					if call.Expression.Kind == ast.KindPropertyAccessExpression {
						prop := call.Expression.AsPropertyAccessExpression()
						declObjType := t.getDeclaredGoType(prop.Expression)
						if declObjType.IsAny() {
							initProducesAny = true
						}
					}
				}
			}
			// Force []byte type for new Uint8Array/ArrayBuffer constructors
			isUint8ArrayCtor := false
			if varDecl.Initializer != nil && varDecl.Initializer.Kind == ast.KindNewExpression {
				ne := varDecl.Initializer.AsNewExpression()
				if ne.Expression.Kind == ast.KindIdentifier {
					ctorName := ne.Expression.AsIdentifier().Text
					if ctorName == "Uint8Array" || ctorName == "ArrayBuffer" {
						isUint8ArrayCtor = true
						varTypeInfo = GoTypeInfo{Category: GoTypeSlice, GoStr: "[]byte", ElemType: "byte"}
						// Track as concrete slice so element access uses direct indexing, not jsrt.Index
						if t.concreteVarTypes == nil {
							t.concreteVarTypes = make(map[string]string)
						}
						t.concreteVarTypes[varName] = "[]byte"
					}
				}
			}

			needsTypedDecl := !varTypeInfo.IsAny() && varTypeInfo.GoStr != "" &&
				initProducesAny && !isDirectLiteral(varDecl.Initializer) && !isUint8ArrayCtor

			// When a typed declaration shadows an any-typed variable, remove from goAnyVars
			if needsTypedDecl && t.goAnyVars != nil {
				delete(t.goAnyVars, varName)
			}

			// Detect function initializers that may reference the variable (init cycle).
			// Use var+assign pattern for all exported function vars to avoid Go init cycle.
			isSelfRefFunc := false
			if varDecl.Initializer != nil &&
				(varDecl.Initializer.Kind == ast.KindArrowFunction || varDecl.Initializer.Kind == ast.KindFunctionExpression) &&
				isExported(node) {
				isSelfRefFunc = true
			}

			if isSelfRefFunc {
				// Emit: var Name func(params) retType; Name = func(params) retType { ... }
				retType := t.getFuncReturnType(varDecl.Initializer)
				t.w.writef("var %s func(", varName)
				t.emitParameterList(varDecl.Initializer)
				t.w.write(")")
				if retType != "" {
					t.w.writef(" %s", retType)
				}
				t.w.newline()
				t.w.writef("%s = ", varName)
			} else if needsTypedDecl {
				// var name Type = expr.(Type)
				t.w.writef("var %s %s = ", varName, varTypeInfo.GoStr)
			} else {
				t.w.writef("%s := ", varName)
			}

			// Set declaration context only for direct array/object literal initializers
			if isDirectLiteral(varDecl.Initializer) && t.ck != nil {
				declType := t.ck.GetTypeAtLocation(decl)
				if declType != nil {
					t.declContext = t.tm.goType(declType)
				}
			}

			// Capture initializer to check if it produces any at Go level
			initCode := ""
			if needsTypedDecl {
				initCode = t.captureExpr(varDecl.Initializer)
			}

			if needsFloat && isIntegerLiteral(varDecl.Initializer) {
				t.w.write("float64(")
				t.emitExpr(varDecl.Initializer)
				t.w.write(")")
			} else if needsTypedDecl && initCode != "" {
				// Write captured code
				t.w.write(initCode)
				// Add type assertion only if emitted code actually produces any
				// Only add type assertion if the expression ENDS with an any-producing pattern
				if strings.HasSuffix(initCode, ".Unwrap()") ||
					strings.HasSuffix(initCode, "}()") && strings.Contains(initCode, "func() any") {
					t.w.writef(".(%s)", varTypeInfo.GoStr)
				}
			} else {
				t.emitExpr(varDecl.Initializer)
			}

			t.declContext = ""

			// Track variables initialized from int-producing calls (indexOf, lastIndexOf, etc.)
			if varName != "" && t.exprProducesInt(varDecl.Initializer) {
				if t.intVars == nil {
					t.intVars = make(map[string]bool)
				}
				t.intVars[varName] = true
			}

			// Track local variables initialized from *string-returning method calls
			// Only track when the call is on a method of the same class (transpiled, not native)
			if varName != "" && varDecl.Initializer != nil &&
				varDecl.Initializer.Kind == ast.KindCallExpression {
				call := varDecl.Initializer.AsCallExpression()
				if call.Expression.Kind == ast.KindPropertyAccessExpression {
					prop := call.Expression.AsPropertyAccessExpression()
					if prop.Expression.Kind == ast.KindThisKeyword {
						retType := t.getGoType(varDecl.Initializer)
						if retType.IsPointer() && retType.GoStr == "*string" {
							if t.goPtrStringVars == nil {
								t.goPtrStringVars = make(map[string]bool)
							}
							t.goPtrStringVars[varName] = true
						}
					}
				}
			}

			// When := produces a concrete type, remove any shadowed goAnyVar
			if varName != "" && !needsTypedDecl && varDecl.Initializer != nil {
				captured := t.captureExpr(varDecl.Initializer)
				if !goCodeProducesAny(captured) && !strings.Contains(captured, ".Then(") {
					if t.goAnyVars != nil {
						delete(t.goAnyVars, varName)
					}
				}
			}

			// Track variables initialized from any-producing expressions at Go level
			// Only for non-typed declarations (`:=` syntax) — typed decls keep their Go type
			if varName != "" && varDecl.Initializer != nil && !needsTypedDecl {
				initCode := t.captureExpr(varDecl.Initializer)
				if strings.Contains(initCode, ".Then(") || goCodeProducesAny(initCode) {
					if t.goAnyVars == nil {
						t.goAnyVars = make(map[string]bool)
					}
					t.goAnyVars[varName] = true
				}
			}

			// Track await Promise.all results as []any at Go level
			if varName != "" && varDecl.Initializer != nil && varDecl.Initializer.Kind == ast.KindAwaitExpression {
				aw := varDecl.Initializer.AsAwaitExpression()
				if isPromiseAllCall(aw.Expression) {
					if t.concreteVarTypes == nil {
						t.concreteVarTypes = make(map[string]string)
					}
					t.concreteVarTypes[varName] = "[]any"
				}
			}

			// Track variables from []any element access as Go-level any
			if varName != "" && varDecl.Initializer != nil && varDecl.Initializer.Kind == ast.KindElementAccessExpression {
				ea := varDecl.Initializer.AsElementAccessExpression()
				if ea.Expression.Kind == ast.KindIdentifier {
					arrVarName := goVarName(ea.Expression.AsIdentifier().Text)
					if t.concreteVarTypes != nil && t.concreteVarTypes[arrVarName] == "[]any" {
						if t.goAnyVars == nil {
							t.goAnyVars = make(map[string]bool)
						}
						t.goAnyVars[varName] = true
					}
				}
			}

			t.w.newline()
		} else {
			// var x: Type → var x Type
			goType := "any"
			if t.ck != nil {
				declType := t.ck.GetTypeAtLocation(decl)
				if declType != nil {
					goType = t.tm.goType(declType)
				}
			}
			if goType == "" {
				goType = "any"
			}
			// Track any-declared variables in goAnyVars for downstream type assertions
			if goType == "any" {
				if t.goAnyVars == nil {
					t.goAnyVars = make(map[string]bool)
				}
				t.goAnyVars[varName] = true
			}
			t.w.writelnf("var %s %s", varName, goType)
		}
	}
}

// emitIfStatement handles if/else if/else chains.
func (t *Transpiler) emitIfStatement(node *ast.Node) {
	ifStmt := node.AsIfStatement()

	// Detect discriminant narrowing: if (shape.kind === "circle") → narrow shape to *Circle
	varName, concreteType := t.detectDiscriminantCheck(ifStmt.Expression)
	// Also detect instanceof narrowing: if (x instanceof Foo) → narrow x to *Foo
	if varName == "" {
		varName, concreteType = t.detectInstanceofNarrowing(ifStmt.Expression)
	}

	t.w.write("if ")
	t.emitCondition(ifStmt.Expression)

	// Set narrowing for the then-branch
	savedNarrowed := t.narrowedTypes
	if varName != "" && concreteType != "" {
		t.narrowedTypes = copyNarrowMap(savedNarrowed)
		t.narrowedTypes[varName] = concreteType
	}

	t.emitBlockOrStmtInline(ifStmt.ThenStatement)

	// Restore narrowing after then-branch
	t.narrowedTypes = savedNarrowed

	if ifStmt.ElseStatement != nil {
		if ifStmt.ElseStatement.Kind == ast.KindIfStatement {
			t.w.write(" else ")
			t.emitIfStatement(ifStmt.ElseStatement)
		} else {
			t.w.write(" else")
			t.emitBlockOrStmt(ifStmt.ElseStatement)
		}
	} else {
		t.w.newline()
	}
}

// detectInstanceofNarrowing detects if (x instanceof Foo) and returns (varName, "*Foo") for type narrowing.
// Only narrows to types in the current package to avoid import cycles.
func (t *Transpiler) detectInstanceofNarrowing(node *ast.Node) (string, string) {
	if node == nil || node.Kind != ast.KindBinaryExpression {
		return "", ""
	}
	bin := node.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindInstanceOfKeyword {
		return "", ""
	}
	if bin.Left.Kind != ast.KindIdentifier || bin.Right.Kind != ast.KindIdentifier {
		return "", ""
	}
	varName := bin.Left.AsIdentifier().Text
	className := bin.Right.AsIdentifier().Text
	// Only narrow to types in the current package (not imported) to avoid import cycles
	if _, ok := t.importedNames[className]; ok {
		return "", "" // imported type — skip narrowing to avoid cycle
	}
	return varName, "*" + goTypeName(className)
}

// detectDiscriminantCheck checks if an expression is a discriminant check pattern:
// expr.field === "literal" → returns (varName, concreteGoType)
func (t *Transpiler) detectDiscriminantCheck(node *ast.Node) (string, string) {
	if node == nil || t.ck == nil {
		return "", ""
	}
	if node.Kind != ast.KindBinaryExpression {
		return "", ""
	}
	bin := node.AsBinaryExpression()
	op := bin.OperatorToken.Kind
	if op != ast.KindEqualsEqualsEqualsToken && op != ast.KindEqualsEqualsToken {
		return "", ""
	}

	// Left: expr.field, Right: "literal"
	var propAccess *ast.PropertyAccessExpression
	var literal string
	if bin.Left.Kind == ast.KindPropertyAccessExpression && bin.Right.Kind == ast.KindStringLiteral {
		propAccess = bin.Left.AsPropertyAccessExpression()
		literal = bin.Right.AsStringLiteral().Text
	} else if bin.Right.Kind == ast.KindPropertyAccessExpression && bin.Left.Kind == ast.KindStringLiteral {
		propAccess = bin.Right.AsPropertyAccessExpression()
		literal = bin.Left.AsStringLiteral().Text
	} else {
		return "", ""
	}

	// The object must be an identifier
	if propAccess.Expression.Kind != ast.KindIdentifier {
		return "", ""
	}
	varName := propAccess.Expression.AsIdentifier().Text

	// Get the declared type (not narrowed) — need union for discriminant matching
	sym := t.ck.GetSymbolAtLocation(propAccess.Expression)
	if sym == nil {
		return "", ""
	}
	objType := t.ck.GetTypeOfSymbol(sym)
	if objType == nil {
		return "", ""
	}

	// If the type is not a union (e.g., already narrowed), try to find the original union
	if objType.Flags()&checker.TypeFlagsUnion == 0 {
		// Check if it's a single object type with the discriminant field — narrow directly
		if objType.Flags()&checker.TypeFlagsObject != 0 {
			fieldName := nodeText(propAccess.Name())
			props := t.ck.GetPropertiesOfType(objType)
			for _, p := range props {
				if p.Name == fieldName {
					pt := t.ck.GetTypeOfSymbol(p)
					if pt != nil && pt.Flags()&checker.TypeFlagsStringLiteral != 0 {
						litStr := strings.Trim(t.ck.TypeToString(pt), "\"")
						if litStr == literal {
							s := objType.Symbol()
							if s != nil {
								return varName, goTypeName(s.Name)
							}
						}
					}
				}
			}
		}
		return "", ""
	}

	fieldName := nodeText(propAccess.Name())
	union := objType.AsUnionType()
	for _, member := range union.Types() {
		if member.Flags()&checker.TypeFlagsObject == 0 {
			continue
		}
		props := t.ck.GetPropertiesOfType(member)
		for _, p := range props {
			if p.Name == fieldName {
				pt := t.ck.GetTypeOfSymbol(p)
				if pt != nil && pt.Flags()&checker.TypeFlagsStringLiteral != 0 {
					// Check if the string literal type matches our literal value
					litStr := t.ck.TypeToString(pt)
					// TypeToString returns quoted: `"circle"` — strip quotes
					litStr = strings.Trim(litStr, "\"")
					if litStr == literal {
						sym := member.Symbol()
						if sym != nil {
							return varName, goTypeName(sym.Name)
						}
					}
				}
			}
		}
	}

	return "", ""
}

// copyNarrowMap creates a shallow copy of a narrowing map (or creates a new one).
func copyNarrowMap(m map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		result[k] = v
	}
	return result
}

// emitBlockOrStmt wraps a single statement in a block if needed (with trailing newline).
func (t *Transpiler) emitBlockOrStmt(node *ast.Node) {
	if node == nil {
		t.w.writeln(" {}")
		return
	}
	if node.Kind == ast.KindBlock {
		t.emitBlock(node)
	} else {
		t.w.openBlock()
		t.emitStatement(node)
		t.w.closeBlock()
	}
}

// emitBlockOrStmtInline wraps in a block, but does NOT write trailing newline.
// This is needed for if/else chains where "}" must be on the same line as "else".
func (t *Transpiler) emitBlockOrStmtInline(node *ast.Node) {
	if node == nil {
		t.w.write(" {}")
		return
	}
	if node.Kind == ast.KindBlock {
		t.emitBlockInline(node)
	} else {
		t.w.openBlock()
		t.emitStatement(node)
		t.w.closeBlockInline()
	}
}

// emitBlockInline emits a block WITHOUT trailing newline on the closing brace.
func (t *Transpiler) emitBlockInline(node *ast.Node) {
	block := node.AsBlock()
	t.w.openBlock()
	if block.Statements != nil {
		for _, stmt := range block.Statements.Nodes {
			t.emitStatement(stmt)
		}
	}
	t.w.closeBlockInline()
}

// emitWhileStatement handles while(cond) { ... }.
func (t *Transpiler) emitWhileStatement(node *ast.Node) {
	ws := node.AsWhileStatement()

	// Special case: while(i--) or while(i++) — postfix as condition
	// Go's i--/i++ are statements, not expressions. Rewrite to: for { i--; if i == 0 { break }; ... }
	if ws.Expression.Kind == ast.KindPostfixUnaryExpression {
		postfix := ws.Expression.AsPostfixUnaryExpression()
		t.w.write("for ")
		t.w.openBlock()
		t.emitExpr(postfix.Operand)
		if postfix.Operator == ast.KindMinusMinusToken {
			t.w.write("--")
		} else {
			t.w.write("++")
		}
		t.w.newline()
		t.w.write("if ")
		t.emitExpr(postfix.Operand)
		t.w.write(" == 0 { break }")
		t.w.newline()
		// Emit body statements inline
		if ws.Statement != nil && ws.Statement.Kind == ast.KindBlock {
			block := ws.Statement.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					t.emitStatement(stmt)
				}
			}
		} else if ws.Statement != nil {
			t.emitStatement(ws.Statement)
		}
		t.w.closeBlock()
		t.w.newline()
		return
	}

	t.w.write("for ")
	t.emitExpr(ws.Expression)
	t.emitBlockOrStmt(ws.Statement)
	t.w.newline()
}

// emitForStatement handles for(init; cond; inc) { ... }.
func (t *Transpiler) emitForStatement(node *ast.Node) {
	fs := node.AsForStatement()

	// Hoist extra init declarations (2nd, 3rd, ...) before the for-loop.
	// e.g., for (let i = 0, len = str.length; ...) → len_ := len(str) \n for i := 0; ...
	if fs.Initializer != nil && fs.Initializer.Kind == ast.KindVariableDeclarationList {
		declList := fs.Initializer.AsVariableDeclarationList()
		if declList.Declarations != nil && len(declList.Declarations.Nodes) > 1 {
			for k := 1; k < len(declList.Declarations.Nodes); k++ {
				extraDecl := declList.Declarations.Nodes[k].AsVariableDeclaration()
				extraName := declList.Declarations.Nodes[k].Name()
				if extraName != nil && extraName.Kind == ast.KindIdentifier {
					extraVarName := goVarName(extraName.AsIdentifier().Text)
					if t.intVars == nil {
						t.intVars = make(map[string]bool)
					}
					t.intVars[extraVarName] = true
					t.w.writef("%s := ", extraVarName)
					if extraDecl.Initializer != nil {
						t.emitExpr(extraDecl.Initializer)
					} else {
						t.w.write("0")
					}
					t.w.newline()
				}
			}
		}
	}

	t.w.write("for ")

	// Init
	if fs.Initializer != nil {
		if fs.Initializer.Kind == ast.KindVariableDeclarationList {
			declList := fs.Initializer.AsVariableDeclarationList()
			if declList.Declarations != nil && len(declList.Declarations.Nodes) > 0 {
				decl := declList.Declarations.Nodes[0].AsVariableDeclaration()
				name := declList.Declarations.Nodes[0].Name()
				if name != nil && name.Kind == ast.KindIdentifier {
					varName := goVarName(name.AsIdentifier().Text)
					// Track for-loop counter as int (scoped — cleaned up after loop body)
					if t.intVars == nil {
						t.intVars = make(map[string]bool)
					}
					wasInt := t.intVars[varName]
					t.intVars[varName] = true
					defer func(n string, prev bool) {
						if prev {
							t.intVars[n] = true
						} else {
							delete(t.intVars, n)
						}
					}(varName, wasInt)
					t.w.writef("%s := ", varName)
					if decl.Initializer != nil {
						t.emitExpr(decl.Initializer)
					} else {
						t.w.write("0")
					}
				}
			}
		} else {
			t.emitExpr(fs.Initializer)
		}
	}
	t.w.write("; ")

	// Condition
	if fs.Condition != nil {
		t.emitExpr(fs.Condition)
	}
	t.w.write("; ")

	// Incrementor — handle comma expressions by taking only the first part.
	// Extra increments are added at the end of the loop body.
	var extraIncrements []*ast.Node
	if fs.Incrementor != nil {
		inc := fs.Incrementor
		if inc.Kind == ast.KindBinaryExpression {
			bin := inc.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindCommaToken {
				t.emitExpr(bin.Left)
				extraIncrements = append(extraIncrements, bin.Right)
			} else {
				t.emitExpr(inc)
			}
		} else {
			t.emitExpr(inc)
		}
	}

	if len(extraIncrements) > 0 {
		// Emit body with extra increments at the end
		t.w.openBlock()
		if fs.Statement != nil && fs.Statement.Kind == ast.KindBlock {
			block := fs.Statement.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					t.emitStatement(stmt)
				}
			}
		} else if fs.Statement != nil {
			t.emitStatement(fs.Statement)
		}
		for _, extra := range extraIncrements {
			t.emitExpr(extra)
			t.w.newline()
		}
		t.w.closeBlock()
	} else {
		t.emitBlockOrStmt(fs.Statement)
	}
	t.w.newline()
}

// emitForInOfStatement handles for...in and for...of.
func (t *Transpiler) emitForInOfStatement(node *ast.Node) {
	fio := node.AsForInOrOfStatement()
	isAwait := fio.AwaitModifier != nil

	// Variable name
	var varName string
	var arrayDestructPattern *ast.Node // ArrayBindingPattern for destructuring
	if fio.Initializer != nil && fio.Initializer.Kind == ast.KindVariableDeclarationList {
		declList := fio.Initializer.AsVariableDeclarationList()
		if declList.Declarations != nil && len(declList.Declarations.Nodes) > 0 {
			name := declList.Declarations.Nodes[0].Name()
			if name != nil && name.Kind == ast.KindIdentifier {
				varName = goVarName(name.AsIdentifier().Text)
			} else if name != nil && name.Kind == ast.KindArrayBindingPattern {
				arrayDestructPattern = name
				varName = fmt.Sprintf("__entry%d", t.tmpVarCounter)
				t.tmpVarCounter++
			}
		}
	}

	t.w.write("for ")
	if varName != "" {
		if node.Kind == ast.KindForInStatement {
			t.w.writef("%s := range ", varName)
		} else if isAwait {
			t.w.write("_, __p := range ")
		} else {
			t.w.writef("_, %s := range ", varName)
		}
	}

	t.emitExpr(fio.Expression)

	if isAwait && varName != "" {
		// Wrap body: resolve promise at the start of each iteration
		t.w.openBlock()
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		actualVar := varName
		if arrayDestructPattern != nil {
			actualVar = "__p_resolved"
		}
		t.w.writef("%s := func() any { __v, __err := __p.Await(); if __err != nil { jsrt.Throw(__err) }; return __v }()", actualVar)
		t.w.newline()
		if arrayDestructPattern != nil {
			t.emitForOfDestructuring(arrayDestructPattern, actualVar)
		}
		// Emit the body statements inline (unwrap block)
		if fio.Statement != nil && fio.Statement.Kind == ast.KindBlock {
			block := fio.Statement.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					t.emitStatement(stmt)
				}
			}
		} else if fio.Statement != nil {
			t.emitStatement(fio.Statement)
		}
		t.w.closeBlock()
	} else if arrayDestructPattern != nil {
		// for...of with destructuring: emit block with destructuring at start
		t.w.openBlock()
		t.emitForOfDestructuring(arrayDestructPattern, varName)
		if fio.Statement != nil && fio.Statement.Kind == ast.KindBlock {
			block := fio.Statement.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					t.emitStatement(stmt)
				}
			}
		} else if fio.Statement != nil {
			t.emitStatement(fio.Statement)
		}
		t.w.closeBlock()
	} else {
		t.emitBlockOrStmt(fio.Statement)
	}
	t.w.newline()
}

// emitForOfDestructuring emits destructuring assignments for a for...of loop with ArrayBindingPattern.
// e.g., for (const [key, value] of entries) → key := __entry[0].(string); value := __entry[1]
func (t *Transpiler) emitForOfDestructuring(pattern *ast.Node, tmpVar string) {
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil {
		return
	}
	for i, elem := range bp.Elements.Nodes {
		if elem.Kind == ast.KindOmittedExpression {
			continue
		}
		elemName := elem.Name()
		if elemName == nil || elemName.Kind != ast.KindIdentifier {
			continue
		}
		localName := goVarName(elemName.AsIdentifier().Text)
		// Use checker type for the element to determine type assertion
		elemGoType := ""
		if t.ck != nil {
			typ := t.ck.GetTypeAtLocation(elemName)
			if typ != nil {
				elemGoType = t.tm.goType(typ)
			}
		}
		if elemGoType != "" && elemGoType != "any" {
			t.w.writef("%s := %s[%d].(%s)", localName, tmpVar, i, elemGoType)
		} else {
			t.w.writef("%s := %s[%d]", localName, tmpVar, i)
		}
		t.w.newline()
	}
}

// emitSwitchStatement handles switch(expr) { case: ... }.
func (t *Transpiler) emitSwitchStatement(node *ast.Node) {
	sw := node.AsSwitchStatement()
	t.w.write("switch ")
	t.emitExpr(sw.Expression)
	t.w.openBlock()

	if sw.CaseBlock != nil {
		caseBlock := sw.CaseBlock.AsCaseBlock()
		if caseBlock.Clauses != nil {
			for _, clause := range caseBlock.Clauses.Nodes {
				cc := clause.AsCaseOrDefaultClause()
				if cc.Expression != nil {
					t.w.write("case ")
					t.emitExpr(cc.Expression)
					t.w.writeln(":")
				} else {
					t.w.writeln("default:")
				}
				t.w.indent++
				if cc.Statements != nil {
					for _, stmt := range cc.Statements.Nodes {
						// Skip break statements — Go switches don't fall through
						if stmt.Kind == ast.KindBreakStatement {
							continue
						}
						t.emitStatement(stmt)
					}
				}
				t.w.indent--
			}
		}
	}

	t.w.closeBlock()
}

// emitObjectDestructuring handles: const { a, b } = expr → a := expr.A; b := expr.B
func (t *Transpiler) emitObjectDestructuring(pattern *ast.Node, initializer *ast.Node) {
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil || initializer == nil {
		return
	}

	tmpVar := fmt.Sprintf("__obj%d", t.tmpVarCounter)
	t.tmpVarCounter++
	t.w.writef("%s := ", tmpVar)
	t.emitExpr(initializer)
	t.w.newline()

	// Check if the initializer is any-typed → use jsrt.GetField for field access
	initIsAny := t.getGoType(initializer).IsAny()

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

		if initIsAny {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.writef("%s := jsrt.GetField(%s, %q)", localName, tmpVar, goExportedName(propName))
			// Track as Go-level any since jsrt.GetField returns any
			if t.goAnyVars == nil {
				t.goAnyVars = make(map[string]bool)
			}
			t.goAnyVars[localName] = true
		} else {
			t.w.writef("%s := %s.%s", localName, tmpVar, goExportedName(propName))
		}
		t.w.newline()

		// Default value: const { a = 1 } = obj → if a == zero { a = 1 }
		if be.Initializer != nil {
			t.emitDestructuringDefault(localName, be.Initializer)
		}
	}
}

// emitArrayDestructuring handles: const [a, b] = expr → a := expr[0]; b := expr[1]
// For go: import function calls, emits Go multi-return: a, b := fn()
func (t *Transpiler) emitArrayDestructuring(pattern *ast.Node, initializer *ast.Node) {
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil || initializer == nil {
		return
	}

	// Check if this is a call to a go: imported function → emit Go multi-return
	if t.isGoNativeCall(initializer) {
		var names []string
		for _, elem := range bp.Elements.Nodes {
			if elem.Kind == ast.KindOmittedExpression {
				names = append(names, "_")
				continue
			}
			elemName := elem.Name()
			if elemName == nil || elemName.Kind != ast.KindIdentifier {
				names = append(names, "_")
				continue
			}
			names = append(names, goVarName(elemName.AsIdentifier().Text))
		}
		t.w.writef("%s := ", strings.Join(names, ", "))
		t.emitExpr(initializer)
		t.w.newline()
		return
	}

	tmpVar := fmt.Sprintf("__arr%d", t.tmpVarCounter)
	t.tmpVarCounter++
	t.w.writef("%s := ", tmpVar)
	t.emitExpr(initializer)
	t.w.newline()

	// Check if the array produces []any at Go level (e.g., from Promise.all destructuring)
	initGoType := t.getGoType(initializer)
	arrayElemIsAny := initGoType.GoStr == "[]any" || initGoType.IsAny()

	// Determine if tmpVar is bare any (needs .([]any) before indexing)
	isInitAny := false
	if initializer.Kind == ast.KindElementAccessExpression {
		// Element access on []any variable → result is any
		ea := initializer.AsElementAccessExpression()
		arrDeclType := t.getDeclaredGoType(ea.Expression)
		if (arrDeclType.IsSlice() && arrDeclType.ElemType == "any") || arrDeclType.IsAny() {
			isInitAny = true
			arrayElemIsAny = true
		}
	} else if initGoType.IsAny() && initializer.Kind != ast.KindAwaitExpression {
		// Checker says any but skip await (IIFEs produce concrete types like []any)
		isInitAny = true
	}

	for i, elem := range bp.Elements.Nodes {
		if elem.Kind == ast.KindOmittedExpression {
			continue
		}
		elemName := elem.Name()
		if elemName == nil || elemName.Kind != ast.KindIdentifier {
			continue
		}
		localName := goVarName(elemName.AsIdentifier().Text)

		be := elem.AsBindingElement()
		concreteName := false

		if be.DotDotDotToken != nil {
			if isInitAny {
				t.w.writef("%s := %s.([]any)[%d:]", localName, tmpVar, i)
			} else {
				t.w.writef("%s := %s[%d:]", localName, tmpVar, i)
			}
		} else if isInitAny {
			// Initializer is any → need .([]any) before indexing
			// Check checker for concrete element type
			elemGoType := "any"
			if t.ck != nil {
				elemType := t.ck.GetTypeAtLocation(elemName)
				if elemType != nil {
					gt := t.tm.goType(elemType)
					if gt != "" && gt != "any" {
						elemGoType = gt
					}
				}
			}
			if elemGoType != "any" {
				t.w.writef("var %s %s = %s.([]any)[%d].(%s)", localName, elemGoType, tmpVar, i, elemGoType)
				concreteName = true
			} else {
				t.w.writef("%s := %s.([]any)[%d]", localName, tmpVar, i)
			}
		} else {
			t.w.writef("%s := %s[%d]", localName, tmpVar, i)
		}
		// Track variables from []any indexing as Go-level any
		if arrayElemIsAny && !concreteName {
			if t.goAnyVars == nil {
				t.goAnyVars = make(map[string]bool)
			}
			t.goAnyVars[localName] = true
		}
		t.w.newline()

		// Default value: const [a = 1] = arr → if a == zero { a = 1 }
		if be.Initializer != nil {
			t.emitDestructuringDefault(localName, be.Initializer)
		}
	}
}

// isGoNativeCall returns true if the expression is a call to a function from a go: imported package.
// Matches patterns: fn() where fn is a go: import, or pkg.Method() where pkg is a go: namespace.
func (t *Transpiler) isGoNativeCall(node *ast.Node) bool {
	if node == nil || node.Kind != ast.KindCallExpression {
		return false
	}
	call := node.AsCallExpression()
	expr := call.Expression

	// Direct call: fn() where fn is a named import from go: package
	if expr.Kind == ast.KindIdentifier {
		name := expr.AsIdentifier().Text
		if pkg, ok := t.importedNames[name]; ok {
			_, isNative := t.goNativeImports[pkg]
			return isNative
		}
		return false
	}

	// Property access: pkg.Method() where pkg is a go: namespace
	if expr.Kind == ast.KindPropertyAccessExpression {
		pa := expr.AsPropertyAccessExpression()
		if pa.Expression.Kind == ast.KindIdentifier {
			objName := pa.Expression.AsIdentifier().Text
			if pkg, ok := t.packageRefs[objName]; ok {
				_, isNative := t.goNativeImports[pkg]
				return isNative
			}
			_, isNative := t.goNativeImports[objName]
			return isNative
		}
	}
	return false
}

// emitTryStatement handles try/catch/finally → func() with defer/recover.
//
// TypeScript:
//
//	try { riskyOp() } catch (e) { handleError(e) } finally { cleanup() }
//
// Go:
//
//	func() {
//	    defer func() { cleanup() }()
//	    defer func() {
//	        if r := recover(); r != nil {
//	            e := jsrt.CatchValue(r)
//	            handleError(e)
//	        }
//	    }()
//	    riskyOp()
//	}()
func (t *Transpiler) emitTryStatement(node *ast.Node) {
	tryStmt := node.AsTryStatement()

	// If inside a function with a return type, use a result variable
	// so that return statements inside try/catch can propagate values.
	// Skip when inside async body — returns are already converted to __resolve().
	// Only declare when the try block actually contains a return statement.
	hasReturnType := t.currentRetType != "" && !t.inAsyncBody
	resultVar := ""
	if hasReturnType && t.blockContainsReturn(tryStmt.TryBlock) {
		resultVar = fmt.Sprintf("__tryResult%d", t.tmpVarCounter)
		t.tmpVarCounter++
		t.w.writef("var %s %s", resultVar, t.currentRetType)
		t.w.newline()
	}

	savedTryResult := t.tryResultVar
	t.tryResultVar = resultVar

	t.w.write("func()")
	t.w.openBlock()

	// Finally block (emitted first as defer, runs last)
	if tryStmt.FinallyBlock != nil {
		t.w.write("defer func()")
		t.w.openBlock()
		finallyBlock := tryStmt.FinallyBlock.AsBlock()
		if finallyBlock.Statements != nil {
			for _, stmt := range finallyBlock.Statements.Nodes {
				t.emitStatement(stmt)
			}
		}
		t.w.closeBlockInline()
		t.w.writeln("()")
	}

	// Catch clause (emitted as defer, runs on panic)
	if tryStmt.CatchClause != nil {
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		cc := tryStmt.CatchClause.AsCatchClause()
		t.w.write("defer func()")
		t.w.openBlock()
		t.w.write("if r := recover(); r != nil")
		t.w.openBlock()

		// Bind the catch variable
		catchVarName := "e"
		if cc.VariableDeclaration != nil {
			varName := cc.VariableDeclaration.Name()
			if varName != nil && varName.Kind == ast.KindIdentifier {
				catchVarName = goVarName(varName.AsIdentifier().Text)
			}
		}
		// Check if the catch variable is actually used in the body (recursive walk)
		catchVarUsed := false
		if cc.Block != nil {
			var walkUsage ast.Visitor
			walkUsage = func(n *ast.Node) bool {
				if n.Kind == ast.KindIdentifier && n.AsIdentifier().Text == catchVarName {
					catchVarUsed = true
					return true
				}
				return n.ForEachChild(walkUsage)
			}
			cc.Block.ForEachChild(walkUsage)
		}

		if catchVarUsed {
			t.w.writef("%s := jsrt.CatchValue(r)", catchVarName)
		} else {
			t.w.write("_ = jsrt.CatchValue(r)")
		}
		t.w.newline()

		// Catch body
		if cc.Block != nil {
			catchBlock := cc.Block.AsBlock()
			if catchBlock.Statements != nil {
				for _, stmt := range catchBlock.Statements.Nodes {
					t.emitStatement(stmt)
				}
			}
		}

		t.w.closeBlock() // close if
		t.w.closeBlockInline()
		t.w.writeln("()")
	}

	// Try block body
	if tryStmt.TryBlock != nil {
		tryBlock := tryStmt.TryBlock.AsBlock()
		if tryBlock.Statements != nil {
			for _, stmt := range tryBlock.Statements.Nodes {
				t.emitStatement(stmt)
			}
		}
	}

	t.w.closeBlockInline()
	t.w.writeln("()")

	t.tryResultVar = savedTryResult

	// Emit return only if the try or catch blocks contain return statements
	if resultVar != "" && containsReturn(tryStmt) {
		t.w.writef("return %s", resultVar)
		t.w.newline()
	}
}

// containsReturn checks if a try statement's try or catch blocks contain return statements.
func containsReturn(tryStmt *ast.TryStatement) bool {
	if hasReturn(tryStmt.TryBlock) {
		return true
	}
	if tryStmt.CatchClause != nil {
		cc := tryStmt.CatchClause.AsCatchClause()
		if hasReturn(cc.Block) {
			return true
		}
	}
	return false
}

// hasReturn checks if a block contains any return statement (recursive).
func hasReturn(block *ast.Node) bool {
	if block == nil {
		return false
	}
	found := false
	var walk ast.Visitor
	walk = func(n *ast.Node) bool {
		if n.Kind == ast.KindReturnStatement {
			found = true
			return true
		}
		// Don't recurse into nested functions
		if n.Kind == ast.KindFunctionDeclaration || n.Kind == ast.KindFunctionExpression ||
			n.Kind == ast.KindArrowFunction {
			return false
		}
		return n.ForEachChild(walk)
	}
	block.ForEachChild(walk)
	return found
}

// isDirectLiteral returns true if the node is an array or object literal
// (not a function call, variable reference, or other expression).
func isDirectLiteral(node *ast.Node) bool {
	if node == nil {
		return false
	}
	return node.Kind == ast.KindArrayLiteralExpression || node.Kind == ast.KindObjectLiteralExpression
}

// isNumberTypedDecl checks if a variable declaration has number type
// (either via explicit type annotation or checker resolution).
func (t *Transpiler) isNumberTypedDecl(decl *ast.Node, varDecl *ast.VariableDeclaration) bool {
	// Check explicit type annotation: let x: number = ...
	if varDecl.Type != nil {
		switch varDecl.Type.Kind {
		case ast.KindNumberKeyword:
			return true
		}
	}
	// Check via type checker
	if t.ck != nil {
		declType := t.ck.GetTypeAtLocation(decl)
		if declType != nil && declType.Flags()&checker.TypeFlagsNumberLike != 0 {
			return true
		}
	}
	return false
}

// isIntegerLiteral checks if a node is an integer numeric literal (no decimal point).
func isIntegerLiteral(node *ast.Node) bool {
	if node == nil || node.Kind != ast.KindNumericLiteral {
		return false
	}
	text := node.AsNumericLiteral().Text
	for _, c := range text {
		if c == '.' || c == 'e' || c == 'E' {
			return false
		}
	}
	return true
}

// emitExportAssignment handles: export default expr
// Emits a package-level variable: var Default = expr
// For function/class expressions, emits the declaration directly.
func (t *Transpiler) emitExportAssignment(node *ast.Node) {
	ea := node.AsExportAssignment()
	if ea.Expression == nil {
		return
	}

	switch ea.Expression.Kind {
	case ast.KindFunctionExpression, ast.KindArrowFunction:
		// export default function() {} → func Default(...) { ... }
		// Emit as a named function "Default"
		t.w.write("func Default(")
		if ea.Expression.Kind == ast.KindFunctionExpression {
			fe := ea.Expression.AsFunctionExpression()
			t.emitParameterList(ea.Expression)
			t.w.write(")")
			retType := t.getFuncReturnType(ea.Expression)
			if retType != "" {
				t.w.writef(" %s", retType)
			}
			if fe.Body != nil {
				t.emitBlock(fe.Body)
			} else {
				t.w.writeln(" {}")
			}
		} else {
			t.emitParameterList(ea.Expression)
			t.w.write(")")
			retType := t.getFuncReturnType(ea.Expression)
			if retType != "" {
				t.w.writef(" %s", retType)
			}
			arrow := ea.Expression.AsArrowFunction()
			if arrow.Body != nil {
				if arrow.Body.Kind == ast.KindBlock {
					t.emitBlock(arrow.Body)
				} else {
					t.w.openBlock()
					t.w.write("return ")
					t.emitExpr(arrow.Body)
					t.w.newline()
					t.w.closeBlock()
				}
			} else {
				t.w.writeln(" {}")
			}
		}
		t.w.newline()

	case ast.KindIdentifier:
		// export default myVar → var Default = myVar
		t.w.write("var Default = ")
		t.emitExpr(ea.Expression)
		t.w.newline()

	default:
		// export default { ... } or export default 42, etc.
		t.w.write("var Default = ")
		t.emitExpr(ea.Expression)
		t.w.newline()
	}
}

// emitExportDeclaration handles:
//   - export { X, Y } from './mod' → import + re-export aliases
//   - export { X, Y } → no-op (already exported via modifier flags)
func (t *Transpiler) emitExportDeclaration(node *ast.Node) {
	ed := node.AsExportDeclaration()

	// Type-only exports: skip
	if ed.IsTypeOnly {
		return
	}

	// export { X } (no from clause) — declarations are already exported via modifier flags
	if ed.ModuleSpecifier == nil {
		return
	}

	// export { X, Y } from './mod' — resolve as import + re-export
	var moduleSpec string
	if ed.ModuleSpecifier.Kind == ast.KindStringLiteral {
		moduleSpec = ed.ModuleSpecifier.AsStringLiteral().Text
	} else {
		return
	}

	if ed.ExportClause == nil {
		return
	}
	if ed.ExportClause.Kind != ast.KindNamedExports {
		return
	}
	named := ed.ExportClause.AsNamedExports()
	if named.Elements == nil {
		return
	}

	// Resolve the source module to a Go package path
	goImportPath, pkgAlias := t.resolveModuleSpec(moduleSpec)
	if goImportPath == "" {
		t.w.writelnf("// re-export from %q — not resolved", moduleSpec)
		return
	}
	t.w.addImport(goImportPath, pkgAlias)

	// For each export specifier, emit a package-level alias
	for _, spec := range named.Elements.Nodes {
		es := spec.AsExportSpecifier()
		if es.IsTypeOnly {
			continue
		}
		exportedName := spec.Name().AsIdentifier().Text
		// Original name (before 'as')
		originalName := exportedName
		if es.PropertyName != nil && es.PropertyName.Kind == ast.KindIdentifier {
			originalName = es.PropertyName.AsIdentifier().Text
		}
		goExported := goExportedName(exportedName)
		goOriginal := goExportedName(originalName)
		t.w.writef("var %s = %s.%s", goExported, pkgAlias, goOriginal)
		t.w.newline()
	}
}

// resolveModuleSpec resolves a module specifier to (goImportPath, alias).
func (t *Transpiler) resolveModuleSpec(moduleSpec string) (string, string) {
	moduleSpec = strings.TrimPrefix(moduleSpec, "node:")

	if goImport, ok := nodeModuleToGoImport[moduleSpec]; ok {
		alias := moduleSpec
		if i := strings.LastIndex(alias, "/"); i >= 0 {
			alias = alias[i+1:]
		}
		return goImport, strings.ReplaceAll(alias, "_", "")
	}
	if goImport, ok := npmToGoImport[moduleSpec]; ok {
		return goImport, filepath.Base(goImport)
	}
	if strings.HasPrefix(moduleSpec, ".") {
		cleanPath := moduleSpec
		cleanPath = strings.TrimPrefix(cleanPath, "./")
		cleanPath = strings.TrimSuffix(cleanPath, ".ts")
		cleanPath = strings.TrimSuffix(cleanPath, ".js")
		if t.currentFileDir != "" && t.currentFileDir != "." {
			cleanPath = filepath.Join(t.currentFileDir, cleanPath)
		}
		cleanPath = filepath.Clean(cleanPath)
		// Same-directory resolution: use the directory as Go package
		dir := filepath.Dir(cleanPath)
		if dir != "." && dir != "" {
			cleanPath = dir
		}
		pkgName := sanitizePkgName(filepath.Base(cleanPath))
		goImportPath := cleanPath
		if t.goModuleName != "" {
			goImportPath = t.goModuleName + "/" + cleanPath
		}
		return goImportPath, pkgName
	}
	if goImportPath, ok := strings.CutPrefix(moduleSpec, "go:"); ok {
		return goImportPath, filepath.Base(goImportPath)
	}
	return "", ""
}

// emitCondition emits an expression as a boolean condition.
// If the expression type is not boolean, wraps with jsrt.ToBool().
func (t *Transpiler) emitCondition(node *ast.Node) {
	if t.isBooleanCondition(node) {
		t.emitExpr(node)
	} else {
		t.w.addImport("github.com/i2y/ramune/jsrt", "")
		t.w.write("jsrt.ToBool(")
		t.emitExpr(node)
		t.w.write(")")
	}
}

// isBooleanCondition checks if an expression is inherently boolean.
func (t *Transpiler) isBooleanCondition(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindBinaryExpression:
		op := node.AsBinaryExpression().OperatorToken.Kind
		switch op {
		case ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken,
			ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken,
			ast.KindLessThanToken, ast.KindGreaterThanToken,
			ast.KindLessThanEqualsToken, ast.KindGreaterThanEqualsToken,
			ast.KindAmpersandAmpersandToken, ast.KindBarBarToken,
			ast.KindInstanceOfKeyword, ast.KindInKeyword:
			return true
		}
	case ast.KindPrefixUnaryExpression:
		if node.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
			return true
		}
	case ast.KindTrueKeyword, ast.KindFalseKeyword:
		return true
	case ast.KindCallExpression:
		// Function calls that return bool
		if t.ck != nil {
			typ := t.ck.GetTypeAtLocation(node)
			if typ != nil && t.tm.goType(typ) == "bool" {
				return true
			}
		}
	case ast.KindIdentifier:
		// Check goAnyVars first — if variable is any at Go level, it's NOT boolean
		vn := goVarName(node.AsIdentifier().Text)
		if t.goAnyVars != nil && t.goAnyVars[vn] {
			return false
		}
		if t.ck != nil {
			typ := t.ck.GetTypeAtLocation(node)
			if typ != nil && t.tm.goType(typ) == "bool" {
				return true
			}
		}
	}
	return false
}

// emitDestructuringDefault emits a zero-value check and assignment for a destructuring default.
// For any/interface types: if varName == nil { varName = default }
// For value types: uses the zero value of the type.
func (t *Transpiler) emitDestructuringDefault(varName string, defaultExpr *ast.Node) {
	// Always use nil check — the variable may be `any` at Go level
	// even if the checker says it's a concrete type (e.g., from any-typed destructuring).
	t.w.writef("if %s == nil { %s = ", varName, varName)
	t.emitExpr(defaultExpr)
	t.w.write(" }")
	t.w.newline()
}
