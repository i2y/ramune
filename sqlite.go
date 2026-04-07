//go:build !nosqlite

package ramune

import (
	"container/list"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// stmtCache is an LRU cache for prepared statements.
type stmtCache struct {
	maxSize int
	stmts   map[string]*list.Element
	order   *list.List // front = oldest, back = newest
}

type stmtEntry struct {
	key  string
	stmt *sql.Stmt
}

func newStmtCache(maxSize int) *stmtCache {
	return &stmtCache{
		maxSize: maxSize,
		stmts:   make(map[string]*list.Element),
		order:   list.New(),
	}
}

func (c *stmtCache) get(key string) *sql.Stmt {
	el, ok := c.stmts[key]
	if !ok {
		return nil
	}
	c.order.MoveToBack(el)
	return el.Value.(*stmtEntry).stmt
}

func (c *stmtCache) put(key string, stmt *sql.Stmt) {
	if el, ok := c.stmts[key]; ok {
		c.order.MoveToBack(el)
		return
	}
	if c.order.Len() >= c.maxSize {
		oldest := c.order.Front()
		if oldest != nil {
			e := oldest.Value.(*stmtEntry)
			e.stmt.Close()
			delete(c.stmts, e.key)
			c.order.Remove(oldest)
		}
	}
	el := c.order.PushBack(&stmtEntry{key: key, stmt: stmt})
	c.stmts[key] = el
}

func (c *stmtCache) closeAll() {
	for _, el := range c.stmts {
		el.Value.(*stmtEntry).stmt.Close()
	}
	c.stmts = nil
	c.order = nil
}

// sqliteDB wraps a database connection with transaction and cache state.
type sqliteDB struct {
	db    *sql.DB
	tx    *sql.Tx // nil when no transaction active
	cache *stmtCache
}

// prepareStmt returns a prepared statement, using the cache when possible.
// During a transaction, uncached statements are prepared on the tx directly
// to avoid deadlocking on the single connection held by the tx.
func (sdb *sqliteDB) prepareStmt(sqlStr string) (*sql.Stmt, error) {
	if sdb.tx != nil {
		// In a transaction: check cache and rebind, or prepare on tx.
		if cached := sdb.cache.get(sqlStr); cached != nil {
			return sdb.tx.Stmt(cached), nil
		}
		return sdb.tx.Prepare(sqlStr)
	}
	// Outside transaction: use cache normally.
	if cached := sdb.cache.get(sqlStr); cached != nil {
		return cached, nil
	}
	stmt, err := sdb.db.Prepare(sqlStr)
	if err != nil {
		return nil, err
	}
	sdb.cache.put(sqlStr, stmt)
	return stmt, nil
}

// execStmt executes a non-query statement (INSERT/UPDATE/DELETE).
func (sdb *sqliteDB) execStmt(sqlStr string, params []any) (sql.Result, error) {
	stmt, err := sdb.prepareStmt(sqlStr)
	if err != nil {
		return nil, err
	}
	return stmt.Exec(params...)
}

// queryStmt executes a query statement (SELECT).
func (sdb *sqliteDB) queryStmt(sqlStr string, params []any) (*sql.Rows, error) {
	stmt, err := sdb.prepareStmt(sqlStr)
	if err != nil {
		return nil, err
	}
	return stmt.Query(params...)
}

// sqliteManager maps integer IDs to open database instances.
type sqliteManager struct {
	mu     sync.Mutex
	dbs    map[int]*sqliteDB
	nextID int
}

func newSQLiteManager() *sqliteManager {
	return &sqliteManager{
		dbs: make(map[int]*sqliteDB),
	}
}

func (m *sqliteManager) open(path string) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, fmt.Errorf("sqlite open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return 0, fmt.Errorf("sqlite ping: %w", err)
	}
	// SQLite only supports one writer at a time. Using a single connection
	// also ensures :memory: databases work correctly with transactions.
	db.SetMaxOpenConns(1)
	// Set safe defaults matching Bun behavior.
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")
	db.Exec("PRAGMA foreign_keys=ON")

	sdb := &sqliteDB{
		db:    db,
		cache: newStmtCache(128),
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.dbs[id] = sdb
	m.mu.Unlock()
	return id, nil
}

func (m *sqliteManager) get(id int) (*sqliteDB, error) {
	m.mu.Lock()
	sdb, ok := m.dbs[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("sqlite: unknown database id %d", id)
	}
	return sdb, nil
}

func (m *sqliteManager) begin(id int) error {
	sdb, err := m.get(id)
	if err != nil {
		return err
	}
	if sdb.tx != nil {
		return fmt.Errorf("sqlite: transaction already active")
	}
	tx, err := sdb.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite begin: %w", err)
	}
	sdb.tx = tx
	return nil
}

