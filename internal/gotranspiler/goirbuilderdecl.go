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

// buildTypeParams extracts type parameters from a class/interface/function node.
func (b *IRBuilder) buildTypeParams(node *ast.Node) []IRTypeParam {
	typeParams := node.TypeParameterList()
	if typeParams == nil || len(typeParams.Nodes) == 0 {
		return nil
	}

	if b.tm.typeParams == nil {
		b.tm.typeParams = make(map[string]bool)
	}

	var result []IRTypeParam
	for _, tp := range typeParams.Nodes {
		tpDecl := tp.AsTypeParameter()
		tpName := tp.Name().AsIdentifier().Text
		b.tm.typeParams[tpName] = true

		constraint := "any"
		if tpDecl.Constraint != nil {
			constraint = b.mapConstraintIR(tpDecl.Constraint)
		}
		result = append(result, IRTypeParam{
			Name:       goExportedName(tpName),
			Constraint: constraint,
		})
	}
	return result
}

// buildTypeParamSuffix returns the type parameter suffix for receiver types (e.g., "[T, U]").
func (b *IRBuilder) buildTypeParamSuffix(node *ast.Node) string {
	typeParams := node.TypeParameterList()
	if typeParams == nil || len(typeParams.Nodes) == 0 {
		return ""
	}
	var parts []string
	for _, tp := range typeParams.Nodes {
		parts = append(parts, goExportedName(tp.Name().AsIdentifier().Text))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// receiverVarName returns the receiver variable name for a class (first letter lowercase).
func receiverVarName(className string) string {
	return toCamelCase(string([]rune(className)[0:1]))
}

// mapConstraintIR maps a TypeScript type constraint to a Go constraint string.
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
			return b.tm.qualifyTypeName(ref.TypeName.AsIdentifier().Text)
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

// resolveFieldType returns the Go type string for a class/interface field node.
func (b *IRBuilder) resolveFieldType(node *ast.Node) string {
	if b.ck == nil {
		return "any"
	}
	fieldType := b.ck.GetTypeAtLocation(node)
	if fieldType == nil {
		return "any"
	}
	goType := b.tm.goType(fieldType)
	if goType == "" {
		return "any"
	}
	return goType
}

// --------------------------------------------------------------------
// Enum declaration
// --------------------------------------------------------------------

// buildEnumDecl builds an enum declaration as IREnumDecl.
func (b *IRBuilder) buildEnumDecl(node *ast.Node) []GoDecl {
	enumDecl := node.AsEnumDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	goName := goTypeName(nodeText(name))

	// Determine string vs numeric enum
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
			} else {
				if em.Initializer != nil && em.Initializer.Kind == ast.KindNumericLiteral {
					value = irFloat64(em.Initializer.AsNumericLiteral().Text)
				} else if i == 0 {
					value = nil // iota
				} else {
					value = nil
				}
			}

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

// buildInterfaceDecl builds an interface declaration as Go interface, struct, or func type.
func (b *IRBuilder) buildInterfaceDecl(node *ast.Node) []GoDecl {
	iface := node.AsInterfaceDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	goName := goTypeName(nodeText(name))

	// Classify members
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

	// Callable interface with no props/methods → func type alias
	if !hasProps && !hasMethods && hasCallSig {
		return b.buildCallableInterface(node, goName)
	}

	// Empty interface with no call sig → skip
	if !hasProps && !hasMethods {
		return nil
	}

	typeParams := b.buildTypeParams(node)

	if hasProps {
		return b.buildPropertyInterface(node, goName, typeParams, hasMethods)
	}

	// Pure method interface → Go interface
	return b.buildMethodInterface(node, goName, typeParams)
}

// buildCallableInterface builds a callable interface as a func type alias.
func (b *IRBuilder) buildCallableInterface(node *ast.Node, goName string) []GoDecl {
	// Skip if same-name function exists
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

	// Build func type string
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

	return []GoDecl{&IRTypeAlias{
		Name:       goName,
		Underlying: funcStr,
		IsExported: isExported(node),
	}}
}

// buildPropertyInterface builds an interface with properties as a Go struct.
func (b *IRBuilder) buildPropertyInterface(node *ast.Node, goName string, typeParams []IRTypeParam, hasMethods bool) []GoDecl {
	iface := node.AsInterfaceDeclaration()

	var fields []IRStructField
	if iface.Members != nil {
		for _, member := range iface.Members.Nodes {
			if member.Kind == ast.KindPropertySignature {
				propName := nodeText(member.Name())
				goType := b.resolveFieldType(member)

				fields = append(fields, IRStructField{
					Name:       goExportedName(propName),
					Typ:        goTypeInfoFromString(goType),
					Tag:        fmt.Sprintf(`json:"%s"`, propName),
					IsExported: true,
				})
			}
		}
	}

	var decls []GoDecl
	decls = append(decls, &IRStructDecl{
		Name:       goName,
		TypeParams: typeParams,
		Fields:     fields,
		IsExported: isExported(node),
	})

	// If also has methods, emit method stubs on the struct
	if hasMethods && iface.Members != nil {
		typeParamSuffix := b.buildTypeParamSuffix(node)
		recVar := receiverVarName(goName)
		for _, member := range iface.Members.Nodes {
			if member.Kind != ast.KindMethodSignature {
				continue
			}
			methodName := goExportedName(nodeText(member.Name()))
			params := b.buildMethodSigParams(member)
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
				Receiver: &IRReceiver{
					Name: recVar,
					Type: "*" + goName + typeParamSuffix,
				},
				IsExported: true,
			})
		}
	}

	return decls
}

