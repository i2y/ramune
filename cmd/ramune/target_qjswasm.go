//go:build qjswasm && !goja

package main

import "github.com/evanw/esbuild/pkg/api"

// esbuildTarget returns ESNext for qjswasm (QuickJS-NG supports ES2023).
func esbuildTarget() api.Target { return api.ESNext }
