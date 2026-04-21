//go:build legacy_goir

package gotranspiler

// GOTIR (Go-Oriented Typed Intermediate Representation)
//
// This IR sits between the TypeScript AST and Go source code emission.
// Every node carries its resolved Go type and emission strategy,
// eliminating the need for mutable type-tracking state during code generation.
//
// Architecture:
//   Phase 1 (Build):   TS AST + Checker → GOTIR  (type resolution, strategy decisions)
//   Phase 2 (Resolve): GOTIR → GOTIR             (cross-reference resolution)
//   Phase 3 (Emit):    GOTIR → Go source          (stateless string generation)

// --------------------------------------------------------------------
// Core interfaces
// --------------------------------------------------------------------

// GoExpr represents a Go expression with a resolved type.
type GoExpr interface {
	goExpr()
	ExprType() GoTypeInfo
}

// GoStmt represents a Go statement.
type GoStmt interface {
	goStmt()
}

// GoDecl represents a top-level Go declaration.
type GoDecl interface {
	goDecl()
}

// GoFile is the root IR node for a single Go source file.
type GoFile struct {
	Package string
	Decls   []GoDecl
	Stmts   []GoStmt // package-level init statements (rare, for var declarations)
}

// --------------------------------------------------------------------
// Expressions
// --------------------------------------------------------------------

// exprBase provides the GoExpr interface implementation.
type exprBase struct {
	Typ GoTypeInfo
}

func (e exprBase) goExpr()              {}
func (e exprBase) ExprType() GoTypeInfo { return e.Typ }

// IRIdent is a variable or constant reference.
type IRIdent struct {
	exprBase
	Name    string // Go identifier name (already converted via goVarName/goExportedName)
	PkgName string // package qualifier, empty for local (e.g., "strings", "web")
}

// IRLiteral is a literal value (string, number, bool, nil).
type IRLiteral struct {
	exprBase
	Value string // Go literal text: `"hello"`, "42", "true", "nil"
}

// IRCompositeLit is a composite literal (struct, slice, map).
// e.g., &MyStruct{Field: value}, []float64{1, 2, 3}, map[string]any{"k": v}
type IRCompositeLit struct {
	exprBase
	TypeStr  string       // Go type string: "&MyStruct", "[]float64", "map[string]any"
	Elements []IRKeyValue // field/key-value pairs, or positional elements (Key empty)
}

// IRKeyValue is a key-value pair in a composite literal.
type IRKeyValue struct {
	Key   string // field name or map key (empty for positional elements)
	Value GoExpr
}

// IRBinaryOp is a binary operation.
type IRBinaryOp struct {
	exprBase
	Op    string // Go operator: "+", "-", "==", "!=", "&&", "||", etc.
	Left  GoExpr
	Right GoExpr
}

// IRUnaryOp is a unary operation (prefix or postfix).
type IRUnaryOp struct {
	exprBase
	Op      string // "!", "-", "^" (bitwise NOT)
	Operand GoExpr
	Postfix bool // true for i++ / i--
}

// IRCall is a regular function call (not a method call).
type IRCall struct {
	exprBase
	Func GoExpr // the function expression
	Args []GoExpr
}

// IRMethodCall is a method call with a resolved dispatch strategy.
type IRMethodCall struct {
	exprBase
	Strategy DispatchTarget
	Object   GoExpr
	Method   string
	Args     []GoExpr
	// Array/string method-specific fields
	ElemGoType string // element type for array helpers (e.g., "float64")
}

// IRFieldAccess is a field/property access on an object.
type IRFieldAccess struct {
	exprBase
	Object         GoExpr
	Field          string
	NeedsAssertion bool       // true if obj is any and needs .(Type)
	AssertType     GoTypeInfo // the type to assert to (when NeedsAssertion)
}

// IRIndexAccess is an index/element access (arr[i], map[key]).
type IRIndexAccess struct {
	exprBase
	Object GoExpr
	Index  GoExpr
}

// IRTypeAssertion is a Go type assertion: expr.(Type)
type IRTypeAssertion struct {
	exprBase
	Expr       GoExpr
	TargetType GoTypeInfo
	Safe       bool // true for val, ok := expr.(Type) pattern
}

