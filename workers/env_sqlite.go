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

func (k *sqliteKV) List(ns, prefix string, limit int) ([]string, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if prefix == "" {
		rows, err = k.db.Query(
			"SELECT key FROM __ramune_kv WHERE namespace = ? ORDER BY key LIMIT ?",
			ns, limit)
	} else {
		p := strings.ReplaceAll(prefix, `\`, `\\`)
		p = strings.ReplaceAll(p, `%`, `\%`)
		p = strings.ReplaceAll(p, `_`, `\_`)
		rows, err = k.db.Query(
			`SELECT key FROM __ramune_kv WHERE namespace = ? AND key LIKE ? ESCAPE '\' ORDER BY key LIMIT ?`,
			ns, p+"%", limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		keys = append(keys, s)
	}
	return keys, rows.Err()
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
