package ramune_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/i2y/ramune"
)

// wptDir is the root of the WPT checkout.
const wptDir = "test/wpt"

// wptResult collects results from a single WPT test assertion.
type wptResult struct {
	Status  int // 0=PASS,1=FAIL,2=TIMEOUT,3=NOTRUN
	Name    string
	Message string
}

// runWPTFile runs a single .any.js WPT test file and returns per-assertion results.
func runWPTFile(t *testing.T, testPath string) []wptResult {
	t.Helper()

	harness, err := os.ReadFile(filepath.Join(wptDir, "resources", "testharness.js"))
	if err != nil {
		t.Fatalf("cannot read testharness.js: %v", err)
	}

	testSrc, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("cannot read test %s: %v", testPath, err)
	}

	// Parse META headers for script dependencies.
	metaScripts := parseMetaScripts(string(testSrc), filepath.Dir(testPath))

	// Load META script sources.
	var metaSrc strings.Builder
	for _, sp := range metaScripts {
		data, err := os.ReadFile(sp)
		if err != nil {
			t.Skipf("cannot read META script %s: %v", sp, err)
		}
		metaSrc.Write(data)
		metaSrc.WriteByte('\n')
	}

	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	// Collect results via Go callback.
	var mu sync.Mutex
	var results []wptResult
	var completionDone bool

	r.RegisterFunc("__wpt_result", func(args []any) (any, error) {
		if len(args) < 3 {
			return nil, nil
		}
		status, _ := args[0].(float64)
		name, _ := args[1].(string)
		message, _ := args[2].(string)
		mu.Lock()
		results = append(results, wptResult{
			Status:  int(status),
			Name:    name,
			Message: message,
		})
		mu.Unlock()
		return nil, nil
	})
	r.RegisterFunc("__wpt_done", func(args []any) (any, error) {
		mu.Lock()
		completionDone = true
		mu.Unlock()
		return nil, nil
	})

	// Shim globals for testharness.js shell mode.
	shim := `
		globalThis.self = globalThis;
		globalThis.GLOBAL = {
			isWindow: function() { return false; },
			isWorker: function() { return false; },
			isShadowRealm: function() { return false; }
		};
		globalThis.location = new URL("http://localhost:0/test");
		if (typeof globalThis.fetch === 'undefined') {
			globalThis.fetch = function() { return Promise.reject(new Error("fetch not available in WPT shell")); };
		}
	`

	// Result collection hooks (must be registered after testharness.js loads).
	hooks := `
		add_result_callback(function(test) {
			__wpt_result(test.status, test.name, test.message || '');
		});
		add_completion_callback(function(tests, status) {
			__wpt_done();
		});
	`

	// Build final script: shim -> testharness.js -> hooks -> META scripts -> test
	var script strings.Builder
	script.WriteString(shim)
	script.WriteByte('\n')
	script.Write(harness)
	script.WriteByte('\n')
	script.WriteString(hooks)
	script.WriteByte('\n')
	script.WriteString(metaSrc.String())
	script.Write(testSrc)

	if err := r.Exec(script.String()); err != nil {
		// Some tests may throw on load — record as test error but don't fatal.
		t.Logf("WPT exec error: %v", err)
	}

	// Run event loop for async/promise tests.
	_ = r.RunEventLoop()

	// If completion callback never fired, call done() manually.
	mu.Lock()
	done := completionDone
	mu.Unlock()
	if !done {
		_ = r.Exec("if (typeof done === 'function') { try { done(); } catch(e) {} }")
		_ = r.RunEventLoop()
	}

	return results
}

var metaScriptRe = regexp.MustCompile(`// META: script=(.+)`)

// parseMetaScripts extracts // META: script= paths from test source.
func parseMetaScripts(src, testDir string) []string {
	matches := metaScriptRe.FindAllStringSubmatch(src, -1)
	var paths []string
	for _, m := range matches {
		p := strings.TrimSpace(m[1])
		if strings.HasPrefix(p, "/") {
			// Absolute WPT path.
			paths = append(paths, filepath.Join(wptDir, p))
		} else {
			// Relative to test file.
			paths = append(paths, filepath.Join(testDir, p))
		}
	}
	return paths
}

