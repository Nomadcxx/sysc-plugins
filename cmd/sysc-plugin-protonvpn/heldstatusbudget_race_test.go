//go:build race

package main

import "time"

// heldStatusBudget is how long a check waits for the plugin to answer while
// a status call is held open. Without the race detector the answer has to be
// quick, because the point of the check is that the held call does not block
// the input loop. Under the race detector the same work takes several times
// longer, and a busy machine adds to it, so the budget is sized for an
// instrumented run.
const heldStatusBudget = 2 * time.Second

// heldStatusDeadline is how long that answer may take before the check calls
// the input loop blocked. Under the race detector the same work takes several
// times longer, so the deadline is sized for an instrumented run too.
const heldStatusDeadline = 2 * time.Second
