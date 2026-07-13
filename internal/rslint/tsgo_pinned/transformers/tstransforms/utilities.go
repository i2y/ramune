package tstransforms

import (
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/ast"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/jsnum"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/printer"
)

func constantExpression(value any, factory *printer.NodeFactory) *ast.Expression {
	switch value := value.(type) {
	case string:
		return factory.NewStringLiteral(value, ast.TokenFlagsNone)
	case jsnum.Number:
		if value.IsInf() {
			if value > 0 {
				return factory.NewIdentifier("Infinity")
			}
			return factory.NewPrefixUnaryExpression(ast.KindMinusToken, factory.NewIdentifier("Infinity"))
		}
		if value.IsNaN() {
			return factory.NewIdentifier("NaN")
		}
		if value < 0 {
			return factory.NewPrefixUnaryExpression(ast.KindMinusToken, constantExpression(-value, factory))
		}
		return factory.NewNumericLiteral(value.String(), ast.TokenFlagsNone)
	}
	return nil
}
