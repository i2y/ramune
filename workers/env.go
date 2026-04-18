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
// __buildEnv() factory. Only env.SECRETS is registered here — env.DB
// and env.KV are installed separately via installSQLiteBinds or
// installKVBackend / installDBBackend.
func registerEnvBinds(rt *ramune.Runtime, b *installedBinds) error {
	return rt.RegisterFunc("__env_list_secrets", func(args []any) (any, error) {
		prefix := b.secretsPrefix
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

// envJSCode installs the JS-side factory for the env object passed to
// each fetch/scheduled invocation. __buildEnvDB and __buildEnvKV are
// stubs that throw unless a SQLite path or typed backend is configured.
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
