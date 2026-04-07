//go:build !nosqlite

package ramune_test

import (
	"testing"

	"github.com/i2y/ramune"
)

func newNodeCompatOrSkip(t *testing.T) *ramune.Runtime {
	t.Helper()
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	return r
}

func TestSQLiteBasic(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// Create table, insert, and query.
	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)");
			db.run("INSERT INTO users (name) VALUES (?)", ["Alice"]);
			db.run("INSERT INTO users (name) VALUES (?)", ["Bob"]);
			var rows = db.all("SELECT * FROM users ORDER BY id");
			db.close();
			return JSON.stringify(rows);
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	got := val.String()
	expected := `[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]`
	if got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestSQLiteParams(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// Parameterized query.
	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, price REAL)");
			db.run("INSERT INTO items (name, price) VALUES (?, ?)", ["Widget", 9.99]);
			db.run("INSERT INTO items (name, price) VALUES (?, ?)", ["Gadget", 24.50]);
			var row = db.get("SELECT * FROM items WHERE name = ?", ["Widget"]);
			db.close();
			return JSON.stringify(row);
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	got := val.String()
	expected := `{"id":1,"name":"Widget","price":9.99}`
	if got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestSQLiteMemory(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// In-memory database with default constructor.
	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database();
			db.run("CREATE TABLE test (val TEXT)");
			db.run("INSERT INTO test (val) VALUES (?)", ["hello"]);
			var row = db.get("SELECT val FROM test");
			db.close();
			return row.val;
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	if got := val.String(); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestSQLiteGetNull(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// db.get on empty result returns null.
	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE empty (id INTEGER)");
			var row = db.get("SELECT * FROM empty");
			db.close();
			return row;
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	if !val.IsNull() {
		t.Errorf("expected null for empty result, got %v", val.String())
	}
}

func TestSQLiteRunResult(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// db.run returns changes and lastInsertRowId.
	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE counter (id INTEGER PRIMARY KEY, n INTEGER)");
			var result = db.run("INSERT INTO counter (n) VALUES (?)", [42]);
			db.close();
			return JSON.stringify(result);
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	got := val.String()
	expected := `{"changes":1,"lastInsertRowId":1}`
	if got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestSQLitePrepare(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	// db.prepare returns a statement with run/all/get.
	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE kv (key TEXT, value TEXT)");
			var ins = db.prepare("INSERT INTO kv (key, value) VALUES (?, ?)");
			ins.run("a", "1");
			ins.run("b", "2");
			var sel = db.prepare("SELECT value FROM kv WHERE key = ?");
			var row = sel.get("b");
			db.close();
			return row.value;
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	if got := val.String(); got != "2" {
		t.Errorf("got %q, want %q", got, "2")
	}
}

func TestSQLiteWAL(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			var row = db.get("PRAGMA journal_mode");
			db.close();
			return row.journal_mode;
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	got := val.String()
	// In-memory databases use "memory" journal mode; WAL applies to file-based.
	// Just verify the PRAGMA works without error.
	if got != "memory" && got != "wal" {
		t.Errorf("got %q, want 'memory' or 'wal'", got)
	}
}

func TestSQLiteTransaction(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)");

			var insertMany = db.transaction(function(names) {
				for (var i = 0; i < names.length; i++) {
					db.run("INSERT INTO items (name) VALUES (?)", [names[i]]);
				}
			});
			insertMany(["Alice", "Bob", "Charlie"]);

			var rows = db.all("SELECT name FROM items ORDER BY id");
			db.close();
			return JSON.stringify(rows);
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	expected := `[{"name":"Alice"},{"name":"Bob"},{"name":"Charlie"}]`
	if got := val.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestSQLiteTransactionRollback(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)");
			db.run("INSERT INTO items (name) VALUES (?)", ["existing"]);

			var failInsert = db.transaction(function() {
				db.run("INSERT INTO items (name) VALUES (?)", ["should_rollback"]);
				throw new Error("intentional failure");
			});

			try { failInsert(); } catch(e) {}

			var rows = db.all("SELECT name FROM items");
			db.close();
			return JSON.stringify(rows);
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	expected := `[{"name":"existing"}]`
	if got := val.String(); got != expected {
		t.Errorf("got %s, want %s (rollback failed)", got, expected)
	}
}

func TestSQLiteQuery(t *testing.T) {
	r := newNodeCompatOrSkip(t)
	defer r.Close()

	val, err := r.Eval(`
		(function() {
			var Database = require('bun:sqlite').Database;
			var db = new Database(':memory:');
			db.run("CREATE TABLE kv (key TEXT, value TEXT)");
			db.run("INSERT INTO kv (key, value) VALUES (?, ?)", ["x", "10"]);
			db.run("INSERT INTO kv (key, value) VALUES (?, ?)", ["y", "20"]);

			var q = db.query("SELECT value FROM kv WHERE key = ?");
			var row = q.get("x");
			var all = db.query("SELECT * FROM kv ORDER BY key").all();
			db.close();
			return JSON.stringify({row: row, all: all});
		})()
	`)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	defer val.Close()

	expected := `{"row":{"value":"10"},"all":[{"key":"x","value":"10"},{"key":"y","value":"20"}]}`
	if got := val.String(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}
