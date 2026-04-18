//go:build !nosqlite

package workers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ramune/workers"
)

func TestRegisterEnvDB(t *testing.T) {
	rt := newTestRuntime(t)
	db := filepath.Join(t.TempDir(), "wk.db")

	const module = `
export default {
  route: "/db",
  async fetch(_req, env) {
    env.DB.exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT)");
    env.DB.prepare("INSERT INTO items (name) VALUES (?)").bind("alpha").run();
    env.DB.prepare("INSERT INTO items (name) VALUES (?)").bind("beta").run();
    const rows = env.DB.prepare("SELECT name FROM items ORDER BY id").all();
    const first = env.DB.prepare("SELECT name FROM items ORDER BY id").first("name");
    return Response.json({ rows: rows.results, first });
  },
};
`
	handler, err := workers.Register(rt, "db.ts", module, workers.WithSQLite(db))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/db")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, `"name":"alpha"`) || !strings.Contains(got, `"name":"beta"`) {
		t.Errorf("expected rows in body, got: %s", got)
	}
	if !strings.Contains(got, `"first":"alpha"`) {
		t.Errorf("expected first=alpha, got: %s", got)
	}
}

func TestRegisterEnvKV(t *testing.T) {
	rt := newTestRuntime(t)
	db := filepath.Join(t.TempDir(), "wk.db")

	const module = `
export default {
  route: "/kv",
  async fetch(_req, env) {
    env.KV.put("a", "1");
    env.KV.put("b", { v: 2 });
    env.KV.put("c", "3");
    const b = env.KV.get("b", { type: "json" });
    env.KV.delete("a");
    const list = env.KV.list();
    const sessions = env.KV.namespace("sessions");
    sessions.put("s1", "one");
    const sList = sessions.list();
    return Response.json({
      b,
      defaultKeys: list.keys.map(k => k.name),
      sessionKeys: sList.keys.map(k => k.name),
    });
  },
};
`
	handler, err := workers.Register(rt, "kv.ts", module, workers.WithSQLite(db))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/kv")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if !strings.Contains(got, `"b":{"v":2}`) {
		t.Errorf("json round-trip failed: %s", got)
	}
	if !strings.Contains(got, `"defaultKeys":["b","c"]`) {
		t.Errorf("default namespace keys wrong: %s", got)
	}
	if !strings.Contains(got, `"sessionKeys":["s1"]`) {
		t.Errorf("session namespace keys wrong: %s", got)
	}
}

func TestEnvDBThrowsWithoutSQLite(t *testing.T) {
	rt := newTestRuntime(t)

	const module = `
export default {
  route: "/probe",
  async fetch(_req, env) {
    try {
      env.DB.prepare("SELECT 1").all();
      return new Response("no-error", { status: 200 });
    } catch (e) {
      return new Response("caught: " + e.message, { status: 200 });
    }
  },
};
`
	handler, err := workers.Register(rt, "nodb.ts", module)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/probe")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "caught:") {
		t.Errorf("expected env.DB to throw, got: %s", body)
	}
	if !strings.Contains(string(body), "not configured") {
		t.Errorf("expected clear error message, got: %s", body)
	}
}
