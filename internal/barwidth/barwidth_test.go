package barwidth

import "testing"

func TestCompact(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  bool
	}{{0, false}, {32, true}, {64, true}, {119, true}, {120, false}, {240, false}} {
		if got := Compact(tc.width); got != tc.want {
			t.Errorf("Compact(%d) = %v, want %v", tc.width, got, tc.want)
		}
	}
}
