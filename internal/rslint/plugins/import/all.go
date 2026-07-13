package import_plugin

import (
	default_rule "github.com/i2y/ramune/internal/rslint/plugins/import/rules/default"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/first"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/namespace"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/newline_after_import"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_cycle"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_default_export"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_duplicates"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_mutable_exports"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_self_import"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_webpack_loader_syntax"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		default_rule.DefaultRule,
		first.FirstRule,
		namespace.NamespaceRule,
		newline_after_import.NewlineAfterImportRule,
		no_cycle.NoCycleRule,
		no_default_export.NoDefaultExportRule,
		no_duplicates.NoDuplicatesRule,
		no_mutable_exports.NoMutableExportsRule,
		no_self_import.NoSelfImportRule,
		no_webpack_loader_syntax.NoWebpackLoaderSyntax,
	}
}
