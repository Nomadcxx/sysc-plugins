package hostcall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestCallTimesOutWhenReplyNeverArrives(t *testing.T) {
	orig := Default
	Default = 100 * time.Millisecond
	defer func() { Default = orig }()

	// The host side of the pipe never delivers a reply; the writer is a plain
	// buffer so the request encoding itself cannot block.
	requests, hostSide := io.Pipe()
	defer requests.Close()
	defer hostSide.Close()
	_ = hostSide
	c := v1.NewClient(requests, &bytes.Buffer{})

	start := time.Now()
	_, err := Call(context.Background(), c, v1.CallStateGet, nil)
	if err == nil {
		t.Fatal("Call returned nil error with no reply coming")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Call took %s, want about %s", elapsed, Default)
	}
}

// The event loop of these plugins must never make an unbounded host call; the
// reader that decodes replies can be stuck behind a full incoming queue while
// the loop waits.
var loopCallPlugins = []string{
	"sysc-plugin-timer",
	"sysc-plugin-kdeconnect",
	"sysc-plugin-screen-recorder",
	"sysc-plugin-notes",
	"sysc-plugin-aiusage",
	"sysc-plugin-mini-docker",
	"sysc-plugin-github-notifications",
}

func TestEventLoopCallsGoThroughHelper(t *testing.T) {
	for _, plugin := range loopCallPlugins {
		path := filepath.Join("..", "..", "cmd", plugin, "main.go")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(src), "c.Call(ctx,") {
			t.Errorf("%s still makes an unbounded host call from the event loop", plugin)
		}
	}
}
