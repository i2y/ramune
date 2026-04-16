package tsoptions

import (
	"sync"

	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/ast"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/core"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/locale"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/tspath"
)

type ParsedBuildCommandLine struct {
	BuildOptions    *core.BuildOptions    `json:"buildOptions"`
	CompilerOptions *core.CompilerOptions `json:"compilerOptions"`
	WatchOptions    *core.WatchOptions    `json:"watchOptions"`
	Projects        []string              `json:"projects"`
	Errors          []*ast.Diagnostic     `json:"errors"`
	Raw             any                   `json:"raw"`

	comparePathsOptions tspath.ComparePathsOptions

	resolvedProjectPaths     []string
	resolvedProjectPathsOnce sync.Once

	locale     locale.Locale
	localeOnce sync.Once
}

func (p *ParsedBuildCommandLine) ResolvedProjectPaths() []string {
	p.resolvedProjectPathsOnce.Do(func() {
		p.resolvedProjectPaths = core.Map(p.Projects, func(project string) string {
			return core.ResolveConfigFileNameOfProjectReference(
				tspath.ResolvePath(p.comparePathsOptions.CurrentDirectory, project),
			)
		})
	})
	return p.resolvedProjectPaths
}

func (p *ParsedBuildCommandLine) Locale() locale.Locale {
	p.localeOnce.Do(func() {
		p.locale, _ = locale.Parse(p.CompilerOptions.Locale)
	})
	return p.locale
}
