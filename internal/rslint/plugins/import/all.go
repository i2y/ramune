package import_plugin

import (
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/first"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/newline_after_import"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_duplicates"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_mutable_exports"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_self_import"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_webpack_loader_syntax"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		first.FirstRule,
		newline_after_import.NewlineAfterImportRule,
		no_duplicates.NoDuplicatesRule,
		no_mutable_exports.NoMutableExportsRule,
		no_self_import.NoSelfImportRule,
		no_webpack_loader_syntax.NoWebpackLoaderSyntax,
	}
}
