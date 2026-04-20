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

// isExtractableType returns nil when t is a v1-extractable type, else a
// Reason describing the rejection. v1 accepts only primitives (number, string,
// boolean) and void; `T | null`, arrays, objects, generics, and everything
// else bail out with a named Reason code.
func isExtractableType(t *checker.Type) *Reason {
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
	if flags&(checker.TypeFlagsStringLike|checker.TypeFlagsNumberLike|checker.TypeFlagsBooleanLike|checker.TypeFlagsVoidLike) != 0 {
		return nil
	}
	if flags&checker.TypeFlagsUnion != 0 {
		return &Reason{Code: reasonUnionType, Detail: "union type not supported in v1"}
	}
	if flags&checker.TypeFlagsObject != 0 {
		return &Reason{Code: reasonObjectType, Detail: "object/reference type not supported in v1"}
	}
	return &Reason{Code: reasonUnhandledKind, Detail: "unclassified type"}
}
