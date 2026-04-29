package ramune

import "github.com/i2y/ramune/jsbridge"

// Compile-time guarantee that the host's *JSFunc (whichever backend it
// was built against — JSC, goja, or qjswasm) satisfies jsbridge.Func.
// A host running ramune can therefore pass a *JSFunc value through to
// emitted Go that was compiled with `--backend tinygo` (which expects
// jsbridge.Func params) without an explicit adapter.
var _ jsbridge.Func = (*JSFunc)(nil)
