package picker

import (
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/checker"
)

// Reason codes. Stable identifiers for scripting / grep use.
const (
	reasonAnyType        = "any-type"
	reasonUnknownType    = "unknown-type"
	reasonGenericType    = "generic-type-param"
	reasonUnionType      = "wide-union"
	reasonIntersection   = "intersection"
	reasonBigInt         = "bigint"
	reasonSymbol         = "symbol"
	reasonObjectType     = "object-type"
	reasonEmptyReturn    = "no-signature"
	reasonRestParam      = "rest-param"
	reasonGeneratorFunc  = "generator-func"
	reasonGenericFunc    = "generic-func"
	reasonUnnamed        = "unnamed"
	reasonMissingBody    = "missing-body"
	reasonClosureCapture = "closure-capture"
	reasonMutParam       = "mutates-parameter"
	reasonThis           = "this-keyword"
	reasonUnhandledKind  = "unhandled-ast-kind"
	reasonForbiddenOp    = "forbidden-operator"
	reasonBuiltinCall    = "builtin-call"
	reasonDynamicCallee  = "dynamic-callee"
	reasonLabeledStmt    = "labeled-statement"
	reasonRegex          = "regex-literal"
	reasonTry            = "try-catch"
	reasonThrow          = "throw-statement"
	reasonSpread         = "spread-element"
	reasonYield          = "yield-expression"
	reasonAwait          = "await-expression"
	reasonFuncLiteral    = "inline-function-literal"
	reasonClassHeritage  = "class-heritage"
	reasonClassStatic    = "class-static"
	reasonClassAccessor  = "class-accessor"
	reasonClassPrivate   = "class-private"
	reasonClassDecorator = "class-decorator"
	reasonClassParamProp = "class-param-property"
	reasonJSFuncSig      = "jsfunc-signature"
)

// isExtractableType returns nil when t is an extractable type, else a
// Reason describing the rejection. Accepts:
//   - primitives (number, string, boolean) and void
//   - Array<T> / ReadonlyArray<T> / T[] where T is itself extractable
//
// Unions (including `T | null`), tuples, objects, generics, Map/Set/Promise,
// and everything else bail with a named Reason code.
func isExtractableType(ck *checker.Checker, t *checker.Type) *Reason {
	return isExtractableTypeWith(ck, t, nil)
}

