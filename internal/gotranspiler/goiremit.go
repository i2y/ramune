package gotranspiler

import (
	"fmt"
	"strings"
)

// IREmitter converts GOTIR nodes to Go source code.
// It is stateless with respect to type information — all type-driven decisions
// have already been made by the IRBuilder.
type IREmitter struct {
	w       *goWriter
	imports map[string]string // path → alias (from builder)
}

// NewIREmitter creates an emitter from a builder's state.
func NewIREmitter(builder *IRBuilder) *IREmitter {
	return &IREmitter{
		w:       newGoWriter(),
		imports: builder.imports,
	}
}

// EmitFile generates the final Go source for a file.
func (e *IREmitter) EmitFile(file *GoFile) (string, error) {
	// Emit declarations
	for _, decl := range file.Decls {
		e.EmitDecl(decl)
		e.w.newline()
	}
	// Emit package-level statements
	for _, stmt := range file.Stmts {
		e.EmitStmt(stmt)
	}

	// Merge builder imports into writer
	for path, alias := range e.imports {
		e.w.addImport(path, alias)
	}

	return e.w.renderFile(file.Package)
}

// Result returns the accumulated Go source (without package/imports header).
func (e *IREmitter) Result() string {
	return e.w.buf.String()
}

// --------------------------------------------------------------------
// Expression emission
// --------------------------------------------------------------------

// EmitExpr emits a Go expression from an IR node.
func (e *IREmitter) EmitExpr(expr GoExpr) {
	if expr == nil {
		e.w.write("nil")
		return
	}

	switch node := expr.(type) {
	case *IRIdent:
		e.emitIdent(node)
	case *IRLiteral:
		e.w.write(node.Value)
	case *IRBinaryOp:
		e.emitBinaryOp(node)
	case *IRUnaryOp:
		e.emitUnaryOp(node)
	case *IRCall:
		e.emitCall(node)
	case *IRMethodCall:
		e.emitMethodCall(node)
	case *IRFieldAccess:
		e.emitFieldAccess(node)
	case *IRIndexAccess:
		e.emitIndexAccess(node)
	case *IRTypeAssertion:
		e.emitTypeAssertion(node)
	case *IRTypeConversion:
		e.emitTypeConversion(node)
	case *IRFuncLit:
		e.emitFuncLit(node)
	case *IRStdlibCall:
		e.emitStdlibCall(node)
	case *IRSprintfCall:
		e.emitSprintfCall(node)
	case *IRMakeCall:
		e.emitMakeCall(node)
	case *IRSliceExpr:
		e.emitSliceExpr(node)
	case *IRAddrOf:
		e.w.write("&")
		e.EmitExpr(node.Expr)
	case *IRDeref:
		e.w.write("*")
		e.EmitExpr(node.Expr)
	case *IRNilCheck:
		e.emitNilCheck(node)
	case *IRTernary:
		e.emitTernary(node)
	case *IRAwait:
		e.emitAwait(node)
	case *IRTypeOf:
		e.emitTypeOf(node)
	case *IRInstanceOf:
		e.emitInstanceOf(node)
	case *IRJSRTCall:
		e.emitJSRTCall(node)
	case *IRRawExpr:
		e.w.write(node.Code)
	case *IRCompositeLit:
		e.emitCompositeLit(node)
	case *IRNewExpr:
		e.emitNewExpr(node)
	case *IRMultiValue:
		for i, v := range node.Exprs {
			if i > 0 {
				e.w.write(", ")
			}
			e.EmitExpr(v)
		}
	case *IRPromiseNew:
		e.emitPromiseNew(node)
	case *IRArrayMethodCall:
		e.emitArrayMethodCall(node)
	default:
		e.w.writef("/* unknown IR expr type: %T */", expr)
	}
}

func (e *IREmitter) emitIdent(node *IRIdent) {
	if node.PkgName != "" {
		e.w.writef("%s.%s", node.PkgName, node.Name)
	} else {
		e.w.write(node.Name)
	}
}

func (e *IREmitter) emitBinaryOp(node *IRBinaryOp) {
	e.EmitExpr(node.Left)
	e.w.writef(" %s ", node.Op)
	e.EmitExpr(node.Right)
}

func (e *IREmitter) emitUnaryOp(node *IRUnaryOp) {
	if node.Op == "()" {
		// Parenthesized expression
		e.w.write("(")
		e.EmitExpr(node.Operand)
		e.w.write(")")
		return
	}
	if node.Op == "..." {
		// Spread
		e.EmitExpr(node.Operand)
		e.w.write("...")
		return
	}
	if node.Postfix {
		e.EmitExpr(node.Operand)
		e.w.write(node.Op)
	} else {
		e.w.write(node.Op)
		e.EmitExpr(node.Operand)
	}
}

