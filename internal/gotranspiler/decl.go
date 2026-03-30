package gotranspiler

import (
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// emitTypeParameters emits Go type parameters [T any, U Constraint] from a function-like or class-like node.
// Also registers the type parameter names in the type mapper for scope tracking.
func (t *Transpiler) emitTypeParameters(node *ast.Node) {
	typeParams := node.TypeParameterList()
	if typeParams == nil || len(typeParams.Nodes) == 0 {
		return
	}

	// Register type params in scope
	if t.tm.typeParams == nil {
		t.tm.typeParams = make(map[string]bool)
	}
	for _, tp := range typeParams.Nodes {
		t.tm.typeParams[tp.Name().AsIdentifier().Text] = true
	}

	t.w.write("[")
	for i, tp := range typeParams.Nodes {
		if i > 0 {
			t.w.write(", ")
		}
		tpDecl := tp.AsTypeParameter()
		tpName := tp.Name().AsIdentifier().Text

		t.w.write(goExportedName(tpName))

		// Constraint
		if tpDecl.Constraint != nil {
			goConstraint := t.mapConstraint(tpDecl.Constraint)
			t.w.writef(" %s", goConstraint)
		} else {
			t.w.write(" any")
		}
	}
	t.w.write("]")
}

// mapConstraint maps a TypeScript type constraint to a Go type constraint string.
func (t *Transpiler) mapConstraint(constraint *ast.Node) string {
	if constraint == nil {
		return "any"
	}

	switch constraint.Kind {
	case ast.KindStringKeyword:
		return "~string"
	case ast.KindNumberKeyword:
		return "~float64"
	case ast.KindBooleanKeyword:
		return "~bool"
	case ast.KindTypeReference:
		ref := constraint.AsTypeReference()
		if ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			return goTypeName(ref.TypeName.AsIdentifier().Text)
		}
	}

	// Fallback: use the checker's type info
	if t.ck != nil {
		ct := t.ck.GetTypeAtLocation(constraint)
		if ct != nil {
			goType := t.tm.goType(ct)
			if goType != "" && goType != "any" {
				return goType
			}
		}
	}

	return "any"
}

// emitFunctionDeclaration handles top-level function declarations.
func (t *Transpiler) emitFunctionDeclaration(node *ast.Node) {
	name := node.Name()
	if name == nil {
		t.w.writeln("/* anonymous function declaration */")
		return
	}

	funcName := nodeText(name)
	goName := goVarName(funcName)
	if isExported(node) {
		goName = goExportedName(funcName)
	}

	// Save/reset *string param tracking before parameter emission
	savedPtrStringVars := t.goPtrStringVars
	t.goPtrStringVars = nil

	t.w.writef("func %s", goName)
	t.emitTypeParameters(node)
	t.w.write("(")
	t.emitParameterList(node)
	t.w.write(")")

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

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
		if !isPrimitiveOrCollectionType(innerType) && !t.isTypeParam(innerType) {
			innerType = "any"
		}
		retType = innerType
		t.w.writef(" *promise.Promise[%s]", innerType)
	} else if retType != "" {
		t.w.writef(" %s", retType)
	}

	body := node.Body()
	if body == nil {
		t.w.writeln(" {}")
		t.goPtrStringVars = savedPtrStringVars
		t.w.newline()
		return
	}

	savedRetType := t.currentRetType
	t.currentRetType = retType

	if isAsync {
		// Wrap body in promise.New
		innerType := retType
		if innerType == "" {
			innerType = "any"
		}
		t.w.openBlock()
		t.w.writef("return promise.New[%s](func(__resolve func(%s), __reject func(error))", innerType, innerType)
		t.w.openBlock()
		savedAsync := t.inAsyncBody
		t.inAsyncBody = true
		block := body.AsBlock()
		if block.Statements != nil {
			for _, stmt := range block.Statements.Nodes {
				t.emitStatement(stmt)
			}
		}
		t.inAsyncBody = savedAsync
		t.w.closeBlockInline() // close promise.New callback
		t.w.writeln(")")       // close promise.New call
		t.w.closeBlock()       // close function body
	} else {
		t.needsDefaultReturn = retType != ""
		t.emitBlock(body)
	}
	t.currentRetType = savedRetType
	t.goPtrStringVars = savedPtrStringVars
	t.w.newline()
}

