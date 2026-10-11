package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
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

func TestPanelOpenReturnsWhileHostReplyIsHeld(t *testing.T) {
	originalDefault := hostcall.Default
	hostcall.Default = 5 * time.Second
	defer func() { hostcall.Default = originalDefault }()

	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()
	c := v1.NewClient(toPlugin, pluginOut)
	calls := make(chan *v1.HostCall, 1)
	go func() {
		msg, err := v1.NewDecoder(hostIn, v1.ToHost).Decode()
		if err == nil {
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess, _ := newSessionForTest(t)
	results := make(chan notesActionResult, 1)
	done := make(chan struct{})
	go func() {
		handlePanel(ctx, c, sess, &v1.InputEvent{Node: "open"}, nil, nil, results)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("panel open waited for the host reply")
	}
	select {
	case call := <-calls:
		if call.Call != v1.CallPanelOpen {
			t.Fatalf("call = %q, want panel.open", call.Call)
		}
	case <-time.After(time.Second):
		t.Fatal("panel.open was not sent")
	}
}

func TestClipboardImportReturnsWhileHostReplyIsHeld(t *testing.T) {
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()
	c := v1.NewClient(toPlugin, pluginOut)
	go func() {
		for {
			if _, err := c.Recv(); err != nil {
				return
			}
		}
	}()
	calls := make(chan *v1.HostCall, 1)
	go func() {
		msg, err := v1.NewDecoder(hostIn, v1.ToHost).Decode()
		if err == nil {
			if call, ok := msg.(*v1.HostCall); ok {
				calls <- call
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess, _ := newSessionForTest(t)
	results := make(chan notesActionResult, 1)
	done := make(chan struct{})
	go func() {
		handlePanel(ctx, c, sess, &v1.InputEvent{Node: "clipboard-import"}, nil, nil, results)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("clipboard import waited for the host reply")
	}
	var call *v1.HostCall
	select {
	case call = <-calls:
		if call.Call != v1.CallClipboardRead {
			t.Fatalf("call = %q, want clipboard.read", call.Call)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard.read was not sent")
	}
	if err := v1.NewEncoder(hostOut).Encode(&v1.HostReply{ID: call.ID, OK: true, Result: json.RawMessage(`{"text":"from clipboard"}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.kind != "clipboard-import" || result.err != nil || result.clipboard.Text != "from clipboard" {
			t.Fatalf("clipboard result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard reply was not returned to the owner")
	}
}

func TestStickyPinReturnsWhileStateReplyIsHeld(t *testing.T) {
	toPlugin, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	defer toPlugin.Close()
	defer hostOut.Close()
	defer hostIn.Close()
	defer pluginOut.Close()
	c := v1.NewClient(toPlugin, pluginOut)
	jobs := make(chan notesIOJob, 1)
	submit := func(job notesIOJob) error { jobs <- job; return nil }
	sess, _ := newSessionForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		handleSticky(ctx, c, sess, &v1.InputEvent{ViewID: "sticky", Node: "surface-pin"},
			view{kind: v1.ViewFloating, name: "a.md", output: "DP-1"}, map[string]view{}, map[string]bool{}, submit)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("surface-pin waited for state.get")
	}

	workerDone := make(chan struct{})
	go func() {
		_ = (<-jobs)(ctx)
		close(workerDone)
	}()
	callCh := make(chan *v1.HostCall, 1)
	go func() {
		msg, err := v1.NewDecoder(hostIn, v1.ToHost).Decode()
		if err == nil {
			if call, ok := msg.(*v1.HostCall); ok {
				callCh <- call
			}
		}
	}()
	select {
	case call := <-callCh:
		if call.Call != v1.CallStateGet {
			t.Fatalf("first sticky pin call = %q, want state.get", call.Call)
		}
	case <-time.After(time.Second):
		t.Fatal("sticky pin did not start its state read")
	}
	select {
	case <-workerDone:
		t.Fatal("worker unexpectedly finished without a host reply")
	default:
	}
	cancel()
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("canceled sticky pin worker did not stop")
	}
}

func TestActionWorkerDrainsAcceptedJobOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan notesIOJob, 1)
	results := make(chan notesActionResult)
	done := startNotesActionWorker(ctx, jobs, results)
	started := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})
	jobs <- func(context.Context) notesActionResult {
		close(started)
		<-release
		close(completed)
		return notesActionResult{}
	}
	<-started
	stopped := make(chan struct{})
	go func() {
		drainNotesActionWorker(jobs, results, done, cancel)
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("shutdown returned while an accepted host action was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the accepted action completed")
	}
	select {
	case <-completed:
	default:
		t.Fatal("accepted action was not completed before shutdown")
	}
}

func TestDeleteQueueFullKeepsTheNoteOnDisk(t *testing.T) {
	sess, dir := newSessionForTest(t)
	path := filepath.Join(dir, "a.md")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sess.Select("a.md"); err != nil {
		t.Fatal(err)
	}
	sess.ProposeDelete("a.md")
	queueFull := errors.New("Notes is busy; try again")
	jobs := make(chan notesIOJob, 1)
	jobs <- func(context.Context) notesActionResult { return notesActionResult{} }
	submit := func(job notesIOJob) error {
		select {
		case jobs <- job:
			return nil
		default:
			return queueFull
		}
	}
	err := submitNotesDelete(submit, sess.ConfirmDeleteName, func(context.Context, string) notesActionResult {
		t.Fatal("delete cleanup ran even though the queue was full")
		return notesActionResult{}
	})
	if !errors.Is(err, queueFull) {
		t.Fatalf("delete error = %v, want queue-full error", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("note was deleted before cleanup could be queued: %v", err)
	}
	if got := sess.Snap().PendingDelete; got != "a.md" {
		t.Fatalf("pending delete = %q, want it retained for retry", got)
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
