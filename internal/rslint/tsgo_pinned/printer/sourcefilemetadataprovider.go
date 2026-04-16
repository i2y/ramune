package printer

import (
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/ast"
	"github.com/i2y/ramune/internal/rslint/tsgo_pinned/tspath"
)

type SourceFileMetaDataProvider interface {
	GetSourceFileMetaData(path tspath.Path) *ast.SourceFileMetaData
}
