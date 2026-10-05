package recorder

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func TestRecorderRecoversFromFailed(t *testing.T) {
	opt := testOpts("hang")
	real := Start
	fails := 1
	opt.Start = func(ctx context.Context, path string, args, env []string) (*Proc, error) {
		if fails > 0 {
			fails--
			return nil, errors.New("start refused")
		}
		return real(ctx, path, args, env)
	}
	r := New(mustConfig(t, map[string]any{"directory": t.TempDir()}), opt)
	t.Cleanup(r.Close)

	r.ToggleRecord("DP-1")
	waitMode(t, r, Failed)
	r.ToggleRecord("DP-1")
	waitMode(t, r, Recording)
	r.ToggleRecord("DP-1")
	waitMode(t, r, Idle)
}

func TestRecorderReconfigureClearsFailed(t *testing.T) {
	opt := testOpts("hang")
	opt.Start = func(context.Context, string, []string, []string) (*Proc, error) {
		return nil, errors.New("start refused")
	}
	r := New(mustConfig(t, map[string]any{"directory": t.TempDir()}), opt)
	t.Cleanup(r.Close)

	r.ToggleRecord("DP-1")
	waitMode(t, r, Failed)
	r.Reconfigure(mustConfig(t, map[string]any{"directory": t.TempDir()}))
	waitMode(t, r, Idle)
}