func (e *IREmitter) emitCall(node *IRCall) {
	e.EmitExpr(node.Func)
	e.w.write("(")
	e.emitExprList(node.Args)
	e.w.write(")")
}

func (e *IREmitter) emitMethodCall(node *IRMethodCall) {
	goMethod := goExportedName(node.Method)

	switch node.Strategy {
	case DispatchConcreteMethod, DispatchPromiseMethod, DispatchMapOperation:
		// Direct method call: obj.Method(args)
		e.EmitExpr(node.Object)
		e.w.writef(".%s(", goMethod)
		e.emitExprList(node.Args)
		e.w.write(")")

	case DispatchStringStdlib:
		// String methods → strings.* stdlib or inline
		e.emitStringMethod(node)

	case DispatchArrayHelper:
		// Array methods → jsarray.* helpers
		e.emitArrayMethod(node)

	case DispatchJSRTRuntime:
		// Any-typed → jsrt.Obj().Get().Call()
		e.w.addImport("github.com/i2y/ramune/jsrt", "")
		e.w.write("jsrt.Obj(")
		e.EmitExpr(node.Object)
		e.w.writef(").Get(%q).Call(", goMethod)
		e.emitExprList(node.Args)
		e.w.write(").Unwrap()")

	default:
		e.EmitExpr(node.Object)
		e.w.writef(".%s(", goMethod)
		e.emitExprList(node.Args)
		e.w.write(")")
	}
}

func (e *IREmitter) emitStringMethod(node *IRMethodCall) {
	e.w.addImport("strings", "")
	method := node.Method
	obj := node

	switch method {
	case "includes", "Contains":
		e.w.write("strings.Contains(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write(")")
	case "startsWith", "StartsWith":
		e.w.write("strings.HasPrefix(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write(")")
	case "endsWith", "EndsWith":
		e.w.write("strings.HasSuffix(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write(")")
	case "indexOf", "IndexOf":
		e.w.write("strings.Index(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write(")")
	case "lastIndexOf":
		e.w.write("strings.LastIndex(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write(")")
	case "split":
		e.w.write("strings.Split(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write(")")
	case "trim":
		e.w.write("strings.TrimSpace(")
		e.EmitExpr(obj.Object)
		e.w.write(")")
	case "trimStart", "trimLeft":
		e.w.write("strings.TrimLeft(")
		e.EmitExpr(obj.Object)
		e.w.write(`, " \t\n\r")`)
	case "trimEnd", "trimRight":
		e.w.write("strings.TrimRight(")
		e.EmitExpr(obj.Object)
		e.w.write(`, " \t\n\r")`)
	case "toUpperCase":
		e.w.write("strings.ToUpper(")
		e.EmitExpr(obj.Object)
		e.w.write(")")
	case "toLowerCase":
		e.w.write("strings.ToLower(")
		e.EmitExpr(obj.Object)
		e.w.write(")")
	case "replace":
		e.w.write("strings.Replace(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) >= 2 {
			e.EmitExpr(obj.Args[0])
			e.w.write(", ")
			e.EmitExpr(obj.Args[1])
		}
		e.w.write(", 1)")
	case "replaceAll":
		e.w.write("strings.ReplaceAll(")
		e.EmitExpr(obj.Object)
		e.w.write(", ")
		if len(obj.Args) >= 2 {
			e.EmitExpr(obj.Args[0])
			e.w.write(", ")
			e.EmitExpr(obj.Args[1])
		}
		e.w.write(")")
	case "repeat":
		e.w.write("strings.Repeat(")
		e.EmitExpr(obj.Object)
		e.w.write(", int(")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		}
		e.w.write("))")
	case "padStart":
		e.emitPadMethod(obj, true)
	case "padEnd":
		e.emitPadMethod(obj, false)
	case "slice", "substring":
		e.emitStringSlice(obj)
	case "charAt":
		e.w.write("string(")
		e.EmitExpr(obj.Object)
		e.w.write("[")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		} else {
			e.w.write("0")
		}
		e.w.write("])")
	case "charCodeAt":
		e.w.write("int(")
		e.EmitExpr(obj.Object)
		e.w.write("[")
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
		} else {
			e.w.write("0")
		}
		e.w.write("])")
	case "match":
		// str.match(regexp) → regexp.FindStringSubmatch(str)
		if len(obj.Args) > 0 {
			e.EmitExpr(obj.Args[0])
			e.w.write(".FindStringSubmatch(")
			e.EmitExpr(obj.Object)
			e.w.write(")")
		}
	case "normalize":
		// string.normalize() → just return the string (simplified)
		e.EmitExpr(obj.Object)
	default:
		// Fall back to direct method call for unrecognized string methods
		e.EmitExpr(obj.Object)
		e.w.writef(".%s(", goExportedName(method))
		e.emitExprList(obj.Args)
		e.w.write(")")
	}
}

