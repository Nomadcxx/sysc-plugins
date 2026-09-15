package recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderUnavailableWithoutBackend(t *testing.T) {
	t.Parallel()
	r := New(mustConfig(t, nil), Options{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	t.Cleanup(r.Close)
	if got := r.Snapshot().Mode; got != Unavailable {
		t.Fatalf("mode = %s, want unavailable", got)
	}
	if r.Snapshot().Err == "" {
		t.Fatal("unavailable snapshot has no error copy")
	}
	r.ToggleRecord("DP-1")
	if got := r.Snapshot().Mode; got != Unavailable {
		t.Fatalf("toggle moved unavailable to %s", got)
	}
}

func TestRecorderToggleRecordAndStop(t *testing.T) {
	r := testRecorder(t, nil, "hang")
	if got := r.Snapshot().Mode; got != Idle {
		t.Fatalf("mode = %s, want idle", got)
	}
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	if r.Ownership().PID <= 0 {
		t.Fatal("recording left no pid")
	}
	r.ToggleRecord("DP-1")
	waitMode(t, r, Idle)
	if r.Snapshot().Artifact == "" {
		t.Fatal("stop left no artifact")
	}
	if st, err := os.Stat(r.Snapshot().Artifact); err != nil || st.Size() == 0 {
		t.Fatalf("artifact = %v err=%v", st, err)
	}
}

func TestRecorderRejectsReplayWhileRecording(t *testing.T) {
	r := testRecorder(t, map[string]any{"replay_enabled": true}, "hang")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	r.ToggleReplay("DP-1")
	time.Sleep(50 * time.Millisecond)
	if got := r.Snapshot().Mode; got != Recording {
		t.Fatalf("replay during record switched to %s", got)
	}
}

func TestRecorderRejectsRecordWhileReplay(t *testing.T) {
	r := testRecorder(t, map[string]any{"replay_enabled": true}, "hang")
	r.ToggleReplay("DP-1")
	waitMode(t, r, ReplayActive)
	r.ToggleRecord("DP-1")
	time.Sleep(50 * time.Millisecond)
	if got := r.Snapshot().Mode; got != ReplayActive {
		t.Fatalf("record during replay switched to %s", got)
	}
}

func TestRecorderRepeatedToggleDoesNotRestart(t *testing.T) {
	r := testRecorder(t, nil, "hang")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	r.ToggleRecord("DP-1")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Idle)
	if got := r.Snapshot().Mode; got != Idle {
		t.Fatalf("mode = %s after repeated stop", got)
	}
}

func TestRecorderProcessExitFails(t *testing.T) {
	r := testRecorder(t, nil, "crash")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Failed)
	if r.Snapshot().Err == "" {
		t.Fatal("failed snapshot has no error")
	}
}

func TestRecorderStartsWhenBackendLogsGsrInfo(t *testing.T) {
	r := testRecorder(t, nil, "silent-run")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	r.ToggleRecord("DP-1")
	waitMode(t, r, Idle)
}

func TestRecorderZeroByteArtifactFails(t *testing.T) {
	r := testRecorder(t, nil, "zero")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	r.ToggleRecord("DP-1")
	waitMode(t, r, Failed)
}

func TestRecorderRetryAfterFailure(t *testing.T) {
	r := testRecorder(t, nil, "crash")
	r.ToggleRecord("DP-1")
	waitMode(t, r, Failed)
	r.Retry()
	waitMode(t, r, Idle)
}

func TestReplayStartSaveAndStop(t *testing.T) {
	r := testRecorder(t, map[string]any{"replay_enabled": true}, "hang")
	keep := filepath.Join(r.cfg.Directory, "keep.mp4")
	if err := os.WriteFile(keep, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ToggleReplay("DP-1")
	waitMode(t, r, ReplayActive)
	r.SaveReplay()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.Snapshot().Artifact != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := r.Snapshot().Artifact
	if got == "" || got == keep {
		t.Fatalf("claimed %q", got)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("claim removed an existing file")
	}
	body, err := os.ReadFile(got)
	if err != nil || string(body) != "mp4" {
		t.Fatalf("claimed body %q err=%v", body, err)
	}
	r.ToggleReplay("DP-1")
	waitMode(t, r, Idle)
}

func TestRecorderAdoptedFromPersistedOwnership(t *testing.T) {
	cfg := mustConfig(t, map[string]any{"directory": t.TempDir()})
	args, err := cfg.RecordArgs("DP-1", filepath.Join(cfg.Directory, "live.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	p := startFake(t, "hang", args)
	waitReady(t, p)
	r := New(cfg, testOpts("hang"))
	t.Cleanup(r.Close)
	r.Recover(Ownership{PID: p.PID(), Exe: os.Args[0], Args: args})
	waitMode(t, r, Adopted)
	if r.Ownership().PID != p.PID() {
		t.Fatalf("pid = %d, want %d", r.Ownership().PID, p.PID())
	}
}

func TestFilenameConfiguredPattern(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 9, 2, 18, 4, 5, 0, time.UTC)
	got, err := destPath(dir, "clip_%Y%m%d_%H%M%S", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "clip_20260902_180405.mp4" {
		t.Fatalf("got %s", got)
	}
}

func TestFilenameCollisionFreeAndPreservesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)
	first := filepath.Join(dir, "recording_20260902_180000.mp4")
	if err := os.WriteFile(first, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := destPath(dir, "recording_%Y%m%d_%H%M%S", now)
	if err != nil {
		t.Fatal(err)
	}
	if got == first {
		t.Fatal("reused an existing path")
	}
	body, err := os.ReadFile(first)
	if err != nil || string(body) != "keep" {
		t.Fatalf("existing file changed: %q err=%v", body, err)
	}
}

func TestFilenameCreatesDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nested", "out")
	got, err := destPath(dir, "take", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != dir {
		t.Fatalf("dir = %s", got)
	}
}

func TestArtifactDestPathDoesNotCreateTheFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := destPath(dir, "fresh_%Y", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("destPath created %s: %v", got, err)
	}
}

func testRecorder(t *testing.T, values map[string]any, behavior string) *Recorder {
	t.Helper()
	if values == nil {
		values = map[string]any{}
	}
	values["directory"] = t.TempDir()
	r := New(mustConfig(t, values), testOpts(behavior))
	t.Cleanup(r.Close)
	return r
}

func testOpts(behavior string) Options {
	return Options{
		Exe: os.Args[0],
		LookPath: func(string) (string, error) {
			return os.Args[0], nil
		},
		Env:      append(os.Environ(), "SYSC_FAKE_RECORDER=1", "SYSC_FAKE_BEHAVIOR="+behavior),
		StopWait: 250 * time.Millisecond,
		Now:      func() time.Time { return time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC) },
	}
}

func mustConfig(t *testing.T, values map[string]any) Config {
	t.Helper()
	cfg, err := ParseConfig(values)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func waitMode(t *testing.T, r *Recorder, want Mode) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var last Mode
	for time.Now().Before(deadline) {
		last = r.Snapshot().Mode
		if last == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("mode = %s, want %s", last, want)
}

func TestRecorderReconfigureRebuildsNextCommand(t *testing.T) {
	r := testRecorder(t, map[string]any{"frame_rate": 60.0}, "hang")
	next, err := ParseConfig(map[string]any{"directory": r.cfg.Directory, "frame_rate": 24.0})
	if err != nil {
		t.Fatal(err)
	}
	r.Reconfigure(next)
	time.Sleep(30 * time.Millisecond)
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	args := r.Ownership().Args
	if !hasPair(args, "-f", "24") {
		t.Fatalf("args = %v, want frame rate 24", args)
	}
}
