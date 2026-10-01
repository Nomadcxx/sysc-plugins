package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/notes"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestInvalidNotesFolderLeavesManagerRecoverable(t *testing.T) {
	sess := applySettings(nil, map[string]any{"notes_dir": "\x00"})
	if sess == nil {
		t.Fatal("invalid folder prevented Notes from creating a recoverable session")
	}
	if got := sess.Snap().ScanError; !strings.Contains(got, "Could not open notes folder") {
		t.Fatalf("folder error = %q", got)
	}
	sess = applySettings(sess, map[string]any{"notes_dir": t.TempDir()})
	if got := sess.Snap().ScanError; got != "" {
		t.Fatalf("successful folder recovery left error %q", got)
	}
}

func TestPinnedSurfacesAreScopedByNoteAndOutput(t *testing.T) {
	pins := []pinnedSurface{
		{Name: "Plan.md", Output: "DP-1"},
		{Name: "Plan.md", Output: "DP-2"},
		{Name: "Inbox.md", Output: "DP-1"},
	}
	if !isPinned(pins, "Plan.md", "DP-2") {
		t.Fatal("second output pin was lost")
	}
	got := setPinned(pins, "Plan.md", "DP-1", false)
	if isPinned(got, "Plan.md", "DP-1") || !isPinned(got, "Plan.md", "DP-2") || !isPinned(got, "Inbox.md", "DP-1") {
		t.Fatalf("unpin changed unrelated surfaces: %+v", got)
	}
}

func TestInvalidStickyColorFallsBackToThemePalette(t *testing.T) {
	for _, color := range []string{"sun", "mint", "sky", "rose", "lilac"} {
		if got := validColor(color); got != color {
			t.Errorf("validColor(%q) = %q", color, got)
		}
	}
	if got := validColor("#ff00ff"); got != "sun" {
		t.Fatalf("arbitrary color = %q, want the safe default", got)
	}
}

func newSessionForTest(t *testing.T) (*notes.Session, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := notes.Open(dir, "md")
	if err != nil {
		t.Fatal(err)
	}
	return notes.NewSession(st, time.Now), dir
}

func TestCascadeStepsAndWraps(t *testing.T) {
	views := map[string]view{}
	for i := 0; i < 9; i++ {
		x, y, slot := cascade(views, "DP-1")
		step := (i % 8) * 28
		if x != 56+step || y != 92+step {
			t.Fatalf("sticky %d at %d,%d", i, x, y)
		}
		views[string(rune('a'+i))] = view{kind: v1.ViewFloating, output: "DP-1", slot: slot, hasSlot: true}
	}
	if x, _, _ := cascade(views, "HDMI-A-1"); x != 56 {
		t.Fatal("cascade is per output")
	}
}

func TestPublisherSkipsUnchangedTrees(t *testing.T) {
	p := publisher{}
	if !p.changed("v1", notes.BarTree()) || p.changed("v1", notes.BarTree()) {
		t.Fatal("first publish must send and an identical one must not")
	}
	p.forget("v1")
	if !p.changed("v1", notes.BarTree()) {
		t.Fatal("a forgotten view must publish again")
	}
}

func TestStickyAndPanelShareOneDocument(t *testing.T) {
	sess, dir := newSessionForTest(t)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.Select("a.md"); err != nil {
		t.Fatal(err)
	}
	if err := sess.TypeNote("a.md", "from sticky"); err != nil {
		t.Fatal(err)
	}
	sess.Tick()
	if snap := sess.Snap(); snap.Body != "from sticky" {
		t.Fatalf("panel sees %q", snap.Body)
	}
}

// fakeHost is a client whose host answers every call with an empty OK.
func fakeHost(t *testing.T) *v1.Client {
	t.Helper()
	toPlugin, hostOut := io.Pipe()
	hostIn, fromPlugin := io.Pipe()
	c := v1.NewClient(toPlugin, fromPlugin)
	go func() {
		dec := json.NewDecoder(hostIn)
		enc := json.NewEncoder(hostOut)
		for {
			var call struct {
				ID string `json:"id"`
			}
			if err := dec.Decode(&call); err != nil {
				return
			}
			_ = enc.Encode(map[string]any{"type": v1.TypeHostReply, "id": call.ID, "ok": true})
		}
	}()
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { hostOut.Close(); fromPlugin.Close() })
	return c
}