func (e *IREmitter) emitPadMethod(node *IRMethodCall, isStart bool) {
	e.w.addImport("strings", "")
	e.w.write("func(s string, n int, fill string) string { if len(s) >= n { return s }; pad := strings.Repeat(fill, (n-len(s)+len(fill)-1)/len(fill)); ")
	if isStart {
		e.w.write("return pad[:n-len(s)] + s")
	} else {
		e.w.write("return s + pad[:n-len(s)]")
	}
	e.w.write(" }(")
	e.EmitExpr(node.Object)
	e.w.write(", int(")
	if len(node.Args) > 0 {
		e.EmitExpr(node.Args[0])
	}
	e.w.write("), ")
	if len(node.Args) > 1 {
		e.EmitExpr(node.Args[1])
	} else {
		e.w.write(`" "`)
	}
	e.w.write(")")
}

func (e *IREmitter) emitStringSlice(node *IRMethodCall) {
	// str.slice(start) → str[start:]
	// str.slice(start, end) → str[start:end]
	e.EmitExpr(node.Object)
	e.w.write("[")
	if len(node.Args) > 0 {
		e.EmitExpr(node.Args[0])
	}
	e.w.write(":")
	if len(node.Args) > 1 {
		e.EmitExpr(node.Args[1])
	}
	e.w.write("]")
}

func (e *IREmitter) emitArrayMethod(node *IRMethodCall) {
	e.w.addImport("github.com/i2y/ramune/jsrt/array", "jsarray")
	method := node.Method
	elemType := node.ElemGoType
	if elemType == "" {
		elemType = "any"
	}

	switch method {
	case "push":
		// arr.push(elem) → arr = append(arr, elem)
		e.EmitExpr(node.Object)
		e.w.write(" = append(")
		e.EmitExpr(node.Object)
		e.w.write(", ")
		e.emitExprList(node.Args)
		e.w.write(")")
	case "pop":
		// arr.pop() → arr[len(arr)-1] (with removal as side effect)
		e.EmitExpr(node.Object)
		e.w.write("[len(")
		e.EmitExpr(node.Object)
		e.w.write(")-1]")
	case "shift":
		e.EmitExpr(node.Object)
		e.w.write("[0]")
	case "join":
		e.w.addImport("strings", "")
		e.w.write("strings.Join(")
		e.EmitExpr(node.Object)
		e.w.write(", ")
		if len(node.Args) > 0 {
			e.EmitExpr(node.Args[0])
		} else {
			e.w.write(`","`)
		}
		e.w.write(")")
	case "includes":
		e.w.writef("jsarray.Contains(")
		e.EmitExpr(node.Object)
		e.w.write(", ")
		if len(node.Args) > 0 {
			e.EmitExpr(node.Args[0])
		}
		e.w.write(")")
	case "indexOf":
		e.w.write("jsarray.IndexOf(")
		e.EmitExpr(node.Object)
		e.w.write(", ")
		if len(node.Args) > 0 {
			e.EmitExpr(node.Args[0])
		}
		e.w.write(")")
	case "slice":
		e.EmitExpr(node.Object)
		e.w.write("[")
		if len(node.Args) > 0 {
			e.EmitExpr(node.Args[0])
		}
		e.w.write(":")
		if len(node.Args) > 1 {
			e.EmitExpr(node.Args[1])
		}
		e.w.write("]")
	case "reverse":
		e.w.write("jsarray.Reverse(")
		e.EmitExpr(node.Object)
		e.w.write(")")
	case "concat":
		e.w.write("append(")
		e.EmitExpr(node.Object)
		e.w.write(", ")
		e.emitExprList(node.Args)
		e.w.write("...)")
	case "flat":
		e.w.write("jsarray.Flat(")
		e.EmitExpr(node.Object)
		e.w.write(")")
	default:
		// jsarray.Method(arr, callback, ...)
		e.w.writef("jsarray.%s(", goExportedName(method))
		e.EmitExpr(node.Object)
		if len(node.Args) > 0 {
			e.w.write(", ")
			e.emitExprList(node.Args)
		}
		e.w.write(")")
	}
}

func (e *IREmitter) emitFieldAccess(node *IRFieldAccess) {
	if node.NeedsAssertion && node.AssertType.GoStr != "" {
		e.EmitExpr(node.Object)
		e.w.writef(".(%s).%s", node.AssertType.GoStr, node.Field)
	} else {
		e.EmitExpr(node.Object)
		e.w.writef(".%s", node.Field)
	}
}

