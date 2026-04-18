package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/workers"
)

// serveCmd implements `ramune serve <entry.ts>` — the Workers-style
// runtime. It reads ramune.toml, resolves npm packages + permissions,
// spawns one or more Runtime instances, and starts an http.Server that
// dispatches requests into the module's default fetch export.
func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var (
		packages                                             packageList
		port                                                 int
		workerCount                                          int
		sqlitePath                                           string
		noSQLite                                             bool
		configPath                                           string
		noConfig                                             bool
		secretsPrefix                                        string
		waitUntilTimeout                                     time.Duration
		envFile                                              string
		sandbox                                              bool
		allowRead, allowWrite, allowNet, allowEnv, allowRun  string
	)
	fs.Var(&packages, "p", "npm package to include (repeatable; merged with ramune.toml)")
	fs.IntVar(&port, "port", 3000, "listening port")
	fs.IntVar(&workerCount, "workers", 1, "number of parallel JS VMs (round-robined)")
	fs.StringVar(&sqlitePath, "sqlite", "", "SQLite DB for env.DB/env.KV (default: .ramune/data.db)")
	fs.BoolVar(&noSQLite, "no-sqlite", false, "disable env.DB/env.KV entirely")
	fs.StringVar(&configPath, "config", "ramune.toml", "path to ramune.toml (use --no-config to skip)")
	fs.BoolVar(&noConfig, "no-config", false, "skip loading ramune.toml")
	fs.StringVar(&secretsPrefix, "secrets-prefix", "", "env.SECRETS variable prefix (default RAMUNE_SECRET_)")
	fs.DurationVar(&waitUntilTimeout, "wait-until-timeout", 30*time.Second, "ctx.waitUntil promise timeout (0 = wait indefinitely)")
	fs.StringVar(&envFile, "env-file", "", "load environment variables from file (default: .env, .env.local)")
	fs.BoolVar(&sandbox, "sandbox", false, "deny all permissions by default")
	fs.StringVar(&allowRead, "allow-read", "", "allow file read (comma-separated paths, or empty for all)")
	fs.StringVar(&allowWrite, "allow-write", "", "allow file write (comma-separated paths)")
	fs.StringVar(&allowNet, "allow-net", "", "allow network access (comma-separated hosts)")
	fs.StringVar(&allowEnv, "allow-env", "", "allow env access (comma-separated vars)")
	fs.StringVar(&allowRun, "allow-run", "", "allow subprocess execution (comma-separated cmds)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: ramune serve <entry.ts> [options]")
		os.Exit(1)
	}
	entry := fs.Arg(0)

	loadDotEnv(envFile)

	// --- ramune.toml ----------------------------------------------------
	var tomlDerived workers.TOMLDerived
	if !noConfig {
		parsed, err := workers.LoadRamuneTOML(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(1)
		}
		if parsed != nil {
			tomlDerived, err = workers.ApplyRamuneTOML(parsed)
			if err != nil {
				fmt.Fprintf(os.Stderr, "serve: %v\n", err)
				os.Exit(1)
			}
		}
	}
	// Merge dependencies from CLI + TOML, deduplicating by name.
	mergedDeps := dedupStrings(append(append([]string{}, packages...), tomlDerived.Dependencies...))

	// --- workers.Option list --------------------------------------------
	wkOpts := make([]workers.Option, 0, len(tomlDerived.Options)+3)
	wkOpts = append(wkOpts, tomlDerived.Options...)
	wkOpts = append(wkOpts, workers.WithWaitUntilTimeout(waitUntilTimeout))
	if secretsPrefix != "" {
		wkOpts = append(wkOpts, workers.WithSecretsPrefix(secretsPrefix))
	}
	if !noSQLite {
		resolved := sqlitePath
		if resolved == "" {
			if err := os.MkdirAll(".ramune", 0o755); err == nil {
				resolved = filepath.Join(".ramune", "data.db")
			}
		}
		if resolved != "" {
			wkOpts = append(wkOpts, workers.WithSQLite(resolved))
		}
	}

	// --- permissions ----------------------------------------------------
	perms := resolvePermissions(tomlDerived.Permissions, sandbox, allowRead, allowWrite, allowNet, allowEnv, allowRun)

	// --- read source file ----------------------------------------------
	absEntry, err := filepath.Abs(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
	src, err := os.ReadFile(absEntry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: read %s: %v\n", entry, err)
		os.Exit(1)
	}

	if !workers.IsWorkersStyle(string(src)) {
		fmt.Fprintf(os.Stderr, "serve: %s does not export a default handler (expected `export default { fetch, ... }`)\n", entry)
		os.Exit(1)
	}

	// --- spawn runtimes -------------------------------------------------
	if workerCount < 1 {
		workerCount = 1
	}
	handlers, closers, err := spawnServeWorkers(workerCount, absEntry, string(src), mergedDeps, perms, wkOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		for _, c := range closers {
			_ = c()
		}
	}()

	mux := roundRobinHandler(handlers)

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Printf("ramune serve %s on http://localhost%s (%d worker%s)\n",
		filepath.Base(entry), addr, workerCount, plural(workerCount))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// spawnServeWorkers creates N Runtimes, each with its own workers.Register
// binding. Returns the per-worker handlers and a close function list.
func spawnServeWorkers(n int, entryAbs, src string, deps []string, perms *ramune.Permissions, wkOpts []workers.Option) ([]http.Handler, []func() error, error) {
	rtOpts := []ramune.Option{ramune.NodeCompat(), ramune.WithFetch()}
	if len(deps) > 0 {
		rtOpts = append(rtOpts, ramune.Dependencies(deps...))
	}
	if perms != nil {
		rtOpts = append(rtOpts, ramune.WithPermissions(perms))
	}

	handlers := make([]http.Handler, 0, n)
	closers := make([]func() error, 0, n)
	for i := 0; i < n; i++ {
		rt, err := ramune.New(rtOpts...)
		if err != nil {
			for _, c := range closers {
				_ = c()
			}
			return nil, nil, fmt.Errorf("ramune.New worker %d: %w", i, err)
		}
		closers = append(closers, rt.Close)
		h, err := workers.Register(rt, entryAbs, src, wkOpts...)
		if err != nil {
			for _, c := range closers {
				_ = c()
			}
			return nil, nil, fmt.Errorf("workers.Register worker %d: %w", i, err)
		}
		handlers = append(handlers, h)
	}
	return handlers, closers, nil
}

// roundRobinHandler returns an http.Handler that dispatches requests
// across the supplied handlers in round-robin order.
func roundRobinHandler(handlers []http.Handler) http.Handler {
	if len(handlers) == 1 {
		return handlers[0]
	}
	var next atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := next.Add(1) - 1
		handlers[idx%uint64(len(handlers))].ServeHTTP(w, r)
	})
}