func TestRecorderReplayRecoversFromFailed(t *testing.T) {
	opt := testOpts("hang")
	real := Start
	fails := 1
	opt.Start = func(ctx context.Context, path string, args, env []string) (*Proc, error) {
		if fails > 0 {
			fails--
			return nil, errors.New("start refused")
		}
		return real(ctx, path, args, env)
	}
	r := New(mustConfig(t, map[string]any{"directory": t.TempDir(), "replay_enabled": true}), opt)
	t.Cleanup(r.Close)

	r.ToggleReplay("DP-1")
	waitMode(t, r, Failed)
	r.ToggleReplay("DP-1")
	waitMode(t, r, ReplayActive)
	r.ToggleReplay("DP-1")
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

// An adopted backend that exits on its own must wake the loop, not leave the
// recorder stuck in Adopted forever.
func TestRecorderAdoptedExitFails(t *testing.T) {
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

	if err := p.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
	waitMode(t, r, Failed)
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

func TestRecoverRestoresTheRecordingDestination(t *testing.T) {
	t.Parallel()
	artifact := filepath.Join(t.TempDir(), "rec.mp4")
	if err := os.WriteFile(artifact, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real process stands in for the adopted backend: the stop path sends
	// it SIGINT, so the test process itself would die on its own PID.
	backend := exec.Command("sleep", "30")
	if err := backend.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Process.Kill() })
	args := []string{"-w", "portal", "-o", artifact}
	own := Ownership{PID: backend.Process.Pid, Exe: os.Args[0], Args: args}
	scan := func() ([]ProcInfo, error) {
		return []ProcInfo{{PID: own.PID, Exe: os.Args[0], Args: args}}, nil
	}
	rec := New(Config{}, Options{
		Now:      func() time.Time { return time.Unix(1_000_000, 0) },
		Scan:     scan,
		LookPath: func(string) (string, error) { return os.Args[0], nil },
	})
	rec.Recover(own)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec.Snapshot().Mode == Adopted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := rec.Snapshot().Mode; got != Adopted {
		t.Fatalf("mode = %q, want adopted", got)
	}
	rec.ToggleRecord("DP-1")
	// An adopted backend has no reap goroutine, so Stop waits out its whole
	// SIGINT grace before giving up and killing it.
	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		snap := rec.Snapshot()
		if snap.Mode == Idle {
			if snap.Artifact != artifact {
				t.Fatalf("artifact = %q, want %q", snap.Artifact, artifact)
			}
			return
		}
		if snap.Mode == Failed {
			t.Fatalf("stop after adoption failed: %s", snap.Err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("stop after adoption never reached idle")
}
func TestReplaySaveLeavesUnrelatedFiles(t *testing.T) {
	r := testRecorder(t, map[string]any{"replay_enabled": true}, "hang")
	r.ToggleReplay("DP-1")
	waitMode(t, r, ReplayActive)
	other := filepath.Join(r.cfg.Directory, "A-download.mp4")
	if err := os.WriteFile(other, []byte("user-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.SaveReplay()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := r.Snapshot()
		if snap.Artifact != "" || snap.Mode == Failed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := r.Snapshot().Artifact
	if got == "" || got == other {
		t.Fatalf("claimed %q err=%s", got, r.Snapshot().Err)
	}
	body, err := os.ReadFile(other)
	if err != nil || string(body) != "user-file" {
		t.Fatalf("other file = %q err=%v", body, err)
	}
	claimed, err := os.ReadFile(got)
	if err != nil || string(claimed) != "mp4" {
		t.Fatalf("claimed body %q err=%v", claimed, err)
	}
}

func TestClaimReplayIgnoresOtherNewFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	other := filepath.Join(dir, "A-download.mp4")
	if err := os.WriteFile(other, []byte("user-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	replay := filepath.Join(dir, "Replay_2026-10-04_12-00-00.mp4")
	if err := os.WriteFile(replay, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "replay_20261004_120000.mp4")
	got, err := claimReplay(dir, []byte(replay+"\n"), time.Now().Add(-time.Second), dest)
	if err != nil {
		t.Fatal(err)
	}
	if got != dest {
		t.Fatalf("claimed %s", got)
	}
	body, err := os.ReadFile(other)
	if err != nil || string(body) != "user-file" {
		t.Fatalf("other file = %q err=%v", body, err)
	}
	if _, err := os.Stat(replay); !os.IsNotExist(err) {
		t.Fatal("replay source still present")
	}
	claimed, err := os.ReadFile(dest)
	if err != nil || string(claimed) != "mp4" {
		t.Fatalf("dest = %q err=%v", claimed, err)
	}
}

func TestClaimReplayRejectsForeignAndStaleFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	other := filepath.Join(dir, "A-download.mp4")
	if err := os.WriteFile(other, []byte("user-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "Replay_2026-10-04_12-00-00.mp4")
	if err := os.WriteFile(outside, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "Replay_2026-10-04_11-00-00.mp4")
	if err := os.WriteFile(stale, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "Replay_2026-10-04_12-00-01.mp4")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "replay_out.mp4")
	signaled := time.Now()
	for _, logs := range []string{
		other + "\n",
		outside + "\n",
		stale + "\n",
		empty + "\n",
		filepath.Join(dir, "Replay_2026-10-04_12-00-00.mp4"),
		"gsr error: Failed to save replay\n",
	} {
		if _, err := claimReplay(dir, []byte(logs), signaled, dest); err == nil {
			t.Fatalf("claimed %q", logs)
		}
	}
	body, err := os.ReadFile(other)
	if err != nil || string(body) != "user-file" {
		t.Fatalf("other file = %q err=%v", body, err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dest was created")
	}
}

func TestReplaySaveAfterLogBufferSaturates(t *testing.T) {
	r := testRecorder(t, map[string]any{"replay_enabled": true}, "flood-ready")
	r.ToggleReplay("DP-1")
	waitMode(t, r, ReplayActive)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !containsReady(r.proc.Logs()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !containsReady(r.proc.Logs()) {
		t.Fatal("fake recorder never became ready")
	}
	if got := len(r.proc.Logs()); got != maxLogBytes {
		t.Fatalf("log buffer = %d, want %d", got, maxLogBytes)
	}
	r.SaveReplay()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.Snapshot().Artifact != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.Snapshot().Artifact; got == "" {
		t.Fatalf("replay save after log saturation failed: %s", r.Snapshot().Err)
	}
	r.ToggleReplay("DP-1")
	waitMode(t, r, Idle)
}
