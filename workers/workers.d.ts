// Workers-style handler types for Ramune.
//
// Copy this file (or add it to your tsconfig.json's "types") when
// authoring Workers-style modules that will run under Ramune:
//
//     export default {
//         async fetch(req, env, ctx) { ... }
//     } satisfies WorkersHandler;
//
// Request / Response / Headers / URL / ReadableStream come from
// TypeScript's built-in DOM or WebWorker libs. If your editor does not
// resolve them, add to tsconfig.json:
//     { "compilerOptions": { "lib": ["ESNext", "WebWorker"] } }

/** Result of a D1 SELECT query. */
interface D1Result<T = Record<string, unknown>> {
  results: T[];
  success: true;
}

/** Result of a D1 write statement (INSERT / UPDATE / DELETE). */
interface D1RunResult {
  success: true;
  meta: {
    changes: number;
    last_row_id: number;
  };
}

/**
 * D1-compatible prepared statement.
 *
 * ```ts
 * const rows = env.DB.prepare("SELECT * FROM posts WHERE author = ?")
 *   .bind("alice")
 *   .all<{ id: string; title: string }>();
 * ```
 */
interface D1PreparedStatement {
  /** Binds positional parameters (replaces ? placeholders in order). */
  bind(...values: unknown[]): this;
  /** Returns all rows from a SELECT query. */
  all<T = Record<string, unknown>>(): D1Result<T>;
  /**
   * Returns the first row (or first column value if colName is given)
   * from a SELECT query, or null if no rows match.
   */
  first<T = unknown>(colName?: string): T | null;
  /** Executes an INSERT / UPDATE / DELETE statement. */
  run(): D1RunResult;
}

/** D1-compatible SQL database facade over Ramune's SQLite integration. */
interface D1Database {
  prepare(sql: string): D1PreparedStatement;
  /** Executes a SQL statement with no parameters. */
  exec(sql: string): D1RunResult;
}

/** Options for KVNamespace.get. */
interface KVNamespaceGetOptions {
  type: "json";
}

/** Options for KVNamespace.list. */
interface KVNamespaceListOptions {
  prefix?: string;
  limit?: number;
}

/** Result of KVNamespace.list. */
interface KVNamespaceListResult {
  keys: Array<{ name: string }>;
}

/**
 * Cloudflare Workers KV-like key/value store, backed by a single
 * SQLite table namespaced by binding name.
 *
 * ```ts
 * env.KV.put("user:42:session", "abc-token");
 * const v = env.KV.get("user:42:session");
 * env.KV.put("config", { theme: "dark" });
 * const cfg = env.KV.get("config", { type: "json" });
 * ```
 */
interface KVNamespace {
  get(key: string): string | null;
  get(key: string, opts: KVNamespaceGetOptions): unknown | null;
  put(key: string, value: string | object): void;
  delete(key: string): void;
  list(opts?: KVNamespaceListOptions): KVNamespaceListResult;
  /** Returns a KVNamespace rooted at a different namespace name. */
  namespace(name: string): KVNamespace;
}

/**
 * Read-only secrets map, populated from environment variables prefixed
 * with RAMUNE_SECRET_ (the prefix is stripped). The prefix is
 * configurable via workers.WithSecretsPrefix on the Go side.
 *
 * ```bash
 * export RAMUNE_SECRET_OPENAI_KEY=sk-...
 * ```
 * ```ts
 * const key = env.SECRETS.OPENAI_KEY; // "sk-..."
 * ```
 */
interface Secrets {
  readonly [key: string]: string;
}

/**
 * The env argument passed to every Workers-style handler. DB and KV
 * only work when Go code opts in via workers.WithSQLite(path); without
 * it, accessing them throws.
 */
interface Env {
  DB: D1Database;
  KV: KVNamespace;
  SECRETS: Secrets;
  [binding: string]: unknown;
}

/** Execution context passed as the third argument to fetch / scheduled. */
interface ExecutionContext {
  /**
   * Extends the handler lifetime until promise settles. The HTTP
   * response is still sent immediately; the executor is held until the
   * promise resolves (or WaitUntilTimeout fires — default 30s).
   *
   * Useful for background work like analytics, cache warming, or log
   * shipping that should not delay the response.
   */
  waitUntil(promise: Promise<unknown>): void;
  /** No-op in Ramune; present for Cloudflare Workers API parity. */
  passThroughOnException(): void;
}

/** Event passed to scheduled handlers. */
interface ScheduledEvent {
  /** Unix timestamp in milliseconds when the event fired. */
  scheduledTime: number;
  /** The cron expression that triggered this event. */
  cron: string;
}

/**
 * The shape of a Workers-style default export.
 *
 * ```ts
 * export default {
 *   route: "/api/hello",
 *   cron: "0 * * * *",
 *   async fetch(request, env, ctx) { ... },
 *   async scheduled(event, env, ctx) { ... },
 * } satisfies WorkersHandler;
 * ```
 */
interface WorkersHandler {
  /** Route pattern (Go net/http ServeMux format). Omit to match all paths. */
  route?: string;
  /** Cron expression. Required if scheduled is defined. */
  cron?: string;
  /** HTTP handler — receives all methods on the declared route. */
  fetch?(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> | Response;
  /** Cron-triggered handler. */
  scheduled?(event: ScheduledEvent, env: Env, ctx: ExecutionContext): Promise<void> | void;
}
