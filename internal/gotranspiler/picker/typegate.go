package picker

import (
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
			if elem.Flags()&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike) == 0 {
				return &Reason{Code: reasonObjectType, Detail: "array element must be primitive in v1"}
			}
			return nil
		}
		if inner := ck.GetPromisedTypeOfPromise(t); inner != nil {
			// Promise<T> bridges via *promise.Promise[T] on all backends.
			// Restrict the resolved type to primitives so the bridge stays
			// in the JSON-clean lane.
			if inner.Flags()&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike) == 0 {
				return &Reason{Code: reasonObjectType, Detail: "Promise<T>: T must be primitive in v1"}
			}
			return nil
		}
		return &Reason{Code: reasonObjectType, Detail: "object/reference type not supported in v1"}
	}
	return &Reason{Code: reasonUnhandledKind, Detail: "unclassified type"}
}

func isPrimitiveOrVoid(flags checker.TypeFlags) bool {
	return flags&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike|checker.TypeFlagsVoidLike) != 0
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
// nil ck or nil type both yield false (cannot prove numeric -> reject).
func isNumberLikeNode(ck *checker.Checker, n *ast.Node) bool {
	if ck == nil || n == nil {
		return false
	}
	t := ck.GetTypeAtLocation(n)
	return t != nil && t.Flags()&checker.TypeFlagsNumberLike != 0
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
