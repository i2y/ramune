package workers

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/i2y/ramune"
)

// RamuneTOML mirrors the on-disk schema of ramune.toml, an optional
// declarative config placed next to the worker entrypoint.
//
// Only fields understood by the workers package are listed here; the
// ramune CLI may parse additional top-level keys separately.
type RamuneTOML struct {
	Dependencies map[string]string `toml:"dependencies"`
	Permissions  *TOMLPermissions  `toml:"permissions"`
	KVNamespaces []TOMLKVBinding   `toml:"kv_namespaces"`
	Secrets      *TOMLSecrets      `toml:"secrets"`
}

// TOMLPermissions mirrors the [permissions] table.
type TOMLPermissions struct {
	Net   string `toml:"net"`
	Read  string `toml:"read"`
	Write string `toml:"write"`
	Env   string `toml:"env"`
	Run   string `toml:"run"`

	NetHosts   []string `toml:"net_hosts"`
	ReadPaths  []string `toml:"read_paths"`
	WritePaths []string `toml:"write_paths"`
	EnvVars    []string `toml:"env_vars"`
	RunCmds    []string `toml:"run_cmds"`
}

// TOMLKVBinding declares a named KV binding exposed on env.
//
//	[[kv_namespaces]]
//	binding = "SESSIONS"
//	namespace = "sessions"
//
// turns into env.SESSIONS backed by the "sessions" namespace of the
// shared SQLite KV store (requires WithSQLite).
type TOMLKVBinding struct {
	Binding   string `toml:"binding"`
	Namespace string `toml:"namespace"`
}

// TOMLSecrets configures how the SECRETS binding is populated. A blank
// prefix falls back to "RAMUNE_SECRET_".
type TOMLSecrets struct {
	Prefix string `toml:"prefix"`
}

// LoadRamuneTOML parses a ramune.toml file. Returns (nil, nil) if the
// file does not exist — the TOML is optional.
func LoadRamuneTOML(path string) (*RamuneTOML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r RamuneTOML
	if _, err := toml.Decode(string(data), &r); err != nil {
		return nil, fmt.Errorf("ramune.toml: %w", err)
	}
	return &r, nil
}

// BuildExtraEnvJS returns a JS snippet that installs a
// globalThis.__extraEnvBindings(env) function binding each declared KV
// namespace onto env under the requested property name. Bindings with
// non-identifier names are skipped silently.
func BuildExtraEnvJS(kv []TOMLKVBinding) string {
	var b strings.Builder
	b.WriteString("globalThis.__extraEnvBindings = function(env) {\n")
	for _, kb := range kv {
		if !isJSIdentifier(kb.Binding) || kb.Namespace == "" {
			continue
		}
		fmt.Fprintf(&b, "  Object.defineProperty(env, %q, {\n", kb.Binding)
		fmt.Fprintf(&b, "    configurable: true, enumerable: true,\n")
		fmt.Fprintf(&b, "    get: function() { return __buildEnvKV(%q); }\n", kb.Namespace)
		b.WriteString("  });\n")
	}
	b.WriteString("};\n")
	return b.String()
}

func isJSIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '$'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// TOMLDerived is the subset of configuration derived from a ramune.toml
// file. Callers pass it to Register (in two parts: Permissions and
// NPM dependencies go to ramune.New, the rest become workers Options).
type TOMLDerived struct {
	Dependencies []string
	Permissions  *ramune.Permissions
	Options      []Option
}

// ApplyRamuneTOML converts a parsed TOML into the pieces the caller
// needs: npm packages to pass to ramune.Dependencies, ramune
// Permissions, and workers.Option values. Returns an empty TOMLDerived
// for a nil input so callers can unconditionally merge.
func ApplyRamuneTOML(r *RamuneTOML) (TOMLDerived, error) {
	var out TOMLDerived
	if r == nil {
		return out, nil
	}

	if len(r.Dependencies) > 0 {
		names := make([]string, 0, len(r.Dependencies))
		for name := range r.Dependencies {
			names = append(names, name)
		}
		out.Dependencies = names
	}

	if r.Permissions != nil {
		p, err := permissionsFromTOML(r.Permissions)
		if err != nil {
			return out, err
		}
		out.Permissions = p
	}

	if len(r.KVNamespaces) > 0 {
		out.Options = append(out.Options, WithExtraEnvJS(BuildExtraEnvJS(r.KVNamespaces)))
	}
	if r.Secrets != nil && r.Secrets.Prefix != "" {
		out.Options = append(out.Options, WithSecretsPrefix(r.Secrets.Prefix))
	}
	return out, nil
}

func permissionsFromTOML(p *TOMLPermissions) (*ramune.Permissions, error) {
	net, err := parsePermState(p.Net, "net")
	if err != nil {
		return nil, err
	}
	read, err := parsePermState(p.Read, "read")
	if err != nil {
		return nil, err
	}
	write, err := parsePermState(p.Write, "write")
	if err != nil {
		return nil, err
	}
	env, err := parsePermState(p.Env, "env")
	if err != nil {
		return nil, err
	}
	run, err := parsePermState(p.Run, "run")
	if err != nil {
		return nil, err
	}
	return &ramune.Permissions{
		Net:        net,
		Read:       read,
		Write:      write,
		Env:        env,
		Run:        run,
		NetHosts:   p.NetHosts,
		ReadPaths:  p.ReadPaths,
		WritePaths: p.WritePaths,
		EnvVars:    p.EnvVars,
		RunCmds:    p.RunCmds,
	}, nil
}

func parsePermState(s, field string) (ramune.PermissionState, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "granted":
		return ramune.PermGranted, nil
	case "denied":
		return ramune.PermDenied, nil
	default:
		return 0, fmt.Errorf("ramune.toml permissions.%s: expected \"granted\" or \"denied\", got %q", field, s)
	}
}
