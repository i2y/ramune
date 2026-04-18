//go:build (quickjs && qjswasm) || (goja && qjswasm)

package ramune

// Setting -tags qjswasm alongside -tags quickjs or -tags goja is a
// contradiction. Pick one backend.
const _qjswasmBackendConflict = "cannot combine -tags qjswasm with -tags quickjs or -tags goja"
