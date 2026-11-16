package hermes

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// pendingApproval is one approval as the bridge shows it, with the request
// identity Task 3a publishes.
func pendingApproval() *Approval {
	return &Approval{
		Pending:   true,
		ToolName:  "terminal",
		Message:   "rm -rf build?",
		Request:   json.RawMessage(`{"command":"rm -rf build","id":"req-1"}`),
		RequestID: "req-1",
	}
}

// approvalState is the state whose signature the buttons carry.
func approvalState() State {
	st := State{Session: SessionInfo{ID: "s1"}, Approval: *pendingApproval()}
	st.Normalize()
	return st
}

func TestApprovalButtonsMapOnlyHonoredChoices(t *testing.T) {
	st := approvalState()
	buttons := ApprovalButtons(st)
	if len(buttons) != 3 {
		t.Fatalf("got %d buttons, want 3", len(buttons))
	}
	wantLabels := []string{"Deny", "Allow", "Allow Session"}
	wantActions := []string{ApprovalDeny, ApprovalAllow, ApprovalAllowSession}
	wantChoices := []string{"deny", "once", "session"}
	wantAll := []bool{false, false, false}

	sig := ApprovalSig(st)
	for i, b := range buttons {
		if b.Label != wantLabels[i] {
			t.Errorf("button %d label = %q, want %q", i, b.Label, wantLabels[i])
		}
		wantID := "approval:" + sig + ":" + wantActions[i]
		if b.ID != wantID {
			t.Errorf("button %d id = %q, want %q", i, b.ID, wantID)
		}
		actionSig, action, ok := parseApprovalAction(b.ID)
		if !ok || actionSig != sig || action != wantActions[i] {
			t.Errorf("button %d id %q does not parse back", i, b.ID)
		}
		body := approvalBody(action, st)
		if body["choice"] != wantChoices[i] {
			t.Errorf("button %d choice = %v, want %q", i, body["choice"], wantChoices[i])
		}
		if body["all"] != wantAll[i] {
			t.Errorf("button %d all = %v, want false", i, body["all"])
		}
		if body["request_id"] != "req-1" {
			t.Errorf("button %d request_id = %v, want req-1", i, body["request_id"])
		}
	}

	if got := ApprovalButtons(State{}); got != nil {
		t.Fatalf("no pending approval returned %v buttons", len(got))
	}
}

func TestApprovalBodyNeverCarriesAForbiddenChoice(t *testing.T) {
	st := approvalState()
	forbidden := map[string]bool{"denied": true, "approved": true, "approve": true, "": true}
	actions := []string{ApprovalAllow, ApprovalAllowSession, ApprovalDeny, "denied", "approved", "approve", "", "allow-all"}
	for _, action := range actions {
		body := approvalBody(action, st)
		choice, ok := body["choice"].(string)
		if !ok || forbidden[choice] {
			t.Fatalf("action %q produced choice %v", action, body["choice"])
		}
		if body["all"] != false {
			t.Fatalf("action %q produced all = %v", action, body["all"])
		}
	}
	// Anything unmapped is a refusal, never a grant.
	for _, action := range []string{"denied", "approved", "approve", "", "allow-all"} {
		if got := approvalBody(action, st)["choice"]; got != "deny" {
			t.Fatalf("unmapped action %q choice = %v, want deny", action, got)
		}
	}
	// The body never uses the stock bridge's older spellings.
	encoded, _ := json.Marshal(approvalBody(ApprovalAllow, st))
	for _, word := range []string{"denied", "approved", "approve"} {
		if strings.Contains(string(encoded), word) {
			t.Fatalf("body %s contains %q", encoded, word)
		}
	}
}

func TestApprovalBodyOmitsRequestIDWhenStateHasNone(t *testing.T) {
	st := approvalState()
	st.Approval.RequestID = ""
	body := approvalBody(ApprovalAllow, st)
	if _, ok := body["request_id"]; ok {
		t.Fatalf("stock bridge body carries request_id: %v", body)
	}
}

func TestApprovalSig(t *testing.T) {
	st := approvalState()
	sig := ApprovalSig(st)
	if len(sig) != 12 {
		t.Fatalf("sig %q is not 12 hex digits", sig)
	}
	for _, c := range sig {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("sig %q is not lowercase hex", sig)
		}
	}
	if again := ApprovalSig(st); again != sig {
		t.Fatalf("sig is not stable: %q then %q", sig, again)
	}

	// The same request re-encoded with a different key order is the same
	// request: canonical JSON keeps the signature.
	reordered := approvalState()
	reordered.Approval.Request = json.RawMessage(`{ "id": "req-1", "command": "rm -rf build" }`)
	if ApprovalSig(reordered) != sig {
		t.Fatal("key order changed the signature")
	}

	changes := []func(*State){
		func(s *State) { s.Session.ID = "s2" },
		func(s *State) { s.Approval.ToolName = "shell" },
		func(s *State) { s.Approval.Message = "rm -rf src?" },
		func(s *State) { s.Approval.Request = json.RawMessage(`{"command":"rm -rf src","id":"req-1"}`) },
	}
	for i, change := range changes {
		other := approvalState()
		change(&other)
		if ApprovalSig(other) == sig {
			t.Fatalf("change %d did not alter the signature", i)
		}
	}
}

// --- service clicks --------------------------------------------------------