// emitInterfaceDeclaration handles interface declarations.
// Interfaces with only methods → Go interface.
// Interfaces with properties → Go struct.
func (t *Transpiler) emitInterfaceDeclaration(node *ast.Node) {
	iface := node.AsInterfaceDeclaration()
	name := node.Name()
	if name == nil {
		return
	}
	goName := goTypeName(nodeText(name))

	// Check if it has only methods (→ Go interface) or also properties (→ Go struct)
	hasProps := false
	hasMethods := false
	if iface.Members != nil {
		for _, member := range iface.Members.Nodes {
			switch member.Kind {
			case ast.KindPropertySignature:
				hasProps = true
			case ast.KindMethodSignature:
				hasMethods = true
			}
		}
	}

	if !hasProps && !hasMethods {
		// Empty or call-signature-only interface — skip (avoids name collision with same-name variables)
		return
	}

	if hasProps {
		// Emit as struct
		t.w.writef("type %s", goName)
		t.emitTypeParameters(node)
		t.w.write(" struct")
		t.w.openBlock()
		if iface.Members != nil {
			for _, member := range iface.Members.Nodes {
				if member.Kind == ast.KindPropertySignature {
					t.emitPropertySignatureAsField(member)
				}
			}
		}
		t.w.closeBlock()
		t.w.newline()

		// If it also has methods, emit them as methods on the struct
		if hasMethods && iface.Members != nil {
			// Build type parameter suffix (e.g., "[T]")
			typeParamSuffix := ""
			typeParams := node.TypeParameterList()
			if typeParams != nil && len(typeParams.Nodes) > 0 {
				var parts []string
				for _, tp := range typeParams.Nodes {
					parts = append(parts, goExportedName(tp.Name().AsIdentifier().Text))
				}
				typeParamSuffix = "[" + strings.Join(parts, ", ") + "]"
			}
			for _, member := range iface.Members.Nodes {
				if member.Kind == ast.KindMethodSignature {
					t.emitMethodSignatureAsMethod(member, goName, typeParamSuffix)
				}
			}
		}
	} else {
		// Pure method interface → Go interface
		t.w.writef("type %s", goName)
		t.emitTypeParameters(node)
		t.w.write(" interface")
		t.w.openBlock()
		if iface.Members != nil {
			for _, member := range iface.Members.Nodes {
				if member.Kind == ast.KindMethodSignature {
					t.emitMethodSignatureInInterface(member)
				}
			}
		}
		t.w.closeBlock()
		t.w.newline()
	}
}

// emitPropertySignatureAsField emits an interface property as a Go struct field.
func (t *Transpiler) emitPropertySignatureAsField(node *ast.Node) {
	name := node.Name()
	if name == nil {
		return
	}

	propName := nodeText(name)
	goFieldName := goExportedName(propName)

	goType := "any"
	if t.ck != nil {
		propType := t.ck.GetTypeAtLocation(node)
		if propType != nil {
			goType = t.tm.goType(propType)
		}
	}
	if goType == "" {
		goType = "any"
	}

	t.w.writelnf("%s %s `json:\"%s\"`", goFieldName, goType, propName)
}

// emitMethodSignatureInInterface emits a method signature inside a Go interface.
func (t *Transpiler) emitMethodSignatureInInterface(node *ast.Node) {
	name := node.Name()
	if name == nil {
		return
	}
	methodName := goExportedName(nodeText(name))

	t.w.writef("%s(", methodName)

	// Parameters
	params := node.Parameters()
	if params != nil {
		for i, param := range params {
			if i > 0 {
				t.w.write(", ")
			}
			goType := "any"
			if t.ck != nil {
				paramType := t.ck.GetTypeAtLocation(param)
				if paramType != nil {
					goType = t.tm.goType(paramType)
				}
			}
			t.w.write(goType)
		}
	}
	t.w.write(")")

	// Return type
	retType := t.getFuncReturnType(node)
	if retType != "" {
		t.w.writef(" %s", retType)
	}
	t.w.newline()
}

// emitMethodSignatureAsMethod emits a method signature as a Go method on a struct.
func (t *Transpiler) emitMethodSignatureAsMethod(node *ast.Node, structName string, typeParamSuffix string) {
	name := node.Name()
	if name == nil {
		return
	}
	methodName := goExportedName(nodeText(name))
	receiverName := string([]rune(structName)[0:1])

	t.w.writef("func (%s *%s%s) %s(", toCamelCase(receiverName), structName, typeParamSuffix, methodName)
	t.emitParameterList(node)
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	if retType != "" {
		t.w.writef(" %s", retType)
	}
	t.w.openBlock()
	t.w.writeln(`panic("not implemented")`)
	t.w.closeBlock()
	t.w.newline()
}

// collectDeclarations performs a lightweight pass over a source file to collect
// type aliases and class names into the shared state maps.
func (t *Transpiler) collectDeclarations(sf *ast.SourceFile) {
	if sf.Statements == nil {
		return
	}

	// Process imports first so type mapper can qualify cross-package type names
	for _, node := range sf.Statements.Nodes {
		if node.Kind == ast.KindImportDeclaration {
			t.emitImportDeclaration(node)
		}
	}
	t.tm.importedNames = t.importedNames
	t.tm.pendingImports = t.pendingImports

	// Collect type aliases and class names
	for _, node := range sf.Statements.Nodes {
		switch node.Kind {
		case ast.KindTypeAliasDeclaration:
			t.collectTypeAlias(node)
		case ast.KindClassDeclaration:
			name := node.Name()
			if name != nil {
				t.classNames[goTypeName(nodeText(name))] = true
			}
		}
	}
}

