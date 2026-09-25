package cat

import (
	"fmt"
	"strconv"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The panel's declared box, mirrored from manifest.json.
const (
	PanelWidth  = 340
	PanelHeight = 440
)

// The sprite keys. Each surface's animator keeps the cat's phase by key, so
// a snapshot that only changes the load or the pace never restarts a pose.
const (
	KeyBarCat   = "cat"
	KeyPanelCat = "hero"
)

// heroSize is the panel cat's square: the whole behaviour set shares one fit,
// so the gallop's reach sets the width and a sitting cat stands a little
// under half the square's height.
const heroSize = 144

// barInset opens the bar pill either side of the cat.
const barInset = 8

// percentWidth reserves the bar reading's width for "100%", so the widget
// does not reflow as load crosses from one digit to three.
const percentWidth = 38

// Frame is everything a view needs to draw the cat at one moment. The poses
// themselves are the host's to step through.
type Frame struct {
	Motion  Motion
	Label   string
	Percent int
	Known   bool
}

// FrameOf reads the cat's current state.
func FrameOf(c *Cat) Frame {
	_, known := c.Load()
	return Frame{Motion: c.Motion(), Label: c.Label(), Percent: c.Percent(), Known: known}
}

func percentText(f Frame) string {
	if !f.Known {
		return "—"
	}
	return strconv.Itoa(f.Percent) + "%"
}

// barSize is the glyph square for a bar view: the user's size, capped by
// the height the host reserved so the row can always lay out.
func barSize(s Settings, height int) int {
	size := s.Size
	if height > 0 && size > height {
		size = height
	}
	return size
}

// sprite is the cat as a host-animated icon: the act's poses and one pass's
// length. Icon is the act's first pose, which reduced motion holds.
func sprite(key string, f Frame, size int, tone v1.Tone) *v1.Node {
	m := f.Motion
	cycle := min(max(int(m.Cycle/time.Millisecond), v1.MinCycleMS), v1.MaxCycleMS)
	return &v1.Node{Kind: v1.KindIcon, Key: key, Icon: m.Frames[0], IconSize: size,
		Frames: m.Frames, CycleMS: cycle, Tone: tone}
}

// BarCat is the bar's cat.
func BarCat(f Frame, s Settings, height int) *v1.Node {
	return sprite(KeyBarCat, f, barSize(s, height), s.tone(f.Percent, f.Known))
}

// BarTree is one control: the cat, and the load beside it when the user
// asked for the number. Clicking it opens the panel.
func BarTree(f Frame, s Settings, height int) *v1.Node {
	// A fixed height keeps the pill the cat's own height and drops the
	// vertical inset, so the padding only opens the sides: the gallop is as
	// wide as its square, and without it nose and tail touch the pill's rim.
	cat := BarCat(f, s, height)
	open := &v1.Node{Kind: v1.KindButton, ID: "open", Name: "Open the cat", Role: "button",
		Height: cat.IconSize, Padding: barInset, Gap: 4, Events: []v1.EventKind{v1.EventActivate},
		Children: []*v1.Node{cat}}
	if s.ShowPercent {
		tone := v1.ToneNormal
		if s.alert(f.Percent, f.Known) {
			tone = v1.ToneError
		}
		open.Children = append(open.Children, &v1.Node{Kind: v1.KindText, Text: percentText(f),
			Tabular: true, Width: percentWidth, Tone: tone})
	}
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{open}}
}

// TooltipTree names what the cat is doing and the load.
func TooltipTree(f Frame) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Cat · " + f.Label, Bold: true},
		{Kind: v1.KindText, Text: "CPU " + percentText(f), Tone: v1.ToneSubtle, Tabular: true},
	}}
}

// PanelCat is the panel's hero cat.
func PanelCat(f Frame, s Settings) *v1.Node {
	n := sprite(KeyPanelCat, f, heroSize, s.tone(f.Percent, f.Known))
	n.CenterX = true
	return n
}

// paceDetail is the hero's caption: how fast the cat is going, or what the
// idle cat is waiting for.
func paceDetail(f Frame, s Settings) string {
	m := f.Motion
	switch {
	case !f.Known:
		return "Measuring CPU…"
	case m.Act == Walk && m.Cycle > 0:
		return fmt.Sprintf("%.1f steps a second", float64(time.Second)/float64(m.Cycle))
	case m.Act == Run && m.Cycle > 0:
		return fmt.Sprintf("%.1f strides a second", float64(time.Second)/float64(m.Cycle))
	case m.Act == Sleep:
		return fmt.Sprintf("Wakes at %d%% CPU", s.Bands.SleepBelow)
	}
	return fmt.Sprintf("Idle below %d%% CPU", s.Bands.SleepBelow)
}

// historySpan is how much time the graph covers at the current sampling.
func historySpan(samples int, every time.Duration) string {
	span := time.Duration(samples) * every
	if span >= time.Minute {
		return fmt.Sprintf("Last %d min", int((span+30*time.Second)/time.Minute))
	}
	return fmt.Sprintf("Last %d s", int(span/time.Second))
}

// PanelTree is the popout: the cat at hero size with what it is doing, the
// live load as a meter, and the recent load as a sparkline.
func PanelTree(f Frame, s Settings, history []float64) *v1.Node {
	tone := s.tone(f.Percent, f.Known)
	valueTone := v1.ToneAccent
	if tone == v1.ToneError {
		valueTone = v1.ToneError
	}

	hero := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 16, Padding: 16, Gap: 6,
		Children: []*v1.Node{
			PanelCat(f, s),
			{Kind: v1.KindText, Text: f.Label, Size: "title", Bold: true, CenterX: true},
			{Kind: v1.KindText, Text: paceDetail(f, s), Size: "caption", Tone: v1.ToneSubtle, CenterX: true},
		}}

	load := 0.0
	if f.Known {
		load = float64(f.Percent) / 100
	}
	meter := &v1.Node{Kind: v1.KindColumn, Gap: 6, Children: []*v1.Node{
		{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "CPU", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: percentText(f), Bold: true, Tabular: true, Tone: valueTone},
		}},
		{Kind: v1.KindProgress, Key: "load", Value: load, Animate: true, Absent: !f.Known},
	}}

	graph := &v1.Node{Kind: v1.KindGraph, Height: 56, Absent: len(history) < v1.MinGraphSamples}
	if !graph.Absent {
		graph.Values = history
	}
	trend := &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		graph,
		{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: historySpan(max(len(history), 1), s.SampleEvery), Size: "caption", Tone: v1.ToneSubtle},
			{Kind: v1.KindText, Text: bandsText(s.Bands), Size: "caption", Tone: v1.ToneSubtle},
		}},
	}}

	return &v1.Node{Kind: v1.KindColumn, Padding: 16, Gap: 14, Children: []*v1.Node{
		{Kind: v1.KindRow, PinEnd: true, Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Cat", Size: "title", Bold: true},
			{Kind: v1.KindButton, ID: "close", Icon: "close", Name: "Close", Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
		}},
		hero,
		meter,
		trend,
	}}
}

// bandsText states the thresholds the cat follows.
func bandsText(b Thresholds) string {
	if b.SleepBelow == 0 {
		return fmt.Sprintf("Top speed %d%%", b.TopAt)
	}
	return fmt.Sprintf("Idle <%d%% · top %d%%", b.SleepBelow, b.TopAt)
}
