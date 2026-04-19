//go:build goja && qjswasm

package ramune

// Setting both -tags goja and -tags qjswasm is a contradiction — pick one.
const _backendConflict = "cannot build with both -tags goja and -tags qjswasm"
