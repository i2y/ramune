package ramune

import (
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
)

// NodeCompat installs a minimal Node.js compatibility layer into the JSC
// context. This enables npm packages that depend on Node.js built-ins to
// run in JSC with Go providing the native functionality.
//
// Supported polyfills:
//   - require() — returns polyfilled modules
//   - process.env, process.cwd(), process.platform, process.arch
//   - child_process.spawnSync / execSync (synchronous only)
//   - fs.readFileSync, fs.existsSync, fs.writeFileSync, fs.mkdirSync
//   - path.join, path.resolve, path.dirname, path.basename, path.sep
//   - Buffer.from (basic)
//   - console.log, console.error, console.warn
//   - setTimeout (immediate execution, no real delay)
func NodeCompat() Option {
	return func(c *config) {
		c.nodeCompat = true
	}
}

// installNodeCompat sets up the Node.js compatibility layer.
// Must be called with rt.mu held.
func (r *Runtime) installNodeCompat() error {
	// Register Go-backed functions.
	if err := r.registerFuncLocked("__go_read_file", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("readFileSync: path required")
		}
		path, _ := args[0].(string)
		if err := r.perms.CheckRead(path); err != nil {
			return nil, err
		}
		return goReadFile(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_write_file", func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("writeFileSync: path and data required")
		}
		path, _ := args[0].(string)
		if err := r.perms.CheckWrite(path); err != nil {
			return nil, err
		}
		return goWriteFile(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_file_exists", goFileExists); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_mkdir", goMkdir); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_environ", func(args []any) (any, error) {
		if r.perms != nil && r.perms.Env == PermDenied && len(r.perms.EnvVars) == 0 {
			return "{}", nil // sandbox: return empty env
		}
		return goEnviron(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_getenv", func(args []any) (any, error) {
		if len(args) >= 1 {
			if key, ok := args[0].(string); ok {
				if err := r.perms.CheckEnv(key); err != nil {
					return "", err
				}
			}
		}
		return goGetenv(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_cwd", goCwd); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_join", goPathJoin); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_resolve", goPathResolve); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_dirname", goPathDirname); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_basename", goPathBasename); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_relative", goPathRelative); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_normalize", goPathNormalize); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_path_is_absolute", goPathIsAbsolute); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_spawn_sync", func(args []any) (any, error) {
		if len(args) >= 1 {
			if cmd, ok := args[0].(string); ok {
				if err := r.perms.CheckRun(cmd); err != nil {
					return nil, err
				}
			}
		}
		return goSpawnSync(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_exec_sync", func(args []any) (any, error) {
		if len(args) >= 1 {
			if cmd, ok := args[0].(string); ok {
				if err := r.perms.CheckRun(cmd); err != nil {
					return nil, err
				}
			}
		}
		return goExecSync(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_readdir", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckRead(p); err != nil {
					return nil, err
				}
			}
		}
		return goReaddir(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_stat", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckRead(p); err != nil {
					return nil, err
				}
			}
		}
		return goStat(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_realpath", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckRead(p); err != nil {
					return nil, err
				}
			}
		}
		return goRealpath(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_access", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckRead(p); err != nil {
					return nil, err
				}
			}
		}
		return goAccess(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_copy_file", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckRead(p); err != nil {
					return nil, err
				}
			}
		}
		if len(args) >= 2 {
			if p, ok := args[1].(string); ok {
				if err := r.perms.CheckWrite(p); err != nil {
					return nil, err
				}
			}
		}
		return goCopyFile(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_rm", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckWrite(p); err != nil {
					return nil, err
				}
			}
		}
		return goRm(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_exec_file_sync", func(args []any) (any, error) {
		if len(args) >= 1 {
			if cmd, ok := args[0].(string); ok {
				if err := r.perms.CheckRun(cmd); err != nil {
					return nil, err
				}
			}
		}
		return goExecFileSync(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_append_file", func(args []any) (any, error) {
		if len(args) >= 1 {
			if p, ok := args[0].(string); ok {
				if err := r.perms.CheckWrite(p); err != nil {
					return nil, err
				}
			}
		}
		return goAppendFile(args)
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_zlib_gzip", goZlibGzip); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_zlib_gunzip", goZlibGunzip); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_zlib_deflate", goZlibDeflate); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_zlib_inflate", goZlibInflate); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_zlib_brotli_compress", goZlibBrotliCompress); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_zlib_brotli_decompress", goZlibBrotliDecompress); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_process_exit", func(args []any) (any, error) {
		code := 0
		if len(args) > 0 {
			if c, ok := args[0].(float64); ok {
				code = int(c)
			}
		}
		os.Exit(code)
		return nil, nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_os_num_cpus", func(args []any) (any, error) {
		return float64(goruntime.NumCPU()), nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_pid", func(args []any) (any, error) {
		return float64(os.Getpid()), nil
	}); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_os_hostname", goOsHostname); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_os_userinfo", goOsUserInfo); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_url_parse", goUrlParse); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_chmod", goChmod); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_rename", goRename); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_cp_sync", goCpSync); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_symlink", goSymlink); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_readlink", goReadlink); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_hrtime", goHrtime); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_cipher", goCryptoCipher); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_decipher", goCryptoDecipher); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_random_int", goCryptoRandomInt); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_scrypt", goCryptoScrypt); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_pbkdf2", goCryptoPbkdf2); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_random_bytes", goCryptoRandomBytes); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_hash", goCryptoHash); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_hmac", goCryptoHmac); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_sign", goCryptoSign); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_verify", goCryptoVerify); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_crypto_generate_key_pair", goCryptoGenerateKeyPair); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tty_isatty", goTTYIsatty); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_tty_getsize", goTTYGetSize); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_resolve_module", r.goResolveModuleFunc()); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_load_module", r.goLoadModuleFunc()); err != nil {
		return err
	}

	if err := r.execLocked(nodeCompatJSSource()); err != nil {
		return err
	}

	// Install DNS module (registers into require._modules set up above).
	if err := r.installDNS(); err != nil {
		return err
	}

	return nil
}

// Go callback implementations are in:
//   nodecompat_fs.go     — filesystem operations
//   nodecompat_path.go   — path, env, cwd, hrtime
//   nodecompat_crypto.go — crypto, zlib
//   nodecompat_misc.go   — os, url, spawn, exec