// isExtractableTypeWith is the cycle-safe inner form. visited carries the
// set of named struct/interface types currently on the recursion stack;
// re-entry rejects with reasonObjectType because Go's type system has no
// direct mutual recursion — the emitted `type A struct { B B }` would
// fail with "invalid recursive type" without pointer indirection that
// the field walker doesn't currently insert. Without this guard the
// recursion blew the goroutine stack outright.
func isExtractableTypeWith(ck *checker.Checker, t *checker.Type, visited map[*checker.Type]bool) *Reason {
	if t == nil {
		return &Reason{Code: reasonAnyType, Detail: "nil type"}
	}
	flags := t.Flags()

	if flags&checker.TypeFlagsAny != 0 {
		return &Reason{Code: reasonAnyType, Detail: "type is `any`"}
	}
	if flags&checker.TypeFlagsUnknown != 0 {
		return &Reason{Code: reasonUnknownType, Detail: "type is `unknown`"}
	}
	if flags&checker.TypeFlagsTypeParameter != 0 {
		return &Reason{Code: reasonGenericType, Detail: "type is a generic parameter (try: wrap in a non-generic helper for the concrete type you actually use)"}
	}
	if flags&checker.TypeFlagsBigIntLike != 0 {
		return &Reason{Code: reasonBigInt, Detail: "bigint not supported (try: use number / float64 if the value range fits)"}
	}
	if flags&checker.TypeFlagsESSymbolLike != 0 {
		return &Reason{Code: reasonSymbol, Detail: "symbol not supported"}
	}
	if flags&checker.TypeFlagsIntersection != 0 {
		return &Reason{Code: reasonIntersection, Detail: "intersection type (try: define a named interface that flattens both members' fields)"}
	}
	if isPrimitiveOrVoid(flags) {
		return nil
	}
	if flags&checker.TypeFlagsUnion != 0 {
		// Accept the two union shapes the body walker can lower without
		// runtime tag dispatch: nullable single-arm (typemapper → `*T`)
		// and uniform-primitive literal sets (typemapper → bare `string`
		// / `float64` / `bool`). Wider unions keep the rejection.
		if union := t.AsUnionType(); union != nil {
			var nonNullable []*checker.Type
			for _, u := range union.Types() {
				if u != nil && u.Flags()&checker.TypeFlagsNullable != 0 {
					continue
				}
				nonNullable = append(nonNullable, u)
			}
			if len(nonNullable) == 1 {
				if r := isExtractableTypeWith(ck, nonNullable[0], visited); r != nil {
					return r
				}
				return nil
			}
			if len(nonNullable) >= 2 && unionShareSinglePrimitive(nonNullable) {
				return nil
			}
		}
		return &Reason{Code: reasonUnionType, Detail: "union type not supported (try: T | null / T | undefined / uniform-primitive literal unions are accepted; for `string | number` etc., split into separately-typed functions)"}
	}
	if flags&checker.TypeFlagsObject != 0 {
		if elem := arrayElementType(ck, t); elem != nil {
			// Restricting element to primitives keeps the body walker's
			// single-level `arr[i]` pattern sound; nested arrays would need
			// access-pattern support the walker does not yet have.
			if !isPrimitiveType(elem.Flags()) {
				return &Reason{Code: reasonObjectType, Detail: "array element must be primitive (try: empty `[]` literals lose their type — push instead of declaring an empty literal, or use `Array.from({length: n}, ...)`; nested arrays are unsupported)"}
			}
			return nil
		}
		if k, v := mapKeyValueType(ck, t); k != nil && v != nil {
			// `Map<string, V>` lowers to `map[string]V` — typemapper
			// already emits the type, the body walker accepts the
			// instance methods (set/get/has/delete/size). Restricting
			// to string keys + primitive values mirrors the same
			// soundness budget the array case uses: avoids hashing a
			// Go-incompatible key type and keeps value reads
			// type-stable.
			if k.Flags()&checker.TypeFlagsStringLike == 0 {
				return &Reason{Code: reasonObjectType, Detail: "Map key must be string (try: convert keys with String() / .toString() before insertion)"}
			}
			if !isPrimitiveType(v.Flags()) {
				return &Reason{Code: reasonObjectType, Detail: "Map value must be primitive (nested Maps and struct values not yet accepted)"}
			}
			return nil
		}
		if elem := setElementType(ck, t); elem != nil {
			// `Set<T>` lowers to `map[T]struct{}` — same hashability
			// budget as Map. Restricting T to primitive keeps the key
			// comparable in Go and avoids the JS-side `===` vs Go
			// equality divergence for object keys.
			if !isPrimitiveType(elem.Flags()) {
				return &Reason{Code: reasonObjectType, Detail: "Set element must be primitive (struct or nested Set values not yet accepted)"}
			}
			return nil
		}
		if isExtractableObjectTypeWith(ck, t, visited) {
			return nil
		}
		if ck.GetPromisedTypeOfPromise(t) != nil {
			// Promise<T> is only acceptable in function-return position
			// (handled by isExtractableReturnType). As a param/local/field
			// type the JS->Go bridge has no way to materialise a Go
			// *promise.Promise[T] from a JS Promise, so awaiting it yields
			// the float64 zero value at runtime.
			return &Reason{Code: reasonObjectType, Detail: "Promise<T> only allowed as function return type"}
		}
		return &Reason{Code: reasonObjectType, Detail: "object/reference type not supported (try: extract anonymous `{...}` into a named `interface`; `Map<K,V>` / `Set<T>` / tuples not yet accepted)"}
	}
	return &Reason{Code: reasonUnhandledKind, Detail: "unclassified type"}
}

func isPrimitiveType(flags checker.TypeFlags) bool {
	return flags&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike) != 0
}

// unionShareSinglePrimitive reports whether every type in the union shares
// a single primitive base — `"up" | "down"` (StringLike), `1 | 2 | 3`
// (NumberLike), `true | false` (BooleanLike). Mirrors typemapper.go's
// allString/allNumber/allBool gating: typemapper unconditionally lowers
// these to bare `string`/`float64`/`bool`, so the picker can accept the
// shape without the body walker doing any extra narrowing.
func unionShareSinglePrimitive(arms []*checker.Type) bool {
	if len(arms) == 0 {
		return false
	}
	const (
		strMask  = checker.TypeFlagsStringLike
		numMask  = checker.TypeFlagsNumberLike
		boolMask = checker.TypeFlagsBooleanLike
	)
	allStr, allNum, allBool := true, true, true
	for _, t := range arms {
		f := t.Flags()
		allStr = allStr && f&strMask != 0
		allNum = allNum && f&numMask != 0
		allBool = allBool && f&boolMask != 0
	}
	return allStr || allNum || allBool
}

func isPrimitiveOrVoid(flags checker.TypeFlags) bool {
	return isPrimitiveType(flags) || flags&checker.TypeFlagsVoidLike != 0
}

