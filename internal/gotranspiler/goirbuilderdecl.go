package gotranspiler

import (
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// --------------------------------------------------------------------
// Common helpers for declaration building
// --------------------------------------------------------------------

// buildTypeParamsAndSuffix extracts type parameters and the receiver suffix in a single pass.
func (b *IRBuilder) buildTypeParamsAndSuffix(node *ast.Node) ([]IRTypeParam, string) {
	typeParams := node.TypeParameterList()
	if typeParams == nil || len(typeParams.Nodes) == 0 {
		return nil, ""
	}

	if b.tm.typeParams == nil {
		b.tm.typeParams = make(map[string]bool)
	}

	var result []IRTypeParam
	var suffixParts []string
	for _, tp := range typeParams.Nodes {
		tpDecl := tp.AsTypeParameter()
		tpName := tp.Name().AsIdentifier().Text
		b.tm.typeParams[tpName] = true

		goName := goExportedName(tpName)
		suffixParts = append(suffixParts, goName)

		constraint := "any"
		if tpDecl.Constraint != nil {
			constraint = b.mapConstraintIR(tpDecl.Constraint)
		}
		result = append(result, IRTypeParam{
			Name:       goName,
			Constraint: constraint,
		})
	}
	return result, "[" + strings.Join(suffixParts, ", ") + "]"
}

// tsUtilityTypes lists TS built-in utility types that have no Go equivalent.
var tsUtilityTypes = map[string]bool{
	"Partial": true, "Required": true, "Readonly": true,
	"Pick": true, "Omit": true, "Record": true,
	"Exclude": true, "Extract": true, "NonNullable": true,
	"ReturnType": true, "Parameters": true, "InstanceType": true,
}

// mapConstraintIR maps a TypeScript type constraint to a Go constraint string.
// Mirrors decl.go's mapConstraint but operates on *IRBuilder instead of *Transpiler.
func (b *IRBuilder) mapConstraintIR(constraint *ast.Node) string {
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
			name := ref.TypeName.AsIdentifier().Text
			// TS utility types (Partial, Required, etc.) have no Go equivalent → any
			if tsUtilityTypes[name] {
				return "any"
			}
			// Generic type reference with type args can't be a Go constraint → any
			if ref.TypeArguments != nil && len(ref.TypeArguments.Nodes) > 0 {
				return "any"
			}
			return b.tm.qualifyTypeName(name)
		}
	}
	if b.ck != nil {
		ct := b.ck.GetTypeAtLocation(constraint)
		if ct != nil {
			goType := b.tm.goType(ct)
			if goType != "" && goType != "any" {
				return goType
			}
		}
	}
	return "any"
}

// resolveFieldGoType returns the Go type string for an AST node via the type checker.
func (b *IRBuilder) resolveFieldGoType(node *ast.Node) string {
	ti := b.getGoType(node)
	if ti.GoStr == "" {
		return "any"
	}
	return ti.GoStr
}

// buildAsyncRetType wraps a return type in Promise if async.
func (b *IRBuilder) buildAsyncRetType(retType string, isAsync bool) GoTypeInfo {
	if !isAsync {
		return goTypeInfoFromString(retType)
	}
	b.addImport("github.com/i2y/ramune/jsrt/promise", "")
	if retType == "" {
		retType = "any"
	}
	return goTypeInfoFromString("*promise.Promise[" + retType + "]")
}

// makeReceiver creates an IRReceiver for a class method.
func makeReceiver(recVar, className, typeParamSuffix string) *IRReceiver {
	return &IRReceiver{
		Name: recVar,
		Type: "*" + className + typeParamSuffix,
	}
}

// withMethodContext saves/restores thisReceiver, currentRetType, inAsyncBody around fn.
func (b *IRBuilder) withMethodContext(recVar string, retType string, isAsync bool, fn func()) {
	savedReceiver := b.thisReceiver
	savedRetType := b.currentRetType
	savedAsync := b.inAsyncBody
	b.thisReceiver = recVar
	b.currentRetType = retType
	b.inAsyncBody = isAsync
	fn()
	b.thisReceiver = savedReceiver
	b.currentRetType = savedRetType
	b.inAsyncBody = savedAsync
}

// receiverVarName returns the receiver variable name for a class (first letter lowercase).
func receiverVarName(className string) string {
	return toCamelCase(string([]rune(className)[0:1]))
}

// buildCallableFuncType builds a func type string from call signatures.
func (b *IRBuilder) buildCallableFuncType(sigs []*checker.Signature) string {
	var paramParts []string
	for _, p := range sigs[0].Parameters() {
		pt := b.ck.GetTypeOfSymbol(p)
		pType := "any"
		if pt != nil {
			pType = b.tm.goType(pt)
			pType = replaceCrossPackageTypes(pType)
		}
		paramParts = append(paramParts, goVarName(p.Name)+" "+pType)
	}

	funcStr := "func(" + strings.Join(paramParts, ", ") + ")"

	retType := b.ck.GetReturnTypeOfSignature(sigs[0])
	if retType != nil {
		goRet := b.tm.goType(retType)
		if goRet != "" {
			goRet = replaceCrossPackageTypes(goRet)
			funcStr += " " + goRet
		}
	}
	return funcStr
}