// IRTypeConversion is a Go type conversion: Type(expr)
type IRTypeConversion struct {
	exprBase
	Expr       GoExpr
	TargetType string // Go type string
}

// IRFuncLit is a function literal (closure).
type IRFuncLit struct {
	exprBase
	Params  []IRParam
	RetType GoTypeInfo
	Body    []GoStmt
	IsAsync bool // wraps body in promise.New[T](func(__resolve, __reject) { ... })
}

// IRParam is a function parameter.
type IRParam struct {
	Name   string
	Typ    GoTypeInfo
	IsRest bool // ...args → variadic
}

// IRStdlibCall is a call to a Go standard library or runtime helper function.
// e.g., strings.Contains(s, sub), fmt.Sprintf(...), len(x), append(s, e)
type IRStdlibCall struct {
	exprBase
	Package string // "strings", "fmt", "jsarray", "jsrt", "" (builtin)
	Func    string // "Contains", "Sprintf", "len", "append"
	Args    []GoExpr
}

// IRSprintfCall generates fmt.Sprintf for template literals.
type IRSprintfCall struct {
	exprBase
	Format string   // format string with %s/%v placeholders
	Args   []GoExpr // expressions for each placeholder
}

// IRMakeCall is a make() expression for slices, maps, or channels.
type IRMakeCall struct {
	exprBase
	TypeStr string // Go type: "[]float64", "map[string]any", "chan int"
	Len     GoExpr // length (may be nil)
	Cap     GoExpr // capacity (may be nil)
}

// IRSliceExpr is a slice expression: s[low:high] or s[low:high:max].
type IRSliceExpr struct {
	exprBase
	Object GoExpr
	Low    GoExpr // may be nil
	High   GoExpr // may be nil
}

// IRAddrOf is the address-of operator: &expr
type IRAddrOf struct {
	exprBase
	Expr GoExpr
}

// IRDeref is the dereference operator: *expr
type IRDeref struct {
	exprBase
	Expr GoExpr
}

// IRNilCheck generates an if-nil-check pattern or nil comparison.
// Used for optional chaining: if x != nil { x.Field } else { nil }
type IRNilCheck struct {
	exprBase
	Expr GoExpr // expression to check for nil
	Then GoExpr // result if not nil
	Else GoExpr // result if nil (usually IRLiteral{Value:"nil"})
}

// IRTernary generates a Go inline conditional pattern.
// Since Go has no ternary, this typically becomes a helper call or IIFE.
type IRTernary struct {
	exprBase
	Cond GoExpr
	Then GoExpr
	Else GoExpr
}

// IRAwait represents awaiting a promise: promise.Await()
type IRAwait struct {
	exprBase
	Expr GoExpr // the promise expression
}

// IRTypeOf generates a typeof check.
// Emits as jsrt.TypeOf(expr) or a direct type switch pattern.
type IRTypeOf struct {
	exprBase
	Expr GoExpr
}

// IRInstanceOf generates an instanceof check.
// Emits as a Go type assertion check: _, ok := expr.(*Type)
type IRInstanceOf struct {
	exprBase
	Expr     GoExpr
	TypeName string // Go type name to check against
}

// IRJSRTCall is a call through the jsrt runtime for dynamic/any-typed operations.
// e.g., jsrt.Obj(x).Get("field").Unwrap(), jsrt.Obj(x).Call("method", args...)
type IRJSRTCall struct {
	exprBase
	Pattern string   // "Get", "Set", "Call", "TypeOf", etc.
	Object  GoExpr   // the object (may be nil for global operations)
	Field   string   // field/method name
	Args    []GoExpr // arguments
}

// IRRawExpr is an escape hatch for emitting raw Go expression text.
// Used for not-yet-migrated patterns during incremental migration.
type IRRawExpr struct {
	exprBase
	Code string
}

// IRMultiValue represents a multi-return value expression in Go.
// e.g., val, err := someFunc()
type IRMultiValue struct {
	exprBase
	Exprs []GoExpr
}

// IRNewExpr generates a struct instantiation: &Type{fields...}
type IRNewExpr struct {
	exprBase
	TypeName string       // Go type name (without *)
	Args     []IRKeyValue // constructor arguments as field assignments
}

