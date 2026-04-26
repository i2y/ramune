// Package gotranspiler implements TypeScript to Go source code transpilation.
//
// It uses the existing tsgo parser and type checker to parse TypeScript source files,
// resolve all types, and generate equivalent Go source code.
package gotranspiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/bundled"
	"github.com/i2y/ramune/internal/tsgo/checker"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

// Transpiler holds the state for a TypeScript-to-Go transpilation session.
type Transpiler struct {
	w            *goWriter
	ck           *checker.Checker
	tm           *typeMapper
	thisReceiver string // Set during class method emission for "this" mapping
	pkgName      string
	goModuleName string // Go module name for resolving relative imports
	projectRoot  string // Root directory of the TS project
	isEntryFile  bool   // Whether this file should generate func main()
	// importedNames maps imported TS names to their Go package alias.
	// e.g., "add" → "utils" (so add(...) becomes utils.Add(...))
	importedNames         map[string]string
	importedOriginalNames map[string]string // local alias → original export name for renamed imports
	packageRefs           map[string]string // names that are direct package references (e.g., "z" → "zod")
	tmpVarCounter         int
	// privateFields tracks fields that are private/protected in the current class.
	// Maps original TS field name → Go field name (lowercase).
	privateFields         map[string]string
	inAsyncBody           bool   // true when emitting statements inside a promise.New callback
	arrayCallbackIdx      int    // >= 0: position of index param in array callback (for number→int mapping); -1 otherwise
	arrayCallbackElemType string // Go type of the array's element, for callback first param type
	// intVars tracks variables known to be int (from for-loop initializers).
	intVars        map[string]bool
	currentRetType string // Go return type of the current function being emitted
	returnContext  string // Struct type name set only during return-statement expression emission
	tryResultVar   string // When inside try/catch, return statements assign to this var
	declContext    string // Expected type from variable declaration, set during initializer emission
	// narrowedTypes maps variable names to concrete Go types in narrowed branches.
	// e.g., inside `if shape.kind === "circle"`: "shape" → "*Circle"
	narrowedTypes map[string]string
	// suppressTypeAssertion suppresses type assertions in property access (used by typeof)
	suppressTypeAssertion bool
	npmResolver           *npmResolver // Resolves npm packages for transpilation
	// goNativeImports maps Go package alias → full import path for go: prefix imports.
	goNativeImports map[string]string
	// classNames tracks declared class names for static member access detection (shared across files).
	classNames map[string]bool
	// localTypeNames tracks type/class/interface names declared in the current file's package.
	// Used by replaceUnknownTypes to distinguish same-package types from cross-package ones.
	localTypeNames map[string]bool
	// constructorArrowFields collects this.field = arrowFunc assignments in constructor
	// so they can be emitted as methods after the constructor.
	constructorArrowFields []constructorArrowField
	// pendingImports maps Go package alias → import path for lazy import resolution.
	// Imports are only added to the output when the alias is actually used in code.
	pendingImports map[string]string
	// currentFileDir is the directory of the current file relative to projectRoot.
	// Used to resolve relative imports correctly in multi-file transpilation.
	// e.g., "utils" for a file at "utils/html.ts"
	currentFileDir string
	// samePackageExports tracks names imported from the same Go package.
	// These need goExportedName() instead of goVarName() in emitIdentifier.
	samePackageExports map[string]bool
	// forceExportedFuncs makes every emitted top-level function a Go
	// exported (PascalCase) symbol, regardless of whether the source TS
	// used `export`. The composer/picker path uses this so that
	// gotranspiler.DiscoverExportedFuncs (reflect-based) can see the
	// extracted helpers when registering the native module bridge.
	forceExportedFuncs bool
	// goAnyVars tracks variables that are any at Go level even though
	// the TS checker says they have a concrete type (e.g., from []any indexing).
	goAnyVars map[string]bool
	// goVarTypes is the unified Go type tracker: maps variable names to their
	// actual emitted Go type string. Replaces ad-hoc maps (goAnyVars, concreteVarTypes,
	// goPtrStringVars, intVars) with a single source of truth.
	goVarTypes map[string]string
	// inThenCallback is set when emitting a .then() callback to force any types.
	inThenCallback bool
	// inCallArg is set when emitting an expression as a function call argument.
	// Used by emitArrowFunction/emitFunctionExpression to use closeBlockInline()
	// instead of closeBlock(), avoiding trailing newline that requires fixTrailingFuncArgs.
	inCallArg bool
	// needsDefaultReturn is set before emitting a function body to add missing returns.
	needsDefaultReturn bool
	// concreteVarTypes tracks variables whose Go type is known to be concrete
	// even though the checker says any (e.g., from new Uint8Array → []byte).
	concreteVarTypes map[string]string
	// stringMethodObjOverride overrides the object expression in emitStringMethodCall.
	stringMethodObjOverride string
	// goPtrStringVars tracks variables known to be *string at Go level (function params).
	goPtrStringVars map[string]bool
	// pendingFuncName is set to inject a name into the next function emission.
	pendingFuncName string
	// currentFuncName is the name of the current function being emitted (for self-call detection).
	currentFuncName string
	// currentFuncParamTypes stores the Go parameter types of the current function.
	currentFuncParamTypes []string
}

