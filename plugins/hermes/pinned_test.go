package hermes

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func pinnedSizes() [][2]int {
	return [][2]int{{pinnedWidth, pinnedHeight}, {400, 480}}
}

func pinnedState(approval bool) PanelState {
	p := heavyPanel(StatusIdle, SectionNone, approval)
	p.Pinned = true
	return p
}

// pinnedHost answers the two host calls a pinned surface makes.
func pinnedHost(t *testing.T, found bool, value string) (*v1.Client, <-chan *v1.HostCall, func()) {
	t.Helper()
	return fakeHost(t, func(call *v1.HostCall) *v1.HostReply {
		reply := &v1.HostReply{ID: call.ID, OK: true}
		switch call.Call {
		case v1.CallStateGet:
			result, err := json.Marshal(v1.StateGetResult{Found: found, Value: json.RawMessage(value)})
			if err != nil {
				t.Errorf("state.get result: %v", err)
			}
			reply.Result = result
		case v1.CallSurfaceOpen:
			result, err := json.Marshal(v1.SurfaceResult{ViewID: "view-pinned"})
			if err != nil {
				t.Errorf("surface.open result: %v", err)
			}
			reply.Result = result
		}
		return reply
	})
}

func nextCall(t *testing.T, calls <-chan *v1.HostCall) *v1.HostCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("no host call")
		return nil
	}
}

func noCall(t *testing.T, calls <-chan *v1.HostCall) {
	t.Helper()
	select {
	case call := <-calls:
		t.Errorf("unexpected %s %s", call.Call, call.Params)
	case <-time.After(50 * time.Millisecond):
	}
}

func surfaceParams(t *testing.T, call *v1.HostCall) v1.SurfaceOpenParams {
	t.Helper()
	var p v1.SurfaceOpenParams
	if call.Call != v1.CallSurfaceOpen || json.Unmarshal(call.Params, &p) != nil {
		t.Fatalf("host saw %s %s, want surface.open", call.Call, call.Params)
	}
	return p
}

func pinnedFlag(t *testing.T, call *v1.HostCall) bool {
	t.Helper()
	if call.Call != v1.CallStateSet {
		t.Fatalf("host saw %s %s, want state.set", call.Call, call.Params)
	}
	var p v1.StateSetParams
	if err := json.Unmarshal(call.Params, &p); err != nil {
		t.Fatalf("state.set params: %v", err)
	}
	var pinned bool
	if p.Key != pinnedStateKey || json.Unmarshal(p.Value, &pinned) != nil {
		t.Fatalf("state.set key %q value %s, want %q bool", p.Key, p.Value, pinnedStateKey)
	}
	return pinned
}

func TestPinnedTreeReusesTheChatPanelBuilders(t *testing.T) {
	for _, size := range pinnedSizes() {
		w, h := size[0], size[1]
		p := pinnedState(true)
		tree := PinnedTree(p, w, h)
		lintView(t, "pinned", tree, v1.ViewFloating, w, h)
		if tree.Width != w || tree.Height != h || tree.Padding != panelPad || tree.Gap != panelGap {
			t.Errorf("%dx%d: root = %+v", w, h, tree)
		}
		for _, key := range []string{"header", "approval", "transcript", "composer-row"} {
			if findKey(tree, key) == nil {
				t.Errorf("%dx%d: no %s", w, h, key)
			}
		}
		for _, id := range []string{"approval-preview:deny", "approval-preview:allow", "approval-preview:allow-session", "composer", "send"} {
			if findID(tree, id) == nil {
				t.Errorf("%dx%d: no %s", w, h, id)
			}
		}
		panel := PanelTree(p, w, h)
		if got, want := findKey(tree, "header"), findKey(panel, "header"); !reflect.DeepEqual(got, want) {
			t.Errorf("%dx%d: pinned header is a copy, not the panel's:\n%+v\n%+v", w, h, got, want)
		}
		if got, want := findKey(tree, "composer-row"), findKey(panel, "composer-row"); !reflect.DeepEqual(got, want) {
			t.Errorf("%dx%d: pinned composer is a copy, not the panel's", w, h)
		}
		if findKey(tree, "transcript").Height <= 0 {
			t.Errorf("%dx%d: transcript has no room", w, h)
		}
		walk(tree, func(n *v1.Node) {
			if strings.HasPrefix(n.Fill, "note-") {
				t.Errorf("%dx%d: pinned tree uses floating note fill %q", w, h, n.Fill)
			}
		})
	}
}

func TestPinnedTreeWithoutApprovalHidesTheCard(t *testing.T) {
	p := pinnedState(false)
	tree := PinnedTree(p, pinnedWidth, pinnedHeight)
	lintView(t, "pinned without approval", tree, v1.ViewFloating, pinnedWidth, pinnedHeight)
	if findKey(tree, "approval") != nil {
		t.Error("no pending approval should mean no card")
	}
	if findKey(tree, "transcript") == nil || findKey(tree, "composer-row") == nil {
		t.Error("the transcript and composer stay")
	}
}

