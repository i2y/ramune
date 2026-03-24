package sourcemap

import "github.com/i2y/ramune/internal/tsgo/core"

type Source interface {
	Text() string
	FileName() string
	ECMALineMap() []core.TextPos
}
