//go:build qjswasm && !goja

package ramune

import "github.com/evanw/esbuild/pkg/api"

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. QuickJS-NG supports the full ES2023 surface so we keep modern
// JS intact just like the JSC and modernc QuickJS backends.
func esbuildTarget() api.Target { return api.ESNext }
