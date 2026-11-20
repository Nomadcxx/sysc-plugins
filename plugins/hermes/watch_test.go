package hermes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitChange waits for the watcher to report path.
func waitChange(t *testing.T, w *Watcher, path string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-w.Changed():
			for _, p := range w.Drain() {
				if p == path {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no change reported for %s", path)
		}
	}
}

func replaceFile(t *testing.T, path, data string) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherReportsWritesAndAtomicReplaces(t *testing.T) {
	for _, mode := range []struct {
		name  string
		force bool
	}{{"inotify", false}, {"stat-poll", true}} {
		t.Run(mode.name, func(t *testing.T) {
			dir := t.TempDir()
			state := filepath.Join(dir, "state.json")
			token := filepath.Join(dir, "bridge.token")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := WatchFiles(ctx, []string{state, token}, 20*time.Millisecond, mode.force)
			if w.Polling() != mode.force {
				t.Fatalf("polling = %v, want %v", w.Polling(), mode.force)
			}
			// Created after the watch started, by rename (the bridge's
			// tmp.replace).
			replaceFile(t, state, `{"bridge":{"status":"online"}}`)
			waitChange(t, w, state)
			// Rewritten in place.
			if err := os.WriteFile(token, []byte("tok-1"), 0o600); err != nil {
				t.Fatal(err)
			}
			waitChange(t, w, token)
			replaceFile(t, state, `{"bridge":{"status":"online"},"updated_at":2}`)
			waitChange(t, w, state)
			// Deleted.
			if err := os.Remove(token); err != nil {
				t.Fatal(err)
			}
			waitChange(t, w, token)
		})
	}
}

func TestWatcherFallsBackToPollingWithoutTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet")
	state := filepath.Join(dir, "state.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := WatchFiles(ctx, []string{state}, 20*time.Millisecond, false)
	if !w.Polling() {
		t.Fatal("watcher on a missing directory did not fall back to the stat poll")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	replaceFile(t, state, `{}`)
	waitChange(t, w, state)
}

func TestWatcherStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := WatchFiles(ctx, []string{filepath.Join(t.TempDir(), "state.json")}, 20*time.Millisecond, false)
	cancel()
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("watcher still running after cancel")
	}
}

func TestReadStateFileNormalizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var msgs []string
	for i := 0; i < MaxMessages+5; i++ {
		msgs = append(msgs, fmt.Sprintf(`{"role":"user","text":"m%d\u001b[31m"}`, i))
	}
	replaceFile(t, path, `{"bridge":{"status":"online"},"messages":[`+strings.Join(msgs, ",")+`]}`)

	s, err := ReadStateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Bridge.Status != "online" || len(s.Messages) != MaxMessages {
		t.Fatalf("state not normalized: status %q, %d messages", s.Bridge.Status, len(s.Messages))
	}
	if last := s.Messages[len(s.Messages)-1]; last.Text != fmt.Sprintf("m%d", MaxMessages+4) || last.ID == "" {
		t.Fatalf("newest message = %+v, want sanitized m%d with an id", last, MaxMessages+4)
	}
	if s.Sessions == nil || s.Events == nil {
		t.Fatal("Normalize did not fill empty lists")
	}
}

func TestReadStateFileFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadStateFile(filepath.Join(dir, "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: err = %v, want not-exist", err)
	}
	for name, data := range map[string]string{
		"corrupt":   `{"bridge":`,
		"not-json":  "hello",
		"array":     `[1,2]`,
		"oversized": `{"x":"` + strings.Repeat("a", MaxStateFileBytes) + `"}`,
	} {
		path := filepath.Join(dir, name+".json")
		replaceFile(t, path, data)
		if _, err := ReadStateFile(path); !errors.Is(err, ErrInvalidStateFile) {
			t.Errorf("%s: err = %v, want ErrInvalidStateFile", name, err)
		}
	}
	if ErrInvalidStateFile.Error() != "Invalid state file" {
		t.Fatalf("message = %q", ErrInvalidStateFile.Error())
	}
	if _, err := ReadStateFile(dir); !errors.Is(err, ErrInvalidStateFile) {
		t.Fatalf("directory: err = %v, want ErrInvalidStateFile", err)
	}
}