// isJSFuncParam reports whether pn is a parameter whose emitted Go type
// is *ramune.JSFunc.
func (t *Transpiler) isJSFuncParam(pn string) bool {
	return t.goVarTypes != nil && t.goVarTypes[pn] == "*ramune.JSFunc"
}

// projectSharedState holds cross-file state collected in a first pass over all source files.
type projectSharedState struct {
	typeAliases map[string]string // Alias name → underlying Go type (shared across all files)
	classNames  map[string]bool   // Declared class names (shared across all files)
}

// getGoType returns the GoTypeInfo for an AST node using the checker's flow-narrowed type.
// This is the PRIMARY type query — it reflects instanceof/typeof narrowing, discriminant checks, etc.
func (t *Transpiler) getGoType(node *ast.Node) GoTypeInfo {
	if t.ck == nil || node == nil {
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	}
	// 'this' is always the class type — never treat as JSObject
	if node.Kind == ast.KindThisKeyword {
		return GoTypeInfo{Category: GoTypePointer, GoStr: "*this", Name: "this"}
	}
	// Package references are never JSObject
	if t.isPackageRef(node) {
		return GoTypeInfo{Category: GoTypePointer, GoStr: "pkg"}
	}
	typ := t.ck.GetTypeAtLocation(node)
	if typ == nil {
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	}
	return t.tm.goTypeInfo(typ)
}

// getEmittedGoType returns the actual Go type of an identifier as tracked at emit time.
// This overrides checker-based type queries when the emitted Go code has a different type
// than what the TS checker reports (e.g., optional params → *string, jsrt.Index → any).
// Returns empty GoTypeInfo if no tracked type exists.
func (t *Transpiler) getEmittedGoType(node *ast.Node) GoTypeInfo {
	if node == nil || node.Kind != ast.KindIdentifier {
		return GoTypeInfo{}
	}
	name := goVarName(node.AsIdentifier().Text)
	// Check samePackageExports for exported name casing
	if t.samePackageExports != nil && t.samePackageExports[node.AsIdentifier().Text] {
		name = goExportedName(node.AsIdentifier().Text)
	}
	if t.goVarTypes != nil {
		if goType, ok := t.goVarTypes[name]; ok {
			return goTypeInfoFromString(goType)
		}
	}
	return GoTypeInfo{}
}

// trackGoVarType records the actual Go type of a variable at emit time.
func (t *Transpiler) trackGoVarType(name string, goType string) {
	if t.goVarTypes == nil {
		t.goVarTypes = make(map[string]string)
	}
	t.goVarTypes[name] = goType
	// Keep legacy maps in sync during migration
	if goType == "any" {
		if t.goAnyVars == nil {
			t.goAnyVars = make(map[string]bool)
		}
		t.goAnyVars[name] = true
	} else if t.goAnyVars != nil {
		delete(t.goAnyVars, name)
	}
	if goType == "*string" {
		if t.goPtrStringVars == nil {
			t.goPtrStringVars = make(map[string]bool)
		}
		t.goPtrStringVars[name] = true
	} else if t.goPtrStringVars != nil {
		delete(t.goPtrStringVars, name)
	}
	if goType == "int" {
		if t.intVars == nil {
			t.intVars = make(map[string]bool)
		}
		t.intVars[name] = true
	}
	if strings.HasPrefix(goType, "[]") || goType == "[]byte" || goType == "[]any" {
		if t.concreteVarTypes == nil {
			t.concreteVarTypes = make(map[string]string)
		}
		t.concreteVarTypes[name] = goType
	}
}

