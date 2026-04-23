//go:build goja

package main

import (
	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/tsgo/core"
)

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. Goja has complete ES5 and nearly complete ES2015+ coverage but
// does not implement every ES2022+ feature (private class fields, top-level
// await, static blocks, Object.hasOwn). Lowering to ES2017 is esbuild's
// highest-compat output that still keeps arrow functions, destructuring,
// async/await, etc. intact -- goja handles that subset cleanly.
func esbuildTarget() api.Target { return api.ES2017 }

// tsgoTarget mirrors esbuildTarget for the tsgo-backed TS->JS path.
func tsgoTarget() core.ScriptTarget { return core.ScriptTargetES2017 }
