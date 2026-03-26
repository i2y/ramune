// Command ramune is a JavaScript runtime powered by JavaScriptCore via purego.
//
// Usage:
//
//	ramune run <file.js>                   Run a JavaScript file
//	ramune run -p lodash -p dayjs file.js  Run with npm packages
//	ramune eval "<expression>"             Evaluate a JS expression
//	ramune repl                            Interactive REPL
//
// Environment:
//
//	Node.js compatibility (require, fs, path, crypto, etc.) is always enabled.
//	globalThis.fetch is available for HTTP requests.
//	setTimeout/setInterval work via the built-in event loop.
package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"context"

	"github.com/charmbracelet/lipgloss"
	"github.com/ergochat/readline"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/fsnotify/fsnotify"
	"github.com/i2y/ramune"
	"github.com/i2y/ramune/internal/registry"
	"github.com/i2y/ramune/internal/rslint/config"
	"github.com/i2y/ramune/internal/rslint/linter"
	"github.com/i2y/ramune/internal/rslint/rule"
	rslintutils "github.com/i2y/ramune/internal/rslint/utils"
	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/format"
	"github.com/i2y/ramune/internal/tsgo/ls/lsutil"
	"github.com/i2y/ramune/internal/tsgo/parser"
	"github.com/i2y/ramune/internal/tsgo/scanner"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

//go:embed skills
var skillsFS embed.FS

// Set at build time via -ldflags "-X main.version=..."
// Falls back to Go module version from go install.
var version = ""

func getVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func printLogo() {
	blue := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	bubble := lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

	logo := blue.Render("  ┏━┓┏━┓┏┳┓╻ ╻┏┓╻┏━╸") + "  " + bubble.Render("○") + "\n" +
		blue.Render("  ┣┳┛┣━┫┃┃┃┃ ┃┃┗┫┣╸ ") + bubble.Render("○") + "\n" +
		blue.Render("  ╹┗╸╹ ╹╹ ╹┗━┛╹ ╹┗━╸") + " " + bubble.Render("。") + "\n"

	fmt.Fprint(os.Stderr, "\n")
	fmt.Fprint(os.Stderr, logo)
	fmt.Fprintf(os.Stderr, "  %s %s\n\n", dim.Render(getVersion()), dim.Render("· JS/TS runtime for Go ("+backendName+")"))
}

