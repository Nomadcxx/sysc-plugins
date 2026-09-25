package worldclock

import (
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func barZones() []Reading {
	return []Reading{
		{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "21:04", Relative: "+9h", DayShift: 1, OnBar: true},
		{Zone: "Europe/Berlin", Label: "Berlin", Clock: "14:04", Relative: "+2h", OnBar: true},
		{Zone: "America/New_York", Label: "New York", Clock: "08:04", Relative: "−4h", OnBar: true},
	}
}

func lintBar(t *testing.T, root *v1.Node) {
	t.Helper()
	for _, f := range shelllint.Tree(root, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
		t.Errorf("bar: %s", f)
	}
}

func TestBarModes(t *testing.T) {
	t.Parallel()
	z := barZones()
	cases := []struct {
		mode  BarMode
		cycle int
		want  string
	}{
		{BarPrimary, 0, "Tokyo 21:04 +1"},
		{BarAll, 0, "Tokyo 21:04 +1"}, // a second entry would exceed barTextBytes
		{BarCycle, 1, "Berlin 14:04"},
		{BarCycle, 5, "New York 08:04"},
		{BarIcon, 0, ""},
	}
	for _, c := range cases {
		if got := BarText(c.mode, z, c.cycle); got != c.want {
			t.Fatalf("%s/%d = %q, want %q", c.mode, c.cycle, got, c.want)
		}
		lintBar(t, Bar(c.mode, z, c.cycle))
	}
	short := []Reading{
		{Label: "UTC", Clock: "12:04", OnBar: true},
		{Label: "Berlin", Clock: "14:04", OnBar: true},
		{Label: "Tokyo", Clock: "21:04", OnBar: true},
	}
	if got := BarText(BarAll, short, 0); got != "UTC 12:04 · Berlin 14:04" {
		t.Fatalf("all = %q", got)
	}
	lintBar(t, Bar(BarAll, short, 0))
}

func TestBarFallsBackToGlobe(t *testing.T) {
	t.Parallel()
	for _, mode := range []BarMode{BarIcon, BarPrimary, BarAll, BarCycle} {
		b := BarButton(mode, nil, 0)
		if b.Icon != "public" || b.Text != "" || b.ID != "open" || b.Key != "bar" {
			t.Fatalf("%s with no zones = %+v", mode, b)
		}
		lintBar(t, Bar(mode, nil, 0))
	}
}

func TestBarLongMultibyteLabelFitsAndKeepsTime(t *testing.T) {
	t.Parallel()
	long := []Reading{{Zone: "Asia/Tokyo", Label: strings.Repeat("東", MaxLabelRunes), Clock: "12:04 PM", DayShift: -1, OnBar: true}}
	got := BarText(BarPrimary, long, 0)
	if !strings.HasSuffix(got, " 12:04 PM −1") || !strings.Contains(got, "…") {
		t.Fatalf("bar = %q", got)
	}
	lintBar(t, Bar(BarPrimary, long, 0))
	lintBar(t, Bar(BarAll, append(long, barZones()...), 0))
}

func TestParseBarMode(t *testing.T) {
	t.Parallel()
	if m, ok := ParseBarMode("cycle"); !ok || m != BarCycle {
		t.Fatal("cycle")
	}
	if _, ok := ParseBarMode("sideways"); ok {
		t.Fatal("unknown mode accepted")
	}
}

func TestOnBarFilters(t *testing.T) {
	t.Parallel()
	z := append(barZones(), Reading{Zone: "UTC", OnBar: false})
	if got := OnBar(z); len(got) != 3 {
		t.Fatalf("on bar = %d", len(got))
	}
}

func TestTooltipListsZonesAndCaps(t *testing.T) {
	t.Parallel()
	root := Tooltip(barZones())
	if err := v1.Validate(root, v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if !hasText(root, "Tokyo 21:04 · +9h, tomorrow", v1.ToneNormal) || !hasText(root, "Berlin 14:04 · +2h", v1.ToneNormal) {
		t.Fatal("tooltip lines")
	}
	many := make([]Reading, 11)
	for i := range many {
		many[i] = Reading{Zone: "UTC", Label: "UTC", Clock: "12:00", Relative: "Same time", OnBar: true}
	}
	capped := Tooltip(many)
	if !hasText(capped, "+3 more", v1.ToneSubtle) {
		t.Fatal("tooltip not capped")
	}
	for _, f := range shelllint.Tree(capped, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
		t.Errorf("tooltip: %s", f)
	}
	if !hasText(Tooltip(nil), "No zones on the bar", v1.ToneSubtle) {
		t.Fatal("empty tooltip")
	}
}
