//go:build !goja

package ramune

import "github.com/evanw/esbuild/pkg/api"

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. JSC and QuickJS both accept modern JS; goja needs lowering.
func esbuildTarget() api.Target { return api.ESNext }