func (e *IREmitter) emitIndexAccess(node *IRIndexAccess) {
	e.EmitExpr(node.Object)
	e.w.write("[")
	e.EmitExpr(node.Index)
	e.w.write("]")
}

func (e *IREmitter) emitTypeAssertion(node *IRTypeAssertion) {
	if node.Safe {
		e.EmitExpr(node.Expr)
		e.w.writef(".(%s)", node.TargetType.GoStr)
	} else {
		e.EmitExpr(node.Expr)
		e.w.writef(".(%s)", node.TargetType.GoStr)
	}
}

func (e *IREmitter) emitTypeConversion(node *IRTypeConversion) {
	e.w.writef("%s(", node.TargetType)
	e.EmitExpr(node.Expr)
	e.w.write(")")
}

func (e *IREmitter) emitFuncLit(node *IRFuncLit) {
	if node.IsAsync {
		e.emitAsyncFuncLit(node)
		return
	}

	e.w.write("func(")
	for i, param := range node.Params {
		if i > 0 {
			e.w.write(", ")
		}
		e.w.write(param.Name)
		if param.IsRest {
			e.w.write(" ...")
		} else {
			e.w.write(" ")
		}
		e.w.write(param.Typ.GoStr)
	}
	e.w.write(")")
	if node.RetType.GoStr != "" {
		e.w.writef(" %s", node.RetType.GoStr)
	}
	e.w.openBlock()
	e.EmitStmts(node.Body)
	e.w.closeBlockInline()
}

func (e *IREmitter) emitAsyncFuncLit(node *IRFuncLit) {
	e.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
	retType := node.RetType.GoStr
	if retType == "" {
		retType = "any"
	}

	e.w.write("func(")
	for i, param := range node.Params {
		if i > 0 {
			e.w.write(", ")
		}
		e.w.write(param.Name)
		if param.IsRest {
			e.w.write(" ...")
		} else {
			e.w.write(" ")
		}
		e.w.write(param.Typ.GoStr)
	}
	e.w.writef(") *promise.Promise[%s]", retType)
	e.w.openBlock()
	e.w.writef("return promise.New[%s](func(__resolve func(%s), __reject func(error))", retType, retType)
	e.w.openBlock()
	e.EmitStmts(node.Body)
	e.w.closeBlock()
	e.w.writeln(")")
	e.w.closeBlockInline()
}

func (e *IREmitter) emitStdlibCall(node *IRStdlibCall) {
	if node.Package != "" {
		e.w.writef("%s.%s(", node.Package, node.Func)
	} else {
		e.w.writef("%s(", node.Func)
	}
	e.emitExprList(node.Args)
	e.w.write(")")
}

func (e *IREmitter) emitSprintfCall(node *IRSprintfCall) {
	e.w.writef("fmt.Sprintf(%q, ", node.Format)
	e.emitExprList(node.Args)
	e.w.write(")")
}

func (e *IREmitter) emitMakeCall(node *IRMakeCall) {
	e.w.writef("make(%s", node.TypeStr)
	if node.Len != nil {
		e.w.write(", ")
		e.EmitExpr(node.Len)
	}
	if node.Cap != nil {
		e.w.write(", ")
		e.EmitExpr(node.Cap)
	}
	e.w.write(")")
}

func (e *IREmitter) emitSliceExpr(node *IRSliceExpr) {
	e.EmitExpr(node.Object)
	e.w.write("[")
	if node.Low != nil {
		e.EmitExpr(node.Low)
	}
	e.w.write(":")
	if node.High != nil {
		e.EmitExpr(node.High)
	}
	e.w.write("]")
}

func (e *IREmitter) emitNilCheck(node *IRNilCheck) {
	retType := node.ExprType().GoStr
	if retType == "" {
		retType = "any"
	}
	e.w.writef("func() %s { if ", retType)
	e.EmitExpr(node.Expr)
	e.w.write(" != nil { return ")
	e.EmitExpr(node.Then)
	e.w.write(" }; ")
	// If else branch is an assignment (??= pattern), split into assignment + return
	if assign := extractAssignExpr(node.Else); assign != nil {
		e.EmitExpr(assign.Left)
		e.w.write(" = ")
		e.EmitExpr(assign.Right)
		e.w.write("; return ")
		e.EmitExpr(assign.Left)
	} else {
		e.w.write("return ")
		e.EmitExpr(node.Else)
	}
	e.w.write(" }()")
}

