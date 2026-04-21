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
	reasonJSFuncNested   = "jsfunc-nested-callable"
	reasonJSFuncSig      = "jsfunc-signature"
)

// isExtractableType returns nil when t is a v1-extractable type, else a
// Reason describing the rejection. Accepts:
//   - primitives (number, string, boolean) and void
//   - Array<T> / ReadonlyArray<T> / T[] where T is itself extractable
//
// Unions (including `T | null`), tuples, objects, generics, Map/Set/Promise,
// and everything else bail with a named Reason code.
func isExtractableType(ck *checker.Checker, t *checker.Type) *Reason {
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
		return &Reason{Code: reasonGenericType, Detail: "type is a generic parameter"}
	}
	if flags&checker.TypeFlagsBigIntLike != 0 {
		return &Reason{Code: reasonBigInt, Detail: "bigint not supported in v1"}
	}
	if flags&checker.TypeFlagsESSymbolLike != 0 {
		return &Reason{Code: reasonSymbol, Detail: "symbol not supported in v1"}
	}
	if flags&checker.TypeFlagsIntersection != 0 {
		return &Reason{Code: reasonIntersection, Detail: "intersection type"}
	}
	if isPrimitiveOrVoid(flags) {
		return nil
	}
	if flags&checker.TypeFlagsUnion != 0 {
		return &Reason{Code: reasonUnionType, Detail: "union type not supported in v1"}
	}
	if flags&checker.TypeFlagsObject != 0 {
		if elem := arrayElementType(ck, t); elem != nil {
			// Restricting element to primitives keeps the body walker's
			// single-level `arr[i]` pattern sound; nested arrays would need
			// access-pattern support the walker does not yet have.
			if !isPrimitiveType(elem.Flags()) {
				return &Reason{Code: reasonObjectType, Detail: "array element must be primitive in v1"}
			}
			return nil
		}
		if isExtractableObjectType(ck, t) {
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
		return &Reason{Code: reasonObjectType, Detail: "object/reference type not supported in v1"}
	}
	return &Reason{Code: reasonUnhandledKind, Detail: "unclassified type"}
}

func isPrimitiveType(flags checker.TypeFlags) bool {
	return flags&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike) != 0
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
				return &Reason{Code: reasonObjectType, Detail: "Promise<T>: T must be primitive in v1"}
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
	for _, p := range props {
		pt := ck.GetTypeOfSymbol(p)
		if pt == nil || !isPrimitiveType(pt.Flags()) {
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

// isJSFuncParamType returns nil when t is a "plain" callable type suitable
// to be received as a *ramune.JSFunc callback: exactly one call signature,
// no rest/default/optional parameters, and every param + return type is
// itself v1-extractable (primitive / T[] / named interface). Used only in
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
	for i, paramSym := range sig.Parameters() {
		if paramSym == nil {
			continue
		}
		pt := ck.GetTypeOfSymbol(paramSym)
		// A nested callable param (callback-of-callback) is rejected — keeps
		// the emitter's `.Call(arg.(T))` lowering single-level.
		if pt != nil && pt.Flags()&checker.TypeFlagsObject != 0 {
			if len(ck.GetSignaturesOfType(pt, checker.SignatureKindCall)) > 0 {
				return &Reason{Code: reasonJSFuncNested, Detail: "callback parameter itself is callable"}
			}
		}
		if r := isExtractableType(ck, pt); r != nil {
			return &Reason{Code: r.Code, Detail: "callback param " + paramSym.Name + ": " + r.Detail}
		}
		_ = i
	}
	ret := ck.GetReturnTypeOfSignature(sig)
	// Callback return is extractable OR void. Promise<T> as a callback return
	// is not wired — the IIFE lowering can't await a JS Promise synchronously.
	if ret != nil && ret.Flags()&checker.TypeFlagsObject != 0 {
		if len(ck.GetSignaturesOfType(ret, checker.SignatureKindCall)) > 0 {
			return &Reason{Code: reasonJSFuncNested, Detail: "callback return is callable"}
		}
	}
	if r := isExtractableType(ck, ret); r != nil {
		return &Reason{Code: r.Code, Detail: "callback return: " + r.Detail}
	}
	return nil
}