// resolvePermissions combines TOML permissions with CLI --sandbox /
// --allow-* overrides. CLI flags trump TOML values when set.
func resolvePermissions(tomlPerms *ramune.Permissions, sandbox bool, allowRead, allowWrite, allowNet, allowEnv, allowRun string) *ramune.Permissions {
	var perms *ramune.Permissions
	switch {
	case sandbox:
		perms = ramune.SandboxPermissions()
	case tomlPerms != nil:
		cp := *tomlPerms
		perms = &cp
	}
	if perms == nil {
		return nil
	}
	applyAllowFlag(&perms.Read, &perms.ReadPaths, allowRead)
	applyAllowFlag(&perms.Write, &perms.WritePaths, allowWrite)
	applyAllowFlag(&perms.Net, &perms.NetHosts, allowNet)
	applyAllowFlag(&perms.Env, &perms.EnvVars, allowEnv)
	applyAllowFlag(&perms.Run, &perms.RunCmds, allowRun)
	return perms
}

func applyAllowFlag(state *ramune.PermissionState, list *[]string, flag string) {
	if flag == "" {
		return
	}
	*state = ramune.PermGranted
	if flag != "true" {
		*list = strings.Split(flag, ",")
	} else {
		*list = nil
	}
}

// dedupStrings preserves order and removes duplicates.
func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
