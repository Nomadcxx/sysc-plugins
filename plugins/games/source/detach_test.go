package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// slowProgram writes a script that outlives Detach: it sleeps, then leaves a
// marker, the way Lutris outlives the click that launched a game.
func slowProgram(t *testing.T) (script, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran")
	script = filepath.Join(dir, "game")
	body := "#!/bin/sh\nsleep 1\ntouch " + marker + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func checkDetached(t *testing.T) {
	t.Helper()
	script, marker := slowProgram(t)
	start := time.Now()
	if err := Detach(context.Background(), script); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if took := time.Since(start); took > 900*time.Millisecond {
		t.Fatalf("Detach waited %v for the program; it must return while the program runs", took)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the detached program never ran to completion")
}

func TestDetachReturnsWhileTheProgramRuns(t *testing.T) {
	checkDetached(t)
}

func TestDetachFallsBackWithoutSystemdRun(t *testing.T) {
	old := scopeRunner
	scopeRunner = filepath.Join(t.TempDir(), "no-systemd-run")
	t.Cleanup(func() { scopeRunner = old })
	checkDetached(t)
}

func TestDetachReportsAMissingProgram(t *testing.T) {
	if err := Detach(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a program that does not exist must be an error")
	}
}
