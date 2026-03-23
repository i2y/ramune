package ramune

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/i2y/ramune/internal/registry"
)

// Dependencies declares npm package dependencies that are automatically
// installed, bundled with esbuild, and evaluated in the JSC context.
// Packages are specified as "name" or "name@version" (e.g., "lodash@4").
// The bundle is cached in ~/.cache/ramune/jsbundles/<hash>/.
// Packages are fetched directly from the npm registry — no npm or bun required.
func Dependencies(pkgs ...string) Option {
	return func(c *config) {
		c.dependencies = pkgs
	}
}

// ensureBundle returns the bundled JS source for the given packages,
// using a cached bundle if available.
func ensureBundle(pkgs []string, nodeCompat bool) (string, error) {
	hash := hashPkgs(pkgs, nodeCompat)
	bundlePath := filepath.Join(jsCacheDir(hash), "bundle.js")

	// Cache hit.
	if data, err := os.ReadFile(bundlePath); err == nil {
		return string(data), nil
	}

	// Cache miss: install from npm registry + bundle with esbuild.
	workDir := filepath.Join(jsCacheDir(hash), "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("ramune: failed to create work dir: %w", err)
	}

	if err := installPackages(workDir, pkgs); err != nil {
		os.RemoveAll(jsCacheDir(hash))
		return "", err
	}

	bundle, err := bundlePackages(workDir, pkgs, nodeCompat)
	if err != nil {
		os.RemoveAll(jsCacheDir(hash))
		return "", err
	}

	if err := os.WriteFile(bundlePath, []byte(bundle), 0o644); err != nil {
		return "", fmt.Errorf("ramune: failed to write bundle cache: %w", err)
	}

	// Remove work dir, keep only bundle.js.
	os.RemoveAll(workDir)

	return bundle, nil
}

// ClearCache removes all cached JS bundles created by Dependencies().
func ClearCache() error {
	return os.RemoveAll(jsCacheBaseDir())
}

// --- internal helpers ---

func jsCacheBaseDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "ramune", "jsbundles")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "ramune", "jsbundles")
}

func jsCacheDir(hash string) string {
	return filepath.Join(jsCacheBaseDir(), hash)
}

func hashPkgs(pkgs []string, nodeCompat bool) string {
	sorted := slices.Clone(pkgs)
	slices.Sort(sorted)
	h := sha256.New()
	if nodeCompat {
		fmt.Fprintf(h, "nodecompat\n")
	}
	for _, p := range sorted {
		fmt.Fprintf(h, "%s\n", p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// sanitizeVarName converts a package name to a valid JS identifier for globalThis.
func sanitizeVarName(pkg string) string {
	name := strings.TrimPrefix(pkg, "@")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

// installPackages resolves and downloads packages from the npm registry.
func installPackages(dir string, pkgs []string) error {
	nodeModulesDir := filepath.Join(dir, "node_modules")

	// Write package.json for esbuild compatibility.
	deps := make(map[string]string, len(pkgs))
	for _, pkg := range pkgs {
		name, version := registry.ParsePackageSpec(pkg)
		deps[name] = version
	}

	var buf strings.Builder
	buf.WriteString(`{"dependencies":{`)
	first := true
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !first {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, "%q:%q", k, deps[k])
		first = false
	}
	buf.WriteString("}}")

	pkgJSON := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgJSON, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("ramune: failed to write package.json: %w", err)
	}

	// Resolve and download from npm registry.
	_, err := registry.ResolveAndInstall(pkgs, nodeModulesDir)
	return err
}

// nodeBuiltins lists Node.js built-in modules that are provided by the
// NodeCompat polyfill layer and should be marked as external during bundling.
var nodeBuiltins = []string{
	"child_process", "fs", "path", "os", "net", "http", "https", "tls",
	"stream", "events", "util", "buffer", "crypto", "url", "querystring",
	"zlib", "string_decoder", "assert", "node:*",
}

func bundlePackages(dir string, pkgs []string, nodeCompat bool) (string, error) {
	var entry strings.Builder
	for _, pkg := range pkgs {
		name, _ := registry.ParsePackageSpec(pkg)
		safeName := sanitizeVarName(name)
		nsName := "_ns_" + safeName
		fmt.Fprintf(&entry, "import * as %s from %q;\n", nsName, name)
		fmt.Fprintf(&entry, "globalThis.%s = %s.default ?? %s;\n", safeName, nsName, nsName)
	}

	entryPath := filepath.Join(dir, "_entry.js")
	if err := os.WriteFile(entryPath, []byte(entry.String()), 0o644); err != nil {
		return "", fmt.Errorf("ramune: failed to write entry point: %w", err)
	}

	platform := api.PlatformBrowser
	var external []string
	if nodeCompat {
		platform = api.PlatformNode
		external = nodeBuiltins
	}

	result := api.Build(api.BuildOptions{
		EntryPoints: []string{entryPath},
		Bundle:      true,
		Format:      api.FormatIIFE,
		Platform:    platform,
		Write:       false,
		NodePaths:   []string{filepath.Join(dir, "node_modules")},
		LogLevel:    api.LogLevelSilent,
		External:    external,
	})

	if len(result.Errors) > 0 {
		return "", fmt.Errorf("ramune: esbuild: %s", result.Errors[0].Text)
	}
	if len(result.OutputFiles) == 0 {
		return "", fmt.Errorf("ramune: esbuild produced no output")
	}

	return string(result.OutputFiles[0].Contents), nil
}
