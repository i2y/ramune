package prefer_strict_equal

import (
	"github.com/i2y/ramune/internal/rslint/shim/ast"
	"github.com/i2y/ramune/internal/rslint/shim/core"
	"github.com/i2y/ramune/internal/rslint/plugins/jest/utils"
	"github.com/i2y/ramune/internal/rslint/rule"
)

// Message Builders

func buildUseToStrictEqualErrorMessage() rule.RuleMessage {
	return rule.RuleMessage{
		Id:          "useToStrictEqual",
		Description: "Use `toStrictEqual()` instead",
	}
}

func buildSuggestReplaceWithStrictEqualErrorMessage() rule.RuleMessage {
	return rule.RuleMessage{
		Id:          "suggestReplaceWithStrictEqual",
		Description: "Replace with `toStrictEqual()`",
	}
}

var PreferStrictEqualRule = rule.Rule{
	Name: "jest/prefer-strict-equal",
	Run: func(ctx rule.RuleContext, options []any) rule.RuleListeners {
		return rule.RuleListeners{
			ast.KindCallExpression: func(node *ast.Node) {
				jestFnCall := utils.ParseJestFnCall(node, ctx)
				if jestFnCall == nil || jestFnCall.Kind != utils.JestFnTypeExpect {
					return
				}

				MemberEntries := jestFnCall.MemberEntries
				if len(MemberEntries) == 0 {
					return
				}

				for _, memberEntry := range MemberEntries {
					kind := memberEntry.Node.Kind
					if kind != ast.KindIdentifier && kind != ast.KindStringLiteral && kind != ast.KindNoSubstitutionTemplateLiteral {
						continue
					}

					if memberEntry.Name != "toEqual" {
						continue
					}

					ctx.ReportNodeWithSuggestions(
						memberEntry.Node,
						buildUseToStrictEqualErrorMessage(),
						rule.RuleSuggestion{
							Message: buildSuggestReplaceWithStrictEqualErrorMessage(),
							FixesArr: []rule.RuleFix{
								{
									Range: core.NewTextRange(memberEntry.Node.Pos(), memberEntry.Node.End()),
									Text: func() string {
										if memberEntry.Node.Kind == ast.KindStringLiteral {
											return "'toStrictEqual'"
										}
										if memberEntry.Node.Kind == ast.KindNoSubstitutionTemplateLiteral {
											return "`toStrictEqual`"
										}
										return "toStrictEqual"
									}(),
								},
							},
						},
					)
				}
			},
		}
	},
}