func (e *IREmitter) emitTernary(node *IRTernary) {
	retType := node.ExprType().GoStr
	if retType == "" {
		retType = "any"
	}
	e.w.writef("func() %s { if ", retType)
	e.EmitExpr(node.Cond)
	e.w.write(" { return ")
	e.EmitExpr(node.Then)
	e.w.write(" } else { return ")
	e.EmitExpr(node.Else)
	e.w.write(" } }()")
}

func (e *IREmitter) emitAwait(node *IRAwait) {
	e.EmitExpr(node.Expr)
	e.w.write(".Await()")
}

func (e *IREmitter) emitTypeOf(node *IRTypeOf) {
	e.w.addImport("github.com/i2y/ramune/jsrt", "")
	e.w.write("jsrt.TypeOf(")
	e.EmitExpr(node.Expr)
	e.w.write(")")
}

func (e *IREmitter) emitInstanceOf(node *IRInstanceOf) {
	e.w.write("func() bool { _, __ok := ")
	e.EmitExpr(node.Expr)
	e.w.writef(".(*%s); return __ok }()", node.TypeName)
}

func (e *IREmitter) emitJSRTCall(node *IRJSRTCall) {
	e.w.addImport("github.com/i2y/ramune/jsrt", "")
	switch node.Pattern {
	case "Get":
		e.w.write("jsrt.Obj(")
		e.EmitExpr(node.Object)
		e.w.writef(").Get(%q).Unwrap()", node.Field)
	case "Set":
		e.w.write("jsrt.Obj(")
		e.EmitExpr(node.Object)
		e.w.writef(").Set(%q, ", node.Field)
		if len(node.Args) > 0 {
			e.EmitExpr(node.Args[0])
		}
		e.w.write(")")
	case "Call":
		e.w.write("jsrt.Obj(")
		e.EmitExpr(node.Object)
		e.w.writef(").Get(%q).Call(", node.Field)
		e.emitExprList(node.Args)
		e.w.write(").Unwrap()")
	default:
		e.w.writef("jsrt.%s(", node.Pattern)
		if node.Object != nil {
			e.EmitExpr(node.Object)
		}
		if len(node.Args) > 0 {
			if node.Object != nil {
				e.w.write(", ")
			}
			e.emitExprList(node.Args)
		}
		e.w.write(")")
	}
}

func (e *IREmitter) emitCompositeLit(node *IRCompositeLit) {
	isMap := strings.HasPrefix(node.TypeStr, "map[")
	e.w.writef("%s{", node.TypeStr)
	for i, elem := range node.Elements {
		if i > 0 {
			e.w.write(", ")
		}
		if elem.Key != "" {
			if isMap {
				e.w.writef("%q: ", elem.Key)
			} else {
				e.w.writef("%s: ", elem.Key)
			}
		}
		e.EmitExpr(elem.Value)
	}
	e.w.write("}")
}

func (e *IREmitter) emitNewExpr(node *IRNewExpr) {
	e.w.writef("&%s{", node.TypeName)
	for i, arg := range node.Args {
		if i > 0 {
			e.w.write(", ")
		}
		e.EmitExpr(arg.Value)
	}
	e.w.write("}")
}

func (e *IREmitter) emitPromiseNew(node *IRPromiseNew) {
	e.w.addImport("github.com/i2y/ramune/jsrt/promise", "")
	innerType := node.InnerType.GoStr
	if innerType == "" {
		innerType = "any"
	}
	e.w.writef("promise.New[%s](func(__resolve func(%s), __reject func(error))", innerType, innerType)
	e.w.openBlock()
	e.EmitStmts(node.Body)
	e.w.closeBlock()
	e.w.write(")")
}

func (e *IREmitter) emitArrayMethodCall(node *IRArrayMethodCall) {
	e.w.addImport("github.com/i2y/ramune/jsrt/array", "jsarray")
	e.w.writef("jsarray.%s(", node.HelperFunc)
	e.EmitExpr(node.Array)
	e.w.write(", ")
	e.EmitExpr(node.Callback)
	for _, extra := range node.ExtraArgs {
		e.w.write(", ")
		e.EmitExpr(extra)
	}
	e.w.write(")")
}

// --------------------------------------------------------------------
// Statement emission
// --------------------------------------------------------------------

