//go:build nowebview

package ramune

import "errors"

// When built with `-tags nowebview`, the WebView bindings (and their
// glaze/purego transitive dependency) are excluded. This is useful for
// binaries that link other FFI stacks such as goffi-based GUIs, which
// would otherwise clash on purego's internal fakecgo symbols.

var webViewMainCh chan func()

var errWebViewNotEnabled = errors.New("WebView disabled at build time (-tags nowebview)")

// InitWebViewMain is a no-op in the nowebview build. Kept for API
// compatibility with cmd/ramune's main routine.
func InitWebViewMain() {}

// DrainWebViewMain blocks until done is closed, emulating the real drain
// so the main goroutine still parks the way callers expect.
func DrainWebViewMain(done <-chan struct{}) { <-done }

type webviewManager struct{}

func newWebviewManager(_ func()) *webviewManager { return &webviewManager{} }

func (m *webviewManager) processEvents(*Runtime) {}
func (m *webviewManager) hasActive() bool        { return false }
func (m *webviewManager) closeAll()              {}

func (r *Runtime) installWebView() error { return nil }