// isBoolLikeType / isNumberLikeType / isStringLikeType are *checker.Type
// predicates suitable for passing to checkExprWithType. nil-safe.
func isBoolLikeType(t *checker.Type) bool {
	return t != nil && t.Flags()&checker.TypeFlagsBooleanLike != 0
}

func isNumberLikeType(t *checker.Type) bool {
	return t != nil && t.Flags()&checker.TypeFlagsNumberLike != 0
}

func isStringLikeType(t *checker.Type) bool {
	return t != nil && t.Flags()&checker.TypeFlagsStringLike != 0
}

// isNumberLikeNode returns true when ck reports a NumberLike type for n.
// nil ck or nil n both yield false (cannot prove numeric -> reject).
func isNumberLikeNode(ck *checker.Checker, n *ast.Node) bool {
	if ck == nil || n == nil {
		return false
	}
	return isNumberLikeType(ck.GetTypeAtLocation(n))
}

// isExtractableReturnType is a superset of isExtractableType that additionally
// accepts `Promise<T>` (T extractable). Promise return values bridge to JS
// Promise via *promise.Promise[T]; the reverse direction (JS Promise -> Go
// *promise.Promise[T]) is not implemented in the runtime, so other positions
// (param/local/field) reject Promise.
func isExtractableReturnType(ck *checker.Checker, t *checker.Type) *Reason {
	if t != nil && t.Flags()&checker.TypeFlagsObject != 0 {
		if inner := ck.GetPromisedTypeOfPromise(t); inner != nil {
			if !isPrimitiveType(inner.Flags()) {
				return &Reason{Code: reasonObjectType, Detail: "Promise<T>: T must be primitive"}
			}
			return nil
		}
	}
	return isExtractableType(ck, t)
}

// isExtractableObjectType returns true when t is a named interface or type
// alias whose every property is a primitive (number/string/boolean) and that
// has no callable, constructible, or index signatures. The emitter renders
// such types as Go structs with JSON tags, which the NativeModuleFromFuncs
// bridge round-trips through field-by-field reconstruction.
//
// Anonymous inline `{ a: number }` types are excluded - they have no
// declared name and the emitter falls back to the `jsrt.Obj` reflection
// path. The detection: anonymous types are flagged ObjectFlagsAnonymous;
// reference types (Array, Promise, Map, Set, etc.) carry ObjectFlagsReference
// and are handled by their own callers above.
func isExtractableObjectType(ck *checker.Checker, t *checker.Type) bool {
	return isExtractableObjectTypeWith(ck, t, nil)
}

func isExtractableObjectTypeWith(ck *checker.Checker, t *checker.Type, visited map[*checker.Type]bool) bool {
	if ck == nil || t == nil {
		return false
	}
	objFlags := t.ObjectFlags()
	if objFlags&checker.ObjectFlagsAnonymous != 0 || objFlags&checker.ObjectFlagsReference != 0 {
		return false
	}
	sym := t.Symbol()
	if sym == nil {
		return false
	}
	name := sym.Name
	if name == "" || strings.HasPrefix(name, "__") {
		return false
	}
	if len(ck.GetSignaturesOfType(t, checker.SignatureKindCall)) > 0 {
		return false
	}
	if len(ck.GetSignaturesOfType(t, checker.SignatureKindConstruct)) > 0 {
		return false
	}
	if len(ck.GetIndexInfosOfType(t)) > 0 {
		return false
	}
	props := ck.GetPropertiesOfType(t)
	if len(props) == 0 {
		return false
	}
	// Mark t in-progress before recursing so a cycle (`A.b: B; B.a: A`)
	// is rejected before the goroutine stack blows. Could be lifted if
	// the emitter learned to insert `*` indirection for recursive
	// fields, but the JSON bridge and field-access walker both assume
	// flat structs today.
	if visited == nil {
		visited = map[*checker.Type]bool{}
	}
	if visited[t] {
		return false
	}
	visited[t] = true
	for _, p := range props {
		pt := ck.GetTypeOfSymbol(p)
		if pt == nil {
			return false
		}
		if r := isExtractableTypeWith(ck, pt, visited); r != nil {
			return false
		}
	}
	return true
}

// arrayElementType returns T when t is `Array<T>` / `ReadonlyArray<T>` /
// `T[]`, else nil. Delegates to the checker's canonical predicate (identity
// check against globalArrayType / globalReadonlyArrayType), which avoids the
// false positive of a user type literally named `Array`.
func arrayElementType(ck *checker.Checker, t *checker.Type) *checker.Type {
	if ck == nil || t == nil {
		return nil
	}
	return ck.GetElementTypeOfArrayType(t)
}

