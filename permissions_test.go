package ramune_test

import (
	"strings"
	"testing"

	"github.com/i2y/ramune"
)

func TestSandboxDenyRead(t *testing.T) {
	rt, err := ramune.New(
		ramune.NodeCompat(),
		ramune.WithPermissions(ramune.SandboxPermissions()),
	)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	_, err = rt.Eval(`require('fs').readFileSync('/etc/passwd', 'utf8')`)
	if err == nil {
		t.Fatal("expected PermissionDenied")
	}
	if !strings.Contains(err.Error(), "PermissionDenied") {
		t.Fatalf("expected PermissionDenied, got: %v", err)
	}
}

func TestSandboxAllowReadPath(t *testing.T) {
	rt, err := ramune.New(
		ramune.NodeCompat(),
		ramune.WithPermissions(&ramune.Permissions{
			Read:      ramune.PermGranted,
			ReadPaths: []string{"/tmp"},
			Write:     ramune.PermDenied,
			Net:       ramune.PermDenied,
			Env:       ramune.PermDenied,
			Run:       ramune.PermDenied,
		}),
	)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	// /tmp should be allowed.
	rt.Exec(`require('fs').writeFileSync('/tmp/ramune_perm_test.txt', 'test')`)
	// writeFileSync will fail (write denied), but readFileSync for /etc should fail.
	_, err = rt.Eval(`require('fs').readFileSync('/etc/passwd', 'utf8')`)
	if err == nil {
		t.Fatal("expected PermissionDenied for /etc")
	}
	if !strings.Contains(err.Error(), "PermissionDenied") {
		t.Fatalf("got: %v", err)
	}
}

func TestSandboxDenyWrite(t *testing.T) {
	rt, err := ramune.New(
		ramune.NodeCompat(),
		ramune.WithPermissions(&ramune.Permissions{
			Read:  ramune.PermGranted,
			Write: ramune.PermDenied,
			Net:   ramune.PermGranted,
			Env:   ramune.PermGranted,
			Run:   ramune.PermGranted,
		}),
	)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	_, err = rt.Eval(`require('fs').writeFileSync('/tmp/ramune_perm_deny.txt', 'test')`)
	if err == nil {
		t.Fatal("expected PermissionDenied for write")
	}
	if !strings.Contains(err.Error(), "PermissionDenied") {
		t.Fatalf("got: %v", err)
	}
}

func TestSandboxDenyRun(t *testing.T) {
	rt, err := ramune.New(
		ramune.NodeCompat(),
		ramune.WithPermissions(&ramune.Permissions{
			Read:  ramune.PermGranted,
			Write: ramune.PermGranted,
			Net:   ramune.PermGranted,
			Env:   ramune.PermGranted,
			Run:   ramune.PermDenied,
		}),
	)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	_, err = rt.Eval(`require('child_process').execSync('echo hi')`)
	if err == nil {
		t.Fatal("expected PermissionDenied for run")
	}
	if !strings.Contains(err.Error(), "PermissionDenied") {
		t.Fatalf("got: %v", err)
	}
}

func TestAllPermissionsDefault(t *testing.T) {
	// Default permissions should allow everything.
	rt, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer rt.Close()

	v, err := rt.Eval(`
		require('fs').writeFileSync('/tmp/ramune_perm_all.txt', 'ok');
		var content = require('fs').readFileSync('/tmp/ramune_perm_all.txt', 'utf8');
		require('fs').rmSync('/tmp/ramune_perm_all.txt');
		content;
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != "ok" {
		t.Fatalf("got %q", s)
	}
}
