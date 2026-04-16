package sourcemap

import "github.com/i2y/ramune/internal/rslint/tsgo_pinned/core"

type Source interface {
	Text() string
	FileName() string
	ECMALineMap() []core.TextPos
}
