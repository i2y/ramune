package ramune

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgotranspile"
)

var esmImportRe = regexp.MustCompile(`(?m)^\s*(import\s+|export\s+(default\s+|const\s+|function\s+|class\s+|let\s+|var\s+|\{))`)

func isESMSource(filename, source string) bool {
	if strings.HasSuffix(filename, ".mjs") {
		return true
	}
	if strings.HasSuffix(filename, ".cjs") {
		return false
	}
	// Walk parent directories for package.json "type": "module".
	dir := filepath.Dir(filename)
	for {
		pkgJSON := filepath.Join(dir, "package.json")
		if data, err := os.ReadFile(pkgJSON); err == nil {
			var pkg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Type == "module" {
				return true
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return esmImportRe.MatchString(source)
}

// transformSource runs tsgo's TS->JS emit on source. When commonJS is
// true, ESM syntax is converted to CJS (module.exports / require); when
// false, module shape is preserved. Dynamic import() is always rewritten
// to Ramune's __dynamicImport polyfill in the CJS path.
func transformSource(filename, source string, commonJS bool) (string, error) {
	module := core.ModuleKindPreserve
	feedName := filepath.Base(filename)
	if commonJS {
		module = core.ModuleKindCommonJS
		// .mjs / .cjs force their module kind regardless of CompilerOptions.
		// Rename the feed so tsgo treats it as ambiguous .js and honours
		// Module=CommonJS. The source content is plain JS in either case.
		switch strings.ToLower(filepath.Ext(feedName)) {
		case ".mjs", ".cjs":
			feedName = strings.TrimSuffix(feedName, filepath.Ext(feedName)) + ".js"
		}
	}
	r, err := tsgotranspile.Transpile(source, tsgotranspile.Options{
		FileName: feedName,
		Target:   tsgoTarget(),
		Module:   module,
	})
	if err != nil {
		return "", fmt.Errorf("transform %s: %w", filepath.Base(filename), err)
	}
	if e := tsgotranspile.FirstError(r.Diagnostics); e != nil {
		return "", fmt.Errorf("transform %s: %w", filepath.Base(filename), e)
	}
	out := r.JS
	if commonJS {
		out = strings.ReplaceAll(out, "import(", "__dynamicImport(")
	}
	return out, nil
}

func resolveFileModule(resolved string) (string, error) {
	// Try exact path first to avoid building the candidate list.
	if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
		return filepath.Abs(resolved)
	}
	for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".tsx", ".json"} {
		c := resolved + ext
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return filepath.Abs(c)
		}
	}
	for _, idx := range []string{"index.js", "index.mjs", "index.cjs", "index.ts"} {
		c := filepath.Join(resolved, idx)
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return filepath.Abs(c)
		}
	}
	return "", fmt.Errorf("Cannot find module '%s'", resolved)
}

func resolveNodeModule(mod, baseDir string) (string, error) {
	pkgName, subpath := splitModulePath(mod)
	dir := baseDir
	for {
		nmDir := filepath.Join(dir, "node_modules", pkgName)
		if info, err := os.Stat(nmDir); err == nil && info.IsDir() {
			if entry, err := resolvePackageEntry(nmDir, subpath); err == nil {
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
	if err := json.Unmarshal(data, &pkg); err != nil {
		return resolveFileModule(filepath.Join(pkgDir, subpath))
	}

	if pkg.Exports != nil {
		if resolved := resolveExports(pkg.Exports, subpath); resolved != "" {
			full := filepath.Join(pkgDir, resolved)
			if _, err := os.Stat(full); err == nil {
				return filepath.Abs(full)
			}
		}
	}

	if subpath == "." && pkg.Main != "" {
		full := filepath.Join(pkgDir, pkg.Main)
		if _, err := os.Stat(full); err == nil {
			return filepath.Abs(full)
		}
		if entry, err := resolveFileModule(full); err == nil {
			return entry, nil
		}
	}

	return resolveFileModule(filepath.Join(pkgDir, subpath))
}

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

// goResolveModuleFunc returns a GoFunc that resolves a module specifier to an absolute path.
func (r *Runtime) goResolveModuleFunc() GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("require: module and basedir required")
		}
		mod, _ := args[0].(string)
		fromDir, _ := args[1].(string)

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
		if err := r.perms.CheckRead(absPath); err != nil {
			return nil, err
		}
		return absPath, nil
	}
}

// goLoadModuleFunc returns a GoFunc that reads a file and applies TS/ESM transforms.
func (r *Runtime) goLoadModuleFunc() GoFunc {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("load: path required")
		}
		absPath, _ := args[0].(string)

		if err := r.perms.CheckRead(absPath); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, err
		}
		source := string(data)

		ext := filepath.Ext(absPath)
		if ext == ".json" {
			return source, nil
		}

		isTS := ext == ".ts" || ext == ".tsx"
		isESM := isESMSource(absPath, source)

		// One tsgo pass covers both TS type-stripping and ESM->CJS conversion.
		if isTS || isESM {
			source, err = transformSource(absPath, source, isESM)
			if err != nil {
				return nil, err
			}
		}

		return source, nil
	}
}