// --------------------------------------------------------------------
// Enum declaration
// --------------------------------------------------------------------

func (b *IRBuilder) buildEnumDecl(node *ast.Node) []GoDecl {
	enumDecl := node.AsEnumDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	goName := goTypeName(nodeText(name))

	// Single-pass: detect string enum and build members simultaneously
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

	baseType := "int"
	if isStringEnum {
		baseType = "string"
	}

	var members []IREnumMember
	if enumDecl.Members != nil {
		for i, member := range enumDecl.Members.Nodes {
			memberName := member.Name()
			em := member.AsEnumMember()
			if memberName == nil || memberName.Kind != ast.KindIdentifier {
				continue
			}
			goMember := goName + goExportedName(memberName.AsIdentifier().Text)

			var value GoExpr
			if isStringEnum {
				if em.Initializer != nil && em.Initializer.Kind == ast.KindStringLiteral {
					value = irString(fmt.Sprintf("%q", em.Initializer.AsStringLiteral().Text))
				} else {
					value = irString(fmt.Sprintf("%q", memberName.AsIdentifier().Text))
				}
			} else if em.Initializer != nil && em.Initializer.Kind == ast.KindNumericLiteral {
				value = irFloat64(em.Initializer.AsNumericLiteral().Text)
			} else if i > 0 {
				// subsequent iota members
			}
			// value == nil means iota (first) or continuation

			members = append(members, IREnumMember{
				Name:  goMember,
				Value: value,
			})
		}
	}

	return []GoDecl{&IREnumDecl{
		Name:       goName,
		BaseType:   baseType,
		Members:    members,
		IsExported: isExported(node),
	}}
}

// --------------------------------------------------------------------
// Interface declaration
// --------------------------------------------------------------------

func (b *IRBuilder) buildInterfaceDecl(node *ast.Node) []GoDecl {
	iface := node.AsInterfaceDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	goName := goTypeName(nodeText(name))

	// Classify members in single pass
	hasProps := false
	hasMethods := false
	hasCallSig := false
	if iface.Members != nil {
		for _, member := range iface.Members.Nodes {
			switch member.Kind {
			case ast.KindPropertySignature:
				hasProps = true
			case ast.KindMethodSignature:
				hasMethods = true
			case ast.KindCallSignature:
				hasCallSig = true
			}
		}
	}

	if !hasProps && !hasMethods && hasCallSig {
		return b.buildCallableInterface(node, goName)
	}
	if !hasProps && !hasMethods {
		return nil
	}

	if hasProps {
		return b.buildPropertyInterface(node, goName, hasMethods)
	}
	return b.buildMethodInterface(node, goName)
}

func (b *IRBuilder) buildCallableInterface(node *ast.Node, goName string) []GoDecl {
	tsName := nodeText(node.Name())
	if b.samePackageExports != nil && (b.samePackageExports[tsName] || b.samePackageExports[toCamelCase(tsName)]) {
		return nil
	}
	if b.ck == nil {
		return nil
	}
	ifaceType := b.ck.GetTypeAtLocation(node)
	if ifaceType == nil {
		return nil
	}
	sigs := b.ck.GetSignaturesOfType(ifaceType, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return nil
	}

	typeParams, _ := b.buildTypeParamsAndSuffix(node)
	defer func() {
		for _, tp := range typeParams {
			delete(b.tm.typeParams, tp.Name)
		}
	}()

	return []GoDecl{&IRTypeAlias{
		Name:       goName,
		Underlying: b.buildCallableFuncType(sigs),
		TypeParams: typeParams,
		IsExported: isExported(node),
	}}
}

