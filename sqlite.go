//go:build !nosqlite

package ramune

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// sqliteManager maps integer IDs to open *sql.DB instances.
type sqliteManager struct {
	mu     sync.Mutex
	dbs    map[int]*sql.DB
	nextID int
}

func newSQLiteManager() *sqliteManager {
	return &sqliteManager{
		dbs: make(map[int]*sql.DB),
	}
}

func (m *sqliteManager) open(path string) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, fmt.Errorf("sqlite open: %w", err)
	}
	// Verify the connection works.
	if err := db.Ping(); err != nil {
		db.Close()
		return 0, fmt.Errorf("sqlite ping: %w", err)
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.dbs[id] = db
	m.mu.Unlock()
	return id, nil
}

func (m *sqliteManager) get(id int) (*sql.DB, error) {
	m.mu.Lock()
	db, ok := m.dbs[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("sqlite: unknown database id %d", id)
	}
	return db, nil
}

func (m *sqliteManager) close(id int) error {
	m.mu.Lock()
	db, ok := m.dbs[id]
	if ok {
		delete(m.dbs, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return db.Close()
}

func (m *sqliteManager) closeAll() {
	m.mu.Lock()
	for id, db := range m.dbs {
		db.Close()
		delete(m.dbs, id)
	}
	m.mu.Unlock()
}

// parseParams converts a JSON-encoded parameter array to []any suitable for sql args.
func parseParams(paramsJSON string) ([]any, error) {
	if paramsJSON == "" || paramsJSON == "[]" || paramsJSON == "null" {
		return nil, nil
	}
	var raw []any
	if err := json.Unmarshal([]byte(paramsJSON), &raw); err != nil {
		return nil, fmt.Errorf("sqlite: invalid params JSON: %w", err)
	}
	// Convert float64 values to int64 where they are whole numbers,
	// since JSON numbers are always float64 but SQLite expects integers.
	for i, v := range raw {
		if f, ok := v.(float64); ok {
			if f == float64(int64(f)) {
				raw[i] = int64(f)
			}
		}
	}
	return raw, nil
}

// installSQLite registers bun:sqlite Go callbacks and the JS module.
// Must be called with rt.mu held.
func (r *Runtime) installSQLite() error {
	mgr := newSQLiteManager()
	r.sqliteMgr = mgr

	// __go_sqlite_open(path) → db ID (float64)
	if err := r.registerFuncLocked("__go_sqlite_open", func(args []any) (any, error) {
		path := ":memory:"
		if len(args) >= 1 {
			if s, ok := args[0].(string); ok && s != "" {
				path = s
			}
		}
		id, err := mgr.open(path)
		if err != nil {
			return nil, err
		}
		return float64(id), nil
	}); err != nil {
		return err
	}

	// __go_sqlite_close(dbId)
	if err := r.registerFuncLocked("__go_sqlite_close", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("sqlite close: db id required")
		}
		id, _ := args[0].(float64)
		return nil, mgr.close(int(id))
	}); err != nil {
		return err
	}

	// __go_sqlite_run(dbId, sql, paramsJSON) → JSON {changes, lastInsertRowId}
	if err := r.registerFuncLocked("__go_sqlite_run", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("sqlite run: db id and sql required")
		}
		id, _ := args[0].(float64)
		sqlStr, _ := args[1].(string)
		paramsJSON := "[]"
		if len(args) >= 3 {
			if s, ok := args[2].(string); ok {
				paramsJSON = s
			}
		}

		db, err := mgr.get(int(id))
		if err != nil {
			return nil, err
		}

		params, err := parseParams(paramsJSON)
		if err != nil {
			return nil, err
		}

		result, err := db.Exec(sqlStr, params...)
		if err != nil {
			return nil, fmt.Errorf("sqlite run: %w", err)
		}

		changes, _ := result.RowsAffected()
		lastID, _ := result.LastInsertId()
		resp := map[string]any{
			"changes":         changes,
			"lastInsertRowId": lastID,
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
	}); err != nil {
		return err
	}

	// __go_sqlite_all(dbId, sql, paramsJSON) → JSON array of row objects
	if err := r.registerFuncLocked("__go_sqlite_all", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("sqlite all: db id and sql required")
		}
		id, _ := args[0].(float64)
		sqlStr, _ := args[1].(string)
		paramsJSON := "[]"
		if len(args) >= 3 {
			if s, ok := args[2].(string); ok {
				paramsJSON = s
			}
		}

		db, err := mgr.get(int(id))
		if err != nil {
			return nil, err
		}

		params, err := parseParams(paramsJSON)
		if err != nil {
			return nil, err
		}

		rows, err := db.Query(sqlStr, params...)
		if err != nil {
			return nil, fmt.Errorf("sqlite query: %w", err)
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("sqlite columns: %w", err)
		}

		var results []map[string]any
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, fmt.Errorf("sqlite scan: %w", err)
			}
			row := make(map[string]any, len(cols))
			for i, col := range cols {
				row[col] = normalizeValue(vals[i])
			}
			results = append(results, row)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("sqlite rows: %w", err)
		}

		if results == nil {
			results = []map[string]any{}
		}
		b, _ := json.Marshal(results)
		return string(b), nil
	}); err != nil {
		return err
	}

	// __go_sqlite_get(dbId, sql, paramsJSON) → JSON of first row or ""
	if err := r.registerFuncLocked("__go_sqlite_get", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("sqlite get: db id and sql required")
		}
		id, _ := args[0].(float64)
		sqlStr, _ := args[1].(string)
		paramsJSON := "[]"
		if len(args) >= 3 {
			if s, ok := args[2].(string); ok {
				paramsJSON = s
			}
		}

		db, err := mgr.get(int(id))
		if err != nil {
			return nil, err
		}

		params, err := parseParams(paramsJSON)
		if err != nil {
			return nil, err
		}

		rows, err := db.Query(sqlStr, params...)
		if err != nil {
			return nil, fmt.Errorf("sqlite query: %w", err)
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("sqlite columns: %w", err)
		}

		if !rows.Next() {
			return "", nil // no rows → JS will get null
		}

		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sqlite scan: %w", err)
		}

		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeValue(vals[i])
		}
		b, _ := json.Marshal(row)
		return string(b), nil
	}); err != nil {
		return err
	}

	// Install the JS module.
	return r.execLocked(sqliteJSSource())
}

