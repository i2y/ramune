//go:build qjswasm && !goja

package main

import (
	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/tsgo/core"
)

// esbuildTarget returns ESNext for qjswasm (QuickJS-NG supports ES2023).
func esbuildTarget() api.Target { return api.ESNext }

// tsgoTarget mirrors esbuildTarget for the tsgo-backed TS->JS path.
func tsgoTarget() core.ScriptTarget { return core.ScriptTargetESNext }