// buildPropertyInterface builds an interface with properties as a Go struct,
// collecting fields and method stubs in a single pass over members.
func (b *IRBuilder) buildPropertyInterface(node *ast.Node, goName string, hasMethods bool) []GoDecl {
	iface := node.AsInterfaceDeclaration()
	typeParams, typeParamSuffix := b.buildTypeParamsAndSuffix(node)
	recVar := receiverVarName(goName)

	var fields []IRStructField
	var decls []GoDecl

	// Single pass: collect fields and method stubs
	if iface.Members != nil {
		for _, member := range iface.Members.Nodes {
			switch member.Kind {
			case ast.KindPropertySignature:
				propName := nodeText(member.Name())
				goType := b.resolveFieldGoType(member)
				fields = append(fields, IRStructField{
					Name:       goExportedName(propName),
					Typ:        goTypeInfoFromString(goType),
					Tag:        fmt.Sprintf(`json:"%s"`, propName),
					IsExported: true,
				})
			case ast.KindMethodSignature:
				if !hasMethods {
					continue
				}
				methodName := goExportedName(nodeText(member.Name()))
				params := b.buildParamList(member)
				retType := b.resolveReturnType(member)
				decls = append(decls, &IRFuncDecl{
					Name:    methodName,
					Params:  params,
					RetType: goTypeInfoFromString(retType),
					Body: []GoStmt{&IRExprStmt{Expr: &IRStdlibCall{
						exprBase: exprBase{},
						Package:  "", Func: "panic",
						Args: []GoExpr{irString(`"not implemented"`)},
					}}},
					Receiver:   makeReceiver(recVar, goName, typeParamSuffix),
					IsExported: true,
				})
			}
		}
	}

	// Prepend struct declaration before method stubs
	structDecl := &IRStructDecl{
		Name:       goName,
		TypeParams: typeParams,
		Fields:     fields,
		IsExported: isExported(node),
	}
	return append([]GoDecl{structDecl}, decls...)
}

func (b *IRBuilder) buildMethodInterface(node *ast.Node, goName string) []GoDecl {
	iface := node.AsInterfaceDeclaration()
	typeParams, _ := b.buildTypeParamsAndSuffix(node)

	var methods []IRMethodSig
	if iface.Members != nil {
		for _, member := range iface.Members.Nodes {
			if member.Kind != ast.KindMethodSignature {
				continue
			}
			methods = append(methods, IRMethodSig{
				Name:       goExportedName(nodeText(member.Name())),
				Params:     b.buildParamList(member),
				RetType:    goTypeInfoFromString(b.resolveReturnType(member)),
				IsExported: true,
			})
		}
	}

	return []GoDecl{&IRInterfaceDecl{
		Name:       goName,
		TypeParams: typeParams,
		Methods:    methods,
		IsExported: isExported(node),
	}}
}

// --------------------------------------------------------------------
// Type alias declaration
// --------------------------------------------------------------------

func (b *IRBuilder) buildTypeAliasDecl(node *ast.Node) []GoDecl {
	ta := node.AsTypeAliasDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	goName := goTypeName(nodeText(name))
	exported := isExported(node)

	// Extract type parameters (generic type alias)
	typeParams, _ := b.buildTypeParamsAndSuffix(node)
	// Clean up type params from mapper after building
	defer func() {
		for _, tp := range typeParams {
			delete(b.tm.typeParams, tp.Name)
		}
	}()

	if ta.Type == nil || b.ck == nil {
		return []GoDecl{&IRTypeAlias{
			Name:       goName,
			Underlying: "any",
			TypeParams: typeParams,
			IsExported: isExported(node),
		}}
	}

	aliasType := b.ck.GetTypeAtLocation(ta.Type)

	// Discriminated union: type Shape = Circle | Square
	if aliasType != nil && aliasType.Flags()&checker.TypeFlagsUnion != 0 {
		variants := b.getDiscriminatedUnionVariants(aliasType.AsUnionType())
		if len(variants) > 0 {
			return b.buildDiscriminatedUnion(goName, variants, isExported(node))
		}
	}

	// Prefix unexported type aliases with file name to avoid cross-file collisions
	if !exported && b.filePrefix != "" {
		prefixed := b.filePrefix + goName
		if b.tm.typeAliasRenames == nil {
			b.tm.typeAliasRenames = make(map[string]string)
		}
		b.tm.typeAliasRenames[goName] = prefixed
		goName = prefixed
	}

	// Callable type (function type alias)
	if aliasType != nil {
		sigs := b.ck.GetSignaturesOfType(aliasType, checker.SignatureKindCall)
		if len(sigs) > 0 && aliasType.Flags()&checker.TypeFlagsObject != 0 {
			return []GoDecl{&IRTypeAlias{
				Name:       goName,
				Underlying: b.buildCallableFuncType(sigs),
				TypeParams: typeParams,
				IsExported: exported,
			}}
		}
	}

	// Simple type alias
	goType := "any"
	if aliasType != nil {
		goType = b.tm.goType(aliasType)
	}
	if goType == "" || goType == goName || strings.HasPrefix(goType, goName+"[") {
		goType = "any"
	}

	return []GoDecl{&IRTypeAlias{
		Name:       goName,
		Underlying: goType,
		TypeParams: typeParams,
		IsExported: exported,
	}}
}

// --------------------------------------------------------------------
// Class declaration
// --------------------------------------------------------------------

