package workers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/i2y/ramune/workers"
)

// inMemoryKV is an example KVBackend. Production backends would wrap
// Redis, DynamoDB, Postgres, etc.
type inMemoryKV struct {
	mu    sync.Mutex
	store map[string]string
}

func newInMemoryKV() *inMemoryKV {
	return &inMemoryKV{store: map[string]string{}}
}

func (k *inMemoryKV) key(ns, key string) string { return ns + "\x00" + key }

func (k *inMemoryKV) Get(ns, key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.store[k.key(ns, key)]
	return v, ok, nil
}

func (k *inMemoryKV) Put(ns, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.store[k.key(ns, key)] = value
	return nil
}

func (k *inMemoryKV) Delete(ns, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.store, k.key(ns, key))
	return nil
}

func (k *inMemoryKV) List(ns, prefix, cursor string, limit int) ([]string, string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	nsPrefix := ns + "\x00" + prefix
	all := make([]string, 0)
	for raw := range k.store {
		if !strings.HasPrefix(raw, nsPrefix) {
			continue
		}
		key := strings.TrimPrefix(raw, ns+"\x00")
		if cursor != "" && key <= cursor {
			continue
		}
		all = append(all, key)
	}
	sort.Strings(all)
	nextCursor := ""
	if limit > 0 && len(all) > limit {
		all = all[:limit]
		nextCursor = all[len(all)-1]
	}
	return all, nextCursor, nil
}

// inMemoryDB is an example DBBackend with a single in-memory key-value
// table — just enough to exercise the plumbing end-to-end without
// pulling in another DB driver.
type inMemoryDB struct {
	mu   sync.Mutex
	rows []map[string]any
	next int64
}

func (d *inMemoryDB) Query(sqlStr string, _ []any) ([]map[string]any, error) {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sqlStr)), "SELECT") {
		d.mu.Lock()
		defer d.mu.Unlock()
		out := make([]map[string]any, len(d.rows))
		for i, r := range d.rows {
			cp := make(map[string]any, len(r))
			for k, v := range r {
				cp[k] = v
			}
			out[i] = cp
		}
		return out, nil
	}
	return nil, nil
}

func (d *inMemoryDB) Exec(sqlStr string, params []any) (int64, int64, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(sqlStr))
	switch {
	case strings.HasPrefix(trimmed, "INSERT"):
		d.mu.Lock()
		defer d.mu.Unlock()
		d.next++
		name := ""
		if len(params) > 0 {
			name, _ = params[0].(string)
		}
		d.rows = append(d.rows, map[string]any{"id": float64(d.next), "name": name})
		return 1, d.next, nil
	case strings.HasPrefix(trimmed, "DELETE"):
		d.mu.Lock()
		defer d.mu.Unlock()
		n := int64(len(d.rows))
		d.rows = nil
		return n, 0, nil
	}
	return 0, 0, nil
}

func TestWithKVBackend(t *testing.T) {
	rt := newTestRuntime(t)
	kv := newInMemoryKV()

	const module = `
export default {
  async fetch(_req, env) {
    env.KV.put("a", { n: 1 });
    env.KV.put("b", "two");
    const a = env.KV.get("a", { type: "json" });
    const b = env.KV.get("b");
    const keys = env.KV.list().keys.map(k => k.name);
    env.KV.delete("b");
    const afterDelete = env.KV.list().keys.map(k => k.name);
    return Response.json({ a, b, keys, afterDelete });
  },
};
`
	handler, err := workers.Register(rt, "kv.ts", module, workers.WithKVBackend(kv))
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
	got := string(body)

	for _, want := range []string{
		`"a":{"n":1}`,
		`"b":"two"`,
		`"keys":["a","b"]`,
		`"afterDelete":["a"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in body: %s", want, got)
		}
	}

	kv.mu.Lock()
	remaining := len(kv.store)
	kv.mu.Unlock()
	if remaining != 1 {
		t.Errorf("expected 1 entry in backend after delete, got %d", remaining)
	}
}

func TestWithDBBackend(t *testing.T) {
	rt := newTestRuntime(t)
	db := &inMemoryDB{}

	const module = `
export default {
  async fetch(_req, env) {
    const ins1 = env.DB.prepare("INSERT INTO t (name) VALUES (?)").bind("alpha").run();
    const ins2 = env.DB.prepare("INSERT INTO t (name) VALUES (?)").bind("beta").run();
    const all = env.DB.prepare("SELECT id, name FROM t").all();
    const first = env.DB.prepare("SELECT name FROM t").first("name");
    return Response.json({
      lastId: ins2.meta.last_row_id,
      firstInsertChanges: ins1.meta.changes,
      rows: all.results,
      first,
    });
  },
};
`
	handler, err := workers.Register(rt, "db.ts", module, workers.WithDBBackend(db))
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
	got := string(body)

	for _, want := range []string{
		`"lastId":2`,
		`"firstInsertChanges":1`,
		`"name":"alpha"`,
		`"name":"beta"`,
		`"first":"alpha"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in body: %s", want, got)
		}
	}
}

func TestKVListCursorPagination(t *testing.T) {
	rt := newTestRuntime(t)
	kv := newInMemoryKV()

	const module = `
export default {
  async fetch(_req, env) {
    for (const k of ["a","b","c","d","e"]) env.KV.put(k, k);
    const p1 = env.KV.list({ limit: 2 });
    const p2 = env.KV.list({ limit: 2, cursor: p1.cursor });
    const p3 = env.KV.list({ limit: 2, cursor: p2.cursor });
    return Response.json({
      p1: { keys: p1.keys.map(k => k.name), complete: p1.list_complete, cursor: p1.cursor || "" },
      p2: { keys: p2.keys.map(k => k.name), complete: p2.list_complete, cursor: p2.cursor || "" },
      p3: { keys: p3.keys.map(k => k.name), complete: p3.list_complete, cursor: p3.cursor || "" },
    });
  },
};
`
	handler, err := workers.Register(rt, "kv-cursor.ts", module, workers.WithKVBackend(kv))
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
	got := string(body)

	for _, want := range []string{
		`"p1":{"keys":["a","b"],"complete":false,"cursor":"b"}`,
		`"p2":{"keys":["c","d"],"complete":false,"cursor":"d"}`,
		`"p3":{"keys":["e"],"complete":true,"cursor":""}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in body: %s", want, got)
		}
	}
}

func TestBackendsMutuallyExclusiveWithSQLite(t *testing.T) {
	t.Parallel()
	rt := newTestRuntime(t)
	kv := newInMemoryKV()

	_, err := workers.Register(rt, "x.ts",
		`export default { fetch() { return new Response(""); } };`,
		workers.WithSQLite(":memory:"),
		workers.WithKVBackend(kv),
	)
	if err == nil {
		t.Fatal("expected error when combining WithSQLite and WithKVBackend")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error does not mention mutual exclusivity: %v", err)
	}
}