func (m *sqliteManager) commit(id int) error {
	sdb, err := m.get(id)
	if err != nil {
		return err
	}
	if sdb.tx == nil {
		return fmt.Errorf("sqlite: no active transaction")
	}
	err = sdb.tx.Commit()
	sdb.tx = nil
	return err
}

func (m *sqliteManager) rollback(id int) error {
	sdb, err := m.get(id)
	if err != nil {
		return err
	}
	if sdb.tx == nil {
		return fmt.Errorf("sqlite: no active transaction")
	}
	err = sdb.tx.Rollback()
	sdb.tx = nil
	return err
}

func (m *sqliteManager) close(id int) error {
	m.mu.Lock()
	sdb, ok := m.dbs[id]
	if ok {
		delete(m.dbs, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if sdb.tx != nil {
		sdb.tx.Rollback()
		sdb.tx = nil
	}
	sdb.cache.closeAll()
	return sdb.db.Close()
}

func (m *sqliteManager) closeAll() {
	m.mu.Lock()
	for id, sdb := range m.dbs {
		if sdb.tx != nil {
			sdb.tx.Rollback()
		}
		sdb.cache.closeAll()
		sdb.db.Close()
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

	// __go_sqlite_begin(dbId)
	if err := r.registerFuncLocked("__go_sqlite_begin", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("sqlite begin: db id required")
		}
		id, _ := args[0].(float64)
		return nil, mgr.begin(int(id))
	}); err != nil {
		return err
	}

	// __go_sqlite_commit(dbId)
	if err := r.registerFuncLocked("__go_sqlite_commit", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("sqlite commit: db id required")
		}
		id, _ := args[0].(float64)
		return nil, mgr.commit(int(id))
	}); err != nil {
		return err
	}

	// __go_sqlite_rollback(dbId)
	if err := r.registerFuncLocked("__go_sqlite_rollback", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("sqlite rollback: db id required")
		}
		id, _ := args[0].(float64)
		return nil, mgr.rollback(int(id))
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

		sdb, err := mgr.get(int(id))
		if err != nil {
			return nil, err
		}

		params, err := parseParams(paramsJSON)
		if err != nil {
			return nil, err
		}

		result, err := sdb.execStmt(sqlStr, params)
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
		return sqliteQuery(mgr, args, false)
	}); err != nil {
		return err
	}

	// __go_sqlite_get(dbId, sql, paramsJSON) → JSON of first row or ""
	if err := r.registerFuncLocked("__go_sqlite_get", func(args []any) (any, error) {
		return sqliteQuery(mgr, args, true)
	}); err != nil {
		return err
	}

	// Install the JS module.
	return r.execLocked(sqliteJSSource())
}

// sqliteQuery handles both "all" (singleRow=false) and "get" (singleRow=true) queries.
func sqliteQuery(mgr *sqliteManager, args []any, singleRow bool) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("sqlite query: db id and sql required")
	}
	id, _ := args[0].(float64)
	sqlStr, _ := args[1].(string)
	paramsJSON := "[]"
	if len(args) >= 3 {
		if s, ok := args[2].(string); ok {
			paramsJSON = s
		}
	}

	sdb, err := mgr.get(int(id))
	if err != nil {
		return nil, err
	}

	params, err := parseParams(paramsJSON)
	if err != nil {
		return nil, err
	}

	rows, err := sdb.queryStmt(sqlStr, params)
	if err != nil {
		return nil, fmt.Errorf("sqlite query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("sqlite columns: %w", err)
	}

	if singleRow {
		if !rows.Next() {
			return "", nil
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

	_sqliteModule.Database.prototype.query = function(sql) {
		var self = this;
		return {
			all: function() { return self.all(sql, Array.prototype.slice.call(arguments)); },
			get: function() { return self.get(sql, Array.prototype.slice.call(arguments)); },
			run: function() { return self.run(sql, Array.prototype.slice.call(arguments)); },
			values: function() { return self.all(sql, Array.prototype.slice.call(arguments)); }
		};
	};

	_sqliteModule.Database.prototype.transaction = function(fn) {
		var self = this;
		return function() {
			__go_sqlite_begin(self._id);
			try {
				var result = fn.apply(this, arguments);
				__go_sqlite_commit(self._id);
				return result;
			} catch(e) {
				__go_sqlite_rollback(self._id);
				throw e;
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
