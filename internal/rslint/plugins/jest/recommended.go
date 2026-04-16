package jest

import (
	"github.com/i2y/ramune/internal/rslint/plugins/jest/rules/no_alias_methods"
	"github.com/i2y/ramune/internal/rslint/plugins/jest/rules/no_disabled_tests"
	"github.com/i2y/ramune/internal/rslint/plugins/jest/rules/no_focused_tests"
	"github.com/i2y/ramune/internal/rslint/plugins/jest/rules/no_test_prefixes"
	"github.com/i2y/ramune/internal/rslint/plugins/jest/rules/prefer_to_have_length"
	"github.com/i2y/ramune/internal/rslint/plugins/jest/rules/valid_describe_callback"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetRecommendedRules() []rule.Rule {
	return []rule.Rule{
		no_alias_methods.NoAliasMethodsRule,
		no_disabled_tests.NoDisabledTestsRule,
		no_focused_tests.NoFocusedTestsRule,
		no_test_prefixes.NoTestPrefixesRule,
		prefer_to_have_length.PreferToHaveLengthRule,
		valid_describe_callback.ValidDescribeCallbackRule,
	}
}
