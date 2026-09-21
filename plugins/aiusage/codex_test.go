package aiusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSession writes one session file with an explicit mtime so the
// newest-by-event-timestamp rule can be exercised against the filesystem.
func writeSession(t *testing.T, dir, name, content string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

// homeEnv injects a fake CODEX_HOME through the Env seam, keeping the tests
// parallel without touching the process environment.
func homeEnv(home string) Env {
	return Env{Env: func(key string) string {
		if key == "CODEX_HOME" {
			return home
		}
		return ""
	}}
}

func TestSnapshotNewestByEventTimestamp(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	sessions := filepath.Join(home, "sessions")

	// The older file on disk carries the newer event: it must win.
	writeSession(t, sessions, "2026/09/roll-a.jsonl",
		`{"timestamp":"2026-09-19T12:00:00Z","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","plan_type":"plus","primary":{"used_percent":20,"window_minutes":300,"resets_at":1787224800},"secondary":{"used_percent":35,"window_minutes":10080,"resets_at":1787743200}}}}`+"\n",
		base.Add(-2*time.Hour))
	writeSession(t, sessions, "2026/09/roll-b.jsonl",
		`{"timestamp":"2026-09-19T11:00:00Z","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":99,"window_minutes":300,"resets_at":1787224800}}}}`+"\n",
		base.Add(-1*time.Hour))

	rep, err := NewSnapshot(homeEnv(home)).Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh {
		t.Fatalf("state = %v, want fresh", rep.State)
	}
	if len(rep.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(rep.Windows))
	}
	if rep.Windows[0].Key != "primary" || rep.Windows[0].UsedPercent != 20 {
		t.Fatalf("primary = %+v", rep.Windows[0])
	}
	if rep.Windows[0].Label != "Session" || rep.Windows[0].ShortLabel != "5h" {
		t.Fatalf("primary label = %q/%q", rep.Windows[0].Label, rep.Windows[0].ShortLabel)
	}
	if rep.Windows[1].UsedPercent != 35 || rep.Windows[1].WindowMinutes != 10080 {
		t.Fatalf("secondary = %+v", rep.Windows[1])
	}
	if rep.Plan != "Plus" {
		t.Fatalf("plan = %q, want Plus", rep.Plan)
	}
	if !rep.UpdatedAt.Equal(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("updated = %v, want the winning event's timestamp", rep.UpdatedAt)
	}
}

func TestSnapshotSkipsPremiumAndTruncatedTail(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	src, err := os.ReadFile("testdata/codex-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, filepath.Join(home, "sessions"), "2026/09/fixture.jsonl", string(src), base)

	rep, err := NewSnapshot(homeEnv(home)).Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The premium row sits after the codex row in the file; the tail scan
	// must not take it, and the truncated final line must not break it.
	if rep.Windows[0].UsedPercent != 20 {
		t.Fatalf("primary = %+v, want the codex snapshot at 20%%", rep.Windows[0])
	}
	if rep.State != StateFresh || rep.Plan != "Plus" {
		t.Fatalf("state/plan = %v/%q", rep.State, rep.Plan)
	}
}

func TestSnapshotNoDataAndMissingHome(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	rep, err := NewSnapshot(homeEnv(home)).Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateNoData || rep.ID != "codex" {
		t.Fatalf("empty sessions = %+v, want NoData", rep)
	}

	rep, err = NewSnapshot(homeEnv(filepath.Join(home, "absent"))).Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateNoData {
		t.Fatalf("missing home = %+v, want NoData", rep)
	}
}

func TestSnapshotTailScanFindsDeepLine(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	// Padding past the first 64 KiB chunk boundary forces the backwards scan
	// through multiple reads, with the only match in the final line.
	pad := strings.Repeat(" ", 140_000)
	row := `{"timestamp":"2026-09-19T12:00:00Z","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","primary":{"used_percent":20,"window_minutes":300,"resets_at":1787224800}}}}`
	writeSession(t, filepath.Join(home, "sessions"), "deep.jsonl", pad+"\n"+row+"\n", base)

	rep, err := NewSnapshot(homeEnv(home)).Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateFresh || rep.Windows[0].UsedPercent != 20 {
		t.Fatalf("deep scan = %+v", rep)
	}
}
