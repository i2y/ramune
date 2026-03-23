package ramune_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/i2y/ramune"
)

func TestFetchJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"message":"hello"}`)
	}))
	defer srv.Close()

	r, err := ramune.New(ramune.WithFetch())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	// Phase 1: start fetch
	if err := r.Exec(fmt.Sprintf(`globalThis.__p = fetch('%s')`, srv.URL)); err != nil {
		t.Fatal(err)
	}
	// Phase 2: chain .then
	if err := r.Exec(`globalThis.__p.then(function(r) { return r.json(); }).then(function(d) { globalThis.__r = d.message; })`); err != nil {
		t.Fatal(err)
	}
	// Phase 3: read result
	v, err := r.Eval(`globalThis.__r`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "hello" {
		t.Fatalf("got %q, want %q", s, "hello")
	}
}

func TestFetchPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "expected POST", 405)
			return
		}
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		fmt.Fprintf(w, "received: %s", body)
	}))
	defer srv.Close()

	r, err := ramune.New(ramune.WithFetch())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	code := fmt.Sprintf(`globalThis.__p = fetch('%s', {method: 'POST', body: 'test data'})`, srv.URL)
	if err := r.Exec(code); err != nil {
		t.Fatal(err)
	}
	if err := r.Exec(`globalThis.__p.then(function(r) { return r.text(); }).then(function(t) { globalThis.__r = t; })`); err != nil {
		t.Fatal(err)
	}
	v, err := r.Eval(`globalThis.__r`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "received: test data" {
		t.Fatalf("got %q, want %q", s, "received: test data")
	}
}

func TestFetchHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the custom header.
		got := r.Header.Get("X-Custom")
		fmt.Fprint(w, got)
	}))
	defer srv.Close()

	r, err := ramune.New(ramune.WithFetch())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	code := fmt.Sprintf(`globalThis.__p = fetch('%s', {headers: {'X-Custom': 'my-value'}})`, srv.URL)
	if err := r.Exec(code); err != nil {
		t.Fatal(err)
	}
	if err := r.Exec(`globalThis.__p.then(function(r) { return r.text(); }).then(function(t) { globalThis.__r = t; })`); err != nil {
		t.Fatal(err)
	}
	v, err := r.Eval(`globalThis.__r`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "my-value" {
		t.Fatalf("got %q, want %q", s, "my-value")
	}
}

func TestFetchResponseHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server", "ramune-test")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	r, err := ramune.New(ramune.WithFetch())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	code := fmt.Sprintf(`globalThis.__p = fetch('%s')`, srv.URL)
	if err := r.Exec(code); err != nil {
		t.Fatal(err)
	}
	if err := r.Exec(`globalThis.__p.then(function(r) { globalThis.__r = r.headers.get('x-server'); })`); err != nil {
		t.Fatal(err)
	}
	v, err := r.Eval(`globalThis.__r`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "ramune-test" {
		t.Fatalf("got %q, want %q", s, "ramune-test")
	}
}

func TestFetchWithNodeCompat(t *testing.T) {
	// fetch should be auto-installed with NodeCompat.
	r, err := ramune.New(ramune.NodeCompat())
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer r.Close()

	v, err := r.Eval(`typeof fetch`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	s, err := v.GoString()
	if err != nil {
		t.Fatal(err)
	}
	if s != "function" {
		t.Fatalf("fetch should be a function with NodeCompat, got %q", s)
	}
}