// buildMethodInterface builds a pure-method interface as a Go interface.
func (b *IRBuilder) buildMethodInterface(node *ast.Node, goName string, typeParams []IRTypeParam) []GoDecl {
	iface := node.AsInterfaceDeclaration()

	var methods []IRMethodSig
	if iface.Members != nil {
		for _, member := range iface.Members.Nodes {
			if member.Kind != ast.KindMethodSignature {
				continue
			}
			methodName := goExportedName(nodeText(member.Name()))
			params := b.buildMethodSigParams(member)
			retType := b.resolveReturnType(member)

			methods = append(methods, IRMethodSig{
				Name:       methodName,
				Params:     params,
				RetType:    goTypeInfoFromString(retType),
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

// buildMethodSigParams builds parameter list from a method signature node.
func (b *IRBuilder) buildMethodSigParams(node *ast.Node) []IRParam {
	params := node.Parameters()
	if params == nil {
		return nil
	}
	var result []IRParam
	for _, param := range params {
		pName := nodeText(param.Name())
		goType := b.resolveFieldType(param)
		result = append(result, IRParam{
			Name: goParamName(pName),
			Typ:  goTypeInfoFromString(goType),
		})
	}
	return result
}

// --------------------------------------------------------------------
// Type alias declaration
// --------------------------------------------------------------------

// buildTypeAliasDecl builds a type alias declaration.
func (b *IRBuilder) buildTypeAliasDecl(node *ast.Node) []GoDecl {
	ta := node.AsTypeAliasDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	goName := goTypeName(nodeText(name))

	if ta.Type == nil || b.ck == nil {
		return []GoDecl{&IRTypeAlias{
			Name:       goName,
			Underlying: "any",
			IsExported: isExported(node),
		}}
	}

	aliasType := b.ck.GetTypeAtLocation(ta.Type)

	// Check for callable type (function type alias)
	if aliasType != nil {
		sigs := b.ck.GetSignaturesOfType(aliasType, checker.SignatureKindCall)
		if len(sigs) > 0 && aliasType.Flags()&checker.TypeFlagsObject != 0 {
			// Build func type
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
			return []GoDecl{&IRTypeAlias{
				Name:       goName,
				Underlying: funcStr,
				IsExported: isExported(node),
			}}
		}
	}

	// Simple type alias
	goType := "any"
	if aliasType != nil {
		goType = b.tm.goType(aliasType)
	}
	if goType == "" {
		goType = "any"
	}

	return []GoDecl{&IRTypeAlias{
		Name:       goName,
		Underlying: goType,
		IsExported: isExported(node),
	}}
}

// --------------------------------------------------------------------
// Class declaration
// --------------------------------------------------------------------

// buildClassDecl builds a class declaration as struct + constructor + methods.
func (b *IRBuilder) buildClassDecl(node *ast.Node) []GoDecl {
	classDecl := node.AsClassDeclaration()
	name := node.Name()
	if name == nil {
		return nil
	}
	className := goTypeName(nodeText(name))
	b.classNames[className] = true

	// Categorize members
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

	// Track overloaded methods
	methodNameCount := map[string]int{}
	overloadedMethods := map[string]bool{}
	if classDecl.Members != nil {
		for _, member := range classDecl.Members.Nodes {
			if member.Kind == ast.KindMethodDeclaration {
				if n := member.Name(); n != nil {
					methodNameCount[nodeText(n)]++
				}
			}
		}
		for name, count := range methodNameCount {
			if count > 1 {
				overloadedMethods[name] = true
			}
		}
	}

	// Classify members
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

	typeParams := b.buildTypeParams(node)
	typeParamSuffix := b.buildTypeParamSuffix(node)

	var decls []GoDecl

	// 1. Build struct
	structDecl := b.buildClassStruct(className, typeParams, baseClassName, fields)
	arrowFieldMethods := structDecl.arrowFields
	decls = append(decls, structDecl.decl)

	// 2. Build constructor
	ctorDecl := b.buildConstructorDecl(constructor, className, baseClassName, node, typeParams)
	if ctorDecl != nil {
		decls = append(decls, ctorDecl.funcDecl)
		// Arrow fields collected from constructor body
		arrowFieldMethods = append(arrowFieldMethods, ctorDecl.arrowFields...)
	}

	// 3. Build instance methods
	for _, method := range methods {
		if md := b.buildMethodDecl(method, className, typeParamSuffix, overloadedMethods); md != nil {
			decls = append(decls, md)
		}
	}

	// 4. Build arrow field methods (from class body)
	for _, af := range arrowFieldMethods {
		if md := b.buildArrowFieldAsMethodDecl(af, className, typeParamSuffix); md != nil {
			decls = append(decls, md)
		}
	}

	// 5. Build getters
	for _, getter := range getters {
		if gd := b.buildGetterDecl(getter, className, typeParamSuffix); gd != nil {
			decls = append(decls, gd)
		}
	}

	// 6. Build setters
	for _, setter := range setters {
		if sd := b.buildSetterDecl(setter, className, typeParamSuffix); sd != nil {
			decls = append(decls, sd)
		}
	}

	// 7. Build static fields
	for _, field := range staticFields {
		if sf := b.buildStaticFieldDecl(field, className); sf != nil {
			decls = append(decls, sf)
		}
	}

	// 8. Build static methods
	for _, method := range staticMethods {
		if sm := b.buildStaticMethodDecl(method, className); sm != nil {
			decls = append(decls, sm)
		}
	}

	b.privateFields = savedPrivateFields
	return decls
}

// classStructResult holds the struct declaration and any arrow function fields to defer.
type classStructResult struct {
	decl        *IRStructDecl
	arrowFields []*ast.Node
}

// buildClassStruct builds the struct declaration for a class.
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
		// Arrow function fields → deferred as methods
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
		if isPrivateOrProtected(field) || ast.IsPrivateIdentifier(fname) {
			goFieldName = goVarName(toCamelCase(propName))
		}

		goType := b.resolveFieldType(field)

		structFields = append(structFields, IRStructField{
			Name:       goFieldName,
			Typ:        goTypeInfoFromString(goType),
			IsExported: !isPrivateOrProtected(field) && !ast.IsPrivateIdentifier(fname),
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

// constructorResult holds the constructor function and any deferred arrow fields.
type constructorResult struct {
	funcDecl    *IRFuncDecl
	arrowFields []*ast.Node
}

// buildConstructorDecl builds the NewClassName factory function.
func (b *IRBuilder) buildConstructorDecl(ctorNode *ast.Node, className string, baseClassName string, classNode *ast.Node, typeParams []IRTypeParam) *constructorResult {
	// Build type param suffix for return type
	typeParamSuffix := b.buildTypeParamSuffix(classNode)

	var params []IRParam
	if ctorNode != nil {
		params = b.buildParamList(ctorNode)
	}

	// Build constructor body
	var body []GoStmt
	var arrowFields []*ast.Node

	// s := &ClassName{}
	body = append(body, &IRVarDecl{
		Name:     "s",
		Typ:      goTypeInfoFromString("*" + className + typeParamSuffix),
		Init:     &IRRawExpr{exprBase: exprBase{}, Code: "&" + className + typeParamSuffix + "{}"},
		UseShort: true,
	})

	// Process constructor body statements
	savedReceiver := b.thisReceiver
	b.thisReceiver = "s"

	if ctorNode != nil {
		ctorBody := ctorNode.Body()
		if ctorBody != nil {
			block := ctorBody.AsBlock()
			if block.Statements != nil {
				for _, stmt := range block.Statements.Nodes {
					result := b.buildConstructorStmt(stmt, "s", className, baseClassName)
					body = append(body, result.stmts...)
					arrowFields = append(arrowFields, result.arrowFields...)
				}
			}
		}
	}

	b.thisReceiver = savedReceiver

	// return s
	body = append(body, &IRReturn{Values: []GoExpr{&IRIdent{exprBase: exprBase{}, Name: "s"}}})

	retType := "*" + className + typeParamSuffix

	return &constructorResult{
		funcDecl: &IRFuncDecl{
			Name:       "New" + className,
			TypeParams: typeParams,
			Params:     params,
			RetType:    goTypeInfoFromString(retType),
			Body:       body,
			IsExported: true,
		},
		arrowFields: arrowFields,
	}
}

// constructorStmtResult holds statements and deferred arrow fields from a constructor statement.
type constructorStmtResult struct {
	stmts       []GoStmt
	arrowFields []*ast.Node
}

// buildConstructorStmt processes a single constructor statement, handling this.x = expr and super() patterns.
func (b *IRBuilder) buildConstructorStmt(node *ast.Node, receiverVar string, className string, baseClassName string) constructorStmtResult {
	if node.Kind == ast.KindExpressionStatement {
		exprStmt := node.AsExpressionStatement()

		// this.field = value
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
						return constructorStmtResult{
							arrowFields: []*ast.Node{node},
						}
					}

					// this.field = expr → s.Field = expr
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

			// this[dynamic] = expr → skip
			if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
				ea := bin.Left.AsElementAccessExpression()
				if ea.Expression.Kind == ast.KindThisKeyword {
					return constructorStmtResult{}
				}
			}
		}

		// super(args...) → s.Base = *NewBase(args...)
		if exprStmt.Expression.Kind == ast.KindCallExpression {
			call := exprStmt.Expression.AsCallExpression()
			if call.Expression.Kind == ast.KindSuperKeyword && baseClassName != "" {
				args := b.buildArgList(call.Arguments)
				if baseClassName == "Error" {
					// s.Message = arg
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
				// s.Base = *NewBase(args...)
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
	}

	// Fallback: regular statement
	stmt := b.BuildStmt(node)
	if stmt == nil {
		return constructorStmtResult{}
	}
	return constructorStmtResult{stmts: []GoStmt{stmt}}
}

// buildMethodDecl builds a class instance method.
func (b *IRBuilder) buildMethodDecl(node *ast.Node, className string, typeParamSuffix string, overloadedMethods map[string]bool) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}

	// Skip overload signatures (declarations without body)
	body := node.Body()
	if body == nil && !ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) {
		return nil
	}

	rawName := nodeText(name)
	methodName := goExportedName(rawName)
	if isPrivateOrProtected(node) || ast.IsPrivateIdentifier(name) {
		methodName = goVarName(toCamelCase(rawName))
	}
	recVar := receiverVarName(className)

	// Save and clear method-level type params
	savedTypeParams := b.tm.typeParams
	methodTypeParams := node.TypeParameterList()
	if methodTypeParams != nil && len(methodTypeParams.Nodes) > 0 {
		newParams := make(map[string]bool)
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

	// Overloaded methods → any return type
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

	savedReceiver := b.thisReceiver
	savedRetType := b.currentRetType
	savedAsync := b.inAsyncBody
	b.thisReceiver = recVar
	b.currentRetType = retType
	b.inAsyncBody = isAsync

	var bodyStmts []GoStmt
	if body != nil {
		bodyStmts = b.buildStmtList(body)
	} else if ast.HasSyntacticModifier(node, ast.ModifierFlagsAbstract) {
		bodyStmts = []GoStmt{&IRExprStmt{Expr: &IRStdlibCall{
			exprBase: exprBase{},
			Package:  "", Func: "panic",
			Args: []GoExpr{irString(`"abstract method not implemented"`)},
		}}}
	}

	b.thisReceiver = savedReceiver
	b.currentRetType = savedRetType
	b.inAsyncBody = savedAsync
	b.tm.typeParams = savedTypeParams

	retTypeInfo := goTypeInfoFromString(retType)
	if isAsync {
		b.addImport("github.com/i2y/ramune/jsrt/promise", "")
		innerType := retType
		if innerType == "" {
			innerType = "any"
		}
		retTypeInfo = goTypeInfoFromString("*promise.Promise[" + innerType + "]")
	}

	return &IRFuncDecl{
		Name:    methodName,
		Params:  params,
		RetType: retTypeInfo,
		Body:    bodyStmts,
		Receiver: &IRReceiver{
			Name: recVar,
			Type: "*" + className + typeParamSuffix,
		},
		IsAsync:    isAsync,
		IsExported: !isPrivateOrProtected(node) && !ast.IsPrivateIdentifier(name),
	}
}

// buildArrowFieldAsMethodDecl converts a class field with arrow function to a method.
func (b *IRBuilder) buildArrowFieldAsMethodDecl(node *ast.Node, className string, typeParamSuffix string) *IRFuncDecl {
	// Handle both class body arrow fields and constructor arrow fields
	var propName string
	var initNode *ast.Node

	if node.Kind == ast.KindPropertyDeclaration {
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
	} else if node.Kind == ast.KindExpressionStatement {
		// Constructor arrow field: this.field = () => { ... }
		exprStmt := node.AsExpressionStatement()
		if exprStmt.Expression.Kind != ast.KindBinaryExpression {
			return nil
		}
		bin := exprStmt.Expression.AsBinaryExpression()
		if bin.Left.Kind != ast.KindPropertyAccessExpression {
			return nil
		}
		prop := bin.Left.AsPropertyAccessExpression()
		propName = nodeText(prop.Name())
		initNode = bin.Right
	} else {
		return nil
	}

	methodName := goExportedName(propName)
	if isPrivateOrProtected(node) || (node.Name() != nil && ast.IsPrivateIdentifier(node.Name())) {
		methodName = goVarName(toCamelCase(propName))
	}

	recVar := receiverVarName(className)
	params := b.buildParamList(initNode)
	retType := b.resolveReturnType(initNode)
	isAsync := ast.HasSyntacticModifier(initNode, ast.ModifierFlagsAsync)

	savedReceiver := b.thisReceiver
	savedRetType := b.currentRetType
	savedAsync := b.inAsyncBody
	b.thisReceiver = recVar
	b.currentRetType = retType
	b.inAsyncBody = isAsync

	var bodyStmts []GoStmt
	body := initNode.Body()
	if initNode.Kind == ast.KindArrowFunction {
		body = initNode.AsArrowFunction().Body
	}
	if body != nil && body.Kind == ast.KindBlock {
		bodyStmts = b.buildStmtList(body)
	} else if body != nil {
		// Expression body → return expr
		bodyStmts = []GoStmt{&IRReturn{Values: []GoExpr{b.BuildExpr(body)}}}
	}

	b.thisReceiver = savedReceiver
	b.currentRetType = savedRetType
	b.inAsyncBody = savedAsync

	retTypeInfo := goTypeInfoFromString(retType)
	if isAsync {
		b.addImport("github.com/i2y/ramune/jsrt/promise", "")
		innerType := retType
		if innerType == "" {
			innerType = "any"
		}
		retTypeInfo = goTypeInfoFromString("*promise.Promise[" + innerType + "]")
	}

	return &IRFuncDecl{
		Name:    methodName,
		Params:  params,
		RetType: retTypeInfo,
		Body:    bodyStmts,
		Receiver: &IRReceiver{
			Name: recVar,
			Type: "*" + className + typeParamSuffix,
		},
		IsAsync:    isAsync,
		IsExported: true,
	}
}

// buildGetterDecl builds a getter method: get foo() → func (r *Class) Foo() RetType.
func (b *IRBuilder) buildGetterDecl(node *ast.Node, className string, typeParamSuffix string) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	rawName := nodeText(name)
	if rawName == "" {
		return nil
	}
	methodName := goExportedName(rawName)
	recVar := receiverVarName(className)

	retType := b.resolveReturnType(node)
	if retType == "" && b.ck != nil {
		getterType := b.ck.GetTypeAtLocation(node)
		if getterType != nil {
			retType = b.tm.goReturnType(getterType)
		}
	}

	savedReceiver := b.thisReceiver
	savedRetType := b.currentRetType
	b.thisReceiver = recVar
	b.currentRetType = retType

	var bodyStmts []GoStmt
	body := node.Body()
	if body != nil {
		bodyStmts = b.buildStmtList(body)
	}

	b.thisReceiver = savedReceiver
	b.currentRetType = savedRetType

	return &IRFuncDecl{
		Name:    methodName,
		RetType: goTypeInfoFromString(retType),
		Body:    bodyStmts,
		Receiver: &IRReceiver{
			Name: recVar,
			Type: "*" + className + typeParamSuffix,
		},
		IsExported: true,
	}
}

// buildSetterDecl builds a setter method: set foo(v) → func (r *Class) SetFoo(v ParamType).
func (b *IRBuilder) buildSetterDecl(node *ast.Node, className string, typeParamSuffix string) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	rawName := nodeText(name)
	if rawName == "" {
		return nil
	}
	methodName := "Set" + goExportedName(rawName)
	recVar := receiverVarName(className)
	params := b.buildParamList(node)

	savedReceiver := b.thisReceiver
	b.thisReceiver = recVar

	var bodyStmts []GoStmt
	body := node.Body()
	if body != nil {
		bodyStmts = b.buildStmtList(body)
	}

	b.thisReceiver = savedReceiver

	return &IRFuncDecl{
		Name:   methodName,
		Params: params,
		Body:   bodyStmts,
		Receiver: &IRReceiver{
			Name: recVar,
			Type: "*" + className + typeParamSuffix,
		},
		IsExported: true,
	}
}

// buildStaticFieldDecl builds a static field as a package-level variable.
func (b *IRBuilder) buildStaticFieldDecl(node *ast.Node, className string) GoDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	propName := nodeText(name)
	goFieldName := goExportedName(propName)
	goType := b.resolveFieldType(node)

	varName := className + "_" + goFieldName

	pd := node.AsPropertyDeclaration()
	var init GoExpr
	if pd.Initializer != nil {
		init = b.BuildExpr(pd.Initializer)
	}

	return &IRStmtDecl{Stmt: &IRVarDecl{
		Name:    varName,
		Typ:     goTypeInfoFromString(goType),
		Init:    init,
		IsConst: false,
	}}
}

// buildStaticMethodDecl builds a static method as a package-level function.
func (b *IRBuilder) buildStaticMethodDecl(node *ast.Node, className string) *IRFuncDecl {
	name := node.Name()
	if name == nil {
		return nil
	}
	rawName := nodeText(name)
	methodName := className + "_" + goExportedName(rawName)

	params := b.buildParamList(node)
	retType := b.resolveReturnType(node)
	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	savedReceiver := b.thisReceiver
	savedRetType := b.currentRetType
	savedAsync := b.inAsyncBody
	b.thisReceiver = ""
	b.currentRetType = retType
	b.inAsyncBody = isAsync

	var bodyStmts []GoStmt
	body := node.Body()
	if body != nil {
		bodyStmts = b.buildStmtList(body)
	}

	b.thisReceiver = savedReceiver
	b.currentRetType = savedRetType
	b.inAsyncBody = savedAsync

	retTypeInfo := goTypeInfoFromString(retType)
	if isAsync {
		b.addImport("github.com/i2y/ramune/jsrt/promise", "")
		innerType := retType
		if innerType == "" {
			innerType = "any"
		}
		retTypeInfo = goTypeInfoFromString("*promise.Promise[" + innerType + "]")
	}

	return &IRFuncDecl{
		Name:       methodName,
		Params:     params,
		RetType:    retTypeInfo,
		Body:       bodyStmts,
		IsAsync:    isAsync,
		IsExported: true,
	}
}

// --------------------------------------------------------------------
// Export declarations
// --------------------------------------------------------------------

// buildExportDecl builds an export declaration.
func (b *IRBuilder) buildExportDecl(node *ast.Node) []GoDecl {
	// Named exports without module specifier (export { X }) are handled by modifier flags
	// Re-exports (export { X } from './mod') are complex — emit as raw for now
	return nil
}

// buildExportAssignment builds an export default declaration.
func (b *IRBuilder) buildExportAssignment(node *ast.Node) []GoDecl {
	ea := node.AsExportAssignment()
	if ea.Expression == nil {
		return nil
	}

	// export default function/class → named "Default"
	if ea.Expression.Kind == ast.KindFunctionExpression || ea.Expression.Kind == ast.KindArrowFunction {
		params := b.buildParamList(ea.Expression)
		retType := b.resolveReturnType(ea.Expression)
		isAsync := ast.HasSyntacticModifier(ea.Expression, ast.ModifierFlagsAsync)

		savedAsync := b.inAsyncBody
		savedRetType := b.currentRetType
		b.inAsyncBody = isAsync
		b.currentRetType = retType

		var bodyStmts []GoStmt
		body := ea.Expression.Body()
		if ea.Expression.Kind == ast.KindArrowFunction {
			body = ea.Expression.AsArrowFunction().Body
		}
		if body != nil && body.Kind == ast.KindBlock {
			bodyStmts = b.buildStmtList(body)
		} else if body != nil {
			bodyStmts = []GoStmt{&IRReturn{Values: []GoExpr{b.BuildExpr(body)}}}
		}

		b.inAsyncBody = savedAsync
		b.currentRetType = savedRetType

		retTypeInfo := goTypeInfoFromString(retType)
		if isAsync {
			b.addImport("github.com/i2y/ramune/jsrt/promise", "")
			innerType := retType
			if innerType == "" {
				innerType = "any"
			}
			retTypeInfo = goTypeInfoFromString("*promise.Promise[" + innerType + "]")
		}

		return []GoDecl{&IRFuncDecl{
			Name:       "Default",
			Params:     params,
			RetType:    retTypeInfo,
			Body:       bodyStmts,
			IsAsync:    isAsync,
			IsExported: true,
		}}
	}

	// export default expr → var Default = expr
	return []GoDecl{&IRStmtDecl{Stmt: &IRVarDecl{
		Name: "Default",
		Typ:  goTypeInfoFromString("any"),
		Init: b.BuildExpr(ea.Expression),
	}}}
}
