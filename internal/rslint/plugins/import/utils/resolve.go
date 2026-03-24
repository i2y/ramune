package utils

import (
	"github.com/i2y/ramune/internal/rslint/shim/ast"

	"github.com/i2y/ramune/internal/rslint/rule"
)

func Resolve(moduleSpecifier *ast.StringLiteralLike, ctx rule.RuleContext) (string, bool) {
	module := ctx.Program.GetResolvedModuleFromModuleSpecifier(ctx.SourceFile, moduleSpecifier)

	if module != nil {
		return module.ResolvedFileName, true
	}

	return "", false
}