func main() {
	if len(os.Args) < 2 {
		printLogo()
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "eval":
		evalCmd(os.Args[2:])
	case "repl":
		replCmd(os.Args[2:])
	case "test":
		testCmd(os.Args[2:])
	case "check":
		checkCmd(os.Args[2:])
	case "fmt":
		fmtCmd(os.Args[2:])
	case "lint":
		lintCmd(os.Args[2:])
	case "init":
		initCmd()
	case "add":
		addCmd(os.Args[2:])
	case "remove":
		removeCmd(os.Args[2:])
	case "install":
		installCmd()
	case "setup-jit":
		setupJITCmd()
	case "x":
		execPkgCmd(os.Args[2:])
	case "build":
		buildCmd(os.Args[2:])
	case "compile":
		compileCmd(os.Args[2:])
	case "bench":
		benchCmd(os.Args[2:])
	case "skills":
		skillsCmd(os.Args[2:])
	case "version", "--version", "-v":
		printLogo()
	case "help", "-h", "--help":
		printUsage()
	default:
		// Treat as filename if it ends in .js/.mjs/.ts/.tsx
		ext := filepath.Ext(os.Args[1])
		if ext == ".js" || ext == ".mjs" || ext == ".ts" || ext == ".tsx" {
			runCmd(os.Args[1:])
		} else {
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: ramune <command> [options] [arguments]

Commands:
  run [file]        Run a JS/TS file (or package.json entry point)
  eval "<expr>"     Evaluate an expression (JS/TS)
  test [pattern]    Run test files (*.test.ts, *.spec.js, etc.)
  check [file|dir]  Type-check TypeScript files with tsgo
  fmt [file|dir]    Format JS/TS files
  lint [file|dir]   Lint JS/TS files
  repl              Interactive REPL (JS/TS)
  setup-jit         Sign binary for JSC JIT (macOS, requires codesign)
  compile [file]    Compile JS/TS to standalone binary
  init              Initialize a new project (creates package.json)
  add <pkg...>      Add npm packages to package.json
  remove <pkg...>   Remove npm packages from package.json
  install           Install packages from package.json
  skills install    Install Ramune Agent Skill for AI agents
  version           Show version
  help              Show this help

Run options:
  -p <package>      Add npm package (repeatable)
  -w, --watch       Watch for file changes and restart
  --check           Type-check before running
  --workers N       Run N parallel workers for Ramune.serve()

Build with -tags quickjs to use QuickJS backend instead of JavaScriptCore.
Supported file types: .js, .mjs, .ts, .tsx

Examples:
  ramune run script.ts
  ramune run -p lodash -p dayjs app.ts
  ramune run                     # uses package.json entry point
  ramune run -w server.ts        # watch mode
  ramune run --workers 4 server.ts  # multi-worker HTTP server
  ramune eval "const x: number = 42; x"
  ramune test                    # run all test files
  ramune repl`)
}

type packageList []string

func (p *packageList) String() string { return strings.Join(*p, ", ") }
func (p *packageList) Set(val string) error {
	*p = append(*p, val)
	return nil
}

func createRuntimeWithOpts(packages []string, extra ...ramune.Option) (*ramune.Runtime, error) {
	opts := []ramune.Option{ramune.NodeCompat(), ramune.WithFetch()}
	if len(packages) > 0 {
		opts = append(opts, ramune.Dependencies(packages...))
	}
	opts = append(opts, extra...)
	return createRuntimeFromOpts(opts)
}

func createRuntime(packages []string) (*ramune.Runtime, error) {
	return createRuntimeWithOpts(packages)
}

func createRuntimeFromOpts(opts []ramune.Option) (*ramune.Runtime, error) {
	rt, err := ramune.New(opts...)
	if err != nil {
		return nil, err
	}

	// Connect console.log/error/warn to stdout/stderr.
	rt.RegisterFunc("__go_stdout", func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		fmt.Fprintln(os.Stdout, strings.Join(parts, " "))
		return nil, nil
	})
	rt.RegisterFunc("__go_stderr", func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		fmt.Fprintln(os.Stderr, strings.Join(parts, " "))
		return nil, nil
	})
	rt.Exec(`
		console.log = function() { __go_stdout.apply(null, Array.prototype.slice.call(arguments)); };
		console.info = console.log;
		console.error = function() { __go_stderr.apply(null, Array.prototype.slice.call(arguments)); };
		console.warn = console.error;
		console.debug = console.log;
	`)

	// Enable local file require: require('./lib/utils'), require('./data.json')
	rt.RegisterFunc("__go_require_resolve", func(args []any) (any, error) {
		if len(args) < 2 {
			return "", fmt.Errorf("require: module and basedir required")
		}
		mod, _ := args[0].(string)
		baseDir, _ := args[1].(string)

		// Relative paths: resolve from baseDir.
		if strings.HasPrefix(mod, "./") || strings.HasPrefix(mod, "../") {
			return resolveFileModule(filepath.Join(baseDir, mod))
		}

		// Non-relative: search node_modules up the directory tree.
		return resolveNodeModule(mod, baseDir)
	})

	rt.Exec(`
(function() {
	var origRequire = globalThis.require;
	var _cache = {};
	globalThis.require = function(mod) {
		// Try built-in modules first
		var builtin = origRequire(mod);
		if (builtin && Object.keys(builtin).length > 0) return builtin;
		if (typeof builtin === 'function') return builtin;

		// Try local file resolve
		var baseDir = globalThis.__dirname || __go_cwd();
		var resolvedJSON = __go_require_resolve(mod, baseDir);
		if (!resolvedJSON) return origRequire(mod);

		var resolved = JSON.parse(resolvedJSON);
		var absPath = resolved.path;

		// Return cached module if available.
		if (_cache[absPath]) return _cache[absPath].exports;

		var source = resolved.source;

		// Check Bun.plugin() loaders.
		if (typeof globalThis.__bunPluginResolve === 'function') {
			var plugin = globalThis.__bunPluginResolve(absPath);
			if (plugin && plugin.callback) {
				var result = plugin.callback({ path: absPath, loader: plugin.loader || 'js' });
				if (result && result.exports) { _cache[absPath] = { exports: result.exports }; return result.exports; }
				if (result && result.contents) source = result.contents;
			}
		}

		// JSON files
		if (absPath.endsWith('.json')) {
			var jsonResult = JSON.parse(source);
			_cache[absPath] = { exports: jsonResult };
			return jsonResult;
		}

		// JS files - wrap in module scope
		var moduleObj = { exports: {} };
		_cache[absPath] = moduleObj;
		var fn = new Function('exports', 'require', 'module', '__filename', '__dirname',
			source + '\n//# sourceURL=' + absPath);
		var dir = absPath.substring(0, absPath.lastIndexOf('/'));
		fn(moduleObj.exports, globalThis.require, moduleObj, absPath, dir);
		return moduleObj.exports;
	};
	globalThis.require.resolve = origRequire.resolve;
	globalThis.require._modules = origRequire._modules;
	globalThis.require.cache = _cache;
})();
	`)

	return rt, nil
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var packages packageList
	var watch bool
	var typeCheck, sandbox bool
	var workers int
	var allowRead, allowWrite, allowNet, allowEnv, allowRun string
	fs.Var(&packages, "p", "npm package to include (repeatable)")
	fs.BoolVar(&watch, "w", false, "watch for file changes and restart")
	fs.BoolVar(&watch, "watch", false, "watch for file changes and restart")
	fs.BoolVar(&typeCheck, "check", false, "type-check with tsgo before running")
	fs.IntVar(&workers, "workers", 0, "number of parallel workers for Ramune.serve()")
	fs.BoolVar(&sandbox, "sandbox", false, "deny all permissions by default")
	fs.StringVar(&allowRead, "allow-read", "", "allow file read (comma-separated paths, or empty for all)")
	fs.StringVar(&allowWrite, "allow-write", "", "allow file write (comma-separated paths)")
	fs.StringVar(&allowNet, "allow-net", "", "allow network access (comma-separated hosts)")
	fs.StringVar(&allowEnv, "allow-env", "", "allow env access (comma-separated vars)")
	fs.StringVar(&allowRun, "allow-run", "", "allow subprocess execution (comma-separated cmds)")
	var envFile string
	fs.StringVar(&envFile, "env-file", "", "load environment variables from file (default: .env)")
	fs.Parse(args)

	// Load .env file(s).
	loadDotEnv(envFile)

	filename := ""
	if fs.NArg() >= 1 {
		filename = fs.Arg(0)
	}

	// If no file specified, try package.json in current directory.
	if filename == "" || filename == "." {
		entry, pkgDeps, err := findEntryPoint(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		filename = entry
		packages = append(packages, pkgDeps...)
	}

	// If filename doesn't look like a file, try package.json scripts.
	if filename != "" && filename != "-" {
		if _, statErr := os.Stat(filename); statErr != nil && !strings.Contains(filename, "/") && !strings.Contains(filename, "\\") {
			if cmd, ok := lookupScript(filename); ok {
				execScript(cmd, fs.Args()[1:])
				return
			}
		}
	}

	var code []byte
	var err error

	if filename == "-" {
		code, err = io.ReadAll(os.Stdin)
	} else {
		code, err = os.ReadFile(filename)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Build permissions.
	var perms *ramune.Permissions
	if sandbox {
		perms = ramune.SandboxPermissions()
		if allowRead != "" {
			perms.Read = ramune.PermGranted
			if allowRead != "true" {
				perms.ReadPaths = strings.Split(allowRead, ",")
			}
		}
		if allowWrite != "" {
			perms.Write = ramune.PermGranted
			if allowWrite != "true" {
				perms.WritePaths = strings.Split(allowWrite, ",")
			}
		}
		if allowNet != "" {
			perms.Net = ramune.PermGranted
			if allowNet != "true" {
				perms.NetHosts = strings.Split(allowNet, ",")
			}
		}
		if allowEnv != "" {
			perms.Env = ramune.PermGranted
			if allowEnv != "true" {
				perms.EnvVars = strings.Split(allowEnv, ",")
			}
		}
		if allowRun != "" {
			perms.Run = ramune.PermGranted
			if allowRun != "true" {
				perms.RunCmds = strings.Split(allowRun, ",")
			}
		}
	}

	var rtOpts []ramune.Option
	if perms != nil {
		rtOpts = append(rtOpts, ramune.WithPermissions(perms))
	}
	rt, err := createRuntimeWithOpts(packages, rtOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	// Forward OS signals to process.emit().
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			name := "SIGINT"
			if sig == syscall.SIGTERM {
				name = "SIGTERM"
			}
			rt.Exec(fmt.Sprintf(`if (typeof process !== 'undefined' && process.emit) { if (!process.emit('%s')) process.exit(1); }`, name))
		}
	}()

	// Set __filename and __dirname.
	if filename != "-" {
		abs, _ := os.Getwd()
		if !strings.HasPrefix(filename, "/") {
			filename = abs + "/" + filename
		}
		rt.Exec(fmt.Sprintf(`globalThis.__filename = %q; globalThis.__dirname = %q;`,
			filename, filename[:strings.LastIndex(filename, "/")]))
	}

	// Type-check if requested.
	if typeCheck && isTypeScript(filename) {
		if err := runTypeCheck(filename); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	// Transpile TypeScript if needed.
	if isTypeScript(filename) {
		code, err = transformTypeScript(filename, code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Transpile ESM if needed.
	esm := isESM(filename, code)
	if esm {
		code, err = bundleESM(filename, code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Multi-worker mode: use RuntimePool for parallel HTTP serving.
	// The user's JS defines __poolHandle instead of Ramune.serve().
	if workers > 0 {
		rt.Close()

		var poolOpts []ramune.Option
		poolOpts = append(poolOpts, ramune.NodeCompat(), ramune.WithFetch())
		if perms != nil {
			poolOpts = append(poolOpts, ramune.WithPermissions(perms))
		}
		pool, err := ramune.NewPool(workers, poolOpts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer pool.Close()

		// Intercept Ramune.serve(): replace with a stub that captures
		// the handler, then use Pool's Go HTTP server for dispatch.
		interceptor := `
		var __workerPort = 3000;
		var __origServe = Ramune.serve;
		Ramune.serve = function(opts) {
			if (opts.port) __workerPort = opts.port;
			globalThis.__bunFetchHandler = opts.fetch;
			return { port: opts.port || 3000, stop: function(){} };
		};
		Bun.serve = Ramune.serve;
		`

		handler := `
		globalThis.__poolHandle = function(req) {
			if (!globalThis.__bunFetchHandler) return { status: 500, body: "no handler" };
			var request = new Request("http://localhost" + req.url, {
				method: req.method,
				headers: req.headers,
				body: req.method !== "GET" && req.method !== "HEAD" ? req.body : undefined
			});
			var resp = globalThis.__bunFetchHandler(request);
			if (!resp) return { status: 200, body: "" };
			// Handle Response objects (have _body from our polyfill).
			if (resp._body !== undefined) {
				return { status: resp.status || 200, body: String(resp._body) };
			}
			// Handle plain objects {status, body, headers}.
			if (resp.body !== undefined && typeof resp.body !== "object") {
				return { status: resp.status || 200, body: String(resp.body) };
			}
			// Handle string return.
			if (typeof resp === "string") {
				return { status: 200, body: resp };
			}
			return { status: resp.status || 200, body: JSON.stringify(resp.body || resp) };
		};
		`

		// Install interceptor, run user code, install pool handler.
		setup := interceptor + "\n" + string(code) + "\n" + handler
		if err := pool.Broadcast(setup); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		// Get port from first worker.
		portVal, _ := pool.Eval("__workerPort")
		port := ":3000"
		if portVal != nil {
			if f, err := portVal.Float64(); err == nil && f > 0 {
				port = fmt.Sprintf(":%d", int(f))
			}
			portVal.Close()
		}

		fmt.Printf("Ramune multi-worker server (%d workers) on %s\n", workers, port)
		if err := pool.ListenAndServe(port, ""); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	// Single runtime execution.
	if esm {
		if filename != "-" {
			rt.Exec(fmt.Sprintf(`globalThis.import_meta_url = "file://%s";`, filename))
		}
		if _, err := rt.EvalAsync(strings.TrimRight(string(code), ";\n\r\t ")); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	} else {
		if err := rt.Exec(string(code)); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	// Run event loop for any pending timers/async operations.
	rt.RunEventLoop()

	// Check process.exitCode.
	if v, err := rt.Eval("typeof process !== 'undefined' && process.exitCode ? process.exitCode : 0"); err == nil {
		if exitCode, err := v.Float64(); err == nil && exitCode != 0 {
			v.Close()
			if !watch {
				os.Exit(int(exitCode))
			}
		} else {
			v.Close()
		}
	}

	if !watch {
		return
	}

	// Watch mode: restart on file changes.
	rt.Close()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()

	absFilename := filename
	if !filepath.IsAbs(absFilename) {
		cwd, _ := os.Getwd()
		absFilename = filepath.Join(cwd, absFilename)
	}
	watcher.Add(absFilename)
	watcher.Add(filepath.Dir(absFilename))

	fmt.Fprintf(os.Stderr, "[watch] watching %s\n", absFilename)

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			ext := filepath.Ext(event.Name)
			if ext != ".js" && ext != ".ts" && ext != ".tsx" && ext != ".mjs" && ext != ".json" {
				continue
			}
			// Debounce.
			time.Sleep(100 * time.Millisecond)
			for len(watcher.Events) > 0 {
				<-watcher.Events
			}
			fmt.Fprintf(os.Stderr, "\n[watch] %s changed, restarting...\n\n", filepath.Base(event.Name))
			runCmd(args)
			return
		case err := <-watcher.Errors:
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		}
	}
}

func replCmd(args []string) {
	fset := flag.NewFlagSet("repl", flag.ExitOnError)
	var packages packageList
	fset.Var(&packages, "p", "npm package to include (repeatable)")
	fset.Parse(args)

	// Auto-load dependencies from package.json if no -p flags given.
	if len(packages) == 0 {
		if data, err := os.ReadFile("package.json"); err == nil {
			var pkg struct {
				Dependencies map[string]string `json:"dependencies"`
			}
			json.Unmarshal(data, &pkg)
			for name, version := range pkg.Dependencies {
				packages = append(packages, name+"@"+version)
			}
		}
	}

	rt, err := createRuntime(packages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	printLogo()

	// Styles.
	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true)
	contStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14"))   // cyan
	strStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))   // green
	nullStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242")) // gray
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))    // red

	// Tab completer using JSC globals.
	completer := &jsCompleter{rt: rt}

	// Setup readline.
	historyFile := filepath.Join(os.Getenv("HOME"), ".ramune_history")
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          promptStyle.Render("ramune> "),
		HistoryFile:     historyFile,
		AutoComplete:    completer,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		// Fallback to basic mode if readline fails.
		fmt.Fprintf(os.Stderr, "readline init failed, using basic mode: %v\n", err)
		replBasicMode(rt)
		return
	}
	defer rl.Close()

	fmt.Println(lipgloss.NewStyle().Bold(true).Render("ramune REPL") + " (JavaScriptCore + TypeScript)")
	fmt.Println(nullStyle.Render("Type .help for commands, .exit or Ctrl+D to quit"))
	fmt.Println()

	var multiline string

	for {
		if multiline != "" {
			rl.SetPrompt(contStyle.Render("  ...> "))
		} else {
			rl.SetPrompt(promptStyle.Render("ramune> "))
		}

		line, err := rl.Readline()
		if err != nil { // EOF or interrupt
			fmt.Println()
			break
		}

		// Commands.
		if multiline == "" {
			switch strings.TrimSpace(line) {
			case ".exit":
				return
			case ".help":
				fmt.Println(lipgloss.NewStyle().Bold(true).Render("  Commands:"))
				fmt.Println("  .exit     Exit the REPL")
				fmt.Println("  .help     Show this help")
				fmt.Println("  .clear    Reset the runtime context")
				fmt.Println()
				fmt.Println(lipgloss.NewStyle().Bold(true).Render("  Features:"))
				fmt.Println("  - TypeScript supported (type annotations are stripped)")
				fmt.Println("  - Up/Down arrows for history")
				fmt.Println("  - Tab for autocomplete")
				fmt.Println("  - Multiline input (auto-detected)")
				continue
			case ".clear":
				rt.Close()
				rt, err = createRuntime(packages)
				if err != nil {
					fmt.Fprintln(os.Stderr, errStyle.Render("Failed to reset: "+err.Error()))
					os.Exit(1)
				}
				fmt.Println(nullStyle.Render("Context cleared"))
				continue
			case "":
				continue
			}
		}

		multiline += line + "\n"

		// Transpile TypeScript.
		evalCode := multiline
		if result := api.Transform(evalCode, api.TransformOptions{
			Loader: api.LoaderTS, Target: api.ESNext,
		}); len(result.Errors) == 0 && len(result.Code) > 0 {
			evalCode = string(result.Code)
		}

		val, evalErr := rt.Eval(evalCode)
		if evalErr != nil {
			errMsg := evalErr.Error()
			if strings.Contains(errMsg, "Unexpected end of script") ||
				strings.Contains(errMsg, "Unexpected EOF") ||
				strings.Contains(errMsg, "Unterminated") {
				continue // multiline input
			}
			fmt.Fprintln(os.Stderr, errStyle.Render(errMsg))
			multiline = ""
			continue
		}

		multiline = ""
		if val != nil {
			if !val.IsUndefined() {
				output := val.String()
				// Use JSON.stringify for arrays and objects.
				if val.IsArray() || strings.HasPrefix(output, "[object") {
					if m, err := val.ToMap(); err == nil {
						if b, e := json.Marshal(m); e == nil {
							output = string(b)
						}
					} else if s, err := val.ToSlice(); err == nil {
						if b, e := json.Marshal(s); e == nil {
							output = string(b)
						}
					}
				}
				// Colorize output based on type.
				if val.IsNull() {
					fmt.Println(nullStyle.Render("null"))
				} else if output == "true" || output == "false" {
					fmt.Println(numStyle.Render(output))
				} else if _, parseErr := fmt.Sscanf(output, "%f", new(float64)); parseErr == nil {
					fmt.Println(numStyle.Render(output))
				} else if strings.HasPrefix(output, "{") || strings.HasPrefix(output, "[") {
					fmt.Println(output)
				} else {
					fmt.Println(strStyle.Render("'" + output + "'"))
				}
			}
			val.Close()
		}
	}
}

// jsCompleter implements readline.AutoCompleter for JSC globals.
type jsCompleter struct{ rt *ramune.Runtime }

func (c *jsCompleter) Do(line []rune, pos int) ([][]rune, int) {
	s := string(line[:pos])
	start := pos
	for start > 0 && (s[start-1] >= 'a' && s[start-1] <= 'z' ||
		s[start-1] >= 'A' && s[start-1] <= 'Z' ||
		s[start-1] >= '0' && s[start-1] <= '9' ||
		s[start-1] == '_') {
		start--
	}
	prefix := s[start:]
	if prefix == "" {
		return nil, 0
	}
	v, err := c.rt.Eval(fmt.Sprintf(
		`(function(){var r=[];for(var k in globalThis){if(k.indexOf(%q)===0)r.push(k)}return JSON.stringify(r.slice(0,20))})()`,
		prefix))
	if err != nil {
		return nil, 0
	}
	raw, _ := v.GoString()
	v.Close()
	var names []string
	json.Unmarshal([]byte(raw), &names)
	var candidates [][]rune
	for _, n := range names {
		candidates = append(candidates, []rune(n[len(prefix):]))
	}
	return candidates, len(prefix)
}

// replBasicMode is a fallback REPL without readline (for non-TTY).
func replBasicMode(rt *ramune.Runtime) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if line == ".exit" {
			return
		}
		val, err := rt.Eval(line)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		if val != nil && !val.IsUndefined() {
			fmt.Println(val.String())
			val.Close()
		}
	}
}

// loadDotEnv loads environment variables from .env files.
// If envFile is specified, only that file is loaded. Otherwise, .env and .env.local are loaded.
// resolveFileModule resolves a file path with extension fallback.
func resolveFileModule(resolved string) (string, error) {
	candidates := []string{
		resolved,
		resolved + ".js",
		resolved + ".ts",
		resolved + ".tsx",
		resolved + ".json",
		filepath.Join(resolved, "index.js"),
		filepath.Join(resolved, "index.ts"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return readAndTranspile(c)
		}
	}
	return "", fmt.Errorf("Cannot find module '%s'", resolved)
}

// resolveNodeModule searches node_modules directories for a package,
// supporting the package.json "exports" field.
func resolveNodeModule(mod, baseDir string) (string, error) {
	// Split module into package name and subpath (e.g. "@foo/bar/baz" -> "@foo/bar", "./baz")
	pkgName, subpath := splitModulePath(mod)

	dir := baseDir
	for {
		nmDir := filepath.Join(dir, "node_modules", pkgName)
		if info, err := os.Stat(nmDir); err == nil && info.IsDir() {
			entry, err := resolvePackageEntry(nmDir, subpath)
			if err == nil {
				return readAndTranspile(entry)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil // let built-in require handle it
}

func splitModulePath(mod string) (string, string) {
	parts := strings.SplitN(mod, "/", 3)
	if strings.HasPrefix(mod, "@") && len(parts) >= 2 {
		// Scoped package: @scope/name[/subpath]
		pkgName := parts[0] + "/" + parts[1]
		subpath := "."
		if len(parts) > 2 {
			subpath = "./" + parts[2]
		}
		return pkgName, subpath
	}
	// Regular package: name[/subpath]
	pkgName := parts[0]
	subpath := "."
	if len(parts) > 1 {
		subpath = "./" + strings.Join(parts[1:], "/")
	}
	return pkgName, subpath
}

// resolvePackageEntry resolves the entry file for a package, checking
// the "exports" field first, then falling back to "main" and index.js.
func resolvePackageEntry(pkgDir, subpath string) (string, error) {
	pkgJSON := filepath.Join(pkgDir, "package.json")
	data, err := os.ReadFile(pkgJSON)
	if err != nil {
		// No package.json — try index.js
		return resolveFileModule(filepath.Join(pkgDir, subpath))
	}

	var pkg struct {
		Main    string `json:"main"`
		Exports any    `json:"exports"`
	}
	json.Unmarshal(data, &pkg)

	// Try "exports" field first.
	if pkg.Exports != nil {
		if resolved := resolveExports(pkg.Exports, subpath); resolved != "" {
			full := filepath.Join(pkgDir, resolved)
			if _, err := os.Stat(full); err == nil {
				return full, nil
			}
		}
	}

	// Fallback: "main" field (only for root subpath ".").
	if subpath == "." && pkg.Main != "" {
		full := filepath.Join(pkgDir, pkg.Main)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}

	// Fallback: direct file resolution.
	return resolveFileModule(filepath.Join(pkgDir, subpath))
}

// resolveExports resolves a subpath against a package.json "exports" field.
// Supports: string, {".": ...}, {"./sub": ...}, {"require": ..., "import": ..., "default": ...}
func resolveExports(exports any, subpath string) string {
	switch v := exports.(type) {
	case string:
		if subpath == "." {
			return v
		}
	case map[string]any:
		// Check for direct subpath match: {"./foo": "..."}
		if val, ok := v[subpath]; ok {
			return resolveExportCondition(val)
		}
		// Check for "." entry when subpath is "."
		if subpath == "." {
			if val, ok := v["."]; ok {
				return resolveExportCondition(val)
			}
			// Condition names at top level: {"require": ..., "import": ..., "default": ...}
			return resolveExportCondition(exports)
		}
	}
	return ""
}

// resolveExportCondition resolves conditional exports (require/import/default).
func resolveExportCondition(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case map[string]any:
		for _, key := range []string{"require", "default", "import"} {
			if sub, ok := v[key]; ok {
				return resolveExportCondition(sub)
			}
		}
	case []any:
		for _, item := range v {
			if r := resolveExportCondition(item); r != "" {
				return r
			}
		}
	}
	return ""
}

func readAndTranspile(absPath string) (string, error) {
	abs, _ := filepath.Abs(absPath)
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	src := string(data)
	if isTypeScript(abs) {
		transformed, err := transformTypeScript(abs, data)
		if err != nil {
			return "", err
		}
		src = string(transformed)
	}
	result := map[string]string{"path": abs, "source": src}
	encoded, _ := json.Marshal(result)
	return string(encoded), nil
}

func loadDotEnv(envFile string) {
	files := []string{".env", ".env.local"}
	if envFile != "" {
		files = []string{envFile}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			if envFile != "" {
				fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", f, err)
			}
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.IndexByte(line, '=')
			if idx < 0 {
				continue
			}
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				val = val[1 : len(val)-1]
			}
			if _, exists := os.LookupEnv(key); !exists {
				os.Setenv(key, val)
			}
		}
	}
}

// lookupScript reads package.json and returns the command for the given script name.
func lookupScript(name string) (string, bool) {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return "", false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", false
	}
	cmd, ok := pkg.Scripts[name]
	return cmd, ok
}

// execScript runs a package.json script command via the shell.
func execScript(command string, extraArgs []string) {
	if len(extraArgs) > 0 {
		command += " " + strings.Join(extraArgs, " ")
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// findEntryPoint reads package.json and returns the entry file and dependencies.
func findEntryPoint(dir string) (string, []string, error) {
	pkgPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		// No package.json — try default files.
		for _, name := range []string{"index.ts", "index.js", "main.ts", "main.js"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil, nil
			}
		}
		return "", nil, fmt.Errorf("no input file or package.json found")
	}

	var pkg struct {
		Main         string            `json:"main"`
		Scripts      map[string]string `json:"scripts"`
		Dependencies map[string]string `json:"dependencies"`
	}
	json.Unmarshal(data, &pkg)

	// Find entry point: main → scripts.start → index.ts → index.js
	entry := ""
	if pkg.Main != "" {
		entry = filepath.Join(dir, pkg.Main)
	} else if start, ok := pkg.Scripts["start"]; ok {
		// Parse "ramune run file.js" or just "file.js"
		parts := strings.Fields(start)
		entry = filepath.Join(dir, parts[len(parts)-1])
	} else {
		for _, name := range []string{"index.ts", "index.js", "main.ts", "main.js"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				entry = p
				break
			}
		}
	}

	if entry == "" {
		return "", nil, fmt.Errorf("no entry point found in package.json")
	}

	// Collect dependencies.
	var deps []string
	for name, version := range pkg.Dependencies {
		if version != "" && version != "*" {
			deps = append(deps, name+"@"+version)
		} else {
			deps = append(deps, name)
		}
	}

	return entry, deps, nil
}

// isTypeScript checks if the file is TypeScript.
func isTypeScript(filename string) bool {
	ext := filepath.Ext(filename)
	return ext == ".ts" || ext == ".tsx"
}

// transformTypeScript transpiles TypeScript to JavaScript using esbuild.
func transformTypeScript(filename string, code []byte) ([]byte, error) {
	loader := api.LoaderTS
	if strings.HasSuffix(filename, ".tsx") {
		loader = api.LoaderTSX
	}
	result := api.Transform(string(code), api.TransformOptions{
		Sourcefile: filepath.Base(filename),
		Loader:     loader,
		Target:     api.ESNext,
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Text
		}
		return nil, fmt.Errorf("TypeScript: %s", strings.Join(msgs, "; "))
	}
	out := string(result.Code)
	// Convert export to CommonJS assignments.
	// `export function greet(...)` → `function greet(...)\nexports.greet = greet;`
	exportedNames := []string{}
	out = regexp.MustCompile(`export\s+default\s+`).ReplaceAllStringFunc(out, func(m string) string {
		exportedNames = append(exportedNames, "default")
		return "module.exports = "
	})
	re := regexp.MustCompile(`export\s+(const|let|var|function|class)\s+(\w+)`)
	out = re.ReplaceAllStringFunc(out, func(m string) string {
		parts := re.FindStringSubmatch(m)
		if len(parts) >= 3 {
			exportedNames = append(exportedNames, parts[2])
		}
		return parts[1] + " " + parts[2]
	})
	// Remove export blocks and add CJS assignments at the end.
	exportBlockRe := regexp.MustCompile(`(?m)^export\s*\{([^}]*)\};?\s*$`)
	out = exportBlockRe.ReplaceAllStringFunc(out, func(m string) string {
		inner := exportBlockRe.FindStringSubmatch(m)
		if len(inner) >= 2 {
			for _, name := range strings.Split(inner[1], ",") {
				name = strings.TrimSpace(name)
				if name != "" {
					exportedNames = append(exportedNames, name)
				}
			}
		}
		return ""
	})
	for _, name := range exportedNames {
		if name != "default" {
			out += fmt.Sprintf("\nexports.%s = %s;", name, name)
		}
	}
	return []byte(out), nil
}

// isESM detects if the code uses ESM syntax.
func isESM(filename string, code []byte) bool {
	// .mjs files are always ESM
	if strings.HasSuffix(filename, ".mjs") {
		return true
	}
	// Check for package.json "type": "module"
	if filename != "-" {
		dir := filepath.Dir(filename)
		for dir != "/" && dir != "." {
			pkgJSON := filepath.Join(dir, "package.json")
			if data, err := os.ReadFile(pkgJSON); err == nil {
				if strings.Contains(string(data), `"type":"module"`) ||
					strings.Contains(string(data), `"type": "module"`) {
					return true
				}
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	// Check for import/export keywords
	src := string(code)
	importRe := regexp.MustCompile(`(?m)^\s*(import\s+|export\s+(default\s+|const\s+|function\s+|class\s+|let\s+|var\s+|\{))`)
	return importRe.MatchString(src)
}

// bundleESM uses esbuild Build API with bundling to resolve ESM imports.
// Falls back to transformESM on failure (e.g., no node_modules).
func bundleESM(filename string, code []byte) ([]byte, error) {
	absPath := filename
	if filename == "-" || !filepath.IsAbs(filename) {
		cwd, _ := os.Getwd()
		if filename == "-" {
			absPath = filepath.Join(cwd, "stdin.mjs")
		} else {
			absPath = filepath.Join(cwd, filename)
		}
	}

	// Write source to temp file for stdin input.
	entryPoint := absPath
	if filename == "-" {
		tmpDir, err := os.MkdirTemp("", "ramune-esm-*")
		if err != nil {
			return transformESM(filename, code)
		}
		defer os.RemoveAll(tmpDir)
		entryPoint = filepath.Join(tmpDir, "stdin.mjs")
		os.WriteFile(entryPoint, code, 0o644)
	}

	// Node.js built-in modules that Ramune's NodeCompat provides.
	external := []string{
		"child_process", "fs", "path", "os", "net", "http", "https", "tls",
		"stream", "events", "util", "buffer", "crypto", "url", "querystring",
		"zlib", "string_decoder", "assert", "readline", "dns", "worker_threads",
		"bun:sqlite", "node:*",
	}

	result := api.Build(api.BuildOptions{
		EntryPoints: []string{entryPoint},
		Bundle:      true,
		Format:      api.FormatCommonJS,
		Platform:    api.PlatformNode,
		Write:       false,
		External:    external,
		LogLevel:    api.LogLevelSilent,
		MainFields:  []string{"module", "main"},
		Conditions:  []string{"import", "node", "default"},
	})

	if len(result.Errors) > 0 || len(result.OutputFiles) == 0 {
		// Fall back to regex-based transform if bundling fails.
		return transformESM(filename, code)
	}

	out := string(result.OutputFiles[0].Contents)

	// Replace dynamic import() with __dynamicImport() polyfill.
	out = strings.ReplaceAll(out, "import(", "__dynamicImport(")

	// Wrap in IIFE. Use async if source has top-level await.
	if strings.Contains(string(code), "await ") {
		out = "(async function() {\n" + out + "\n})();\n"
	} else {
		out = "(function() {\n" + out + "\n})();\n"
	}

	return []byte(out), nil
}

// transformESM transpiles ESM to IIFE using esbuild (single-file, no bundling).
func transformESM(filename string, code []byte) ([]byte, error) {
	absPath := filename
	if filename == "-" {
		absPath = "stdin.mjs"
	}

	// Use esbuild Transform API (not Build) with ESM format, then wrap.
	transformed := api.Transform(string(code), api.TransformOptions{
		Sourcefile: filepath.Base(absPath),
		Loader:     api.LoaderJS,
		Format:     api.FormatESModule,
		Target:     api.ESNext,
	})

	result := struct {
		Errors      []api.Message
		OutputFiles []api.OutputFile
	}{
		Errors: transformed.Errors,
	}
	if len(transformed.Errors) == 0 {
		// Wrap in async IIFE to support top-level await.
		// Replace import statements with require() calls.
		out := string(transformed.Code)
		// Convert `import X from "Y"` → `const X = require("Y")`
		// Convert `import { A, B } from "Y"` → `const { A, B } = require("Y")`
		importRe := regexp.MustCompile(`import\s+(\{[^}]+\}|\w+)\s+from\s+("[^"]+"|'[^']+');?\n?`)
		out = importRe.ReplaceAllStringFunc(out, func(match string) string {
			parts := importRe.FindStringSubmatch(match)
			if len(parts) < 3 {
				return match
			}
			binding := parts[1]
			mod := parts[2]
			// Default import: `import X from "Y"` → `const X = require(Y).default || require(Y)`
			if !strings.HasPrefix(binding, "{") {
				return fmt.Sprintf("const %s = (function() { var _m = require(%s); return _m.default !== undefined ? _m.default : _m; })();\n", binding, mod)
			}
			return fmt.Sprintf("const %s = require(%s);\n", binding, mod)
		})
		// Remove export statements: `export default X` → `X`, `export const X` → `const X`
		out = regexp.MustCompile(`export\s+default\s+`).ReplaceAllString(out, "")
		out = regexp.MustCompile(`export\s+(const|let|var|function|class)\s+`).ReplaceAllString(out, "$1 ")
		// Remove export blocks: `export { X, Y };`
		out = regexp.MustCompile(`(?m)^export\s*\{[^}]*\};?\s*$`).ReplaceAllString(out, "")

		wrapped := "(async function() {\n" + out + "\n})();\n"
		result.OutputFiles = []api.OutputFile{{Contents: []byte(wrapped)}}
	}

	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Text
		}
		return nil, fmt.Errorf("ESM transform: %s", strings.Join(msgs, "; "))
	}

	if len(result.OutputFiles) == 0 {
		return code, nil
	}
	return result.OutputFiles[0].Contents, nil
}