// goTypeInfoFromString constructs a GoTypeInfo from a Go type string.
func goTypeInfoFromString(goType string) GoTypeInfo {
	switch {
	case goType == "any":
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	case goType == "string" || goType == "float64" || goType == "bool" || goType == "int":
		return GoTypeInfo{Category: GoTypePrimitive, GoStr: goType}
	case strings.HasPrefix(goType, "*promise.Promise["):
		inner := goType[len("*promise.Promise[") : len(goType)-1]
		return GoTypeInfo{Category: GoTypePromise, GoStr: goType, ElemType: inner}
	case strings.HasPrefix(goType, "[]"):
		return GoTypeInfo{Category: GoTypeSlice, GoStr: goType, ElemType: goType[2:]}
	case strings.HasPrefix(goType, "map["):
		return GoTypeInfo{Category: GoTypeMap, GoStr: goType}
	case strings.HasPrefix(goType, "func("):
		return GoTypeInfo{Category: GoTypeFunc, GoStr: goType}
	case strings.HasPrefix(goType, "*"):
		return GoTypeInfo{Category: GoTypePointer, GoStr: goType, Name: goType[1:]}
	case isGenericType(goType):
		bracketIdx := strings.Index(goType, "[")
		return GoTypeInfo{Category: GoTypePointer, GoStr: goType, Name: goType[:bracketIdx]}
	case isValidGoIdentifier(goType):
		return GoTypeInfo{Category: GoTypePointer, GoStr: goType, Name: goType}
	default:
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: goType}
	}
}

// codeProducesConcreteType checks if a captured Go expression produces a concrete (non-interface) type
// that cannot have type assertions applied.
// This prevents double assertions: once .(Type) is applied, the result is concrete.
func codeProducesConcreteType(code string) bool {
	trimmed := strings.TrimSpace(code)
	// Map literal: map[...]...{...}
	if strings.HasPrefix(trimmed, "map[") {
		return true
	}
	// Slice literal: []T{...}
	if strings.HasPrefix(trimmed, "[]") && strings.Contains(trimmed, "{") {
		return true
	}
	// Struct literal: &Type{...} or Type{...}
	if strings.HasPrefix(trimmed, "&") && strings.Contains(trimmed, "{") {
		return true
	}
	// Already has type assertion: expr.(Type) → result is concrete
	// Strip outer parentheses for matching
	inner := trimmed
	for strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
		inner = inner[1 : len(inner)-1]
	}
	// Match .(Identifier) at end — type assertion result
	if strings.HasSuffix(inner, ")") {
		idx := strings.LastIndex(inner, ".(")
		if idx >= 0 {
			typeName := inner[idx+2 : len(inner)-1]
			if isValidGoIdentifier(typeName) || (strings.Contains(typeName, ".") && !strings.Contains(typeName, "(")) {
				return true
			}
			if isGenericType(typeName) {
				return true
			}
			// map[K]V, []T, *T type assertions produce concrete types
			if strings.HasPrefix(typeName, "map[") || strings.HasPrefix(typeName, "[]") || strings.HasPrefix(typeName, "*") {
				return true
			}
		}
	}
	// Type conversion: Type(expr) — e.g., T(form)
	if len(inner) > 2 && inner[0] >= 'A' && inner[0] <= 'Z' && strings.HasSuffix(inner, ")") {
		parenIdx := strings.Index(inner, "(")
		if parenIdx > 0 && isValidGoIdentifier(inner[:parenIdx]) {
			return true
		}
	}
	// fmt.Sprint/Sprintf always returns string (concrete)
	if strings.HasPrefix(inner, "fmt.Sprint") {
		return true
	}
	// Struct field access: expr.FieldName — field result is concrete if expr is concrete
	// Pattern: ends with .PascalCaseField (not a method call, no parens after)
	if dotIdx := strings.LastIndex(inner, "."); dotIdx > 0 && !strings.HasSuffix(inner, ")") {
		fieldName := inner[dotIdx+1:]
		if fieldName != "" && fieldName[0] >= 'A' && fieldName[0] <= 'Z' && isValidGoIdentifier(fieldName) {
			// Check if the object part produces concrete
			objPart := inner[:dotIdx]
			if codeProducesConcreteType(objPart) {
				return true
			}
		}
	}
	return false
}

// exprProducesConcreteGoType checks if an AST expression produces a concrete Go type
// (tracked in goVarTypes) that shouldn't have type assertions applied.
func (t *Transpiler) exprProducesConcreteGoType(node *ast.Node) bool {
	if node == nil {
		return false
	}
	// Check goVarTypes for identifiers
	if node.Kind == ast.KindIdentifier {
		emitted := t.getEmittedGoType(node)
		if emitted.GoStr != "" && !emitted.IsAny() {
			return true
		}
	}
	// Check captured code
	code := t.captureExpr(node)
	return codeProducesConcreteType(code)
}

