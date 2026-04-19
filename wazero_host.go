//go:build qjswasm && !quickjs && !goja

package ramune

import "github.com/fastschema/qjs"

// codeOpt is a small helper that returns a fastschema EvalOption for
// executing JS source. Centralized so every evalXLocked call shares the
// same option construction cost (constant) rather than importing
// qjs.Code into every file.
func codeOpt(src string) qjs.EvalOptionFunc {
	return qjs.Code(src)
}
