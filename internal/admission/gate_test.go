package admission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedWaitAndCancellation(t *testing.T) {
	g := New(1, 1)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Acquire(ctx) }()
	deadline := time.After(time.Second)
	for len(g.total) != 2 {
		select {
		case <-deadline:
			t.Fatal("waiter did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := g.Acquire(context.Background()); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	g.Release()
	if len(g.total) != 0 || len(g.active) != 0 {
		t.Fatal("capacity leak")
	}
	if err := g.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.Release()
}

func TestBurstExecutesWithinActiveBound(t *testing.T) {
	g := New(2, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var active atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := g.Acquire(ctx); err != nil {
				t.Error(err)
				return
			}
			defer g.Release()
			if n := active.Add(1); n > 2 {
				t.Errorf("active=%d", n)
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
		})
	}
	wg.Wait()
	if len(g.total) != 0 {
		t.Fatal("capacity leak")
	}
}