func testCmd(args []string) {
	fset := flag.NewFlagSet("test", flag.ExitOnError)
	var packages packageList
	fset.Var(&packages, "p", "npm package to include (repeatable)")
	fset.Parse(args)

	// Find test files.
	pattern := "."
	if fset.NArg() > 0 {
		pattern = fset.Arg(0)
	}

	var testFiles []string
	filepath.Walk(pattern, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".test.js") || strings.HasSuffix(name, ".test.ts") ||
			strings.HasSuffix(name, ".spec.js") || strings.HasSuffix(name, ".spec.ts") ||
			strings.HasSuffix(name, "_test.js") || strings.HasSuffix(name, "_test.ts") {
			testFiles = append(testFiles, path)
		}
		return nil
	})

	if len(testFiles) == 0 {
		fmt.Fprintln(os.Stderr, "No test files found")
		os.Exit(1)
	}

	// Test framework JS — injected into each test runtime.
	// Phase 5d: beforeEach/afterEach/beforeAll/afterAll, test.skip/only/todo, async tests.
	testFramework := `
globalThis.__tests = [];
globalThis.__currentDescribe = '';
globalThis.__beforeEach = [];
globalThis.__afterEach = [];
globalThis.__beforeAll = [];
globalThis.__afterAll = [];
globalThis.__hasOnly = false;
globalThis.__onlyTests = [];

globalThis.beforeEach = function(fn) { globalThis.__beforeEach.push(fn); };
globalThis.afterEach = function(fn) { globalThis.__afterEach.push(fn); };
globalThis.beforeAll = function(fn) { globalThis.__beforeAll.push(fn); };
globalThis.afterAll = function(fn) { globalThis.__afterAll.push(fn); };

globalThis.describe = function(name, fn) {
	var prev = globalThis.__currentDescribe;
	var prevBE = globalThis.__beforeEach.slice();
	var prevAE = globalThis.__afterEach.slice();
	globalThis.__currentDescribe = prev ? prev + ' > ' + name : name;
	fn();
	globalThis.__currentDescribe = prev;
	globalThis.__beforeEach = prevBE;
	globalThis.__afterEach = prevAE;
};

function __runTest(name, fn, opts) {
	var fullName = globalThis.__currentDescribe ? globalThis.__currentDescribe + ' > ' + name : name;
	if (opts && opts.skip) {
		globalThis.__tests.push({name: fullName, pass: true, ms: 0, skipped: true});
		return;
	}
	if (opts && opts.todo) {
		globalThis.__tests.push({name: fullName, pass: true, ms: 0, todo: true});
		return;
	}
	if (opts && opts.only) {
		globalThis.__hasOnly = true;
		globalThis.__onlyTests.push(fullName);
	}
	var start = Date.now();
	try {
		for (var i = 0; i < globalThis.__beforeEach.length; i++) globalThis.__beforeEach[i]();
		var result = fn();
		if (result && typeof result.then === 'function') {
			// Async test — store promise for later resolution.
			globalThis.__tests.push({name: fullName, pass: true, ms: Date.now() - start, async: true, promise: result});
		} else {
			globalThis.__tests.push({name: fullName, pass: true, ms: Date.now() - start});
		}
		for (var i = 0; i < globalThis.__afterEach.length; i++) globalThis.__afterEach[i]();
	} catch(e) {
		globalThis.__tests.push({name: fullName, pass: false, ms: Date.now() - start, error: String(e)});
		for (var i = 0; i < globalThis.__afterEach.length; i++) try { globalThis.__afterEach[i](); } catch(_) {}
	}
}

globalThis.test = globalThis.it = function(name, fn) { __runTest(name, fn); };
globalThis.test.skip = function(name, fn) { __runTest(name, fn, {skip: true}); };
globalThis.test.only = function(name, fn) { __runTest(name, fn, {only: true}); };
globalThis.test.todo = function(name) { __runTest(name, function(){}, {todo: true}); };

globalThis.expect = function(actual) {
	return {
		toBe: function(expected) {
			if (actual !== expected) throw new Error('Expected ' + JSON.stringify(expected) + ' but got ' + JSON.stringify(actual));
		},
		toEqual: function(expected) {
			if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error('Expected ' + JSON.stringify(expected) + ' but got ' + JSON.stringify(actual));
		},
		toBeTruthy: function() { if (!actual) throw new Error('Expected truthy but got ' + JSON.stringify(actual)); },
		toBeFalsy: function() { if (actual) throw new Error('Expected falsy but got ' + JSON.stringify(actual)); },
		toBeNull: function() { if (actual !== null) throw new Error('Expected null but got ' + JSON.stringify(actual)); },
		toBeUndefined: function() { if (actual !== undefined) throw new Error('Expected undefined but got ' + JSON.stringify(actual)); },
		toBeDefined: function() { if (actual === undefined) throw new Error('Expected defined but got undefined'); },
		toContain: function(item) {
			if (typeof actual === 'string') { if (actual.indexOf(item) === -1) throw new Error('Expected "' + actual + '" to contain "' + item + '"'); }
			else if (Array.isArray(actual)) { if (actual.indexOf(item) === -1) throw new Error('Expected array to contain ' + JSON.stringify(item)); }
		},
		toThrow: function(expected) {
			var threw = false, msg = '';
			try { actual(); } catch(e) { threw = true; msg = String(e); }
			if (!threw) throw new Error('Expected function to throw');
			if (expected && msg.indexOf(expected) === -1) throw new Error('Expected error containing "' + expected + '" but got "' + msg + '"');
		},
		toBeGreaterThan: function(n) { if (!(actual > n)) throw new Error('Expected ' + actual + ' > ' + n); },
		toBeGreaterThanOrEqual: function(n) { if (!(actual >= n)) throw new Error('Expected ' + actual + ' >= ' + n); },
		toBeLessThan: function(n) { if (!(actual < n)) throw new Error('Expected ' + actual + ' < ' + n); },
		toBeLessThanOrEqual: function(n) { if (!(actual <= n)) throw new Error('Expected ' + actual + ' <= ' + n); },
		toHaveLength: function(n) { if (actual.length !== n) throw new Error('Expected length ' + n + ' but got ' + actual.length); },
		toMatch: function(pattern) {
			if (typeof pattern === 'string') { if (actual.indexOf(pattern) === -1) throw new Error('Expected to match'); }
			else if (pattern instanceof RegExp) { if (!pattern.test(actual)) throw new Error('Expected to match ' + pattern); }
		},
		toBeInstanceOf: function(cls) { if (!(actual instanceof cls)) throw new Error('Expected instance of ' + cls.name); },
		toBeCloseTo: function(num, digits) {
			digits = digits === undefined ? 2 : digits;
			var diff = Math.abs(actual - num);
			if (diff >= Math.pow(10, -digits) / 2) throw new Error('Expected ' + actual + ' to be close to ' + num);
		},
		not: {
			toBe: function(expected) { if (actual === expected) throw new Error('Expected not ' + JSON.stringify(expected)); },
			toEqual: function(expected) { if (JSON.stringify(actual) === JSON.stringify(expected)) throw new Error('Expected not equal'); },
			toContain: function(item) {
				if (typeof actual === 'string' && actual.indexOf(item) !== -1) throw new Error('Expected not to contain');
				if (Array.isArray(actual) && actual.indexOf(item) !== -1) throw new Error('Expected not to contain');
			},
			toBeNull: function() { if (actual === null) throw new Error('Expected not null'); },
			toBeUndefined: function() { if (actual === undefined) throw new Error('Expected not undefined'); },
			toThrow: function() { try { actual(); } catch(e) { throw new Error('Expected not to throw but threw: ' + e); } },
		},
		toHaveBeenCalled: function() {
			if (!actual || !actual._isMockFn || actual.mock.calls.length === 0) throw new Error('Expected function to have been called');
		},
		toHaveBeenCalledTimes: function(n) {
			if (!actual || !actual._isMockFn) throw new Error('Expected a mock function');
			if (actual.mock.calls.length !== n) throw new Error('Expected ' + n + ' calls but got ' + actual.mock.calls.length);
		},
		toHaveBeenCalledWith: function() {
			if (!actual || !actual._isMockFn) throw new Error('Expected a mock function');
			var expected = Array.prototype.slice.call(arguments);
			var found = actual.mock.calls.some(function(call) { return JSON.stringify(call) === JSON.stringify(expected); });
			if (!found) throw new Error('Expected to have been called with ' + JSON.stringify(expected));
		},
		toHaveReturnedWith: function(value) {
			if (!actual || !actual._isMockFn) throw new Error('Expected a mock function');
			var found = actual.mock.results.some(function(r) { return r.type === 'return' && JSON.stringify(r.value) === JSON.stringify(value); });
			if (!found) throw new Error('Expected to have returned with ' + JSON.stringify(value));
		}
	};
};

// --- jest.fn / jest.mock ---
var _allMocks = [];
globalThis.jest = {
	fn: function(impl) {
		function mockFn() {
			var args = Array.prototype.slice.call(arguments);
			mockFn.mock.calls.push(args);
			mockFn.mock.instances.push(this);
			try {
				var fn = mockFn._onceQueue.length > 0 ? mockFn._onceQueue.shift() : mockFn._impl;
				var result = fn ? fn.apply(this, args) : undefined;
				mockFn.mock.results.push({ type: 'return', value: result });
				return result;
			} catch(e) {
				mockFn.mock.results.push({ type: 'throw', value: e });
				throw e;
			}
		}
		mockFn._isMockFn = true;
		_allMocks.push(mockFn);
		mockFn._impl = impl || null;
		mockFn._onceQueue = [];
		mockFn.mock = { calls: [], results: [], instances: [] };
		mockFn.mockClear = function() { mockFn.mock.calls = []; mockFn.mock.results = []; mockFn.mock.instances = []; return mockFn; };
		mockFn.mockReset = function() { mockFn.mockClear(); mockFn._impl = null; mockFn._onceQueue = []; return mockFn; };
		mockFn.mockImplementation = function(fn) { mockFn._impl = fn; return mockFn; };
		mockFn.mockImplementationOnce = function(fn) { mockFn._onceQueue.push(fn); return mockFn; };
		mockFn.mockReturnValue = function(val) { mockFn._impl = function() { return val; }; return mockFn; };
		mockFn.mockReturnValueOnce = function(val) { return mockFn.mockImplementationOnce(function() { return val; }); };
		mockFn.mockResolvedValue = function(val) { mockFn._impl = function() { return Promise.resolve(val); }; return mockFn; };
		mockFn.mockResolvedValueOnce = function(val) { return mockFn.mockImplementationOnce(function() { return Promise.resolve(val); }); };
		mockFn.mockRejectedValue = function(val) { mockFn._impl = function() { return Promise.reject(val); }; return mockFn; };
		mockFn.mockRejectedValueOnce = function(val) { return mockFn.mockImplementationOnce(function() { return Promise.reject(val); }); };
		return mockFn;
	},
	spyOn: function(obj, method) {
		var original = obj[method];
		var spy = globalThis.jest.fn(original);
		spy.mockRestore = function() { obj[method] = original; };
		obj[method] = spy;
		return spy;
	},
	mock: function() {},
	unmock: function() {},
	clearAllMocks: function() { _allMocks.forEach(function(m) { m.mockClear(); }); },
	resetAllMocks: function() { _allMocks.forEach(function(m) { m.mockReset(); }); }
};
`

	totalPass := 0
	totalFail := 0
	totalSkip := 0
	startTime := time.Now()

	for _, file := range testFiles {
		fmt.Fprintf(os.Stderr, " %s\n", file)

		code, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			totalFail++
			continue
		}

		rt, err := createRuntime(packages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			totalFail++
			continue
		}

		rt.Exec(testFramework)

		// Set __filename/__dirname.
		abs, _ := filepath.Abs(file)
		rt.Exec(fmt.Sprintf(`globalThis.__filename = %q; globalThis.__dirname = %q;`,
			abs, filepath.Dir(abs)))

		// Transpile if TypeScript.
		src := string(code)
		if isTypeScript(file) {
			transformed, err := transformTypeScript(file, code)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
				totalFail++
				rt.Close()
				continue
			}
			src = string(transformed)
		}

		if err := rt.Exec(src); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			totalFail++
			rt.Close()
			continue
		}

		// Read results.
		v, err := rt.Eval("JSON.stringify(globalThis.__tests)")
		if err != nil {
			rt.Close()
			continue
		}
		resultsJSON, _ := v.GoString()
		v.Close()
		rt.Close()

		var results []struct {
			Name    string `json:"name"`
			Pass    bool   `json:"pass"`
			MS      int    `json:"ms"`
			Error   string `json:"error"`
			Skipped bool   `json:"skipped"`
			Todo    bool   `json:"todo"`
		}
		json.Unmarshal([]byte(resultsJSON), &results)

		for _, r := range results {
			if r.Skipped {
				totalSkip++
				fmt.Fprintf(os.Stderr, "  \033[33m○\033[0m %s (skipped)\n", r.Name)
			} else if r.Todo {
				totalSkip++
				fmt.Fprintf(os.Stderr, "  \033[35m◌\033[0m %s (todo)\n", r.Name)
			} else if r.Pass {
				totalPass++
				fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m %s (%dms)\n", r.Name, r.MS)
			} else {
				totalFail++
				fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s (%dms)\n", r.Name, r.MS)
				fmt.Fprintf(os.Stderr, "    \033[31m%s\033[0m\n", r.Error)
			}
		}
	}

	elapsed := time.Since(startTime)
	fmt.Fprintln(os.Stderr)
	skipStr := ""
	if totalSkip > 0 {
		skipStr = fmt.Sprintf(", \033[33m%d skipped\033[0m", totalSkip)
	}
	if totalFail > 0 {
		fmt.Fprintf(os.Stderr, " \033[32m%d passed\033[0m, \033[31m%d failed\033[0m%s (%v)\n", totalPass, totalFail, skipStr, elapsed.Round(time.Millisecond))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, " \033[32m%d passed\033[0m%s (%v)\n", totalPass, skipStr, elapsed.Round(time.Millisecond))
}

