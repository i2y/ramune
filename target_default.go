//go:build !goja && !qjswasm

package ramune

import "github.com/evanw/esbuild/pkg/api"

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. JSC accepts modern JS; goja needs lowering.
func esbuildTarget() api.Target { return api.ESNext }
