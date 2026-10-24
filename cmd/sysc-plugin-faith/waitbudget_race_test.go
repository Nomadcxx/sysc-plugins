//go:build race

package main

import "time"

// waitBudget is how long a check waits for the plugin under the race
// detector. The instrumented corpus decode the translation stub triggers
// is several times slower than the plain one, and a busy machine adds to
// it, so the budget is sized for an instrumented run.
const waitBudget = 30 * time.Second
