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
