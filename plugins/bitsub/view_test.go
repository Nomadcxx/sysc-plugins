package bitsub

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var now = time.Date(2026, 11, 29, 14, 40, 0, 0, time.Local)

func item(id, name string, minor int64, cur subs.Currency, aud int64, c subs.Cycle, target string, kind subs.TargetKind, intent subs.Intent) subs.Item {
	d := subs.MustDate(target)
	return subs.Item{ID: id, Name: name, Price: subs.Money{Minor: minor, Currency: cur}, PriceAUD: aud, Cycle: c, Interval: 1,
		Target: d, TargetKind: kind, Intent: intent, CancelBy: d}
}

func fixture() *subs.Snapshot {
	synced := now.Add(-2 * time.Minute)
	series := make([]subs.MonthTotal, 12)
	for i := range series {
		series[i] = subs.MonthTotal{Month: fmt.Sprintf("m%02d", i), AUDMinor: int64(20000 + 1500*i)}
	}
	return &subs.Snapshot{
		Items: []subs.Item{
			item("speechify", "Speechify Premium", 13900, "USD", 21550, subs.Year, "2026-12-02", subs.TargetTrialEnd, subs.Undecided),
			item("synthetic", "Synthetic", 2000, "USD", 3101, subs.Month, "2026-12-05", subs.TargetRenewal, subs.Undecided),
			item("stan", "Stan", 1700, "AUD", 1700, subs.Month, "2026-12-10", subs.TargetRenewal, subs.Cancel),
			item("netflix", "Netflix", 1899, "AUD", 1899, subs.Month, "2026-12-14", subs.TargetRenewal, subs.Keep),
			item("hetzner", "Hetzner VPS", 549, "EUR", 931, subs.Month, "2026-12-20", subs.TargetRenewal, subs.Keep),
			item("spotify", "Spotify", 1399, "AUD", 1399, subs.Month, "2026-12-22", subs.TargetRenewal, subs.Keep),
			item("minimax", "MiniMax", 1000, "USD", 1550, subs.Month, "2026-12-27", subs.TargetRenewal, subs.Undecided),
		},
		Totals:   subs.Totals{MonthlyAUD: 34280, YearlyAUD: 411360, Next30AUD: 39611, Next30Count: 7},
		Series:   series,
		Channels: subs.Channels{CalendarSyncedAt: &synced, NextDigest: time.Date(2026, 11, 30, 9, 0, 0, 0, time.Local)},
		LastSeq:  4, ServedBy: "archPC", GeneratedAt: now,
	}
}

func state() *State {
	return &State{Snap: fixture(), Now: now, Online: true, LastOK: now, ClientConfig: "~/.config/bitsub/client.json", HostMinor: 28}
}

