//go:build race

package main

import "time"

// inputResponsivenessBudget is how long the check waits for the plugin to
// answer a view request while an output-context reply is held, under the race
// detector. The instrumented run is several times slower than the plain one
// and a busy machine adds to it, so the budget is sized for an instrumented
// run. The assertion itself is unchanged: the plugin still has to answer
// while the output-context reply is held.
const inputResponsivenessBudget = 2 * time.Second
