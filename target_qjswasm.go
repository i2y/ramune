//go:build qjswasm && !goja

package ramune

import (
	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/tsgo/core"
)

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. QuickJS-NG supports the full ES2023 surface so we keep modern
// JS intact just like the JSC and modernc QuickJS backends.
func esbuildTarget() api.Target { return api.ESNext }

// tsgoTarget mirrors esbuildTarget for the tsgo-backed TS->JS path.
func tsgoTarget() core.ScriptTarget { return core.ScriptTargetESNext }
