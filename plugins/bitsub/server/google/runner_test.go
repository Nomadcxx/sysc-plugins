package google

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stubGog(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gog")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecRunnerArgvStdinAndErrors(t *testing.T) {
	r := ExecRunner{Bin: stubGog(t, `printf '%s|' "$@"; cat`), Account: "me@example.com"}
	out, err := r.RunStdin(ctx, "body; $(rm -rf /)", "gmail", "send", "--subject", "a b")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "--account|me@example.com|--json|--no-input|gmail|send|--subject|a b|body; $(rm -rf /)" {
		t.Fatalf("%q", out)
	}
	fail := ExecRunner{Bin: stubGog(t, `echo "insufficient scope" >&2; exit 4`), Account: "a"}
	if _, err := fail.Run(ctx, "calendar", "events"); err == nil || !strings.Contains(err.Error(), "insufficient scope") ||
		!strings.Contains(err.Error(), "calendar events") {
		t.Fatalf("%v", err)
	}
	slow := ExecRunner{Bin: stubGog(t, `sleep 5`), Account: "a", Timeout: 100 * time.Millisecond}
	start := time.Now()
	if _, err := slow.Run(ctx, "calendar", "events"); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout not enforced: %v after %v", err, time.Since(start))
	}
	missing := ExecRunner{Bin: filepath.Join(t.TempDir(), "absent"), Account: "a"}
	if _, err := missing.Run(ctx, "calendar", "events"); err == nil {
		t.Fatal("missing binary succeeded")
	}
}
