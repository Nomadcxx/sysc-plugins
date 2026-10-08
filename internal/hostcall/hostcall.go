// Package hostcall bounds host calls made from a plugin's event loop.
//
// A plugin's client reader is the only place call replies are decoded, and it
// can stall behind a full incoming queue while the event loop waits in Call.
// An unbounded call from the loop may therefore wait forever. Calls outside
// the loop are fine; calls a loop makes should go through Call.
package hostcall

import (
	"context"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Default bounds one host call made from the event loop. It is a variable so
// tests can shorten it.
var Default = 5 * time.Second

// Call invokes c.Call with a deadline unless the caller already set an earlier
// one. A reply that misses the deadline is dropped, as the SDK does for any
// late reply; the caller sees the context error.
func Call(ctx context.Context, c *v1.Client, kind v1.CallKind, params any) (v1.HostReply, error) {
	ctx, cancel := context.WithTimeout(ctx, Default)
	defer cancel()
	return c.Call(ctx, kind, params)
}
