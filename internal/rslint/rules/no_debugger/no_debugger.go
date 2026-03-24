package no_debugger

import (
	"github.com/i2y/ramune/internal/rslint/rule"
	"github.com/i2y/ramune/internal/rslint/shim/ast"
)

// https://eslint.org/docs/latest/rules/no-debugger
var NoDebuggerRule = rule.Rule{
	Name: "no-debugger",
	Run: func(ctx rule.RuleContext, options any) rule.RuleListeners {
		return rule.RuleListeners{
			ast.KindDebuggerStatement: func(node *ast.Node) {
				ctx.ReportNode(node, rule.RuleMessage{
					Id:          "no-debugger",
					Description: "Unexpected 'debugger' statement.",
				})
			},
		}
	},
}