// approvalHarness connects client-only with a long poll, so a click is the
// only thing that fetches /state after the first one.
func approvalHarness(t *testing.T, remote *fakeBridge, approval *Approval) (*harness, State) {
	t.Helper()
	remote.set(func(fb *fakeBridge) { fb.approval = approval })
	h := newHarness(t)
	s := h.clientSettings(remote, "remote-tok")
	s.PollInterval = time.Hour
	s.NotifyOnApproval = false
	h.svc.Configure(s)
	v := h.waitFor("attention", func(v View) bool { return v.Status.Key == StatusAttention })
	return h, v.State
}

func TestApprovalClickPostsEachButton(t *testing.T) {
	cases := []struct {
		name string
		idx  int
		want string
	}{
		{"deny", 0, `{"all":false,"choice":"deny","request_id":"req-1"}`},
		{"allow", 1, `{"all":false,"choice":"once","request_id":"req-1"}`},
		{"allow-session", 2, `{"all":false,"choice":"session","request_id":"req-1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := newFakeBridge(t, "remote-tok")
			h, st := approvalHarness(t, remote, pendingApproval())
			buttons := ApprovalButtons(st)
			if len(buttons) != 3 {
				t.Fatalf("got %d buttons", len(buttons))
			}
			h.svc.ApprovalClick(buttons[tc.idx].ID)
			waitUntil(t, "approval posted", func() bool { return remote.count("/approval") == 1 })
			time.Sleep(80 * time.Millisecond)
			if n := remote.count("/approval"); n != 1 {
				t.Fatalf("posted %d approvals, want 1", n)
			}
			for _, r := range remote.requests() {
				if r.Path != "/approval" {
					continue
				}
				if r.Method != http.MethodPost {
					t.Errorf("/approval method = %s, want POST", r.Method)
				}
				if r.Body != tc.want {
					t.Errorf("/approval body = %s, want %s", r.Body, tc.want)
				}
			}
			if v := h.svc.Snapshot(); v.ActionError != "" {
				t.Fatalf("action error after a valid click: %q", v.ActionError)
			}
		})
	}
}

func TestApprovalClickWithoutRequestIDOmitsIt(t *testing.T) {
	remote := newFakeBridge(t, "remote-tok")
	a := pendingApproval()
	a.RequestID = ""
	a.Request = json.RawMessage(`{"command":"rm -rf build"}`)
	h, st := approvalHarness(t, remote, a)
	h.svc.ApprovalClick(ApprovalButtons(st)[1].ID)
	waitUntil(t, "approval posted", func() bool { return remote.count("/approval") == 1 })
	for _, r := range remote.requests() {
		if r.Path == "/approval" && strings.Contains(r.Body, "request_id") {
			t.Fatalf("stock-bridge body carries request_id: %s", r.Body)
		}
	}
}

func TestApprovalClickStaleCachedSignaturePostsNothing(t *testing.T) {
	remote := newFakeBridge(t, "remote-tok")
	h, _ := approvalHarness(t, remote, pendingApproval())
	before := remote.count("/state")

	h.svc.ApprovalClick("approval:000000000000:allow")
	v := h.waitFor("changed message", func(v View) bool { return v.ActionError == MsgApprovalChanged })
	if v.ActionError != "The approval changed; check the card again" {
		t.Fatalf("message = %q", v.ActionError)
	}
	time.Sleep(80 * time.Millisecond)
	if n := remote.count("/approval"); n != 0 {
		t.Fatalf("a stale click posted %d approvals", n)
	}
	if n := remote.count("/state"); n != before {
		t.Fatalf("a stale click fetched /state %d extra times", n-before)
	}
}

func TestApprovalClickRefetchedSignatureMismatchPostsNothing(t *testing.T) {
	remote := newFakeBridge(t, "remote-tok")
	h, st := approvalHarness(t, remote, pendingApproval())
	buttons := ApprovalButtons(st)

	// The bridge now shows a different request; the cached card is stale.
	other := pendingApproval()
	other.Request = json.RawMessage(`{"command":"rm -rf src","id":"req-2"}`)
	other.RequestID = "req-2"
	other.Message = "rm -rf src?"
	remote.set(func(fb *fakeBridge) { fb.approval = other })

	h.svc.ApprovalClick(buttons[1].ID)
	v := h.waitFor("changed message", func(v View) bool { return v.ActionError == MsgApprovalChanged })
	if v.ActionError != "The approval changed; check the card again" {
		t.Fatalf("message = %q", v.ActionError)
	}
	time.Sleep(80 * time.Millisecond)
	if n := remote.count("/approval"); n != 0 {
		t.Fatalf("a stale click posted %d approvals", n)
	}
}

func TestApprovalClick409ShowsMessageAndDoesNotRetry(t *testing.T) {
	remote := newFakeBridge(t, "remote-tok")
	remote.handle("/approval", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"approval_mismatch"}`))
	})
	h, st := approvalHarness(t, remote, pendingApproval())
	before := remote.count("/state")

	h.svc.ApprovalClick(ApprovalButtons(st)[1].ID)
	v := h.waitFor("changed message", func(v View) bool { return v.ActionError == MsgApprovalChanged })
	if v.ActionError != "The approval changed; check the card again" {
		t.Fatalf("message = %q", v.ActionError)
	}
	waitUntil(t, "state refetched after 409", func() bool { return remote.count("/state") > before })
	time.Sleep(120 * time.Millisecond)
	if n := remote.count("/approval"); n != 1 {
		t.Fatalf("a 409 was retried: %d approval posts", n)
	}
}
