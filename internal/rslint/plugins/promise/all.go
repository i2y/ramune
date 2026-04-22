package promise_plugin

import (
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/param_names"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		param_names.ParamNamesRule,
	}
}
