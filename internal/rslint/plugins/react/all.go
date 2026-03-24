package react_plugin

import (
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_boolean_value"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_closing_tag_location"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_equals_spacing"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_filename_extension"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_first_prop_new_line"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_max_props_per_line"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_props_no_multi_spaces"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_uses_react"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_uses_vars"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/jsx_wrap_multilines"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/react_in_jsx_scope"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/self_closing_comp"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/style_prop_object"
	"github.com/i2y/ramune/internal/rslint/plugins/react/rules/void_dom_elements_no_children"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		jsx_boolean_value.JsxBooleanValueRule,
		jsx_closing_tag_location.JsxClosingTagLocationRule,
		jsx_equals_spacing.JsxEqualsSpacingRule,
		jsx_filename_extension.JsxFilenameExtensionRule,
		jsx_first_prop_new_line.JsxFirstPropNewLineRule,
		jsx_max_props_per_line.JsxMaxPropsPerLineRule,
		jsx_props_no_multi_spaces.JsxPropsNoMultiSpacesRule,
		jsx_uses_react.JsxUsesReactRule,
		jsx_uses_vars.JsxUsesVarsRule,
		jsx_wrap_multilines.JsxWrapMultilinesRule,
		react_in_jsx_scope.ReactInJsxScopeRule,
		self_closing_comp.SelfClosingCompRule,
		style_prop_object.StylePropObjectRule,
		void_dom_elements_no_children.VoidDomElementsNoChildrenRule,
	}
}
