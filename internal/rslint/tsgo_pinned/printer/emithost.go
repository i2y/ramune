package printer

import (
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/ast"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/core"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/tsoptions"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/tspath"
)

// NOTE: EmitHost operations must be thread-safe
type EmitHost interface {
	Options() *core.CompilerOptions
	SourceFiles() []*ast.SourceFile
	UseCaseSensitiveFileNames() bool
	GetCurrentDirectory() string
	CommonSourceDirectory() string
	IsEmitBlocked(file string) bool
	WriteFile(fileName string, text string) error
	GetEmitModuleFormatOfFile(file ast.HasFileName) core.ModuleKind
	GetEmitResolver() EmitResolver
	GetProjectReferenceFromSource(path tspath.Path) *tsoptions.SourceOutputAndProjectReference
	IsSourceFileFromExternalLibrary(file *ast.SourceFile) bool
}