// collectTypeAlias extracts a type alias mapping without emitting Go code.
func (t *Transpiler) collectTypeAlias(node *ast.Node) {
	ta := node.AsTypeAliasDeclaration()
	name := node.Name()
	if name == nil {
		return
	}
	goName := goTypeName(nodeText(name))

	if ta.Type == nil || t.ck == nil {
		return
	}

	// Skip discriminated unions (they produce interface types, not simple aliases)
	aliasType := t.ck.GetTypeAtLocation(ta.Type)
	if aliasType != nil && aliasType.Flags()&checker.TypeFlagsUnion != 0 {
		union := aliasType.AsUnionType()
		variants := t.getDiscriminatedUnionVariants(union)
		if len(variants) > 0 {
			return
		}
	}

	goType := t.tm.goType(aliasType)

	if t.tm.typeAliases == nil {
		t.tm.typeAliases = make(map[string]string)
	}
	if goType != "" && goType != goName && goType != "any" {
		t.tm.typeAliases[goName] = goType
	}
	if goType == "any" && ta.Type != nil {
		if t.isRecordLikeType(ta.Type) {
			t.tm.typeAliases[goName] = "map[string]any"
		}
	}
}

// emitTypeAliasDeclaration handles type aliases.
func (t *Transpiler) emitTypeAliasDeclaration(node *ast.Node) {
	ta := node.AsTypeAliasDeclaration()
	name := node.Name()
	if name == nil {
		return
	}
	goName := goTypeName(nodeText(name))

	// Check for discriminated union: type Shape = Circle | Square
	if ta.Type != nil && t.ck != nil {
		aliasType := t.ck.GetTypeAtLocation(ta.Type)
		if aliasType != nil && aliasType.Flags()&checker.TypeFlagsUnion != 0 {
			union := aliasType.AsUnionType()
			variants := t.getDiscriminatedUnionVariants(union)
			if len(variants) > 0 {
				t.emitDiscriminatedUnion(goName, variants)
				return
			}
		}
	}

	if ta.Type != nil && t.ck != nil {
		aliasType := t.ck.GetTypeAtLocation(ta.Type)
		goType := t.tm.goType(aliasType)

		// Record alias mapping so goTypeInfo can resolve alias names to concrete Go types.
		if t.tm.typeAliases == nil {
			t.tm.typeAliases = make(map[string]string)
		}
		if goType != "" && goType != goName && goType != "any" {
			t.tm.typeAliases[goName] = goType
		}
		// If goType resolved to "any" but the type alias involves Record/Map patterns,
		// record as map[string]any (handles multi-file cases where checker can't resolve complex generics)
		if goType == "any" && ta.Type != nil {
			if t.isRecordLikeType(ta.Type) {
				t.tm.typeAliases[goName] = "map[string]any"
			}
		}

		if goType != "" && goType != "any" && goType != goName {
			// Skip primitive type aliases and unresolved type parameters
			switch goType {
			case "bool", "string", "float64", "int":
				// skip emit (but alias is recorded above)
			default:
				if aliasType != nil && aliasType.Flags()&checker.TypeFlagsTypeParameter != 0 {
					break
				}
				t.w.writelnf("type %s = %s", goName, goType)
			}
		}
	} else {
		// No checker — skip rather than emit useless `type X = any`
	}
	t.w.newline()
}

