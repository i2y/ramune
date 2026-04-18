package workers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/workers"
)

func TestLoadRamuneTOML_Missing(t *testing.T) {
	t.Parallel()
	got, err := workers.LoadRamuneTOML(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestLoadAndApplyRamuneTOML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ramune.toml")
	content := `
[dependencies]
hono = "*"

[permissions]
net = "granted"
read = "denied"
net_hosts = ["api.example.com"]

[[kv_namespaces]]
binding = "SESSIONS"
namespace = "sessions"

[[kv_namespaces]]
binding = "CACHE"
namespace = "cache"

[secrets]
prefix = "APP_"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	parsed, err := workers.LoadRamuneTOML(path)
	if err != nil {
		t.Fatalf("LoadRamuneTOML: %v", err)
	}
	if parsed == nil {
		t.Fatal("expected non-nil parsed config")
	}

	derived, err := workers.ApplyRamuneTOML(parsed)
	if err != nil {
		t.Fatalf("ApplyRamuneTOML: %v", err)
	}

	if len(derived.Dependencies) != 1 || derived.Dependencies[0] != "hono" {
		t.Errorf("unexpected deps: %v", derived.Dependencies)
	}
	if derived.Permissions == nil {
		t.Fatal("expected permissions")
	}
	if derived.Permissions.Net != ramune.PermGranted {
		t.Errorf("net perm = %v; want granted", derived.Permissions.Net)
	}
	if derived.Permissions.Read != ramune.PermDenied {
		t.Errorf("read perm = %v; want denied", derived.Permissions.Read)
	}
	if len(derived.Permissions.NetHosts) != 1 || derived.Permissions.NetHosts[0] != "api.example.com" {
		t.Errorf("net_hosts = %v", derived.Permissions.NetHosts)
	}
	if len(derived.Options) == 0 {
		t.Error("expected at least one Option from KV + secrets")
	}
}

func TestApplyTOMLBindsNamedKV(t *testing.T) {
	rt := newTestRuntime(t)
	dbPath := filepath.Join(t.TempDir(), "wk.db")

	toml := &workers.RamuneTOML{
		KVNamespaces: []workers.TOMLKVBinding{
			{Binding: "SESSIONS", Namespace: "sessions"},
		},
	}
	derived, err := workers.ApplyRamuneTOML(toml)
	if err != nil {
		t.Fatalf("ApplyRamuneTOML: %v", err)
	}

	const module = `
export default {
  route: "/",
  async fetch(_req, env) {
    env.SESSIONS.put("u42", JSON.stringify({ name: "Alice" }));
    const raw = env.SESSIONS.get("u42");
    return Response.json({ raw });
  },
};
`
	opts := append(derived.Options, workers.WithSQLite(dbPath))
	handler, err := workers.Register(rt, "kv.ts", module, opts...)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `Alice`) {
		t.Errorf("env.SESSIONS round-trip failed: %s", body)
	}
}

func TestParsePermStateRejectsUnknown(t *testing.T) {
	t.Parallel()
	toml := &workers.RamuneTOML{
		Permissions: &workers.TOMLPermissions{Net: "maybe"},
	}
	if _, err := workers.ApplyRamuneTOML(toml); err == nil {
		t.Fatal("expected error for invalid perm state")
	}
}
