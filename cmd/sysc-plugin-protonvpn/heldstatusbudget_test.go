//go:build !race

package main

import "time"

// heldStatusBudget is how long a check waits for the plugin to answer while
// a status call is held open. The held call must not block the input loop,
// so the answer has to be quick.
const heldStatusBudget = 50 * time.Millisecond

// heldStatusDeadline is how long that answer may take before the check calls
// the input loop blocked.
const heldStatusDeadline = 50 * time.Millisecond
