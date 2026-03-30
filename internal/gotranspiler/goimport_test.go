package gotranspiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempTS(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGoImportStdlib(t *testing.T) {
	path := writeTempTS(t, `
import { Println } from "go:fmt"
Println("hello")
`)
	result, err := TranspileFile(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) > 0 {
		t.Logf("warnings: %v", result.Errors)
	}
	t.Logf("output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, `"fmt"`) {
		t.Errorf("expected fmt import")
	}
	if !strings.Contains(result.GoSource, "fmt.Println") {
		t.Errorf("expected fmt.Println call")
	}
}

func TestGoImportStdlibSubpackage(t *testing.T) {
	path := writeTempTS(t, `
import { NewReader } from "go:bufio"
const r = NewReader(null)
`)
	result, err := TranspileFile(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, `"bufio"`) {
		t.Errorf("expected bufio import")
	}
	if !strings.Contains(result.GoSource, "bufio.NewReader") {
		t.Errorf("expected bufio.NewReader call")
	}
}

func TestGoImportNamespace(t *testing.T) {
	path := writeTempTS(t, `
import * as http from "go:net/http"
http.ListenAndServe(":8080", null)
`)
	result, err := TranspileFile(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, `"net/http"`) {
		t.Errorf("expected net/http import")
	}
	if !strings.Contains(result.GoSource, "http.ListenAndServe") {
		t.Errorf("expected http.ListenAndServe call")
	}
}

func TestGoImportMultiReturn(t *testing.T) {
	path := writeTempTS(t, `
import * as http from "go:net/http"
const [resp, err] = http.Get("http://example.com")
`)
	result, err := TranspileFile(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "resp, err :=") {
		t.Errorf("expected multi-return destructuring")
	}
	if strings.Contains(result.GoSource, "__arr") {
		t.Errorf("should not use tmp array for go: import calls")
	}
}

func TestGoImportThirdParty(t *testing.T) {
	path := writeTempTS(t, `
import { Default } from "go:github.com/gin-gonic/gin"
const r = Default()
`)
	result, err := TranspileFile(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, `"github.com/gin-gonic/gin"`) {
		t.Errorf("expected gin import")
	}
	if !strings.Contains(result.GoSource, "gin.Default") {
		t.Errorf("expected gin.Default call")
	}
}

func TestGoImportMultipleFromSamePackage(t *testing.T) {
	path := writeTempTS(t, `
import { Println, Sprintf } from "go:fmt"
Println(Sprintf("Hello %s", "world"))
`)
	result, err := TranspileFile(path, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("output:\n%s", result.GoSource)
	if !strings.Contains(result.GoSource, "fmt.Println") {
		t.Errorf("expected fmt.Println")
	}
	if !strings.Contains(result.GoSource, "fmt.Sprintf") {
		t.Errorf("expected fmt.Sprintf")
	}
}

func TestIsThirdPartyGoImport(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"fmt", false},
		{"net/http", false},
		{"encoding/json", false},
		{"github.com/gin-gonic/gin", true},
		{"golang.org/x/text/language", true},
		{"modernc.org/sqlite", true},
	}
	for _, tt := range tests {
		got := isThirdPartyGoImport(tt.path)
		if got != tt.want {
			t.Errorf("isThirdPartyGoImport(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestGoModulePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/gin-gonic/gin", "github.com/gin-gonic/gin"},
		{"github.com/gin-gonic/gin/middleware", "github.com/gin-gonic/gin"},
		{"golang.org/x/text/language", "golang.org/x/text"},
		{"modernc.org/sqlite", "modernc.org/sqlite"},
	}
	for _, tt := range tests {
		got := goModulePath(tt.input)
		if got != tt.want {
			t.Errorf("goModulePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
