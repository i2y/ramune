package gotranspiler

import (
	"fmt"
	"path/filepath"

	"github.com/i2y/ramune/internal/tsgo/ast"
	"github.com/i2y/ramune/internal/tsgo/bundled"
	"github.com/i2y/ramune/internal/tsgo/compiler"
	"github.com/i2y/ramune/internal/tsgo/core"
	"github.com/i2y/ramune/internal/tsgo/tsoptions"
	"github.com/i2y/ramune/internal/tsgo/tspath"
	"github.com/i2y/ramune/internal/tsgo/vfs/osvfs"
)

// BuildProgramForFile parses filename through the bundled tsgo compiler host
// and returns the Program plus the resolved *ast.SourceFile for the file.
//
// This is the single source of truth for "give me a tsgo Program focused on
// one TS file" - it replaces ~30 lines of host/config/program boilerplate
// that was previously duplicated across transpiler entry points, the picker,
// the composer, and their tests.
func BuildProgramForFile(filename string) (*compiler.Program, *ast.SourceFile, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve path: %w", err)
	}
	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCachedFSCompilerHost(filepath.Dir(abs), fs, bundled.LibPath(), nil, nil)
	cfg := tsoptions.NewParsedCommandLine(
		&core.CompilerOptions{NoEmit: core.TSTrue, SkipLibCheck: core.TSTrue, AllowJs: core.TSTrue},
		[]string{abs},
		tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          filepath.Dir(abs),
		},
	)
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         cfg,
		Host:           host,
		SingleThreaded: core.TSTrue,
	})
	for _, f := range program.SourceFiles() {
		if f.FileName() == abs {
			return program, f, nil
		}
	}
	return nil, nil, fmt.Errorf("source file not found in program: %s", abs)
}
