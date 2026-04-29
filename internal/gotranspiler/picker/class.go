package picker

import (
	"fmt"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// IsClassExtractable classifies a ClassDeclaration node for extraction.
//
// Scope (reject when any fails):
//   - no type parameters (generic classes)
//   - no heritage clauses (extends / implements)
//   - no decorators
//   - no static fields and no static initialization blocks
//   - no accessors (get/set) and no `#`-private identifiers
//   - every property declaration is typed and extractable, no optional (`?`)
//     fields, no initializers (constructor-initialised only)
//   - constructor body is only `this.<field> = <expr>` statements; <expr> is
//     body-extractable
//   - every method (instance or static) has an extractable signature
//     (params + return) and a body using the same allowlist as free
//     functions, plus `this`, `this.<field>` (read/write), and
//     `this.<method>(...)` self-calls (instance methods only — static
//     methods cannot reference `this` or instance members)
//
// Accepts both `export class C { ... }` and bare `class C { ... }`.
//
// staticMethods (input/output): when non-nil, the picker writes the
// accepted static method names back keyed under the class's identifier
// so other extracted functions' bodies can validate `Class.method(...)`
// calls. nil disables the cross-reference channel.
func IsClassExtractable(node *ast.Node, ck *checker.Checker, topLevelFuncs map[string]struct{}, staticMethods map[string]map[string]bool) (bool, Reason) {
	if node == nil || node.Kind != ast.KindClassDeclaration {
		return false, Reason{Code: reasonUnhandledKind, Detail: "not a class declaration"}
	}
	cd := node.AsClassDeclaration()
	if cd == nil {
		return false, Reason{Code: reasonUnhandledKind, Detail: "nil class declaration"}
	}

	name := node.Name()
	if name == nil {
		return false, Reason{Code: reasonUnnamed, Detail: "class has no name"}
	}

	if cd.TypeParameters != nil && len(cd.TypeParameters.Nodes) > 0 {
		return false, Reason{Code: reasonGenericFunc, Detail: "class has type parameters"}
	}
	if cd.HeritageClauses != nil && len(cd.HeritageClauses.Nodes) > 0 {
		return false, Reason{Code: reasonClassHeritage, Detail: "extends/implements not supported"}
	}
	if mods := node.Modifiers(); mods != nil {
		for _, m := range mods.Nodes {
			if m.Kind == ast.KindDecorator {
				return false, Reason{Code: reasonClassDecorator, Detail: "class decorator"}
			}
			if m.Kind == ast.KindAbstractKeyword {
				return false, Reason{Code: reasonClassHeritage, Detail: "abstract class"}
			}
		}
	}

	// First pass: classify members and collect the field/method name sets so the
	// body walker can validate `this.field` / `this.method(...)` references.
	var fields []*ast.Node
	var methods []*ast.Node
	var statics []*ast.Node
	var constructor *ast.Node
	thisFields := map[string]bool{}
	thisMethods := map[string]bool{}
	staticNames := map[string]bool{}

	if cd.Members != nil {
		for _, member := range cd.Members.Nodes {
			isStatic := ast.HasSyntacticModifier(member, ast.ModifierFlagsStatic)
			if ast.HasSyntacticModifier(member, ast.ModifierFlagsAbstract) {
				return false, Reason{Code: reasonClassHeritage, Detail: "abstract member"}
			}
			if mods := member.Modifiers(); mods != nil {
				for _, m := range mods.Nodes {
					if m.Kind == ast.KindDecorator {
						return false, Reason{Code: reasonClassDecorator, Detail: "member decorator"}
					}
				}
			}

			switch member.Kind {
			case ast.KindPropertyDeclaration:
				if isStatic {
					return false, Reason{Code: reasonClassStatic, Detail: "static field not supported — only static methods (try: replace with a top-level `const` outside the class)"}
				}
				mname := member.Name()
				if mname == nil {
					return false, Reason{Code: reasonUnhandledKind, Detail: "unnamed property"}
				}
				if ast.IsPrivateIdentifier(mname) {
					return false, Reason{Code: reasonClassPrivate, Detail: "#private field"}
				}
				if mname.Kind != ast.KindIdentifier {
					return false, Reason{Code: reasonUnhandledKind, Detail: "computed property name"}
				}
				fields = append(fields, member)
				thisFields[mname.AsIdentifier().Text] = true
			case ast.KindMethodDeclaration:
				mname := member.Name()
				if mname == nil {
					return false, Reason{Code: reasonUnhandledKind, Detail: "unnamed method"}
				}
				if ast.IsPrivateIdentifier(mname) {
					return false, Reason{Code: reasonClassPrivate, Detail: "#private method"}
				}
				if mname.Kind != ast.KindIdentifier {
					return false, Reason{Code: reasonUnhandledKind, Detail: "computed method name"}
				}
				if isStatic {
					statics = append(statics, member)
					staticNames[mname.AsIdentifier().Text] = true
				} else {
					methods = append(methods, member)
					thisMethods[mname.AsIdentifier().Text] = true
				}
			case ast.KindConstructor:
				if isStatic {
					return false, Reason{Code: reasonClassStatic, Detail: "static constructor"}
				}
				if constructor != nil {
					return false, Reason{Code: reasonUnhandledKind, Detail: "overloaded constructor"}
				}
				constructor = member
			case ast.KindGetAccessor, ast.KindSetAccessor:
				return false, Reason{Code: reasonClassAccessor, Detail: "getter/setter not supported"}
			case ast.KindClassStaticBlockDeclaration:
				return false, Reason{Code: reasonClassStatic, Detail: "static initialization block"}
			case ast.KindSemicolonClassElement:
				// stray `;` between members — harmless, skip
				continue
			default:
				return false, Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("class member kind %v not supported", member.Kind)}
			}
		}
	}

	// Field check: typed and extractable, no `?`, no initializer.
	for _, f := range fields {
		pd := f.AsPropertyDeclaration()
		if pd == nil {
			continue
		}
		if pd.PostfixToken != nil && pd.PostfixToken.Kind == ast.KindQuestionToken {
			return false, Reason{Code: reasonUnhandledKind, Detail: "optional field `" + f.Name().AsIdentifier().Text + "`"}
		}
		if pd.Initializer != nil {
			return false, Reason{Code: reasonUnhandledKind, Detail: "field `" + f.Name().AsIdentifier().Text + "` has initializer (use constructor assignment)"}
		}
		if ck != nil {
			if r := isExtractableType(ck, ck.GetTypeAtLocation(f)); r != nil {
				return false, Reason{Code: r.Code, Detail: "field `" + f.Name().AsIdentifier().Text + "`: " + r.Detail}
			}
		}
	}

	// Constructor check: parameters extractable; body is only `this.<field> = expr`.
	// Uses a scoped bodyCtx so expression RHS is validated with the same
	// allowlist as function bodies.
	if constructor != nil {
		if r := checkConstructor(constructor, ck, topLevelFuncs, thisFields, thisMethods); r != nil {
			return false, *r
		}
	}

	// Method check: signature + body.
	for _, m := range methods {
		if r := checkClassMethod(m, ck, topLevelFuncs, thisFields, thisMethods, staticMethods, false); r != nil {
			return false, *r
		}
	}
	for _, m := range statics {
		if r := checkClassMethod(m, ck, topLevelFuncs, thisFields, thisMethods, staticMethods, true); r != nil {
			return false, *r
		}
	}

	// Publish accepted static names so other extracted bodies can call
	// `<ClassName>.<method>(...)` after this point in the same Pick pass.
	if staticMethods != nil && len(staticNames) > 0 {
		className := name.AsIdentifier().Text
		entry := staticMethods[className]
		if entry == nil {
			entry = map[string]bool{}
			staticMethods[className] = entry
		}
		for n := range staticNames {
			entry[n] = true
		}
	}

	return true, Reason{}
}