// emitEnumDeclaration handles enum declarations.
func (t *Transpiler) emitEnumDeclaration(node *ast.Node) {
	enumDecl := node.AsEnumDeclaration()
	name := node.Name()
	if name == nil {
		return
	}
	goName := goTypeName(nodeText(name))

	// Determine if it's a string or numeric enum
	isStringEnum := false
	if enumDecl.Members != nil {
		for _, member := range enumDecl.Members.Nodes {
			em := member.AsEnumMember()
			if em.Initializer != nil && em.Initializer.Kind == ast.KindStringLiteral {
				isStringEnum = true
				break
			}
		}
	}

	if isStringEnum {
		t.w.writelnf("type %s string", goName)
		t.w.newline()
		t.w.writeln("const (")
		t.w.indent++
		if enumDecl.Members != nil {
			for _, member := range enumDecl.Members.Nodes {
				memberName := member.Name()
				em := member.AsEnumMember()
				if memberName != nil && memberName.Kind == ast.KindIdentifier {
					goMember := goName + goExportedName(memberName.AsIdentifier().Text)
					if em.Initializer != nil && em.Initializer.Kind == ast.KindStringLiteral {
						t.w.writelnf("%s %s = %q", goMember, goName, em.Initializer.AsStringLiteral().Text)
					} else {
						t.w.writelnf("%s %s = %q", goMember, goName, memberName.AsIdentifier().Text)
					}
				}
			}
		}
		t.w.indent--
		t.w.writeln(")")
	} else {
		t.w.writelnf("type %s int", goName)
		t.w.newline()
		t.w.writeln("const (")
		t.w.indent++
		if enumDecl.Members != nil {
			for i, member := range enumDecl.Members.Nodes {
				memberName := member.Name()
				em := member.AsEnumMember()
				if memberName != nil && memberName.Kind == ast.KindIdentifier {
					goMember := goName + goExportedName(memberName.AsIdentifier().Text)
					if em.Initializer != nil && em.Initializer.Kind == ast.KindNumericLiteral {
						t.w.writelnf("%s %s = %s", goMember, goName, em.Initializer.AsNumericLiteral().Text)
					} else if i == 0 {
						t.w.writelnf("%s %s = iota", goMember, goName)
					} else {
						t.w.writelnf("%s", goMember)
					}
				}
			}
		}
		t.w.indent--
		t.w.writeln(")")
	}
	t.w.newline()
}

// emitClassDeclaration handles class declarations.
func (t *Transpiler) emitClassDeclaration(node *ast.Node) {
	classDecl := node.AsClassDeclaration()
	name := node.Name()
	if name == nil {
		return
	}
	className := goTypeName(nodeText(name))
	t.classNames[className] = true

	// Collect fields, methods, constructor, getters, setters
	var fields []*ast.Node
	var methods []*ast.Node
	var staticFields []*ast.Node
	var staticMethods []*ast.Node
	var getters []*ast.Node
	var setters []*ast.Node
	var constructor *ast.Node
	var baseClassName string

	// Check for extends clause
	if classDecl.HeritageClauses != nil {
		for _, clause := range classDecl.HeritageClauses.Nodes {
			hc := clause.AsHeritageClause()
			if hc.Token == ast.KindExtendsKeyword && hc.Types != nil {
				for _, typeExpr := range hc.Types.Nodes {
					ewta := typeExpr.AsExpressionWithTypeArguments()
					if ewta.Expression.Kind == ast.KindIdentifier {
						baseClassName = goTypeName(ewta.Expression.AsIdentifier().Text)
					}
				}
			}
		}
	}

	if classDecl.Members != nil {
		for _, member := range classDecl.Members.Nodes {
			isStatic := ast.HasSyntacticModifier(member, ast.ModifierFlagsStatic)
			switch member.Kind {
			case ast.KindPropertyDeclaration:
				if isStatic {
					staticFields = append(staticFields, member)
				} else {
					fields = append(fields, member)
				}
			case ast.KindMethodDeclaration:
				if isStatic {
					staticMethods = append(staticMethods, member)
				} else {
					methods = append(methods, member)
				}
			case ast.KindGetAccessor:
				getters = append(getters, member)
			case ast.KindSetAccessor:
				setters = append(setters, member)
			case ast.KindConstructor:
				constructor = member
			}
		}
	}

	// Build private field map for this class
	savedPrivateFields := t.privateFields
	t.privateFields = make(map[string]string)
	for _, field := range fields {
		fname := field.Name()
		if fname == nil {
			continue
		}
		var tsName string
		if ast.IsPrivateIdentifier(fname) {
			tsName = strings.TrimPrefix(fname.AsPrivateIdentifier().Text, "#")
		} else {
			tsName = nodeText(fname)
		}
		if isPrivateOrProtected(field) || ast.IsPrivateIdentifier(fname) {
			t.privateFields[tsName] = goVarName(toCamelCase(tsName))
		}
	}

	// Emit struct
	t.w.writef("type %s", className)
	t.emitTypeParameters(node)
	t.w.write(" struct")
	t.w.openBlock()
	if baseClassName != "" {
		// Map well-known TS base classes to Go equivalents
		if baseClassName == "Error" {
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.writelnf("jsrt.JSError")
		} else {
			t.w.writelnf("%s", baseClassName)
		}
	}
	for _, field := range fields {
		t.emitClassField(field)
	}
	t.w.closeBlock()
	t.w.newline()

	// Emit constructor
	t.emitConstructor(constructor, className, baseClassName, node)

	// Build type parameter suffix for receiver type (e.g., "[T]" for Box[T])
	typeParamSuffix := ""
	typeParams := node.TypeParameterList()
	if typeParams != nil && len(typeParams.Nodes) > 0 {
		var parts []string
		for _, tp := range typeParams.Nodes {
			parts = append(parts, goExportedName(tp.Name().AsIdentifier().Text))
		}
		typeParamSuffix = "[" + strings.Join(parts, ", ") + "]"
	}

	// Emit methods
	for _, method := range methods {
		t.emitClassMethod(method, className, typeParamSuffix)
	}

	// Emit getters as methods: get foo() → func (r *Class) Foo() RetType
	for _, getter := range getters {
		t.emitGetterMethod(getter, className, typeParamSuffix)
	}

	// Emit setters as methods: set foo(v) → func (r *Class) SetFoo(v ParamType)
	for _, setter := range setters {
		t.emitSetterMethod(setter, className, typeParamSuffix)
	}

	// Emit static fields as package-level variables
	for _, field := range staticFields {
		t.emitStaticField(field, className)
	}

	// Emit static methods as package-level functions
	for _, method := range staticMethods {
		t.emitStaticMethod(method, className)
	}

	t.privateFields = savedPrivateFields
}