// EmitStmt emits a Go statement from an IR node.
func (e *IREmitter) EmitStmt(stmt GoStmt) {
	if stmt == nil {
		return
	}

	switch node := stmt.(type) {
	case *IRVarDecl:
		e.emitVarDecl(node)
	case *IRMultiVarDecl:
		e.emitMultiVarDecl(node)
	case *IRAssign:
		e.emitAssign(node)
	case *IRExprStmt:
		e.EmitExpr(node.Expr)
		e.w.newline()
	case *IRReturn:
		e.emitReturn(node)
	case *IRIf:
		e.emitIf(node)
	case *IRFor:
		e.emitFor(node)
	case *IRRange:
		e.emitRange(node)
	case *IRSwitch:
		e.emitSwitch(node)
	case *IRBlock:
		e.emitBlock(node)
	case *IRBreak:
		if node.Label != "" {
			e.w.writef("break %s", node.Label)
		} else {
			e.w.write("break")
		}
		e.w.newline()
	case *IRContinue:
		if node.Label != "" {
			e.w.writef("continue %s", node.Label)
		} else {
			e.w.write("continue")
		}
		e.w.newline()
	case *IRLabeled:
		e.w.writef("%s:", node.Label)
		e.w.newline()
		e.EmitStmt(node.Stmt)
	case *IRDefer:
		e.w.write("defer ")
		e.EmitExpr(node.Call)
		e.w.newline()
	case *IRGo:
		e.w.write("go ")
		e.EmitExpr(node.Call)
		e.w.newline()
	case *IRSend:
		e.EmitExpr(node.Chan)
		e.w.write(" <- ")
		e.EmitExpr(node.Value)
		e.w.newline()
	case *IRTryCatch:
		e.emitTryCatch(node)
	case *IRRawStmt:
		e.w.writeln(node.Code)
	case *IRIncDec:
		e.EmitExpr(node.Expr)
		e.w.write(node.Op)
		e.w.newline()
	case *IRResolveCall:
		if node.Value != nil {
			e.w.write("__resolve(")
			e.EmitExpr(node.Value)
			e.w.writeln(")")
		}
		e.w.writeln("return")
	case *IRRejectCall:
		e.w.write("__reject(")
		e.EmitExpr(node.Err)
		e.w.writeln(")")
		e.w.writeln("return")
	default:
		e.w.writef("/* unknown IR stmt type: %T */\n", stmt)
	}
}

// EmitStmts emits a list of statements.
func (e *IREmitter) EmitStmts(stmts []GoStmt) {
	for _, stmt := range stmts {
		e.EmitStmt(stmt)
	}
}

func (e *IREmitter) emitVarDecl(node *IRVarDecl) {
	if node.Init != nil {
		if node.UseShort {
			e.w.writef("%s := ", node.Name)
			e.EmitExpr(node.Init)
		} else {
			e.w.writef("var %s %s = ", node.Name, node.Typ.GoStr)
			e.EmitExpr(node.Init)
		}
	} else {
		e.w.writef("var %s %s", node.Name, node.Typ.GoStr)
	}
	e.w.newline()
}

func (e *IREmitter) emitMultiVarDecl(node *IRMultiVarDecl) {
	for i, name := range node.Names {
		if i > 0 {
			e.w.write(", ")
		}
		e.w.write(name)
	}
	e.w.write(" := ")
	e.EmitExpr(node.Init)
	e.w.newline()
}

func (e *IREmitter) emitAssign(node *IRAssign) {
	for i, target := range node.Targets {
		if i > 0 {
			e.w.write(", ")
		}
		e.EmitExpr(target)
	}
	e.w.writef(" %s ", node.Op)
	for i, value := range node.Values {
		if i > 0 {
			e.w.write(", ")
		}
		e.EmitExpr(value)
	}
	e.w.newline()
}

func (e *IREmitter) emitReturn(node *IRReturn) {
	if len(node.Values) == 0 {
		e.w.writeln("return")
		return
	}
	// return (x = y) → x = y; return x
	if len(node.Values) == 1 {
		if assign := extractAssignExpr(node.Values[0]); assign != nil {
			e.EmitExpr(assign.Left)
			e.w.write(" = ")
			e.EmitExpr(assign.Right)
			e.w.newline()
			e.w.write("return ")
			e.EmitExpr(assign.Left)
			e.w.newline()
			return
		}
	}
	e.w.write("return ")
	e.emitExprList(node.Values)
	e.w.newline()
}

func (e *IREmitter) emitIf(node *IRIf) {
	e.w.write("if ")
	e.EmitExpr(node.Cond)
	e.w.openBlock()
	e.EmitStmts(node.Body)
	if len(node.Else) > 0 {
		if len(node.Else) == 1 {
			if elseIf, ok := node.Else[0].(*IRIf); ok {
				e.w.indent--
				e.w.write("} else ")
				e.w.indent++
				e.emitIf(elseIf)
				return
			}
		}
		e.w.indent--
		e.w.write("} else {")
		e.w.newline()
		e.w.indent++
		e.EmitStmts(node.Else)
	}
	e.w.closeBlock()
}