// writeTypeAssertion writes .(targetType) to the buffer, but only if the last-written code
// doesn't already produce a concrete type. Prevents double assertions and assertions on concrete types.
func (t *Transpiler) writeTypeAssertion(targetType string) {
	if targetType == "" || targetType == "any" {
		return
	}
	// Check if the buffer already ends with a concrete type expression
	buf := t.w.buf.String()
	lastExpr := lastExprInBuffer(buf)
	if len(lastExpr) > 0 && codeProducesConcreteType(lastExpr) {
		return
	}
	// Check if the last expression is a tracked variable with known concrete Go type
	varName := strings.TrimSpace(lastExpr)
	if t.goVarTypes != nil {
		if goType, ok := t.goVarTypes[varName]; ok && goType != "any" {
			return // concrete variable — no assertion needed
		}
	}
	t.w.writef(".(%s)", targetType)
}

// writeTypeAssertionChecked writes .(targetType) only if the given AST expression
// produces an any/interface type at Go level. Uses checker + tracker for accurate detection.
func (t *Transpiler) writeTypeAssertionChecked(targetType string, expr *ast.Node) {
	if targetType == "" || targetType == "any" {
		return
	}
	if expr != nil && t.exprProducesConcreteGoType(expr) {
		return
	}
	t.w.writef(".(%s)", targetType)
}

// lastExprInBuffer extracts a rough approximation of the last expression from the buffer.
func lastExprInBuffer(buf string) string {
	// Find the last line
	lastNewline := strings.LastIndex(buf, "\n")
	var line string
	if lastNewline >= 0 {
		line = strings.TrimSpace(buf[lastNewline+1:])
	} else {
		line = strings.TrimSpace(buf)
	}
	// If it's an assignment, look at the RHS
	if eqIdx := strings.LastIndex(line, " = "); eqIdx >= 0 {
		rhs := strings.TrimSpace(line[eqIdx+3:])
		if rhs != "" {
			return rhs
		}
	}
	return line
}

// emitTypeAssertionOrConversion writes a type assertion .() or conversion T() for a return expression.
// If the expression produces a concrete Go type, uses conversion instead of assertion.
// If the target type equals the expression type, skips entirely.
func (t *Transpiler) emitReturnTypeCoercion(exprCode string, expr *ast.Node, targetType string) {
	if targetType == "" || targetType == "any" {
		return
	}
	// Check if expression produces a concrete Go type
	isConcrete := codeProducesConcreteType(exprCode)
	if !isConcrete && expr != nil {
		isConcrete = t.exprProducesConcreteGoType(expr)
	}
	if isConcrete {
		if t.isTypeParam(targetType) {
			// Concrete → type parameter: need type conversion T(expr)
			// Re-emit as T(expr) — but exprCode is already written, so wrap it
			// Actually, we can't wrap already-written code. This is called AFTER expr is written.
			// So we just skip the assertion. The caller should handle conversion before calling.
		}
		// Same concrete type or incompatible → skip assertion (Go compiler handles)
		return
	}
	t.w.writef(".(%s)", targetType)
}

// getDeclaredGoType returns the GoTypeInfo for a symbol's declared type (ignoring narrowing).
// Use this to know the Go variable's static type (what it was declared as).
func (t *Transpiler) getDeclaredGoType(node *ast.Node) GoTypeInfo {
	if t.ck == nil || node == nil {
		return GoTypeInfo{Category: GoTypeJSObject, GoStr: "any"}
	}
	// Unwrap parenthesized and type assertion expressions to find the underlying identifier
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
	if inner.Kind != ast.KindIdentifier {
		// For non-identifiers, fall back to getGoType
		return t.getGoType(node)
	}
	node = inner
	sym := t.ck.GetSymbolAtLocation(node)
	if sym == nil {
		return t.getGoType(node)
	}
	declType := t.ck.GetTypeOfSymbol(sym)
	if declType == nil {
		return t.getGoType(node)
	}
	return t.tm.goTypeInfo(declType)
}

// TranspileResult holds the output of a transpilation.
type TranspileResult struct {
	GoSource string // The generated Go source code
	Errors   []string
}

// ProjectResult holds the output of a multi-file transpilation.
type ProjectResult struct {
	// Files maps output file paths (relative to outDir) to Go source code.
	Files  map[string]string
	Errors []string
	// GoImports holds third-party Go module paths imported via go: prefix.
	GoImports []string
}