// wptCategory defines a set of WPT tests to run.
type wptCategory struct {
	Name  string
	Files []string // .any.js file paths
}

// discoverWPTTests finds .any.js files under a WPT directory.
// Skips .https.any.js (requires TLS context) and idlharness tests (require IDL parser).
func discoverWPTTests(dir string) []string {
	var files []string
	_ = filepath.Walk(filepath.Join(wptDir, dir), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".any.js") {
			return nil
		}
		// Skip HTTPS-only tests and IDL harness tests.
		if strings.Contains(path, ".https.any.js") {
			return nil
		}
		if strings.Contains(filepath.Base(path), "idlharness") {
			return nil
		}
		return nil
	})
	// Need to actually append
	_ = filepath.Walk(filepath.Join(wptDir, dir), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".any.js") {
			return nil
		}
		if strings.Contains(path, ".https.any.js") {
			return nil
		}
		if strings.Contains(filepath.Base(path), "idlharness") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}

func TestWPT(t *testing.T) {
	if _, err := os.Stat(filepath.Join(wptDir, "resources", "testharness.js")); err != nil {
		t.Skip("WPT checkout not found; run: git clone --depth 1 --filter=blob:none --sparse https://github.com/web-platform-tests/wpt.git test/wpt")
	}

	categories := []wptCategory{
		{"encoding", discoverWPTTests("encoding")},
		{"url", discoverWPTTests("url")},
		{"urlpattern", discoverWPTTests("urlpattern")},
		{"dom/abort", discoverWPTTests("dom/abort")},
		{"dom/events", discoverWPTTests("dom/events")},
		{"compression", discoverWPTTests("compression")},
		{"streams", discoverWPTTests("streams")},
		{"webmessaging", discoverWPTTests("webmessaging")},
		{"FileAPI/blob", discoverWPTTests("FileAPI/blob")},
		{"hr-time", discoverWPTTests("hr-time")},
		{"html/webappapis/atob", discoverWPTTests("html/webappapis/atob")},
		{"html/webappapis/timers", discoverWPTTests("html/webappapis/timers")},
		{"html/webappapis/microtask-queuing", discoverWPTTests("html/webappapis/microtask-queuing")},
		{"html/webappapis/structured-clone", discoverWPTTests("html/webappapis/structured-clone")},
	}

	var totalPass, totalFail, totalSkip int

	for _, cat := range categories {
		if len(cat.Files) == 0 {
			continue
		}
		t.Run(cat.Name, func(t *testing.T) {
			var catPass, catFail int
			for _, f := range cat.Files {
				testName := strings.TrimPrefix(f, wptDir+"/")
				t.Run(filepath.Base(f), func(t *testing.T) {
					results := runWPTFile(t, f)
					if len(results) == 0 {
						t.Logf("  [SKIP] %s (no assertions)", testName)
						totalSkip++
						return
					}
					for _, r := range results {
						switch r.Status {
						case 0: // PASS
							catPass++
						case 1: // FAIL
							catFail++
							t.Logf("  FAIL: %s - %s", r.Name, r.Message)
						case 2: // TIMEOUT
							catFail++
							t.Logf("  TIMEOUT: %s", r.Name)
						case 3: // NOTRUN
							totalSkip++
						}
					}
				})
			}
			totalPass += catPass
			totalFail += catFail
			t.Logf("  %s: %d pass, %d fail", cat.Name, catPass, catFail)
		})
	}

	fmt.Fprintf(os.Stderr, "\n=== WPT Summary ===\n")
	fmt.Fprintf(os.Stderr, "PASS: %d  FAIL: %d  SKIP: %d  TOTAL: %d\n", totalPass, totalFail, totalSkip, totalPass+totalFail+totalSkip)
	if totalPass+totalFail > 0 {
		fmt.Fprintf(os.Stderr, "Pass rate: %.1f%%\n", float64(totalPass)/float64(totalPass+totalFail)*100)
	}
}
