//go:build !race

package main

import "time"

// waitBudget is how long a check waits for the plugin without the race
// detector.
const waitBudget = 5 * time.Second