func checkCmd(args []string) {
	if len(args) < 1 {
		args = []string{"."}
	}

	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if info.IsDir() {
			filepath.Walk(arg, func(path string, fi os.FileInfo, err error) error {
				if fi != nil && fi.IsDir() && fi.Name() == "node_modules" {
					return filepath.SkipDir
				}
				if isTypeScript(path) {
					files = append(files, path)
				}
				return nil
			})
		} else {
			files = append(files, arg)
		}
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No TypeScript files found")
		os.Exit(1)
	}

	// Resolve absolute paths
	absFiles := make([]string, len(files))
	for i, f := range files {
		abs, _ := filepath.Abs(f)
		absFiles[i] = tspath.NormalizePath(abs)
	}

	cwd, _ := os.Getwd()
	fs := osvfs.FS()
	host := compiler.NewCachedFSCompilerHost(cwd, fs, "", nil, nil)

	compilerOpts := &core.CompilerOptions{
		NoEmit: core.TSTrue,
		Strict: core.TSTrue,
	}

	config := tsoptions.NewParsedCommandLine(compilerOpts, absFiles, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
		CurrentDirectory:          cwd,
	})

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	ctx := context.Background()
	hasErrors := false

	for _, sourceFile := range program.SourceFiles() {
		// Skip declaration files and non-target files
		if sourceFile.IsDeclarationFile {
			continue
		}

		diags := program.GetSemanticDiagnostics(ctx, sourceFile)
		diags = append(diags, program.GetSyntacticDiagnostics(ctx, sourceFile)...)
		for _, d := range diags {
			hasErrors = true
			if d.File() != nil {
				line, col := scanner.GetECMALineAndUTF16CharacterOfPosition(d.File(), d.Pos())
				fmt.Fprintf(os.Stderr, "%s(%d,%d): error TS%d: %s\n",
					d.File().FileName(), line+1, col+1, d.Code(), d.String())
			} else {
				fmt.Fprintf(os.Stderr, "error TS%d: %s\n", d.Code(), d.String())
			}
		}
	}

	if hasErrors {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✓ %d file(s) checked, no errors\n", len(files))
}

