//go:build quickjs && goja

package ramune

// Setting both -tags quickjs and -tags goja is a contradiction.
// Pick one backend.
const _backendConflict = "cannot build with both -tags quickjs and -tags goja"
