package gotranspiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PackageAnalysis holds the result of analyzing an npm package.
type PackageAnalysis struct {
	Name         string
	Version      string
	EntryFile    string   // Resolved entry point (.ts file)
	SourceFiles  []string // All .ts source files
	IsPureTS     bool
	Dependencies []string
	GoImportPath string // Resolved Go import path for this package
}

// npmResolver manages recursive npm package resolution with circular dependency detection.
type npmResolver struct {
	projectRoot  string
	goModuleName string
	resolving    map[string]bool             // packages currently being resolved (cycle detection)
	resolved     map[string]*PackageAnalysis // cached results
}

func newNpmResolver(projectRoot, goModuleName string) *npmResolver {
	return &npmResolver{
		projectRoot:  projectRoot,
		goModuleName: goModuleName,
		resolving:    make(map[string]bool),
		resolved:     make(map[string]*PackageAnalysis),
	}
}

// resolve analyzes an npm package and returns its analysis, or nil if not suitable for transpilation.
func (nr *npmResolver) resolve(pkgName string) *PackageAnalysis {
	if cached, ok := nr.resolved[pkgName]; ok {
		return cached
	}
	if nr.resolving[pkgName] {
		return nil // circular dependency
	}

	nr.resolving[pkgName] = true
	defer delete(nr.resolving, pkgName)

	analysis := nr.analyzePackage(pkgName)
	nr.resolved[pkgName] = analysis
	return analysis
}

// analyzePackage inspects a package in node_modules and determines if it can be transpiled.
func (nr *npmResolver) analyzePackage(pkgName string) *PackageAnalysis {
	pkgDir := filepath.Join(nr.projectRoot, "node_modules", pkgName)

	// Read package.json
	pkgJsonPath := filepath.Join(pkgDir, "package.json")
	data, err := os.ReadFile(pkgJsonPath)
	if err != nil {
		return nil
	}

	var pkgJson struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Main         string            `json:"main"`
		Types        string            `json:"types"`
		Typings      string            `json:"typings"`
		Dependencies map[string]string `json:"dependencies"`
		Scripts      struct {
			Install     string `json:"install"`
			PostInstall string `json:"postinstall"`
		} `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkgJson); err != nil {
		return nil
	}

	// Check for native build scripts
	if pkgJson.Scripts.Install != "" || pkgJson.Scripts.PostInstall != "" {
		return nil
	}

	// Resolve entry point (prefer .ts files)
	entryFile := resolveEntryPoint(pkgDir, pkgJson.Types, pkgJson.Typings, pkgJson.Main)
	if entryFile == "" {
		return nil
	}

	// Only proceed if entry is TypeScript
	if !isTypeScriptFile(entryFile) {
		return nil
	}

	// Single walk: check for native files AND collect .ts sources
	hasNative, sourceFiles := walkPackage(pkgDir, entryFile)
	if hasNative {
		return nil
	}

	// Resolve Go import path
	goImportPath := nr.goModuleName + "/npm/" + sanitizePkgName(pkgName)

	analysis := &PackageAnalysis{
		Name:         pkgName,
		Version:      pkgJson.Version,
		EntryFile:    entryFile,
		SourceFiles:  sourceFiles,
		IsPureTS:     true,
		GoImportPath: goImportPath,
	}

	// Collect dependencies
	for dep := range pkgJson.Dependencies {
		analysis.Dependencies = append(analysis.Dependencies, dep)
	}

	return analysis
}

// resolveEntryPoint finds the TypeScript entry point of a package.
func resolveEntryPoint(pkgDir, types, typings, main string) string {
	// Priority: types → typings → main (with .ts extension check) → index.ts
	candidates := []string{}

	if types != "" {
		candidates = append(candidates, types)
		// Also check .ts version of .d.ts
		candidates = append(candidates, strings.TrimSuffix(types, ".d.ts")+".ts")
	}
	if typings != "" {
		candidates = append(candidates, typings)
		candidates = append(candidates, strings.TrimSuffix(typings, ".d.ts")+".ts")
	}
	if main != "" {
		candidates = append(candidates, main)
		// Try .ts version of .js
		candidates = append(candidates, strings.TrimSuffix(main, ".js")+".ts")
	}
	candidates = append(candidates, "src/index.ts", "index.ts", "index.mts")

	for _, c := range candidates {
		p := filepath.Join(pkgDir, c)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// nativeExts contains file extensions that indicate native bindings.
var nativeExts = map[string]bool{
	".node": true, ".gyp": true, ".cc": true, ".cpp": true,
	".c": true, ".wasm": true,
}

// walkPackage performs a single directory walk to check for native files
// and collect TypeScript source files simultaneously.
func walkPackage(pkgDir, entryFile string) (hasNative bool, tsFiles []string) {
	filepath.Walk(pkgDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if nativeExts[filepath.Ext(path)] || info.Name() == "binding.gyp" {
			hasNative = true
			return filepath.SkipAll
		}
		if isTypeScriptFile(path) && !strings.HasSuffix(path, ".d.ts") {
			tsFiles = append(tsFiles, path)
		}
		return nil
	})
	// Ensure entry file is first
	if len(tsFiles) > 0 && tsFiles[0] != entryFile {
		for i, f := range tsFiles {
			if f == entryFile {
				tsFiles[0], tsFiles[i] = tsFiles[i], tsFiles[0]
				break
			}
		}
	}
	return
}

func isTypeScriptFile(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts"
}

func sanitizePkgName(name string) string {
	return GoPackageName(strings.TrimPrefix(name, "@"))
}
