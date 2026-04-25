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
	"sync/atomic"
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

	// BlobBackend, when non-nil, services env.<bucket> object-storage
	// bindings. See [BlobBackend]. The host is responsible for telling
	// the worker which bindings exist; this Config field just enables
	// the dispatch path.
	BlobBackend BlobBackend

	// ExtraCrons is a list of additional cron expressions that should
	// fire the module's scheduled handler. Used by the host
	// (chinotto.toml's [[workers]].crons, etc.) to declare schedules
	// without requiring the worker to embed a single `cron` string in
	// its source. If the module also exports a non-empty `cron` field,
	// that schedule is registered first and these are appended.
	ExtraCrons []string

	// Fetch, when non-nil, replaces globalThis.fetch for this worker
	// with a thin JS wrapper that forwards every call through fn.
	//
	// Installed AFTER the module has been evaluated, so modules that
	// capture fetch into a local variable before Fetch is wired up
	// still see Ramune's default. Intended for platforms — notably
	// openworkers inside a Firecracker guest — that need per-worker
	// egress policy independent of the runtime-wide [ramune.WithFetch].
	Fetch func(*http.Request) (*http.Response, error)
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

// WithCrons appends one or more cron expressions that fire the
// module's scheduled handler. Combined with any `cron` field the
// module exports.
func WithCrons(exprs ...string) Option {
	return func(c *Config) {
		c.ExtraCrons = append(c.ExtraCrons, exprs...)
	}
}

// WithFetchFunc installs a per-worker override of globalThis.fetch.
// The supplied fn receives a populated *http.Request (method,
// headers, body) and must return a full *http.Response. Intended for
// platforms that need to route egress through a side channel — e.g.
// a Firecracker guest forwarding through a host-side proxy over
// vsock — independent of [ramune.WithFetch]'s runtime-wide hook.
func WithFetchFunc(fn func(*http.Request) (*http.Response, error)) Option {
	return func(c *Config) { c.Fetch = fn }
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

// Prepared is a transpiled Workers module ready to attach to one or
// more Runtimes. Transpile runs esbuild once; each Runtime reuses the
// same output via AttachPrepared. Callers with a single Runtime can
// use the Register shortcut which bundles Prepare + AttachPrepared.
type Prepared struct {
	filename    string
	cacheKey    string
	transformed string
}

// Prepare runs esbuild on the source and returns a Prepared module
// that can be attached to one or more Runtimes via AttachPrepared.
// Use this when serving a single entry across N independent VMs
// (ramune serve --workers N) to avoid running esbuild per VM.
func Prepare(filename, code string) (*Prepared, error) {
	if !IsWorkersStyle(code) {
		return nil, fmt.Errorf("workers: %s is not a Workers-style module (no \"export default\")", filename)
	}
	transformed, err := TranspileModule(filename, code)
	if err != nil {
		return nil, err
	}
	return &Prepared{
		filename:    filename,
		cacheKey:    moduleCacheKey(filename),
		transformed: transformed,
	}, nil
}

// Register is a convenience that wraps Prepare + AttachPrepared. It
// returns an http.Handler that dispatches matching requests through
// the module's fetch export. If the module declares a cron expression
// and a scheduled() function, that handler is installed on the
// runtime's cron manager (requires Ramune's cron support).
//
// The returned handler uses Go's http.ServeMux to apply the module's
// route pattern (Go 1.22+ syntax: /foo/{id}, /bar/{rest...}). If the
// module omits route, the handler matches every path.
func Register(rt *ramune.Runtime, filename, code string, opts ...Option) (http.Handler, error) {
	p, err := Prepare(filename, code)
	if err != nil {
		return nil, err
	}
	return AttachPrepared(rt, p, opts...)
}

// AttachPrepared binds a previously-prepared module to rt. Safe to call
// with the same Prepared on multiple Runtimes; each gets its own copy
// of the module globals.
func AttachPrepared(rt *ramune.Runtime, p *Prepared, opts ...Option) (http.Handler, error) {
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

	if err := installBindings(rt, &cfg); err != nil {
		return nil, fmt.Errorf("workers: install bindings: %w", err)
	}

	if err := rt.Exec(p.transformed); err != nil {
		return nil, fmt.Errorf("workers: evaluate %s: %w", p.filename, err)
	}

	mod, err := ExtractModuleConfig(rt)
	if err != nil {
		return nil, err
	}

	// Move the default export to a stable cache key and clean up the
	// transient __workers_export global.
	persist := fmt.Sprintf(
		`(function(){ if (typeof __workers_export !== "undefined") { globalThis[%q] = __workers_export.default; delete globalThis.__workers_export; } })();`,
		p.cacheKey,
	)
	if err := rt.Exec(persist); err != nil {
		return nil, fmt.Errorf("workers: persist default export: %w", err)
	}

	if mod.HasScheduled && mod.Cron != "" {
		if err := registerScheduled(rt, p.cacheKey, mod.Cron); err != nil {
			return nil, fmt.Errorf("workers: register scheduled: %w", err)
		}
	}
	if mod.HasScheduled && len(cfg.ExtraCrons) > 0 {
		for i, expr := range cfg.ExtraCrons {
			if err := registerScheduledWithID(rt, p.cacheKey, expr, fmt.Sprintf("workers:%s:extra:%d", p.cacheKey, i)); err != nil {
				return nil, fmt.Errorf("workers: register extra cron[%d] %q: %w", i, expr, err)
			}
		}
	}

	if cfg.Fetch != nil {
		if err := installPerWorkerFetch(rt, cfg.Fetch); err != nil {
			return nil, fmt.Errorf("workers: install per-worker fetch: %w", err)
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

	dispatch := newFetchDispatcher(rt, p.cacheKey, cfg)
	mux := http.NewServeMux()
	mux.Handle(route, dispatch)
	return &workerHandler{mux: mux, dispatch: dispatch, cfg: cfg}, nil
}

// workerHandler is the http.Handler returned by AttachPrepared. It
// additionally implements [io.Closer] so callers can drain in-flight
// requests (including their ctx.waitUntil background promises) before
// tearing down the worker's Runtime.
//
// Close() rejects new requests with 503, waits for every already-
// accepted request's executor goroutine to finish, and returns
// success. It does NOT close the Ramune runtime itself — the caller
// retains ownership of rt.Close().
type workerHandler struct {
	mux      *http.ServeMux
	dispatch *fetchDispatcher
	cfg      Config
	closed   atomic.Bool
}

// ServeHTTP implements http.Handler. Returns 503 once Close has been
// called; otherwise delegates to the route mux.
func (h *workerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.closed.Load() {
		http.Error(w, "workers: handler draining", http.StatusServiceUnavailable)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// Close drains the handler. Safe to call multiple times; subsequent
// calls are no-ops.
//
// Drain budget: WaitUntilTimeout + a 5s buffer for Go-side cleanup.
// Callers who want a strict bound should structure their shutdown
// around their own context and call rt.Close when it expires.
func (h *workerHandler) Close() error {
	if !h.closed.CompareAndSwap(false, true) {
		return nil
	}
	budget := h.cfg.WaitUntilTimeout
	if budget <= 0 {
		budget = 30 * time.Second
	}
	budget += 5 * time.Second
	return h.dispatch.wait(budget)
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
