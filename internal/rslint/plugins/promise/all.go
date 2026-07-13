package promise_plugin

import (
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/always_return"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/avoid_new"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/catch_or_return"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_callback_in_promise"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_multiple_resolved"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_nesting"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_new_statics"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_promise_in_callback"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_return_in_finally"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/no_return_wrap"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/param_names"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/prefer_await_to_then"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/prefer_catch"
	"github.com/i2y/ramune/internal/rslint/plugins/promise/rules/valid_params"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		always_return.AlwaysReturnRule,
		avoid_new.AvoidNewRule,
		catch_or_return.CatchOrReturnRule,
		no_callback_in_promise.NoCallbackInPromiseRule,
		no_multiple_resolved.NoMultipleResolvedRule,
		no_nesting.NoNestingRule,
		no_new_statics.NoNewStaticsRule,
		no_promise_in_callback.NoPromiseInCallbackRule,
		no_return_in_finally.NoReturnInFinallyRule,
		no_return_wrap.NoReturnWrapRule,
		param_names.ParamNamesRule,
		prefer_await_to_then.PreferAwaitToThenRule,
		prefer_catch.PreferCatchRule,
		valid_params.ValidParamsRule,
	}
}
