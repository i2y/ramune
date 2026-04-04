package ramune

// GoFunc is a Go function that can be called from JavaScript.
// Arguments are converted to Go types: bool, float64, string, nil,
// map[string]any (for objects), []any (for arrays), or *JSFunc (for functions).
type GoFunc func(args []any) (any, error)

// GoFuncWithContext is a callback that receives a CallbackContext for safe
// engine access. Use RegisterFuncWithContext to register these.
type GoFuncWithContext func(ctx *CallbackContext, args []any) (any, error)

// CallbackContext provides safe access to the JS engine from within a GoFunc.
// Value methods like Attr() and Call() dispatch to the engine goroutine,
// which deadlocks inside a callback (already on the engine goroutine).
// CallbackContext calls engine functions directly and returns Go values.
type CallbackContext struct {
	rt *Runtime
}

// GCConfig configures garbage collection behavior for a Runtime.
type GCConfig struct {
	// DisableAutoGC disables Go's automatic GC while the HTTP server is
	// running. Manual GC is triggered every GCInterval requests.
	// Default: true (for HTTP server stability).
	DisableAutoGC bool

	// GCInterval is the number of HTTP requests between manual GC cycles.
	// Lower values use more CPU but prevent memory growth.
	// Default: 5000. Set to 0 to disable manual GC.
	GCInterval int

	// GCPercent sets the Go GC target percentage (same as GOGC env var).
	// Only used when DisableAutoGC is false.
	// Default: 100 (Go's default). Set to -1 to disable GC entirely.
	GCPercent int
}

// DefaultGCConfig returns the default GC configuration.
func DefaultGCConfig() GCConfig {
	return GCConfig{
		DisableAutoGC: true,
		GCInterval:    2000,
		GCPercent:     100,
	}
}

// config holds resolved configuration for a Runtime.
type config struct {
	libraryPath  string
	dependencies []string // npm packages for Dependencies()
	preloadJS    string   // JS to execute before loading dependency bundles
	nodeCompat   bool     // install Node.js compatibility layer
	withFetch    bool     // install fetch polyfill
	gc           *GCConfig
	permissions  *Permissions
	modules      []Module // user-provided modules for require()
}

// Option configures a Runtime.
type Option func(*config)

// WithLibraryPath sets an explicit path to the JavaScriptCore shared library.
// Ignored when using the QuickJS backend.
func WithLibraryPath(path string) Option {
	return func(c *config) { c.libraryPath = path }
}

// WithGC configures garbage collection behavior.
// See GCConfig for details on each setting.
func WithGC(gc GCConfig) Option {
	return func(c *config) { c.gc = &gc }
}

// noCopy may be embedded into structs which must not be copied
// after the first use.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// itoa converts an int to string without importing strconv.
// Used by async managers to build JS code strings.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
