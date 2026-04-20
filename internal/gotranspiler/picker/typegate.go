package picker

import (
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
	reasonAsyncFunc      = "async-func"
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

// isExtractableType returns ok=true when t is a v1-extractable type.
// v1 accepts only primitives (number, string, boolean) and void.
// null-only and undefined-only types are also rejected in v1.
func isExtractableType(t *checker.Type) (ok bool, reason Reason) {
	if t == nil {
		return false, Reason{Code: reasonAnyType, Detail: "nil type"}
	}
	flags := t.Flags()

	// Hard bailouts first.
	if flags&checker.TypeFlagsAny != 0 {
		return false, Reason{Code: reasonAnyType, Detail: "type is `any`"}
	}
	if flags&checker.TypeFlagsUnknown != 0 {
		return false, Reason{Code: reasonUnknownType, Detail: "type is `unknown`"}
	}
	if flags&checker.TypeFlagsTypeParameter != 0 {
		return false, Reason{Code: reasonGenericType, Detail: "type is a generic parameter"}
	}
	if flags&checker.TypeFlagsBigIntLike != 0 {
		return false, Reason{Code: reasonBigInt, Detail: "bigint not supported in v1"}
	}
	if flags&checker.TypeFlagsESSymbolLike != 0 {
		return false, Reason{Code: reasonSymbol, Detail: "symbol not supported in v1"}
	}
	if flags&checker.TypeFlagsIntersection != 0 {
		return false, Reason{Code: reasonIntersection, Detail: "intersection type"}
	}

	// Accept primitives & void.
	if flags&checker.TypeFlagsStringLike != 0 {
		return true, Reason{}
	}
	if flags&checker.TypeFlagsNumberLike != 0 {
		return true, Reason{}
	}
	if flags&checker.TypeFlagsBooleanLike != 0 {
		return true, Reason{}
	}
	if flags&checker.TypeFlagsVoidLike != 0 {
		return true, Reason{}
	}

	// v1 rejects unions (including T | null) to keep the predicate airtight.
	// v1.2+ will accept `T | null` / `T | undefined` with extractable T.
	if flags&checker.TypeFlagsUnion != 0 {
		return false, Reason{Code: reasonUnionType, Detail: "union type not supported in v1"}
	}

	// Object / reference types (arrays, Promise, user structs, function types).
	// v1 rejects all of these. Later versions will allow a subset.
	if flags&checker.TypeFlagsObject != 0 {
		return false, Reason{Code: reasonObjectType, Detail: "object/reference type not supported in v1"}
	}

	return false, Reason{Code: reasonUnhandledKind, Detail: "unclassified type"}
}
