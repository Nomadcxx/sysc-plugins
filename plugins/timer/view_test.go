package timer

import (
	"testing"
	"time"
)

func TestBarTreeHasAccessibleControls(t *testing.T) {
	t.Parallel()
	root := BarTree("04:12", false)
	if root.Kind != "row" || len(root.Children) != 3 {
		t.Fatalf("%+v", root)
	}
	start := root.Children[1]
	if start.Name == "" || start.Role == "" || start.ID != "start" {
		t.Fatalf("start = %+v", start)
	}
}

func TestPanelTreeIncludesDurationField(t *testing.T) {
	t.Parallel()
	root := PanelTree("05:00", "5m", false, 0.6)
	if root.Kind != "column" {
		t.Fatal(root.Kind)
	}
	foundDuration, foundProgress := false, false
	for _, c := range root.Children {
		if c.ID == "duration" {
			foundDuration = true
		}
		if c.Kind == "progress" && c.Value == 0.6 {
			foundProgress = true
		}
	}
	if !foundDuration {
		t.Fatal("missing duration field")
	}
	if !foundProgress {
		t.Fatal("missing progress bar")
	}
}

func TestFormatClockSwitchesToHours(t *testing.T) {
	t.Parallel()
	if got := FormatClock(59 * time.Second); got != "00:59" {
		t.Fatalf("59s = %q", got)
	}
	if got := FormatClock(90 * time.Minute); got != "1:30:00" {
		t.Fatalf("90m = %q", got)
	}
	if got := FormatClock(-time.Second); got != "00:00" {
		t.Fatalf("negative = %q", got)
	}
}
