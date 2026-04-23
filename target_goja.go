//go:build goja

package ramune

import (
	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/tsgo/core"
)

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. Goja is nearly complete through ES2017 (async/await, async arrow
// functions, exponentiation); features added in ES2018+ (spread in object
// literals, private class fields, top-level await, regex named groups) are
// lowered by esbuild.
func esbuildTarget() api.Target { return api.ES2017 }

// tsgoTarget mirrors esbuildTarget for the tsgo-backed TS->JS path.
func tsgoTarget() core.ScriptTarget { return core.ScriptTargetES2017 }
