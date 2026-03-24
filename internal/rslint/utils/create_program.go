package utils

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/i2y/ramune/internal/rslint/shim/ast"
	"github.com/i2y/ramune/internal/rslint/shim/bundled"
	"github.com/i2y/ramune/internal/rslint/shim/compiler"
	"github.com/i2y/ramune/internal/rslint/shim/core"
	"github.com/i2y/ramune/internal/rslint/shim/scanner"
	"github.com/i2y/ramune/internal/rslint/shim/tsoptions"
	"github.com/i2y/ramune/internal/rslint/shim/tspath"
	"github.com/i2y/ramune/internal/rslint/shim/vfs"
)

// SyntacticError carries structured diagnostics for syntax errors.
// Callers can type-assert to access the raw diagnostics for rich rendering.
type SyntacticError struct {
	Diagnostics []*ast.Diagnostic
	msg         string
}

func (e *SyntacticError) Error() string {
	return e.msg
}

func CreateCompilerHost(cwd string, fs vfs.FS) compiler.CompilerHost {
	defaultLibraryPath := bundled.LibPath()
	return compiler.NewCompilerHost(cwd, fs, defaultLibraryPath, nil, nil)
}

func CreateProgram(singleThreaded bool, fs vfs.FS, cwd string, tsconfigPath string, host compiler.CompilerHost) (*compiler.Program, error) {
	resolvedConfigPath := tspath.ResolvePath(cwd, tsconfigPath)
	if !fs.FileExists(resolvedConfigPath) {
		return nil, fmt.Errorf("couldn't read tsconfig at %v", resolvedConfigPath)
	}

	configParseResult, _ := tsoptions.GetParsedCommandLineOfConfigFile(tsconfigPath, &core.CompilerOptions{}, nil, host, nil)

	return createProgramFromConfig(singleThreaded, configParseResult, host)
}

// CreateProgramFromOptions creates a program from in-memory compiler options and root file names,
// without requiring a tsconfig file on disk.
func CreateProgramFromOptions(singleThreaded bool, compilerOptions *core.CompilerOptions, rootFileNames []string, host compiler.CompilerHost) (*compiler.Program, error) {
	configParseResult := tsoptions.NewParsedCommandLine(compilerOptions, rootFileNames, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: host.FS().UseCaseSensitiveFileNames(),
		CurrentDirectory:          host.GetCurrentDirectory(),
	})

	return createProgramFromConfig(singleThreaded, configParseResult, host)
}

func createProgramFromConfig(singleThreaded bool, config *tsoptions.ParsedCommandLine, host compiler.CompilerHost) (*compiler.Program, error) {
	opts := compiler.ProgramOptions{
		Config:         config,
		SingleThreaded: core.TSTrue,
		Host:           host,
	}
	if !singleThreaded {
		opts.SingleThreaded = core.TSFalse
	}
	program := compiler.NewProgram(opts)
	if program == nil {
		return nil, errors.New("couldn't create program")
	}

	syntacticDiags := program.GetSyntacticDiagnostics(context.Background(), nil)
	if len(syntacticDiags) != 0 {
		var msgs []string
		for _, d := range syntacticDiags {
			if d.File() != nil {
				line, col := scanner.GetECMALineAndUTF16CharacterOfPosition(d.File(), d.Pos())
				msgs = append(msgs, fmt.Sprintf("  %s(%d,%d): error TS%d: %s",
					d.File().FileName(), line+1, col+1, d.Code(), d.String()))
			} else {
				msgs = append(msgs, fmt.Sprintf("  error TS%d: %s", d.Code(), d.String()))
			}
		}
		return nil, &SyntacticError{
			Diagnostics: syntacticDiags,
			msg:         fmt.Sprintf("found %d syntactic error(s):\n%s", len(syntacticDiags), strings.Join(msgs, "\n")),
		}
	}

	program.BindSourceFiles()

	// program.CreateCheckers()

	return program, nil
}