// mapKeyValueType returns (K, V) when t is `Map<K, V>` (the global
// stdlib reference type), else (nil, nil). Identifies via the target's
// symbol name `Map` plus a Reference object-flag — same shape the
// typemapper's reference-type lowering checks before emitting
// `map[K]V`. A user type literally named `Map` would have no
// type-args and fail the length check below.
func mapKeyValueType(ck *checker.Checker, t *checker.Type) (*checker.Type, *checker.Type) {
	if ck == nil || t == nil {
		return nil, nil
	}
	if t.ObjectFlags()&checker.ObjectFlagsReference == 0 {
		return nil, nil
	}
	target := t.Target()
	if target == nil || target.Symbol() == nil {
		return nil, nil
	}
	if target.Symbol().Name != "Map" {
		return nil, nil
	}
	args := ck.GetTypeArguments(t)
	if len(args) < 2 {
		return nil, nil
	}
	return args[0], args[1]
}

// setElementType returns T when t is the global `Set<T>`, else nil.
// Mirrors mapKeyValueType — the typemapper already lowers `Set<T>` to
// `map[T]struct{}` in goObjectType, so the picker just needs the same
// detection shape to gate the body walker on accepted Set methods.
func setElementType(ck *checker.Checker, t *checker.Type) *checker.Type {
	if ck == nil || t == nil {
		return nil
	}
	if t.ObjectFlags()&checker.ObjectFlagsReference == 0 {
		return nil
	}
	target := t.Target()
	if target == nil || target.Symbol() == nil {
		return nil
	}
	if target.Symbol().Name != "Set" {
		return nil
	}
	args := ck.GetTypeArguments(t)
	if len(args) < 1 {
		return nil
	}
	return args[0]
}

// isJSFuncParamType returns nil when t is a "plain" callable type suitable
// to be received as a *ramune.JSFunc callback: exactly one call signature,
// no rest/default/optional parameters, and every param + return type is
// itself extractable (primitive / T[] / named interface). Used only in
// parameter position — JSFunc cannot round-trip as a local/field/return,
// because the JS<-Go side of the bridge has no way to materialise a
// *ramune.JSFunc out of a returned Go value.
//
// This is intentionally stricter than "anything callable": nesting a JSFunc
// inside another JSFunc's signature would require the emitter to generate
// double-indirected Call wrapping, and TS Function (no call sig) / tuples
// with callable elements have no sound lowering either.
func isJSFuncParamType(ck *checker.Checker, t *checker.Type) *Reason {
	if ck == nil || t == nil {
		return &Reason{Code: reasonJSFuncSig, Detail: "nil type"}
	}
	if t.Flags()&checker.TypeFlagsObject == 0 {
		return &Reason{Code: reasonJSFuncSig, Detail: "not an object type"}
	}
	sigs := ck.GetSignaturesOfType(t, checker.SignatureKindCall)
	if len(sigs) == 0 {
		return &Reason{Code: reasonJSFuncSig, Detail: "no call signature"}
	}
	if len(sigs) > 1 {
		return &Reason{Code: reasonJSFuncSig, Detail: "overloaded callable not supported"}
	}
	if len(ck.GetSignaturesOfType(t, checker.SignatureKindConstruct)) > 0 {
		return &Reason{Code: reasonJSFuncSig, Detail: "constructor signature not supported"}
	}
	sig := sigs[0]
	if ck.HasEffectiveRestParameter(sig) {
		return &Reason{Code: reasonRestParam, Detail: "callback rest parameter not supported"}
	}
	for _, paramSym := range sig.Parameters() {
		if paramSym == nil {
			continue
		}
		pt := ck.GetTypeOfSymbol(paramSym)
		// Nested callable params (callback-of-callback) would require a
		// double-indirected Call wrapping the emitter doesn't generate.
		if pt != nil && pt.Flags()&checker.TypeFlagsObject != 0 {
			if len(ck.GetSignaturesOfType(pt, checker.SignatureKindCall)) > 0 {
				return &Reason{Code: reasonObjectType, Detail: "callback parameter `" + paramSym.Name + "` itself is callable"}
			}
		}
		if r := isExtractableType(ck, pt); r != nil {
			return &Reason{Code: r.Code, Detail: "callback param " + paramSym.Name + ": " + r.Detail}
		}
	}
	ret := ck.GetReturnTypeOfSignature(sig)
	if ret != nil && ret.Flags()&checker.TypeFlagsObject != 0 {
		if len(ck.GetSignaturesOfType(ret, checker.SignatureKindCall)) > 0 {
			return &Reason{Code: reasonObjectType, Detail: "callback return is callable"}
		}
	}
	if r := isExtractableType(ck, ret); r != nil {
		return &Reason{Code: r.Code, Detail: "callback return: " + r.Detail}
	}
	return nil
}
