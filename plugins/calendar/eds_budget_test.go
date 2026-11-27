package calendar

import "testing"

func TestICalendarAggregateBudget(t *testing.T) {
	const limit = 16 * 1024 * 1024
	cases := []struct {
		name string
		used uint64
		add  uint64
		want bool
	}{
		{name: "first event", used: 0, add: 65536, want: true},
		{name: "exactly full", used: limit - 1024, add: 1024, want: true},
		{name: "one byte over", used: limit - 1024, add: 1025, want: false},
		{name: "already full", used: limit, add: 1, want: false},
		{name: "invalid used count", used: limit + 1, add: 0, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := icalBudgetAllows(tc.used, tc.add); got != tc.want {
				t.Fatalf("budget allows used=%d add=%d = %t, want %t", tc.used, tc.add, got, tc.want)
			}
		})
	}
}
