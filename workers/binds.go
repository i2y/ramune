package workers

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/i2y/ramune"
)

// installedRuntimes tracks which Runtimes have had the bindings
// installed so Register can be called multiple times with different
// modules on the same Runtime. Later Register calls reuse the Go
// callbacks and only re-exec ExtraEnvJS / refresh SecretsPrefix.
var installedRuntimes sync.Map // *ramune.Runtime → *installedBinds

type installedBinds struct {
	// secretsPrefix is read by __env_list_secrets on every request.
	// Held by pointer so Register calls can update it in place.
	secretsPrefix string
}

// installBindings registers the Go callback functions and JS helpers
// that every Workers-style module depends on. Safe to call repeatedly
// on the same Runtime.
func installBindings(rt *ramune.Runtime, cfg *Config) error {
	if prior, ok := installedRuntimes.Load(rt); ok {
		prior.(*installedBinds).secretsPrefix = cfg.SecretsPrefix
		if cfg.ExtraEnvJS != "" {
			if err := rt.Exec(cfg.ExtraEnvJS); err != nil {
				return fmt.Errorf("workers: re-install extra env JS: %w", err)
			}
		}
		return nil
	}

	b := &installedBinds{secretsPrefix: cfg.SecretsPrefix}
	installedRuntimes.Store(rt, b)

	if err := registerRequestBinds(rt); err != nil {
		return err
	}
	if err := registerResponseBinds(rt); err != nil {
		return err
	}
	if err := registerEnvBinds(rt, b); err != nil {
		return err
	}
	if err := rt.Exec(envJSCode); err != nil {
		return fmt.Errorf("workers: install env JS: %w", err)
	}
	if err := rt.Exec(fetchDispatchJS); err != nil {
		return fmt.Errorf("workers: install fetch dispatch JS: %w", err)
	}
	// SQLite installs both KV + DB backends; WithKVBackend/WithDBBackend
	// install the relevant one independently. SQLitePath is mutually
	// exclusive with the typed options (validated in Register).
	if cfg.SQLitePath != "" {
		if err := installSQLiteBinds(rt, cfg); err != nil {
			return fmt.Errorf("workers: install sqlite binds: %w", err)
		}
	}
	if cfg.KVBackend != nil {
		if err := installKVBackend(rt, cfg.KVBackend); err != nil {
			return fmt.Errorf("workers: install KV backend: %w", err)
		}
	}
	if cfg.DBBackend != nil {
		if err := installDBBackend(rt, cfg.DBBackend); err != nil {
			return fmt.Errorf("workers: install DB backend: %w", err)
		}
	}
	if cfg.ExtraEnvJS != "" {
		if err := rt.Exec(cfg.ExtraEnvJS); err != nil {
			return fmt.Errorf("workers: install extra env JS: %w", err)
		}
	}
	return nil
}

// regFunc wraps RegisterFunc with a uniform error prefix used by every
// workers callback installation site.
func regFunc(rt *ramune.Runtime, name string, fn ramune.GoFunc) error {
	if err := rt.RegisterFunc(name, fn); err != nil {
		return fmt.Errorf("workers: RegisterFunc %s: %w", name, err)
	}
	return nil
}

// registerRequestBinds installs the read-side request helpers.
func registerRequestBinds(rt *ramune.Runtime) error {
	if err := regFunc(rt, "__readGoRequestBody", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__readGoRequestBody")
		if err != nil {
			return "", err
		}
		if state.r.Body == nil {
			return "", nil
		}
		data, err := io.ReadAll(state.r.Body)
		if err != nil {
			return "", fmt.Errorf("__readGoRequestBody: %w", err)
		}
		return string(data), nil
	}); err != nil {
		return err
	}

	return regFunc(rt, "__getGoRequestHeaders", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__getGoRequestHeaders")
		if err != nil {
			return map[string]any{}, nil
		}
		headers := make(map[string]any, len(state.r.Header)+1)
		for k, v := range state.r.Header {
			switch len(v) {
			case 0:
			case 1:
				headers[k] = v[0]
			default:
				headers[k] = strings.Join(v, ", ")
			}
		}
		// Go's net/http parses the Host header into r.Host instead of
		// leaving it in r.Header. Expose it back to JS so workers can
		// match on the Host header the way Cloudflare Workers do.
		if state.r.Host != "" {
			if _, exists := headers["Host"]; !exists {
				headers["Host"] = state.r.Host
			}
		}
		return headers, nil
	})
}

// registerResponseBinds installs the write-side response helpers plus
// the signal-closing __detachResponse used to release the HTTP handler.
func registerResponseBinds(rt *ramune.Runtime) error {
	if err := regFunc(rt, "__detachResponse", func(args []any) (any, error) {
		if reqID, ok := toInt64(argAt(args, 0)); ok {
			if v, ok := requestRegistry.Load(reqID); ok {
				v.(*requestState).signal()
			}
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := regFunc(rt, "__writeWorkerResponse", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__writeWorkerResponse")
		if err != nil {
			return nil, err
		}
		applyHeaders(state, stringArg(args, 2))
		state.writeHeader(intArg(args, 1, 200))
		if body := stringArg(args, 3); body != "" {
			state.writeBody(body)
		}
		return nil, nil
	}); err != nil {
		return err
	}

	if err := regFunc(rt, "__writeWorkerResponseStart", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__writeWorkerResponseStart")
		if err != nil {
			return nil, err
		}
		applyHeaders(state, stringArg(args, 2))
		state.writeHeader(intArg(args, 1, 200))
		state.flush()
		return nil, nil
	}); err != nil {
		return err
	}

	return regFunc(rt, "__writeWorkerResponseChunk", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__writeWorkerResponseChunk")
		if err != nil {
			return nil, err
		}
		if text := stringArg(args, 1); text != "" {
			state.writeBody(text)
			state.flush()
		}
		return nil, nil
	})
}

// applyHeaders parses a JSON header map and sets it on the response.
// Malformed input is silently ignored so a broken user-supplied object
// cannot prevent the status write from happening.
func applyHeaders(state *requestState, headersJSON string) {
	if headersJSON == "" || headersJSON == "{}" {
		return
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		return
	}
	h := state.w.Header()
	for k, v := range headers {
		h.Set(k, v)
	}
}

// stateFromArgs resolves args[0] to the requestState registered for the
// call's reqID. Used by every response-writing binding.
func stateFromArgs(args []any, fn string) (*requestState, error) {
	reqID, ok := toInt64(argAt(args, 0))
	if !ok {
		return nil, fmt.Errorf("%s: missing or invalid reqID", fn)
	}
	v, loaded := requestRegistry.Load(reqID)
	if !loaded {
		return nil, fmt.Errorf("%s: unknown request id %d", fn, reqID)
	}
	return v.(*requestState), nil
}

// ---- JS-side primitive helpers --------------------------------------

func argAt(args []any, i int) any {
	if i >= len(args) {
		return nil
	}
	return args[i]
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

func intArg(args []any, i, def int) int {
	if n, ok := toInt64(argAt(args, i)); ok {
		return int(n)
	}
	return def
}

func stringArg(args []any, i int) string {
	if s, ok := argAt(args, i).(string); ok {
		return s
	}
	return ""
}