func TestDeleteForgetsTheClosedStickies(t *testing.T) {
	c := fakeHost(t)
	views := map[string]view{"s1": {kind: v1.ViewFloating, name: "a.md"}, "p1": {kind: v1.ViewPanel}}
	var pins []pinnedSurface
	if err := closeDeleted(context.Background(), c, "a.md", views, &pins, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := views["s1"]; ok {
		t.Fatal("a closed sticky stays in views, so the next snapshot reports the deleted file as an error")
	}
}

func TestRetargetStickiesFollowsARename(t *testing.T) {
	views := map[string]view{"s1": {kind: v1.ViewFloating, name: "a.md"}, "s2": {kind: v1.ViewFloating, name: "b.md"}}
	retargetStickies(views, "a.md", "alpha.md")
	if views["s1"].name != "alpha.md" || views["s2"].name != "b.md" {
		t.Fatalf("views = %+v", views)
	}
}

// stateHost answers state.get and state.set from a map, and every other call
// with a bare ok.
func stateHost(t *testing.T) *v1.Client {
	t.Helper()
	toPlugin, hostOut := io.Pipe()
	hostIn, fromPlugin := io.Pipe()
	c := v1.NewClient(toPlugin, fromPlugin)
	store := map[string]json.RawMessage{}
	go func() {
		dec := json.NewDecoder(hostIn)
		enc := json.NewEncoder(hostOut)
		for {
			var call v1.HostCall
			if err := dec.Decode(&call); err != nil {
				return
			}
			reply := map[string]any{"type": v1.TypeHostReply, "id": call.ID, "ok": true}
			switch call.Call {
			case v1.CallStateGet:
				var p v1.StateGetParams
				_ = json.Unmarshal(call.Params, &p)
				v, ok := store[p.Key]
				reply["result"] = v1.StateGetResult{Found: ok, Value: v}
			case v1.CallStateSet:
				var p v1.StateSetParams
				_ = json.Unmarshal(call.Params, &p)
				if string(p.Value) == "null" || len(p.Value) == 0 {
					delete(store, p.Key)
				} else {
					store[p.Key] = p.Value
				}
			}
			_ = enc.Encode(reply)
		}
	}()
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { hostOut.Close(); fromPlugin.Close() })
	return c
}

// The shell keeps a sticky's position and size under its surface key, so a
// renamed note's sticky must reopen under the key it already had.
func TestRenameKeepsTheStickySurfaceKey(t *testing.T) {
	ctx := context.Background()
	c := stateHost(t)
	var pins []pinnedSurface
	loadPins := func() error { return nil }
	before, err := stickyKey(ctx, c, "a.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateNoteState(ctx, c, "a.md", "alpha.md", &pins, loadPins); err != nil {
		t.Fatal(err)
	}
	if err := migrateNoteState(ctx, c, "alpha.md", "omega.md", &pins, loadPins); err != nil {
		t.Fatal(err)
	}
	after, err := stickyKey(ctx, c, "omega.md")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("key after two renames = %q, want the original %q", after, before)
	}
	// A new note under the old name must not share the renamed sticky's
	// geometry.
	if fresh, _ := stickyKey(ctx, c, "a.md"); fresh == before {
		t.Fatalf("a new a.md reuses the renamed note's key %q", fresh)
	}
}

// Closing a sticky frees its place: the next one goes there instead of onto
// a sticky that is still open.
func TestCascadeReusesTheFreedSlot(t *testing.T) {
	views := map[string]view{}
	for _, id := range []string{"a", "b", "c"} {
		_, _, slot := cascade(views, "DP-1")
		views[id] = view{kind: v1.ViewFloating, output: "DP-1", slot: slot, hasSlot: true}
	}
	delete(views, "b")
	x, y, slot := cascade(views, "DP-1")
	if slot != 1 || x != 56+28 || y != 92+28 {
		t.Fatalf("next sticky at slot %d (%d,%d), want slot 1 where b was", slot, x, y)
	}
}
