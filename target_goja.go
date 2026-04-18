//go:build goja

package ramune

import "github.com/evanw/esbuild/pkg/api"

// esbuildTarget returns the esbuild JS target appropriate for the active
// backend. Goja is nearly complete through ES2017 (async/await, async arrow
// functions, exponentiation); features added in ES2018+ (spread in object
// literals, private class fields, top-level await, regex named groups) are
// lowered by esbuild.
func esbuildTarget() api.Target { return api.ES2017 }
