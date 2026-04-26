package workers

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/i2y/ramune"
)

// KVBackend is the contract for an env.KV storage backend.
//
// Methods operate on flat string values within a namespace. Implementations
// must be safe for concurrent use; each JS call into env.KV may run on a
// distinct goroutine.
//
// Passing a KVBackend via WithKVBackend replaces the env.KV facade for the
// Runtime. WithSQLite already provides a KVBackend internally; supplying
// both WithSQLite and WithKVBackend is rejected to avoid hidden precedence.
type KVBackend interface {
	// Get returns the value stored at (ns, key). ok is false when the
	// key does not exist; err is reserved for infrastructure failures.
	Get(ns, key string) (value string, ok bool, err error)
	// Put writes value at (ns, key), overwriting any existing value.
	Put(ns, key, value string) error
	// Delete removes (ns, key). Deleting a missing key must succeed.
	Delete(ns, key string) error
	// List returns up to limit keys within ns whose name starts with
	// prefix (empty prefix = all). Keys must be sorted ascending.
	//
	// cursor is an opaque position marker from a prior call; empty
	// means start from the beginning. nextCursor is empty when the
	// list has been exhausted, otherwise it is the opaque marker the
	// caller passes to resume.
	List(ns, prefix, cursor string, limit int) (keys []string, nextCursor string, err error)
}

// DBBackend is the contract for an env.DB SQL backend.
//
// SQL is passed through verbatim with ?-style positional parameters
// (D1 convention). Backends needing $1/$2 syntax (Postgres) should
// rewrite internally. Implementations must be safe for concurrent use.
//
// Passing a DBBackend via WithDBBackend replaces the env.DB facade.
// WithSQLite + WithDBBackend is rejected.
type DBBackend interface {
	// Query runs a read statement (SELECT) and returns every row as a
	// map keyed by column name. Values use JS-friendly Go types:
	// string, float64, bool, nil, []byte (coerced to string at the
	// boundary).
	Query(sql string, params []any) (rows []map[string]any, err error)
	// Exec runs a write statement. changes is the rows-affected count,
	// lastInsertID is the last auto-increment id, or 0 if unsupported.
	Exec(sql string, params []any) (changes, lastInsertID int64, err error)
}

// BlobBackend is the contract for env.<bucket> object-storage bindings.
//
// JS API surface (env.BUCKET.get / put / delete / list) matches the
// shape of Cloudflare R2 but is intentionally minimal: HTTPMetadata,
// custom metadata, multipart uploads, conditional writes, signed URLs,
// lifecycle policies, and S3 compatibility are NOT part of this
// interface. Worker code that depends on those features will not be
// portable. The interface is intended to make basic put/get/list code
// work the same on chinotto and on Cloudflare; nothing more.
type BlobBackend interface {
	// Get returns the body bytes for (bucket, key). exists is false
	// when the key has no entry. err is reserved for infrastructure
	// failures.
	Get(bucket, key string) (body []byte, info BlobObjectInfo, exists bool, err error)
	// Put writes body at (bucket, key), overwriting any existing
	// entry, and returns the resulting metadata.
	Put(bucket, key string, body []byte) (BlobObjectInfo, error)
	// Delete removes (bucket, key). Deleting a missing key must
	// succeed.
	Delete(bucket, key string) error
	// List returns up to limit objects whose key starts with prefix
	// within bucket, sorted ascending by key. cursor is opaque (empty
	// = start). nextCursor is empty when the list is exhausted.
	List(bucket, prefix, cursor string, limit int) (objects []BlobObjectInfo, nextCursor string, err error)
}

// BlobObjectInfo carries the metadata an BlobBackend implementation can
// surface alongside a key. Backends that do not track a particular
// field should leave it zero-valued.
type BlobObjectInfo struct {
	Key      string
	Size     int64
	ETag     string
	Uploaded time.Time
}

// WithKVBackend installs a pluggable storage backend for env.KV.
// Cannot be combined with WithSQLite.
func WithKVBackend(b KVBackend) Option {
	return func(c *Config) { c.KVBackend = b }
}

// WithDBBackend installs a pluggable SQL backend for env.DB.
// Cannot be combined with WithSQLite.
func WithDBBackend(b DBBackend) Option {
	return func(c *Config) { c.DBBackend = b }
}

// WithBlobBackend installs a pluggable object-storage backend. The
// bucket name appears as the first argument to each interface method,
// so a single backend can multiplex across buckets.
func WithBlobBackend(b BlobBackend) Option {
	return func(c *Config) { c.BlobBackend = b }
}