// TranspileFile transpiles a single TypeScript file to Go source code.
// The file is treated as the entry point (generates func main()).
func TranspileFile(filename string, pkgName string) (*TranspileResult, error) {
	return transpileFile(filename, pkgName, true)
}

// TranspileLibraryFile transpiles a TypeScript file as a library (no func main()).
// Top-level statements become func init().
func TranspileLibraryFile(filename string, pkgName string) (*TranspileResult, error) {
	return transpileFile(filename, pkgName, false)
}

func transpileFile(filename string, pkgName string, isEntry bool) (*TranspileResult, error) {
	code, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return transpileSource(filename, string(code), pkgName, isEntry)
}

// TranspileSource transpiles TypeScript source code to Go source code.
func TranspileSource(filename string, source string, pkgName string) (*TranspileResult, error) {
	return transpileSource(filename, source, pkgName, true)
}

func transpileSource(filename string, source string, pkgName string, isEntry bool) (*TranspileResult, error) {
	if pkgName == "" {
		pkgName = "main"
	}

	absFile, err := filepath.Abs(filename)
	if err != nil {
		absFile = filename
	}

	// Set up the compiler pipeline with bundled lib.d.ts for full type resolution
	cwd := filepath.Dir(absFile)
	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCachedFSCompilerHost(cwd, fs, bundled.LibPath(), nil, nil)

	compilerOpts := &core.CompilerOptions{
		NoEmit:       core.TSTrue,
		SkipLibCheck: core.TSTrue,
		AllowJs:      core.TSTrue,
	}

	config := tsoptions.NewParsedCommandLine(
		compilerOpts,
		[]string{absFile},
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          cwd,
		},
	)

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	ctx := context.Background()
	result := &TranspileResult{}

	// Find our source file
	var sourceFile *ast.SourceFile
	for _, sf := range program.SourceFiles() {
		if sf.FileName() == absFile {
			sourceFile = sf
			break
		}
	}

	if sourceFile == nil {
		return nil, fmt.Errorf("source file not found in program: %s", absFile)
	}

	// Get the type checker
	ck, done := program.GetTypeCheckerForFile(ctx, sourceFile)
	defer done()

	// Create the transpiler
	t := &Transpiler{
		w:                newGoWriter(),
		ck:               ck,
		pkgName:          pkgName,
		isEntryFile:      isEntry,
		classNames:       make(map[string]bool),
		arrayCallbackIdx: -1,
	}
	t.tm = newTypeMapper(ck)

	// Emit the source file
	t.emitSourceFile(sourceFile)

	// Pass pending imports to writer for auto-resolution in renderFile
	t.w.pendingImports = t.pendingImports

	// Render the final Go source
	goSource, err := t.w.renderFile(pkgName)
	if err != nil {
		result.GoSource = goSource // Include raw source for debugging
		result.Errors = append(result.Errors, err.Error())
		return result, nil
	}

	result.GoSource = goSource
	return result, nil
}

