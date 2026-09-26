package githubnotifications

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorCoalescesRefreshTriggers(t *testing.T) {
	c := NewCoordinator(context.Background())
	defer c.Close()

	started := make(chan int, 3)
	firstRelease := make(chan struct{})
	var runs atomic.Int32
	run := func(context.Context) {
		n := int(runs.Add(1))
		started <- n
		if n == 1 {
			<-firstRelease
		}
	}
	complete := func() {}
	if !c.RequestRefresh(run, complete) {
		t.Fatal("first refresh was not queued")
	}
	if got := receive(t, started); got != 1 {
		t.Fatalf("first refresh number = %d", got)
	}
	for range 8 {
		c.RequestRefresh(run, complete)
	}
	close(firstRelease)
	if got := receive(t, started); got != 2 {
		t.Fatalf("coalesced refresh number = %d, want 2", got)
	}
	barrier := make(chan struct{})
	if !c.Submit(func(context.Context) {}, func() { close(barrier) }) {
		t.Fatal("barrier was not queued")
	}
	for {
		select {
		case done := <-c.Completed():
			done()
		case <-barrier:
			if got := runs.Load(); got != 2 {
				t.Fatalf("refresh ran %d times, want one initial plus one follow-up", got)
			}
			return
		case <-time.After(2 * time.Second):
			t.Fatal("coordinator did not drain queued work")
		}
	}
}

func TestCoordinatorSerializesOperationsAndCancelsActiveWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewCoordinator(ctx)
	started := make(chan struct{})
	second := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	active, maximum := 0, 0
	enter := func() {
		mu.Lock()
		active++
		if active > maximum {
			maximum = active
		}
		mu.Unlock()
	}
	leave := func() { mu.Lock(); active--; mu.Unlock() }
	if !c.Submit(func(context.Context) {
		enter()
		close(started)
		<-release
		leave()
	}, nil) {
		t.Fatal("first operation not queued")
	}
	if !c.Submit(func(context.Context) {
		enter()
		close(second)
		leave()
	}, nil) {
		t.Fatal("second operation not queued")
	}
	wait(t, started)
	close(release)
	wait(t, second)
	mu.Lock()
	got := maximum
	mu.Unlock()
	if got != 1 {
		t.Fatalf("maximum concurrent operations = %d, want 1", got)
	}

	blocked := make(chan struct{})
	cancelled := make(chan struct{})
	c2 := NewCoordinator(context.Background())
	defer c2.Close()
	c2.Submit(func(ctx context.Context) {
		close(blocked)
		<-ctx.Done()
		close(cancelled)
	}, nil)
	wait(t, blocked)
	c2.Close()
	wait(t, cancelled)
	cancel()
	wait(t, c.Done())
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coordinator")
		var zero T
		return zero
	}
}

func wait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coordinator")
	}
}