// normalizeValue converts sql driver values to JSON-friendly types.
// database/sql returns int64, float64, []byte, string, bool, or nil.
func normalizeValue(v any) any {
	switch val := v.(type) {
	case []byte:
		return string(val)
	case int64:
		return val
	case float64:
		return val
	case bool:
		return val
	case string:
		return val
	case nil:
		return nil
	default:
		return fmt.Sprintf("%v", val)
	}
}

func sqliteJSSource() string {
	return `
(function() {
	var _sqliteModule = {
		Database: function Database(path) {
			this._id = __go_sqlite_open(path || ':memory:');
		}
	};

	_sqliteModule.Database.prototype.run = function(sql, params) {
		var result = __go_sqlite_run(this._id, sql, JSON.stringify(params || []));
		return JSON.parse(result);
	};

	_sqliteModule.Database.prototype.all = function(sql, params) {
		var result = __go_sqlite_all(this._id, sql, JSON.stringify(params || []));
		return JSON.parse(result);
	};

	_sqliteModule.Database.prototype.get = function(sql, params) {
		var result = __go_sqlite_get(this._id, sql, JSON.stringify(params || []));
		return result ? JSON.parse(result) : null;
	};

	_sqliteModule.Database.prototype.close = function() {
		__go_sqlite_close(this._id);
	};

	_sqliteModule.Database.prototype.exec = function(sql) {
		__go_sqlite_run(this._id, sql, '[]');
	};

	_sqliteModule.Database.prototype.prepare = function(sql) {
		var self = this;
		return {
			_sql: sql,
			_db: self,
			run: function() {
				var params = Array.prototype.slice.call(arguments);
				return self.run(sql, params);
			},
			all: function() {
				var params = Array.prototype.slice.call(arguments);
				return self.all(sql, params);
			},
			get: function() {
				var params = Array.prototype.slice.call(arguments);
				return self.get(sql, params);
			}
		};
	};

	// Register as bun:sqlite module.
	if (typeof globalThis.require === 'function' && globalThis.require._modules) {
		globalThis.require._modules['bun:sqlite'] = _sqliteModule;
	}
	// Also store on globalThis for direct access and require fallback.
	globalThis.__sqliteModule = _sqliteModule;
})();
`
}