// emitSourceFile processes all top-level statements in a source file.
func (t *Transpiler) emitSourceFile(sf *ast.SourceFile) {
	if sf.Statements == nil {
		return
	}

	// Process imports first (they add to the writer's import map)
	var decls []*ast.Node
	var stmts []*ast.Node

	for _, node := range sf.Statements.Nodes {
		if node.Kind == ast.KindImportDeclaration {
			t.emitImportDeclaration(node)
		}
	}

	// Pre-scan: collect local type names and exported names for same-file forward references.
	t.localTypeNames = make(map[string]bool)
	for _, node := range sf.Statements.Nodes {
		switch node.Kind {
		case ast.KindClassDeclaration:
			if node.Name() != nil {
				t.localTypeNames[goExportedName(nodeText(node.Name()))] = true
			}
		case ast.KindInterfaceDeclaration:
			if node.Name() != nil {
				t.localTypeNames[goExportedName(nodeText(node.Name()))] = true
			}
		case ast.KindTypeAliasDeclaration:
			if node.Name() != nil {
				t.localTypeNames[goExportedName(nodeText(node.Name()))] = true
			}
		}
	}
	for _, node := range sf.Statements.Nodes {
		if node.Kind == ast.KindVariableStatement && isExported(node) {
			varStmt := node.AsVariableStatement()
			declList := varStmt.DeclarationList.AsVariableDeclarationList()
			if declList.Declarations != nil {
				for _, decl := range declList.Declarations.Nodes {
					name := decl.Name()
					if name != nil && name.Kind == ast.KindIdentifier {
						if t.samePackageExports == nil {
							t.samePackageExports = make(map[string]bool)
						}
						t.samePackageExports[name.AsIdentifier().Text] = true
					}
				}
			}
		}
		if node.Kind == ast.KindFunctionDeclaration && isExported(node) {
			fd := node.AsFunctionDeclaration()
			if fd.Name() != nil && fd.Name().Kind == ast.KindIdentifier {
				if t.samePackageExports == nil {
					t.samePackageExports = make(map[string]bool)
				}
				t.samePackageExports[fd.Name().AsIdentifier().Text] = true
			}
		}
	}

	// Link import maps to type mapper for cross-package type qualification
	t.tm.importedNames = t.importedNames
	t.tm.pendingImports = t.pendingImports

	for _, node := range sf.Statements.Nodes {
		switch node.Kind {
		case ast.KindImportDeclaration:
			// Already processed above
		case ast.KindExportDeclaration:
			decls = append(decls, node)
		case ast.KindFunctionDeclaration, ast.KindClassDeclaration,
			ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration,
			ast.KindEnumDeclaration:
			decls = append(decls, node)
		case ast.KindVariableStatement:
			if t.isEntryFile {
				// Entry file: executable statements go in main()
				stmts = append(stmts, node)
			} else {
				// Library file: all variable statements at package level
				decls = append(decls, node)
			}
		case ast.KindExportAssignment:
			decls = append(decls, node)
		default:
			stmts = append(stmts, node)
		}
	}

	// Emit declarations first
	for _, decl := range decls {
		// Exported variable statements at package level need var syntax
		if decl.Kind == ast.KindVariableStatement {
			t.emitPackageLevelVarStatement(decl)
		} else {
			t.emitStatement(decl)
		}
	}

	// Emit executable statements
	if len(stmts) > 0 {
		if t.isEntryFile {
			// Entry file: wrap in func main()
			t.w.write("func main()")
			t.w.openBlock()
			for _, stmt := range stmts {
				t.emitStatement(stmt)
			}
			t.w.closeBlock()
		} else {
			// Library file: emit as func init() for side-effect code
			t.w.write("func init()")
			t.w.openBlock()
			for _, stmt := range stmts {
				t.emitStatement(stmt)
			}
			t.w.closeBlock()
		}
	}

	// Import resolution for qualified type names is handled in renderFile via pendingImports.
}

// TranspileToDir transpiles a TypeScript file and writes the output to a directory.
func TranspileToDir(filename string, outDir string, pkgName string) error {
	result, err := TranspileFile(filename, pkgName)
	if err != nil {
		return err
	}

	if len(result.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "Transpilation warnings:")
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	}

	// Create output directory
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Write Go source file
	baseName := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	outFile := filepath.Join(outDir, baseName+".go")
	if err := os.WriteFile(outFile, []byte(result.GoSource), 0o644); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	// Write go.mod if not exists
	goModPath := filepath.Join(outDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		goMod := fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/i2y/ramune v0.4.0\n", baseName)
		if err := os.WriteFile(goModPath, []byte(goMod), 0o644); err != nil {
			return fmt.Errorf("writing go.mod: %w", err)
		}
	}

	return nil
}

// TranspileProject transpiles multiple TypeScript files into a Go project.
// entryFile is the main file (gets func main()), others become library packages.
// moduleName is the Go module name (e.g., "myapp").
func TranspileProject(files []string, entryFile string, _ string, moduleName string) (*ProjectResult, error) {
	return transpileProject(files, entryFile, moduleName, false)
}

// TranspileProjectWithNpm transpiles multiple TS files with npm package resolution.
func TranspileProjectWithNpm(files []string, entryFile string, _ string, moduleName string) (*ProjectResult, error) {
	return transpileProject(files, entryFile, moduleName, true)
}

// collectProjectDeclarations performs a lightweight first pass over all source files
// to collect type aliases and class names into shared maps for cross-file resolution.
func collectProjectDeclarations(program *compiler.Program, absFileSet map[string]bool, ctx context.Context, moduleName, projectRoot string, resolver *npmResolver) *projectSharedState {
	shared := &projectSharedState{
		typeAliases: make(map[string]string),
		classNames:  make(map[string]bool),
	}
	for _, sf := range program.SourceFiles() {
		if !absFileSet[sf.FileName()] {
			continue
		}
		ck, done := program.GetTypeCheckerForFile(ctx, sf)
		t := &Transpiler{
			w:                newGoWriter(), // throwaway writer
			ck:               ck,
			goModuleName:     moduleName,
			projectRoot:      projectRoot,
			classNames:       shared.classNames,
			npmResolver:      resolver,
			arrayCallbackIdx: -1,
		}
		// Compute currentFileDir for relative import resolution
		relPath, _ := filepath.Rel(projectRoot, sf.FileName())
		t.currentFileDir = filepath.Dir(relPath)

		t.tm = newTypeMapper(ck)
		t.tm.typeAliases = shared.typeAliases
		t.collectDeclarations(sf)
		done()
	}
	return shared
}

