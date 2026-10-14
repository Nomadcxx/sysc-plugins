// Package hostcall bounds host calls made from a plugin's event loop.
//
// A plugin's client reader is the only place call replies are decoded, and it
// can stall behind a full incoming queue while the event loop waits in Call.
// An unbounded call from the loop may therefore wait forever. Calls outside
// the loop are fine; calls a loop makes should go through Call.
package hostcall

import (
	"context"
	"sync/atomic"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// defaultTimeout bounds one host call made from the event loop.
var defaultTimeout atomic.Int64

func init() { defaultTimeout.Store(int64(5 * time.Second)) }

// Timeout returns the current bound for one host call.
func Timeout() time.Duration {
	return time.Duration(defaultTimeout.Load())
}

// SetTimeout overrides the host call bound and returns a restore function.
// Restore it only after every plugin goroutine that may still be in a call has
// exited; a plugin goroutine that nobody joins only becomes safe because the
// bound is atomic.
func SetTimeout(d time.Duration) (restore func()) {
	prev := defaultTimeout.Swap(int64(d))
	return func() { defaultTimeout.Store(prev) }
}

// Call invokes c.Call with a deadline unless the caller already set an earlier
// one. A reply that misses the deadline is dropped, as the SDK does for any
// late reply; the caller sees the context error.
func Call(ctx context.Context, c *v1.Client, kind v1.CallKind, params any) (v1.HostReply, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout())
	defer cancel()
	return c.Call(ctx, kind, params)
}

// Async makes a best-effort host call without waiting on its reply. The SDK
// encoder serializes this write with snapshots sent by the event loop.
func Async(ctx context.Context, c *v1.Client, kind v1.CallKind, params any) {
	go func() { _, _ = Call(ctx, c, kind, params) }()
}