// checkConstructor validates a constructor's parameters and body. The body is
// restricted to `this.<field> = <expr>` statements — all field initialisation
// happens here (field initializers are rejected up in the field loop).
func checkConstructor(ctor *ast.Node, ck *checker.Checker, topLevelFuncs map[string]struct{}, thisFields, thisMethods map[string]bool) *Reason {
	cd := ctor.AsConstructorDeclaration()
	if cd == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "nil constructor"}
	}
	if cd.TypeParameters != nil && len(cd.TypeParameters.Nodes) > 0 {
		return &Reason{Code: reasonGenericFunc, Detail: "generic constructor"}
	}

	paramNames := map[string]bool{}
	if cd.Parameters != nil {
		for i, p := range cd.Parameters.Nodes {
			pd := p.AsParameterDeclaration()
			if pd == nil {
				continue
			}
			// Parameter-property shorthand (`constructor(public x: number)`)
			// has modifiers on the parameter. The emitter would need to treat
			// those as field declarations AND as parameters — defer.
			if mods := p.Modifiers(); mods != nil && len(mods.Nodes) > 0 {
				return &Reason{Code: reasonClassParamProp, Detail: "parameter-property shorthand"}
			}
			if pd.DotDotDotToken != nil {
				return &Reason{Code: reasonRestParam, Detail: "rest parameter in constructor"}
			}
			if pd.Initializer != nil {
				return &Reason{Code: reasonUnhandledKind, Detail: "default parameter value in constructor"}
			}
			if pd.QuestionToken != nil {
				return &Reason{Code: reasonUnhandledKind, Detail: "optional parameter in constructor"}
			}
			if pd.Name() == nil || pd.Name().Kind != ast.KindIdentifier {
				return &Reason{Code: reasonUnhandledKind, Detail: "constructor param destructuring"}
			}
			pname := pd.Name().AsIdentifier().Text
			paramNames[pname] = true
			if ck != nil {
				if r := isExtractableType(ck, ck.GetTypeAtLocation(pd.Name())); r != nil {
					return &Reason{Code: r.Code, Detail: fmt.Sprintf("constructor param %d (%s): %s", i, pname, r.Detail)}
				}
			}
		}
	}

	ctx := &bodyCtx{
		ck:            ck,
		paramNames:    paramNames,
		topLevelFuncs: topLevelFuncs,
		localNames:    map[string]bool{},
		inMethod:      true,
		thisFields:    thisFields,
		thisMethods:   thisMethods,
	}

	body := cd.Body
	if body == nil {
		return nil
	}
	if body.Kind != ast.KindBlock {
		return &Reason{Code: reasonUnhandledKind, Detail: "constructor body is not a block"}
	}
	blk := body.AsBlock()
	if blk.Statements == nil {
		return nil
	}
	for _, stmt := range blk.Statements.Nodes {
		if r := checkConstructorStatement(stmt, ctx); r != nil {
			return r
		}
	}
	return nil
}