// emitClassField emits a class property as a struct field.
func (t *Transpiler) emitClassField(node *ast.Node) {
	name := node.Name()
	if name == nil {
		return
	}

	// Handle #privateField or private/protected modifier
	var propName string
	if ast.IsPrivateIdentifier(name) {
		propName = name.AsPrivateIdentifier().Text
		propName = strings.TrimPrefix(propName, "#")
	} else {
		propName = nodeText(name)
	}

	goFieldName := goExportedName(propName)
	if isPrivateOrProtected(node) || ast.IsPrivateIdentifier(name) {
		goFieldName = goVarName(toCamelCase(propName))
	}

	goType := "any"
	if t.ck != nil {
		fieldType := t.ck.GetTypeAtLocation(node)
		if fieldType != nil {
			goType = t.tm.goType(fieldType)
		}
	}
	if goType == "" {
		goType = "any"
	}
	t.w.writelnf("%s %s", goFieldName, goType)
}

// emitConstructor emits a NewClassName factory function.
func (t *Transpiler) emitConstructor(node *ast.Node, className string, baseClassName string, classNode *ast.Node) {
	t.w.writef("func New%s", className)
	// Include type parameters from the class declaration
	if classNode != nil {
		t.emitTypeParameters(classNode)
	}
	t.w.write("(")
	if node != nil {
		t.emitParameterList(node)
	}
	t.w.writef(") *%s", className)
	// Include type params in return type
	if classNode != nil {
		typeParams := classNode.TypeParameterList()
		if typeParams != nil && len(typeParams.Nodes) > 0 {
			t.w.write("[")
			for i, tp := range typeParams.Nodes {
				if i > 0 {
					t.w.write(", ")
				}
				t.w.write(goExportedName(tp.Name().AsIdentifier().Text))
			}
			t.w.write("]")
		}
	}
	t.w.openBlock()
	// Include type params in struct literal for generic classes
	typeParamInst := ""
	if classNode != nil {
		tp := classNode.TypeParameterList()
		if tp != nil && len(tp.Nodes) > 0 {
			var names []string
			for _, p := range tp.Nodes {
				names = append(names, goExportedName(p.Name().AsIdentifier().Text))
			}
			typeParamInst = "[" + strings.Join(names, ", ") + "]"
		}
	}
	t.w.writef("s := &%s%s{}", className, typeParamInst)
	t.w.newline()

	savedReceiver := t.thisReceiver
	t.thisReceiver = "s"
	if node != nil {
		body := node.Body()
		if body != nil {
			block := body.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					t.emitConstructorStatement(stmt, "s", baseClassName)
				}
			}
		}
	}

	t.thisReceiver = savedReceiver
	t.w.writeln("return s")
	t.w.closeBlock()
	t.w.newline()
}

