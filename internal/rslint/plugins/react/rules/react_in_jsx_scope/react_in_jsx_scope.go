package react_in_jsx_scope

import (
	"github.com/i2y/ramune/internal/rslint/rule"
)

// ReactInJsxScopeRule ensures React is in scope when using JSX.
//
// This rule is implemented as a no-op because it depends on ESLint's scope analysis.
// In rslint with TypeChecker, TypeScript already catches missing React imports
// via ts(2304) "Cannot find name 'React'", making this rule redundant.
var ReactInJsxScopeRule = rule.Rule{
	Name: "react/react-in-jsx-scope",
	Run: func(ctx rule.RuleContext, options []any) rule.RuleListeners {
		return rule.RuleListeners{}
	},
}
