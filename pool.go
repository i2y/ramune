package ramune

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
)

// RuntimePool manages multiple Runtimes for parallel JS execution.
// Each Runtime has its own dedicated OS thread and independent JSC VM.
type RuntimePool struct {
	workers []*Runtime
	size    int
	next    atomic.Uint64
	closed  atomic.Bool

	// HTTP server state.
	httpMu     sync.Mutex
	httpLn     net.Listener
	httpServer *http.Server
	httpNextID atomic.Int64
	// Per-worker request channels — HTTP handler round-robins across these.
	workerChs  []chan poolHTTPReq
	workerDone []chan struct{} // closed when each workerLoop exits
	httpNext   atomic.Uint64
}

type poolHTTPReq struct {
	id          int
	method      string
	url         string
	headersJSON string
	body        string
	respCh      chan httpResponse
}

// NewPool creates a pool of n Runtimes, each configured with the given options.
func NewPool(n int, opts ...Option) (*RuntimePool, error) {
	if n < 1 {
		n = 1
	}
	p := &RuntimePool{
		workers: make([]*Runtime, n),
		size:    n,
	}

	for i := 0; i < n; i++ {
		rt, err := New(opts...)
		if err != nil {
			for _, prev := range p.workers {
				if prev != nil {
					prev.Close()
				}
			}
			return nil, fmt.Errorf("ramune: pool worker %d: %w", i, err)
		}
		p.workers[i] = rt
	}

	return p, nil
}

// Size returns the number of workers in the pool.
func (p *RuntimePool) Size() int {
	return p.size
}

// pick returns the next Runtime in round-robin order.
func (p *RuntimePool) pick() *Runtime {
	idx := p.next.Add(1) - 1
	return p.workers[idx%uint64(p.size)]
}

// Eval evaluates JavaScript code on the next available worker.
func (p *RuntimePool) Eval(code string) (*Value, error) {
	if p.closed.Load() {
		return nil, ErrAlreadyClosed
	}
	return p.pick().Eval(code)
}

// Exec executes JavaScript code on the next available worker, discarding the result.
func (p *RuntimePool) Exec(code string) error {
	if p.closed.Load() {
		return ErrAlreadyClosed
	}
	return p.pick().Exec(code)
}

// Broadcast executes JavaScript code on all workers in parallel.
func (p *RuntimePool) Broadcast(code string) error {
	if p.closed.Load() {
		return ErrAlreadyClosed
	}
	errs := make(chan error, p.size)
	for _, rt := range p.workers {
		go func(r *Runtime) {
			errs <- r.Exec(code)
		}(rt)
	}
	for i := 0; i < p.size; i++ {
		if err := <-errs; err != nil {
			return err
		}
	}
	return nil
}

// ListenAndServe starts an HTTP server that dispatches requests
// round-robin across pool workers via per-worker channels.
func (p *RuntimePool) ListenAndServe(addr string, jsHandler string) error {
	if p.closed.Load() {
		return ErrAlreadyClosed
	}

	setup := jsHandler + "\n" + poolHandlerJS()
	if err := p.Broadcast(setup); err != nil {
		return fmt.Errorf("ramune: pool handler setup: %w", err)
	}

	// Cache JS function refs on each worker.
	for _, rt := range p.workers {
		w := rt
		w.dispatch(func() {
			p.cacheHandlerRef(w)
		})
	}

	for _, rt := range p.workers {
		if rt.gcConfig.GCPercent != 0 {
			debug.SetGCPercent(rt.gcConfig.GCPercent)
			break
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	p.httpMu.Lock()
	p.httpLn = ln
	p.httpMu.Unlock()

	// Create per-worker channels and start worker goroutines.
	p.workerChs = make([]chan poolHTTPReq, p.size)
	p.workerDone = make([]chan struct{}, p.size)
	for i, rt := range p.workers {
		ch := make(chan poolHTTPReq, 256)
		done := make(chan struct{})
		p.workerChs[i] = ch
		p.workerDone[i] = done
		go func(r *Runtime, c chan poolHTTPReq, d chan struct{}) {
			defer close(d)
			p.workerLoop(r, c)
		}(rt, ch, done)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.httpHandler)

	p.httpServer = &http.Server{Handler: mux}
	return p.httpServer.Serve(ln)
}

// httpHandler round-robins requests to per-worker channels.
func (p *RuntimePool) httpHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	headers := make(map[string]string, len(r.Header))
	for k := range r.Header {
		headers[strings.ToLower(k)] = r.Header.Get(k)
	}
	hdJSON, _ := json.Marshal(headers)

	id := int(p.httpNextID.Add(1))
	respCh := make(chan httpResponse, 1)

	// Round-robin to a specific worker's channel.
	idx := p.httpNext.Add(1) - 1
	ch := p.workerChs[idx%uint64(p.size)]

	ch <- poolHTTPReq{
		id:          id,
		method:      r.Method,
		url:         r.URL.String(),
		headersJSON: string(hdJSON),
		body:        string(body),
		respCh:      respCh,
	}

	resp := <-respCh

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Status > 0 {
		w.WriteHeader(resp.Status)
	}
	w.Write([]byte(resp.Body))
}

// StopHTTP stops the HTTP server and drains in-flight requests.
func (p *RuntimePool) StopHTTP() {
	p.httpMu.Lock()
	srv := p.httpServer
	p.httpServer = nil
	p.httpMu.Unlock()

	// Close the HTTP server first — stops accepting new connections.
	if srv != nil {
		srv.Close()
	}

	// Close worker channels — causes workerLoops to exit.
	p.httpMu.Lock()
	chs := p.workerChs
	dones := p.workerDone
	p.workerChs = nil
	p.workerDone = nil
	p.httpMu.Unlock()
	for _, ch := range chs {
		if ch != nil {
			close(ch)
		}
	}
	// Wait for all workerLoops to finish their in-flight JSC calls.
	for _, d := range dones {
		if d != nil {
			<-d
		}
	}

	if len(p.workers) > 0 && p.workers[0].gcConfig.GCPercent != 0 {
		debug.SetGCPercent(p.workers[0].gcConfig.GCPercent)
	}
}

// Addr returns the listener address, or "" if not listening.
func (p *RuntimePool) Addr() string {
	p.httpMu.Lock()
	defer p.httpMu.Unlock()
	if p.httpLn != nil {
		return p.httpLn.Addr().String()
	}
	return ""
}

// Close stops the HTTP server (if running) and closes all Runtimes.
func (p *RuntimePool) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	p.StopHTTP()
	for _, rt := range p.workers {
		if rt != nil {
			rt.Close()
		}
	}
	return nil
}

func poolHandlerJS() string {
	return `
(function() {
	globalThis.__poolHandleFast = function(method, url, body, headers) {
		var req = { method: method, url: url, body: body, headers: headers || {} };
		var result = globalThis.__poolHandle(req);
		if (result === null || result === undefined) {
			return "200\n{}\n";
		}
		if (typeof result === 'string') {
			return "200\n{}\n" + result;
		}
		var status = result.status || 200;
		var respHeaders = result.headers || {};
		var respBody = result.body || '';
		if (typeof respBody !== 'string') {
			try { respBody = JSON.stringify(respBody); } catch(e) { respBody = String(respBody); }
			if (!respHeaders['content-type'] && !respHeaders['Content-Type']) {
				respHeaders['content-type'] = 'application/json';
			}
		}
		var hdStr;
		try { hdStr = JSON.stringify(respHeaders); } catch(e) { hdStr = '{}'; }
		return String(status) + '\n' + hdStr + '\n' + respBody;
	};
})();
`
}