// IRPromiseNew wraps a body in promise.New[T](func(resolve, reject) { ... })
type IRPromiseNew struct {
	exprBase
	InnerType GoTypeInfo // T in Promise[T]
	Body      []GoStmt   // statements inside the promise callback
}

// IRArrayMethodCall is a specialized call to jsarray.* helpers.
// Separated from IRMethodCall because array methods have complex callback typing.
type IRArrayMethodCall struct {
	exprBase
	HelperFunc string   // "Map", "Filter", "Reduce", "Find", "ForEach", etc.
	Array      GoExpr   // the array expression
	Callback   GoExpr   // the callback function (IRFuncLit)
	ExtraArgs  []GoExpr // extra args (e.g., initial value for Reduce)
	ElemType   string   // Go element type of the array
}

// --------------------------------------------------------------------
// Statements
// --------------------------------------------------------------------

// stmtBase provides the GoStmt interface implementation.
type stmtBase struct{}

func (s stmtBase) goStmt() {}

// IRVarDecl is a variable declaration.
type IRVarDecl struct {
	stmtBase
	Name     string
	Typ      GoTypeInfo
	Init     GoExpr // may be nil for zero-value declarations
	IsConst  bool   // const vs var
	UseShort bool   // := vs var
}

// IRMultiVarDecl is a multi-variable declaration (e.g., from destructuring or multi-return).
// val, err := someFunc()
type IRMultiVarDecl struct {
	stmtBase
	Names []string
	Types []GoTypeInfo
	Init  GoExpr // the expression producing multiple values
}

// IRAssign is an assignment statement.
type IRAssign struct {
	stmtBase
	Targets []GoExpr
	Op      string // "=", "+=", "-=", etc.
	Values  []GoExpr
}

// IRExprStmt is an expression used as a statement.
type IRExprStmt struct {
	stmtBase
	Expr GoExpr
}

// IRReturn is a return statement.
type IRReturn struct {
	stmtBase
	Values []GoExpr // may be empty for bare return
}

// IRIf is an if/else statement.
type IRIf struct {
	stmtBase
	Cond GoExpr
	Body []GoStmt
	Else []GoStmt // may be empty; single IRIf element = else if
}

// IRFor is a C-style for loop.
type IRFor struct {
	stmtBase
	Init GoStmt // may be nil
	Cond GoExpr // may be nil (infinite loop)
	Post GoStmt // may be nil
	Body []GoStmt
}

// IRRange is a for-range loop.
type IRRange struct {
	stmtBase
	Key   string // "_" if unused
	Value string // "_" if unused, empty if no value binding
	Over  GoExpr
	Body  []GoStmt
}

// IRSwitch is a switch statement.
type IRSwitch struct {
	stmtBase
	Tag   GoExpr // may be nil for type switch
	Cases []IRCase
}

// IRCase is a single case in a switch.
type IRCase struct {
	Exprs []GoExpr // empty = default case
	Body  []GoStmt
}

// IRBlock is a scoped block of statements.
type IRBlock struct {
	stmtBase
	Stmts []GoStmt
	Bare  bool // if true, emit statements without surrounding braces
}

// IRBreak is a break statement with optional label.
type IRBreak struct {
	stmtBase
	Label string // empty for unlabeled break
}

// IRContinue is a continue statement with optional label.
type IRContinue struct {
	stmtBase
	Label string
}

// IRLabeled is a labeled statement.
type IRLabeled struct {
	stmtBase
	Label string
	Stmt  GoStmt
}

// IRDefer is a defer statement.
type IRDefer struct {
	stmtBase
	Call GoExpr // the deferred function call
}

// IRGo is a go statement (goroutine launch).
type IRGo struct {
	stmtBase
	Call GoExpr
}

// IRSend is a channel send: ch <- value
type IRSend struct {
	stmtBase
	Chan  GoExpr
	Value GoExpr
}

// IRTryCatch represents a try/catch/finally → Go error handling pattern.
// The specific Go pattern depends on context:
//   - Simple: if-err-check after call
//   - Complex: func() with recover()
type IRTryCatch struct {
	stmtBase
	TryBody     []GoStmt
	CatchVar    string   // catch parameter name (empty if unused)
	CatchBody   []GoStmt // may be empty if no catch
	FinallyBody []GoStmt // may be empty if no finally
}

