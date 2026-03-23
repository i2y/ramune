package ramune_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ramune"
)

func TestEvalWithContext(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ctx := context.Background()
	v, err := r.EvalWithContext(ctx, "1 + 2")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 3.0 {
		t.Fatalf("got %f, want 3.0", f)
	}
}

func TestEvalWithContextTimeout(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// Create an already-cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.EvalWithContext(ctx, "1 + 2")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestEvalWithContextDeadlineExceeded(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// Create a context with a deadline that has already passed.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, err := r.EvalWithContext(ctx, "1 + 2")
	if err == nil {
		t.Fatal("expected error from expired deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestEvalWithContextClosedRuntime(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	ctx := context.Background()
	_, err := r.EvalWithContext(ctx, "1")
	if !errors.Is(err, ramune.ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got: %v", err)
	}
}

func TestEvalAsyncWithContext(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	v, err := r.EvalAsyncWithContext(ctx, `
		new Promise(function(resolve) {
			setTimeout(function() { resolve(42); }, 10);
		})
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 42.0 {
		t.Fatalf("got %f, want 42", f)
	}
}

func TestEvalAsyncWithContextSyncValue(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	v, err := r.EvalAsyncWithContext(ctx, `1 + 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	f, err := v.Float64()
	if err != nil {
		t.Fatal(err)
	}
	if f != 3.0 {
		t.Fatalf("got %f, want 3", f)
	}
}

func TestEvalAsyncWithContextTimeout(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// Use a very short timeout for a long-running async operation.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := r.EvalAsyncWithContext(ctx, `
		new Promise(function(resolve) {
			setTimeout(function() { resolve(42); }, 5000);
		})
	`)
	if err == nil {
		t.Fatal("expected error from context timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestEvalAsyncWithContextCancelled(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately.
	cancel()

	_, err := r.EvalAsyncWithContext(ctx, `
		new Promise(function(resolve) {
			setTimeout(function() { resolve(42); }, 5000);
		})
	`)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestEvalAsyncWithContextReject(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := r.EvalAsyncWithContext(ctx, `
		new Promise(function(resolve, reject) {
			setTimeout(function() { reject(new Error('boom')); }, 10);
		})
	`)
	if err == nil {
		t.Fatal("expected error from rejected promise")
	}
	var jsErr *ramune.JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("expected *JSError, got %T: %v", err, err)
	}
	if jsErr.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestEvalAsyncWithContextClosedRuntime(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	ctx := context.Background()
	_, err := r.EvalAsyncWithContext(ctx, "1")
	if !errors.Is(err, ramune.ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got: %v", err)
	}
}

func TestRunEventLoopWithContext(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`
		globalThis.fired = false;
		setTimeout(function() { globalThis.fired = true; }, 10);
	`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.RunEventLoopWithContext(ctx); err != nil {
		t.Fatal(err)
	}

	v, err := r.Eval("globalThis.fired")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	b, _ := v.Bool()
	if !b {
		t.Fatal("callback should have fired after RunEventLoopWithContext")
	}
}

func TestRunEventLoopWithContextTimeout(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	// setInterval without clearInterval should be cancelled by context.
	if err := r.Exec(`setInterval(function() {}, 10)`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := r.RunEventLoopWithContext(ctx)
	if err == nil {
		t.Fatal("expected error from context timeout")
	}
	// May return context.DeadlineExceeded or internal timeout depending on timing.
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestRunEventLoopWithContextCancelled(t *testing.T) {
	r := newOrSkip(t)
	defer r.Close()

	if err := r.Exec(`setInterval(function() {}, 10)`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := r.RunEventLoopWithContext(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestRunEventLoopWithContextClosedRuntime(t *testing.T) {
	r := newOrSkip(t)
	r.Close()

	ctx := context.Background()
	err := r.RunEventLoopWithContext(ctx)
	if !errors.Is(err, ramune.ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got: %v", err)
	}
}
