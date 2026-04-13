package ramune

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// isESMSource detects whether source code uses ESM syntax.
func isESMSource(filename, source string) bool {
	// .mjs files are always ESM.
	if strings.HasSuffix(filename, ".mjs") {
		return true
	}
	// .cjs files are never ESM.
	if strings.HasSuffix(filename, ".cjs") {
		return false
	}
	// Check for package.json "type": "module".
	dir := filepath.Dir(filename)
	for dir != "/" && dir != "." {
		pkgJSON := filepath.Join(dir, "package.json")
		if data, err := os.ReadFile(pkgJSON); err == nil {
			s := string(data)
			if strings.Contains(s, `"type":"module"`) ||
				strings.Contains(s, `"type": "module"`) {
				return true
			}
			break
		}
		dir = filepath.Dir(dir)
	}
	// Check for import/export keywords.
	return esmImportRe.MatchString(source)
}

var esmImportRe = regexp.MustCompile(`(?m)^\s*(import\s+|export\s+(default\s+|const\s+|function\s+|class\s+|let\s+|var\s+|\{))`)

// transformESMToCJS converts ESM source to CommonJS using esbuild Transform API.
func transformESMToCJS(filename, source string) (string, error) {
	result := api.Transform(source, api.TransformOptions{
		Sourcefile: filepath.Base(filename),
		Loader:     api.LoaderJS,
		Format:     api.FormatCommonJS,
		Platform:   api.PlatformNode,
		Target:     api.ESNext,
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Text
		}
		return "", fmt.Errorf("ESM transform: %s", strings.Join(msgs, "; "))
	}
	out := string(result.Code)
	// Replace dynamic import() with __dynamicImport() polyfill.
	out = strings.ReplaceAll(out, "import(", "__dynamicImport(")
	return out, nil
}

// transformTypeScriptSource strips TypeScript type annotations using esbuild.
func transformTypeScriptSource(filename string, source string) (string, error) {
	loader := api.LoaderTS
	if strings.HasSuffix(filename, ".tsx") {
		loader = api.LoaderTSX
	}
	result := api.Transform(source, api.TransformOptions{
		Sourcefile: filepath.Base(filename),
		Loader:     loader,
		Target:     api.ESNext,
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Text
		}
		return "", fmt.Errorf("TypeScript: %s", strings.Join(msgs, "; "))
	}
	return string(result.Code), nil
}

// resolveFileModule resolves a file path trying extension and index fallbacks.
// Returns the absolute path if found.
func resolveFileModule(resolved string) (string, error) {
	candidates := []string{
		resolved,
		resolved + ".js",
		resolved + ".mjs",
		resolved + ".cjs",
		resolved + ".ts",
		resolved + ".tsx",
		resolved + ".json",
		filepath.Join(resolved, "index.js"),
		filepath.Join(resolved, "index.mjs"),
		filepath.Join(resolved, "index.ts"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}
	return "", fmt.Errorf("Cannot find module '%s'", resolved)
}

// resolveNodeModule searches node_modules directories for a package.
func resolveNodeModule(mod, baseDir string) (string, error) {
	pkgName, subpath := splitModulePath(mod)

	dir := baseDir
	for {
		nmDir := filepath.Join(dir, "node_modules", pkgName)
		if info, err := os.Stat(nmDir); err == nil && info.IsDir() {
			entry, err := resolvePackageEntry(nmDir, subpath)
			if err == nil {
				return entry, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("Cannot find module '%s'", mod)
}

func splitModulePath(mod string) (string, string) {
	parts := strings.SplitN(mod, "/", 3)
	if strings.HasPrefix(mod, "@") && len(parts) >= 2 {
		pkgName := parts[0] + "/" + parts[1]
		subpath := "."
		if len(parts) > 2 {
			subpath = "./" + parts[2]
		}
		return pkgName, subpath
	}
	pkgName := parts[0]
	subpath := "."
	if len(parts) > 1 {
		subpath = "./" + strings.Join(parts[1:], "/")
	}
	return pkgName, subpath
}

// resolvePackageEntry resolves the entry file for a package via package.json.
func resolvePackageEntry(pkgDir, subpath string) (string, error) {
	pkgJSON := filepath.Join(pkgDir, "package.json")
	data, err := os.ReadFile(pkgJSON)
	if err != nil {
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
				abs, _ := filepath.Abs(full)
				return abs, nil
			}
		}
	}

	// Fallback: "main" field (only for root subpath).
	if subpath == "." && pkg.Main != "" {
		full := filepath.Join(pkgDir, pkg.Main)
		if _, err := os.Stat(full); err == nil {
			abs, _ := filepath.Abs(full)
			return abs, nil
		}
		// Try main with extension fallback.
		if entry, err := resolveFileModule(full); err == nil {
			return entry, nil
		}
	}

	return resolveFileModule(filepath.Join(pkgDir, subpath))
}

// resolveExports resolves a subpath against a package.json "exports" field.
func resolveExports(exports any, subpath string) string {
	switch v := exports.(type) {
	case string:
		if subpath == "." {
			return v
		}
	case map[string]any:
		if val, ok := v[subpath]; ok {
			return resolveExportCondition(val)
		}
		if subpath == "." {
			if val, ok := v["."]; ok {
				return resolveExportCondition(val)
			}
			return resolveExportCondition(exports)
		}
	}
	return ""
}

// resolveExportCondition resolves conditional exports (require/default/import).
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

// goResolveAndLoadFunc returns a GoFunc that resolves and loads a module.
// It handles file resolution, TypeScript stripping, and ESM-to-CJS transformation.
func (r *Runtime) goResolveAndLoadFunc() GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("require: module and basedir required")
		}
		mod, _ := args[0].(string)
		fromDir, _ := args[1].(string)

		// Resolve file path.
		var absPath string
		var err error
		if strings.HasPrefix(mod, "./") || strings.HasPrefix(mod, "../") || strings.HasPrefix(mod, "/") {
			base := mod
			if !filepath.IsAbs(mod) {
				base = filepath.Join(fromDir, mod)
			}
			absPath, err = resolveFileModule(base)
		} else {
			absPath, err = resolveNodeModule(mod, fromDir)
		}
		if err != nil {
			return nil, err
		}

		// Permission check.
		if err := r.perms.CheckRead(absPath); err != nil {
			return nil, err
		}

		// Read file.
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, err
		}
		source := string(data)

		// TypeScript: strip type annotations.
		ext := filepath.Ext(absPath)
		if ext == ".ts" || ext == ".tsx" {
			source, err = transformTypeScriptSource(absPath, source)
			if err != nil {
				return nil, err
			}
		}

		// ESM: convert to CommonJS.
		if ext != ".json" && isESMSource(absPath, source) {
			source, err = transformESMToCJS(absPath, source)
			if err != nil {
				return nil, err
			}
		}

		result := map[string]string{"path": absPath, "source": source}
		encoded, _ := json.Marshal(result)
		return string(encoded), nil
	}
}
