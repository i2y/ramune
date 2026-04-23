//go:build !goja && !qjswasm

package main

import (
	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/tsgo/core"
)

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. JSC accepts modern JS, so we use ESNext.
func esbuildTarget() api.Target { return api.ESNext }

// tsgoTarget mirrors esbuildTarget for the tsgo-backed TS->JS path.
func tsgoTarget() core.ScriptTarget { return core.ScriptTargetESNext }
