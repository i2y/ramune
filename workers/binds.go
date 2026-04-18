package workers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/i2y/ramune"
)

// installOnce tracks which Runtimes have already had the bindings
// installed so Register can be called multiple times with different
// modules on the same Runtime without re-registering.
var installOnce sync.Map // *ramune.Runtime → *installedBinds

type installedBinds struct {
	cfg Config
}

// installBindings registers the Go callback functions and JS helpers
// that every Workers-style module depends on.
//
// Called at Register time. Safe to call repeatedly on the same Runtime
// — only the first call actually registers the Go callbacks; later
// calls can still update the effective SecretsPrefix via the stored
// config.
func installBindings(rt *ramune.Runtime, cfg *Config) error {
	if prior, ok := installOnce.Load(rt); ok {
		// Update the prefix if the caller supplied a new one. Mutating
		// through the stored pointer lets the Go callbacks pick up the
		// change at the next invocation.
		b := prior.(*installedBinds)
		b.cfg.SecretsPrefix = cfg.SecretsPrefix
		// ExtraEnvJS may differ per module — re-exec it.
		if cfg.ExtraEnvJS != "" {
			if err := rt.Exec(cfg.ExtraEnvJS); err != nil {
				return fmt.Errorf("workers: re-install extra env JS: %w", err)
			}
		}
		return nil
	}

	b := &installedBinds{cfg: *cfg}
	installOnce.Store(rt, b)

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
	// Order matters: SQLite installs both KV + DB backends, then any
	// explicit KVBackend/DBBackend override. SQLitePath is mutually
	// exclusive with the backend options (validated in Register), so
	// only one branch of each pair actually runs.
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

// registerRequestBinds installs the read-side request helpers.
func registerRequestBinds(rt *ramune.Runtime) error {
	if err := rt.RegisterFunc("__readGoRequestBody", func(args []any) (any, error) {
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
		return fmt.Errorf("workers: RegisterFunc __readGoRequestBody: %w", err)
	}

	if err := rt.RegisterFunc("__getGoRequestHeaders", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__getGoRequestHeaders")
		if err != nil {
			return map[string]any{}, nil
		}
		headers := make(map[string]any, len(state.r.Header))
		for k, v := range state.r.Header {
			switch len(v) {
			case 0:
				// drop
			case 1:
				headers[k] = v[0]
			default:
				headers[k] = strings.Join(v, ", ")
			}
		}
		return headers, nil
	}); err != nil {
		return fmt.Errorf("workers: RegisterFunc __getGoRequestHeaders: %w", err)
	}

	return nil
}

// registerResponseBinds installs the write-side response helpers plus
// the signal-closing __detachResponse used to release the HTTP handler.
func registerResponseBinds(rt *ramune.Runtime) error {
	if err := rt.RegisterFunc("__detachResponse", func(args []any) (any, error) {
		reqID, ok := toInt64(argAt(args, 0))
		if !ok {
			return nil, nil
		}
		if v, ok := requestRegistry.Load(reqID); ok {
			v.(*requestState).signal()
		}
		return nil, nil
	}); err != nil {
		return fmt.Errorf("workers: RegisterFunc __detachResponse: %w", err)
	}

	if err := rt.RegisterFunc("__writeWorkerResponse", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__writeWorkerResponse")
		if err != nil {
			return nil, err
		}
		status := intArg(args, 1, 200)
		headersJSON := stringArg(args, 2)
		body := stringArg(args, 3)
		applyHeaders(state, headersJSON)
		state.statusMu.Lock()
		if !state.started {
			state.w.WriteHeader(status)
			state.started = true
		}
		state.statusMu.Unlock()
		if body != "" {
			if _, err := io.WriteString(state.w, body); err != nil {
				return nil, nil // client gone; drop silently
			}
			state.statusMu.Lock()
			state.written = true
			state.statusMu.Unlock()
		}
		return nil, nil
	}); err != nil {
		return fmt.Errorf("workers: RegisterFunc __writeWorkerResponse: %w", err)
	}

	if err := rt.RegisterFunc("__writeWorkerResponseStart", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__writeWorkerResponseStart")
		if err != nil {
			return nil, err
		}
		status := intArg(args, 1, 200)
		headersJSON := stringArg(args, 2)
		applyHeaders(state, headersJSON)
		state.statusMu.Lock()
		if !state.started {
			state.w.WriteHeader(status)
			state.started = true
		}
		state.statusMu.Unlock()
		if state.flusher != nil {
			state.flusher.Flush()
		}
		return nil, nil
	}); err != nil {
		return fmt.Errorf("workers: RegisterFunc __writeWorkerResponseStart: %w", err)
	}

	if err := rt.RegisterFunc("__writeWorkerResponseChunk", func(args []any) (any, error) {
		state, err := stateFromArgs(args, "__writeWorkerResponseChunk")
		if err != nil {
			return nil, err
		}
		text := stringArg(args, 1)
		if text == "" {
			return nil, nil
		}
		if _, err := io.WriteString(state.w, text); err != nil {
			return nil, nil // client gone
		}
		state.statusMu.Lock()
		state.written = true
		state.statusMu.Unlock()
		if state.flusher != nil {
			state.flusher.Flush()
		}
		return nil, nil
	}); err != nil {
		return fmt.Errorf("workers: RegisterFunc __writeWorkerResponseChunk: %w", err)
	}

	return nil
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

// ensureHandler keeps a reference to http.Handler-only helpers so the
// import is not pruned when build tags disable some features.
var _ http.Handler = (*dummyHandler)(nil)

type dummyHandler struct{}

func (dummyHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}
