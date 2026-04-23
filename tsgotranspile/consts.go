package tsgotranspile

import "github.com/i2y/ramune/internal/tsgo/core"

// Type aliases so external callers can construct Options without reaching
// into the vendored tsgo internals (which stay in internal/).
type (
	ScriptTarget = core.ScriptTarget
	ModuleKind   = core.ModuleKind
	JsxEmit      = core.JsxEmit
)

// Script target values. Only the ones Ramune actually uses are surfaced.
const (
	ScriptTargetES2017 ScriptTarget = core.ScriptTargetES2017
	ScriptTargetESNext ScriptTarget = core.ScriptTargetESNext
)

// Module kinds.
const (
	ModuleKindCommonJS ModuleKind = core.ModuleKindCommonJS
	ModuleKindESNext   ModuleKind = core.ModuleKindESNext
	ModuleKindPreserve ModuleKind = core.ModuleKindPreserve
)

// JSX emit modes.
const (
	JsxEmitNone        JsxEmit = core.JsxEmitNone
	JsxEmitPreserve    JsxEmit = core.JsxEmitPreserve
	JsxEmitReact       JsxEmit = core.JsxEmitReact
	JsxEmitReactJSX    JsxEmit = core.JsxEmitReactJSX
	JsxEmitReactJSXDev JsxEmit = core.JsxEmitReactJSXDev
)
