package gotranspiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/bundled"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

// TranspileFileIR transpiles a TypeScript file using the IR pipeline.
func TranspileFileIR(filename string, pkgName string) (*TranspileResult, error) {
	return transpileFileIR(filename, pkgName, true)
}

// TranspileLibraryFileIR transpiles a TypeScript file as a library using the IR pipeline.
func TranspileLibraryFileIR(filename string, pkgName string) (*TranspileResult, error) {
	return transpileFileIR(filename, pkgName, false)
}

func transpileFileIR(filename string, pkgName string, isEntry bool) (*TranspileResult, error) {
	code, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return transpileSourceIR(filename, string(code), pkgName, isEntry)
}

// TranspileSourceIR transpiles TypeScript source code using the IR pipeline.
func TranspileSourceIR(filename string, source string, pkgName string) (*TranspileResult, error) {
	return transpileSourceIR(filename, source, pkgName, true)
}

func transpileSourceIR(filename string, source string, pkgName string, isEntry bool) (*TranspileResult, error) {
	if pkgName == "" {
		pkgName = "main"
	}

	absFile, err := filepath.Abs(filename)
	if err != nil {
		absFile = filename
	}

	// Set up the compiler pipeline (same as old path)
	cwd := filepath.Dir(absFile)
	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCachedFSCompilerHost(cwd, fs, bundled.LibPath(), nil, nil)

	compilerOpts := &core.CompilerOptions{
		NoEmit:       core.TSTrue,
		SkipLibCheck: core.TSTrue,
		AllowJs:      core.TSTrue,
	}

	config := tsoptions.NewParsedCommandLine(
		compilerOpts,
		[]string{absFile},
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          cwd,
		},
	)

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})

	ctx := context.Background()
	result := &TranspileResult{}

	var sourceFile *ast.SourceFile
	for _, sf := range program.SourceFiles() {
		if sf.FileName() == absFile {
			sourceFile = sf
			break
		}
	}

	if sourceFile == nil {
		return nil, fmt.Errorf("source file not found in program: %s", absFile)
	}

	ck, done := program.GetTypeCheckerForFile(ctx, sourceFile)
	defer done()

	// Phase 1+2: Build IR
	tm := newTypeMapper(ck)
	builder := NewIRBuilderFromChecker(ck, tm)

	file := builder.BuildSourceFile(sourceFile, pkgName, isEntry)

	// Phase 3: Emit Go source
	emitter := NewIREmitter(builder)
	goSource, err := emitter.EmitFile(file)
	if err != nil {
		result.GoSource = goSource
		result.Errors = append(result.Errors, err.Error())
		return result, nil
	}

	result.GoSource = goSource
	return result, nil
}

// BuildSourceFile converts a TypeScript source file to a GoFile IR.
func (b *IRBuilder) BuildSourceFile(sf *ast.SourceFile, pkgName string, isEntry bool) *GoFile {
	file := &GoFile{Package: pkgName}

	if sf.Statements == nil {
		return file
	}

	// Pass 1: Process imports
	for _, node := range sf.Statements.Nodes {
		if node.Kind == ast.KindImportDeclaration {
			b.processImportDeclaration(node)
		}
	}

	// Link imports to type mapper
	b.tm.importedNames = b.importedNames
	b.tm.pendingImports = b.pendingImports

	// Pass 1.5: Collect class names for forward references
	for _, node := range sf.Statements.Nodes {
		if node.Kind == ast.KindClassDeclaration {
			if n := node.Name(); n != nil {
				b.classNames[goTypeName(nodeText(n))] = true
			}
		}
	}

	// Pass 2: Categorize and build
	var decls []GoDecl
	var stmts []GoStmt

	for _, node := range sf.Statements.Nodes {
		switch node.Kind {
		case ast.KindImportDeclaration:
			// Already processed

		case ast.KindFunctionDeclaration:
			decl := b.buildFuncDecl(node)
			if decl != nil {
				decls = append(decls, decl)
			}

		case ast.KindClassDeclaration:
			decls = append(decls, b.buildClassDecl(node)...)
		case ast.KindInterfaceDeclaration:
			decls = append(decls, b.buildInterfaceDecl(node)...)
		case ast.KindTypeAliasDeclaration:
			decls = append(decls, b.buildTypeAliasDecl(node)...)
		case ast.KindEnumDeclaration:
			decls = append(decls, b.buildEnumDecl(node)...)
		case ast.KindExportDeclaration:
			decls = append(decls, b.buildExportDecl(node)...)
		case ast.KindExportAssignment:
			decls = append(decls, b.buildExportAssignment(node)...)

		case ast.KindVariableStatement:
			if isEntry {
				s := b.buildVariableStmt(node)
				if s != nil {
					stmts = append(stmts, s)
				}
			} else {
				s := b.buildVariableStmt(node)
				if s != nil {
					decls = append(decls, &IRStmtDecl{Stmt: s})
				}
			}

		default:
			s := b.BuildStmt(node)
			if s != nil {
				stmts = append(stmts, s)
			}
		}
	}

	file.Decls = decls

	// Wrap executable statements
	if len(stmts) > 0 {
		funcName := "init"
		if isEntry && pkgName == "main" {
			funcName = "main"
		}
		file.Decls = append(file.Decls, &IRFuncDecl{
			Name:       funcName,
			Body:       stmts,
			IsExported: false,
		})
	}

	return file
}