func transpileProject(files []string, entryFile string, moduleName string, enableNpm bool) (*ProjectResult, error) {
	if moduleName == "" {
		moduleName = "app"
	}
	if entryFile == "" && len(files) > 0 {
		entryFile = files[0]
	}
	// Special value: "__none__" means no entry file (all files are libraries)
	if entryFile == "__none__" {
		entryFile = ""
	}

	// Resolve all file paths to absolute
	absFiles := make([]string, len(files))
	for i, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, fmt.Errorf("resolving path %s: %w", f, err)
		}
		absFiles[i] = abs
	}
	absEntry, _ := filepath.Abs(entryFile)

	// Find the common root directory
	projectRoot := commonDir(absFiles)

	// Set up npm resolver if enabled and node_modules exists
	var resolver *npmResolver
	if enableNpm {
		nmDir := filepath.Join(projectRoot, "node_modules")
		if info, err := os.Stat(nmDir); err == nil && info.IsDir() {
			resolver = newNpmResolver(projectRoot, moduleName)
		}
	}

	// Set up the compiler pipeline with all files and bundled lib.d.ts
	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCachedFSCompilerHost(projectRoot, fs, bundled.LibPath(), nil, nil)

	compilerOpts := &core.CompilerOptions{
		NoEmit:       core.TSTrue,
		SkipLibCheck: core.TSTrue,
		AllowJs:      core.TSTrue,
	}

	config := tsoptions.NewParsedCommandLine(
		compilerOpts,
		absFiles,
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          projectRoot,
		},
	)

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	ctx := context.Background()
	result := &ProjectResult{Files: make(map[string]string)}

	// Build a set of our source files (exclude lib files)
	absFileSet := make(map[string]bool)
	for _, f := range absFiles {
		absFileSet[f] = true
	}

	// Pass 1: Collect type aliases and class names from all files
	shared := collectProjectDeclarations(program, absFileSet, ctx, moduleName, projectRoot, resolver)

	// Pass 2: Transpile each file with shared state
	for _, sf := range program.SourceFiles() {
		if !absFileSet[sf.FileName()] {
			continue
		}

		ck, done := program.GetTypeCheckerForFile(ctx, sf)

		isEntry := sf.FileName() == absEntry

		// Determine Go package name based on relative path
		relPath, _ := filepath.Rel(projectRoot, sf.FileName())
		dir := filepath.Dir(relPath)
		base := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
		pkgName := "main"
		if !isEntry {
			if dir == "." {
				pkgName = sanitizePkgName(base)
			} else {
				pkgName = sanitizePkgName(filepath.Base(dir))
			}
		}

		t := &Transpiler{
			w:                newGoWriter(),
			ck:               ck,
			pkgName:          pkgName,
			goModuleName:     moduleName,
			projectRoot:      projectRoot,
			isEntryFile:      isEntry,
			npmResolver:      resolver,
			classNames:       shared.classNames,
			arrayCallbackIdx: -1,
			currentFileDir:   dir,
		}
		t.tm = newTypeMapper(ck)
		t.tm.typeAliases = shared.typeAliases

		t.emitSourceFile(sf)

		// Pass pending imports to writer for auto-resolution in renderFile
		t.w.pendingImports = t.pendingImports

		goSource, err := t.w.renderFile(pkgName)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", relPath, err.Error()))
			result.Files[relToGo(relPath, isEntry)] = goSource
		} else {
			result.Files[relToGo(relPath, isEntry)] = goSource
		}

		for _, imp := range t.goNativeImports {
			if isThirdPartyGoImport(imp) {
				result.GoImports = append(result.GoImports, imp)
			}
		}

		done()
	}

	// Transpile resolved npm packages
	if resolver != nil {
		for _, analysis := range resolver.resolved {
			if analysis == nil || len(analysis.SourceFiles) == 0 {
				continue
			}
			npmResult, err := TranspileProject(analysis.SourceFiles, analysis.EntryFile, "", moduleName+"/npm/"+sanitizePkgName(analysis.Name))
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("npm %s: %s", analysis.Name, err.Error()))
				continue
			}
			for path, source := range npmResult.Files {
				result.Files[filepath.Join("npm", sanitizePkgName(analysis.Name), path)] = source
			}
			result.Errors = append(result.Errors, npmResult.Errors...)
		}
	}

	return result, nil
}

