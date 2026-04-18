// A runnable demonstration of a custom env binding.
//
// Run with:
//
//	cd examples/workers/custom-binding
//	go run .
//	curl -X POST http://localhost:3333/notify \
//	    -H "Content-Type: application/json" \
//	    -d '{"user":"alice@example.com","body":"welcome"}'
//
// The worker receives env.EMAIL on top of the standard env.KV /
// env.SECRETS / env.DB because the Go program wires a custom binding
// via RegisterFunc + WithExtraEnvJS — no Ramune changes required.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/i2y/ramune"
	"github.com/i2y/ramune/workers"
)

// Sender is the interface the JS code effectively talks to. In a real
// project this would back onto SMTP, SendGrid, SES, etc.
type Sender interface {
	Send(to, subject, body string) error
}

// recordingSender prints every message and retains the last one so
// the demo HTTP handler can display what was sent.
type recordingSender struct {
	mu   sync.Mutex
	last string
}

func (r *recordingSender) Send(to, subject, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = fmt.Sprintf("to=%s subject=%q body=%q", to, subject, body)
	log.Printf("[EMAIL] %s", r.last)
	return nil
}

// installEmailBinding registers the Go callback that env.EMAIL.send
// will call. The binding takes a single opts object; we unmarshal it
// via JSON so the interface stays simple.
func installEmailBinding(rt *ramune.Runtime, s Sender) error {
	return rt.RegisterFunc("__env_email_send", func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("EMAIL.send: opts required")
		}
		b, _ := json.Marshal(args[0])
		var opts struct {
			To, Subject, Body string
		}
		if err := json.Unmarshal(b, &opts); err != nil {
			return nil, fmt.Errorf("EMAIL.send: invalid opts: %w", err)
		}
		if opts.To == "" {
			return nil, fmt.Errorf("EMAIL.send: 'to' required")
		}
		return nil, s.Send(opts.To, opts.Subject, opts.Body)
	})
}

// emailFacadeJS exposes env.EMAIL on the Workers-style env object.
// The IIFE wraps any pre-existing __extraEnvBindings (from other
// packages or other bindings) so the hook stays composable.
const emailFacadeJS = `
globalThis.__extraEnvBindings = (function(prev) {
    return function(env) {
        if (prev) prev(env);
        env.EMAIL = {
            send: function(opts) { return __env_email_send(opts); },
        };
    };
})(globalThis.__extraEnvBindings);
`

func main() {
	rt, err := ramune.New(ramune.NodeCompat(), ramune.WithFetch())
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	sender := &recordingSender{}
	if err := installEmailBinding(rt, sender); err != nil {
		log.Fatal(err)
	}

	wd, _ := os.Getwd()
	workerPath := filepath.Join(wd, "worker.ts")
	src, err := os.ReadFile(workerPath)
	if err != nil {
		log.Fatalf("reading worker.ts: %v", err)
	}

	handler, err := workers.Register(rt, workerPath, string(src),
		workers.WithExtraEnvJS(emailFacadeJS))
	if err != nil {
		log.Fatal(err)
	}

	addr := ":3333"
	log.Printf("listening on http://localhost%s (custom env.EMAIL binding)", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
