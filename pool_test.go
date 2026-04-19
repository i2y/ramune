package ramune_test

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

func TestPoolEval(t *testing.T) {
	pool, err := ramune.NewPool(4)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer pool.Close()

	for i := 0; i < 8; i++ {
		v, err := pool.Eval(fmt.Sprintf("1 + %d", i))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := v.Float64()
		v.Close()
		if int(f) != 1+i {
			t.Fatalf("got %f, want %d", f, 1+i)
		}
	}
}

func TestPoolBroadcast(t *testing.T) {
	pool, err := ramune.NewPool(4)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer pool.Close()

	if err := pool.Broadcast("globalThis.poolWorker = true"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		v, err := pool.Eval("globalThis.poolWorker")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := v.Bool()
		v.Close()
		if !b {
			t.Fatalf("worker %d: globalThis.poolWorker not set", i)
		}
	}
}

func TestPoolConcurrentEval(t *testing.T) {
	pool, err := ramune.NewPool(4)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer pool.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v, err := pool.Eval(fmt.Sprintf("%d * 2", n))
			if err != nil {
				errs <- err
				return
			}
			f, _ := v.Float64()
			v.Close()
			if int(f) != n*2 {
				errs <- fmt.Errorf("got %f, want %d", f, n*2)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestPoolHTTPServer(t *testing.T) {
	nWorkers := runtime.NumCPU()
	if nWorkers > 8 {
		nWorkers = 8
	}
	pool, err := ramune.NewPool(nWorkers)
	if err != nil {
		t.Skipf("JSC not available: %v", err)
	}
	defer pool.Close()

	errCh := make(chan error, 1)
	go func() {
		err := pool.ListenAndServe(":0", `
			globalThis.__poolHandle = function(req) {
				return {
					status: 200,
					headers: { "content-type": "application/json" },
					body: JSON.stringify({ method: req.method, url: req.url })
				};
			};
		`)
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for listener to be ready.
	var addr string
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		addr = pool.Addr()
		if addr != "" {
			break
		}
	}
	if addr == "" {
		select {
		case err := <-errCh:
			t.Fatalf("server failed to start: %v", err)
		default:
			t.Fatal("server did not start in time")
		}
	}

	base := "http://" + addr

	// Send concurrent requests.
	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(base + "/hello")
			if err != nil {
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == 200 && len(body) > 0 {
				success.Add(1)
			}
		}()
	}
	wg.Wait()

	if success.Load() < 45 {
		t.Fatalf("only %d/50 requests succeeded", success.Load())
	}
}