func (b *IRBuilder) buildClassDecl(node *ast.Node) []GoDecl {
	classDecl := node.AsClassDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	className := goTypeName(nodeText(name))
	b.classNames[className] = true

	var fields, methods, staticFields, staticMethods, getters, setters []*ast.Node
	var constructor *ast.Node
	var baseClassName string

	// Extends clause
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

	// Single pass: classify members and count method names for overload detection
	methodNameCount := map[string]int{}
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
					if n := member.Name(); n != nil {
						methodNameCount[nodeText(n)]++
					}
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

	overloadedMethods := make(map[string]bool, len(methodNameCount))
	for n, count := range methodNameCount {
		if count > 1 {
			overloadedMethods[n] = true
		}
	}

	// Collect all method-like names (method declarations + constructor arrow assignments)
	methodGoNames := make(map[string]bool, len(methodNameCount))
	for n := range methodNameCount {
		methodGoNames[goExportedName(n)] = true
	}
	if constructor != nil {
		if ctorBody := constructor.Body(); ctorBody != nil {
			block := ctorBody.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					if stmt.Kind == ast.KindExpressionStatement {
						expr := stmt.AsExpressionStatement().Expression
						if expr.Kind == ast.KindBinaryExpression {
							bin := expr.AsBinaryExpression()
							if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindPropertyAccessExpression {
								prop := bin.Left.AsPropertyAccessExpression()
								if prop.Expression.Kind == ast.KindThisKeyword &&
									(bin.Right.Kind == ast.KindArrowFunction || bin.Right.Kind == ast.KindFunctionExpression) {
									methodGoNames[goExportedName(nodeText(prop.Name()))] = true
								}
							}
						}
					}
				}
			}
		}
	}

	// Remove fields whose Go name collides with a method name (method takes priority)
	if len(methodGoNames) > 0 && len(fields) > 0 {
		filtered := fields[:0]
		for _, f := range fields {
			if fn := f.Name(); fn != nil {
				goFieldName := goExportedName(nodeText(fn))
				if methodGoNames[goFieldName] {
					continue
				}
			}
			filtered = append(filtered, f)
		}
		fields = filtered
	}

	// Build private field map (save/restore)
	savedPrivateFields := b.privateFields
	b.privateFields = make(map[string]string)
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
			b.privateFields[tsName] = goVarName(toCamelCase(tsName))
		}
	}

	typeParams, typeParamSuffix := b.buildTypeParamsAndSuffix(node)

	var decls []GoDecl

	// 1. Struct
	structResult := b.buildClassStruct(className, typeParams, baseClassName, fields)
	arrowFieldMethods := structResult.arrowFields
	decls = append(decls, structResult.decl)

	// 2. Constructor
	ctorResult := b.buildConstructorDecl(constructor, className, baseClassName, typeParamSuffix, typeParams)
	decls = append(decls, ctorResult.funcDecl)
	arrowFieldMethods = append(arrowFieldMethods, ctorResult.arrowFields...)

	// 3. Instance methods
	for _, method := range methods {
		if md := b.buildMethodDecl(method, className, typeParamSuffix, overloadedMethods); md != nil {
			decls = append(decls, md)
		}
	}

	// 4. Arrow field methods
	for _, af := range arrowFieldMethods {
		if md := b.buildArrowFieldAsMethodDecl(af, className, typeParamSuffix); md != nil {
			decls = append(decls, md)
		}
	}

	// 5. Getters
	for _, getter := range getters {
		if gd := b.buildGetterDecl(getter, className, typeParamSuffix); gd != nil {
			decls = append(decls, gd)
		}
	}

	// 6. Setters
	for _, setter := range setters {
		if sd := b.buildSetterDecl(setter, className, typeParamSuffix); sd != nil {
			decls = append(decls, sd)
		}
	}

	// 7. Static fields
	for _, field := range staticFields {
		if sf := b.buildStaticFieldDecl(field, className); sf != nil {
			decls = append(decls, sf)
		}
	}

	// 8. Static methods
	for _, method := range staticMethods {
		if sm := b.buildStaticMethodDecl(method, className); sm != nil {
			decls = append(decls, sm)
		}
	}

	b.privateFields = savedPrivateFields
	return decls
}

type classStructResult struct {
	decl        *IRStructDecl
	arrowFields []*ast.Node
}

