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
		tpDecl := tp.AsTypeParameterDeclaration()
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
		ref := constraint.AsTypeReferenceNode()
		if ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
			return t.tm.qualifyTypeName(ref.TypeName.AsIdentifier().Text)
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
		// Check for call signatures → emit as func type
		if iface.Members != nil {
			for _, member := range iface.Members.Nodes {
				if member.Kind == ast.KindCallSignature {
					// Callable interface → type Name = func(params) retType
					if t.ck != nil {
						// Skip if a same-name function/variable exists (avoids redeclaration)
						tsName := nodeText(name)
						if (t.samePackageExports != nil && t.samePackageExports[tsName]) ||
							(t.samePackageExports != nil && t.samePackageExports[toCamelCase(tsName)]) {
							return
						}
						ifaceType := t.ck.GetTypeAtLocation(node)
						if ifaceType != nil {
							sigs := t.ck.GetSignaturesOfType(ifaceType, checker.SignatureKindCall)
							if len(sigs) > 0 {
								t.w.writef("type %s = func(", goName)
								for i, p := range sigs[0].Parameters() {
									if i > 0 {
										t.w.write(", ")
									}
									pt := t.ck.GetTypeOfSymbol(p)
									pType := "any"
									if pt != nil {
										pType = t.tm.goType(pt)
										pType = replaceCrossPackageTypes(pType)
										pType = t.replaceUnknownTypes(pType)
									}
									t.w.writef("%s %s", goVarName(p.Name), pType)
								}
								t.w.write(")")
								retType := t.ck.GetReturnTypeOfSignature(sigs[0])
								if retType != nil {
									goRet := t.tm.goType(retType)
									if goRet != "" {
										goRet = replaceCrossPackageTypes(goRet)
										goRet = t.replaceUnknownTypes(goRet)
										t.w.writef(" %s", goRet)
									}
								}
								t.w.newline()
								t.w.newline()
								return
							}
						}
					}
				}
			}
		}
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

	// For intersection types (e.g., string & HtmlEscaped), try to resolve named constituents
	// from the TS AST directly, since the checker may expand aliases to anonymous types.
	if (goType == "string" || goType == "float64" || goType == "bool" || goType == "any") &&
		aliasType != nil && aliasType.Flags()&checker.TypeFlagsIntersection != 0 {
		if ta.Type.Kind == ast.KindIntersectionType {
			// Scan intersection constituents for named type references
			for _, member := range ta.Type.AsIntersectionTypeNode().Types.Nodes {
				if member.Kind == ast.KindTypeReference {
					ref := member.AsTypeReferenceNode()
					if ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
						refName := ref.TypeName.AsIdentifier().Text
						goRefName := goTypeName(refName)
						if goRefName != goName && goRefName != "string" && goRefName != "float64" && goRefName != "bool" {
							goType = t.tm.qualifyTypeName(refName)
							break
						}
					}
				}
			}
		}
	}

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

		// Use pre-pass mapping if available (handles intersection types resolved from AST)
		if t.tm.typeAliases != nil {
			if prePassType, ok := t.tm.typeAliases[goName]; ok && prePassType != goType {
				goType = prePassType
			}
		}

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

		// If type alias resolves to itself (e.g., Env → Env), try to emit as struct
		if goType == goName || goType == "any" {
			if aliasType != nil && (aliasType.Flags()&checker.TypeFlagsObject != 0 ||
				aliasType.Flags()&checker.TypeFlagsIntersection != 0) {
				props := t.ck.GetPropertiesOfType(aliasType)
				if len(props) > 0 {
					t.w.writef("type %s struct", goName)
					t.w.openBlock()
					// For intersection types (e.g., string & HtmlEscaped):
					// - Add Value field for primitive part
					// - Embed named type for object part (promotes its fields)
					if aliasType.Flags()&checker.TypeFlagsIntersection != 0 {
						inter := aliasType.AsIntersectionType()
						for _, u := range inter.Types() {
							if u.Flags()&checker.TypeFlagsStringLike != 0 {
								t.w.writeln("Value string")
							} else if u.Flags()&checker.TypeFlagsNumberLike != 0 {
								t.w.writeln("Value float64")
							} else if u.Flags()&checker.TypeFlagsBooleanLike != 0 {
								t.w.writeln("Value bool")
							}
						}
						// Embed named type constituents from AST
						if ta.Type != nil && ta.Type.Kind == ast.KindIntersectionType {
							for _, member := range ta.Type.AsIntersectionTypeNode().Types.Nodes {
								if member.Kind == ast.KindTypeReference {
									ref := member.AsTypeReferenceNode()
									if ref.TypeName != nil && ref.TypeName.Kind == ast.KindIdentifier {
										refName := goExportedName(ref.TypeName.AsIdentifier().Text)
										t.w.writeln(refName)
									}
								}
							}
						}
						// Skip individual property emission — they come from the embedded type
						props = nil
					}
					// For intersection types, extract properties from the TS AST type declaration
					// (not from checker which merges ALL constituent properties including prototypes)
					if aliasType.Flags()&checker.TypeFlagsIntersection != 0 && ta.Type != nil {
						props = nil // reset
						// Walk the AST intersection members and extract explicit property signatures
						if ta.Type.Kind == ast.KindIntersectionType {
							for _, member := range ta.Type.AsIntersectionTypeNode().Types.Nodes {
								if member.Kind == ast.KindTypeLiteral {
									// Inline object type: { isEscaped: true, callbacks?: ... }
									typeLit := member.AsTypeLiteralNode()
									if typeLit.Members != nil {
										for _, m := range typeLit.Members.Nodes {
											if m.Kind == ast.KindPropertySignature && m.Name() != nil {
												sym := t.ck.GetSymbolAtLocation(m.Name())
												if sym != nil {
													props = append(props, sym)
												}
											}
										}
									}
								} else if member.Kind == ast.KindTypeReference {
									// Named type reference (e.g., HtmlEscaped) → get its declared properties
									ref := member.AsTypeReferenceNode()
									if ref.TypeName != nil {
										refType := t.ck.GetTypeAtLocation(ref.TypeName)
										if refType != nil && refType.Flags()&checker.TypeFlagsObject != 0 {
											// Use the referenced type's own properties (not merged)
											refProps := t.ck.GetPropertiesOfType(refType)
											for _, p := range refProps {
												if isValidGoIdentifier(p.Name) && !strings.HasPrefix(p.Name, "__") {
													props = append(props, p)
												}
											}
										}
									}
								}
							}
						}
					}
					for _, p := range props {
						// Skip internal/special properties (e.g., __@iterator)
						if !isValidGoIdentifier(p.Name) || strings.HasPrefix(p.Name, "__") {
							continue
						}
						propName := goExportedName(p.Name)
						propType := "any"
						pt := t.ck.GetTypeOfSymbol(p)
						if pt != nil {
							propType = t.tm.goType(pt)
						}
						if propType == "" || propType == goName {
							propType = "any"
						}
						t.w.writelnf("%s %s", propName, propType)
					}
					t.w.closeBlock()
					t.w.newline()
					return
				}
			}
			// If it's a callable type (function signature), emit as func type
			// Skip if same-name function exists (avoids redeclaration)
			tsName := nodeText(name)
			hasSameNameFunc := (t.samePackageExports != nil && t.samePackageExports[tsName]) ||
				(t.samePackageExports != nil && t.samePackageExports[toCamelCase(tsName)])
			if aliasType != nil && !hasSameNameFunc {
				sigs := t.ck.GetSignaturesOfType(aliasType, checker.SignatureKindCall)
				if len(sigs) > 0 {
					t.w.writef("type %s = func(", goName)
					for i, p := range sigs[0].Parameters() {
						if i > 0 {
							t.w.write(", ")
						}
						pt := t.ck.GetTypeOfSymbol(p)
						pType := "any"
						if pt != nil {
							pType = t.tm.goType(pt)
							// Avoid import cycles: use any for cross-package types in type aliases
							if strings.Contains(pType, ".") {
								pType = "any"
							}
						}
						t.w.writef("%s %s", goVarName(p.Name), pType)
					}
					t.w.write(")")
					retType := t.ck.GetReturnTypeOfSignature(sigs[0])
					if retType != nil {
						goRet := t.tm.goType(retType)
						if goRet != "" {
							goRet = replaceCrossPackageTypes(goRet)
							goRet = t.replaceUnknownTypes(goRet)
							t.w.writef(" %s", goRet)
						}
					}
					t.w.newline()
					t.w.newline()
					return
				}
			}
		}

		if goType != "" && goType != "any" && goType != goName {
			switch goType {
			case "bool", "string", "float64", "int":
				// Emit primitive type aliases only if exported and non-generic
				if isExported(node) && (ta.TypeParameters == nil || len(ta.TypeParameters.Nodes) == 0) {
					t.w.writelnf("type %s = %s", goName, goType)
				}
			default:
				if aliasType != nil && aliasType.Flags()&checker.TypeFlagsTypeParameter != 0 {
					break
				}
				// Skip if a same-name function/variable exists (avoids redeclaration)
				tsName := nodeText(name)
				if (t.samePackageExports != nil && t.samePackageExports[tsName]) ||
					(t.samePackageExports != nil && t.samePackageExports[toCamelCase(tsName)]) {
					break
				}
				// For intersection types with primitives, emit struct with Value field
				if aliasType != nil && aliasType.Flags()&checker.TypeFlagsIntersection != 0 {
					inter := aliasType.AsIntersectionType()
					var valueType string
					for _, u := range inter.Types() {
						if u.Flags()&checker.TypeFlagsStringLike != 0 {
							valueType = "string"
						} else if u.Flags()&checker.TypeFlagsNumberLike != 0 {
							valueType = "float64"
						}
					}
					if valueType != "" {
						t.w.writef("type %s struct", goName)
						t.w.openBlock()
						t.w.writelnf("Value %s", valueType)
						// Embed the object type
						t.w.writeln(goType)
						t.w.closeBlock()
						t.w.newline()
						// Update alias mapping to self — HtmlEscapedString is now its own struct
						if t.tm.typeAliases != nil {
							t.tm.typeAliases[goName] = goName
						}
						break
					}
				}
				// Replace cross-package and unresolvable type references with any
				emitGoType := replaceCrossPackageTypes(goType)
				emitGoType = t.replaceUnknownTypes(emitGoType)
				t.w.writelnf("type %s = %s", goName, emitGoType)
			}
		}
		// Fallback: emit type alias for any-resolved types that are still referenced
		// Skip if a same-name function/variable exists (avoids redeclaration)
		fallbackTsName := nodeText(name)
		hasSameNameFallback := (t.samePackageExports != nil && t.samePackageExports[fallbackTsName]) ||
			(t.samePackageExports != nil && t.samePackageExports[toCamelCase(fallbackTsName)])
		if !hasSameNameFallback && (goType == "any" || goType == goName) {
			// Determine a reasonable Go type based on the TS type structure
			fallbackType := "any"
			if aliasType != nil && aliasType.Flags()&checker.TypeFlagsUnion != 0 {
				// Analyze union members to pick the best Go type
				members := aliasType.Types()
				allNumber := true
				allString := true
				allRecord := true
				for _, m := range members {
					if m.Flags()&checker.TypeFlagsNumberLike == 0 {
						allNumber = false
					}
					if m.Flags()&checker.TypeFlagsStringLike == 0 {
						allString = false
					}
					if m.Flags()&checker.TypeFlagsObject == 0 {
						allRecord = false
					}
				}
				if allNumber && len(members) > 0 {
					fallbackType = "int"
				} else if allString && len(members) > 0 {
					fallbackType = "string"
				} else if allRecord && len(members) > 0 {
					fallbackType = "map[string]any"
				}
			}
			// Only emit if the type is actually used (referenced in other declarations)
			t.w.writelnf("type %s = %s", goName, fallbackType)
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

	// Track overloaded method names (methods with multiple declarations)
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
	var arrowFieldMethods []*ast.Node
	for _, field := range fields {
		pd := field.AsPropertyDeclaration()
		if pd.Initializer != nil &&
			(pd.Initializer.Kind == ast.KindArrowFunction || pd.Initializer.Kind == ast.KindFunctionExpression) {
			arrowFieldMethods = append(arrowFieldMethods, field)
		} else {
			t.emitClassField(field)
		}
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

	// Emit methods (including arrow function fields converted to methods)
	for _, method := range methods {
		t.emitClassMethod(method, className, typeParamSuffix, overloadedMethods)
	}
	for _, field := range arrowFieldMethods {
		t.emitArrowFieldAsMethod(field, className, typeParamSuffix)
	}
	// Emit arrow function fields collected from constructor body
	for _, caf := range t.constructorArrowFields {
		goClassName := goExportedName(className)
		receiverVar := strings.ToLower(goClassName[:1])
		t.w.writef("func (%s *%s%s) %s(", receiverVar, goClassName, typeParamSuffix, caf.goName)
		savedPtrStringVars := t.goPtrStringVars
		t.goPtrStringVars = nil
		t.emitParameterList(caf.initNode)
		t.w.write(")")
		retType := t.getFuncReturnType(caf.initNode)
		if retType != "" {
			t.w.writef(" %s", retType)
		}
		body := caf.initNode.Body()
		if caf.initNode.Kind == ast.KindArrowFunction {
			body = caf.initNode.AsArrowFunction().Body
		}
		savedReceiver := t.thisReceiver
		savedRetType := t.currentRetType
		t.thisReceiver = receiverVar
		t.currentRetType = retType
		if body != nil && body.Kind == ast.KindBlock {
			t.needsDefaultReturn = retType != ""
			t.emitBlock(body)
		} else if body != nil {
			t.w.write(" { return ")
			t.emitExpr(body)
			t.w.write(" }")
		} else {
			t.w.write(" {}")
		}
		t.thisReceiver = savedReceiver
		t.currentRetType = savedRetType
		t.goPtrStringVars = savedPtrStringVars
		t.w.newline()
	}
	t.constructorArrowFields = nil

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

// replaceCrossPackageTypes replaces qualified type references like "pkgcontext.Context[any, any, any]"
// with "any" in type alias definitions to avoid import cycles.
// Preserves well-known packages: promise.Promise, web.Response, etc.
func replaceCrossPackageTypes(goType string) string {
	if !strings.Contains(goType, ".") {
		return goType
	}
	var result strings.Builder
	i := 0
	for i < len(goType) {
		// Find qualified name: identifier.Identifier possibly followed by [...]
		if i > 0 && goType[i-1] != ' ' && goType[i-1] != '(' && goType[i-1] != '*' && goType[i-1] != ',' {
			result.WriteByte(goType[i])
			i++
			continue
		}
		// Try to match pkg.Name[...] pattern
		j := i
		for j < len(goType) && (goType[j] == '_' || (goType[j] >= 'a' && goType[j] <= 'z') || (goType[j] >= 'A' && goType[j] <= 'Z') || (j > i && goType[j] >= '0' && goType[j] <= '9')) {
			j++
		}
		if j < len(goType) && goType[j] == '.' {
			pkg := goType[i:j]
			// Allow well-known packages
			if pkg == "promise" || pkg == "web" || pkg == "jsrt" || pkg == "jsarray" {
				result.WriteString(goType[i : j+1])
				i = j + 1
				continue
			}
			// Skip the qualified type name including any [...] suffix
			k := j + 1
			for k < len(goType) && (goType[k] == '_' || (goType[k] >= 'a' && goType[k] <= 'z') || (goType[k] >= 'A' && goType[k] <= 'Z') || (goType[k] >= '0' && goType[k] <= '9')) {
				k++
			}
			// Skip generic args [...]
			if k < len(goType) && goType[k] == '[' {
				depth := 1
				k++
				for k < len(goType) && depth > 0 {
					if goType[k] == '[' {
						depth++
					} else if goType[k] == ']' {
						depth--
					}
					k++
				}
			}
			result.WriteString("any")
			i = k
		} else {
			result.WriteByte(goType[i])
			i++
		}
	}
	return result.String()
}

// replaceUnknownTypes replaces non-primitive, non-well-known generic types with any
// when they reference types not available in the current package (e.g., Hono[any, any, any, any]).
func (t *Transpiler) replaceUnknownTypes(goType string) string {
	// Quick check — only process types with generic instantiations
	if !strings.Contains(goType, "[") {
		return goType
	}
	var result strings.Builder
	i := 0
	for i < len(goType) {
		// Try to match PascalCase identifier followed by [...]
		if (i == 0 || goType[i-1] == ' ' || goType[i-1] == '(' || goType[i-1] == ')' || goType[i-1] == '*' || goType[i-1] == ',' || goType[i-1] == '[') &&
			goType[i] >= 'A' && goType[i] <= 'Z' {
			j := i
			for j < len(goType) && (goType[j] == '_' || (goType[j] >= 'a' && goType[j] <= 'z') || (goType[j] >= 'A' && goType[j] <= 'Z') || (j > i && goType[j] >= '0' && goType[j] <= '9')) {
				j++
			}
			name := goType[i:j]
			if j < len(goType) && goType[j] == '[' {
				// Keep if the type is defined in the current file's package
				if t.localTypeNames != nil && t.localTypeNames[name] {
					result.WriteString(goType[i : j+1])
					i = j + 1
					continue
				}
				// Replace with any — type is from another package
				{
					depth := 1
					k := j + 1
					for k < len(goType) && depth > 0 {
						if goType[k] == '[' {
							depth++
						} else if goType[k] == ']' {
							depth--
						}
						k++
					}
					result.WriteString("any")
					i = k
					continue
				}
			}
		}
		result.WriteByte(goType[i])
		i++
	}
	return result.String()
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

					// Skip arrow function / function expression assignments in constructor —
					// these are handled as methods via emitArrowFieldAsMethod
					if bin.Right.Kind == ast.KindArrowFunction || bin.Right.Kind == ast.KindFunctionExpression {
						t.w.writelnf("/* %s.%s assigned in constructor — emitted as method */", receiverVar, goName)
						// Collect and emit as method later
						t.constructorArrowFields = append(t.constructorArrowFields, constructorArrowField{
							propName: propName,
							goName:   goName,
							initNode: bin.Right,
						})
						return
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
			// this[dynamic] = expr → skip (Go structs can't do dynamic property set)
			if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
				ea := bin.Left.AsElementAccessExpression()
				if ea.Expression.Kind == ast.KindThisKeyword {
					t.w.writeln("/* dynamic property assignment skipped (Go struct limitation) */")
					return
				}
			}
		}
		// Object.assign(this, obj) → skip
		if exprStmt.Expression.Kind == ast.KindCallExpression {
			call := exprStmt.Expression.AsCallExpression()
			if call.Expression.Kind == ast.KindPropertyAccessExpression {
				prop := call.Expression.AsPropertyAccessExpression()
				if prop.Expression.Kind == ast.KindIdentifier && prop.Expression.AsIdentifier().Text == "Object" &&
					nodeText(prop.Name()) == "assign" {
					t.w.writeln("/* Object.assign skipped */")
					return
				}
			}
		}
		// super(args...) → s.BaseClass = *NewBaseClass(args...)
		if exprStmt.Expression.Kind == ast.KindCallExpression {
			call := exprStmt.Expression.AsCallExpression()
			if call.Expression.Kind == ast.KindSuperKeyword && baseClassName != "" {
				if baseClassName == "Error" {
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
		// forEach with dynamic property assignment → skip entire loop
		if exprStmt.Expression.Kind == ast.KindCallExpression {
			call := exprStmt.Expression.AsCallExpression()
			if call.Expression.Kind == ast.KindPropertyAccessExpression {
				prop := call.Expression.AsPropertyAccessExpression()
				methodName := nodeText(prop.Name())
				if methodName == "forEach" {
					// Check if callback contains this[dynamic] = ... pattern
					if t.callbackContainsDynamicThisAssign(call) {
						t.w.writeln("/* forEach with dynamic this[method] assignment skipped */")
						return
					}
				}
			}
		}
	}
	// Variable statement with rest destructuring → handle rest
	if node.Kind == ast.KindVariableStatement {
		varStmt := node.AsVariableStatement()
		declList := varStmt.DeclarationList.AsVariableDeclarationList()
		if declList.Declarations != nil {
			for _, decl := range declList.Declarations.Nodes {
				vd := decl.AsVariableDeclaration()
				name := decl.Name()
				if name != nil && name.Kind == ast.KindObjectBindingPattern {
					bp := name.AsBindingPattern()
					if bp.Elements != nil {
						hasRest := false
						for _, elem := range bp.Elements.Nodes {
							be := elem.AsBindingElement()
							if be.DotDotDotToken != nil {
								hasRest = true
								break
							}
						}
						if hasRest && vd.Initializer != nil {
							t.emitObjectDestructuringWithRest(name, vd.Initializer, receiverVar)
							return
						}
					}
				}
			}
		}
	}
	// Fallback
	t.emitStatement(node)
}

// constructorArrowField tracks arrow function assignments found in constructor body
// that should be emitted as methods.
type constructorArrowField struct {
	propName string
	goName   string
	initNode *ast.Node
}

// callbackContainsDynamicThisAssign checks if a forEach call's callback contains this[x] = ... patterns.
func (t *Transpiler) callbackContainsDynamicThisAssign(call *ast.CallExpression) bool {
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return false
	}
	cb := call.Arguments.Nodes[0]
	if cb.Kind != ast.KindArrowFunction && cb.Kind != ast.KindFunctionExpression {
		return false
	}
	body := cb.Body()
	if cb.Kind == ast.KindArrowFunction {
		body = cb.AsArrowFunction().Body
	}
	if body == nil || body.Kind != ast.KindBlock {
		return false
	}
	block := body.AsBlock()
	if block.Statements == nil {
		return false
	}
	for _, stmt := range block.Statements.Nodes {
		if stmt.Kind == ast.KindExpressionStatement {
			expr := stmt.AsExpressionStatement().Expression
			if expr.Kind == ast.KindBinaryExpression {
				bin := expr.AsBinaryExpression()
				if bin.OperatorToken.Kind == ast.KindEqualsToken && bin.Left.Kind == ast.KindElementAccessExpression {
					ea := bin.Left.AsElementAccessExpression()
					if ea.Expression.Kind == ast.KindThisKeyword {
						return true
					}
				}
			}
		}
	}
	return false
}

// emitObjectDestructuringWithRest handles const { a, b, ...rest } = obj in constructor context.
func (t *Transpiler) emitObjectDestructuringWithRest(pattern *ast.Node, initializer *ast.Node, receiverVar string) {
	bp := pattern.AsBindingPattern()
	if bp.Elements == nil {
		return
	}

	initCode := t.captureExpr(initializer)
	var namedKeys []string

	for _, elem := range bp.Elements.Nodes {
		be := elem.AsBindingElement()
		name := be.Name()
		if name == nil {
			continue
		}
		if be.DotDotDotToken != nil {
			// Rest element: ...rest → rest := jsrt.OmitFields(obj, "key1", "key2", ...)
			restName := goVarName(name.AsIdentifier().Text)
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.writef("%s := jsrt.OmitFields(%s", restName, initCode)
			for _, k := range namedKeys {
				t.w.writef(", %q", goExportedName(k))
			}
			t.w.write(")")
			t.w.newline()
		} else {
			// Named element: a → a := jsrt.GetField(obj, "A")
			propName := name.AsIdentifier().Text
			varName := goVarName(propName)
			namedKeys = append(namedKeys, propName)
			t.w.addImport("github.com/i2y/ramune/jsrt", "")
			t.w.writef("%s := jsrt.GetField(%s, %q)", varName, initCode, goExportedName(propName))
			t.w.newline()
		}
	}
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

// emitArrowFieldAsMethod converts a class field with arrow function initializer to a method.
func (t *Transpiler) emitArrowFieldAsMethod(node *ast.Node, className string, typeParamSuffix string) {
	pd := node.AsPropertyDeclaration()
	name := node.Name()
	if name == nil || pd.Initializer == nil {
		return
	}

	var propName string
	if ast.IsPrivateIdentifier(name) {
		propName = strings.TrimPrefix(name.AsPrivateIdentifier().Text, "#")
	} else {
		propName = nodeText(name)
	}

	methodName := goExportedName(propName)
	if isPrivateOrProtected(node) || ast.IsPrivateIdentifier(name) {
		methodName = goVarName(toCamelCase(propName))
	}

	goClassName := goExportedName(className)
	receiverVar := strings.ToLower(goClassName[:1])

	// Remove method-level type params from scope (Go methods can't have extra type params)
	savedTypeParams := t.tm.typeParams
	if pd.Initializer != nil {
		methodTPs := pd.Initializer.TypeParameterList()
		if methodTPs != nil && len(methodTPs.Nodes) > 0 {
			newParams := make(map[string]bool)
			for k, v := range savedTypeParams {
				newParams[k] = v
			}
			for _, tp := range methodTPs.Nodes {
				delete(newParams, tp.Name().AsIdentifier().Text)
			}
			t.tm.typeParams = newParams
		}
	}

	t.w.writef("func (%s *%s%s) %s(", receiverVar, goClassName, typeParamSuffix, methodName)

	savedPtrStringVars := t.goPtrStringVars
	t.goPtrStringVars = nil
	t.emitParameterList(pd.Initializer)
	t.w.write(")")

	retType := t.getFuncReturnType(pd.Initializer)
	if retType != "" {
		t.w.writef(" %s", retType)
	}

	body := pd.Initializer.Body()
	if pd.Initializer.Kind == ast.KindArrowFunction {
		body = pd.Initializer.AsArrowFunction().Body
	}

	savedReceiver := t.thisReceiver
	savedRetType := t.currentRetType
	t.thisReceiver = receiverVar
	t.currentRetType = retType

	if body != nil && body.Kind == ast.KindBlock {
		t.needsDefaultReturn = retType != ""
		t.emitBlock(body)
	} else if body != nil {
		// Check for assignment-in-return: (a = b) → { a = b; return a }
		inner := body
		for inner.Kind == ast.KindParenthesizedExpression {
			inner = inner.AsParenthesizedExpression().Expression
		}
		if inner.Kind == ast.KindBinaryExpression {
			bin := inner.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				t.w.write(" { ")
				t.emitExpr(inner)
				t.w.write("; return ")
				t.emitExpr(bin.Left)
				t.w.write(" }")
			} else {
				t.w.write(" { return ")
				t.emitExpr(body)
				t.w.write(" }")
			}
		} else {
			t.w.write(" { return ")
			t.emitExpr(body)
			t.w.write(" }")
		}
	} else {
		t.w.write(" {}")
	}

	t.thisReceiver = savedReceiver
	t.currentRetType = savedRetType
	t.goPtrStringVars = savedPtrStringVars
	t.tm.typeParams = savedTypeParams
	t.w.newline()
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
func (t *Transpiler) emitClassMethod(node *ast.Node, className string, typeParamSuffix string, overloadedMethods map[string]bool) {
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

	// Save and clear method-level type params to prevent scope leaking.
	// Method-level generics (e.g., json<T>()) cannot be expressed in Go methods,
	// so we remove them from scope → they'll resolve to 'any'.
	savedTypeParams := t.tm.typeParams
	methodTypeParams := node.TypeParameterList()
	if methodTypeParams != nil && len(methodTypeParams.Nodes) > 0 {
		// Copy the class-level type params, but do NOT add method-level ones
		newParams := make(map[string]bool)
		for k, v := range savedTypeParams {
			newParams[k] = v
		}
		// Explicitly remove method-level type params so they resolve to 'any'
		for _, tp := range methodTypeParams.Nodes {
			delete(newParams, tp.Name().AsIdentifier().Text)
		}
		t.tm.typeParams = newParams
	}

	t.w.writef("func (%s *%s%s) %s(", receiverVar, className, typeParamSuffix, methodName)
	savedPtrStringVars := t.goPtrStringVars
	t.goPtrStringVars = nil
	t.emitParameterList(node)
	t.w.write(")")

	retType := t.getFuncReturnType(node)
	// Overloaded methods return different types → use any
	if overloadedMethods[rawName] {
		retType = "any"
	}
	if strings.HasPrefix(retType, "*promise.Promise[") {
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
	t.tm.typeParams = savedTypeParams
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

// getDiscriminatedUnionVariants checks if a union is a discriminated union.
func (t *Transpiler) getDiscriminatedUnionVariants(union *checker.UnionType) []discriminatedVariant {
	return detectDiscriminatedUnion(t.ck, union)
}

// detectDiscriminatedUnion checks if a union is a discriminated union
// (all members are named object types with a common discriminant field).
func detectDiscriminatedUnion(ck *checker.Checker, union *checker.UnionType) []discriminatedVariant {
	types := union.Types()
	if len(types) < 2 {
		return nil
	}

	var variants []discriminatedVariant
	for _, ut := range types {
		if ut.Flags()&checker.TypeFlagsNullable != 0 {
			continue
		}
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

	for _, fieldName := range discriminantFieldNames {
		allHave := true
		for _, v := range variants {
			if ck.GetPropertyOfType(v.Type, fieldName) == nil {
				allHave = false
				break
			}
		}
		if allHave {
			return variants
		}
	}

	return nil
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
