package workers_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/workers"
)

func newTestRuntime(t *testing.T) *ramune.Runtime {
	t.Helper()
	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
	if err != nil {
		t.Fatalf("ramune.New: %v", err)
	}
	t.Cleanup(func() { rt.Close() })
	return rt
}

func TestIsWorkersStyle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"export default { fetch() {} }", true},
		{"// comment\nexport default {fetch(){}}", true},
		{"routerAdd('GET','/',()=>{})", false},
		{"", false},
	}
	for _, tc := range cases {
		got := workers.IsWorkersStyle(tc.in)
		if got != tc.want {
			t.Errorf("IsWorkersStyle(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestTranspileModule(t *testing.T) {
	t.Parallel()
	src := `export default { hello: "world" };`
	out, err := workers.TranspileModule("m.ts", src)
	if err != nil {
		t.Fatalf("TranspileModule: %v", err)
	}
	if !strings.Contains(out, "__workers_export") {
		t.Errorf("expected __workers_export in output:\n%s", out)
	}
	if !strings.Contains(out, "world") {
		t.Errorf("expected original value preserved:\n%s", out)
	}
}

func TestRegisterHelloFetch(t *testing.T) {
	rt := newTestRuntime(t)

	const module = `
export default {
  route: "/api/hello",
  async fetch(request) {
    const url = new URL(request.url);
    const name = url.searchParams.get("name") || "world";
    return Response.json({ message: "Hello, " + name + "!", method: request.method });
  },
};
`
	handler, err := workers.Register(rt, "hello.ts", module)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/hello?name=Alice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"Hello, Alice!"`) {
		t.Errorf("unexpected body: %s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected content-type: %s", ct)
	}
}

func TestRegisterWaitUntilReturnsFast(t *testing.T) {
	rt := newTestRuntime(t)

	const module = `
globalThis.__bgDone = false;
export default {
  route: "/wu",
  async fetch(_req, _env, ctx) {
    ctx.waitUntil(new Promise(resolve => setTimeout(() => {
      globalThis.__bgDone = true;
      resolve();
    }, 600)));
    return new Response("fast", { headers: { "Content-Type": "text/plain" } });
  },
};
`
	handler, err := workers.Register(rt, "wu.ts", module,
		workers.WithWaitUntilTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	start := time.Now()
	resp, err := http.Get(srv.URL + "/wu")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	elapsed := time.Since(start)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "fast" {
		t.Errorf("body=%q want \"fast\"", body)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("response took %s — waitUntil is blocking the handler", elapsed)
	}

	// Give the background promise up to 2s to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, err := rt.Eval("globalThis.__bgDone === true")
		if err == nil && v != nil {
			done, _ := v.Bool()
			v.Close()
			if done {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("background waitUntil promise did not complete within 2s")
}

func TestRegisterStreamingResponse(t *testing.T) {
	rt := newTestRuntime(t)

	const module = `
export default {
  route: "/sse",
  async fetch() {
    const enc = new TextEncoder();
    const stream = new ReadableStream({
      async start(c) {
        for (let i = 0; i < 3; i++) {
          c.enqueue(enc.encode("chunk" + i + "\n"));
          await new Promise(r => setTimeout(r, 40));
        }
        c.close();
      },
    });
    return new Response(stream, { headers: { "Content-Type": "text/plain" } });
  },
};
`
	handler, err := workers.Register(rt, "sse.ts", module)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if want := "chunk0\nchunk1\nchunk2\n"; !bytes.Equal(body, []byte(want)) {
		t.Errorf("streamed body = %q; want %q", body, want)
	}
}

func TestRegisterSecretsEnv(t *testing.T) {
	os.Setenv("RAMUNE_SECRET_HELLO", "world")
	t.Cleanup(func() { os.Unsetenv("RAMUNE_SECRET_HELLO") })

	rt := newTestRuntime(t)

	const module = `
export default {
  route: "/s",
  fetch(_req, env) {
    return Response.json({ hello: env.SECRETS.HELLO, frozen: Object.isFrozen(env.SECRETS) });
  },
};
`
	handler, err := workers.Register(rt, "s.ts", module)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/s")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.Contains(got, `"hello":"world"`) || !strings.Contains(got, `"frozen":true`) {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestWithFetchFuncRoutesThroughFn(t *testing.T) {
	rt := newTestRuntime(t)

	var gotURL string
	fetchFn := func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"X-Routed": []string{"yes"}, "Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("from-go")),
		}, nil
	}

	const module = `
export default {
  async fetch(_req, _env) {
    const r = await fetch("https://upstream.example/hello");
    return Response.json({
      body: await r.text(),
      routed: r.headers.get("x-routed"),
      status: r.status,
    });
  },
};
`
	handler, err := workers.Register(rt, "fetch.ts", module, workers.WithFetchFunc(fetchFn))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	if gotURL != "https://upstream.example/hello" {
		t.Errorf("fetchFn received URL %q; want upstream.example/hello", gotURL)
	}
	for _, want := range []string{`"body":"from-go"`, `"routed":"yes"`, `"status":200`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in body: %s", want, got)
		}
	}
}

func TestAttachPreparedHandlerIsCloser(t *testing.T) {
	rt := newTestRuntime(t)

	const module = `
export default {
  async fetch(_req, env, ctx) {
    ctx.waitUntil(new Promise(r => setTimeout(r, 50)));
    return new Response("ok");
  },
};
`
	handler, err := workers.Register(rt, "close.ts", module,
		workers.WithWaitUntilTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	closer, ok := handler.(io.Closer)
	if !ok {
		t.Fatalf("handler does not implement io.Closer")
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	// Close should wait for the waitUntil timer to settle.
	start := time.Now()
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Errorf("Close returned too fast (%v); expected to wait for the 50ms waitUntil", elapsed)
	}

	// After close, new requests should 503.
	resp2, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET after close: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("after close, got status %d; want %d", resp2.StatusCode, http.StatusServiceUnavailable)
	}

	// Close again should be a no-op.
	if err := closer.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestRegisterMissingExport(t *testing.T) {
	t.Parallel()
	rt := newTestRuntime(t)
	_, err := workers.Register(rt, "nope.ts", "const x = 1;")
	if err == nil {
		t.Fatal("expected error for non-module source")
	}
	if !strings.Contains(err.Error(), "export default") {
		t.Errorf("error does not mention export default: %v", err)
	}
}