func (b *IRBuilder) buildClassStruct(className string, typeParams []IRTypeParam, baseClassName string, fields []*ast.Node) classStructResult {
	var embedded []string
	if baseClassName != "" {
		if baseClassName == "Error" {
			b.addImport("github.com/i2y/ramune/jsrt", "")
			embedded = append(embedded, "jsrt.JSError")
		} else {
			embedded = append(embedded, baseClassName)
		}
	}

	var structFields []IRStructField
	var arrowFields []*ast.Node

	for _, field := range fields {
		pd := field.AsPropertyDeclaration()
		if pd.Initializer != nil &&
			(pd.Initializer.Kind == ast.KindArrowFunction || pd.Initializer.Kind == ast.KindFunctionExpression) {
			arrowFields = append(arrowFields, field)
			continue
		}

		fname := field.Name()
		if fname == nil {
			continue
		}
		var propName string
		if ast.IsPrivateIdentifier(fname) {
			propName = strings.TrimPrefix(fname.AsPrivateIdentifier().Text, "#")
		} else {
			propName = nodeText(fname)
		}

		goFieldName := goExportedName(propName)
		isPrivate := isPrivateOrProtected(field) || ast.IsPrivateIdentifier(fname)
		if isPrivate {
			goFieldName = goVarName(toCamelCase(propName))
		}

		structFields = append(structFields, IRStructField{
			Name:       goFieldName,
			Typ:        goTypeInfoFromString(b.resolveFieldGoType(field)),
			IsExported: !isPrivate,
		})
	}

	return classStructResult{
		decl: &IRStructDecl{
			Name:       className,
			TypeParams: typeParams,
			Embedded:   embedded,
			Fields:     structFields,
			IsExported: true,
		},
		arrowFields: arrowFields,
	}
}

type constructorResult struct {
	funcDecl    *IRFuncDecl
	arrowFields []*ast.Node
}

func (b *IRBuilder) buildConstructorDecl(ctorNode *ast.Node, className string, baseClassName string, typeParamSuffix string, typeParams []IRTypeParam) *constructorResult {
	var params []IRParam
	if ctorNode != nil {
		params = b.buildParamList(ctorNode)
	}

	var body []GoStmt
	var arrowFields []*ast.Node

	// s := &ClassName{}
	body = append(body, &IRVarDecl{
		Name:     "s",
		Typ:      goTypeInfoFromString("*" + className + typeParamSuffix),
		Init:     &IRRawExpr{exprBase: exprBase{}, Code: "&" + className + typeParamSuffix + "{}"},
		UseShort: true,
	})

	savedReceiver := b.thisReceiver
	b.thisReceiver = "s"

	if ctorNode != nil {
		ctorBody := ctorNode.Body()
		if ctorBody != nil {
			block := ctorBody.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					result := b.buildConstructorStmt(stmt, "s", baseClassName)
					body = append(body, result.stmts...)
					arrowFields = append(arrowFields, result.arrowFields...)
				}
			}
		}
	}

	b.thisReceiver = savedReceiver

	body = append(body, &IRReturn{Values: []GoExpr{&IRIdent{exprBase: exprBase{}, Name: "s"}}})

	return &constructorResult{
		funcDecl: &IRFuncDecl{
			Name:       "New" + className,
			TypeParams: typeParams,
			Params:     params,
			RetType:    goTypeInfoFromString("*" + className + typeParamSuffix),
			Body:       body,
			IsExported: true,
		},
		arrowFields: arrowFields,
	}
}

type constructorStmtResult struct {
	stmts       []GoStmt
	arrowFields []*ast.Node
}

func (b *IRBuilder) buildConstructorStmt(node *ast.Node, receiverVar string, baseClassName string) constructorStmtResult {
	if node.Kind != ast.KindExpressionStatement {
		stmt := b.BuildStmt(node)
		if stmt == nil {
			return constructorStmtResult{}
		}
		return constructorStmtResult{stmts: []GoStmt{stmt}}
	}

	exprStmt := node.AsExpressionStatement()

	if exprStmt.Expression.Kind == ast.KindBinaryExpression {
		bin := exprStmt.Expression.AsBinaryExpression()
		if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindPropertyAccessExpression {
			prop := bin.Left.AsPropertyAccessExpression()
			if prop.Expression.Kind == ast.KindThisKeyword {
				propName := nodeText(prop.Name())
				goName := goExportedName(propName)
				if pf, ok := b.privateFields[propName]; ok {
					goName = pf
				}

				// Arrow function → deferred as method
				if bin.Right.Kind == ast.KindArrowFunction || bin.Right.Kind == ast.KindFunctionExpression {
					return constructorStmtResult{arrowFields: []*ast.Node{node}}
				}

				target := &IRFieldAccess{
					exprBase: exprBase{},
					Object:   &IRIdent{exprBase: exprBase{}, Name: receiverVar},
					Field:    goName,
				}
				return constructorStmtResult{
					stmts: []GoStmt{&IRAssign{
						Targets: []GoExpr{target},
						Op:      "=",
						Values:  []GoExpr{b.BuildExpr(bin.Right)},
					}},
				}
			}
		}

		if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
			ea := bin.Left.AsElementAccessExpression()
			if ea.Expression.Kind == ast.KindThisKeyword {
				return constructorStmtResult{}
			}
		}
	}

	if exprStmt.Expression.Kind == ast.KindCallExpression {
		call := exprStmt.Expression.AsCallExpression()
		if call.Expression.Kind == ast.KindSuperKeyword && baseClassName != "" {
			args := b.buildArgList(call.Arguments)
			if baseClassName == "Error" {
				var value GoExpr
				if len(args) > 0 {
					value = args[0]
				} else {
					value = irString(`""`)
				}
				return constructorStmtResult{
					stmts: []GoStmt{&IRAssign{
						Targets: []GoExpr{&IRFieldAccess{
							exprBase: exprBase{},
							Object:   &IRIdent{exprBase: exprBase{}, Name: receiverVar},
							Field:    "Message",
						}},
						Op:     "=",
						Values: []GoExpr{value},
					}},
				}
			}
			return constructorStmtResult{
				stmts: []GoStmt{&IRAssign{
					Targets: []GoExpr{&IRFieldAccess{
						exprBase: exprBase{},
						Object:   &IRIdent{exprBase: exprBase{}, Name: receiverVar},
						Field:    baseClassName,
					}},
					Op: "=",
					Values: []GoExpr{&IRDeref{
						exprBase: exprBase{},
						Expr: &IRCall{
							exprBase: exprBase{},
							Func:     &IRIdent{exprBase: exprBase{}, Name: "New" + baseClassName},
							Args:     args,
						},
					}},
				}},
			}
		}

		// Object.assign(this, obj) → skip
		if call.Expression.Kind == ast.KindPropertyAccessExpression {
			prop := call.Expression.AsPropertyAccessExpression()
			if prop.Expression.Kind == ast.KindIdentifier && prop.Expression.AsIdentifier().Text == "Object" &&
				nodeText(prop.Name()) == "assign" {
				return constructorStmtResult{}
			}
		}
	}

	stmt := b.BuildStmt(node)
	if stmt == nil {
		return constructorStmtResult{}
	}
	return constructorStmtResult{stmts: []GoStmt{stmt}}
}