// IRRawStmt is an escape hatch for emitting raw Go statement text.
type IRRawStmt struct {
	stmtBase
	Code string
}

// IRIncDec is an increment/decrement statement: i++ or i--
type IRIncDec struct {
	stmtBase
	Expr GoExpr
	Op   string // "++" or "--"
}

// IRResolveCall generates __resolve(value) inside async promise bodies.
type IRResolveCall struct {
	stmtBase
	Value GoExpr // the value to resolve with (may be nil for void)
}

// IRRejectCall generates __reject(err) inside async promise bodies.
type IRRejectCall struct {
	stmtBase
	Err GoExpr
}

// --------------------------------------------------------------------
// Declarations
// --------------------------------------------------------------------

// declBase provides the GoDecl interface implementation.
type declBase struct{}

func (d declBase) goDecl() {}

// IRFuncDecl is a top-level or method function declaration.
type IRFuncDecl struct {
	declBase
	Name       string
	TypeParams []IRTypeParam // generic type parameters
	Params     []IRParam
	RetType    GoTypeInfo
	Body       []GoStmt
	Receiver   *IRReceiver // non-nil for methods
	IsAsync    bool        // wraps body in promise.New
	IsExported bool
}

// IRTypeParam is a generic type parameter declaration.
type IRTypeParam struct {
	Name       string // Go name (PascalCase)
	Constraint string // Go constraint: "any", "comparable", etc.
}

// IRReceiver is a method receiver.
type IRReceiver struct {
	Name string // receiver variable name (e.g., "r")
	Type string // receiver type (e.g., "*MyStruct")
}

// IRStructDecl declares a struct type.
type IRStructDecl struct {
	declBase
	Name       string
	TypeParams []IRTypeParam
	Embedded   []string // embedded type names (base class, composed interfaces)
	Fields     []IRStructField
	IsExported bool
}

// IRStructField is a field in a struct declaration.
type IRStructField struct {
	Name       string
	Typ        GoTypeInfo
	Tag        string // struct tag (e.g., `json:"name"`)
	IsExported bool
}

// IRInterfaceDecl declares an interface type.
type IRInterfaceDecl struct {
	declBase
	Name       string
	TypeParams []IRTypeParam
	Methods    []IRMethodSig
	Embedded   []string // embedded interface names
	IsExported bool
}

// IRMethodSig is a method signature in an interface.
type IRMethodSig struct {
	Name       string
	Params     []IRParam
	RetType    GoTypeInfo
	IsExported bool
}

// IRTypeAlias declares a type alias: type Name = UnderlyingType
type IRTypeAlias struct {
	declBase
	Name       string
	Underlying string // Go type string
	TypeParams []IRTypeParam
	IsExported bool
}

// IREnumDecl declares an enum as a set of Go constants.
type IREnumDecl struct {
	declBase
	Name       string
	BaseType   string // "float64", "string", or "int"
	Members    []IREnumMember
	IsExported bool
}

// IREnumMember is a single enum constant.
type IREnumMember struct {
	Name  string
	Value GoExpr // the constant value (may be nil for iota)
}

// IRVarGroupDecl is a package-level variable declaration (var block).
type IRVarGroupDecl struct {
	declBase
	Vars []IRVarDecl
}

// IRConstGroupDecl is a package-level const block (for enums/iota).
type IRConstGroupDecl struct {
	declBase
	TypeName string // the type name (for typed constants)
	Consts   []IRConstDecl
}

// IRConstDecl is a single constant declaration.
type IRConstDecl struct {
	Name  string
	Value GoExpr // may be nil (uses iota)
}

// IRRawDecl is an escape hatch for emitting raw Go declaration text.
type IRRawDecl struct {
	declBase
	Code string
}

// IRImportDecl captures a required Go import.
type IRImportDecl struct {
	declBase
	Path  string // import path
	Alias string // alias (empty for default)
}

// IRStmtDecl wraps a statement as a declaration (for package-level init code).
type IRStmtDecl struct {
	declBase
	Stmt GoStmt
}