// emitConstructorStatement handles statements inside a constructor.
func (t *Transpiler) emitConstructorStatement(node *ast.Node, receiverVar string, baseClassName string) {
	if node.Kind == ast.KindExpressionStatement {
		exprStmt := node.AsExpressionStatement()
		if exprStmt.Expression.Kind == ast.KindBinaryExpression {
			bin := exprStmt.Expression.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindPropertyAccessExpression {
				prop := bin.Left.AsPropertyAccessExpression()
				if prop.Expression.Kind == ast.KindThisKeyword {
					propName := nodeText(prop.Name())
					goName := goExportedName(propName)
					if pf, ok := t.privateFields[propName]; ok {
						goName = pf
					}
					// If the field has a concrete type and the RHS might be any, add type assertion
					goLeft := ""
					if t.ck != nil {
						leftType := t.ck.GetTypeAtLocation(bin.Left)
						if leftType != nil {
							goLeft = t.tm.goType(leftType)
						}
					}
					rhs := t.captureExpr(bin.Right)
					needsAssertion := goLeft != "" && goLeft != "any" &&
						(strings.Contains(rhs, "jsrt.GetField(") || strings.Contains(rhs, "jsrt.Index(") ||
							strings.Contains(rhs, "jsrt.Obj(") || strings.Contains(rhs, ".Unwrap()"))
					t.w.writef("%s.%s = ", receiverVar, goName)
					if needsAssertion {
						t.w.writef("%s.(%s)", rhs, goLeft)
					} else {
						t.w.write(rhs)
					}
					t.w.newline()
					return
				}
			}
		}
		// super(args...) → s.BaseClass = *NewBaseClass(args...)
		if exprStmt.Expression.Kind == ast.KindCallExpression {
			call := exprStmt.Expression.AsCallExpression()
			if call.Expression.Kind == ast.KindSuperKeyword && baseClassName != "" {
				if baseClassName == "Error" {
					// extends Error: super(msg) → s.Message = msg.(string)
					t.w.writef("%s.Message = ", receiverVar)
					if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
						rhs := t.captureExpr(call.Arguments.Nodes[0])
						if strings.Contains(rhs, "jsrt.GetField(") || strings.Contains(rhs, "jsrt.Index(") ||
							strings.Contains(rhs, "jsrt.Obj(") || strings.Contains(rhs, ".Unwrap()") {
							t.w.writef("%s.(string)", rhs)
						} else {
							t.w.write(rhs)
						}
					} else {
						t.w.write(`""`)
					}
					t.w.newline()
				} else {
					t.w.writef("%s.%s = *New%s(", receiverVar, baseClassName, baseClassName)
					t.emitCallArgs(call.Arguments)
					t.w.write(")")
					t.w.newline()
				}
				return
			}
		}
	}
	// Fallback
	t.emitStatement(node)
}

// isPrivateOrProtected checks if a node has private or protected modifier.
func isPrivateOrProtected(node *ast.Node) bool {
	return ast.HasSyntacticModifier(node, ast.ModifierFlagsNonPublicAccessibilityModifier)
}

// isTypeParam checks if the given type name is a currently in-scope type parameter.
func (t *Transpiler) isTypeParam(name string) bool {
	if t.tm.typeParams != nil {
		return t.tm.typeParams[name]
	}
	return false
}

// isPrimitiveOrCollectionType returns true for types that can be used as Promise type params.
func isPrimitiveOrCollectionType(t string) bool {
	switch t {
	case "string", "float64", "bool", "int", "any", "error":
		return true
	}
	return strings.HasPrefix(t, "map[") || strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "*")
}

// emitClassMethod emits a class method as a Go method on the struct.
func (t *Transpiler) emitClassMethod(node *ast.Node, className string, typeParamSuffix string) {
	name := node.Name()
	if name == nil {
		return
	}
	// Skip overload signatures (declarations without body) — only emit the implementation
	body := node.Body()
	if body == nil && !ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) {
		return
	}
	rawName := nodeText(name)
	methodName := goExportedName(rawName)
	if isPrivateOrProtected(node) || ast.IsPrivateIdentifier(name) {
		methodName = goVarName(toCamelCase(rawName))
	}
	receiverVar := toCamelCase(string([]rune(className)[0:1]))

	t.w.writef("func (%s *%s%s) %s(", receiverVar, className, typeParamSuffix, methodName)
	savedPtrStringVars := t.goPtrStringVars
	t.goPtrStringVars = nil
	t.emitParameterList(node)
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)
	if isAsync && strings.HasPrefix(retType, "*promise.Promise[") {
		innerType := retType[len("*promise.Promise[") : len(retType)-1]
		switch innerType {
		case "string", "float64", "bool", "int", "any", "[]any", "[]string", "[]float64":
		default:
			retType = "*promise.Promise[any]"
		}
	}
	if retType != "" {
		t.w.writef(" %s", retType)
	}

	if body != nil {
		savedReceiver := t.thisReceiver
		savedRetType := t.currentRetType
		t.thisReceiver = receiverVar
		t.currentRetType = retType
		t.needsDefaultReturn = retType != ""
		t.emitBlock(body)
		t.thisReceiver = savedReceiver
		t.currentRetType = savedRetType
	} else if ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) {
		// Abstract method — emit panic stub so subclasses must override
		t.w.openBlock()
		t.w.writeln(`panic("abstract method not implemented")`)
		t.w.closeBlock()
	}
	t.goPtrStringVars = savedPtrStringVars
	t.w.newline()
}