func nodeCompatJSSource() string {
	src := `
(function() {
	// --- path ---
	var path = {
		join: function() {
			var args = Array.prototype.slice.call(arguments);
			return __go_path_join.apply(null, args);
		},
		resolve: function() {
			var args = Array.prototype.slice.call(arguments);
			return __go_path_resolve.apply(null, args);
		},
		dirname: function(p) { return __go_path_dirname(p); },
		basename: function(p, ext) {
			var b = __go_path_basename(p);
			if (ext && b.endsWith(ext)) b = b.slice(0, -ext.length);
			return b;
		},
		relative: function(from, to) { return __go_path_relative(from, to); },
		normalize: function(p) { return __go_path_normalize(p); },
		isAbsolute: function(p) { return __go_path_is_absolute(p); },
		sep: '/',
		delimiter: ':',
		posix: null, // set below
		win32: null,
		extname: function(p) {
			var base = __go_path_basename(p);
			var i = base.lastIndexOf('.');
			if (i <= 0) return '';
			return base.slice(i);
		},
		parse: function(p) {
			var dir = __go_path_dirname(p);
			var base = __go_path_basename(p);
			var ext = path.extname(p);
			var name = ext ? base.slice(0, -ext.length) : base;
			var root = p.charAt(0) === '/' ? '/' : '';
			return { root: root, dir: dir, base: base, ext: ext, name: name };
		},
		format: function(obj) {
			var dir = obj.dir || obj.root || '';
			var base = obj.base || ((obj.name || '') + (obj.ext || ''));
			if (dir && dir !== obj.root) return dir + '/' + base;
			return dir + base;
		}
	};
	path.posix = path;

	// --- fs ---
	var fs = {
		readFileSync: function(p, opts) {
			var result = __go_read_file(String(p));
			if (opts === 'utf8' || opts === 'utf-8' || (opts && opts.encoding)) {
				return result;
			}
			return { toString: function() { return result; } };
		},
		writeFileSync: function(p, data) {
			__go_write_file(String(p), String(data));
		},
		appendFileSync: function(p, data) {
			__go_append_file(String(p), String(data));
		},
		existsSync: function(p) {
			return __go_file_exists(String(p));
		},
		mkdirSync: function(p, opts) {
			__go_mkdir(String(p));
		},
		readdirSync: function(p, opts) {
			var withFileTypes = opts && opts.withFileTypes;
			var raw = __go_readdir(String(p), withFileTypes ? 'true' : 'false');
			var entries = JSON.parse(raw);
			if (withFileTypes) {
				return entries.map(function(e) {
					return {
						name: e.name,
						isFile: function() { return e.isFile; },
						isDirectory: function() { return e.isDirectory; }
					};
				});
			}
			return entries;
		},
		statSync: function(p) {
			try {
				var raw = __go_stat(String(p));
				var s = JSON.parse(raw);
				return {
					isFile: function() { return s.isFile; },
					isDirectory: function() { return s.isDirectory; },
					size: s.size,
					mtimeMs: s.mtimeMs,
					mtime: new Date(s.mtimeMs)
				};
			} catch(e) {
				var err = new Error('ENOENT: no such file or directory: ' + p);
				err.code = 'ENOENT';
				throw err;
			}
		},
		lstatSync: function(p) { return this.statSync(p); },
		unlinkSync: function(p) { __go_rm(String(p), 'false'); },
		realpathSync: function(p) { return __go_realpath(String(p)); },
		accessSync: function(p) { __go_access(String(p)); },
		copyFileSync: function(src, dest) { __go_copy_file(String(src), String(dest)); },
		rmSync: function(p, opts) {
			var recursive = (opts && opts.recursive) ? 'true' : 'false';
			__go_rm(String(p), recursive);
		},
		chmodSync: function(p, mode) { __go_chmod(String(p), mode); },
		symlinkSync: function(target, p) { __go_symlink(String(target), String(p)); },
		readlinkSync: function(p) { return __go_readlink(String(p)); },
		createReadStream: function(p, opts) {
			var rs = new (_modules['stream'].Readable)({ read: function() {} });
			try {
				var content = __go_read_file(String(p));
				// Chunk into 4096-byte pieces.
				var chunkSize = (opts && opts.highWaterMark) || 4096;
				for (var i = 0; i < content.length; i += chunkSize) {
					rs.push(content.slice(i, i + chunkSize));
				}
				rs.push(null);
			} catch(e) { rs.emit('error', e); }
			return rs;
		},
		createWriteStream: function(p) {
			var ws = new (_modules['stream'].Writable)({
				write: function(chunk, enc, cb) { cb(); }
			});
			var origEnd = ws.end.bind(ws);
			ws.end = function(chunk, enc, cb) {
				if (chunk) ws._chunks.push(chunk);
				try { __go_write_file(String(p), ws._chunks.join('')); }
				catch(e) { ws.emit('error', e); }
				return origEnd(undefined, enc, cb);
			};
			return ws;
		},
		renameSync: function(oldPath, newPath) {
			__go_rename(String(oldPath), String(newPath));
		},
		cpSync: function(src, dest) {
			__go_cp_sync(String(src), String(dest));
		}
	};
	// Async callback versions — wrap sync operations in setTimeout(cb, 0).
	fs.readFile = function(path, opts, cb) {
		if (typeof opts === 'function') { cb = opts; opts = 'utf8'; }
		try { var data = fs.readFileSync(path, opts); if (cb) setTimeout(function() { cb(null, data); }, 0); }
		catch(e) { if (cb) setTimeout(function() { cb(e); }, 0); }
	};
	fs.writeFile = function(path, data, opts, cb) {
		if (typeof opts === 'function') { cb = opts; opts = {}; }
		try { fs.writeFileSync(path, data, opts); if (cb) setTimeout(function() { cb(null); }, 0); }
		catch(e) { if (cb) setTimeout(function() { cb(e); }, 0); }
	};
	fs.stat = function(path, cb) {
		try { var s = fs.statSync(path); if (cb) setTimeout(function() { cb(null, s); }, 0); }
		catch(e) { if (cb) setTimeout(function() { cb(e); }, 0); }
	};
	fs.readdir = function(path, opts, cb) {
		if (typeof opts === 'function') { cb = opts; opts = {}; }
		try { var entries = fs.readdirSync(path, opts); if (cb) setTimeout(function() { cb(null, entries); }, 0); }
		catch(e) { if (cb) setTimeout(function() { cb(e); }, 0); }
	};
	fs.unlink = function(path, cb) {
		try { fs.rmSync(path); if (cb) setTimeout(function() { cb(null); }, 0); }
		catch(e) { if (cb) setTimeout(function() { cb(e); }, 0); }
	};
	// fs.promises — Promise wrappers around sync implementations.
	fs.promises = {
		readFile: function(p, opts) { try { return Promise.resolve(fs.readFileSync(p, opts)); } catch(e) { return Promise.reject(e); } },
		writeFile: function(p, data) { try { fs.writeFileSync(p, data); return Promise.resolve(); } catch(e) { return Promise.reject(e); } },
		access: function(p) { try { fs.accessSync(p); return Promise.resolve(); } catch(e) { return Promise.reject(e); } },
		stat: function(p) { try { return Promise.resolve(fs.statSync(p)); } catch(e) { return Promise.reject(e); } },
		readdir: function(p, opts) { try { return Promise.resolve(fs.readdirSync(p, opts)); } catch(e) { return Promise.reject(e); } },
		mkdir: function(p, opts) { try { fs.mkdirSync(p, opts); return Promise.resolve(); } catch(e) { return Promise.reject(e); } },
		rm: function(p, opts) { try { fs.rmSync(p, opts); return Promise.resolve(); } catch(e) { return Promise.reject(e); } },
		copyFile: function(src, dest) { try { fs.copyFileSync(src, dest); return Promise.resolve(); } catch(e) { return Promise.reject(e); } },
		rename: function(o, n) { try { fs.renameSync(o, n); return Promise.resolve(); } catch(e) { return Promise.reject(e); } },
		appendFile: function(p, data) { try { fs.appendFileSync(p, data); return Promise.resolve(); } catch(e) { return Promise.reject(e); } }
	};

	// --- child_process ---
	var child_process = {
		spawnSync: function(cmd, args, opts) {
			var argsJSON = args ? JSON.stringify(args) : '[]';
			var optsJSON = opts ? JSON.stringify({cwd: opts.cwd, env: opts.env, input: opts.input ? String(opts.input) : ''}) : '{}';
			var result = JSON.parse(__go_spawn_sync(cmd, argsJSON, optsJSON));
			return {
				status: result.status,
				stdout: result.stdout,
				stderr: result.stderr,
				error: result.error ? new Error(result.error) : null,
				output: [null, result.stdout, result.stderr]
			};
		},
		execSync: function(cmd, opts) {
			var o = {};
			if (opts) { o.cwd = opts.cwd; if (opts.env) o.env = opts.env; }
			var optsJSON = JSON.stringify(o);
			return __go_exec_sync(cmd, optsJSON);
		},
		execFileSync: function(file, args, opts) {
			var argsJSON = args ? JSON.stringify(args) : '[]';
			var o = {};
			if (opts) { o.cwd = opts.cwd; if (opts.env) o.env = opts.env; }
			var optsJSON = JSON.stringify(o);
			return __go_exec_file_sync(file, argsJSON, optsJSON);
		},
		spawn: function(cmd, args, opts) {
			// Deferred-execution spawn: collects input, runs synchronously when
			// event listeners are set up. Simulates async spawn for SDKs.
			var inputChunks = [];
			var dataCallbacks = [];
			var closeCallbacks = [];
			var errorCallbacks = [];
			var exitCallbacks = [];
			var stderrDataCallbacks = [];
			var executed = false;
			var result = null;

			function doExec() {
				if (executed) return;
				executed = true;
				var argsJSON = args ? JSON.stringify(args) : '[]';
				var input = inputChunks.join('');
				var optsJSON = JSON.stringify({
					cwd: (opts && opts.cwd) || '',
					env: (opts && opts.env) || null,
					input: input
				});
				var raw = __go_spawn_sync(cmd, argsJSON, optsJSON);
				result = JSON.parse(raw);
			}

			// Split output into line-based chunks for event firing.
			function fireChunked(data, callbacks) {
				if (!data || callbacks.length === 0) return;
				var lines = data.split('\n');
				for (var i = 0; i < lines.length; i++) {
					var chunk = lines[i];
					if (i < lines.length - 1) chunk += '\n';
					else if (chunk === '') continue;
					for (var j = 0; j < callbacks.length; j++) {
						callbacks[j](chunk);
					}
				}
			}

			var child = new EventEmitter();
			child.pid = 1;
			child.killed = false;
			child.exitCode = null;
			child.connected = true;

			child.stdout = new EventEmitter();
			child.stdout.setEncoding = function() { return this; };
			child.stdout.pipe = function(dest) { return dest; };
			child.stderr = new EventEmitter();
			child.stderr.setEncoding = function() { return this; };

			var stdinEnded = false;
			var execScheduled = false;

			function scheduleExec() {
				if (execScheduled) return;
				execScheduled = true;
				setImmediate(function() {
					if (executed) return;
					doExec();
					child.exitCode = result.status;
					// Fire stdout data events line by line.
					if (result.stdout) {
						var lines = result.stdout.split('\n');
						for (var i = 0; i < lines.length; i++) {
							var chunk = lines[i];
							if (i < lines.length - 1) chunk += '\n';
							else if (chunk === '') continue;
							child.stdout.emit('data', chunk);
						}
						child.stdout.emit('end');
					}
					if (result.stderr) {
						child.stderr.emit('data', result.stderr);
						child.stderr.emit('end');
					}
					child.emit('close', result.status);
					child.emit('exit', result.status);
				});
			}

			child.stdin = new EventEmitter();
			child.stdin.writable = true;
			child.stdin.write = function(data) {
				inputChunks.push(String(data));
				// Schedule execution after all writes in this tick.
				scheduleExec();
				return true;
			};
			child.stdin.end = function(data) {
				if (data) inputChunks.push(String(data));
				stdinEnded = true;
				scheduleExec();
			};

			child.kill = function(signal) { child.killed = true; child.emit('exit', null, signal || 'SIGTERM'); };
			child.ref = function() { return child; };
			child.unref = function() { return child; };
			return child;
		},
		fork: function() {
			throw new Error('child_process.fork is not supported in ramune');
		}
	};

	// Node.js compat: global === globalThis
	if (typeof globalThis.global === 'undefined') {
		globalThis.global = globalThis;
	}

	// --- process ---
	var _platform = '__PLATFORM__';
	globalThis.process = globalThis.process || {};
	var p = globalThis.process;
	// Populate process.env from real environment so spread works.
	var __envCache = JSON.parse(__go_environ());
	p.env = new Proxy(__envCache, {
		get: function(target, name) {
			if (typeof name !== 'string') return undefined;
			// Always call __go_getenv for permission-checked access.
			// The cache is only used for Object.keys/spread.
			try {
				var v = __go_getenv(name);
				if (v !== '') { target[name] = v; return v; }
			} catch(e) {
				// Permission denied — remove from cache if present.
				delete target[name];
				return undefined;
			}
			return undefined;
		},
		set: function(target, name, value) {
			target[name] = String(value);
			return true;
		},
		has: function(target, name) {
			if (name in target) return true;
			return __go_getenv(String(name)) !== '';
		},
		ownKeys: function(target) {
			return Object.keys(target);
		},
		getOwnPropertyDescriptor: function(target, name) {
			if (name in target) return { value: target[name], writable: true, enumerable: true, configurable: true };
			return undefined;
		}
	});
	p.cwd = function() { return __go_cwd(); };
	p.pid = __go_pid();
	p.platform = _platform;
	p.arch = '__ARCH__';
	p.version = 'v20.0.0';
	p.versions = { node: '20.0.0' };
	p.argv = [];
	p.exit = function(code) {
		if (code !== undefined && code !== null) p._exitCode = code;
		p.emit('exit', p._exitCode);
		if (typeof __go_process_exit === 'function') __go_process_exit(p._exitCode);
	};
	p.stdout = {
		write: function(s) { __go_stdout_raw(String(s)); return true; },
		isTTY: __go_tty_isatty(1),
		columns: (function() { try { return JSON.parse(__go_tty_getsize(1)).columns; } catch(e) { return 80; } })(),
		rows: (function() { try { return JSON.parse(__go_tty_getsize(1)).rows; } catch(e) { return 24; } })()
	};
	p.stderr = {
		write: function(s) { __go_stderr_raw(String(s)); return true; },
		isTTY: __go_tty_isatty(2)
	};
	p.nextTick = function(fn) { queueMicrotask(fn); };
	p.hrtime = function(prev) {
		var raw = JSON.parse(__go_hrtime());
		if (prev) { raw[0] -= prev[0]; raw[1] -= prev[1]; if (raw[1] < 0) { raw[0]--; raw[1] += 1e9; } }
		return raw;
	};
	p.memoryUsage = function() { return { rss: 0, heapTotal: 0, heapUsed: 0, external: 0, arrayBuffers: 0 }; };
	p._exitCode = 0;
	// EventEmitter methods are patched onto process after EventEmitter is defined (see below).
	p.on = function() { return p; };
	p.emit = function() { return false; };
	Object.defineProperty(p, 'exitCode', {
		get: function() { return p._exitCode; },
		set: function(v) { p._exitCode = v; }
	});

	// --- navigator ---
	if (typeof globalThis.navigator === 'undefined') {
		globalThis.navigator = {
			userAgent: 'Ramune/' + (globalThis.Ramune && globalThis.Ramune.version || '0.1.0'),
			platform: p.platform || 'unknown',
			hardwareConcurrency: typeof __go_os_num_cpus === 'function' ? __go_os_num_cpus() : 1,
			language: 'en',
			languages: ['en']
		};
	}

	// --- events ---
	class EventEmitter {
		constructor() {
			this._events = {};
		}
		on(event, fn) {
			if (!this._events[event]) this._events[event] = [];
			this._events[event].push(fn);
			return this;
		}
		once(event, fn) {
			var self = this;
			function wrapper() {
				fn.apply(this, arguments);
				self.removeListener(event, wrapper);
			}
			return this.on(event, wrapper);
		}
		emit(event) {
			var args = Array.prototype.slice.call(arguments, 1);
			var fns = this._events[event] || [];
			for (var i = 0; i < fns.length; i++) fns[i].apply(this, args);
			return fns.length > 0;
		}
		removeListener(event, fn) {
			var fns = this._events[event] || [];
			this._events[event] = fns.filter(function(f) { return f !== fn; });
			return this;
		}
		removeAllListeners(event) {
			if (event) delete this._events[event];
			else this._events = {};
			return this;
		}
		listeners(event) {
			return (this._events[event] || []).slice();
		}
		listenerCount(event) {
			return (this._events[event] || []).length;
		}
		addListener(event, fn) { return this.on(event, fn); }
		off(event, fn) { return this.removeListener(event, fn); }
	}

	var events = {
		EventEmitter: EventEmitter,
		setMaxListeners: function() {},
		getMaxListeners: function() { return 10; },
		once: function(emitter, event) {
			return new Promise(function(resolve) { emitter.once(event, resolve); });
		}
	};

	// Patch process to use EventEmitter (process is defined before EventEmitter).
	p._events = {};
	p.on = EventEmitter.prototype.on;
	p.addListener = EventEmitter.prototype.addListener;
	p.once = EventEmitter.prototype.once;
	p.off = EventEmitter.prototype.off;
	p.removeListener = EventEmitter.prototype.removeListener;
	p.removeAllListeners = EventEmitter.prototype.removeAllListeners;
	p.emit = EventEmitter.prototype.emit;
	p.listeners = EventEmitter.prototype.listeners;
	p.listenerCount = EventEmitter.prototype.listenerCount;
	p.setMaxListeners = function() { return p; };
	p.getMaxListeners = function() { return 10; };
	p.stdin = new EventEmitter();
	p.stdin.isTTY = __go_tty_isatty(0);
	p.stdin.readable = true;
	p.stdin.setEncoding = function() { return p.stdin; };
	p.stdin.resume = function() { return p.stdin; };
	p.stdin.pause = function() { return p.stdin; };
	p.stdin.read = function() { return null; };
	p.stdin.destroy = function() { return p.stdin; };
	p.stdin.unref = function() { return p.stdin; };
	p.stdin.ref = function() { return p.stdin; };
	p.stdin.pipe = function(dest) {
		p.stdin.on('data', function(chunk) { dest.write(chunk); });
		p.stdin.on('end', function() { dest.end(); });
		return dest;
	};

	// --- stream ---
	class Readable extends EventEmitter {
		constructor(opts) {
			super();
			this._buffer = [];
			this._ended = false;
			this._flowing = null;
			this._readableEnded = false;
			this.readable = true;
			this.readableHighWaterMark = (opts && opts.highWaterMark) || 16384;
			this.objectMode = !!(opts && opts.objectMode);
			this._readableBytes = 0;
			this._pipes = [];
			this._destroyed = false;
			if (opts && opts.read) this._read = opts.read;
			if (opts && opts.destroy) this._destroy = opts.destroy;
		}
		get readableFlowing() { return this._flowing; }
		get readableEnded() { return this._readableEnded; }
		get readableLength() { return this._readableBytes; }
		_read() {}
		push(chunk) {
			if (chunk === null) {
				this._ended = true;
				this._readableEnded = true;
				if (this._flowing !== false || this._buffer.length === 0) {
					this.emit('end');
				}
				return false;
			}
			var clen = this.objectMode ? 1 : (typeof chunk === 'string' ? chunk.length : (chunk && chunk.length || 0));
			this._readableBytes += clen;
			if (this._flowing) {
				this._readableBytes -= clen;
				this.emit('data', chunk);
			} else {
				var wasEmpty = this._buffer.length === 0;
				this._buffer.push(chunk);
				if (wasEmpty) this.emit('readable');
			}
			return this._readableBytes < this.readableHighWaterMark;
		}
		read(size) {
			if (this._buffer.length === 0) {
				if (this._ended && !this._readableEnded) {
					this._readableEnded = true;
					this.emit('end');
				}
				return null;
			}
			if (size === undefined || size === null || size === 0) {
				if (this.objectMode) {
					var item = this._buffer.shift();
					this._readableBytes--;
					if (this._buffer.length === 0 && this._ended) {
						this._readableEnded = true;
						setImmediate(this.emit.bind(this, 'end'));
					}
					return item;
				}
				var all = this._buffer.join('');
				this._buffer = [];
				this._readableBytes = 0;
				if (this._ended) {
					this._readableEnded = true;
					setImmediate(this.emit.bind(this, 'end'));
				}
				return all;
			}
			var result = '';
			while (result.length < size && this._buffer.length > 0) {
				var c = this._buffer.shift();
				this._readableBytes -= (typeof c === 'string' ? c.length : (c && c.length || 0));
				result += c;
			}
			return result || null;
		}
		unshift(chunk) {
			if (chunk !== null && chunk !== undefined) {
				this._buffer.unshift(chunk);
			}
		}
		pipe(dest, opts) {
			var self = this;
			function ondata(chunk) {
				var ret = dest.write(chunk);
				if (ret === false) self.pause();
			}
			function ondrain() { self.resume(); }
			function onend() { if (!opts || opts.end !== false) dest.end(); }
			function cleanup() {
				self.removeListener('data', ondata);
				self.removeListener('end', onend);
				dest.removeListener('drain', ondrain);
				dest.removeListener('close', cleanup);
			}
			self._pipes.push({ dest: dest, cleanup: cleanup });
			self.on('data', ondata);
			self.on('end', onend);
			dest.on('drain', ondrain);
			dest.on('close', cleanup);
			self.resume();
			dest.emit('pipe', self);
			return dest;
		}
		unpipe(dest) {
			var removed = [];
			if (dest) {
				this._pipes = this._pipes.filter(function(p) {
					if (p.dest === dest) { removed.push(p); return false; }
					return true;
				});
			} else {
				removed = this._pipes;
				this._pipes = [];
			}
			for (var i = 0; i < removed.length; i++) removed[i].cleanup();
			return this;
		}
		resume() {
			this._flowing = true;
			while (this._buffer.length > 0) {
				var c = this._buffer.shift();
				this._readableBytes -= this.objectMode ? 1 : (typeof c === 'string' ? c.length : (c && c.length || 0));
				this.emit('data', c);
			}
			if (this._ended && !this._readableEnded) {
				this._readableEnded = true;
				this.emit('end');
			}
			this._read(0);
			return this;
		}
		pause() {
			this._flowing = false;
			return this;
		}
		setEncoding() { return this; }
		destroy(err) {
			if (this._destroyed) return this;
			this._destroyed = true;
			this._buffer = [];
			this._readableBytes = 0;
			this._ended = true;
			var self = this;
			if (this._destroy) {
				this._destroy(err, function(e) {
					if (e) self.emit('error', e);
					self.emit('close');
				});
			} else {
				if (err) this.emit('error', err);
				this.emit('close');
			}
			return this;
		}
		on(event, fn) {
			super.on(event, fn);
			if (event === 'data' && this._flowing !== false) this.resume();
			return this;
		}
		[Symbol.asyncIterator]() {
			var self = this;
			var ended = false;
			var err = null;
			var waiting = null;
			function onError(e) { err = e; if (waiting) { waiting.reject(e); waiting = null; } }
			function onEnd() { ended = true; if (waiting) { waiting.resolve({ value: undefined, done: true }); waiting = null; } }
			self.on('error', onError);
			self.on('end', onEnd);
			function cleanup() {
				self.removeListener('error', onError);
				self.removeListener('end', onEnd);
			}
			return {
				next: function() {
					if (err) return Promise.reject(err);
					var chunk = self.read();
					if (chunk !== null) return Promise.resolve({ value: chunk, done: false });
					if (ended || self._ended) { cleanup(); return Promise.resolve({ value: undefined, done: true }); }
					return new Promise(function(resolve, reject) {
						waiting = { resolve: resolve, reject: reject };
						function onData(c) {
							self.removeListener('data', onData);
							if (waiting) { waiting = null; resolve({ value: c, done: false }); }
						}
						self.on('data', onData);
					});
				},
				return: function() {
					if (waiting) { waiting.resolve({ value: undefined, done: true }); waiting = null; }
					cleanup();
					self.destroy();
					return Promise.resolve({ value: undefined, done: true });
				}
			};
		}
		static from(iter) {
			var r = new Readable();
			var started = false;
			r._read = function() {
				if (started) return;
				started = true;
				if (iter && typeof iter[Symbol.asyncIterator] === 'function') {
					var ai = iter[Symbol.asyncIterator]();
					(function next() {
						ai.next().then(function(res) {
							if (res.done) { r.push(null); } else { r.push(res.value); next(); }
						}).catch(function(e) { r.destroy(); r.emit('error', e); });
					})();
				} else {
					try {
						var it = iter[Symbol.iterator] ? iter[Symbol.iterator]() : iter;
						var res;
						while (!(res = it.next()).done) r.push(res.value);
						r.push(null);
					} catch(e) { r.destroy(); r.emit('error', e); }
				}
			};
			return r;
		}
	}

	class Writable extends EventEmitter {
		constructor(opts) {
			super();
			this._chunks = [];
			this._finished = false;
			this._writing = false;
			this._writableEnded = false;
			this._destroyed = false;
			this._corked = 0;
			this._corkedChunks = [];
			this.writable = true;
			this.writableHighWaterMark = (opts && opts.highWaterMark) || 16384;
			this.writableLength = 0;
			if (opts && opts.write) this._write = opts.write;
			if (opts && opts.writev) this._writev = opts.writev;
			if (opts && opts.destroy) this._destroy = opts.destroy;
			if (opts && opts.final) this._final = opts.final;
			this.objectMode = !!(opts && opts.objectMode);
		}
		get writableEnded() { return this._writableEnded; }
		get writableFinished() { return this._finished; }
		_write(chunk, encoding, cb) { cb(); }
		write(chunk, encoding, cb) {
			if (typeof encoding === 'function') { cb = encoding; encoding = 'utf8'; }
			if (this._corked > 0) {
				this._corkedChunks.push({ chunk: chunk, encoding: encoding || 'utf8', cb: cb });
				return false;
			}
			this._chunks.push(chunk);
			var clen = this.objectMode ? 1 : (typeof chunk === 'string' ? chunk.length : (chunk && chunk.length || 0));
			this.writableLength += clen;
			var self = this;
			this._writing = true;
			this._write(chunk, encoding || 'utf8', function(err) {
				self._writing = false;
				self.writableLength -= clen;
				if (err) self.emit('error', err);
				else self.emit('drain');
				if (cb) cb(err);
			});
			return this.writableLength < this.writableHighWaterMark;
		}
		cork() { this._corked++; }
		uncork() {
			this._corked--;
			if (this._corked <= 0) {
				this._corked = 0;
				var chunks = this._corkedChunks;
				this._corkedChunks = [];
				for (var i = 0; i < chunks.length; i++) {
					this.write(chunks[i].chunk, chunks[i].encoding, chunks[i].cb);
				}
			}
		}
		end(chunk, encoding, cb) {
			if (typeof chunk === 'function') { cb = chunk; chunk = undefined; }
			if (typeof encoding === 'function') { cb = encoding; encoding = undefined; }
			if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
			this._writableEnded = true;
			var self = this;
			function doFinish() {
				self._finished = true;
				self.emit('prefinish');
				self.emit('finish');
				if (cb) cb();
			}
			if (this._final) {
				this._final(function(err) { if (err) self.emit('error', err); doFinish(); });
			} else {
				doFinish();
			}
			return this;
		}
		destroy(err) {
			if (this._destroyed) return this;
			this._destroyed = true;
			this._finished = true;
			var self = this;
			if (this._destroy) {
				this._destroy(err, function(e) {
					if (e) self.emit('error', e);
					self.emit('close');
				});
			} else {
				if (err) this.emit('error', err);
				this.emit('close');
			}
			return this;
		}
	}

	class Duplex extends Readable {
		constructor(opts) {
			super(opts);
			this._chunks = [];
			this._finished = false;
			this._writing = false;
			this._writableEnded = false;
			this._corked = 0;
			this._corkedChunks = [];
			this.writable = true;
			this.writableHighWaterMark = (opts && opts.highWaterMark) || 16384;
			this.writableLength = 0;
			this.allowHalfOpen = opts && opts.allowHalfOpen !== undefined ? opts.allowHalfOpen : true;
			if (opts && opts.write) this._write = opts.write;
			if (opts && opts.writev) this._writev = opts.writev;
			if (opts && opts.final) this._final = opts.final;
		}
	}
	// Mixin Writable methods (ES6 class methods are non-enumerable, so use getOwnPropertyNames).
	Object.getOwnPropertyNames(Writable.prototype).forEach(function(k) {
		if (k !== 'constructor' && !Duplex.prototype.hasOwnProperty(k)) {
			Object.defineProperty(Duplex.prototype, k, Object.getOwnPropertyDescriptor(Writable.prototype, k));
		}
	});

	class Transform extends Duplex {
		constructor(opts) {
			super(opts);
			if (opts && opts.transform) this._transform = opts.transform;
			if (opts && opts.flush) this._flush = opts.flush;
		}
		_transform(chunk, encoding, cb) { cb(null, chunk); }
		_flush(cb) { cb(); }
		_write(chunk, encoding, cb) {
			var self = this;
			this._transform(chunk, encoding, function(err, data) {
				if (data !== undefined && data !== null) self.push(data);
				cb(err);
			});
		}
		end(chunk, encoding, cb) {
			if (typeof chunk === 'function') { cb = chunk; chunk = undefined; }
			if (typeof encoding === 'function') { cb = encoding; encoding = undefined; }
			if (chunk !== undefined && chunk !== null) this.write(chunk, encoding);
			var self = this;
			this._flush(function(err, data) {
				if (data !== undefined && data !== null) self.push(data);
				self.push(null);
				self._finished = true;
				self.emit('finish');
				if (cb) cb(err);
			});
			return this;
		}
	}

	class PassThrough extends Transform {
		_transform(chunk, encoding, cb) { cb(null, chunk); }
	}

	function _streamFinished(s, cb) {
		if (s._ended || s._finished) { cb(); return function() {}; }
		var done = false;
		function onceDone(err) {
			if (done) return; done = true;
			cb(err || null);
		}
		if (s.readable !== false) s.on('end', onceDone);
		if (s.writable !== false) s.on('finish', onceDone);
		s.on('error', onceDone);
		s.on('close', function() { onceDone(s._ended || s._finished ? null : new Error('premature close')); });
		return function() { done = true; };
	}

	function _streamPipeline() {
		var streams = Array.prototype.slice.call(arguments);
		var cb = typeof streams[streams.length - 1] === 'function' ? streams.pop() : null;
		if (streams.length < 2) {
			var err = new Error('pipeline requires at least 2 streams');
			if (cb) { cb(err); return; }
			return Promise.reject(err);
		}
		function destroyAll(e) {
			for (var i = 0; i < streams.length; i++) {
				if (streams[i].destroy) streams[i].destroy();
			}
		}
		function run(resolve, reject) {
			for (var i = 0; i < streams.length - 1; i++) {
				streams[i].pipe(streams[i + 1]);
			}
			for (var i = 0; i < streams.length - 1; i++) {
				(function(s) {
					s.on('error', function(e) { destroyAll(e); if (resolve) { reject(e); resolve = null; } else if (cb) cb(e); });
				})(streams[i]);
			}
			var last = streams[streams.length - 1];
			_streamFinished(last, function(err) {
				if (err) destroyAll(err);
				if (resolve) { err ? reject(err) : resolve(); resolve = null; }
				else if (cb) cb(err);
			});
		}
		if (!cb) return new Promise(function(res, rej) { run(res, rej); });
		run(null, null);
	}

	var stream = {
		Readable: Readable,
		Writable: Writable,
		Duplex: Duplex,
		Transform: Transform,
		PassThrough: PassThrough,
		Stream: EventEmitter,
		pipeline: _streamPipeline,
		finished: function(s, cb) {
			if (!cb) return new Promise(function(res, rej) { _streamFinished(s, function(e) { e ? rej(e) : res(); }); });
			return _streamFinished(s, cb);
		}
	};

	// --- URLSearchParams ---
	if (typeof globalThis.URLSearchParams === 'undefined') {
		globalThis.URLSearchParams = function(init) {
			this._p = {};
			if (typeof init === 'string') {
				init = init.replace(/^\?/, '');
				init.split('&').forEach(function(pair) {
					var kv = pair.split('=');
					this._p[decodeURIComponent(kv[0])] = decodeURIComponent(kv[1] || '');
				}.bind(this));
			} else if (init && typeof init === 'object') {
				for (var k in init) this._p[k] = String(init[k]);
			}
		};
		URLSearchParams.prototype.get = function(k) { return this._p[k] !== undefined ? this._p[k] : null; };
		URLSearchParams.prototype.has = function(k) { return k in this._p; };
		URLSearchParams.prototype.set = function(k, v) { this._p[k] = String(v); };
		URLSearchParams.prototype.delete = function(k) { delete this._p[k]; };
		URLSearchParams.prototype.toString = function() {
			var parts = [];
			for (var k in this._p) parts.push(encodeURIComponent(k) + '=' + encodeURIComponent(this._p[k]));
			return parts.length ? parts.join('&') : '';
		};
		URLSearchParams.prototype.forEach = function(cb) { for (var k in this._p) cb(this._p[k], k); };
	}

	// --- URL (Web standard) ---
	if (typeof globalThis.URL === 'undefined') {
		globalThis.URL = function(input, base) {
			var s = String(input);
			if (base) {
				// Resolve relative URL against base.
				var b = String(base);
				if (s.indexOf('://') === -1) {
					if (s.charAt(0) === '/') {
						var m = b.match(/^(https?:\/\/[^\/]+)/);
						s = (m ? m[1] : '') + s;
					} else {
						s = b.replace(/[^\/]*$/, '') + s;
					}
				}
			}
			var parsed = JSON.parse(__go_url_parse(s));
			this.protocol = parsed.protocol || '';
			this.hostname = parsed.hostname || '';
			this.host = parsed.host || '';
			this.port = parsed.port || '';
			this.pathname = parsed.pathname || '/';
			this.search = parsed.search || '';
			this.hash = parsed.hash || '';
			this.href = s;
			this.origin = this.protocol + '//' + this.host;
			this.username = '';
			this.password = '';
			if (parsed.auth) {
				var ap = parsed.auth.split(':');
				this.username = ap[0] || '';
				this.password = ap[1] || '';
			}
			// searchParams
			var sp = {};
			if (parsed.query) {
				parsed.query.split('&').forEach(function(pair) {
					var kv = pair.split('=');
					sp[decodeURIComponent(kv[0])] = decodeURIComponent(kv[1] || '');
				});
			}
			this.searchParams = new URLSearchParams(parsed.query || '');
		};
		URL.prototype._buildHref = function() {
			var h = this.protocol + '//';
			if (this.username) { h += this.username; if (this.password) h += ':' + this.password; h += '@'; }
			h += this.host;
			h += this.pathname;
			if (this.search) h += this.search;
			if (this.hash) h += this.hash;
			this.href = h;
		};
		URL.prototype.toString = function() { return this.href; };
		URL.prototype.toJSON = function() { return this.href; };
		URL.canParse = function(url, base) {
			try { new URL(url, base); return true; } catch(e) { return false; }
		};
		URL.parse = function(url, base) {
			try { return new URL(url, base); } catch(e) { return null; }
		};
	}

	// --- url (Node.js module) ---
	var url = {
		parse: function(s) {
			try {
				return JSON.parse(__go_url_parse(s));
			} catch(e) { return {}; }
		},
		format: function(obj) {
			var s = '';
			if (obj.protocol) s += obj.protocol + '//';
			if (obj.auth) s += obj.auth + '@';
			if (obj.hostname) s += obj.hostname;
			if (obj.port) s += ':' + obj.port;
			if (obj.pathname) s += obj.pathname;
			if (obj.search) s += obj.search;
			if (obj.hash) s += obj.hash;
			return s;
		},
		resolve: function(from, to) {
			if (to.indexOf('://') !== -1) return to;
			if (to.charAt(0) === '/') {
				var parsed = url.parse(from);
				return (parsed.protocol || '') + '//' + (parsed.host || '') + to;
			}
			var base = from.replace(/[^\/]*$/, '');
			return base + to;
		},
		URL: globalThis.URL,
		URLSearchParams: globalThis.URLSearchParams,
		fileURLToPath: function(u) {
			if (u == null) return __go_cwd();
			var s = typeof u === 'string' ? u : String(u);
			if (s.indexOf('file://') === 0) return decodeURIComponent(s.slice(7));
			return s;
		},
		pathToFileURL: function(p) {
			return 'file://' + encodeURI(p);
		}
	};

	// --- os ---
	var osModule = {
		platform: function() { return _platform; },
		arch: function() { return p.arch; },
		homedir: function() { return __go_getenv('HOME'); },
		tmpdir: function() { return __go_getenv('TMPDIR') || '/tmp'; },
		hostname: function() { return __go_os_hostname(); },
		type: function() { return _platform === 'darwin' ? 'Darwin' : 'Linux'; },
		release: function() { return ''; },
		cpus: function() {
			var n = typeof __go_os_num_cpus === 'function' ? __go_os_num_cpus() : 4;
			var arr = [];
			for (var i = 0; i < n; i++) arr.push({model: 'CPU', speed: 2400});
			return arr;
		},
		totalmem: function() { return 8 * 1024 * 1024 * 1024; },
		freemem: function() { return 4 * 1024 * 1024 * 1024; },
		userInfo: function() { return JSON.parse(__go_os_userinfo()); },
		networkInterfaces: function() { return {}; },
		endianness: function() { return 'LE'; },
		EOL: '\n',
		constants: {
			signals: __SIGNALS__,
			errno: {},
			priority: { PRIORITY_LOW: 19, PRIORITY_BELOW_NORMAL: 10, PRIORITY_NORMAL: 0, PRIORITY_ABOVE_NORMAL: -7, PRIORITY_HIGH: -14, PRIORITY_HIGHEST: -20 }
		}
	};

	// --- util ---
	var util = {
		inherits: function(ctor, superCtor) {
			ctor.prototype = Object.create(superCtor.prototype);
			ctor.prototype.constructor = ctor;
		},
		promisify: function(fn) {
			return function() {
				var args = Array.prototype.slice.call(arguments);
				return new Promise(function(resolve, reject) {
					args.push(function(err, result) { if (err) reject(err); else resolve(result); });
					fn.apply(null, args);
				});
			};
		},
		callbackify: function(fn) {
			return function() {
				var args = Array.prototype.slice.call(arguments);
				var cb = args.pop();
				Promise.resolve(fn.apply(null, args)).then(function(r) { cb(null, r); }, function(e) { cb(e); });
			};
		},
		inspect: function(obj, opts) {
			try { return JSON.stringify(obj, null, 2); } catch(e) { return String(obj); }
		},
		format: function() {
			var args = Array.prototype.slice.call(arguments);
			if (typeof args[0] === 'string' && args[0].indexOf('%') !== -1) {
				var fmt = args[0]; var i = 1;
				return fmt.replace(/%[sdj%]/g, function(m) {
					if (m === '%%') return '%';
					if (i >= args.length) return m;
					var a = args[i++];
					if (m === '%s') return String(a);
					if (m === '%d') return Number(a);
					if (m === '%j') return JSON.stringify(a);
					return a;
				});
			}
			return args.map(String).join(' ');
		},
		deprecate: function(fn) { return fn; },
		debuglog: function(section) {
			var enabled = (typeof process !== 'undefined' && process.env && process.env.NODE_DEBUG || '').split(',').indexOf(section) >= 0;
			return function() { if (enabled) console.error.apply(console, [section.toUpperCase() + ':'].concat(Array.prototype.slice.call(arguments))); };
		},
		debug: function(section) {
			return util.debuglog(section);
		},
		types: {
			isDate: function(v) { return v instanceof Date; },
			isRegExp: function(v) { return v instanceof RegExp; },
			isPromise: function(v) { return v && typeof v.then === 'function'; },
			isArrayBuffer: function(v) { return v instanceof ArrayBuffer; },
			isSharedArrayBuffer: function(v) { return typeof SharedArrayBuffer !== 'undefined' && v instanceof SharedArrayBuffer; },
			isUint8Array: function(v) { return v instanceof Uint8Array; },
			isTypedArray: function(v) { return ArrayBuffer.isView(v) && !(v instanceof DataView); },
			isDataView: function(v) { return v instanceof DataView; },
			isMap: function(v) { return v instanceof Map; },
			isSet: function(v) { return v instanceof Set; },
			isWeakMap: function(v) { return v instanceof WeakMap; },
			isWeakSet: function(v) { return v instanceof WeakSet; },
			isProxy: function() { return false; },
			isExternal: function() { return false; },
			isNativeError: function(v) { return v instanceof Error; },
			isNumberObject: function(v) { return v instanceof Number; },
			isStringObject: function(v) { return v instanceof String; },
			isBooleanObject: function(v) { return v instanceof Boolean; },
			isSymbolObject: function(v) { return typeof v === 'object' && v !== null && v.constructor === Symbol; },
			isGeneratorFunction: function(v) { return typeof v === 'function' && v.constructor && v.constructor.name === 'GeneratorFunction'; },
			isAsyncFunction: function(v) { return typeof v === 'function' && v.constructor && v.constructor.name === 'AsyncFunction'; }
		},
		TextDecoder: typeof TextDecoder !== 'undefined' ? TextDecoder : function() {},
		TextEncoder: typeof TextEncoder !== 'undefined' ? TextEncoder : function() {}
	};

	// --- Buffer ---
	if (typeof globalThis.Buffer === 'undefined') {
		var _fBuf = new ArrayBuffer(8);
		var _fDV = new DataView(_fBuf);
		var _te = typeof TextEncoder !== 'undefined' ? new TextEncoder() : null;
		function _toNeedle(val) {
			return typeof val === 'object' ? val._data : (typeof val === 'number' ? String.fromCharCode(val) : String(val));
		}
		function _u8ToStr(bytes) {
			var s = '';
			for (var i = 0; i < bytes.length; i += 8192) s += String.fromCharCode.apply(null, bytes.subarray(i, Math.min(i + 8192, bytes.length)));
			return s;
		}
		function BufferObj(data) {
			this._data = data || '';
			this.length = this._data.length;
			this._isBuffer = true;
		}
		BufferObj.prototype.toString = function(encoding, start, end) {
			var d = this._data;
			if (start !== undefined || end !== undefined) d = d.slice(start || 0, end !== undefined ? end : d.length);
			if (!encoding || encoding === 'utf8' || encoding === 'utf-8') {
				var nonASCII = false;
				for (var i = 0; i < d.length; i++) { if (d.charCodeAt(i) > 127) { nonASCII = true; break; } }
				if (!nonASCII) return d;
				var arr = new Uint8Array(d.length);
				for (var i = 0; i < d.length; i++) arr[i] = d.charCodeAt(i);
				return new TextDecoder('utf-8').decode(arr);
			}
			if (encoding === 'hex') {
				var hex = '';
				for (var i = 0; i < d.length; i++) {
					hex += ('0' + d.charCodeAt(i).toString(16)).slice(-2);
				}
				return hex;
			}
			if (encoding === 'base64' || encoding === 'base64url') {
				var chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
				var b64 = '';
				for (var i = 0; i < d.length; i += 3) {
					var b0 = d.charCodeAt(i), b1 = i+1 < d.length ? d.charCodeAt(i+1) : 0, b2 = i+2 < d.length ? d.charCodeAt(i+2) : 0;
					b64 += chars[(b0>>2)&63] + chars[((b0&3)<<4)|((b1>>4)&15)];
					b64 += i+1 < d.length ? chars[((b1&15)<<2)|((b2>>6)&3)] : '=';
					b64 += i+2 < d.length ? chars[b2&63] : '=';
				}
				if (encoding === 'base64url') return b64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
				return b64;
			}
			if (encoding === 'ascii') {
				var s = '';
				for (var i = 0; i < d.length; i++) s += String.fromCharCode(d.charCodeAt(i) & 0x7F);
				return s;
			}
			if (encoding === 'latin1' || encoding === 'binary') {
				return d;
			}
			if (encoding === 'utf16le' || encoding === 'ucs2' || encoding === 'ucs-2') {
				var s = '';
				for (var i = 0; i + 1 < d.length; i += 2) {
					s += String.fromCharCode(d.charCodeAt(i) | (d.charCodeAt(i + 1) << 8));
				}
				return s;
			}
			return d;
		};
		BufferObj.prototype.slice = function(start, end) {
			return new BufferObj(this._data.slice(start, end));
		};
		BufferObj.prototype.copy = function(target, targetStart) {
			targetStart = targetStart || 0;
			var src = this._data;
			for (var i = 0; i < src.length && (targetStart + i) < target.length; i++) {
				target._data = target._data.substring(0, targetStart + i) + src[i] + target._data.substring(targetStart + i + 1);
			}
		};
		BufferObj.prototype.equals = function(other) {
			return this._data === other._data;
		};
		BufferObj.prototype.indexOf = function(val, byteOffset) {
			return this._data.indexOf(_toNeedle(val), byteOffset);
		};
		BufferObj.prototype.lastIndexOf = function(val, byteOffset) {
			var needle = _toNeedle(val);
			return byteOffset !== undefined ? this._data.lastIndexOf(needle, byteOffset) : this._data.lastIndexOf(needle);
		};
		BufferObj.prototype.includes = function(val, byteOffset) {
			return this.indexOf(val, byteOffset) !== -1;
		};
		BufferObj.prototype.write = function(str, offset) {
			offset = offset || 0;
			this._data = this._data.substring(0, offset) + str + this._data.substring(offset + str.length);
			this.length = this._data.length;
			return str.length;
		};
		BufferObj.prototype.toJSON = function() {
			var arr = [];
			for (var i = 0; i < this._data.length; i++) arr.push(this._data.charCodeAt(i));
			return { type: 'Buffer', data: arr };
		};
		BufferObj.prototype.readUInt8 = function(offset) { return this._data.charCodeAt(offset || 0) & 0xFF; };
		BufferObj.prototype.readInt8 = function(offset) { var v = this.readUInt8(offset); return v > 127 ? v - 256 : v; };
		BufferObj.prototype.readUInt16BE = function(offset) { offset = offset || 0; return (this.readUInt8(offset) << 8) | this.readUInt8(offset+1); };
		BufferObj.prototype.readUInt16LE = function(offset) { offset = offset || 0; return this.readUInt8(offset) | (this.readUInt8(offset+1) << 8); };
		BufferObj.prototype.readUInt32BE = function(offset) { offset = offset || 0; return ((this.readUInt8(offset) << 24) | (this.readUInt8(offset+1) << 16) | (this.readUInt8(offset+2) << 8) | this.readUInt8(offset+3)) >>> 0; };
		BufferObj.prototype.readUInt32LE = function(offset) { offset = offset || 0; return (this.readUInt8(offset) | (this.readUInt8(offset+1) << 8) | (this.readUInt8(offset+2) << 16) | (this.readUInt8(offset+3) << 24)) >>> 0; };
		BufferObj.prototype.readInt16BE = function(offset) { var v = this.readUInt16BE(offset); return v > 32767 ? v - 65536 : v; };
		BufferObj.prototype.readInt16LE = function(offset) { var v = this.readUInt16LE(offset); return v > 32767 ? v - 65536 : v; };
		BufferObj.prototype.readInt32BE = function(offset) { var v = this.readUInt32BE(offset); return v > 2147483647 ? v - 4294967296 : v; };
		BufferObj.prototype.readInt32LE = function(offset) { var v = this.readUInt32LE(offset); return v > 2147483647 ? v - 4294967296 : v; };
		BufferObj.prototype.writeUInt8 = function(val, offset) { offset = offset || 0; this._data = this._data.substring(0,offset) + String.fromCharCode(val & 0xFF) + this._data.substring(offset+1); this.length = this._data.length; return offset + 1; };
		BufferObj.prototype.writeUInt16BE = function(val, offset) { offset = offset || 0; this.writeUInt8((val >> 8) & 0xFF, offset); this.writeUInt8(val & 0xFF, offset+1); return offset + 2; };
		BufferObj.prototype.writeUInt16LE = function(val, offset) { offset = offset || 0; this.writeUInt8(val & 0xFF, offset); this.writeUInt8((val >> 8) & 0xFF, offset+1); return offset + 2; };
		BufferObj.prototype.writeUInt32BE = function(val, offset) { offset = offset || 0; this.writeUInt8((val >> 24) & 0xFF, offset); this.writeUInt8((val >> 16) & 0xFF, offset+1); this.writeUInt8((val >> 8) & 0xFF, offset+2); this.writeUInt8(val & 0xFF, offset+3); return offset + 4; };
		BufferObj.prototype.writeUInt32LE = function(val, offset) { offset = offset || 0; this.writeUInt8(val & 0xFF, offset); this.writeUInt8((val >> 8) & 0xFF, offset+1); this.writeUInt8((val >> 16) & 0xFF, offset+2); this.writeUInt8((val >> 24) & 0xFF, offset+3); return offset + 4; };
		BufferObj.prototype.writeInt8 = function(val, offset) { this.writeUInt8(val < 0 ? val + 256 : val, offset); return (offset || 0) + 1; };
		BufferObj.prototype.writeInt16BE = function(val, offset) { this.writeUInt16BE(val < 0 ? val + 65536 : val, offset); return (offset || 0) + 2; };
		BufferObj.prototype.writeInt16LE = function(val, offset) { this.writeUInt16LE(val < 0 ? val + 65536 : val, offset); return (offset || 0) + 2; };
		BufferObj.prototype.writeInt32BE = function(val, offset) { this.writeUInt32BE(val < 0 ? val + 4294967296 : val, offset); return (offset || 0) + 4; };
		BufferObj.prototype.writeInt32LE = function(val, offset) { this.writeUInt32LE(val < 0 ? val + 4294967296 : val, offset); return (offset || 0) + 4; };
		BufferObj.prototype.readFloatBE = function(offset) { offset = offset || 0; for (var i = 0; i < 4; i++) _fDV.setUint8(i, this._data.charCodeAt(offset + i)); return _fDV.getFloat32(0, false); };
		BufferObj.prototype.readFloatLE = function(offset) { offset = offset || 0; for (var i = 0; i < 4; i++) _fDV.setUint8(i, this._data.charCodeAt(offset + i)); return _fDV.getFloat32(0, true); };
		BufferObj.prototype.readDoubleBE = function(offset) { offset = offset || 0; for (var i = 0; i < 8; i++) _fDV.setUint8(i, this._data.charCodeAt(offset + i)); return _fDV.getFloat64(0, false); };
		BufferObj.prototype.readDoubleLE = function(offset) { offset = offset || 0; for (var i = 0; i < 8; i++) _fDV.setUint8(i, this._data.charCodeAt(offset + i)); return _fDV.getFloat64(0, true); };
		BufferObj.prototype.writeFloatBE = function(val, offset) { offset = offset || 0; _fDV.setFloat32(0, val, false); var s = ''; for (var i = 0; i < 4; i++) s += String.fromCharCode(_fDV.getUint8(i)); this._data = this._data.substring(0, offset) + s + this._data.substring(offset + 4); this.length = this._data.length; return offset + 4; };
		BufferObj.prototype.writeFloatLE = function(val, offset) { offset = offset || 0; _fDV.setFloat32(0, val, true); var s = ''; for (var i = 0; i < 4; i++) s += String.fromCharCode(_fDV.getUint8(i)); this._data = this._data.substring(0, offset) + s + this._data.substring(offset + 4); this.length = this._data.length; return offset + 4; };
		BufferObj.prototype.writeDoubleBE = function(val, offset) { offset = offset || 0; _fDV.setFloat64(0, val, false); var s = ''; for (var i = 0; i < 8; i++) s += String.fromCharCode(_fDV.getUint8(i)); this._data = this._data.substring(0, offset) + s + this._data.substring(offset + 8); this.length = this._data.length; return offset + 8; };
		BufferObj.prototype.writeDoubleLE = function(val, offset) { offset = offset || 0; _fDV.setFloat64(0, val, true); var s = ''; for (var i = 0; i < 8; i++) s += String.fromCharCode(_fDV.getUint8(i)); this._data = this._data.substring(0, offset) + s + this._data.substring(offset + 8); this.length = this._data.length; return offset + 8; };
		BufferObj.prototype.fill = function(val, offset, end) {
			offset = offset || 0; end = end || this.length;
			var ch = typeof val === 'number' ? String.fromCharCode(val) : (val ? String(val).charAt(0) : '\0');
			for (var i = offset; i < end; i++) { this._data = this._data.substring(0,i) + ch + this._data.substring(i+1); }
			return this;
		};
		BufferObj.prototype.compare = function(other) {
			var a = this._data, b = other._data;
			if (a < b) return -1;
			if (a > b) return 1;
			return 0;
		};
		BufferObj.prototype.subarray = BufferObj.prototype.slice;
		BufferObj.prototype.swap16 = function() {
			if (this.length % 2 !== 0) throw new RangeError('Buffer size must be a multiple of 16-bits');
			var d = this._data, s = '';
			for (var i = 0; i < d.length; i += 2) s += d.charAt(i+1) + d.charAt(i);
			this._data = s; return this;
		};
		BufferObj.prototype.swap32 = function() {
			if (this.length % 4 !== 0) throw new RangeError('Buffer size must be a multiple of 32-bits');
			var d = this._data, s = '';
			for (var i = 0; i < d.length; i += 4) s += d.charAt(i+3) + d.charAt(i+2) + d.charAt(i+1) + d.charAt(i);
			this._data = s; return this;
		};
		BufferObj.prototype.swap64 = function() {
			if (this.length % 8 !== 0) throw new RangeError('Buffer size must be a multiple of 64-bits');
			var d = this._data, s = '';
			for (var i = 0; i < d.length; i += 8) s += d.charAt(i+7) + d.charAt(i+6) + d.charAt(i+5) + d.charAt(i+4) + d.charAt(i+3) + d.charAt(i+2) + d.charAt(i+1) + d.charAt(i);
			this._data = s; return this;
		};
		BufferObj.prototype.readBigUInt64BE = function(offset) {
			offset = offset || 0;
			var hi = BigInt(this.readUInt32BE(offset)), lo = BigInt(this.readUInt32BE(offset + 4));
			return (hi << 32n) | lo;
		};
		BufferObj.prototype.readBigUInt64LE = function(offset) {
			offset = offset || 0;
			var lo = BigInt(this.readUInt32LE(offset)), hi = BigInt(this.readUInt32LE(offset + 4));
			return (hi << 32n) | lo;
		};
		BufferObj.prototype.readBigInt64BE = function(offset) {
			var v = this.readBigUInt64BE(offset);
			return v >= 0x8000000000000000n ? v - 0x10000000000000000n : v;
		};
		BufferObj.prototype.readBigInt64LE = function(offset) {
			var v = this.readBigUInt64LE(offset);
			return v >= 0x8000000000000000n ? v - 0x10000000000000000n : v;
		};
		BufferObj.prototype.writeBigUInt64BE = function(val, offset) {
			offset = offset || 0; val = BigInt(val);
			this.writeUInt32BE(Number((val >> 32n) & 0xFFFFFFFFn), offset);
			this.writeUInt32BE(Number(val & 0xFFFFFFFFn), offset + 4);
			return offset + 8;
		};
		BufferObj.prototype.writeBigUInt64LE = function(val, offset) {
			offset = offset || 0; val = BigInt(val);
			this.writeUInt32LE(Number(val & 0xFFFFFFFFn), offset);
			this.writeUInt32LE(Number((val >> 32n) & 0xFFFFFFFFn), offset + 4);
			return offset + 8;
		};
		BufferObj.prototype.writeBigInt64BE = function(val, offset) {
			return this.writeBigUInt64BE(val < 0n ? val + 0x10000000000000000n : val, offset);
		};
		BufferObj.prototype.writeBigInt64LE = function(val, offset) {
			return this.writeBigUInt64LE(val < 0n ? val + 0x10000000000000000n : val, offset);
		};
		BufferObj.prototype[Symbol.iterator] = function() {
			var i = 0, d = this._data;
			return { next: function() { return i < d.length ? { value: d.charCodeAt(i++), done: false } : { done: true }; } };
		};

		globalThis.Buffer = {
			from: function(data, encoding) {
				if (typeof data === 'string') {
					if (encoding === 'hex') {
						var s = '';
						for (var i = 0; i < data.length; i += 2) {
							s += String.fromCharCode(parseInt(data.substr(i, 2), 16));
						}
						return new BufferObj(s);
					}
					if (encoding === 'base64') {
						if (typeof atob === 'function') return new BufferObj(atob(data));
						return new BufferObj(data);
					}
					return new BufferObj(data);
				}
				if (Array.isArray(data)) {
					return new BufferObj(String.fromCharCode.apply(null, data));
				}
				if (data && data._isBuffer) {
					return new BufferObj(data._data);
				}
				if (data instanceof ArrayBuffer) {
					var bo = encoding || 0, len = arguments[2] !== undefined ? arguments[2] : data.byteLength - bo;
					return new BufferObj(_u8ToStr(new Uint8Array(data, bo, len)));
				}
				if (ArrayBuffer.isView(data)) {
					return new BufferObj(_u8ToStr(new Uint8Array(data.buffer, data.byteOffset, data.byteLength)));
				}
				return new BufferObj('');
			},
			alloc: function(size, fill, encoding) {
				var pat = '\0';
				if (fill !== undefined && fill !== null) {
					if (typeof fill === 'number') { pat = String.fromCharCode(fill & 0xFF); }
					else if (typeof fill === 'string') {
						pat = fill;
						if (encoding === 'hex') { pat = ''; for (var i = 0; i < fill.length; i += 2) pat += String.fromCharCode(parseInt(fill.substr(i, 2), 16)); }
					} else if (fill._isBuffer) { pat = fill._data; }
				}
				var s = '';
				for (var i = 0; i < size; i++) s += pat.charAt(i % pat.length);
				return new BufferObj(s);
			},
			allocUnsafe: function(size) { return globalThis.Buffer.alloc(size); },
			isBuffer: function(obj) { return obj instanceof BufferObj || (obj && obj._isBuffer === true); },
			concat: function(list, totalLength) {
				var s = '';
				for (var i = 0; i < list.length; i++) s += list[i]._data || list[i].toString();
				return new BufferObj(s);
			},
			byteLength: function(str, encoding) {
				if (!encoding || encoding === 'utf8' || encoding === 'utf-8') return _te ? _te.encode(str).length : str.length;
				if (encoding === 'hex') return str.length >>> 1;
				if (encoding === 'base64') { var pad = 0; if (str[str.length - 1] === '=') pad++; if (str[str.length - 2] === '=') pad++; return Math.ceil(str.length * 3 / 4) - pad; }
				if (encoding === 'utf16le' || encoding === 'ucs2' || encoding === 'ucs-2') return str.length * 2;
				return str.length;
			},
			isEncoding: function() { return true; },
			compare: function(a, b) { return a.compare(b); },
			poolSize: 8192
		};
	}

	// --- console extensions ---
	var _consoleTimers = {};
	if (!globalThis.console.time) {
		globalThis.console.time = function(label) { _consoleTimers[label || 'default'] = performance.now(); };
	}
	if (!globalThis.console.timeEnd) {
		globalThis.console.timeEnd = function(label) {
			label = label || 'default';
			var start = _consoleTimers[label];
			if (start !== undefined) { console.log(label + ': ' + (performance.now() - start).toFixed(3) + 'ms'); delete _consoleTimers[label]; }
		};
	}
	if (!globalThis.console.timeLog) {
		globalThis.console.timeLog = function(label) {
			label = label || 'default';
			var start = _consoleTimers[label];
			if (start !== undefined) console.log(label + ': ' + (performance.now() - start).toFixed(3) + 'ms');
		};
	}
	if (!globalThis.console.trace) {
		globalThis.console.trace = function() {
			var args = Array.prototype.slice.call(arguments);
			console.log.apply(console, ['Trace:'].concat(args));
			try { throw new Error(); } catch(e) { if (e.stack) console.log(e.stack.split('\n').slice(2).join('\n')); }
		};
	}
	if (!globalThis.console.table) {
		globalThis.console.table = function(data) {
			if (Array.isArray(data)) {
				if (data.length === 0) { console.log('[]'); return; }
				if (typeof data[0] === 'object' && data[0] !== null) {
					var keys = Object.keys(data[0]);
					var header = '(index) | ' + keys.join(' | ');
					console.log(header);
					console.log(header.replace(/[^|]/g, '-'));
					for (var i = 0; i < data.length; i++) {
						var row = i + '       | ' + keys.map(function(k) { return String(data[i][k]); }).join(' | ');
						console.log(row);
					}
				} else {
					for (var i = 0; i < data.length; i++) console.log(i + ': ' + String(data[i]));
				}
			} else if (typeof data === 'object' && data !== null) {
				var keys = Object.keys(data);
				for (var i = 0; i < keys.length; i++) console.log(keys[i] + ': ' + String(data[keys[i]]));
			} else {
				console.log(data);
			}
		};
	}
	if (!globalThis.console.assert) {
		globalThis.console.assert = function(cond) {
			if (!cond) { var args = Array.prototype.slice.call(arguments, 1); console.error.apply(console, ['Assertion failed:'].concat(args.length ? args : ['console.assert'])); }
		};
	}
	if (!globalThis.console.count) {
		var _consoleCounts = {};
		globalThis.console.count = function(label) { label = label || 'default'; _consoleCounts[label] = (_consoleCounts[label] || 0) + 1; console.log(label + ': ' + _consoleCounts[label]); };
		globalThis.console.countReset = function(label) { delete _consoleCounts[label || 'default']; };
	}

	// --- setTimeout/setInterval ---
	if (typeof globalThis.setTimeout === 'undefined') {
		globalThis.setTimeout = function(fn, ms) { return 0; };
	}
	if (typeof globalThis.clearTimeout === 'undefined') {
		globalThis.clearTimeout = function() {};
	}
	if (typeof globalThis.setInterval === 'undefined') {
		globalThis.setInterval = function() { return 0; };
	}
	if (typeof globalThis.clearInterval === 'undefined') {
		globalThis.clearInterval = function() {};
	}
	if (typeof globalThis.setImmediate === 'undefined') {
		globalThis.setImmediate = function(fn) { return 0; };
	}
	if (typeof globalThis.queueMicrotask === 'undefined') {
		globalThis.queueMicrotask = function(fn) { Promise.resolve().then(fn); };
	}

	// --- crypto ---
	var crypto = {
		randomBytes: function(n) {
			var hexStr = __go_crypto_random_bytes(n);
			return {
				_hex: hexStr,
				toString: function(enc) {
					if (enc === 'hex') return hexStr;
					// Convert hex to raw characters.
					var s = '';
					for (var i = 0; i < hexStr.length; i += 2) {
						s += String.fromCharCode(parseInt(hexStr.substr(i, 2), 16));
					}
					return s;
				},
				length: n
			};
		},
		randomUUID: function() {
			var hexStr = __go_crypto_random_bytes(16);
			var h = hexStr.split('');
			// Set version (4) and variant (8-b).
			h[12] = '4';
			h[16] = ((parseInt(h[16], 16) & 0x3) | 0x8).toString(16);
			return h.slice(0,8).join('') + '-' + h.slice(8,12).join('') + '-' +
				h.slice(12,16).join('') + '-' + h.slice(16,20).join('') + '-' +
				h.slice(20,32).join('');
		},
		createHash: function(algorithm) {
			var _data = '';
			return {
				update: function(data) { _data += String(data); return this; },
				digest: function(encoding) {
					var hex = __go_crypto_hash(algorithm, _data);
					if (encoding === 'hex') return hex;
					if (encoding === 'base64') {
						// Hex to bytes to base64.
						var bytes = [];
						for (var i = 0; i < hex.length; i += 2) {
							bytes.push(parseInt(hex.substr(i, 2), 16));
						}
						// Use btoa if available, otherwise return hex.
						if (typeof btoa === 'function') {
							return btoa(String.fromCharCode.apply(null, bytes));
						}
						return hex;
					}
					return hex;
				}
			};
		},
		createHmac: function(algorithm, key) {
			var _data = '';
			return {
				update: function(data) { _data += String(data); return this; },
				digest: function(encoding) {
					var hex = __go_crypto_hmac(algorithm, _data, String(key));
					if (encoding === 'hex') return hex;
					if (encoding === 'base64') {
						var bytes = [];
						for (var i = 0; i < hex.length; i += 2) {
							bytes.push(parseInt(hex.substr(i, 2), 16));
						}
						if (typeof btoa === 'function') {
							return btoa(String.fromCharCode.apply(null, bytes));
						}
						return hex;
					}
					return hex;
				}
			};
		},
		scryptSync: function(password, salt, keylen) {
			var hex = __go_crypto_scrypt(String(password), String(salt), keylen);
			return globalThis.Buffer.from(hex, 'hex');
		},
		pbkdf2Sync: function(password, salt, iterations, keylen, digest) {
			var hex = __go_crypto_pbkdf2(String(password), String(salt), iterations, keylen, digest || 'sha1');
			return globalThis.Buffer.from(hex, 'hex');
		},
		createCipheriv: function(algorithm, key, iv) {
			var _data = '';
			var keyHex = globalThis.Buffer.isBuffer(key) ? key.toString('hex') : globalThis.Buffer.from(String(key)).toString('hex');
			var ivHex = globalThis.Buffer.isBuffer(iv) ? iv.toString('hex') : globalThis.Buffer.from(String(iv)).toString('hex');
			return {
				update: function(data, inputEnc, outputEnc) {
					_data += String(data);
					return '';
				},
				final: function(encoding) {
					var hex = __go_crypto_cipher(algorithm, keyHex, ivHex, _data);
					if (encoding === 'hex') return hex;
					return globalThis.Buffer.from(hex, 'hex');
				},
				setAutoPadding: function() { return this; }
			};
		},
		createDecipheriv: function(algorithm, key, iv) {
			var _data = '';
			var keyHex = globalThis.Buffer.isBuffer(key) ? key.toString('hex') : globalThis.Buffer.from(String(key)).toString('hex');
			var ivHex = globalThis.Buffer.isBuffer(iv) ? iv.toString('hex') : globalThis.Buffer.from(String(iv)).toString('hex');
			return {
				update: function(data, inputEnc) {
					if (inputEnc === 'hex') _data += data;
					else if (globalThis.Buffer.isBuffer(data)) _data += data.toString('hex');
					else _data += globalThis.Buffer.from(String(data)).toString('hex');
					return '';
				},
				final: function(encoding) {
					var plain = __go_crypto_decipher(algorithm, keyHex, ivHex, _data);
					if (encoding === 'utf8' || encoding === 'utf-8') return plain;
					return globalThis.Buffer.from(plain);
				},
				setAutoPadding: function() { return this; }
			};
		},
		randomInt: function(min, max) {
			if (max === undefined) { max = min; min = 0; }
			return __go_crypto_random_int(min, max);
		},
		timingSafeEqual: function(a, b) {
			var sa = a._data || a.toString();
			var sb = b._data || b.toString();
			if (sa.length !== sb.length) return false;
			var diff = 0;
			for (var i = 0; i < sa.length; i++) {
				diff |= sa.charCodeAt(i) ^ sb.charCodeAt(i);
			}
			return diff === 0;
		},
		createSign: function(algorithm) {
			var _data = '';
			return {
				update: function(data) { _data += String(data); return this; },
				sign: function(privateKey, encoding) {
					var keyPEM = typeof privateKey === 'string' ? privateKey : (privateKey.key || String(privateKey));
					var hex = __go_crypto_sign(algorithm, _data, keyPEM);
					if (encoding === 'hex') return hex;
					if (encoding === 'base64') {
						var bytes = [];
						for (var i = 0; i < hex.length; i += 2) bytes.push(parseInt(hex.substr(i, 2), 16));
						if (typeof btoa === 'function') return btoa(String.fromCharCode.apply(null, bytes));
						return hex;
					}
					return globalThis.Buffer ? globalThis.Buffer.from(hex, 'hex') : hex;
				}
			};
		},
		createVerify: function(algorithm) {
			var _data = '';
			return {
				update: function(data) { _data += String(data); return this; },
				verify: function(publicKey, signature, encoding) {
					var keyPEM = typeof publicKey === 'string' ? publicKey : (publicKey.key || String(publicKey));
					var sigHex;
					if (encoding === 'hex') sigHex = signature;
					else if (encoding === 'base64') {
						var raw = (typeof atob === 'function') ? atob(signature) : '';
						sigHex = '';
						for (var i = 0; i < raw.length; i++) sigHex += ('0' + raw.charCodeAt(i).toString(16)).slice(-2);
					} else if (globalThis.Buffer && globalThis.Buffer.isBuffer(signature)) {
						sigHex = signature.toString('hex');
					} else {
						sigHex = String(signature);
					}
					return __go_crypto_verify(algorithm, _data, keyPEM, sigHex);
				}
			};
		},
		generateKeyPairSync: function(type, options) {
			var optsStr = options ? JSON.stringify(options) : '{}';
			var result = __go_crypto_generate_key_pair(type, optsStr);
			return JSON.parse(result);
		}
	};

	// --- Symbol.dispose / Symbol.asyncDispose (TC39 Explicit Resource Management) ---
	if (!Symbol.dispose) Symbol.dispose = Symbol('Symbol.dispose');
	if (!Symbol.asyncDispose) Symbol.asyncDispose = Symbol('Symbol.asyncDispose');

	// --- AbortController ---
	if (typeof globalThis.AbortController === 'undefined') {
		class AbortSignal extends EventEmitter {
			constructor() {
				super();
				this.aborted = false;
				this.reason = undefined;
			}
			addEventListener(event, fn) { this.on(event, fn); }
			removeEventListener(event, fn) { this.removeListener(event, fn); }
			throwIfAborted() { if (this.aborted) throw this.reason; }
			static timeout(ms) { var s = new AbortSignal(); setTimeout(function() { s.aborted = true; s.reason = new DOMException('The operation timed out.', 'TimeoutError'); s.emit('abort'); }, ms); return s; }
			static abort(reason) { var s = new AbortSignal(); s.aborted = true; s.reason = reason !== undefined ? reason : new DOMException('The operation was aborted.', 'AbortError'); return s; }
			static any(signals) {
				var s = new AbortSignal();
				function onAbort() {
					if (s.aborted) return;
					for (var i = 0; i < signals.length; i++) {
						if (signals[i].aborted) {
							s.aborted = true;
							s.reason = signals[i].reason;
							s.emit('abort');
							return;
						}
					}
				}
				for (var i = 0; i < signals.length; i++) {
					if (signals[i].aborted) { onAbort(); return s; }
					signals[i].addEventListener('abort', onAbort);
				}
				return s;
			}
		}

		globalThis.AbortController = class AbortController {
			constructor() { this.signal = new AbortSignal(); }
			abort(reason) {
				this.signal.aborted = true;
				this.signal.reason = reason || new Error('AbortError');
				this.signal.emit('abort');
			}
		};
		globalThis.AbortSignal = AbortSignal;
	}

	// --- TextEncoder / TextDecoder ---
	if (typeof globalThis.TextEncoder === 'undefined') {
		globalThis.TextEncoder = function() {};
		globalThis.TextEncoder.prototype.encode = function(str) {
			var arr = [];
			for (var i = 0; i < str.length; i++) {
				var c = str.charCodeAt(i);
				if (c < 0x80) { arr.push(c); }
				else if (c < 0x800) { arr.push(0xC0 | (c >> 6), 0x80 | (c & 0x3F)); }
				else if (c < 0xD800 || c >= 0xE000) { arr.push(0xE0 | (c >> 12), 0x80 | ((c >> 6) & 0x3F), 0x80 | (c & 0x3F)); }
				else {
					i++;
					c = 0x10000 + (((c & 0x3FF) << 10) | (str.charCodeAt(i) & 0x3FF));
					arr.push(0xF0 | (c >> 18), 0x80 | ((c >> 12) & 0x3F), 0x80 | ((c >> 6) & 0x3F), 0x80 | (c & 0x3F));
				}
			}
			return new Uint8Array(arr);
		};
	}
	if (typeof globalThis.TextDecoder === 'undefined') {
		globalThis.TextDecoder = function() {};
		globalThis.TextDecoder.prototype.decode = function(buf) {
			var arr = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
			var str = '', i = 0;
			while (i < arr.length) {
				var c = arr[i++];
				if (c < 0x80) { str += String.fromCharCode(c); }
				else if (c < 0xE0) { str += String.fromCharCode(((c & 0x1F) << 6) | (arr[i++] & 0x3F)); }
				else if (c < 0xF0) { str += String.fromCharCode(((c & 0x0F) << 12) | ((arr[i++] & 0x3F) << 6) | (arr[i++] & 0x3F)); }
				else {
					var cp = ((c & 0x07) << 18) | ((arr[i++] & 0x3F) << 12) | ((arr[i++] & 0x3F) << 6) | (arr[i++] & 0x3F);
					cp -= 0x10000;
					str += String.fromCharCode(0xD800 + (cp >> 10), 0xDC00 + (cp & 0x3FF));
				}
			}
			return str;
		};
	}

	// --- performance ---
	if (typeof globalThis.performance === 'undefined') {
		var _perfOriginNs = JSON.parse(__go_hrtime());
		var _perfOriginMs = Date.now();
		var _perfMarks = {};
		var _perfMeasures = [];
		function Performance() {}
		Performance.prototype.now = function() {
			var raw = JSON.parse(__go_hrtime());
			var ds = raw[0] - _perfOriginNs[0];
			var dn = raw[1] - _perfOriginNs[1];
			return ds * 1e3 + dn / 1e6;
		};
		Performance.prototype.mark = function(name) {
			var t = this.now();
			_perfMarks[name] = t;
			return { name: name, entryType: 'mark', startTime: t, duration: 0 };
		};
		Performance.prototype.measure = function(name, startMark, endMark) {
			var s = startMark && _perfMarks[startMark] !== undefined ? _perfMarks[startMark] : 0;
			var e = endMark && _perfMarks[endMark] !== undefined ? _perfMarks[endMark] : this.now();
			var entry = { name: name, entryType: 'measure', startTime: s, duration: e - s };
			_perfMeasures.push(entry);
			return entry;
		};
		Performance.prototype[Symbol.toStringTag] = 'Performance';
		var __perf = new Performance();
		__perf.timeOrigin = _perfOriginMs;
		globalThis.Performance = Performance;
		globalThis.performance = __perf;
		Performance.prototype.getEntriesByName = function(name) {
			return _perfMeasures.filter(function(e) { return e.name === name; });
		};
		Performance.prototype.getEntriesByType = function(type) {
			if (type === 'mark') {
				return Object.keys(_perfMarks).map(function(k) { return { name: k, entryType: 'mark', startTime: _perfMarks[k], duration: 0 }; });
			}
			if (type === 'measure') return _perfMeasures.slice();
			return [];
		};
		Performance.prototype.clearMarks = function(name) { if (name) delete _perfMarks[name]; else _perfMarks = {}; };
		Performance.prototype.clearMeasures = function(name) { if (name) _perfMeasures = _perfMeasures.filter(function(e) { return e.name !== name; }); else _perfMeasures = []; };
	}

	// --- structuredClone ---
	if (typeof globalThis.structuredClone === 'undefined') {
		globalThis.structuredClone = function(obj) {
			if (obj === null || typeof obj !== 'object') return obj;
			var seen = new Map();
			function clone(val) {
				if (val === null || typeof val !== 'object') return val;
				if (seen.has(val)) return seen.get(val);
				if (val instanceof Date) { var d = new Date(val.getTime()); seen.set(val, d); return d; }
				if (val instanceof RegExp) { var r = new RegExp(val.source, val.flags); seen.set(val, r); return r; }
				if (val instanceof Map) {
					var m = new Map();
					seen.set(val, m);
					val.forEach(function(v, k) { m.set(clone(k), clone(v)); });
					return m;
				}
				if (val instanceof Set) {
					var s = new Set();
					seen.set(val, s);
					val.forEach(function(v) { s.add(clone(v)); });
					return s;
				}
				if (ArrayBuffer.isView(val)) {
					return new val.constructor(val.buffer.slice(0));
				}
				if (val instanceof ArrayBuffer) {
					return val.slice(0);
				}
				if (Array.isArray(val)) {
					var arr = [];
					seen.set(val, arr);
					for (var i = 0; i < val.length; i++) arr.push(clone(val[i]));
					return arr;
				}
				if (val instanceof Error) {
					var e = new val.constructor(val.message);
					seen.set(val, e);
					e.stack = val.stack;
					return e;
				}
				var out = Object.create(Object.getPrototypeOf(val));
				seen.set(val, out);
				var keys = Object.keys(val);
				for (var i = 0; i < keys.length; i++) out[keys[i]] = clone(val[keys[i]]);
				return out;
			}
			return clone(obj);
		};
	}

	// --- Blob / File ---
	if (typeof globalThis.Blob === 'undefined') {
		function __normalizeMIME(t) {
			if (!t) return '';
			var out = '';
			for (var i = 0; i < t.length; i++) {
				var c = t.charCodeAt(i);
				if (c >= 0x20 && c <= 0x7E) out += t[i];
			}
			return out.toLowerCase();
		}
		function Blob(parts, options) {
			options = options || {};
			this.type = __normalizeMIME(options.type);
			this._parts = [];
			if (parts != null) {
				if (typeof parts === 'string' || typeof parts === 'number' || typeof parts === 'boolean') {
					throw new TypeError("Failed to construct 'Blob': The provided value cannot be converted to a sequence.");
				}
				for (var i = 0; i < parts.length; i++) {
					var p = parts[i];
					if (typeof p === 'string') this._parts.push(p);
					else if (p instanceof Blob) this._parts.push(p._text());
					else if (p instanceof ArrayBuffer) this._parts.push(new TextDecoder().decode(new Uint8Array(p)));
					else if (ArrayBuffer.isView(p)) this._parts.push(new TextDecoder().decode(p instanceof Uint8Array ? p : new Uint8Array(p.buffer, p.byteOffset, p.byteLength)));
					else this._parts.push(String(p));
				}
			}
		}
		Blob.prototype._text = function() {
			if (this._joined === undefined) this._joined = this._parts.join('');
			return this._joined;
		};
		Object.defineProperty(Blob.prototype, 'size', { get: function() {
			if (this._byteSize === undefined) this._byteSize = new TextEncoder().encode(this._text()).length;
			return this._byteSize;
		}});
		Blob.prototype.text = function() { return Promise.resolve(this._text()); };
		Blob.prototype.arrayBuffer = function() {
			var buf = new TextEncoder().encode(this._text());
			return Promise.resolve(buf.buffer);
		};
		Blob.prototype.slice = function(start, end, type) {
			var bytes = new TextEncoder().encode(this._text());
			var size = bytes.length;
			start = start !== undefined ? start : 0;
			end = end !== undefined ? end : size;
			if (start < 0) start = Math.max(size + start, 0); else start = Math.min(start, size);
			if (end < 0) end = Math.max(size + end, 0); else end = Math.min(end, size);
			if (start > end) start = end;
			var sliced = bytes.slice(start, end);
			return new Blob([sliced], { type: type !== undefined ? type : '' });
		};
		Blob.prototype.stream = function() {
			var blob = this;
			return new ReadableStream({
				start: function(controller) {
					var data = new TextEncoder().encode(blob._text());
					if (data.length > 0) controller.enqueue(data);
					controller.close();
				}
			});
		};
		Blob.prototype.bytes = function() {
			return Promise.resolve(new Uint8Array(new TextEncoder().encode(this._text())));
		};
		Blob.prototype[Symbol.toStringTag] = 'Blob';
		globalThis.Blob = Blob;

		function File(parts, name, options) {
			Blob.call(this, parts, options);
			this.name = name;
			this.lastModified = (options && options.lastModified) || Date.now();
		}
		File.prototype = Object.create(Blob.prototype);
		File.prototype.constructor = File;
		File.prototype[Symbol.toStringTag] = 'File';
		globalThis.File = File;
	}

	// --- FormData ---
	if (typeof globalThis.FormData === 'undefined') {
		function FormData() {
			this._entries = [];
		}
		FormData.prototype.append = function(name, value, filename) {
			if (value instanceof Blob && !(value instanceof File)) {
				value = new File([value._text()], filename || 'blob', { type: value.type });
			}
			this._entries.push([String(name), value]);
		};
		FormData.prototype.set = function(name, value, filename) {
			this.delete(name);
			this.append(name, value, filename);
		};
		FormData.prototype.delete = function(name) {
			this._entries = this._entries.filter(function(e) { return e[0] !== name; });
		};
		FormData.prototype.get = function(name) {
			for (var i = 0; i < this._entries.length; i++) {
				if (this._entries[i][0] === name) return this._entries[i][1];
			}
			return null;
		};
		FormData.prototype.getAll = function(name) {
			return this._entries.filter(function(e) { return e[0] === name; }).map(function(e) { return e[1]; });
		};
		FormData.prototype.has = function(name) {
			return this._entries.some(function(e) { return e[0] === name; });
		};
		function __makeIter(arr, mapFn) {
			var idx = 0;
			return { next: function() {
				if (idx >= arr.length) return { done: true, value: undefined };
				return { done: false, value: mapFn(arr[idx++]) };
			}, [Symbol.iterator]: function() { return this; } };
		}
		FormData.prototype.entries = function() { return __makeIter(this._entries, function(e) { return e.slice(); }); };
		FormData.prototype.keys = function() { return __makeIter(this._entries, function(e) { return e[0]; }); };
		FormData.prototype.values = function() { return __makeIter(this._entries, function(e) { return e[1]; }); };
		FormData.prototype.forEach = function(cb, thisArg) {
			for (var i = 0; i < this._entries.length; i++) {
				cb.call(thisArg, this._entries[i][1], this._entries[i][0], this);
			}
		};
		FormData.prototype[Symbol.iterator] = FormData.prototype.entries;
		FormData.prototype[Symbol.toStringTag] = 'FormData';
		globalThis.FormData = FormData;
	}

	// --- EventTarget / Event (DOM-like, needed by many npm packages) ---
	if (typeof globalThis.Event === 'undefined') {
		globalThis.Event = function Event(type, opts) {
			this.type = type;
			this.bubbles = !!(opts && opts.bubbles);
			this.cancelable = !!(opts && opts.cancelable);
			this.composed = !!(opts && opts.composed);
			this.defaultPrevented = false;
			this._stopImmediate = false;
			this.target = null;
			this.currentTarget = null;
		};
		globalThis.Event.prototype.preventDefault = function() {
			if (this.cancelable) this.defaultPrevented = true;
		};
		globalThis.Event.prototype.stopPropagation = function() {};
		globalThis.Event.prototype.stopImmediatePropagation = function() { this._stopImmediate = true; };
	}
	if (typeof globalThis.CustomEvent === 'undefined') {
		globalThis.CustomEvent = function CustomEvent(type, opts) {
			globalThis.Event.call(this, type, opts);
			this.detail = (opts && opts.detail !== undefined) ? opts.detail : null;
		};
		globalThis.CustomEvent.prototype = Object.create(globalThis.Event.prototype);
		globalThis.CustomEvent.prototype.constructor = globalThis.CustomEvent;
	}
	if (typeof globalThis.EventTarget === 'undefined') {
		globalThis.EventTarget = function EventTarget() {
			this._listeners = {};
		};
		globalThis.EventTarget.prototype.addEventListener = function(type, fn, opts) {
			if (!this._listeners[type]) this._listeners[type] = [];
			if (this._listeners[type].indexOf(fn) >= 0) return;
			if (opts && opts.once) {
				var self = this;
				var wrapped = function(e) { self.removeEventListener(type, wrapped); fn.call(self, e); };
				wrapped._orig = fn;
				this._listeners[type].push(wrapped);
			} else {
				this._listeners[type].push(fn);
			}
		};
		globalThis.EventTarget.prototype.removeEventListener = function(type, fn) {
			if (!this._listeners[type]) return;
			this._listeners[type] = this._listeners[type].filter(function(f) { return f !== fn && f._orig !== fn; });
		};
		globalThis.EventTarget.prototype.dispatchEvent = function(event) {
			event.target = this;
			event.currentTarget = this;
			var fns = (this._listeners[event.type] || []).slice();
			for (var i = 0; i < fns.length; i++) {
				if (event._stopImmediate) break;
				var f = fns[i];
				if (typeof f === 'object' && f.handleEvent) f.handleEvent(event);
				else f.call(this, event);
			}
			return !event.defaultPrevented;
		};
	}

	// --- async_hooks: AsyncLocalStorage ---
	function AsyncLocalStorage() {
		this._store = undefined;
		this._enabled = true;
	}
	AsyncLocalStorage.prototype.getStore = function() {
		return this._enabled ? this._store : undefined;
	};
	AsyncLocalStorage.prototype.run = function(store, callback) {
		var prev = this._store;
		this._store = store;
		var args = Array.prototype.slice.call(arguments, 2);
		var result;
		try {
			result = callback.apply(null, args);
		} catch(e) {
			this._store = prev;
			throw e;
		}
		if (result && typeof result.then === 'function') {
			var self = this;
			var restore = function() { self._store = prev; };
			return result.then(function(v) { restore(); return v; }, function(e) { restore(); throw e; });
		}
		this._store = prev;
		return result;
	};
	AsyncLocalStorage.prototype.exit = function(callback) {
		var args = [undefined, callback].concat(Array.prototype.slice.call(arguments, 1));
		return this.run.apply(this, args);
	};
	AsyncLocalStorage.prototype.enterWith = function(store) {
		this._store = store;
	};
	AsyncLocalStorage.prototype.disable = function() {
		this._enabled = false;
		this._store = undefined;
	};
	AsyncLocalStorage.snapshot = function() {
		return function(fn) {
			return fn.apply(null, Array.prototype.slice.call(arguments, 1));
		};
	};
	AsyncLocalStorage.bind = function(fn) { return fn; };

	function AsyncResource(type) { this.type = type; }
	AsyncResource.prototype.runInAsyncScope = function(fn, thisArg) {
		return fn.apply(thisArg, Array.prototype.slice.call(arguments, 2));
	};
	AsyncResource.prototype.emitDestroy = function() { return this; };
	AsyncResource.prototype.asyncId = function() { return 0; };
	AsyncResource.prototype.triggerAsyncId = function() { return 0; };
	AsyncResource.bind = function(fn) { return fn; };

	var async_hooks = {
		AsyncLocalStorage: AsyncLocalStorage,
		AsyncResource: AsyncResource,
		executionAsyncId: function() { return 0; },
		triggerAsyncId: function() { return 0; },
		createHook: function() { return { enable: function() {}, disable: function() {} }; },
		executionAsyncResource: function() { return {}; }
	};

	// --- Module registry ---
	var _modules = {
		'async_hooks': async_hooks,
		'path': path,
		'fs': fs,
		'child_process': child_process,
		'events': events,
		'stream': stream,
		'url': url,
		'os': osModule,
		'util': util,
		'crypto': crypto,
		'buffer': { Buffer: globalThis.Buffer },
		'process': globalThis.process,
		'net': {
			Socket: function() { this._events = {}; this.connect = function() { return this; }; this.write = function() { return true; }; this.end = function() {}; this.destroy = function() {}; this.on = function(e,f) { return this; }; },
			createConnection: function() { return new (_modules['net'].Socket)(); },
			createServer: function() { throw new Error('net.createServer is not supported in ramune'); }
		},
		'http': (function() {
			class IncomingMessage extends Readable {
				constructor(raw) {
					super();
					this.statusCode = raw.status;
					this.statusMessage = raw.statusText || '';
					this.headers = raw.headers || {};
					this._body = raw.body || '';
					this.httpVersion = '1.1';
					this.method = null;
					this.url = '';
					this.complete = false;
				}
				_deliver() {
					var self = this;
					if (self._body) {
						var lines = self._body.match(/.{1,4096}/g) || [];
						for (var i = 0; i < lines.length; i++) self.push(lines[i]);
					}
					self.push(null);
					self.complete = true;
				}
				setTimeout(ms, cb) { if (cb) this.on('timeout', cb); return this; }
			}

			class ClientRequest extends EventEmitter {
				constructor(opts, cb) {
					super();
					this._opts = opts;
					this._body = '';
					this._callback = cb;
				}
				write(data) { this._body += String(data); return true; }
				end(data) {
					if (data) this._body += String(data);
					var o = this._opts;
					var url = (o.protocol || 'http:') + '//' + (o.hostname || o.host || 'localhost') + (o.port ? ':' + o.port : '') + (o.path || o.pathname || '/');
					var optsJSON = JSON.stringify({ method: o.method || 'GET', headers: o.headers || {}, body: this._body });
					try {
						var raw = JSON.parse(__go_http_request(url, optsJSON));
						var res = new IncomingMessage(raw);
						if (this._callback) this._callback(res);
						this.emit('response', res);
						res._deliver();
					} catch(e) {
						this.emit('error', e);
					}
					return this;
				}
				setTimeout(ms, cb) { if (cb) this.on('timeout', cb); return this; }
				abort() {}
				destroy() {}
			}

			var httpModule = {
				request: function(urlOrOpts, optsOrCb, cb) {
					var opts, callback;
					if (typeof urlOrOpts === 'string') {
						var parsed = JSON.parse(__go_url_parse(urlOrOpts));
						opts = { protocol: parsed.protocol, hostname: parsed.hostname, port: parsed.port, path: parsed.pathname + (parsed.search || ''), headers: {} };
						if (typeof optsOrCb === 'function') { callback = optsOrCb; }
						else if (typeof optsOrCb === 'object') { for (var k in optsOrCb) opts[k] = optsOrCb[k]; callback = cb; }
					} else {
						opts = urlOrOpts || {};
						callback = typeof optsOrCb === 'function' ? optsOrCb : cb;
					}
					return new ClientRequest(opts, callback);
				},
				get: function(urlOrOpts, optsOrCb, cb) {
					var req = httpModule.request(urlOrOpts, optsOrCb, cb);
					req.end();
					return req;
				},
				createServer: function(handler) {
					var server = {
						_handler: handler, _bunServer: null,
						listen: function(port, host, cb) {
							if (typeof host === 'function') { cb = host; host = undefined; }
							var self = this;
							self._bunServer = Ramune.serve({
								port: (port !== undefined && port !== null) ? port : 3000,
								fetch: function(req) {
									return new Promise(function(resolve) {
										var url = new URL(req.url);
										var nodeReq = new IncomingMessage({status: 0, body: ''});
										nodeReq.method = req.method;
										nodeReq.url = url.pathname + (url.search || '');
										nodeReq.httpVersion = '1.1';
										nodeReq.headers = {};
										nodeReq.rawHeaders = [];
										if (req.headers && typeof req.headers.forEach === 'function') {
											req.headers.forEach(function(v, k) { nodeReq.headers[k] = v; nodeReq.rawHeaders.push(k, v); });
										}
										nodeReq.socket = { remoteAddress: '127.0.0.1', remotePort: 0, localAddress: '0.0.0.0', localPort: 0 };
										nodeReq.connection = nodeReq.socket;

										var resStatus = 200, resHeaders = {}, resBody = '', streaming = false, streamCtrl = null;
										var nodeRes = new EventEmitter();
										nodeRes.statusCode = 200;
										nodeRes.statusMessage = 'OK';
										nodeRes.headersSent = false;
										nodeRes.writableEnded = false;
										nodeRes.writableFinished = false;
										nodeRes.writeHead = function(s, msg, h) {
											if (typeof msg === 'object') { h = msg; msg = undefined; }
											resStatus = s; this.statusCode = s;
											if (msg) this.statusMessage = msg;
											if (h) for (var k in h) resHeaders[k] = h[k];
											this.headersSent = true;
											return this;
										};
										nodeRes.setHeader = function(k, v) { resHeaders[k] = v; return this; };
										nodeRes.getHeader = function(k) { return resHeaders[k]; };
										nodeRes.getHeaders = function() { var o = {}; for (var k in resHeaders) o[k] = resHeaders[k]; return o; };
										nodeRes.hasHeader = function(k) { return k in resHeaders; };
										nodeRes.removeHeader = function(k) { delete resHeaders[k]; };
										nodeRes.cork = function() {};
										nodeRes.uncork = function() {};
										nodeRes.flushHeaders = function() { nodeRes.headersSent = true; };
										nodeRes.write = function(c) {
											nodeRes.headersSent = true;
											if (!streaming && typeof ReadableStream !== 'undefined') {
												streaming = true;
												var rs = new ReadableStream({
													start: function(ctrl) { streamCtrl = ctrl; ctrl.enqueue(new TextEncoder().encode(String(c))); }
												});
												resolve(new Response(rs, { status: resStatus, headers: resHeaders }));
											} else if (streamCtrl) {
												streamCtrl.enqueue(new TextEncoder().encode(String(c)));
											} else {
												resBody += String(c);
											}
											return true;
										};
										nodeRes.end = function(d) {
											nodeRes.writableEnded = true;
											if (streaming) {
												if (d) streamCtrl.enqueue(new TextEncoder().encode(String(d)));
												streamCtrl.close();
											} else {
												if (d) resBody += String(d);
												nodeRes.headersSent = true;
												resolve(new Response(resBody, { status: resStatus, headers: resHeaders }));
											}
											nodeRes.writableFinished = true;
											nodeRes.emit('prefinish');
											nodeRes.emit('finish');
										};

										self._handler(nodeReq, nodeRes);
										var body = req._body || '';
										if (body) { nodeReq.push(body); }
										nodeReq.push(null);
									});
								}
							});
							self.address = function() { return { port: self._bunServer.port, address: '0.0.0.0' }; };
							if (cb) setTimeout(cb, 0);
							return self;
						},
						close: function(cb) { if (this._bunServer) this._bunServer.stop(); if (cb) setTimeout(cb, 0); },
						closeAllConnections: function() {},
						on: function(ev, fn) { if (ev === 'request' && typeof fn === 'function') this._handler = fn; return this; }
					};
					return server;
				},
				IncomingMessage: IncomingMessage,
				ClientRequest: ClientRequest,
				ServerResponse: EventEmitter,
				STATUS_CODES: {'200':'OK','201':'Created','204':'No Content','301':'Moved','302':'Found','304':'Not Modified','400':'Bad Request','401':'Unauthorized','403':'Forbidden','404':'Not Found','500':'Internal Server Error'}
			};
			return httpModule;
		})(),
		'https': null, // set below
		'tls': {
			connect: function() { throw new Error('tls.connect: use installAsyncNet'); },
			createServer: function() { throw new Error('tls.createServer: use installTCPServer'); }
		},
		'zlib': {
			constants: {
				Z_NO_FLUSH: 0, Z_PARTIAL_FLUSH: 1, Z_SYNC_FLUSH: 2, Z_FULL_FLUSH: 3,
				Z_FINISH: 4, Z_BLOCK: 5, Z_TREES: 6,
				Z_OK: 0, Z_STREAM_END: 1, Z_NEED_DICT: 2, Z_ERRNO: -1,
				Z_STREAM_ERROR: -2, Z_DATA_ERROR: -3, Z_MEM_ERROR: -4,
				Z_BUF_ERROR: -5, Z_VERSION_ERROR: -6,
				Z_NO_COMPRESSION: 0, Z_BEST_SPEED: 1, Z_BEST_COMPRESSION: 9, Z_DEFAULT_COMPRESSION: -1,
				Z_FILTERED: 1, Z_HUFFMAN_ONLY: 2, Z_RLE: 3, Z_FIXED: 4, Z_DEFAULT_STRATEGY: 0,
				BROTLI_OPERATION_PROCESS: 0, BROTLI_OPERATION_FLUSH: 1, BROTLI_OPERATION_FINISH: 2
			},
			gzipSync: function(data) {
				var s = typeof data === 'string' ? data : data.toString();
				return globalThis.Buffer.from(__go_zlib_gzip(s), 'hex');
			},
			gunzipSync: function(data) {
				var hexStr = (data && data._isBuffer) ? data.toString('hex') : String(data);
				return globalThis.Buffer.from(__go_zlib_gunzip(hexStr));
			},
			deflateSync: function(data) {
				var s = typeof data === 'string' ? data : data.toString();
				return globalThis.Buffer.from(__go_zlib_deflate(s), 'hex');
			},
			inflateSync: function(data) {
				var hexStr = (data && data._isBuffer) ? data.toString('hex') : String(data);
				return globalThis.Buffer.from(__go_zlib_inflate(hexStr));
			},
			brotliCompressSync: function(data) {
				var s = typeof data === 'string' ? data : data.toString();
				return globalThis.Buffer.from(__go_zlib_brotli_compress(s), 'hex');
			},
			brotliDecompressSync: function(data) {
				var hexStr = (data && data._isBuffer) ? data.toString('hex') : String(data);
				return globalThis.Buffer.from(__go_zlib_brotli_decompress(hexStr));
			}
		},
		'string_decoder': { StringDecoder: function() { this.write = function(b) { return b.toString(); }; this.end = function() { return ''; }; } },
		'tty': {
			isatty: function(fd) { return __go_tty_isatty(typeof fd === 'number' ? fd : 0); },
			ReadStream: function(fd) {
				this.fd = fd;
				this.isTTY = __go_tty_isatty(fd);
				this.isRaw = false;
				this.setRawMode = function(mode) { this.isRaw = !!mode; return this; };
			},
			WriteStream: function(fd) {
				this.fd = fd;
				this.isTTY = __go_tty_isatty(fd);
				var size = JSON.parse(__go_tty_getsize(fd));
				this.columns = size.columns;
				this.rows = size.rows;
				this.getWindowSize = function() { return [this.columns, this.rows]; };
			}
		},
		'readline': {
			createInterface: function(opts) {
				var input = opts.input || opts;
				var rl = new EventEmitter();
				var lineQueue = [];
				var waiters = [];
				var closed = false;

				rl.close = function() { closed = true; rl.emit('close'); for (var i = 0; i < waiters.length; i++) waiters[i]({value: undefined, done: true}); waiters = []; };

				if (input && input.on) {
					var buffer = '';
					input.on('data', function(chunk) {
						buffer += String(chunk);
						var lines = buffer.split('\n');
						buffer = lines.pop();
						for (var i = 0; i < lines.length; i++) {
							var line = lines[i];
							rl.emit('line', line);
							if (waiters.length > 0) {
								waiters.shift()({value: line, done: false});
							} else {
								lineQueue.push(line);
							}
						}
					});
					input.on('end', function() {
						if (buffer) {
							rl.emit('line', buffer);
							if (waiters.length > 0) waiters.shift()({value: buffer, done: false});
							else lineQueue.push(buffer);
						}
						closed = true;
						rl.emit('close');
						for (var i = 0; i < waiters.length; i++) waiters[i]({value: undefined, done: true});
						waiters = [];
					});
				}

				// Make readline async-iterable (for await...of support).
				rl[Symbol.asyncIterator] = function() {
					return {
						next: function() {
							if (lineQueue.length > 0) {
								return Promise.resolve({value: lineQueue.shift(), done: false});
							}
							if (closed) {
								return Promise.resolve({value: undefined, done: true});
							}
							return new Promise(function(resolve) { waiters.push(resolve); });
						},
						return: function() { return Promise.resolve({value: undefined, done: true}); }
					};
				};
				return rl;
			}
		},
		'querystring': {
			escape: function(s) { return encodeURIComponent(s); },
			unescape: function(s) { return decodeURIComponent(s); },
			stringify: function(obj, sep, eq) {
				sep = sep || '&'; eq = eq || '=';
				return Object.keys(obj).map(function(k) {
					var v = obj[k];
					if (Array.isArray(v)) return v.map(function(item) { return encodeURIComponent(k) + eq + encodeURIComponent(item); }).join(sep);
					return encodeURIComponent(k) + eq + encodeURIComponent(v);
				}).join(sep);
			},
			parse: function(s, sep, eq) {
				sep = sep || '&'; eq = eq || '=';
				var obj = {};
				s.split(sep).forEach(function(pair) {
					var idx = pair.indexOf(eq);
					if (idx < 0) return;
					var k = decodeURIComponent(pair.slice(0, idx));
					var v = decodeURIComponent(pair.slice(idx + eq.length));
					if (obj[k] !== undefined) {
						if (!Array.isArray(obj[k])) obj[k] = [obj[k]];
						obj[k].push(v);
					} else {
						obj[k] = v;
					}
				});
				return obj;
			}
		},
		'assert': (function() {
			function AssertionError(msg) { this.message = msg; this.name = 'AssertionError'; }
			AssertionError.prototype = Object.create(Error.prototype);
			function assertFn(v, msg) { if (!v) throw new AssertionError(msg || 'assertion failed'); }
			assertFn.ok = assertFn;
			assertFn.strictEqual = function(a, b, msg) { if (a !== b) throw new AssertionError(msg || JSON.stringify(a) + ' !== ' + JSON.stringify(b)); };
			assertFn.notStrictEqual = function(a, b, msg) { if (a === b) throw new AssertionError(msg || 'values are strictly equal'); };
			assertFn.deepStrictEqual = function(a, b, msg) { if (JSON.stringify(a) !== JSON.stringify(b)) throw new AssertionError(msg || 'not deep equal'); };
			assertFn.notDeepStrictEqual = function(a, b, msg) { if (JSON.stringify(a) === JSON.stringify(b)) throw new AssertionError(msg || 'deep equal'); };
			assertFn.throws = function(fn, expected, msg) {
				try { fn(); } catch(e) {
					if (expected && typeof expected === 'function' && !(e instanceof expected)) throw new AssertionError(msg || 'wrong error type');
					return;
				}
				throw new AssertionError(msg || 'missing expected exception');
			};
			assertFn.doesNotThrow = function(fn, msg) { try { fn(); } catch(e) { throw new AssertionError(msg || 'got unwanted exception: ' + e); } };
			assertFn.fail = function(msg) { throw new AssertionError(msg || 'assertion failed'); };
			assertFn.equal = function(a, b, msg) { if (a != b) throw new AssertionError(msg || a + ' != ' + b); };
			assertFn.notEqual = function(a, b, msg) { if (a == b) throw new AssertionError(msg || 'values are equal'); };
			assertFn.AssertionError = AssertionError;
			return assertFn;
		})()
	};
	_modules['https'] = _modules['http']; // https uses same Go net/http (handles TLS)
	_modules['timers'] = {
		setTimeout: globalThis.setTimeout,
		clearTimeout: globalThis.clearTimeout,
		setInterval: globalThis.setInterval,
		clearInterval: globalThis.clearInterval,
		setImmediate: globalThis.setImmediate
	};
	_modules['timers/promises'] = {
		setTimeout: function(ms, value) { return new Promise(function(resolve) { globalThis.setTimeout(function() { resolve(value); }, ms); }); },
		setInterval: function() { throw new Error('timers/promises.setInterval is not supported'); },
		setImmediate: function(value) { return new Promise(function(resolve) { globalThis.setImmediate(function() { resolve(value); }); }); }
	};
	// --- PerformanceObserver ---
	(function() {
		var _observers = [];
		var _origMark = globalThis.performance.mark;
		var _origMeasure = globalThis.performance.measure;
		globalThis.performance.mark = function(name) {
			var entry = _origMark.call(globalThis.performance, name);
			_notifyObservers('mark', entry);
			return entry;
		};
		globalThis.performance.measure = function(name, startMark, endMark) {
			var entry = _origMeasure.call(globalThis.performance, name, startMark, endMark);
			_notifyObservers('measure', entry);
			return entry;
		};
		function _notifyObservers(type, entry) {
			for (var i = 0; i < _observers.length; i++) {
				var obs = _observers[i];
				if (obs._types.indexOf(type) !== -1) {
					obs._buffer.push(entry);
					if (obs._callback) {
						var list = { getEntries: function() { return obs._buffer.slice(); },
							getEntriesByType: function(t) { return obs._buffer.filter(function(e) { return e.entryType === t; }); },
							getEntriesByName: function(n) { return obs._buffer.filter(function(e) { return e.name === n; }); } };
						try { obs._callback(list, obs); } catch(e) {}
						obs._buffer = [];
					}
				}
			}
		}
		globalThis.PerformanceObserver = function(callback) {
			this._callback = callback;
			this._types = [];
			this._buffer = [];
		};
		globalThis.PerformanceObserver.prototype.observe = function(opts) {
			if (opts && opts.entryTypes) this._types = opts.entryTypes;
			else if (opts && opts.type) this._types = [opts.type];
			if (_observers.indexOf(this) === -1) _observers.push(this);
		};
		globalThis.PerformanceObserver.prototype.disconnect = function() {
			var idx = _observers.indexOf(this);
			if (idx !== -1) _observers.splice(idx, 1);
		};
		globalThis.PerformanceObserver.prototype.takeRecords = function() {
			var r = this._buffer; this._buffer = []; return r;
		};
		globalThis.PerformanceObserver.prototype.getEntries = function() { return this._buffer.slice(); };
		globalThis.PerformanceObserver.supportedEntryTypes = ['mark', 'measure'];
	})();
	_modules['perf_hooks'] = { performance: globalThis.performance, PerformanceObserver: globalThis.PerformanceObserver };

	// require - file-based module loading with ESM-to-CJS transform
	var _cache = {};
	function _getBaseDir() {
		return globalThis.__dirname || __go_cwd();
	}
	function makeRequire(fromDir) {
		var req = function(mod) {
			if (_modules[mod]) return _modules[mod];
			if (mod.indexOf('node:') === 0) {
				var name = mod.slice(5);
				if (_modules[name]) return _modules[name];
			}

			// Dependencies() packages (globalThis)
			var varName = mod.replace(/^@/, '').replace(/\//g, '_').replace(/-/g, '_');
			var gv = globalThis[varName];
			if (gv != null && typeof gv === 'object') {
				for (var _k in gv) return gv; // O(1) non-empty check
			}
			if (typeof gv === 'function') return gv;

			// Resolve path (no file read)
			var dir = fromDir || _getBaseDir();
			var absPath;
			try {
				absPath = __go_resolve_module(mod, dir);
			} catch(e) {
				throw new Error("Cannot find module '" + mod + "'\nRequire stack:\n- " + dir);
			}

			if (_cache[absPath]) return _cache[absPath].exports;

			// JSON files
			if (absPath.slice(-5) === '.json') {
				var jsonResult = JSON.parse(__go_load_module(absPath));
				_cache[absPath] = { exports: jsonResult };
				return jsonResult;
			}

			// CJS module wrapper
			var moduleObj = { exports: {} };
			_cache[absPath] = moduleObj; // before eval for circular requires

			var source = __go_load_module(absPath);
			if (typeof globalThis.__bunPluginResolve === 'function') {
				var plugin = globalThis.__bunPluginResolve(absPath);
				if (plugin && plugin.callback) {
					var pResult = plugin.callback({ path: absPath, loader: plugin.loader || 'js' });
					if (pResult && pResult.exports) { moduleObj.exports = pResult.exports; return pResult.exports; }
					if (pResult && pResult.contents) source = pResult.contents;
				}
			}

			var fn = new Function('exports', 'require', 'module', '__filename', '__dirname',
				source + '\n//# sourceURL=' + absPath);
			var modDir = absPath.substring(0, absPath.lastIndexOf('/'));
			var childRequire = makeRequire(modDir);
			fn.call(moduleObj.exports, moduleObj.exports, childRequire, moduleObj, absPath, modDir);
			return moduleObj.exports;
		};

		req.resolve = function(mod) {
			if (_modules[mod]) return mod;
			if (mod.indexOf('node:') === 0 && _modules[mod.slice(5)]) return mod;
			return __go_resolve_module(mod, fromDir || _getBaseDir());
		};
		req.resolve.paths = function() { return null; };
		req._modules = _modules;
		req.cache = _cache;
		return req;
	}
	globalThis.require = makeRequire(null);

	// Dynamic import() polyfill — delegates to require() and wraps in Promise.
	globalThis.__dynamicImport = function(mod) {
		try {
			var m = globalThis.require(mod);
			return Promise.resolve(m);
		} catch(e) {
			return Promise.reject(e);
		}
	};

	// module/exports (for CommonJS bundles)
	if (typeof globalThis.module === 'undefined') {
		globalThis.module = { exports: {} };
	}
	if (typeof globalThis.exports === 'undefined') {
		globalThis.exports = globalThis.module.exports;
	}

	// module.createRequire — used by ESM-to-CJS interop in many npm packages
	var createRequire = function(filename) {
		var s = String(filename || '').replace(/^file:\/\//, '');
		var idx = s.lastIndexOf('/');
		var dir = idx >= 0 ? s.substring(0, idx) : '.';
		return makeRequire(dir);
	};
	globalThis.module.createRequire = createRequire;
	_modules['module'] = {
		createRequire: createRequire,
		Module: { createRequire: createRequire }
	};
})();
`
	src = strings.Replace(src, "__PLATFORM__", guessPlatform(), 1)
	src = strings.Replace(src, "__ARCH__", guessArch(), 1)
	src = strings.Replace(src, "__SIGNALS__", signalConstants(), 1)
	return src
}
