package ramune_test

import (
	"sync/atomic"
	"testing"
)

func TestOnReadyFiresOnceAfterEventLoopIdle(t *testing.T) {
	rt := newOrSkip(t)
	defer rt.Close()

	var fired atomic.Int32
	rt.OnReady(func() { fired.Add(1) })

	// Schedule a timer; event loop must tick until it fires, then
	// become idle and trigger OnReady.
	if err := rt.Exec(`globalThis.__awoke = false; setTimeout(function(){ globalThis.__awoke = true; }, 10);`); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := rt.RunEventLoop(); err != nil {
		t.Fatalf("RunEventLoop: %v", err)
	}

	if got := fired.Load(); got != 1 {
		t.Fatalf("OnReady fired %d times; want 1", got)
	}

	v, err := rt.Eval("__awoke")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v != nil {
		defer v.Close()
	}
	b, _ := v.Bool()
	if !b {
		t.Errorf("timer callback did not run before OnReady fire")
	}

	// Second RunEventLoop with no pending work: OnReady must NOT
	// fire again.
	if err := rt.RunEventLoop(); err != nil {
		t.Fatalf("RunEventLoop second: %v", err)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("OnReady refired after fire; count=%d", got)
	}
}

func TestOnReadyReplacedBeforeFire(t *testing.T) {
	rt := newOrSkip(t)
	defer rt.Close()

	var first, second atomic.Int32
	rt.OnReady(func() { first.Add(1) })
	rt.OnReady(func() { second.Add(1) })

	if err := rt.RunEventLoop(); err != nil {
		t.Fatalf("RunEventLoop: %v", err)
	}

	if first.Load() != 0 {
		t.Errorf("first callback fired despite being replaced")
	}
	if second.Load() != 1 {
		t.Errorf("second callback fire count %d; want 1", second.Load())
	}
}