// emitGetterMethod emits a class getter as a Go method: get foo() → func (r *Class) Foo() RetType.
func (t *Transpiler) emitGetterMethod(node *ast.Node, className string, typeParamSuffix string) {
	name := node.Name()
	if name == nil {
		return
	}
	rawName := nodeText(name)
	if rawName == "" {
		// Computed property name — skip
		return
	}
	methodName := goExportedName(rawName)
	receiverVar := toCamelCase(string([]rune(className)[0:1]))

	t.w.writef("func (%s *%s%s) %s()", receiverVar, className, typeParamSuffix, methodName)

	// For getters, resolve the return type from the getter's type (not call signatures)
	retType := t.getFuncReturnType(node)
	if retType == "" && t.ck != nil {
		getterType := t.ck.GetTypeAtLocation(node)
		if getterType != nil {
			retType = t.tm.goReturnType(getterType)
		}
	}
	if retType != "" {
		t.w.writef(" %s", retType)
	}

	body := node.Body()
	if body != nil {
		savedReceiver := t.thisReceiver
		savedRetType := t.currentRetType
		t.thisReceiver = receiverVar
		t.currentRetType = retType
		t.emitBlock(body)
		t.thisReceiver = savedReceiver
		t.currentRetType = savedRetType
	} else {
		t.w.writeln(" {}")
	}
	t.w.newline()
}

// emitSetterMethod emits a class setter as a Go method: set foo(v) → func (r *Class) SetFoo(v ParamType).
func (t *Transpiler) emitSetterMethod(node *ast.Node, className string, typeParamSuffix string) {
	name := node.Name()
	if name == nil {
		return
	}
	rawName := nodeText(name)
	if rawName == "" {
		return
	}
	methodName := "Set" + goExportedName(rawName)
	receiverVar := toCamelCase(string([]rune(className)[0:1]))

	t.w.writef("func (%s *%s%s) %s(", receiverVar, className, typeParamSuffix, methodName)
	t.emitParameterList(node)
	t.w.write(")")

	body := node.Body()
	if body != nil {
		savedReceiver := t.thisReceiver
		t.thisReceiver = receiverVar
		t.emitBlock(body)
		t.thisReceiver = savedReceiver
	} else {
		t.w.writeln(" {}")
	}
	t.w.newline()
}

// emitStaticField emits a static class field as a package-level variable.
func (t *Transpiler) emitStaticField(node *ast.Node, className string) {
	name := node.Name()
	if name == nil {
		return
	}
	propName := nodeText(name)
	goFieldName := goExportedName(propName)

	goType := "any"
	if t.ck != nil {
		fieldType := t.ck.GetTypeAtLocation(node)
		if fieldType != nil {
			goType = t.tm.goType(fieldType)
		}
	}
	if goType == "" {
		goType = "any"
	}

	// Check for initializer
	prop := node.AsPropertyDeclaration()
	if prop.Initializer != nil {
		t.w.writef("var %s_%s %s = ", className, goFieldName, goType)
		t.emitExpr(prop.Initializer)
		t.w.newline()
	} else {
		t.w.writelnf("var %s_%s %s", className, goFieldName, goType)
	}
	t.w.newline()
}

// emitStaticMethod emits a static class method as a package-level function.
func (t *Transpiler) emitStaticMethod(node *ast.Node, className string) {
	name := node.Name()
	if name == nil {
		return
	}
	rawName := nodeText(name)
	methodName := goExportedName(rawName)

	t.w.writef("func %s_%s(", className, methodName)
	t.emitParameterList(node)
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	if retType != "" {
		t.w.writef(" %s", retType)
	}

	body := node.Body()
	if body != nil {
		savedReceiver := t.thisReceiver
		savedRetType := t.currentRetType
		t.thisReceiver = "" // no receiver for static methods
		t.currentRetType = retType
		t.emitBlock(body)
		t.thisReceiver = savedReceiver
		t.currentRetType = savedRetType
	} else {
		t.w.writeln(" {}")
	}
	t.w.newline()
}

// isRecordLikeType checks if a type node references Record, Map, or similar map-like patterns.
// Used to detect type aliases that should be treated as map[string]any even when
// the checker can't fully resolve complex generic types in multi-file contexts.
func (t *Transpiler) isRecordLikeType(node *ast.Node) bool {
	if node == nil {
		return false
	}
	// Check if the type references "Record" directly or transitively
	if node.Kind == ast.KindTypeReference {
		name := node.Name()
		if name != nil && name.Kind == ast.KindIdentifier {
			text := name.AsIdentifier().Text
			if text == "Record" {
				return true
			}
			// Check if the referenced type is itself a Record-like alias
			if t.tm.typeAliases != nil {
				if underlying, ok := t.tm.typeAliases[goTypeName(text)]; ok {
					return strings.HasPrefix(underlying, "map[")
				}
			}
		}
	}
	// Check type arguments recursively (e.g., SimplifyBodyData<Record<string, T>>)
	// Only TypeReference nodes have type arguments
	if node.Kind == ast.KindTypeReference {
		typeArgs := node.TypeArguments()
		for _, arg := range typeArgs {
			if t.isRecordLikeType(arg) {
				return true
			}
		}
	}
	return false
}