func lintCmd(args []string) {
	fset := flag.NewFlagSet("lint", flag.ExitOnError)
	fix := fset.Bool("fix", false, "automatically fix lint issues")
	fset.Parse(args)

	targets := fset.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}

	var files []string
	for _, arg := range targets {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if info.IsDir() {
			filepath.Walk(arg, func(path string, fi os.FileInfo, err error) error {
				if fi != nil && fi.IsDir() && fi.Name() == "node_modules" {
					return filepath.SkipDir
				}
				if isJSOrTS(path) {
					abs, _ := filepath.Abs(path)
					files = append(files, tspath.NormalizePath(abs))
				}
				return nil
			})
		} else {
			abs, _ := filepath.Abs(arg)
			files = append(files, tspath.NormalizePath(abs))
		}
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No JS/TS files found")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	fs := osvfs.FS()
	host := rslintutils.CreateCompilerHost(cwd, fs)

	compilerOpts := &core.CompilerOptions{
		NoEmit:       core.TSTrue,
		AllowJs:      core.TSTrue,
		CheckJs:      core.TSFalse,
		Strict:       core.TSTrue,
		SkipLibCheck: core.TSTrue,
	}

	program, err := rslintutils.CreateProgramFromOptions(true, compilerOpts, files, host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	config.RegisterAllRules()

	// Try loading rslint.json/rslint.jsonc, fall back to all registered rules
	loader := config.NewConfigLoader(fs, cwd)
	rslintConfig, _, _, configErr := loader.LoadConfiguration("")
	useAllRules := configErr != nil
	hasErrors := false

	fileCount, _ := linter.RunLinter(
		[]*compiler.Program{program},
		true,
		files,
		nil,
		func(sourceFile *ast.SourceFile) []linter.ConfiguredRule {
			if useAllRules {
				// No config file: enable all registered rules with warning severity
				var rules []linter.ConfiguredRule
				for name, r := range config.GlobalRuleRegistry.GetAllRules() {
					ruleCopy := r
					rules = append(rules, linter.ConfiguredRule{
						Name:     name,
						Severity: rule.SeverityWarning,
						Run: func(ctx rule.RuleContext) rule.RuleListeners {
							return ruleCopy.Run(ctx, nil)
						},
					})
				}
				return rules
			}
			rules, _ := config.GlobalRuleRegistry.GetEnabledRules(rslintConfig, sourceFile.FileName(), cwd, false)
			return rules
		},
		func(d rule.RuleDiagnostic) {
			hasErrors = true
			severity := "warning"
			if d.Severity == rule.SeverityError {
				severity = "error"
			}
			if d.SourceFile != nil {
				line, col := scanner.GetECMALineAndUTF16CharacterOfPosition(d.SourceFile, d.Range.Pos())
				fmt.Fprintf(os.Stderr, "%s(%d,%d): %s [%s] %s\n",
					d.SourceFile.FileName(), line+1, col+1, d.Message.Description, d.RuleName, severity)
			} else {
				fmt.Fprintf(os.Stderr, "%s [%s] %s\n", d.Message.Description, d.RuleName, severity)
			}

			if *fix && d.SourceFile != nil {
				fixes := d.Fixes()
				if len(fixes) > 0 {
					source := d.SourceFile.Text()
					changes := make([]core.TextChange, len(fixes))
					for i, f := range fixes {
						changes[i] = core.TextChange{TextRange: f.Range, NewText: f.Text}
					}
					result := core.ApplyBulkEdits(source, changes)
					os.WriteFile(d.SourceFile.FileName(), []byte(result), 0644)
				}
			}
		},
	)

	_ = fileCount

	if hasErrors {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "✓ %d file(s) linted, no issues\n", len(files))
}

// runTypeCheck performs in-process type checking on a single file.
func runTypeCheck(filename string) error {
	absPath, _ := filepath.Abs(filename)
	normalized := tspath.NormalizePath(absPath)

	cwd, _ := os.Getwd()
	fs := osvfs.FS()
	host := compiler.NewCachedFSCompilerHost(cwd, fs, "", nil, nil)

	compilerOpts := &core.CompilerOptions{
		NoEmit: core.TSTrue,
		Strict: core.TSTrue,
	}

	config := tsoptions.NewParsedCommandLine(compilerOpts, []string{normalized}, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
		CurrentDirectory:          cwd,
	})

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	ctx := context.Background()
	var errs []string

	for _, sourceFile := range program.SourceFiles() {
		if sourceFile.IsDeclarationFile {
			continue
		}
		diags := program.GetSemanticDiagnostics(ctx, sourceFile)
		diags = append(diags, program.GetSyntacticDiagnostics(ctx, sourceFile)...)
		for _, d := range diags {
			if d.File() != nil {
				line, col := scanner.GetECMALineAndUTF16CharacterOfPosition(d.File(), d.Pos())
				errs = append(errs, fmt.Sprintf("%s(%d,%d): error TS%d: %s",
					d.File().FileName(), line+1, col+1, d.Code(), d.String()))
			} else {
				errs = append(errs, fmt.Sprintf("error TS%d: %s", d.Code(), d.String()))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

func isJSOrTS(filename string) bool {
	ext := filepath.Ext(filename)
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs", ".cts":
		return true
	}
	return false
}

func scriptKindForFile(filename string) core.ScriptKind {
	kind := core.GetScriptKindFromFileName(filename)
	if kind == core.ScriptKindUnknown {
		return core.ScriptKindJS
	}
	return kind
}

func fmtCmd(args []string) {
	fset := flag.NewFlagSet("fmt", flag.ExitOnError)
	checkOnly := fset.Bool("check", false, "check formatting without writing changes")
	fset.Parse(args)

	targets := fset.Args()
	if len(targets) == 0 {
		targets = []string{"."}
	}

	var files []string
	for _, arg := range targets {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if info.IsDir() {
			filepath.Walk(arg, func(path string, fi os.FileInfo, err error) error {
				if fi != nil && fi.IsDir() && fi.Name() == "node_modules" {
					return filepath.SkipDir
				}
				if isJSOrTS(path) {
					files = append(files, path)
				}
				return nil
			})
		} else {
			files = append(files, arg)
		}
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No JS/TS files found")
		os.Exit(1)
	}

	opts := lsutil.GetDefaultFormatCodeSettings()
	ctx := format.WithFormatCodeSettings(context.Background(), opts, "\n")

	hasErrors := false
	formatted := 0
	for _, f := range files {
		source, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", f, err)
			hasErrors = true
			continue
		}

		absPath, _ := filepath.Abs(f)
		normalizedPath := tspath.NormalizePath(absPath)
		sourceText := string(source)
		sourceFile := parser.ParseSourceFile(
			ast.SourceFileParseOptions{
				FileName: normalizedPath,
				Path:     tspath.Path(normalizedPath),
			},
			sourceText,
			scriptKindForFile(f),
		)

		changes := format.FormatDocument(ctx, sourceFile)
		if len(changes) == 0 {
			continue
		}

		if *checkOnly {
			fmt.Fprintf(os.Stderr, "%s\n", f)
			hasErrors = true
			continue
		}

		result := core.ApplyBulkEdits(sourceText, changes)
		if err := os.WriteFile(f, []byte(result), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", f, err)
			hasErrors = true
			continue
		}
		formatted++
	}

	if *checkOnly {
		if hasErrors {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "✓ %d file(s) already formatted\n", len(files))
	} else if !hasErrors {
		fmt.Fprintf(os.Stderr, "✓ %d file(s) formatted\n", formatted)
	}

	if hasErrors && !*checkOnly {
		os.Exit(1)
	}
}

func setupJITCmd() {
	if goruntime.GOOS != "darwin" {
		fmt.Println("JIT is enabled by default on Linux. No setup needed.")
		return
	}

	// Find the ramune binary.
	binPath, err := exec.LookPath("ramune")
	if err != nil {
		binPath = filepath.Join(os.Getenv("GOPATH"), "bin", "ramune")
		if os.Getenv("GOPATH") == "" {
			home, _ := os.UserHomeDir()
			binPath = filepath.Join(home, "go", "bin", "ramune")
		}
	}

	if _, err := os.Stat(binPath); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot find ramune binary at %s\n", binPath)
		os.Exit(1)
	}

	// Write entitlements to temp file.
	entitlements := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>com.apple.security.cs.allow-jit</key>
    <true/>
    <key>com.apple.security.cs.allow-unsigned-executable-memory</key>
    <true/>
</dict>
</plist>`
	tmpFile := filepath.Join(os.TempDir(), "ramune-entitlements.plist")
	os.WriteFile(tmpFile, []byte(entitlements), 0o644)
	defer os.Remove(tmpFile)

	// Sign.
	cmd := exec.Command("codesign", "--force", "--sign", "-", "--entitlements", tmpFile, binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to sign: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("JIT enabled for %s\n", binPath)
	fmt.Println("JavaScriptCore will now use its JIT compiler for full performance.")
}

func initCmd() {
	if _, err := os.Stat("package.json"); err == nil {
		fmt.Fprintln(os.Stderr, "package.json already exists")
		os.Exit(1)
	}
	cwd, _ := os.Getwd()
	name := filepath.Base(cwd)
	pkg := map[string]any{
		"name":         name,
		"version":      "1.0.0",
		"main":         "index.ts",
		"type":         "module",
		"dependencies": map[string]string{},
	}
	data, _ := json.MarshalIndent(pkg, "", "  ")
	os.WriteFile("package.json", append(data, '\n'), 0o644)
	fmt.Println("Created package.json")
}

func addCmd(args []string) {
	// Parse -d flag for devDependencies.
	dev := false
	var packages []string
	for _, a := range args {
		if a == "-d" {
			dev = true
		} else {
			packages = append(packages, a)
		}
	}

	if len(packages) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ramune add [-d] <package> [package...]")
		os.Exit(1)
	}

	// Read or create package.json.
	var pkg map[string]any
	if data, err := os.ReadFile("package.json"); err == nil {
		json.Unmarshal(data, &pkg)
	} else {
		pkg = map[string]any{"name": "app", "version": "1.0.0", "dependencies": map[string]string{}}
	}

	depsKey := "dependencies"
	if dev {
		depsKey = "devDependencies"
	}

	deps, _ := pkg[depsKey].(map[string]any)
	if deps == nil {
		deps = map[string]any{}
	}

	// Resolve each package from the npm registry to get the actual version.
	for _, arg := range packages {
		name := arg
		versionRange := "latest"
		if i := strings.LastIndex(arg, "@"); i > 0 {
			name = arg[:i]
			versionRange = arg[i+1:]
		}

		resolved, err := registry.ResolvePackage(name, versionRange)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		deps[name] = "^" + resolved.Version
		fmt.Printf("+ %s@%s\n", name, resolved.Version)
	}

	pkg[depsKey] = deps
	data, _ := json.MarshalIndent(pkg, "", "  ")
	os.WriteFile("package.json", append(data, '\n'), 0o644)

	// Install packages.
	installCmd()
}

func removeCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ramune remove <package> [package...]")
		os.Exit(1)
	}

	data, err := os.ReadFile("package.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "No package.json found")
		os.Exit(1)
	}

	var pkg map[string]any
	json.Unmarshal(data, &pkg)

	deps, _ := pkg["dependencies"].(map[string]any)
	devDeps, _ := pkg["devDependencies"].(map[string]any)
	for _, name := range args {
		delete(deps, name)
		if devDeps != nil {
			delete(devDeps, name)
		}
		os.RemoveAll(filepath.Join("node_modules", name))
		fmt.Printf("- %s\n", name)
	}

	pkg["dependencies"] = deps
	if devDeps != nil {
		pkg["devDependencies"] = devDeps
	}
	out, _ := json.MarshalIndent(pkg, "", "  ")
	os.WriteFile("package.json", append(out, '\n'), 0o644)

	// Update lockfile.
	lf, _ := registry.ReadLockfile("ramune.lock")
	if lf != nil {
		for key := range lf.Packages {
			for _, name := range args {
				if strings.HasPrefix(key, name+"@") {
					delete(lf.Packages, key)
				}
			}
		}
		registry.WriteLockfile("ramune.lock", lf)
	}
}

func installCmd() {
	data, err := os.ReadFile("package.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "No package.json found")
		os.Exit(1)
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	json.Unmarshal(data, &pkg)
	// Merge dependencies and devDependencies.
	allDeps := make(map[string]string)
	for name, version := range pkg.Dependencies {
		allDeps[name] = version
	}
	for name, version := range pkg.DevDependencies {
		allDeps[name] = version
	}
	if len(allDeps) == 0 {
		fmt.Println("No dependencies to install")
		return
	}

	// Check for lockfile.
	lf, _ := registry.ReadLockfile("ramune.lock")
	if lf != nil && len(lf.Packages) > 0 {
		fmt.Println("Installing from ramune.lock...")
		if err := registry.InstallFromLockfile(lf, "node_modules"); err != nil {
			fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("Resolving packages...")
		specs := make([]string, 0, len(allDeps))
		for name, version := range allDeps {
			specs = append(specs, name+"@"+version)
		}
		resolved, err := registry.ResolveAndInstall(specs, "node_modules")
		if err != nil {
			fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
			os.Exit(1)
		}
		registry.WriteLockfile("ramune.lock", registry.LockfileFromResolved(resolved))
	}
	fmt.Println("Done")
}

func evalCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: no expression specified")
		os.Exit(1)
	}

	expr := strings.Join(args, " ")

	// Transpile TypeScript type annotations if present.
	if result := api.Transform(expr, api.TransformOptions{
		Loader: api.LoaderTS,
		Target: api.ESNext,
	}); len(result.Errors) == 0 && len(result.Code) > 0 {
		jsCode := strings.TrimRight(string(result.Code), "\n\r\t ")
		// If the result contains multiple statements, wrap in an IIFE
		// that returns the last expression.
		lines := strings.Split(jsCode, "\n")
		if len(lines) > 1 {
			last := strings.TrimRight(lines[len(lines)-1], "; ")
			prev := strings.Join(lines[:len(lines)-1], "\n")
			expr = fmt.Sprintf("(function(){%s; return %s})()", prev, last)
		} else {
			expr = strings.TrimRight(jsCode, ";")
		}
	}

	rt, err := createRuntime(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	val, err := rt.EvalAsync(expr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer val.Close()

	if !val.IsUndefined() && !val.IsNull() {
		fmt.Println(val.String())
	}
}

func skillsCmd(args []string) {
	if len(args) == 0 || args[0] != "install" {
		fmt.Fprintln(os.Stderr, "usage: ramune skills install")
		os.Exit(1)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	data, _ := skillsFS.ReadFile("skills/ramune/SKILL.md")
	targets := []string{
		filepath.Join(home, ".agents", "skills", "ramune"),
		filepath.Join(".claude", "skills", "ramune"),
	}
	for _, dir := range targets {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("+ ramune skill installed")
	fmt.Printf("  %s\n", filepath.Join(home, ".agents", "skills", "ramune", "SKILL.md"))
	fmt.Printf("  %s\n", filepath.Join(".claude", "skills", "ramune", "SKILL.md"))
}

// benchCmd implements `ramune bench [dir|file]`.
// Runs .bench.js/.bench.ts files and reports timing results.
func benchCmd(args []string) {
	fset := flag.NewFlagSet("bench", flag.ExitOnError)
	var packages packageList
	fset.Var(&packages, "p", "npm package to include (repeatable)")
	fset.Parse(args)

	pattern := "."
	if fset.NArg() > 0 {
		pattern = fset.Arg(0)
	}

	var benchFiles []string
	filepath.Walk(pattern, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if strings.HasSuffix(name, ".bench.js") || strings.HasSuffix(name, ".bench.ts") {
			benchFiles = append(benchFiles, path)
		}
		return nil
	})

	if len(benchFiles) == 0 {
		fmt.Fprintln(os.Stderr, "No bench files found (*.bench.js, *.bench.ts)")
		os.Exit(1)
	}

	benchFramework := `
globalThis.__benches = [];

globalThis.bench = function(name, fn, opts) {
	opts = opts || {};
	var iterations = opts.iterations || 1000;
	var warmup = opts.warmup || 100;
	// Warmup
	for (var i = 0; i < warmup; i++) fn();
	// Measure
	var start = Date.now();
	for (var i = 0; i < iterations; i++) fn();
	var elapsed = Date.now() - start;
	var opsPerSec = Math.round(iterations / (elapsed / 1000));
	var nsPerOp = Math.round((elapsed * 1000000) / iterations);
	globalThis.__benches.push({name: name, iterations: iterations, ms: elapsed, opsPerSec: opsPerSec, nsPerOp: nsPerOp});
};
`

	for _, file := range benchFiles {
		fmt.Fprintf(os.Stderr, " %s\n", file)

		code, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			continue
		}

		rt, err := createRuntime(packages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			continue
		}

		rt.Exec(benchFramework)

		abs, _ := filepath.Abs(file)
		rt.Exec(fmt.Sprintf(`globalThis.__filename=%q;globalThis.__dirname=%q;`, abs, filepath.Dir(abs)))

		src := string(code)
		if isTypeScript(file) {
			transformed, err := transformTypeScript(file, code)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
				rt.Close()
				continue
			}
			src = string(transformed)
		}

		if err := rt.Exec(src); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			rt.Close()
			continue
		}

		v, err := rt.Eval("JSON.stringify(globalThis.__benches)")
		if err != nil {
			rt.Close()
			continue
		}
		resultsJSON, _ := v.GoString()
		v.Close()
		rt.Close()

		var results []struct {
			Name       string `json:"name"`
			Iterations int    `json:"iterations"`
			MS         int    `json:"ms"`
			OpsPerSec  int    `json:"opsPerSec"`
			NsPerOp    int    `json:"nsPerOp"`
		}
		json.Unmarshal([]byte(resultsJSON), &results)

		for _, r := range results {
			fmt.Fprintf(os.Stderr, "  %s: %d ops/s (%d ns/op, %d iterations in %dms)\n",
				r.Name, r.OpsPerSec, r.NsPerOp, r.Iterations, r.MS)
		}
	}
}

// buildCmd implements `ramune build <file> [--outdir=dist] [--minify] [--bundle]`.
// Wraps esbuild (already a dependency) as a CLI command.
func buildCmd(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	var outdir, outfile, format, platform string
	var minify, bundle, sourcemap bool
	fs.StringVar(&outdir, "outdir", "", "output directory")
	fs.StringVar(&outfile, "outfile", "", "output file (single entry)")
	fs.StringVar(&format, "format", "", "output format: esm, cjs, iife")
	fs.StringVar(&platform, "platform", "browser", "target platform: browser, node")
	fs.BoolVar(&minify, "minify", false, "minify output")
	fs.BoolVar(&bundle, "bundle", false, "bundle dependencies")
	fs.BoolVar(&sourcemap, "sourcemap", false, "generate source maps")
	fs.Parse(args)

	entries := fs.Args()
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ramune build <file...> [--outdir=dist] [--minify] [--bundle]")
		os.Exit(1)
	}

	opts := api.BuildOptions{
		EntryPoints: entries,
		Write:       true,
		LogLevel:    api.LogLevelInfo,
	}

	if outdir != "" {
		opts.Outdir = outdir
	} else if outfile != "" {
		opts.Outfile = outfile
	} else {
		opts.Outdir = "dist"
	}

	if bundle {
		opts.Bundle = true
	}
	if minify {
		opts.MinifySyntax = true
		opts.MinifyWhitespace = true
		opts.MinifyIdentifiers = true
	}
	if sourcemap {
		opts.Sourcemap = api.SourceMapLinked
	}
	switch format {
	case "esm":
		opts.Format = api.FormatESModule
	case "cjs":
		opts.Format = api.FormatCommonJS
	case "iife":
		opts.Format = api.FormatIIFE
	}
	switch platform {
	case "node":
		opts.Platform = api.PlatformNode
	default:
		opts.Platform = api.PlatformBrowser
	}

	result := api.Build(opts)
	if len(result.Errors) > 0 {
		os.Exit(1)
	}
}

// compileCmd implements `ramune compile <file> -o <output> [--minify] [--http]`.
// Bundles JS/TS into a standalone Go binary via go:embed.
func compileCmd(args []string) {
	fs := flag.NewFlagSet("compile", flag.ExitOnError)
	var output string
	var minify, httpMode bool
	fs.StringVar(&output, "o", "", "output binary name")
	fs.BoolVar(&minify, "minify", false, "minify bundled JS")
	fs.BoolVar(&httpMode, "http", false, "run event loop for HTTP server")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: ramune compile <file> [-o output] [--minify] [--http]")
		os.Exit(1)
	}
	filename := fs.Arg(0)

	code, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// TypeScript → JS via esbuild.
	if isTypeScript(filename) {
		code, err = transformTypeScript(filename, code)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// Bundle with esbuild.
	absPath, _ := filepath.Abs(filename)
	external := []string{
		"child_process", "fs", "path", "os", "net", "http", "https", "tls",
		"stream", "events", "util", "buffer", "crypto", "url", "querystring",
		"zlib", "string_decoder", "assert", "readline", "dns", "worker_threads",
		"bun:sqlite", "node:*",
	}
	buildOpts := api.BuildOptions{
		EntryPoints: []string{absPath},
		Bundle:      true,
		Format:      api.FormatCommonJS,
		Platform:    api.PlatformNode,
		Write:       false,
		External:    external,
		LogLevel:    api.LogLevelWarning,
		MainFields:  []string{"module", "main"},
	}
	if minify {
		buildOpts.MinifySyntax = true
		buildOpts.MinifyWhitespace = true
		buildOpts.MinifyIdentifiers = true
	}

	buildResult := api.Build(buildOpts)
	bundledJS := string(code)
	if len(buildResult.Errors) == 0 && len(buildResult.OutputFiles) > 0 {
		bundledJS = string(buildResult.OutputFiles[0].Contents)
	}

	if output == "" {
		output = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}

	// Create temp directory for Go project.
	tmpDir, err := os.MkdirTemp("", "ramune-compile-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Write bundled JS.
	if err := os.WriteFile(filepath.Join(tmpDir, "app.bundle.js"), []byte(bundledJS), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Generate main.go.
	eventLoop := `rt.RunEventLoop()`
	if httpMode {
		eventLoop = `for { if err := rt.RunEventLoopFor(365 * 24 * time.Hour); err != nil { log.Fatal(err) } }`
	}
	timeImport := ""
	if httpMode {
		timeImport = `"time"`
	}

	mainGo := fmt.Sprintf(`package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"strings"
	%s

	"github.com/i2y/ramune"
)

//go:embed app.bundle.js
var appJS string

func main() {
	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	// Connect console.log/error to stdout/stderr.
	rt.RegisterFunc("__go_stdout", func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args { parts[i] = fmt.Sprint(a) }
		fmt.Fprintln(os.Stdout, strings.Join(parts, " "))
		return nil, nil
	})
	rt.RegisterFunc("__go_stderr", func(args []any) (any, error) {
		parts := make([]string, len(args))
		for i, a := range args { parts[i] = fmt.Sprint(a) }
		fmt.Fprintln(os.Stderr, strings.Join(parts, " "))
		return nil, nil
	})
	rt.Exec(`+"`"+`
		console.log = function() { __go_stdout.apply(null, Array.prototype.slice.call(arguments)); };
		console.info = console.log;
		console.error = function() { __go_stderr.apply(null, Array.prototype.slice.call(arguments)); };
		console.warn = console.error;
		console.debug = console.log;
	`+"`"+`)

	if err := rt.Exec(appJS); err != nil {
		log.Fatal(err)
	}
	%s
}
`, timeImport, eventLoop)

	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Find ramune module path for replace directive.
	ramuneModPath := findRamuneModPath()
	goMod := `module ramune-compiled-app

go 1.23
`
	if ramuneModPath != "" {
		goMod += fmt.Sprintf("\nrequire github.com/i2y/ramune v0.0.0\n\nreplace github.com/i2y/ramune => %s\n", ramuneModPath)
	} else {
		goMod += "\nrequire github.com/i2y/ramune v0.1.0\n"
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// go mod tidy + go build.
	tidyCmd := exec.Command("go", "mod", "tidy")
	tidyCmd.Dir = tmpDir
	tidyCmd.Stderr = os.Stderr
	if err := tidyCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "go mod tidy failed: %v\n", err)
		os.Exit(1)
	}

	absOutput, _ := filepath.Abs(output)
	buildCmd := exec.Command("go", "build", "-o", absOutput, ".")
	buildCmd.Dir = tmpDir
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "go build failed: %v\n", err)
		os.Exit(1)
	}

	// Codesign on macOS for JIT.
	if goruntime.GOOS == "darwin" {
		entPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>com.apple.security.cs.allow-jit</key><true/>
<key>com.apple.security.cs.allow-unsigned-executable-memory</key><true/>
</dict></plist>`
		entFile := filepath.Join(tmpDir, "ent.plist")
		os.WriteFile(entFile, []byte(entPlist), 0o644)
		exec.Command("codesign", "--force", "--sign", "-", "--entitlements", entFile, absOutput).Run()
	}

	fmt.Printf("compiled: %s\n", output)
}