// processImportDeclaration extracts import bindings from a TypeScript import declaration.
func (b *IRBuilder) processImportDeclaration(node *ast.Node) {
	imp := node.AsImportDeclaration()
	if imp.ModuleSpecifier == nil {
		return
	}
	modulePath := ""
	if imp.ModuleSpecifier.Kind == ast.KindStringLiteral {
		modulePath = imp.ModuleSpecifier.AsStringLiteral().Text
	}
	if modulePath == "" {
		return
	}

	// Determine Go package alias from module path
	goAlias := modulePathToGoAlias(modulePath)
	goImportPath := modulePathToGoImport(modulePath)

	// Relative imports are same-package in Go — no package prefix needed.
	isRelative := len(modulePath) > 0 && modulePath[0] == '.'

	if imp.ImportClause == nil {
		return
	}
	ic := imp.ImportClause.AsImportClause()

	// For relative imports, use empty alias so names resolve without prefix.
	effectiveAlias := goAlias
	if isRelative {
		effectiveAlias = ""
	}

	// Default import: import foo from "bar"
	if ic.Name() != nil {
		name := ic.Name().AsIdentifier().Text
		if b.importedNames == nil {
			b.importedNames = make(map[string]string)
		}
		b.importedNames[name] = effectiveAlias
		if goImportPath != "" {
			b.pendingImports[goAlias] = goImportPath
		}
	}

	// Named imports: import { a, b } from "bar"
	if ic.NamedBindings != nil {
		if ic.NamedBindings.Kind == ast.KindNamedImports {
			ni := ic.NamedBindings.AsNamedImports()
			if ni.Elements != nil {
				for _, elem := range ni.Elements.Nodes {
					is := elem.AsImportSpecifier()
					localName := nodeText(is.Name())
					if b.importedNames == nil {
						b.importedNames = make(map[string]string)
					}
					b.importedNames[localName] = effectiveAlias

					// Track original name for renamed imports
					if is.PropertyName != nil {
						originalName := nodeText(is.PropertyName)
						if originalName != localName {
							if b.importedOriginalNames == nil {
								b.importedOriginalNames = make(map[string]string)
							}
							b.importedOriginalNames[localName] = originalName
						}
					}
				}
			}
			if goImportPath != "" {
				b.pendingImports[goAlias] = goImportPath
			}
		} else if ic.NamedBindings.Kind == ast.KindNamespaceImport {
			// import * as foo from "bar"
			nsImport := ic.NamedBindings.AsNamespaceImport()
			name := nodeText(nsImport.Name())
			if isRelative {
				// Same-package namespace: import * as types from './types'
				// In Go, no prefix needed — track for property access rewriting.
				if b.samePackageNamespaces == nil {
					b.samePackageNamespaces = make(map[string]bool)
				}
				b.samePackageNamespaces[name] = true
			} else {
				if b.packageRefs == nil {
					b.packageRefs = make(map[string]string)
				}
				b.packageRefs[name] = goAlias
				if goImportPath != "" {
					b.pendingImports[goAlias] = goImportPath
				}
			}
		}
	}
}

// buildFuncDecl builds a function declaration.
func (b *IRBuilder) buildFuncDecl(node *ast.Node) GoDecl {
	fd := node.AsFunctionDeclaration()
	name := ""
	if fd.Name() != nil {
		name = nodeText(fd.Name())
	}
	if name == "" {
		return nil
	}

	isExported := isExported(node)
	goName := goVarName(name)
	if isExported {
		goName = goExportedName(name)
	}

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)
	retType := b.resolveReturnType(node)
	params := b.buildParamList(node)

	savedAsync := b.inAsyncBody
	savedRetType := b.currentRetType
	b.inAsyncBody = isAsync
	b.currentRetType = retType

	var body []GoStmt
	if fd.Body != nil {
		body = b.buildStmtList(fd.Body)
	}

	b.inAsyncBody = savedAsync
	b.currentRetType = savedRetType

	retTypeInfo := goTypeInfoFromString(retType)
	if isAsync {
		b.addImport("github.com/i2y/ramune/jsrt/promise", "")
		innerType := retType
		if innerType == "" {
			innerType = "any"
		}
		retTypeInfo = goTypeInfoFromString("*promise.Promise[" + innerType + "]")
	}

	return &IRFuncDecl{
		Name:       goName,
		Params:     params,
		RetType:    retTypeInfo,
		Body:       body,
		IsAsync:    isAsync,
		IsExported: isExported,
	}
}

// modulePathToGoAlias converts a TS module path to a Go package alias.
func modulePathToGoAlias(modulePath string) string {
	// Relative paths: ./utils → utils
	base := filepath.Base(modulePath)
	base = stripExtension(base)
	if base == "" || base == "." {
		return "pkg"
	}
	// Sanitize: replace hyphens with underscores
	alias := ""
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			alias += string(r)
		} else if r == '-' {
			alias += "_"
		}
	}
	if alias == "" {
		return "pkg"
	}
	return alias
}

// modulePathToGoImport converts a TS module path to a Go import path.
func modulePathToGoImport(modulePath string) string {
	if modulePath == "" || modulePath[0] == '.' {
		return ""
	}

	// go: prefix → direct Go import path
	if goPath, ok := strings.CutPrefix(modulePath, "go:"); ok {
		return goPath
	}

	// Strip node: prefix (e.g., "node:fs" → "fs")
	cleanPath := strings.TrimPrefix(modulePath, "node:")

	// Node.js builtin → jsrt adapter
	if goImport, ok := nodeModuleToGoImport[cleanPath]; ok {
		return goImport
	}

	// npm package → compat adapter
	if goImport, ok := npmToGoImport[cleanPath]; ok {
		return goImport
	}

	return ""
}

// stripExtension removes .ts, .js, .tsx extensions.
func stripExtension(name string) string {
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts"} {
		if len(name) > len(ext) && name[len(name)-len(ext):] == ext {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

// isExported is defined in importmapper.go
