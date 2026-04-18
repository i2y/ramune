// Package workers provides a Cloudflare-Workers-style handler runtime on
// top of Ramune. User code is authored as an ES module with a default
// export:
//
//	export default {
//	    route: "/api/hello",
//	    async fetch(request, env, ctx) {
//	        return Response.json({ ok: true });
//	    },
//	};
//
// Call [Register] with the module source and a *ramune.Runtime; the
// returned http.Handler dispatches incoming requests through fetch().
//
// The ctx argument exposes waitUntil(promise): the Go HTTP handler
// returns as soon as the Response has been written, but the JavaScript
// executor continues draining pending promises up to WaitUntilTimeout.
//
// This package does not provide its own HTTP server — use it inside any
// Go HTTP server (net/http, chi, Echo, the ramune CLI, etc.).
package workers

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/i2y/ramune"
)

// Config controls how Register wires up a Workers-style module.
type Config struct {
	// WaitUntilTimeout bounds how long the executor is held after the
	// HTTP response has been written, waiting for ctx.waitUntil
	// promises to settle. Non-positive disables the timeout.
	WaitUntilTimeout time.Duration

	// SecretsPrefix is the environment-variable prefix consulted when
	// building env.SECRETS. A variable named "<prefix>API_KEY=abc"
	// appears as env.SECRETS.API_KEY. Empty string uses the default
	// "RAMUNE_SECRET_".
	SecretsPrefix string

	// ExtraEnvJS is JavaScript appended to the env builder. It runs
	// once per runtime with the partially-built env object exposed via
	// the globalThis.__extraEnvBindings hook. Used by callers (e.g.
	// ramune.toml loaders) to attach named bindings.
	ExtraEnvJS string

	// SQLitePath, when non-empty, opens a SQLite database at the given
	// filesystem path and installs env.DB (D1-compatible) and env.KV
	// (Workers-KV-like). When empty, env.DB / env.KV access throws
	// unless a KVBackend / DBBackend has been supplied. Mutually
	// exclusive with KVBackend and DBBackend.
	SQLitePath string

	// KVBackend, when non-nil, provides the storage for env.KV. See
	// the [KVBackend] interface docs. Mutually exclusive with
	// SQLitePath.
	KVBackend KVBackend

	// DBBackend, when non-nil, provides the SQL engine for env.DB.
	// See [DBBackend]. Mutually exclusive with SQLitePath.
	DBBackend DBBackend
}

// Option configures Register.
type Option func(*Config)

// WithWaitUntilTimeout sets the ctx.waitUntil promise timeout.
// Non-positive disables the timeout (wait indefinitely).
func WithWaitUntilTimeout(d time.Duration) Option {
	return func(c *Config) { c.WaitUntilTimeout = d }
}

// WithSecretsPrefix overrides the env.SECRETS variable prefix.
// The default is "RAMUNE_SECRET_".
func WithSecretsPrefix(p string) Option {
	return func(c *Config) { c.SecretsPrefix = p }
}

// WithExtraEnvJS attaches JavaScript that extends the env object.
// The snippet must define globalThis.__extraEnvBindings(env) or
// mutate env directly. Runs once per Runtime, after the default env
// builders (SECRETS, and, if enabled, DB/KV) are installed.
func WithExtraEnvJS(js string) Option {
	return func(c *Config) { c.ExtraEnvJS = js }
}

// WithSQLite opens a SQLite database at the given path and installs
// env.DB (a D1-compatible SQL facade) and env.KV (a Workers-KV-like
// key/value store backed by a single table __ramune_kv). Without this
// option, env.DB and env.KV access throws at runtime.
//
// An empty path disables SQLite; ":memory:" uses an in-process DB.
// Multiple Register calls that pass the same path share a DB handle.
func WithSQLite(path string) Option {
	return func(c *Config) { c.SQLitePath = path }
}

// Register attaches a Workers-style module to rt and returns an
// http.Handler that dispatches matching requests through the module's
// fetch export. If the module declares a cron expression and a
// scheduled() function, that handler is registered on the runtime's
// cron manager (requires Ramune's cron support to be available).
//
// Register may be called multiple times on the same Runtime with
// different modules; the Go-side bindings are installed once per
// Runtime and the module code is cached under a filename-derived key.
//
// The returned handler uses Go's http.ServeMux to apply the module's
// route pattern (Go 1.22+ syntax: /foo/{id}, /bar/{rest...}). If the
// module omits route, the handler matches every path.
func Register(rt *ramune.Runtime, filename, code string, opts ...Option) (http.Handler, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.SecretsPrefix == "" {
		cfg.SecretsPrefix = defaultSecretsPrefix
	}
	if err := validateBackends(&cfg); err != nil {
		return nil, err
	}

	if !IsWorkersStyle(code) {
		return nil, fmt.Errorf("workers: %s is not a Workers-style module (no \"export default\")", filename)
	}

	transformed, err := TranspileModule(filename, code)
	if err != nil {
		return nil, err
	}

	if err := installBindings(rt, &cfg); err != nil {
		return nil, fmt.Errorf("workers: install bindings: %w", err)
	}

	if err := rt.Exec(transformed); err != nil {
		return nil, fmt.Errorf("workers: evaluate %s: %w", filename, err)
	}

	cacheKey := moduleCacheKey(filename)
	mod, err := extractModuleConfig(rt)
	if err != nil {
		return nil, err
	}

	// Move the default export to a stable cache key so dispatches can
	// locate it, then clean up the transient __workers_export global.
	persist := fmt.Sprintf(
		`(function(){ if (typeof __workers_export !== "undefined") { globalThis[%q] = __workers_export.default; delete globalThis.__workers_export; } })();`,
		cacheKey,
	)
	if err := rt.Exec(persist); err != nil {
		return nil, fmt.Errorf("workers: persist default export: %w", err)
	}

	if mod.HasScheduled && mod.Cron != "" {
		if err := registerScheduled(rt, cacheKey, mod.Cron); err != nil {
			return nil, fmt.Errorf("workers: register scheduled: %w", err)
		}
	}

	if !mod.HasFetch {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "workers: module has no fetch handler", http.StatusNotImplemented)
		}), nil
	}

	route := strings.TrimSpace(mod.Route)
	if route == "" {
		route = "/"
	}

	dispatch := newFetchDispatcher(rt, cacheKey, cfg)
	mux := http.NewServeMux()
	mux.Handle(route, dispatch)
	// Register a second pattern for sub-paths when route is "/" — the
	// default ServeMux treats "/" as the catch-all so no extra work is
	// needed. For explicit routes, the user opts in to Go 1.22 {...}
	// wildcards themselves.
	return mux, nil
}

func defaultConfig() Config {
	return Config{
		WaitUntilTimeout: 30 * time.Second,
		SecretsPrefix:    defaultSecretsPrefix,
	}
}

// moduleCacheKey derives a globalThis property name from a filename.
// The result is used to cache the default export per-runtime so later
// dispatches can access it without re-evaluating the module IIFE.
func moduleCacheKey(filename string) string {
	base := filepath.Base(filename)
	// Drop known extensions so that "hello.ts" and "hello.js" do not
	// collide — esbuild has already produced the same output either
	// way, but duplicates of the same logical name would clobber.
	for _, ext := range []string{".ts", ".tsx", ".mts", ".cts", ".mjs", ".js"} {
		if strings.HasSuffix(base, ext) {
			base = strings.TrimSuffix(base, ext)
			break
		}
	}
	// Replace everything that is not a JS identifier character.
	var b strings.Builder
	b.WriteString("__wk_")
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '$':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
