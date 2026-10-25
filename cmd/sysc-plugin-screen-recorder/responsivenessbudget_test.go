//go:build !race

package main

import "time"

// inputResponsivenessBudget is how long the check waits for the plugin to
// answer a view request while an output-context reply is held, without the
// race detector. The plain run is fast enough for the tight budget.
const inputResponsivenessBudget = 50 * time.Millisecond