// validateBackends enforces the mutual-exclusivity rule between
// WithSQLite and the typed backend options. Returns nil for valid
// combinations.
func validateBackends(cfg *Config) error {
	if cfg.SQLitePath != "" && cfg.KVBackend != nil {
		return fmt.Errorf("workers: WithSQLite and WithKVBackend are mutually exclusive; use one or the other for env.KV")
	}
	if cfg.SQLitePath != "" && cfg.DBBackend != nil {
		return fmt.Errorf("workers: WithSQLite and WithDBBackend are mutually exclusive; use one or the other for env.DB")
	}
	return nil
}

// installKVBackend registers the four __env_kv_* callbacks that back
// the env.KV facade and installs a __buildEnvKV JS implementation that
// calls into them. Reused by the SQLite path and by user-supplied
// KVBackend values.
func installKVBackend(rt *ramune.Runtime, b KVBackend) error {
	if err := regFunc(rt, "__env_kv_get", func(args []any) (any, error) {
		ns, _ := args[0].(string)
		key, _ := args[1].(string)
		if ns == "" || key == "" {
			return nil, nil
		}
		v, ok, err := b.Get(ns, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return v, nil
	}); err != nil {
		return err
	}
	if err := regFunc(rt, "__env_kv_put", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("__env_kv_put: namespace, key, value required")
		}
		ns, _ := args[0].(string)
		key, _ := args[1].(string)
		val, _ := args[2].(string)
		if ns == "" || key == "" {
			return nil, fmt.Errorf("__env_kv_put: namespace and key must be non-empty")
		}
		return nil, b.Put(ns, key, val)
	}); err != nil {
		return err
	}
	if err := regFunc(rt, "__env_kv_delete", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, nil
		}
		ns, _ := args[0].(string)
		key, _ := args[1].(string)
		if ns == "" || key == "" {
			return nil, nil
		}
		return nil, b.Delete(ns, key)
	}); err != nil {
		return err
	}
	if err := regFunc(rt, "__env_kv_list", func(args []any) (any, error) {
		emptyList := map[string]any{
			"keys":          []any{},
			"list_complete": true,
		}
		if len(args) < 1 {
			return emptyList, nil
		}
		ns, _ := args[0].(string)
		if ns == "" {
			return emptyList, nil
		}
		prefix := ""
		if len(args) > 1 {
			prefix, _ = args[1].(string)
		}
		cursor := ""
		if len(args) > 2 {
			cursor, _ = args[2].(string)
		}
		limit := 1000
		if len(args) > 3 {
			if n, ok := toInt64(args[3]); ok && n > 0 {
				limit = int(n)
			}
		}
		keys, nextCursor, err := b.List(ns, prefix, cursor, limit)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, map[string]any{"name": k})
		}
		result := map[string]any{
			"keys":          out,
			"list_complete": nextCursor == "",
		}
		if nextCursor != "" {
			result["cursor"] = nextCursor
		}
		return result, nil
	}); err != nil {
		return err
	}
	return rt.Exec(kvBuilderJS)
}

