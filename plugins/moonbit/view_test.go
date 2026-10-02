package moonbit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// states builds one State per phase by folding the same event contract the
// daemon emits, so the tests exercise the real Fold paths.
func states() map[Phase]*State {
	out := map[Phase]*State{}
	with := &State{Cache: &CacheInfo{Files: 17772, Bytes: 4511971727,
		Categories: []CategoryStat{{Name: "Pacman Cache", Files: 498, Bytes: 782317440},
			{Name: "Journal Logs", Files: 12, Bytes: 128849018}}}}
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
	sched := Schedule{Known: true, DaemonEnabled: true, DaemonActive: true, ScanTimer: true}
	out[PhaseAuth] = &State{Phase: PhaseAuth, AuthFor: "a deep scan", AuthErr: "Sorry, that password wasn't accepted. Try again.", Back: PhaseIdle}
	out[PhaseDocker] = &State{Phase: PhaseDocker, Notice: "Docker freed 1.2GB."}
	out[PhaseDockerConfirm] = &State{Phase: PhaseDockerConfirm, DockerOp: "all"}
	out[PhaseSchedule] = &State{Phase: PhaseSchedule, Schedule: sched}
	out[PhaseWorking] = &State{Phase: PhaseWorking, Working: "Cleaning Docker", Back: PhaseDocker}
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
	stale := &CacheInfo{Categories: []CategoryStat{{Name: "old cache", Files: 7, Bytes: 70}}}

	t.Run("empty scan does not reuse stale cache", func(t *testing.T) {
		s := &State{Cache: stale}
		s.StartScan()
		s.Fold(Event{T: "done"})
		if len(s.Review) != 0 || len(s.SelectedStats()) != 0 {
			t.Fatalf("empty scan reused cached categories: review=%+v selected=%+v", s.Review, s.SelectedStats())
		}
	})

	t.Run("zero-file categories are omitted", func(t *testing.T) {
		s := &State{Cache: stale}
		s.StartScan()
		s.Fold(Event{T: "category_done", Name: "empty", Files: 0})
		s.Fold(Event{T: "category_done", Name: "fresh", Files: 2, Bytes: 200})
		s.Fold(Event{T: "done"})
		if len(s.Review) != 1 || s.Review[0].Name != "fresh" {
			t.Fatalf("review must contain only cleanable results from this scan: %+v", s.Review)
		}
	})
}

func findID(n *v1.Node, id string) *v1.Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if f := findID(c, id); f != nil {
			return f
		}
	}
	return nil
}

func TestEmptyReviewOffersScanActionsWithoutClean(t *testing.T) {
	p := Panel(&State{Phase: PhaseReview})
	for _, id := range []string{"scan_quick", "scan_deep", "back"} {
		if findID(p, id) == nil {
			t.Errorf("empty review missing %s action", id)
		}
	}
	if findID(p, "to_confirm") != nil {
		t.Fatal("empty review must not offer a clean action")
	}
}

// With nothing selected the clean action stays in its slot, disabled, so the
// bar does not jump and the reason is announced.
func TestReviewDisablesCleanWhenNothingIsSelected(t *testing.T) {
	s := states()[PhaseReview]
	for name := range s.Selected {
		s.Selected[name] = false
	}
	clean := findID(Panel(s), "to_confirm")
	if clean == nil || !clean.Disabled {
		t.Fatalf("clean action = %+v, want present and disabled", clean)
	}
}

func TestConfirmSaysWhatIsEmptiedRatherThanDeleted(t *testing.T) {
	s := &State{Phase: PhaseConfirm, Selected: map[string]bool{"Pacman Cache": true, "System Logs": true},
		Review: []CategoryStat{
			{Name: "Pacman Cache", Files: 10, Bytes: 1000},
			{Name: "System Logs", Files: 3, Bytes: 30, Truncate: true},
		}}
	got := confirmCaption(s)
	if !strings.Contains(got, "10 deleted, 3 files emptied in place") {
		t.Fatalf("confirm caption = %q, want the delete and truncate split", got)
	}
}

func TestPluralAndCount(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{plural(1, "category", "categories"), "1 category"},
		{plural(2, "category", "categories"), "2 categories"},
		{count(12297), "12,297"},
		{count(999), "999"},
		{count(1000000), "1,000,000"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestScanValueCountsFailedAndSkippedCategories(t *testing.T) {
	s := &State{ScanTotal: 4, ScanCats: []CategoryStat{{Name: "a"}}, ScanErrs: []string{"b: x"}, ScanSkipped: []string{"c"}}
	if v := s.ScanValue(); v != 0.75 {
		t.Fatalf("scan value = %v, want 0.75 with three of four categories settled", v)
	}
}

func TestDoneRecordsTheScanStampForTheClean(t *testing.T) {
	s := &State{}
	s.StartScan()
	s.Fold(Event{T: "category_done", Name: "Logs", Files: 2, Truncate: true})
	s.Fold(Event{T: "done", ScannedAt: "2026-10-02T12:30:54.078412521Z"})
	if s.ScannedAt != "2026-10-02T12:30:54.078412521Z" || !s.Review[0].Truncate {
		t.Fatalf("state after done = %+v", s)
	}
	s.StartScan()
	if s.ScannedAt != "" {
		t.Fatal("a new scan must drop the old stamp")
	}
}

func TestBarUsesThemeTintedMoonbitMark(t *testing.T) {
	icon := Bar(&State{}).Children[0].Children[0]
	if icon.Kind != v1.KindIcon || icon.Icon != "moonbit" || icon.Tone != v1.ToneAccent {
		t.Fatalf("bar icon = %+v, want the theme-accent Moonbit mark", icon)
	}
}

func TestLastScanRollsUpTheUsersCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan_results.json")
	if err := os.WriteFile(path, []byte(`{"scan_results":{"files":[
		{"path":"/a","size":10,"category_name":"npm Cache"},
		{"path":"/b","size":5,"category_name":"Pacman Cache"},
		{"path":"/c","size":1,"category_name":"npm Cache"}]},
		"total_size":16,"total_files":3,"scanned_at":"2026-10-02T12:30:54.078412521Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYSC_MOONBIT_CACHE", path)
	c, err := LastScan()
	if err != nil {
		t.Fatal(err)
	}
	if c.Files != 3 || c.Bytes != 16 || c.ScannedAt != "2026-10-02T12:30:54.078412521Z" {
		t.Fatalf("cache = %+v", c)
	}
	if len(c.Categories) != 2 || c.Categories[0] != (CategoryStat{Name: "npm Cache", Files: 2, Bytes: 11}) {
		t.Fatalf("categories = %+v", c.Categories)
	}

	t.Setenv("SYSC_MOONBIT_CACHE", filepath.Join(t.TempDir(), "absent.json"))
	if c, err := LastScan(); c != nil || err != nil {
		t.Fatalf("missing cache = (%+v, %v), want no scan and no error", c, err)
	}
}

// Every root run, whatever starts it, passes the password prompt first.
func TestPasswordFieldIsMaskedAndClearable(t *testing.T) {
	f := findID(Panel(&State{Phase: PhaseAuth, AuthReseed: 3}), "password")
	if f == nil || !f.Masked || f.Text != "" || f.Reseed != 3 {
		t.Fatalf("password field = %+v, want masked, empty, reseeded", f)
	}
}
