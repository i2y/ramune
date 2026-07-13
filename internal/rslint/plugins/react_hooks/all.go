package react_hooks

import (
	"github.com/i2y/ramune/internal/rslint/plugins/react_hooks/rules/exhaustive_deps"
	"github.com/i2y/ramune/internal/rslint/plugins/react_hooks/rules/rules_of_hooks"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		rules_of_hooks.RulesOfHooksRule,
		exhaustive_deps.ExhaustiveDepsRule,
	}
}