func TestPinnedSurfaceUsesTheHeaderAndComposerRoutes(t *testing.T) {
	p := pinnedState(true)
	tree := PinnedTree(p, pinnedWidth, pinnedHeight)
	composer := findID(tree, "composer")
	if composer == nil || !composer.SubmitOnEnter || composer.Key != "composer" {
		t.Fatalf("composer = %+v", composer)
	}
	plan, err := SendPlan(p.SessionID, "Run the tests", p.Composer.OneShot)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Path != "/prompt" {
		t.Errorf("send plan = %+v", plan)
	}
	for _, id := range []string{"approval-preview:deny", "approval-preview:allow", "approval-preview:allow-session"} {
		if findID(tree, id) == nil {
			t.Errorf("no %s on the pinned surface", id)
		}
	}
	if got := findID(tree, "pin"); got == nil || !strings.Contains(got.Name, "Unpin") {
		t.Errorf("pin button on a pinned surface = %+v", got)
	}
}

func TestPinOpensTheSurfaceAndPersistsTheState(t *testing.T) {
	c, calls, stop := pinnedHost(t, false, "")
	defer stop()
	pin := NewPinnedSurface(c)
	if pin.Pinned() {
		t.Fatal("a new surface starts unpinned")
	}
	if err := pin.Pin(context.Background()); err != nil {
		t.Fatal(err)
	}
	open := surfaceParams(t, nextCall(t, calls))
	if open.Key != pinnedKey || open.Title != pinnedTitle {
		t.Errorf("surface.open = %+v", open)
	}
	if open.Width != pinnedWidth || open.Height != pinnedHeight {
		t.Errorf("surface.open size = %dx%d", open.Width, open.Height)
	}
	if open.X != pinnedX || open.Y != pinnedY {
		t.Errorf("surface.open offset = %d,%d", open.X, open.Y)
	}
	if !pinnedFlag(t, nextCall(t, calls)) {
		t.Error("pin must persist true")
	}
	if !pin.Pinned() || pin.ViewID() != "view-pinned" {
		t.Errorf("pinned = %v, view = %q", pin.Pinned(), pin.ViewID())
	}
}

