package faith

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var t0 = time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

func newSession(t *testing.T, mut func(*Settings)) *Session {
	t.Helper()
	s := DefaultSettings()
	if mut != nil {
		mut(&s)
	}
	ss, err := NewSession(s, rand.New(rand.NewPCG(9, 10)), t0, true)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func click(node string, ev v1.EventKind, button v1.PointerButton) *v1.InputEvent {
	return &v1.InputEvent{ViewID: "bar-1", Node: node, Event: ev, Button: button, Output: "DP-1"}
}

func effectsOf[T any](effects []Effect) []T {
	var out []T
	for _, e := range effects {
		if v, ok := e.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func TestCrossClicks(t *testing.T) {
	s := newSession(t, nil)
	if n := effectsOf[NotifyEffect](s.Input(click(NodeCross, v1.EventActivate, ""), t0)); len(n) != 1 || n[0].Params.Summary == "" {
		t.Fatalf("left click (activate) gave %v", n)
	}
	if e := s.Input(click(NodeCross, v1.EventPointer, v1.ButtonPrimary), t0); len(e) != 0 {
		t.Fatalf("the primary press gave %v; a left click would pray twice", e)
	}
	e := s.Input(click(NodeCross, v1.EventPointer, v1.ButtonSecondary), t0)
	p := effectsOf[OpenPanelEffect](e)
	if len(p) != 1 || p[0].Output != "DP-1" || p[0].Instance != "bar-1" || len(effectsOf[NotifyEffect](e)) != 0 {
		t.Fatalf("right click gave %v", e)
	}
	if e := s.Input(click(NodeCross, v1.EventPointer, v1.ButtonMiddle), t0); len(e) != 0 {
		t.Fatalf("middle click gave %v", e)
	}
}

func TestPrayersFollowTheTraditionSetting(t *testing.T) {
	s := newSession(t, func(st *Settings) { st.Tradition = Orthodox; st.PrayerSeconds = 12 })
	seen := map[string]bool{}
	for range len(PrayerPool(Orthodox)) {
		n := effectsOf[NotifyEffect](s.pray())
		seen[n[0].Params.Summary] = true
		if n[0].Params.TimeoutMS != 12000 {
			t.Fatalf("timeout %d", n[0].Params.TimeoutMS)
		}
	}
	if !seen["The Trisagion"] || seen["Hail Mary"] || len(seen) != len(PrayerPool(Orthodox)) {
		t.Fatalf("an orthodox round drew %v", seen)
	}
}

func TestNavigationAndCommentaryRequests(t *testing.T) {
	s := newSession(t, nil)
	s.ref = mustRef(t, "JHN 3:16")
	if e := s.Input(click(NodeNext, v1.EventActivate, ""), t0); len(effectsOf[FetchCommentaryEffect](e)) != 0 || len(effectsOf[SaveStateEffect](e)) != 1 {
		t.Fatalf("next with no panel open gave %v", e)
	}
	if s.ref.Key() != "JHN 3:17" {
		t.Fatalf("after next: %s", s.ref.Key())
	}
	f := effectsOf[FetchCommentaryEffect](s.PanelOpened())
	if len(f) != 1 || f[0].Ref.Key() != "JHN 3:17" {
		t.Fatalf("opening the panel gave %v", f)
	}
	stale := f[0].Gen
	f = effectsOf[FetchCommentaryEffect](s.Input(click(NodePrev, v1.EventActivate, ""), t0))
	if len(f) != 1 || f[0].Ref.Key() != "JHN 3:16" {
		t.Fatalf("prev with the panel open gave %v", f)
	}
	if s.CommentaryResult(stale, "old", true, nil) {
		t.Fatal("a stale note was applied")
	}
	if !s.CommentaryResult(f[0].Gen, "For God so loved", true, nil) || s.commentary != CommentaryReady || s.note != "For God so loved" {
		t.Fatal("the current note was not applied")
	}
	s.Input(click(NodeNext, v1.EventActivate, ""), t0)
	s.CommentaryResult(s.noteGen, "", false, errors.New("offline"))
	if s.commentary != CommentaryUnavailable {
		t.Fatalf("an error left status %v", s.commentary)
	}
	s.PanelClosed()
	if e := s.Input(click(NodeNext, v1.EventActivate, ""), t0); len(effectsOf[FetchCommentaryEffect](e)) != 0 {
		t.Fatal("a closed panel still fetched commentary")
	}
}

func TestCrossReferenceAndReadButtons(t *testing.T) {
	s := newSession(t, nil)
	s.ref = mustRef(t, "JHN 3:16")
	s.Input(click(XrefNode(0), v1.EventActivate, ""), t0)
	if s.ref.Key() != "ROM 5:8" {
		t.Fatalf("xref:0 went to %s", s.ref.Key())
	}
	u := effectsOf[OpenURLEffect](s.Input(click(NodeRead, v1.EventActivate, ""), t0))
	if len(u) != 1 || u[0].URL != "https://biblehub.com/bsb/romans/5.htm" {
		t.Fatalf("read gave %v", u)
	}
	if e := s.Input(click(XrefNode(9), v1.EventActivate, ""), t0); e != nil {
		t.Fatalf("xref:9 gave %v", e)
	}
}

func TestRefreshWaitsForAClosedPanel(t *testing.T) {
	s := newSession(t, func(st *Settings) { st.RefreshMinutes = 10 })
	start := s.ref
	if changed, _ := s.Tick(t0.Add(9 * time.Minute)); changed {
		t.Fatal("refreshed early")
	}
	s.PanelOpened()
	if changed, _ := s.Tick(t0.Add(11 * time.Minute)); changed {
		t.Fatal("refreshed under an open panel")
	}
	s.PanelClosed()
	if changed, _ := s.Tick(t0.Add(11 * time.Minute)); !changed || s.ref == start {
		t.Fatal("did not refresh once the panel closed")
	}
	s.settings.RefreshMinutes = 0
	if changed, _ := s.Tick(t0.Add(24 * time.Hour)); changed {
		t.Fatal("refreshed with refresh off")
	}
}

func TestDailyModeChangesAtMidnight(t *testing.T) {
	s := newSession(t, func(st *Settings) { st.VerseMode = ModeDaily })
	if s.ref != s.pool.Daily(t0) {
		t.Fatal("daily mode did not start on the day's verse")
	}
	if changed, _ := s.Tick(t0.Add(10 * time.Hour)); changed {
		t.Fatal("changed within the day")
	}
	if changed, _ := s.Tick(t0.Add(16 * time.Hour)); !changed || s.ref != s.pool.Daily(t0.Add(16*time.Hour)) {
		t.Fatal("did not change after midnight")
	}
	s.Restore(Persisted{Ref: &Ref{Book: 0, Chapter: 1, Verse: 1}})
	if s.ref.Key() == "GEN 1:1" {
		t.Fatal("restore overrode the daily verse")
	}
}

func TestSettingsChangesApply(t *testing.T) {
	s := newSession(t, nil)
	s.ref = mustRef(t, "LUK 17:35")
	s.ApplySettings(map[string]any{"translation": "WEB", "prayer_seconds": float64(500), "show_reference": true, "verse_mode": "bogus"}, t0)
	st := s.Settings()
	if st.Translation != "WEB" || st.PrayerSeconds != 300 || !st.ShowReference || st.VerseMode != ModePool {
		t.Fatalf("settings = %+v", st)
	}
	if s.bible.ID != "WEB" || s.ref.Key() != "LUK 17:35" {
		t.Fatalf("bible %s ref %s", s.bible.ID, s.ref.Key())
	}
	s.ApplySettings(map[string]any{"translation": "NIV"}, t0)
	if s.Settings().Translation != "WEB" {
		t.Fatal("accepted an unbundled translation")
	}
	e := s.ApplySettings(map[string]any{"verse_mode": "bible"}, t0)
	if len(effectsOf[SaveStateEffect](e)) != 1 {
		t.Fatalf("a mode change gave %v", e)
	}
}

func TestPersistedRoundTrip(t *testing.T) {
	s := newSession(t, nil)
	s.ref = mustRef(t, "ROM 8:38-39")
	s.pray()
	p := s.Persisted()
	s2 := newSession(t, nil)
	s2.Restore(p)
	if s2.ref.Key() != "ROM 8:38-39" || s2.bag.Pos != 1 || s2.bag.Last != s.bag.Last {
		t.Fatalf("restored %s, bag %+v", s2.ref.Key(), s2.bag)
	}
	s2.Restore(Persisted{Ref: &Ref{}})
	if s2.ref.Key() != "ROM 8:38-39" {
		t.Fatal("a zero reference (a stored null) replaced the current one")
	}
	s2.Restore(Persisted{Ref: &Ref{Book: 99, Chapter: 1, Verse: 1}})
	if s2.ref.Key() != "ROM 8:38-39" {
		t.Fatal("a corrupt reference replaced the current one")
	}
}

func TestSessionViewsFit(t *testing.T) {
	s := newSession(t, nil)
	if find(s.BarTree(), NodeCross) == nil || find(s.PanelTree(), NodeNew) == nil || len(texts(s.TooltipTree())) < 3 {
		t.Fatal("session views are incomplete")
	}
}