func (b *IRBuilder) buildMethodDecl(node *ast.Node, className string, typeParamSuffix string, overloadedMethods map[string]bool) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}

	body := node.Body()
	if body == nil && !ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) {
		return nil
	}

	rawName := nodeText(name)
	methodName := goExportedName(rawName)
	isPrivate := isPrivateOrProtected(node) || ast.IsPrivateIdentifier(name)
	if isPrivate {
		methodName = goVarName(toCamelCase(rawName))
	}
	recVar := receiverVarName(className)

	// Save and clear method-level type params
	savedTypeParams := b.tm.typeParams
	methodTypeParams := node.TypeParameterList()
	if methodTypeParams != nil && len(methodTypeParams.Nodes) > 0 {
		newParams := make(map[string]bool, len(savedTypeParams))
		for k, v := range savedTypeParams {
			newParams[k] = v
		}
		for _, tp := range methodTypeParams.Nodes {
			delete(newParams, tp.Name().AsIdentifier().Text)
		}
		b.tm.typeParams = newParams
	}

	params := b.buildParamList(node)
	retType := b.resolveReturnType(node)
	if overloadedMethods[rawName] {
		retType = "any"
	}

	// Sanitize Promise return type
	if strings.HasPrefix(retType, "*promise.Promise[") {
		innerType := retType[len("*promise.Promise[") : len(retType)-1]
		if !isPrimitiveOrCollectionType(innerType) {
			retType = "*promise.Promise[any]"
		}
	}

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	var bodyStmts []GoStmt
	b.withMethodContext(recVar, retType, isAsync, func() {
		if body != nil {
			bodyStmts = b.buildStmtList(body)
		} else if ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) {
			bodyStmts = []GoStmt{&IRExprStmt{Expr: &IRStdlibCall{
				exprBase: exprBase{},
				Package:  "", Func: "panic",
				Args: []GoExpr{irString(`"abstract method not implemented"`)},
			}}}
		}
	})

	b.tm.typeParams = savedTypeParams

	return &IRFuncDecl{
		Name:       methodName,
		Params:     params,
		RetType:    b.buildAsyncRetType(retType, isAsync),
		Body:       bodyStmts,
		Receiver:   makeReceiver(recVar, className, typeParamSuffix),
		IsAsync:    isAsync,
		IsExported: !isPrivate,
	}
}

