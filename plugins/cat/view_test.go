package cat

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// frames are the states a view can be asked to draw.
func frames() map[string]Frame {
	motion := func(act Act, cycle time.Duration) Motion {
		return Motion{Act: act, Frames: scripts[act].frames, Cycle: cycle}
	}
	return map[string]Frame{
		"measuring": {Motion: motion(Sit, scripts[Sit].cycle), Label: "Sitting"},
		"asleep":    {Motion: motion(Sleep, scripts[Sleep].cycle), Label: "Asleep", Percent: 3, Known: true},
		"grooming":  {Motion: motion(Groom, scripts[Groom].cycle), Label: "Grooming", Percent: 6, Known: true},
		"strolling": {Motion: motion(Walk, SlowestStep), Label: "Strolling", Percent: 12, Known: true},
		"zooming":   {Motion: motion(Run, FastestStride), Label: "Zooming", Percent: 100, Known: true},
	}
}

func settingsVariants() map[string]Settings {
	plain := DefaultSettings()
	number := DefaultSettings()
	number.ShowPercent = true
	number.Size = maxSize
	awake := DefaultSettings()
	awake.Bands.SleepBelow = 0
	return map[string]Settings{"default": plain, "percent": number, "never-sleeps": awake}
}

func fullHistory() []float64 {
	var h History
	for i := 0; i < HistoryLen+5; i++ {
		h.Push(float64(i%10) / 10)
	}
	return h.Values(nil)
}

// Every view lays out at the size the host opens it at, in every state.
func TestEveryViewLaysOut(t *testing.T) {
	t.Parallel()
	for fname, f := range frames() {
		for sname, s := range settingsVariants() {
			for _, hist := range [][]float64{nil, {0.4}, fullHistory()} {
				check := func(view v1.ViewKind, root *v1.Node, w, h int) {
					t.Helper()
					for _, finding := range shelllint.Tree(root, view, w, h) {
						t.Errorf("%s/%s/%d samples %s: %s", fname, sname, len(hist), view, finding)
					}
				}
				check(v1.ViewBar, BarTree(f, s, shelllint.BarHeight), shelllint.BarWidth, shelllint.BarHeight)
				check(v1.ViewTooltip, TooltipTree(f), shelllint.TooltipWidth, shelllint.TooltipHeight)
				check(v1.ViewPanel, PanelTree(f, s, hist), PanelWidth, PanelHeight)
			}
		}
	}
}

// The cat is a host-animated sprite in both animated views, keyed so the
// host keeps its phase across the snapshots each sample sends.
func TestTheCatIsAKeyedSprite(t *testing.T) {
	t.Parallel()
	f := frames()["strolling"]
	s := DefaultSettings()
	for _, tc := range []struct {
		root *v1.Node
		key  string
	}{
		{BarTree(f, s, 32), KeyBarCat},
		{PanelTree(f, s, nil), KeyPanelCat},
	} {
		cat := findKey(tc.root, tc.key)
		if cat == nil {
			t.Fatalf("no node keyed %q", tc.key)
		}
		if cat.Kind != v1.KindIcon || len(cat.Frames) < 2 || cat.CycleMS != int(SlowestStep/time.Millisecond) {
			t.Fatalf("%s = %+v, want a sprite at the walk's pace", tc.key, cat)
		}
		if cat.Icon != cat.Frames[0] {
			t.Fatalf("%s rests on %s, not its first pose %s", tc.key, cat.Icon, cat.Frames[0])
		}
	}
}

func findKey(n *v1.Node, key string) *v1.Node {
	if n.Key == key {
		return n
	}
	for _, c := range n.Children {
		if got := findKey(c, key); got != nil {
			return got
		}
	}
	return nil
}

// The bar cat never outgrows the strip the host reserved.
func TestBarCatFitsTheReservedHeight(t *testing.T) {
	t.Parallel()
	s := DefaultSettings()
	s.Size = maxSize
	if got := BarCat(frames()["zooming"], s, 26).IconSize; got != 26 {
		t.Fatalf("icon size in a 26 px bar = %d", got)
	}
	if got := BarCat(frames()["zooming"], s, 0).IconSize; got != maxSize {
		t.Fatalf("icon size with no reported height = %d, want the setting", got)
	}
}

// The reading's box is fixed, so the bar does not reflow as the load
// crosses from one digit to three.
func TestBarReadingHasAFixedWidth(t *testing.T) {
	t.Parallel()
	s := DefaultSettings()
	s.ShowPercent = true
	var widths []int
	for _, f := range frames() {
		open := BarTree(f, s, 32).Children[0]
		widths = append(widths, open.Children[1].Width)
	}
	for _, w := range widths {
		if w != percentWidth {
			t.Fatalf("reading widths = %v, want all %d", widths, percentWidth)
		}
	}
}