// discriminatedVariant represents one member of a discriminated union.
type discriminatedVariant struct {
	Name string        // Go type name (e.g., "Circle")
	Type *checker.Type // Checker type
}

// getDiscriminatedUnionVariants checks if a union is a discriminated union
// (all members are named object types with a common string literal discriminant field).
// Returns the variants, or nil if not a discriminated union.
func (t *Transpiler) getDiscriminatedUnionVariants(union *checker.UnionType) []discriminatedVariant {
	types := union.Types()
	if len(types) < 2 {
		return nil
	}

	var variants []discriminatedVariant
	for _, ut := range types {
		// Skip null/undefined
		if ut.Flags()&checker.TypeFlagsNullable != 0 {
			continue
		}
		// Must be an object type with a symbol (named type)
		if ut.Flags()&checker.TypeFlagsObject == 0 {
			return nil
		}
		sym := ut.Symbol()
		if sym == nil || sym.Name == "" || strings.HasPrefix(sym.Name, "__") {
			return nil
		}
		variants = append(variants, discriminatedVariant{
			Name: goTypeName(sym.Name),
			Type: ut,
		})
	}

	if len(variants) < 2 {
		return nil
	}

	// Check that all variants have a common string-literal field (discriminant)
	// For simplicity, check for a "kind" or "type" field
	hasCommonField := false
	for _, fieldName := range discriminantFieldNames {
		allHave := true
		for _, v := range variants {
			props := t.ck.GetPropertiesOfType(v.Type)
			found := false
			for _, p := range props {
				if p.Name == fieldName {
					found = true
					break
				}
			}
			if !found {
				allHave = false
				break
			}
		}
		if allHave {
			hasCommonField = true
			break
		}
	}

	if !hasCommonField {
		return nil
	}

	return variants
}

// emitDiscriminatedUnion generates a Go interface with a marker method
// for a discriminated union type. Each variant struct gets the marker method.
func (t *Transpiler) emitDiscriminatedUnion(name string, variants []discriminatedVariant) {
	// Find common fields across all variants for interface methods
	var commonFields []string
	if len(variants) > 0 {
		firstProps := t.ck.GetPropertiesOfType(variants[0].Type)
		for _, p := range firstProps {
			allHave := true
			for _, v := range variants[1:] {
				found := false
				for _, vp := range t.ck.GetPropertiesOfType(v.Type) {
					if vp.Name == p.Name {
						found = true
						break
					}
				}
				if !allHave || !found {
					allHave = false
					break
				}
			}
			if allHave {
				commonFields = append(commonFields, p.Name)
			}
		}
	}

	// Generate the interface with getter methods for common fields
	markerMethod := fmt.Sprintf("is%s", name)
	t.w.writef("type %s interface", name)
	t.w.openBlock()
	t.w.writelnf("%s()", markerMethod)
	for _, field := range commonFields {
		goField := goExportedName(field)
		// Determine field type unified across ALL variants (fall back to any if they differ)
		fieldType := ""
		for _, v := range variants {
			vType := "any"
			for _, p := range t.ck.GetPropertiesOfType(v.Type) {
				if p.Name == field {
					pt := t.ck.GetTypeOfSymbol(p)
					if pt != nil {
						vType = t.tm.goType(pt)
					}
					break
				}
			}
			if fieldType == "" {
				fieldType = vType
			} else if fieldType != vType {
				fieldType = "any"
				break
			}
		}
		if fieldType == "" {
			fieldType = "any"
		}
		t.w.writelnf("Get%s() %s", goField, fieldType)
	}
	t.w.closeBlock()
	t.w.newline()

	// Generate marker method and getter methods on each variant (value receivers for interface compatibility)
	for _, v := range variants {
		receiverVar := toCamelCase(string([]rune(v.Name)[0:1]))
		t.w.writef("func (%s %s) %s() {}", receiverVar, v.Name, markerMethod)
		t.w.newline()
		for _, field := range commonFields {
			goField := goExportedName(field)
			fieldType := "any"
			props := t.ck.GetPropertiesOfType(v.Type)
			for _, p := range props {
				if p.Name == field {
					pt := t.ck.GetTypeOfSymbol(p)
					if pt != nil {
						fieldType = t.tm.goType(pt)
					}
					break
				}
			}
			if fieldType == "" {
				fieldType = "any"
			}
			t.w.writef("func (%s %s) Get%s() %s { return %s.%s }", receiverVar, v.Name, goField, fieldType, receiverVar, goField)
			t.w.newline()
		}
	}
	t.w.newline()
}