// installBlobBackend registers the four __env_blob_* callbacks and
// installs a __buildEnvBlob JS implementation. Body bytes cross the
// boundary as base64 strings to keep the wire format simple and
// backend-agnostic; user code never sees the encoding because the
// JS facade returns a body wrapper with arrayBuffer() / text() /
// json() helpers.
func installBlobBackend(rt *ramune.Runtime, b BlobBackend) error {
	if err := regFunc(rt, "__env_blob_get", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("__env_blob_get: bucket, key required")
		}
		bucket, _ := args[0].(string)
		key, _ := args[1].(string)
		if bucket == "" || key == "" {
			return nil, nil
		}
		body, info, ok, err := b.Get(bucket, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		etag := info.ETag
		if etag == "" && body != nil {
			sum := md5.Sum(body)
			etag = hex.EncodeToString(sum[:])
		}
		size := info.Size
		if size == 0 {
			size = int64(len(body))
		}
		out := map[string]any{
			"key":      info.Key,
			"size":     float64(size),
			"etag":     etag,
			"body_b64": base64.StdEncoding.EncodeToString(body),
		}
		if !info.Uploaded.IsZero() {
			out["uploaded"] = info.Uploaded.UTC().Format(time.RFC3339)
		}
		return out, nil
	}); err != nil {
		return err
	}

	if err := regFunc(rt, "__env_blob_put", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("__env_blob_put: bucket, key, body_b64 required")
		}
		bucket, _ := args[0].(string)
		key, _ := args[1].(string)
		bodyB64, _ := args[2].(string)
		if bucket == "" || key == "" {
			return nil, fmt.Errorf("__env_blob_put: bucket and key must be non-empty")
		}
		body, err := base64.StdEncoding.DecodeString(bodyB64)
		if err != nil {
			return nil, fmt.Errorf("__env_blob_put: body_b64: %w", err)
		}
		info, err := b.Put(bucket, key, body)
		if err != nil {
			return nil, err
		}
		etag := info.ETag
		if etag == "" {
			sum := md5.Sum(body)
			etag = hex.EncodeToString(sum[:])
		}
		size := info.Size
		if size == 0 {
			size = int64(len(body))
		}
		out := map[string]any{
			"key":  info.Key,
			"size": float64(size),
			"etag": etag,
		}
		if !info.Uploaded.IsZero() {
			out["uploaded"] = info.Uploaded.UTC().Format(time.RFC3339)
		}
		return out, nil
	}); err != nil {
		return err
	}

	if err := regFunc(rt, "__env_blob_delete", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, nil
		}
		bucket, _ := args[0].(string)
		key, _ := args[1].(string)
		if bucket == "" || key == "" {
			return nil, nil
		}
		return nil, b.Delete(bucket, key)
	}); err != nil {
		return err
	}

	if err := regFunc(rt, "__env_blob_list", func(args []any) (any, error) {
		if len(args) < 1 {
			return map[string]any{"objects": []any{}, "truncated": false}, nil
		}
		bucket, _ := args[0].(string)
		if bucket == "" {
			return map[string]any{"objects": []any{}, "truncated": false}, nil
		}
		prefix := ""
		if len(args) > 1 {
			prefix, _ = args[1].(string)
		}
		cursor := ""
		if len(args) > 2 {
			cursor, _ = args[2].(string)
		}
		limit := 1000
		if len(args) > 3 {
			if n, ok := toInt64(args[3]); ok && n > 0 {
				limit = int(n)
			}
		}
		objects, nextCursor, err := b.List(bucket, prefix, cursor, limit)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(objects))
		for _, o := range objects {
			row := map[string]any{
				"key":  o.Key,
				"size": float64(o.Size),
				"etag": o.ETag,
			}
			if !o.Uploaded.IsZero() {
				row["uploaded"] = o.Uploaded.UTC().Format(time.RFC3339)
			}
			out = append(out, row)
		}
		result := map[string]any{
			"objects":   out,
			"truncated": nextCursor != "",
		}
		if nextCursor != "" {
			result["cursor"] = nextCursor
		}
		return result, nil
	}); err != nil {
		return err
	}

	return rt.Exec(blobBuilderJS)
}

// installDBBackend registers __env_db_exec and installs a __buildEnvDB
// implementation that routes calls through it. Reused by SQLite and
// user-supplied DBBackend values.
func installDBBackend(rt *ramune.Runtime, b DBBackend) error {
	if err := regFunc(rt, "__env_db_exec", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("__env_db_exec: sql required")
		}
		sqlStr, _ := args[0].(string)
		if sqlStr == "" {
			return nil, fmt.Errorf("__env_db_exec: sql must be non-empty")
		}
		var params []any
		if len(args) > 1 {
			if p, ok := args[1].([]any); ok {
				params = p
			}
		}
		isQuery := true
		if len(args) > 2 {
			if v, ok := args[2].(bool); ok {
				isQuery = v
			}
		}
		if isQuery {
			rows, err := b.Query(sqlStr, params)
			if err != nil {
				return nil, err
			}
			results := make([]any, 0, len(rows))
			for _, row := range rows {
				results = append(results, coerceRow(row))
			}
			return map[string]any{
				"results": results,
				"success": true,
			}, nil
		}
		changes, lastID, err := b.Exec(sqlStr, params)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"success": true,
			"meta": map[string]any{
				"changes":     float64(changes),
				"last_row_id": float64(lastID),
			},
		}, nil
	}); err != nil {
		return err
	}
	return rt.Exec(dbBuilderJS)
}

// coerceRow normalizes a row's values so they cross the Ramune boundary
// as JS-friendly types. []byte becomes string (SQLite TEXT round-trip),
// int64 becomes float64. Other types pass through.
func coerceRow(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		switch t := v.(type) {
		case []byte:
			out[k] = string(t)
		case int64:
			out[k] = float64(t)
		default:
			out[k] = v
		}
	}
	return out
}