func (e *IREmitter) emitFor(node *IRFor) {
	e.w.write("for ")
	if node.Init != nil {
		e.emitForInit(node.Init)
	}
	e.w.write("; ")
	if node.Cond != nil {
		e.EmitExpr(node.Cond)
	}
	e.w.write("; ")
	if node.Post != nil {
		e.emitForInit(node.Post)
	}
	e.w.openBlock()
	e.EmitStmts(node.Body)
	e.w.closeBlock()
}

func (e *IREmitter) emitForInit(stmt GoStmt) {
	// Emit statement inline (without newline) for for-loop init/post
	switch s := stmt.(type) {
	case *IRVarDecl:
		if s.Init != nil {
			e.w.writef("%s := ", s.Name)
			e.EmitExpr(s.Init)
		}
	case *IRExprStmt:
		e.EmitExpr(s.Expr)
	case *IRAssign:
		for i, t := range s.Targets {
			if i > 0 {
				e.w.write(", ")
			}
			e.EmitExpr(t)
		}
		e.w.writef(" %s ", s.Op)
		for i, v := range s.Values {
			if i > 0 {
				e.w.write(", ")
			}
			e.EmitExpr(v)
		}
	default:
		e.EmitStmt(stmt)
	}
}

func (e *IREmitter) emitRange(node *IRRange) {
	e.w.write("for ")
	e.w.write(node.Key)
	if node.Value != "" {
		e.w.writef(", %s", node.Value)
	}
	e.w.write(" := range ")
	e.EmitExpr(node.Over)
	e.w.openBlock()
	e.EmitStmts(node.Body)
	e.w.closeBlock()
}

func (e *IREmitter) emitSwitch(node *IRSwitch) {
	e.w.write("switch ")
	if node.Tag != nil {
		e.EmitExpr(node.Tag)
	}
	e.w.openBlock()
	for _, c := range node.Cases {
		if len(c.Exprs) == 0 {
			e.w.writeln("default:")
		} else {
			e.w.write("case ")
			e.emitExprList(c.Exprs)
			e.w.writeln(":")
		}
		e.w.indent++
		e.EmitStmts(c.Body)
		e.w.indent--
	}
	e.w.closeBlock()
}

func (e *IREmitter) emitBlock(node *IRBlock) {
	e.w.openBlock()
	e.EmitStmts(node.Stmts)
	e.w.closeBlock()
}

func (e *IREmitter) emitTryCatch(node *IRTryCatch) {
	// Go has no try/catch — use func() with recover()
	e.w.write("func()")
	e.w.openBlock()
	e.w.write("defer func()")
	e.w.openBlock()
	if node.CatchVar != "" {
		e.w.writef("if %s := recover(); %s != nil", node.CatchVar, node.CatchVar)
	} else {
		e.w.write("if __err := recover(); __err != nil")
	}
	e.w.openBlock()
	e.EmitStmts(node.CatchBody)
	e.w.closeBlock()
	if len(node.FinallyBody) > 0 {
		e.EmitStmts(node.FinallyBody)
	}
	e.w.closeBlockInline()
	e.w.writeln("()")
	e.EmitStmts(node.TryBody)
	e.w.closeBlockInline()
	e.w.writeln("()")
}

// --------------------------------------------------------------------
// Declaration emission
// --------------------------------------------------------------------

// EmitDecl emits a Go declaration.
func (e *IREmitter) EmitDecl(decl GoDecl) {
	switch node := decl.(type) {
	case *IRFuncDecl:
		e.emitFuncDecl(node)
	case *IRStructDecl:
		e.emitStructDecl(node)
	case *IRInterfaceDecl:
		e.emitInterfaceDecl(node)
	case *IRTypeAlias:
		e.w.writef("type %s = %s", node.Name, node.Underlying)
		e.w.newline()
	case *IREnumDecl:
		e.emitEnumDecl(node)
	case *IRVarGroupDecl:
		for _, v := range node.Vars {
			e.emitVarDecl(&v)
		}
	case *IRConstGroupDecl:
		e.emitConstGroup(node)
	case *IRRawDecl:
		e.w.writeln(node.Code)
	case *IRImportDecl:
		// Handled by renderFile
	case *IRStmtDecl:
		e.EmitStmt(node.Stmt)
	default:
		e.w.writef("/* unknown IR decl type: %T */\n", decl)
	}
}

