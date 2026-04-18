//go:build !goja && !qjswasm

package main

import "github.com/evanw/esbuild/pkg/api"

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. JSC and QuickJS both accept modern JS, so we use ESNext.
func esbuildTarget() api.Target { return api.ESNext }