func (b *IRBuilder) buildArrowFieldAsMethodDecl(node *ast.Node, className string, typeParamSuffix string) *IRFuncDecl {
	var propName string
	var initNode *ast.Node

	switch node.Kind {
	case ast.KindPropertyDeclaration:
		pd := node.AsPropertyDeclaration()
		fname := node.Name()
		if fname == nil || pd.Initializer == nil {
			return nil
		}
		if ast.IsPrivateIdentifier(fname) {
			propName = strings.TrimPrefix(fname.AsPrivateIdentifier().Text, "#")
		} else {
			propName = nodeText(fname)
		}
		initNode = pd.Initializer
	case ast.KindExpressionStatement:
		exprStmt := node.AsExpressionStatement()
		if exprStmt.Expression.Kind != ast.KindBinaryExpression {
			return nil
		}
		bin := exprStmt.Expression.AsBinaryExpression()
		if bin.Left.Kind != ast.KindPropertyAccessExpression {
			return nil
		}
		propName = nodeText(bin.Left.AsPropertyAccessExpression().Name())
		initNode = bin.Right
	default:
		return nil
	}

	methodName := goExportedName(propName)
	if node.Name() != nil && (isPrivateOrProtected(node) || ast.IsPrivateIdentifier(node.Name())) {
		methodName = goVarName(toCamelCase(propName))
	}

	recVar := receiverVarName(className)
	params := b.buildParamList(initNode)
	retType := b.resolveReturnType(initNode)
	isAsync := ast.HasSyntacticModifier(initNode, ast.ModifierFlagsAsync)

	var bodyStmts []GoStmt
	b.withMethodContext(recVar, retType, isAsync, func() {
		body := initNode.Body()
		if initNode.Kind == ast.KindArrowFunction {
			body = initNode.AsArrowFunction().Body
		}
		if body != nil && body.Kind == ast.KindBlock {
			bodyStmts = b.buildStmtList(body)
		} else if body != nil {
			bodyStmts = []GoStmt{&IRReturn{Values: []GoExpr{b.BuildExpr(body)}}}
		}
	})

	return &IRFuncDecl{
		Name:       methodName,
		Params:     params,
		RetType:    b.buildAsyncRetType(retType, isAsync),
		Body:       bodyStmts,
		Receiver:   makeReceiver(recVar, className, typeParamSuffix),
		IsAsync:    isAsync,
		IsExported: true,
	}
}

func (b *IRBuilder) buildGetterDecl(node *ast.Node, className string, typeParamSuffix string) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	rawName := nodeText(name)
	if rawName == "" {
		return nil
	}
	recVar := receiverVarName(className)

	retType := b.resolveReturnType(node)
	if retType == "" && b.ck != nil {
		getterType := b.ck.GetTypeAtLocation(node)
		if getterType != nil {
			retType = b.tm.goReturnType(getterType)
		}
	}

	var bodyStmts []GoStmt
	b.withMethodContext(recVar, retType, false, func() {
		if body := node.Body(); body != nil {
			bodyStmts = b.buildStmtList(body)
		}
	})

	return &IRFuncDecl{
		Name:       goExportedName(rawName),
		RetType:    goTypeInfoFromString(retType),
		Body:       bodyStmts,
		Receiver:   makeReceiver(recVar, className, typeParamSuffix),
		IsExported: true,
	}
}

func (b *IRBuilder) buildSetterDecl(node *ast.Node, className string, typeParamSuffix string) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	rawName := nodeText(name)
	if rawName == "" {
		return nil
	}
	recVar := receiverVarName(className)

	var bodyStmts []GoStmt
	b.withMethodContext(recVar, "", false, func() {
		if body := node.Body(); body != nil {
			bodyStmts = b.buildStmtList(body)
		}
	})

	return &IRFuncDecl{
		Name:       "Set" + goExportedName(rawName),
		Params:     b.buildParamList(node),
		Body:       bodyStmts,
		Receiver:   makeReceiver(recVar, className, typeParamSuffix),
		IsExported: true,
	}
}

func (b *IRBuilder) buildStaticFieldDecl(node *ast.Node, className string) GoDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	propName := nodeText(name)

	pd := node.AsPropertyDeclaration()
	var init GoExpr
	if pd.Initializer != nil {
		init = b.BuildExpr(pd.Initializer)
	}

	return &IRStmtDecl{Stmt: &IRVarDecl{
		Name: className + "_" + goExportedName(propName),
		Typ:  goTypeInfoFromString(b.resolveFieldGoType(node)),
		Init: init,
	}}
}

func (b *IRBuilder) buildStaticMethodDecl(node *ast.Node, className string) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}

	params := b.buildParamList(node)
	retType := b.resolveReturnType(node)
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	var bodyStmts []GoStmt
	b.withMethodContext("", retType, isAsync, func() {
		if body := node.Body(); body != nil {
			bodyStmts = b.buildStmtList(body)
		}
	})

	return &IRFuncDecl{
		Name:       className + "_" + goExportedName(nodeText(name)),
		Params:     params,
		RetType:    b.buildAsyncRetType(retType, isAsync),
		Body:       bodyStmts,
		IsAsync:    isAsync,
		IsExported: true,
	}
}

// --------------------------------------------------------------------
// Export declarations
// --------------------------------------------------------------------

// buildExportDecl handles export declarations.
// Named exports (export { X }) are handled by modifier flags on the declaration itself.
// Re-exports (export { X } from './mod') are not yet supported.
func (b *IRBuilder) buildExportDecl(_ *ast.Node) []GoDecl {
	return nil
}

