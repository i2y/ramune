//go:build !nosqlite

package workers

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/i2y/ramune"

	_ "modernc.org/sqlite"
)

// sqliteDBs caches open *sql.DB handles keyed by the resolved path.
// Multiple Register calls that pass the same SQLitePath share a single
// connection.
var sqliteDBs sync.Map // string → *sharedDB

type sharedDB struct {
	db   *sql.DB
	path string
	once sync.Once
	err  error
}

func openSharedDB(path string) (*sql.DB, error) {
	key := canonicalisePath(path)
	v, _ := sqliteDBs.LoadOrStore(key, &sharedDB{path: key})
	sh := v.(*sharedDB)
	sh.once.Do(func() {
		db, err := sql.Open("sqlite", key)
		if err != nil {
			sh.err = fmt.Errorf("workers: sqlite open %s: %w", key, err)
			return
		}
		if err := db.Ping(); err != nil {
			db.Close()
			sh.err = fmt.Errorf("workers: sqlite ping %s: %w", key, err)
			return
		}
		db.Exec("PRAGMA journal_mode=WAL")
		db.Exec("PRAGMA busy_timeout=5000")
		db.Exec("PRAGMA foreign_keys=ON")
		if _, err := db.Exec(`
			CREATE TABLE IF NOT EXISTS __ramune_kv (
				namespace TEXT NOT NULL,
				key TEXT NOT NULL,
				value TEXT,
				PRIMARY KEY (namespace, key)
			);`); err != nil {
			db.Close()
			sh.err = fmt.Errorf("workers: sqlite init kv table: %w", err)
			return
		}
		sh.db = db
	})
	if sh.err != nil {
		return nil, sh.err
	}
	return sh.db, nil
}

func canonicalisePath(p string) string {
	if p == ":memory:" || strings.HasPrefix(p, "file::memory:") {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// installSQLiteBinds opens the SQLite DB and installs both env.DB and
// env.KV by constructing backends that satisfy DBBackend / KVBackend
// and delegating to installDBBackend / installKVBackend.
func installSQLiteBinds(rt *ramune.Runtime, cfg *Config) error {
	db, err := openSharedDB(cfg.SQLitePath)
	if err != nil {
		return err
	}
	if err := installKVBackend(rt, &sqliteKV{db: db}); err != nil {
		return err
	}
	return installDBBackend(rt, &sqliteSQLBackend{db: db})
}

// OpenSQLiteKV opens (or reuses) the shared SQLite connection at path
// and returns a [KVBackend] backed by it. The __ramune_kv table is
// created on first open.
//
// Intended for tools that need direct access to the same KV store the
// runtime uses — management CLIs, migrations, benchmarks. For the
// runtime path, [WithSQLite] is still the right knob.
func OpenSQLiteKV(path string) (KVBackend, error) {
	db, err := openSharedDB(path)
	if err != nil {
		return nil, err
	}
	return &sqliteKV{db: db}, nil
}

// OpenSQLiteDB opens (or reuses) the shared SQLite connection at path
// and returns a [DBBackend] backed by it. No schema is pre-installed;
// the caller's SQL is responsible.
//
// Intended for openworkers-style platforms that want to wire D1
// bindings at per-database SQLite files independent of the KV path.
func OpenSQLiteDB(path string) (DBBackend, error) {
	db, err := openSharedDB(path)
	if err != nil {
		return nil, err
	}
	return &sqliteSQLBackend{db: db}, nil
}

// sqliteKV is the KVBackend implementation backed by the shared
// __ramune_kv table.
type sqliteKV struct {
	db *sql.DB
}

func (k *sqliteKV) Get(ns, key string) (string, bool, error) {
	row := k.db.QueryRow("SELECT value FROM __ramune_kv WHERE namespace = ? AND key = ?", ns, key)
	var v sql.NullString
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	if !v.Valid {
		return "", false, nil
	}
	return v.String, true, nil
}

func (k *sqliteKV) Put(ns, key, value string) error {
	_, err := k.db.Exec(
		`INSERT INTO __ramune_kv (namespace, key, value) VALUES (?, ?, ?)
		 ON CONFLICT (namespace, key) DO UPDATE SET value = excluded.value`,
		ns, key, value)
	return err
}

func (k *sqliteKV) Delete(ns, key string) error {
	_, err := k.db.Exec("DELETE FROM __ramune_kv WHERE namespace = ? AND key = ?", ns, key)
	return err
}

func (k *sqliteKV) List(ns, prefix, cursor string, limit int) ([]string, string, error) {
	// Fetch limit+1 so we can detect whether another page exists and,
	// if so, return the last key we actually emit as the next cursor.
	fetch := limit + 1
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case prefix == "" && cursor == "":
		rows, err = k.db.Query(
			"SELECT key FROM __ramune_kv WHERE namespace = ? ORDER BY key LIMIT ?",
			ns, fetch)
	case prefix == "":
		rows, err = k.db.Query(
			"SELECT key FROM __ramune_kv WHERE namespace = ? AND key > ? ORDER BY key LIMIT ?",
			ns, cursor, fetch)
	default:
		p := strings.ReplaceAll(prefix, `\`, `\\`)
		p = strings.ReplaceAll(p, `%`, `\%`)
		p = strings.ReplaceAll(p, `_`, `\_`)
		if cursor == "" {
			rows, err = k.db.Query(
				`SELECT key FROM __ramune_kv WHERE namespace = ? AND key LIKE ? ESCAPE '\' ORDER BY key LIMIT ?`,
				ns, p+"%", fetch)
		} else {
			rows, err = k.db.Query(
				`SELECT key FROM __ramune_kv WHERE namespace = ? AND key LIKE ? ESCAPE '\' AND key > ? ORDER BY key LIMIT ?`,
				ns, p+"%", cursor, fetch)
		}
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	keys := make([]string, 0, limit)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, "", err
		}
		keys = append(keys, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if len(keys) > limit {
		keys = keys[:limit]
		nextCursor = keys[len(keys)-1]
	}
	return keys, nextCursor, nil
}

// sqliteSQLBackend is the DBBackend over database/sql + SQLite. Column
// values are normalized to JS-friendly types here so Exec/Query can
// reuse the same coerceRow that user backends get for free.
type sqliteSQLBackend struct {
	db *sql.DB
}

func (s *sqliteSQLBackend) Query(sqlStr string, params []any) ([]map[string]any, error) {
	rows, err := s.db.Query(sqlStr, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	for rows.Next() {
		scanDest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range scanDest {
			ptrs[i] = &scanDest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, name := range cols {
			row[name] = scanDest[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *sqliteSQLBackend) Exec(sqlStr string, params []any) (int64, int64, error) {
	res, err := s.db.Exec(sqlStr, params...)
	if err != nil {
		return 0, 0, err
	}
	changes, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return changes, lastID, nil
}
