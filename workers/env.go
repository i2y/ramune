package workers

import (
	"os"
	"strings"

	"github.com/i2y/ramune"
)

// defaultSecretsPrefix is the fallback env-var prefix used when no
// SecretsPrefix is configured. Callers can override via
// WithSecretsPrefix.
const defaultSecretsPrefix = "RAMUNE_SECRET_"

// registerEnvBinds installs the Go-side helpers that back the JS
// __buildEnv() factory. Only env.SECRETS is supported here; env.DB and
// env.KV are wired up by Phase 2 in env_sqlite.go.
func registerEnvBinds(rt *ramune.Runtime, b *installedBinds) error {
	return rt.RegisterFunc("__env_list_secrets", func(args []any) (any, error) {
		prefix := b.cfg.SecretsPrefix
		if prefix == "" {
			prefix = defaultSecretsPrefix
		}
		result := map[string]any{}
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, prefix) {
				continue
			}
			parts := strings.SplitN(e, "=", 2)
			key := strings.TrimPrefix(parts[0], prefix)
			val := ""
			if len(parts) > 1 {
				val = parts[1]
			}
			result[key] = val
		}
		return result, nil
	})
}

// envJSCode is the JS-side factory for the env object passed to each
// fetch/scheduled invocation.
//
// Phase 1: only __buildEnvSecrets is populated. __buildEnvDB and
// __buildEnvKV are left as sentinel stubs so user code that tries to
// use env.DB / env.KV without opting into SQLite gets a clear error
// rather than "undefined is not a function".
//
// Phase 2 overrides these stubs when WithSQLite is used.
const envJSCode = `
(function() {
	var __cachedSecrets = null;
	globalThis.__buildEnvSecrets = function() {
		if (!__cachedSecrets) {
			__cachedSecrets = Object.freeze(__env_list_secrets());
		}
		return __cachedSecrets;
	};

	if (typeof globalThis.__buildEnvDB !== "function") {
		globalThis.__buildEnvDB = function() {
			throw new Error("workers: env.DB is not configured (use workers.WithSQLite(...) in Go)");
		};
	}
	if (typeof globalThis.__buildEnvKV !== "function") {
		globalThis.__buildEnvKV = function(_ns) {
			throw new Error("workers: env.KV is not configured (use workers.WithSQLite(...) in Go)");
		};
	}

	globalThis.__buildEnv = function() {
		var env = { SECRETS: __buildEnvSecrets() };
		// DB and KV are installed lazily — only materialised when the
		// user code reads env.DB / env.KV, so the sentinel stubs above
		// do not trigger on every request.
		Object.defineProperty(env, "DB", {
			configurable: true,
			enumerable: true,
			get: function() { return __buildEnvDB(); }
		});
		Object.defineProperty(env, "KV", {
			configurable: true,
			enumerable: true,
			get: function() { return __buildEnvKV("default"); }
		});
		if (typeof globalThis.__extraEnvBindings === "function") {
			globalThis.__extraEnvBindings(env);
		}
		return env;
	};
})();
`
