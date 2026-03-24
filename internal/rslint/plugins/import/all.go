package import_plugin

import (
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_self_import"
	"github.com/i2y/ramune/internal/rslint/plugins/import/rules/no_webpack_loader_syntax"
	"github.com/i2y/ramune/internal/rslint/rule"
)

func GetAllRules() []rule.Rule {
	return []rule.Rule{
		no_self_import.NoSelfImportRule,
		no_webpack_loader_syntax.NoWebpackLoaderSyntax,
	}
}