// walk visits every node.
func walk(n *v1.Node, fn func(*v1.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}

func allText(n *v1.Node) string {
	var b strings.Builder
	walk(n, func(n *v1.Node) { b.WriteString(n.Text + "\n") })
	return b.String()
}

func find(n *v1.Node, id string) *v1.Node {
	var hit *v1.Node
	walk(n, func(n *v1.Node) {
		if n.ID == id {
			hit = n
		}
	})
	return hit
}

func count(n *v1.Node) int {
	c := 0
	walk(n, func(*v1.Node) { c++ })
	return c
}

func checkView(t *testing.T, name string, tree *v1.Node, view v1.ViewKind, w, h int) {
	t.Helper()
	if err := v1.Validate(tree, view); err != nil {
		t.Errorf("%s: validate: %v", name, err)
	}
	for _, f := range lint.Tree(tree, view, w, h) {
		t.Errorf("%s: lint: %s", name, f)
	}
}

func TestBarStatesSameNodeCount(t *testing.T) {
	quiet := state()
	quiet.Snap.Items = quiet.Snap.Items[3:] // only kept items
	q := Bar(quiet, 240)
	if !strings.Contains(allText(q), "A$343/mo") {
		t.Fatalf("quiet bar: %q", allText(q))
	}
	pending := Bar(state(), 240)
	if !strings.Contains(allText(pending), "Speechify Premium · 3d") {
		t.Fatalf("pending bar: %q", allText(pending))
	}
	if count(q) != count(pending) {
		t.Fatalf("node count %d vs %d", count(q), count(pending))
	}
	off := state()
	off.Online = false
	if count(Bar(off, 240)) != count(q) {
		t.Fatal("offline bar changes node count")
	}
	narrow := Bar(state(), 64)
	if strings.Contains(allText(narrow), "Speechify") || strings.Contains(allText(narrow), "A$") {
		t.Fatalf("compact bar has a label: %q", allText(narrow))
	}
	for _, w := range []int{240, 120, 64} {
		for name, s := range map[string]*State{"quiet": quiet, "pending": state(), "offline": off, "empty": {Now: now}} {
			checkView(t, fmt.Sprintf("bar %s @%d", name, w), Bar(s, w), v1.ViewBar, w, 32)
		}
	}
	checkView(t, "tooltip", Tooltip(state()), v1.ViewTooltip, 280, 200)
	checkView(t, "tooltip offline", Tooltip(off), v1.ViewTooltip, 280, 200)
	checkView(t, "tooltip empty", Tooltip(&State{Now: now}), v1.ViewTooltip, 280, 200)
}

func TestPanelLintsInEveryState(t *testing.T) {
	states := map[string]*State{"normal": state()}
	empty := state()
	empty.Snap.Items = nil
	states["empty"] = empty
	off := state()
	off.Online, off.LastOK = false, now.Add(-38*time.Minute)
	states["offline"] = off
	auth := state()
	auth.Online, auth.AuthFailed = false, true
	states["auth failed"] = auth
	confirming := state()
	confirming.Confirming = "stan"
	states["confirming"] = confirming
	many := state()
	for i := 0; i < 23; i++ {
		many.Snap.Items = append(many.Snap.Items, item(fmt.Sprintf("x%d", i), fmt.Sprintf("Extra service %d", i), 999, "AUD", 999, subs.Month, "2026-12-28", subs.TargetRenewal, subs.Undecided))
	}
	states["30 items"] = many
	approx := state()
	approx.Snap.Totals.Approx = true
	approx.Snap.Channels = subs.Channels{CalendarError: "insufficient scope", EmailError: "token expired", FXStale: true}
	states["approx totals"] = approx
	states["never connected"] = &State{Now: now}
	for name, s := range states {
		checkView(t, "panel "+name, Panel(s), v1.ViewPanel, PanelWidth, PanelHeight)
	}

	if !strings.Contains(allText(Panel(empty)), "Nothing tracked yet. Ask an agent to add a subscription.") {
		t.Error("empty copy missing")
	}
	if !strings.Contains(allText(Panel(off)), "Offline: archPC hasn't answered since 14:02 (38 min ago). Changes are paused.") {
		t.Errorf("offline copy: %s", allText(Panel(off)))
	}
	if !strings.Contains(allText(Panel(auth)), "Token rejected: check ~/.config/bitsub/client.json.") {
		t.Error("auth copy missing")
	}
	if txt := allText(Panel(approx)); !strings.Contains(txt, "≈A$342.80") || !strings.Contains(txt, "Calendar out of date: insufficient scope") {
		t.Errorf("approx/errors: %s", txt)
	}
	txt := allText(Panel(state()))
	for _, want := range []string{"Per month", "A$342.80", "A$4,113.60 a year", "Next 30 days", "A$396.11", "7 renewals",
		"Charged per month · 12 months", "Calendar synced 2 min ago", "next email digest Mon 30 Nov", "served by archPC"} {
		if !strings.Contains(txt, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	var graph *v1.Node
	walk(Panel(state()), func(n *v1.Node) {
		if n.Kind == v1.KindGraph {
			graph = n
		}
	})
	if graph == nil || len(graph.Values) != 12 || graph.Values[11] != 1 || graph.Absent {
		t.Fatalf("graph %+v", graph)
	}
	if !strings.Contains(txt, "Peak A$365") {
		t.Errorf("peak label missing")
	}
	zero := state()
	for i := range zero.Snap.Series {
		zero.Snap.Series[i].AUDMinor = 0
	}
	walk(Panel(zero), func(n *v1.Node) {
		if n.Kind == v1.KindGraph && !n.Absent {
			t.Error("all-zero graph not absent")
		}
	})
}

func TestHeaderPlainBeforeSpans(t *testing.T) {
	p := Panel(state())
	header := p.Children[0]
	if len(header.Children) != 3 {
		t.Fatalf("header rows %d", len(header.Children))
	}
	for _, row := range header.Children {
		if row.Size != "mono" || !row.CenterX || !row.Bold || row.Tone != v1.ToneAccent || row.Text == "" {
			t.Errorf("header row %+v", row)
		}
	}
}

func TestRowButtonsReflectIntent(t *testing.T) {
	p := Panel(state())
	check := func(id, fill string) {
		t.Helper()
		n := find(p, id)
		if n == nil || n.Fill != fill || n.Role != "button" || n.Name == "" || len(n.Events) == 0 {
			t.Errorf("%s: %+v", id, n)
		}
	}
	check("keep:netflix", "accent")
	check("cancel:netflix", "outline")
	check("keep:stan", "outline")
	check("cancel:stan", "error")
	check("keep:synthetic", "outline")
	check("cancel:synthetic", "outline")
	off := state()
	off.Online = false
	po := Panel(off)
	for _, id := range []string{"keep:netflix", "cancel:stan", "keep:synthetic"} {
		if n := find(po, id); n == nil || n.Fill != "soft" || !n.Disabled {
			t.Errorf("offline %s: %+v", id, n)
		}
	}
	c := state()
	c.Confirming = "stan"
	pc := Panel(c)
	if find(pc, "keep:stan") != nil || find(pc, "cancel:stan") != nil {
		t.Error("confirming row still shows Keep/Cancel")
	}
	if find(pc, "confirm-yes:stan") == nil || find(pc, "confirm-no:stan") == nil || !strings.Contains(allText(pc), "Have you cancelled it?") {
		t.Error("confirm controls missing")
	}
	if find(pc, "keep:netflix") == nil {
		t.Error("other rows lost their buttons")
	}
}

func TestDateLineCopy(t *testing.T) {
	today := subs.Today(now, time.Local)
	cases := []struct {
		it   subs.Item
		want string
		tone v1.Tone
	}{
		{item("a", "A", 1, "AUD", 1, subs.Year, "2026-12-02", subs.TargetTrialEnd, subs.Undecided), "Trial ends Wed 2 Dec · in 3 days", v1.ToneAccent},
		{item("a", "A", 1, "AUD", 1, subs.Year, "2026-11-30", subs.TargetTrialEnd, subs.Undecided), "Trial ends Mon 30 Nov · tomorrow", v1.ToneError},
		{item("a", "A", 1, "AUD", 1, subs.Year, "2026-11-29", subs.TargetTrialEnd, subs.Undecided), "Trial ends Sun 29 Nov · today", v1.ToneError},
		{item("a", "A", 1, "AUD", 1, subs.Month, "2026-12-14", subs.TargetRenewal, subs.Keep), "Renews Mon 14 Dec", v1.ToneNormal},
		{item("a", "A", 1, "AUD", 1, subs.Month, "2026-12-01", subs.TargetRenewal, subs.Keep), "Renews Tue 1 Dec", v1.ToneNormal},
		{item("a", "A", 1, "AUD", 1, subs.Month, "2026-12-14", subs.TargetRenewal, subs.Undecided), "Renews Mon 14 Dec", v1.ToneNormal},
		{item("a", "A", 1, "AUD", 1, subs.Month, "2026-12-04", subs.TargetRenewal, subs.Undecided), "Renews Fri 4 Dec", v1.ToneAccent},
		{item("a", "A", 1, "AUD", 1, subs.Month, "2026-12-10", subs.TargetRenewal, subs.Cancel), "Renews Thu 10 Dec · cancel before then", v1.ToneNormal},
	}
	for _, c := range cases {
		got, tone := dateLine(c.it, today)
		if got != c.want || tone != c.tone {
			t.Errorf("%s/%s: %q %q, want %q %q", c.it.Target, c.it.Intent, got, tone, c.want, c.tone)
		}
	}
	notice := item("a", "A", 1, "AUD", 1, subs.Month, "2026-12-10", subs.TargetRenewal, subs.Cancel)
	notice.CancelBy = subs.MustDate("2026-12-08")
	if got, _ := dateLine(notice, today); got != "Renews Thu 10 Dec · cancel by Tue 8 Dec" {
		t.Errorf("notice days: %q", got)
	}
	charged := notice
	charged.ProbablyCharged = true
	if got, tone := dateLine(charged, today); got != "Charged — confirm whether you cancelled" || tone != v1.ToneError {
		t.Errorf("charged: %q %q", got, tone)
	}
}

func TestPriceLineSplitsOriginal(t *testing.T) {
	main, orig := priceParts(item("a", "A", 13900, "USD", 21550, subs.Year, "2026-12-02", subs.TargetTrialEnd, subs.Undecided))
	if main != "A$215.50/yr" || orig != "(US$139.00)" {
		t.Fatalf("%q %q", main, orig)
	}
	main, orig = priceParts(item("a", "A", 1899, "AUD", 1899, subs.Month, "2026-12-02", subs.TargetRenewal, subs.Keep))
	if main != "A$18.99/mo" || orig != "" {
		t.Fatalf("%q %q", main, orig)
	}
	noRate := item("a", "A", 3000, "USD", 0, subs.Month, "2026-12-02", subs.TargetRenewal, subs.Keep)
	noRate.PriceApprox = true
	if main, orig = priceParts(noRate); main != "US$30.00/mo" || orig != "" {
		t.Fatalf("%q %q", main, orig)
	}
}

func TestBarLabelFitsWidth(t *testing.T) {
	if got := urgentLabel("Speechify Premium", "3d", labelBudget(240)); got != "Speechify Premium · 3d" {
		t.Errorf("240: %q", got)
	}
	if got := urgentLabel("Speechify Premium", "3d", labelBudget(200)); got != "Speechify… · 3d" || len(got) > labelBudget(200) {
		t.Errorf("200: %q", got)
	}
	if got := urgentLabel("Speechify Premium", "today", labelBudget(120)); got != "today" {
		t.Errorf("120: %q", got)
	}
}