func (b *IRBuilder) buildExportAssignment(node *ast.Node) []GoDecl {
	ea := node.AsExportAssignment()
	if ea.Expression == nil {
		return nil
	}

	if ea.Expression.Kind == ast.KindFunctionExpression || ea.Expression.Kind == ast.KindArrowFunction {
		params := b.buildParamList(ea.Expression)
		retType := b.resolveReturnType(ea.Expression)
		isAsync := ast.HasSyntacticModifier(ea.Expression, ast.ModifierFlagsAsync)

		var bodyStmts []GoStmt
		b.withMethodContext("", retType, isAsync, func() {
			body := ea.Expression.Body()
			if ea.Expression.Kind == ast.KindArrowFunction {
				body = ea.Expression.AsArrowFunction().Body
			}
			if body != nil && body.Kind == ast.KindBlock {
				bodyStmts = b.buildStmtList(body)
			} else if body != nil {
				bodyStmts = []GoStmt{&IRReturn{Values: []GoExpr{b.BuildExpr(body)}}}
			}
		})

		return []GoDecl{&IRFuncDecl{
			Name:       "Default",
			Params:     params,
			RetType:    b.buildAsyncRetType(retType, isAsync),
			Body:       bodyStmts,
			IsAsync:    isAsync,
			IsExported: true,
		}}
	}

	return []GoDecl{&IRStmtDecl{Stmt: &IRVarDecl{
		Name: "Default",
		Typ:  goTypeInfoFromString("any"),
		Init: b.BuildExpr(ea.Expression),
	}}}
}

// --------------------------------------------------------------------
// Discriminated union
// --------------------------------------------------------------------

// getDiscriminatedUnionVariants checks if a union is a discriminated union
// (all members are named object types with a common discriminant field).
func (b *IRBuilder) getDiscriminatedUnionVariants(union *checker.UnionType) []discriminatedVariant {
	return detectDiscriminatedUnion(b.ck, union)
}

// commonFieldInfo holds a shared field across union variants.
type commonFieldInfo struct {
	name   string // TS field name
	goType string // unified Go type
}

// buildDiscriminatedUnion generates an IRInterfaceDecl with marker + getters,
// plus IRFuncDecl methods on each variant struct.
func (b *IRBuilder) buildDiscriminatedUnion(name string, variants []discriminatedVariant, exported bool) []GoDecl {
	markerMethod := "is" + name

	// Find common fields across all variants
	var commonFields []commonFieldInfo
	if len(variants) > 0 {
		firstProps := b.ck.GetPropertiesOfType(variants[0].Type)
		for _, p := range firstProps {
			allHave := true
			for _, v := range variants[1:] {
				if b.ck.GetPropertyOfType(v.Type, p.Name) == nil {
					allHave = false
					break
				}
			}
			if allHave {
				commonFields = append(commonFields, commonFieldInfo{name: p.Name})
			}
		}
	}

	// Determine unified type for each common field
	for i, field := range commonFields {
		fieldType := ""
		for _, v := range variants {
			vType := "any"
			if p := b.ck.GetPropertyOfType(v.Type, field.name); p != nil {
				if pt := b.ck.GetTypeOfSymbol(p); pt != nil {
					vType = b.tm.goType(pt)
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
		commonFields[i].goType = fieldType
	}

	// Build interface: marker + getter methods
	methods := []IRMethodSig{{Name: markerMethod}}
	for _, field := range commonFields {
		methods = append(methods, IRMethodSig{
			Name:    "Get" + goExportedName(field.name),
			RetType: goTypeInfoFromString(field.goType),
		})
	}

	var decls []GoDecl
	decls = append(decls, &IRInterfaceDecl{
		Name:       name,
		Methods:    methods,
		IsExported: exported,
	})

	// Generate marker + getter methods on each variant
	for _, v := range variants {
		recVar := receiverVarName(v.Name)

		// Marker method
		decls = append(decls, &IRFuncDecl{
			Name:     markerMethod,
			Receiver: &IRReceiver{Name: recVar, Type: v.Name},
		})

		// Getter methods for common fields
		for _, field := range commonFields {
			goField := goExportedName(field.name)
			varFieldType := field.goType
			if p := b.ck.GetPropertyOfType(v.Type, field.name); p != nil {
				if pt := b.ck.GetTypeOfSymbol(p); pt != nil {
					varFieldType = b.tm.goType(pt)
				}
			}
			if varFieldType == "" {
				varFieldType = "any"
			}
			decls = append(decls, &IRFuncDecl{
				Name:    "Get" + goField,
				RetType: goTypeInfoFromString(varFieldType),
				Body: []GoStmt{
					&IRReturn{Values: []GoExpr{
						&IRFieldAccess{
							exprBase: exprBase{Typ: goTypeInfoFromString(varFieldType)},
							Object:   &IRIdent{Name: recVar},
							Field:    goField,
						},
					}},
				},
				Receiver: &IRReceiver{Name: recVar, Type: v.Name},
			})
		}
	}

	return decls
}