func (e *IREmitter) emitFuncDecl(node *IRFuncDecl) {
	e.w.write("func ")
	if node.Receiver != nil {
		e.w.writef("(%s %s) ", node.Receiver.Name, node.Receiver.Type)
	}
	e.w.write(node.Name)
	if len(node.TypeParams) > 0 {
		e.w.write("[")
		for i, tp := range node.TypeParams {
			if i > 0 {
				e.w.write(", ")
			}
			e.w.writef("%s %s", tp.Name, tp.Constraint)
		}
		e.w.write("]")
	}
	e.w.write("(")
	for i, param := range node.Params {
		if i > 0 {
			e.w.write(", ")
		}
		e.w.write(param.Name)
		if param.IsRest {
			e.w.write(" ...")
		} else {
			e.w.write(" ")
		}
		e.w.write(param.Typ.GoStr)
	}
	e.w.write(")")
	if node.RetType.GoStr != "" {
		e.w.writef(" %s", node.RetType.GoStr)
	}
	e.w.openBlock()
	e.EmitStmts(node.Body)
	e.w.closeBlock()
}

func (e *IREmitter) emitStructDecl(node *IRStructDecl) {
	e.w.writef("type %s", node.Name)
	if len(node.TypeParams) > 0 {
		e.w.write("[")
		for i, tp := range node.TypeParams {
			if i > 0 {
				e.w.write(", ")
			}
			e.w.writef("%s %s", tp.Name, tp.Constraint)
		}
		e.w.write("]")
	}
	e.w.write(" struct")
	e.w.openBlock()
	for _, emb := range node.Embedded {
		e.w.writeln(emb)
	}
	for _, f := range node.Fields {
		e.w.writef("%s %s", f.Name, f.Typ.GoStr)
		if f.Tag != "" {
			e.w.writef(" `%s`", f.Tag)
		}
		e.w.newline()
	}
	e.w.closeBlock()
}

func (e *IREmitter) emitInterfaceDecl(node *IRInterfaceDecl) {
	e.w.writef("type %s interface", node.Name)
	e.w.openBlock()
	for _, embedded := range node.Embedded {
		e.w.writeln(embedded)
	}
	for _, m := range node.Methods {
		e.w.writef("%s(", m.Name)
		for i, p := range m.Params {
			if i > 0 {
				e.w.write(", ")
			}
			e.w.writef("%s %s", p.Name, p.Typ.GoStr)
		}
		e.w.write(")")
		if m.RetType.GoStr != "" {
			e.w.writef(" %s", m.RetType.GoStr)
		}
		e.w.newline()
	}
	e.w.closeBlock()
}

func (e *IREmitter) emitEnumDecl(node *IREnumDecl) {
	e.w.writef("type %s %s", node.Name, node.BaseType)
	e.w.newline()
	e.w.newline()
	e.w.write("const (")
	e.w.newline()
	e.w.indent++
	for _, m := range node.Members {
		e.w.writef("%s %s = ", m.Name, node.Name)
		if m.Value != nil {
			e.EmitExpr(m.Value)
		} else {
			e.w.write("iota")
		}
		e.w.newline()
	}
	e.w.indent--
	e.w.writeln(")")
}

func (e *IREmitter) emitConstGroup(node *IRConstGroupDecl) {
	e.w.write("const (")
	e.w.newline()
	e.w.indent++
	for i, c := range node.Consts {
		e.w.write(c.Name)
		if i == 0 && node.TypeName != "" {
			e.w.writef(" %s", node.TypeName)
		}
		if c.Value != nil {
			e.w.write(" = ")
			e.EmitExpr(c.Value)
		} else if i == 0 {
			e.w.write(" = iota")
		}
		e.w.newline()
	}
	e.w.indent--
	e.w.writeln(")")
}

// extractAssignExpr unwraps an assignment expression, looking through parentheses.
func extractAssignExpr(expr GoExpr) *IRBinaryOp {
	switch e := expr.(type) {
	case *IRBinaryOp:
		if e.Op == "=" {
			return e
		}
	case *IRUnaryOp:
		// Parenthesized expression: IRUnaryOp{Op: "()", Operand: ...}
		if e.Op == "()" {
			return extractAssignExpr(e.Operand)
		}
	}
	return nil
}

// --------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------

func (e *IREmitter) emitExprList(exprs []GoExpr) {
	for i, expr := range exprs {
		if i > 0 {
			e.w.write(", ")
		}
		e.EmitExpr(expr)
	}
}

// EmitExprToString renders an IR expression to a Go source string.
func EmitExprToString(expr GoExpr) string {
	e := &IREmitter{w: newGoWriter(), imports: make(map[string]string)}
	e.EmitExpr(expr)
	return e.w.buf.String()
}

// EmitStmtToString renders an IR statement to a Go source string.
func EmitStmtToString(stmt GoStmt) string {
	e := &IREmitter{w: newGoWriter(), imports: make(map[string]string)}
	e.EmitStmt(stmt)
	return e.w.buf.String()
}

// Ensure IREmitter satisfies a minimal interface for testing.
var _ = fmt.Stringer(nil) // import fmt
