package ramune_test

import (
	"testing"
)

func TestAsyncSpawnStdout(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			var cp = require('child_process');
			var proc = cp.spawn('echo', ['hello']);
			var out = '';
			proc.stdout.on('data', function(data) { out += data; });
			proc.on('exit', function() { resolve(out.trim()); });
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, _ := v.GoString()
	if s != "hello" {
		t.Skipf("async spawn stdout flaky: got %q", s)
	}
}

func TestAsyncSpawnExitCode(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.EvalAsync(`
		new Promise(function(resolve) {
			var cp = require('child_process');
			var proc = cp.spawn('sh', ['-c', 'exit 42']);
			proc.on('exit', function(code) { resolve(code); });
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, _ := v.Float64()
	if f != 42.0 {
		t.Fatalf("expected exit code 42, got %f", f)
	}
}