// kvBuilderJS replaces the env.KV stub with a facade that delegates to
// the Go-side callbacks installed by installKVBackend.
const kvBuilderJS = `
(function() {
	globalThis.__buildEnvKV = function(namespace) {
		var ns = namespace || "default";
		var kv;
		kv = {
			get: function(key, opts) {
				var v = __env_kv_get(ns, key);
				if (v === null || v === undefined) return null;
				if (opts && opts.type === "json") {
					try { return JSON.parse(v); } catch (e) { return null; }
				}
				return v;
			},
			put: function(key, value) {
				if (value && typeof value === "object") {
					value = JSON.stringify(value);
				} else {
					value = String(value);
				}
				return __env_kv_put(ns, key, value);
			},
			delete: function(key) {
				return __env_kv_delete(ns, key);
			},
			list: function(opts) {
				opts = opts || {};
				return __env_kv_list(ns, opts.prefix || "", opts.cursor || "", opts.limit || 1000);
			},
			namespace: function(name) {
				return __buildEnvKV(name);
			},
		};
		return kv;
	};
})();
`

// blobBuilderJS installs __buildEnvBlob, returning a per-bucket
// facade with put/get/delete/list. Body bytes round-trip through
// base64 strings on the Go boundary; the get() return wraps them
// with arrayBuffer / text / json helpers so user code matches the
// Cloudflare R2 ObjectBody shape closely enough for handler
// portability.
const blobBuilderJS = `
(function() {
	function _b64encode(bytes) {
		var s = "";
		for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
		return globalThis.btoa ? globalThis.btoa(s) : Buffer.from(s, "binary").toString("base64");
	}
	function _b64decode(b64) {
		var bin = globalThis.atob ? globalThis.atob(b64) : Buffer.from(b64, "base64").toString("binary");
		var out = new Uint8Array(bin.length);
		for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
		return out;
	}
	async function _coerceBody(value) {
		if (value == null) return new Uint8Array(0);
		if (typeof value === "string") return new TextEncoder().encode(value);
		if (value instanceof Uint8Array) return value;
		if (value instanceof ArrayBuffer) return new Uint8Array(value);
		if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
		if (typeof value.arrayBuffer === "function") return new Uint8Array(await value.arrayBuffer());
		throw new TypeError("env.<bucket>.put: body must be string, ArrayBuffer, ArrayBufferView, or Blob-like");
	}
	function _wrapObject(meta, bytes) {
		return {
			key: meta.key,
			size: meta.size,
			etag: meta.etag,
			uploaded: meta.uploaded || null,
			arrayBuffer: function() {
				return Promise.resolve(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength));
			},
			text: function() {
				return Promise.resolve(new TextDecoder().decode(bytes));
			},
			json: function() {
				return Promise.resolve(JSON.parse(new TextDecoder().decode(bytes)));
			},
		};
	}
	globalThis.__buildEnvBlob = function(bucket) {
		var b = bucket;
		return {
			put: async function(key, value) {
				var bytes = await _coerceBody(value);
				var meta = __env_blob_put(b, key, _b64encode(bytes));
				return meta;
			},
			get: async function(key) {
				var raw = __env_blob_get(b, key);
				if (raw === null || raw === undefined) return null;
				var bytes = _b64decode(raw.body_b64);
				return _wrapObject(raw, bytes);
			},
			delete: function(key) {
				return Promise.resolve(__env_blob_delete(b, key));
			},
			list: function(opts) {
				opts = opts || {};
				return Promise.resolve(__env_blob_list(b, opts.prefix || "", opts.cursor || "", opts.limit || 1000));
			},
		};
	};
})();
`

// dbBuilderJS replaces the env.DB stub with a D1-compatible facade.
const dbBuilderJS = `
(function() {
	globalThis.__buildEnvDB = function() {
		return {
			prepare: function(sql) {
				var _sql = sql;
				var _params = [];
				return {
					bind: function() {
						_params = Array.prototype.slice.call(arguments);
						return this;
					},
					all: function() {
						return __env_db_exec(_sql, _params, true);
					},
					first: function(colName) {
						var r = __env_db_exec(_sql + " LIMIT 1", _params, true);
						var row = r && r.results && r.results[0];
						if (!row) return null;
						return colName ? row[colName] : row;
					},
					run: function() {
						return __env_db_exec(_sql, _params, false);
					},
				};
			},
			exec: function(sql) {
				return __env_db_exec(sql, [], false);
			},
		};
	};
})();
`
