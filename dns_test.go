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

func TestDNSResolve4(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		(function() {
			var addrs = JSON.parse(__go_dns_resolve4('localhost'));
			return JSON.stringify(addrs);
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s := v.String()
	if !strings.Contains(s, "127.0.0.1") {
		t.Fatalf("expected 127.0.0.1 in result, got %q", s)
	}
}

func TestDNSReverse(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		(function() {
			var names = JSON.parse(__go_dns_reverse('127.0.0.1'));
			return typeof names === 'object' && names !== null ? 'ok' : 'fail';
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s := v.String()
	if s != "ok" {
		t.Fatalf("expected ok, got %q", s)
	}
}

func TestDNSResolveRRType(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		(function() {
			var dns = require('dns');
			var called = false;
			dns.resolve('localhost', 'A', function(err, addrs) {
				called = !err && Array.isArray(addrs);
			});
			return String(typeof dns.resolve4 === 'function' &&
				typeof dns.resolve6 === 'function' &&
				typeof dns.resolveMx === 'function' &&
				typeof dns.resolveTxt === 'function' &&
				typeof dns.resolveCname === 'function' &&
				typeof dns.resolveNs === 'function' &&
				typeof dns.resolveSrv === 'function' &&
				typeof dns.reverse === 'function' &&
				typeof dns.promises.resolve4 === 'function' &&
				typeof dns.promises.reverse === 'function');
		})()
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s := v.String()
	if s != "true" {
		t.Fatalf("expected true, got %q", s)
	}
}