// findRamuneModPath finds the local ramune module path for replace directive.
func findRamuneModPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for dir != "/" && dir != "." {
		modFile := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modFile); err == nil {
			if strings.Contains(string(data), "module github.com/i2y/ramune") {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	// Try CWD as well.
	cwd, _ := os.Getwd()
	for cwd != "/" && cwd != "." {
		modFile := filepath.Join(cwd, "go.mod")
		if data, err := os.ReadFile(modFile); err == nil {
			if strings.Contains(string(data), "module github.com/i2y/ramune") {
				return cwd
			}
		}
		cwd = filepath.Dir(cwd)
	}
	return ""
}

// execPkgCmd implements `ramune x <package> [args...]` (like npx).
// It installs the package to a temp dir, finds its bin entry, and runs it.
func execPkgCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ramune x <package> [args...]")
		os.Exit(1)
	}

	pkgSpec := args[0]
	pkgArgs := args[1:]

	// Parse "cowsay@1" into name + version.
	pkgName := pkgSpec
	if i := strings.LastIndex(pkgSpec, "@"); i > 0 {
		pkgName = pkgSpec[:i]
	}
	// Handle scoped packages: @scope/pkg
	if strings.HasPrefix(pkgSpec, "@") {
		if slash := strings.Index(pkgSpec, "/"); slash > 0 {
			rest := pkgSpec[slash+1:]
			if at := strings.LastIndex(rest, "@"); at > 0 {
				pkgName = pkgSpec[:slash+1+at]
			} else {
				pkgName = pkgSpec
			}
		}
	}

	// Create temp work dir.
	workDir, err := os.MkdirTemp("", "ramune-x-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(workDir)

	// Detect package manager.
	pmCmd := "npm"
	if _, err := exec.LookPath("bun"); err == nil {
		pmCmd = "bun"
	}

	// Write minimal package.json.
	os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"name":"ramune-x","private":true}`), 0o644)

	// Install the package.
	installArgs := []string{"install", pkgSpec}
	if pmCmd == "npm" {
		installArgs = append(installArgs, "--no-fund", "--no-audit")
	}
	cmd := exec.Command(pmCmd, installArgs...)
	cmd.Dir = workDir
	cmd.Stderr = os.Stderr
	if out, err := cmd.Output(); err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	// Find the bin entry in the installed package.
	binPath := findPackageBin(workDir, pkgName)
	if binPath == "" {
		fmt.Fprintf(os.Stderr, "error: no bin entry found for %s\n", pkgName)
		os.Exit(1)
	}

	// Read the bin script.
	code, err := os.ReadFile(binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Set up runtime.
	rt, err := createRuntimeWithOpts([]string{pkgSpec})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	// Provide process.argv.
	jsArgs := `["ramune","` + pkgName + `"`
	for _, a := range pkgArgs {
		jsArgs += `,"` + strings.ReplaceAll(strings.ReplaceAll(a, `\`, `\\`), `"`, `\"`) + `"`
	}
	jsArgs += `]`
	rt.Exec(`if(typeof process!=='undefined')process.argv=` + jsArgs)
	rt.Exec(fmt.Sprintf(`globalThis.__filename=%q;globalThis.__dirname=%q;`, binPath, filepath.Dir(binPath)))

	// Execute.
	if err := rt.Exec(string(code)); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	rt.RunEventLoop()
}

// findPackageBin looks for the "bin" field in a package's package.json.
func findPackageBin(workDir, pkgName string) string {
	pkgJSONPath := filepath.Join(workDir, "node_modules", pkgName, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return ""
	}
	var pkg map[string]any
	json.Unmarshal(data, &pkg)

	// "bin" can be a string or an object.
	switch bin := pkg["bin"].(type) {
	case string:
		return filepath.Join(workDir, "node_modules", pkgName, bin)
	case map[string]any:
		// Try the package name first, then any entry.
		baseName := pkgName
		if slash := strings.LastIndex(pkgName, "/"); slash >= 0 {
			baseName = pkgName[slash+1:]
		}
		if path, ok := bin[baseName].(string); ok {
			return filepath.Join(workDir, "node_modules", pkgName, path)
		}
		for _, path := range bin {
			if s, ok := path.(string); ok {
				return filepath.Join(workDir, "node_modules", pkgName, s)
			}
		}
	}

	// Fallback: try "main" field.
	if main, ok := pkg["main"].(string); ok {
		return filepath.Join(workDir, "node_modules", pkgName, main)
	}
	return ""
}