func TestAlertPaintsTheCatInTheErrorTone(t *testing.T) {
	t.Parallel()
	s := DefaultSettings()
	hot := frames()["zooming"]
	if got := BarCat(hot, s, 32).Tone; got != v1.ToneError {
		t.Fatalf("cat tone at 100%% = %q, want error", got)
	}
	if got := BarCat(frames()["strolling"], s, 32).Tone; got != s.Tone {
		t.Fatalf("cat tone at 18%% = %q, want the setting's %q", got, s.Tone)
	}
	s.AlertAbove = 0
	if got := BarCat(hot, s, 32).Tone; got != s.Tone {
		t.Fatalf("alert off: tone = %q, want %q", got, s.Tone)
	}
	if got := BarCat(frames()["measuring"], DefaultSettings(), 32).Tone; got == v1.ToneError {
		t.Fatal("an unmeasured cat raised the alert")
	}
}

func TestPanelCaptions(t *testing.T) {
	t.Parallel()
	s := DefaultSettings()
	for name, want := range map[string]string{
		"measuring": "Measuring CPU…",
		"asleep":    "Wakes at 10% CPU",
		"grooming":  "Idle below 10% CPU",
		"strolling": "0.8 steps a second",
		"zooming":   "2.5 strides a second",
	} {
		if got := paceDetail(frames()[name], s); got != want {
			t.Fatalf("%s caption = %q, want %q", name, got, want)
		}
	}
	if got := historySpan(HistoryLen, 2*time.Second); got != "Last 2 min" {
		t.Fatalf("span = %q", got)
	}
	if got := historySpan(10, 2*time.Second); got != "Last 20 s" {
		t.Fatalf("span = %q", got)
	}
}

// The code's defaults and bounds must be the manifest's: the host sends only
// values the user stored, so a drift here is a setting that shows one value
// in the settings page and behaves as another.
func TestDefaultsMatchTheManifest(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Protocol struct{ Minor int }
		Panels   []struct{ Width, Height int }
		Settings []struct {
			Key      string
			Default  any
			Min, Max *float64
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Protocol.Minor < v1MinorIconSize {
		t.Fatalf("manifest declares minor %d; icon_size needs %d", m.Protocol.Minor, v1MinorIconSize)
	}
	if len(m.Panels) != 1 || m.Panels[0].Width != PanelWidth || m.Panels[0].Height != PanelHeight {
		t.Fatalf("manifest panel = %+v, want %dx%d", m.Panels, PanelWidth, PanelHeight)
	}
	values := map[string]any{}
	for _, st := range m.Settings {
		values[st.Key] = st.Default
		switch st.Key {
		case "size":
			if *st.Min != minSize || *st.Max != maxSize {
				t.Fatalf("size bounds %v..%v, code has %d..%d", *st.Min, *st.Max, minSize, maxSize)
			}
		case "sample_seconds":
			if *st.Min != minSample || *st.Max != maxSample {
				t.Fatalf("sample bounds %v..%v, code has %d..%d", *st.Min, *st.Max, minSample, maxSample)
			}
		}
	}
	if got, want := ParseSettings(values), DefaultSettings(); got != want {
		t.Fatalf("manifest defaults parse to %+v, code defaults are %+v", got, want)
	}
	if got := ParseSettings(nil); got != DefaultSettings() {
		t.Fatalf("empty settings = %+v", got)
	}
}

// v1MinorIconSize is the protocol minor that carries icon_size, frames and
// cycle_ms.
const v1MinorIconSize = 9

func TestParseSettingsClampsAndMapsTones(t *testing.T) {
	t.Parallel()
	s := ParseSettings(map[string]any{
		"size": 99.0, "tone": "subtle", "sleep_below": 70.0, "top_speed_at": 20.0,
		"alert_above": -3.0, "sample_seconds": 0.0, "show_percent": true,
	})
	if s.Size != maxSize || s.Tone != v1.ToneSubtle || s.AlertAbove != 0 || s.SampleEvery != time.Second || !s.ShowPercent {
		t.Fatalf("parsed = %+v", s)
	}
	if s.Bands.TopAt <= s.Bands.SleepBelow {
		t.Fatalf("bands %+v leave no range to run in", s.Bands)
	}
	if got := ParseSettings(map[string]any{"tone": "normal"}).Tone; got != v1.ToneNormal {
		t.Fatalf("normal tone = %q", got)
	}
}

func TestHistoryKeepsTheNewestInOrder(t *testing.T) {
	t.Parallel()
	var h History
	if got := h.Values(nil); len(got) != 0 {
		t.Fatalf("empty history = %v", got)
	}
	for i := 0; i < HistoryLen+3; i++ {
		h.Push(float64(i))
	}
	got := h.Values(nil)
	if len(got) != HistoryLen || got[0] != 3 || got[len(got)-1] != HistoryLen+2 {
		t.Fatalf("history = %v…%v (%d)", got[0], got[len(got)-1], len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]+1 {
			t.Fatalf("history out of order at %d: %v", i, got[i-5:i+1])
		}
	}
	if len(got) > v1.MaxGraphSamples {
		t.Fatalf("history holds %d, past the wire's %d", len(got), v1.MaxGraphSamples)
	}
}

func TestTooltipNamesGaitAndLoad(t *testing.T) {
	t.Parallel()
	tree := TooltipTree(frames()["zooming"])
	var text []string
	for _, c := range tree.Children {
		text = append(text, c.Text)
	}
	if got := strings.Join(text, " / "); got != "Cat · Zooming / CPU 100%" {
		t.Fatalf("tooltip = %q", got)
	}
}