func TestUnpinClosesTheSurfaceAndPersistsTheState(t *testing.T) {
	c, calls, stop := pinnedHost(t, false, "")
	defer stop()
	pin := NewPinnedSurface(c)
	if err := pin.Pin(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextCall(t, calls)
	nextCall(t, calls)
	if err := pin.Unpin(context.Background()); err != nil {
		t.Fatal(err)
	}
	call := nextCall(t, calls)
	var close v1.SurfaceCloseParams
	if call.Call != v1.CallSurfaceClose || json.Unmarshal(call.Params, &close) != nil || close.View != "view-pinned" {
		t.Errorf("host saw %s %s, want surface.close view-pinned", call.Call, call.Params)
	}
	if pinnedFlag(t, nextCall(t, calls)) {
		t.Error("unpin must persist false")
	}
	if pin.Pinned() || pin.ViewID() != "" {
		t.Errorf("pinned = %v, view = %q", pin.Pinned(), pin.ViewID())
	}
}

func TestRestoreReopensOnlyAPinnedSurface(t *testing.T) {
	c, calls, stop := pinnedHost(t, true, "true")
	defer stop()
	pin := NewPinnedSurface(c)
	if err := pin.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	get := nextCall(t, calls)
	var params v1.StateGetParams
	if get.Call != v1.CallStateGet || json.Unmarshal(get.Params, &params) != nil || params.Key != pinnedStateKey {
		t.Fatalf("host saw %s %s, want state.get", get.Call, get.Params)
	}
	open := surfaceParams(t, nextCall(t, calls))
	if open.Key != pinnedKey || open.Width != pinnedWidth || open.Height != pinnedHeight {
		t.Errorf("restored surface.open = %+v", open)
	}
	if !pin.Pinned() {
		t.Error("restore must reopen pinned")
	}

	c2, calls2, stop2 := pinnedHost(t, true, "false")
	defer stop2()
	unpinned := NewPinnedSurface(c2)
	if err := unpinned.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextCall(t, calls2)
	noCall(t, calls2)
	if unpinned.Pinned() {
		t.Error("a false setting must not reopen the surface")
	}

	c3, calls3, stop3 := pinnedHost(t, false, "")
	defer stop3()
	absent := NewPinnedSurface(c3)
	if err := absent.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextCall(t, calls3)
	noCall(t, calls3)
	if absent.Pinned() {
		t.Error("a missing setting must not reopen the surface")
	}
}

func TestOpenFocusesThePinnedSurfaceOrOpensThePanel(t *testing.T) {
	c, calls, stop := pinnedHost(t, false, "")
	defer stop()
	pin := NewPinnedSurface(c)
	if err := pin.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	call := nextCall(t, calls)
	var panel v1.PanelParams
	if call.Call != v1.CallPanelOpen || json.Unmarshal(call.Params, &panel) != nil || panel.Entry != "panel" {
		t.Errorf("unpinned open saw %s %s, want panel.open panel", call.Call, call.Params)
	}
	if pin.Pinned() {
		t.Error("opening the chat panel must not pin")
	}

	if err := pin.Pin(context.Background()); err != nil {
		t.Fatal(err)
	}
	nextCall(t, calls)
	nextCall(t, calls)
	if err := pin.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	open := surfaceParams(t, nextCall(t, calls))
	if open.Key != pinnedKey {
		t.Errorf("pinned open = %+v", open)
	}
	noCall(t, calls)
}

func TestPinReportsARefusedHostCall(t *testing.T) {
	c, _, stop := fakeHost(t, func(call *v1.HostCall) *v1.HostReply {
		return &v1.HostReply{ID: call.ID, OK: false, Error: "no floating surfaces"}
	})
	defer stop()
	pin := NewPinnedSurface(c)
	if err := pin.Pin(context.Background()); err == nil || !strings.Contains(err.Error(), "no floating surfaces") {
		t.Errorf("refused pin err = %v", err)
	}
	if pin.Pinned() {
		t.Error("a refused surface.open must not leave the surface pinned")
	}
}

// TestPinRollbackClosesTheOpenedSurface covers qa-9 defect 3: when the pin
// state cannot be saved the surface opened by Pin must not be left behind.
func TestPinRollbackClosesTheOpenedSurface(t *testing.T) {
	c, calls, stop := fakeHost(t, func(call *v1.HostCall) *v1.HostReply {
		reply := &v1.HostReply{ID: call.ID, OK: true}
		switch call.Call {
		case v1.CallSurfaceOpen:
			result, _ := json.Marshal(v1.SurfaceResult{ViewID: "view-pinned"})
			reply.Result = result
		case v1.CallSurfaceClose:
			// The rollback close must succeed; the pin still fails.
		case v1.CallStateSet:
			reply.OK, reply.Error = false, "state store unavailable"
		}
		return reply
	})
	defer stop()
	p := NewPinnedSurface(c)
	if err := p.Pin(context.Background()); err == nil {
		t.Fatal("Pin succeeded although state.set failed")
	}
	var closed string
	for i := 0; i < 4; i++ {
		select {
		case call := <-calls:
			if call.Call == v1.CallSurfaceClose {
				var params v1.SurfaceCloseParams
				if err := json.Unmarshal(call.Params, &params); err != nil {
					t.Fatalf("surface.close params: %v", err)
				}
				closed = params.View
			}
		case <-time.After(time.Second):
			i = 4
		}
	}
	if closed != "view-pinned" {
		t.Fatalf("Pin rollback did not close the opened surface (closed %q)", closed)
	}
	if p.Pinned() || p.ViewID() != "" {
		t.Fatalf("Pin rollback left pinned=%v view=%q", p.Pinned(), p.ViewID())
	}
}

// TestPinnedGeometryIsTheDesignLiterals pins qa-9 defect 1: the surface is
// 640x720 at 32,32, and the test states the numbers rather than reading the
// constants it is checking.
func TestPinnedGeometryIsTheDesignLiterals(t *testing.T) {
	if pinnedWidth != 640 || pinnedHeight != 720 || pinnedX != 32 || pinnedY != 32 {
		t.Fatalf("pinned geometry constants = %dx%d at %d,%d, want 640x720 at 32,32",
			pinnedWidth, pinnedHeight, pinnedX, pinnedY)
	}
	c, calls, stop := pinnedHost(t, false, "")
	defer stop()
	if err := NewPinnedSurface(c).Pin(context.Background()); err != nil {
		t.Fatal(err)
	}
	params := surfaceParams(t, nextCall(t, calls))
	if params.Width != 640 || params.Height != 720 || params.X != 32 || params.Y != 32 {
		t.Fatalf("surface.open = %dx%d at %d,%d, want 640x720 at 32,32",
			params.Width, params.Height, params.X, params.Y)
	}
}

// TestPinnedTreeAlwaysOffersUnpin pins qa-9 defect 2: PinnedTree sets
// Pinned itself, so an unpinned input still renders the Unpin action.
func TestPinnedTreeAlwaysOffersUnpin(t *testing.T) {
	p := heavyPanel(StatusIdle, SectionNone, false)
	p.Pinned = false
	var pin *v1.Node
	walk(PinnedTree(p, 640, 720), func(n *v1.Node) {
		if n.ID == "pin" {
			pin = n
		}
	})
	if pin == nil {
		t.Fatal("pinned tree has no pin control")
	}
	if pin.Text != "Unpin" {
		t.Fatalf("unpinned input rendered pin %q, want Unpin", pin.Text)
	}
}
