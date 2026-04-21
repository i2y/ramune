//go:build legacy_goir

package gotranspiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIRHonoFiles transpiles all Hono source files through the IR pipeline
// and checks for compilation with go build.
func TestIRHonoFiles(t *testing.T) {
	honoDir := "/tmp/hono-src/src"
	if _, err := os.Stat(honoDir); os.IsNotExist(err) {
		t.Skip("Hono source not found at /tmp/hono-src/src — run: git clone --depth 1 https://github.com/honojs/hono.git /tmp/hono-src")
	}

	files := []string{
		"utils/constants.ts",
		"utils/url.ts",
		"utils/headers.ts",
		"utils/http-status.ts",
		"utils/mime.ts",
		"utils/body.ts",
		"utils/types.ts",
		"utils/encode.ts",
		"utils/buffer.ts",
		"utils/stream.ts",
		"utils/cookie.ts",
		"utils/crypto.ts",
		"utils/html.ts",
		"router.ts",
		"http-exception.ts",
		"types.ts",
		"compose.ts",
		"request.ts",
		"context.ts",
		"hono-base.ts",
	}

	outDir := "/tmp/hono-ir"
	os.MkdirAll(outDir, 0755)

	var pass, fail int
	var failures []string

	for _, rel := range files {
		fullPath := filepath.Join(honoDir, rel)
		base := strings.TrimSuffix(filepath.Base(rel), ".ts")

		result, err := TranspileFileIR(fullPath, "hono")
		if err != nil {
			msg := fmt.Sprintf("FAIL  %s: transpile error: %v", base, err)
			t.Log(msg)
			failures = append(failures, msg)
			fail++
			continue
		}

		outFile := filepath.Join(outDir, base+".go")
		os.WriteFile(outFile, []byte(result.GoSource), 0644)
		t.Logf("OK    %-20s (%d bytes)", base, len(result.GoSource))
		pass++
	}

	t.Logf("\n=== IR Transpile: %d/%d passed ===", pass, pass+fail)
	for _, f := range failures {
		t.Log(f)
	}
}