// checkConstructorStatement accepts only `this.<field> = <expr>;` where
// `<expr>` is body-extractable. Everything else (local vars, conditionals,
// method calls, super calls) is rejected — the emitter's constructor
// path was written for a richer set but the picker contract stays narrow to
// keep the generated Go predictable.
func checkConstructorStatement(stmt *ast.Node, ctx *bodyCtx) *Reason {
	if stmt.Kind != ast.KindExpressionStatement {
		return &Reason{Code: reasonUnhandledKind, Detail: fmt.Sprintf("constructor may only contain `this.field = expr` (got %v)", stmt.Kind)}
	}
	es := stmt.AsExpressionStatement()
	if es == nil || es.Expression == nil || es.Expression.Kind != ast.KindBinaryExpression {
		return &Reason{Code: reasonUnhandledKind, Detail: "constructor statement is not an assignment"}
	}
	be := es.Expression.AsBinaryExpression()
	if be.OperatorToken.Kind != ast.KindEqualsToken {
		return &Reason{Code: reasonForbiddenOp, Detail: "constructor supports only plain `=` assignment"}
	}
	if be.Left.Kind != ast.KindPropertyAccessExpression {
		return &Reason{Code: reasonUnhandledKind, Detail: "constructor LHS must be `this.<field>`"}
	}
	pa := be.Left.AsPropertyAccessExpression()
	if pa.Expression == nil || pa.Expression.Kind != ast.KindThisKeyword {
		return &Reason{Code: reasonUnhandledKind, Detail: "constructor LHS must start with `this`"}
	}
	if pa.Name() == nil || pa.Name().Kind != ast.KindIdentifier {
		return &Reason{Code: reasonUnhandledKind, Detail: "constructor LHS `this.<name>` must be a plain identifier"}
	}
	fieldName := pa.Name().AsIdentifier().Text
	if !ctx.thisFields[fieldName] {
		return &Reason{Code: reasonUnhandledKind, Detail: "constructor assigns to unknown field `" + fieldName + "`"}
	}
	return checkExpr(be.Right, ctx)
}

// checkClassMethod validates a MethodDeclaration's signature and body.
// Same rules as a free function, plus `this` / `this.<field>` / `this.<method>`
// for instance methods. isStatic switches off the `this`-related allowances
// (static method bodies must not reference `this` or instance members).
func checkClassMethod(m *ast.Node, ck *checker.Checker, topLevelFuncs map[string]struct{}, thisFields, thisMethods map[string]bool, staticMethods map[string]map[string]bool, isStatic bool) *Reason {
	md := m.AsMethodDeclaration()
	if md == nil {
		return &Reason{Code: reasonUnhandledKind, Detail: "nil method"}
	}
	name := m.Name().AsIdentifier().Text
	label := "method `" + name + "` "
	if isStatic {
		label = "static method `" + name + "` "
	}
	if md.AsteriskToken != nil {
		return &Reason{Code: reasonGeneratorFunc, Detail: "generator " + label}
	}
	if md.TypeParameters != nil && len(md.TypeParameters.Nodes) > 0 {
		return &Reason{Code: reasonGenericFunc, Detail: "generic " + label}
	}
	if md.Body == nil {
		return &Reason{Code: reasonMissingBody, Detail: label + "has no body (overload / abstract)"}
	}
	if md.PostfixToken != nil && md.PostfixToken.Kind == ast.KindQuestionToken {
		return &Reason{Code: reasonUnhandledKind, Detail: "optional " + label}
	}

	paramNames, jsFuncParams, reason := checkCallableSignature(ck, m, md.Parameters, label)
	if reason != nil {
		return reason
	}

	ctx := &bodyCtx{
		ck:               ck,
		paramNames:       paramNames,
		jsFuncParamNames: jsFuncParams,
		topLevelFuncs:    topLevelFuncs,
		staticMethods:    staticMethods,
		localNames:       map[string]bool{},
		inAsync:          ast.HasSyntacticModifier(m, ast.ModifierFlagsAsync),
	}
	if !isStatic {
		ctx.inMethod = true
		ctx.thisFields = thisFields
		ctx.thisMethods = thisMethods
	}
	return checkBody(md.Body, ctx)
}
