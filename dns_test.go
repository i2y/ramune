package ramune_test

import (
	"strings"
	"testing"
)

func TestDNSLookup(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		(function() {
			var result = JSON.parse(__go_dns_lookup('localhost'));
			return result.address;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s := v.String()
	if s != "127.0.0.1" && s != "::1" {
		t.Fatalf("expected 127.0.0.1 or ::1, got %q", s)
	}
}

func TestDNSResolve(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		(function() {
			var addrs = JSON.parse(__go_dns_resolve('localhost'));
			return JSON.stringify(addrs);
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s := v.String()
	if !strings.Contains(s, "127.0.0.1") && !strings.Contains(s, "::1") {
		t.Fatalf("expected array containing 127.0.0.1 or ::1, got %q", s)
	}
}

func TestDNSPromises(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.EvalAsync(`
		(async function() {
			var dns = require('dns');
			var result = await dns.promises.lookup('localhost');
			return result.address;
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s := v.String()
	if s != "127.0.0.1" && s != "::1" {
		t.Fatalf("expected 127.0.0.1 or ::1, got %q", s)
	}
}