// TranspileProjectToDir transpiles multiple TS files and writes the Go project to outDir.
func TranspileProjectToDir(files []string, entryFile string, outDir string, moduleName string) error {
	return transpileProjectToDir(files, entryFile, outDir, moduleName, false)
}

// TranspileProjectToDirWithNpm transpiles with npm package resolution.
func TranspileProjectToDirWithNpm(files []string, entryFile string, outDir string, moduleName string) error {
	return transpileProjectToDir(files, entryFile, outDir, moduleName, true)
}

func transpileProjectToDir(files []string, entryFile string, outDir string, moduleName string, enableNpm bool) error {
	var result *ProjectResult
	var err error
	if enableNpm {
		result, err = TranspileProjectWithNpm(files, entryFile, outDir, moduleName)
	} else {
		result, err = TranspileProject(files, entryFile, outDir, moduleName)
	}
	if err != nil {
		return err
	}

	if len(result.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "Transpilation warnings:")
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	}

	// Write each file
	for relPath, goSource := range result.Files {
		outFile := filepath.Join(outDir, relPath)
		if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", relPath, err)
		}
		if err := os.WriteFile(outFile, []byte(goSource), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", relPath, err)
		}
	}

	// Write go.mod
	if moduleName == "" {
		moduleName = "app"
	}
	goModPath := filepath.Join(outDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		result.GoImports = dedupeGoImports(result.GoImports)

		var b strings.Builder
		fmt.Fprintf(&b, "module %s\n\ngo 1.26\n\nrequire github.com/i2y/ramune v0.4.0\n", moduleName)
		if len(result.GoImports) > 0 {
			b.WriteString("\nrequire (\n")
			for _, mod := range result.GoImports {
				fmt.Fprintf(&b, "\t%s v0.0.0 // run: go mod tidy\n", mod)
			}
			b.WriteString(")\n")
		}

		if err := os.WriteFile(goModPath, []byte(b.String()), 0o644); err != nil {
			return fmt.Errorf("writing go.mod: %w", err)
		}

		if len(result.GoImports) > 0 {
			fmt.Fprintln(os.Stderr, "Run 'go mod tidy' in the output directory to resolve module versions.")
		}
	}

	return nil
}

// relToGo converts a relative TS file path to a Go file path.
// Entry file goes to the root, library files go to subdirectories by basename.
func relToGo(relPath string, isEntry bool) string {
	base := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	dir := filepath.Dir(relPath)
	// Sanitize names for Go: replace hyphens with underscores
	base = strings.ReplaceAll(base, "-", "_")

	if isEntry {
		return base + ".go"
	}

	// Non-entry file in root dir → create a subdirectory package
	if dir == "." {
		return filepath.Join(base, base+".go")
	}

	// Preserve directory structure, sanitize dir names
	sanitizedDir := strings.ReplaceAll(dir, "-", "_")
	return filepath.Join(sanitizedDir, base+".go")
}

// isThirdPartyGoImport returns true if the Go import path is a third-party module
// (contains a dot in the first path component, e.g., "github.com/gin-gonic/gin").
func isThirdPartyGoImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return strings.ContainsRune(first, '.')
}

// goModulePath extracts the Go module path from a package import path.
// For "github.com/gin-gonic/gin/middleware" it returns "github.com/gin-gonic/gin".
// Most modules use 3 components: host/owner/repo.
func goModulePath(importPath string) string {
	parts := strings.Split(importPath, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[:3], "/")
	}
	return importPath
}

// dedupeGoImports returns unique Go module paths from a list of import paths.
func dedupeGoImports(imports []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, imp := range imports {
		mod := goModulePath(imp)
		if !seen[mod] {
			seen[mod] = true
			result = append(result, mod)
		}
	}
	return result
}

// commonDir returns the longest common directory of a set of absolute paths.
func commonDir(paths []string) string {
	if len(paths) == 0 {
		cwd, _ := os.Getwd()
		return cwd
	}
	if len(paths) == 1 {
		return filepath.Dir(paths[0])
	}

	common := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		d := filepath.Dir(p)
		for !strings.HasPrefix(d+string(filepath.Separator), common+string(filepath.Separator)) {
			parent := filepath.Dir(common)
			if parent == common {
				return common
			}
			common = parent
		}
	}
	return common
}
