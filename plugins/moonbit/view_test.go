package moonbit

import (
	"testing"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// states builds one State per phase by folding the same event contract the
// daemon emits, so the tests exercise the real Fold paths.
func states() map[Phase]*State {
	out := map[Phase]*State{}
	with := &State{}
	with.Fold(Event{T: "status", Daemon: true, Cache: &CacheInfo{Files: 17772, Bytes: 4511971727,
		Categories: []CategoryStat{{Name: "Pacman Cache", Files: 498, Bytes: 782317440},
			{Name: "Journal Logs", Files: 12, Bytes: 128849018}}}})
	out[PhaseIdle] = with

	sc := &State{}
	sc.StartScan()
	sc.Fold(Event{T: "category", Name: "Pacman Cache", I: 1, Total: 3})
	sc.Fold(Event{T: "scan", Files: 42, Bytes: 1024, Dir: "/var/cache/pacman/pkg"})
	out[PhaseScanning] = sc

	rv := &State{}
	rv.StartScan()
	rv.Fold(Event{T: "category", Name: "Pacman Cache", I: 1, Total: 2})
	rv.Fold(Event{T: "category_done", Name: "Pacman Cache", Files: 498, Bytes: 782317440})
	rv.Fold(Event{T: "category", Name: "Journal Logs", I: 2, Total: 2})
	rv.Fold(Event{T: "category_done", Name: "Journal Logs", Files: 12, Bytes: 128849018})
	rv.Fold(Event{T: "done", Files: 510, Bytes: 911166458})
	if rv.Phase != PhaseReview {
		panic("done must land in review")
	}
	out[PhaseReview] = rv

	cf := &State{Phase: PhaseConfirm}
	cf.Review = rv.Review
	cf.Selected = rv.Selected
	out[PhaseConfirm] = cf

	cl := &State{}
	cl.StartClean()
	cl.Fold(Event{T: "clean_begin", Files: 510, Bytes: 911166458})
	cl.Fold(Event{T: "clean", Done: 12, Total: 510, Freed: 40960, File: "/var/cache/pacman/pkg/linux-1.img.pkg"})
	out[PhaseCleaning] = cl

	done := &State{}
	done.Fold(Event{T: "clean_done", Deleted: 508, Freed: 900000000, Errors: []string{"one"}})
	out[PhaseDone] = done

	er := &State{}
	er.Fold(Event{T: "error", Msg: "moonbit daemon unreachable"})
	out[PhaseError] = er
	return out
}

func TestViewsValidate(t *testing.T) {
	for phase, s := range states() {
		if err := v1.Validate(Bar(s), v1.ViewBar); err != nil {
			t.Errorf("phase %d bar: %v", phase, err)
		}
		if err := v1.Validate(Tooltip(s), v1.ViewTooltip); err != nil {
			t.Errorf("phase %d tooltip: %v", phase, err)
		}
		if err := v1.Validate(Panel(s), v1.ViewPanel); err != nil {
			t.Errorf("phase %d panel: %v", phase, err)
		}
	}
}

// Each wordmark row is centred on its own, so the rows must share one width
// or every row shifts against the one above and the strokes stop meeting.
func TestPanelWordmarkRowsShareOneWidth(t *testing.T) {
	header := Panel(states()[PhaseIdle]).Children[0]
	if header.Kind != v1.KindColumn || len(header.Children) != 3 {
		t.Fatalf("wordmark header = %+v, want a column of three rows", header)
	}
	want := utf8.RuneCountInString(header.Children[0].Text)
	for i, row := range header.Children {
		if n := utf8.RuneCountInString(row.Text); n != want {
			t.Errorf("row %d is %d cells wide, want %d", i, n, want)
		}
		if row.Kind != v1.KindText || row.Size != "mono" || row.Tone != v1.ToneAccent || !row.CenterX {
			t.Errorf("row %d = %+v, want centred mono accent text", i, row)
		}
	}
}

func TestFoldReviewToCleanRoundTrip(t *testing.T) {
	s := states()[PhaseReview]
	if len(s.SelectedStats()) != 2 {
		t.Fatalf("all categories with files should start selected, got %d", len(s.SelectedStats()))
	}
	s.Selected["Journal Logs"] = false
	if got := len(s.SelectedStats()); got != 1 {
		t.Fatalf("deselect leaves %d", got)
	}
	s.Fold(Event{T: "clean_begin", Files: 498, Bytes: 782317440})
	if s.Phase != PhaseCleaning || s.CleanTotal != 498 {
		t.Fatalf("clean_begin: %+v", s)
	}
	s.Fold(Event{T: "cancelled"})
	if s.Phase != PhaseIdle {
		t.Fatalf("cancel must return to idle")
	}
}

func TestScanValueCeiling(t *testing.T) {
	s := &State{}
	s.StartScan()
	s.Fold(Event{T: "category", Name: "a", I: 1, Total: 1})
	s.Fold(Event{T: "category_done", Name: "a", Files: 1, Bytes: 1})
	if v := s.ScanValue(); v > 0.99 || v <= 0 {
		t.Fatalf("scan value %v must be in (0, 0.99]", v)
	}
}

func TestDoneUsesOnlyCurrentCleanableCategories(t *testing.T) {
	stale := &Event{Cache: &CacheInfo{Categories: []CategoryStat{{Name: "old cache", Files: 7, Bytes: 70}}}}

	t.Run("empty scan does not reuse stale cache", func(t *testing.T) {
		s := &State{Status: stale}
		s.StartScan()
		s.Fold(Event{T: "done"})
		if len(s.Review) != 0 || len(s.SelectedStats()) != 0 {
			t.Fatalf("empty scan reused cached categories: review=%+v selected=%+v", s.Review, s.SelectedStats())
		}
	})

	t.Run("zero-file categories are omitted", func(t *testing.T) {
		s := &State{Status: stale}
		s.StartScan()
		s.Fold(Event{T: "category_done", Name: "empty", Files: 0})
		s.Fold(Event{T: "category_done", Name: "fresh", Files: 2, Bytes: 200})
		s.Fold(Event{T: "done"})
		if len(s.Review) != 1 || s.Review[0].Name != "fresh" {
			t.Fatalf("review must contain only cleanable results from this scan: %+v", s.Review)
		}
	})
}

func TestEmptyReviewOffersScanActionsWithoutClean(t *testing.T) {
	nodes := reviewBody(&State{Phase: PhaseReview})
	if len(nodes) != 3 || nodes[1].Text != "No cleanable files found in this scan." {
		t.Fatalf("missing empty scan state: %+v", nodes)
	}
	ids := map[string]bool{}
	for _, button := range nodes[2].Children {
		ids[button.ID] = true
	}
	for _, id := range []string{"scan_quick", "scan_deep", "back"} {
		if !ids[id] {
			t.Errorf("empty review missing %s action", id)
		}
	}
	if ids["to_confirm"] {
		t.Fatal("empty review must not offer a clean action")
	}
}

func TestReviewOmitsCleanActionWhenNothingIsSelected(t *testing.T) {
	s := states()[PhaseReview]
	for name := range s.Selected {
		s.Selected[name] = false
	}
	nodes := reviewBody(s)
	ids := map[string]bool{}
	for _, button := range nodes[len(nodes)-1].Children {
		ids[button.ID] = true
	}
	if ids["to_confirm"] {
		t.Fatal("review must not offer a clean action with zero selected categories")
	}
}

func TestBarUsesThemeTintedMoonbitMark(t *testing.T) {
	icon := Bar(&State{}).Children[0].Children[0]
	if icon.Kind != v1.KindIcon || icon.Icon != "moonbit" || icon.Tone != v1.ToneAccent {
		t.Fatalf("bar icon = %+v, want the theme-accent Moonbit mark", icon)
	}
}
