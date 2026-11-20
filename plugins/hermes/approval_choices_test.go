package hermes

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// withRequest is pendingApproval with request as the Hermes payload.
func withRequest(request string) State {
	st := approvalState()
	st.Approval.Request = json.RawMessage(request)
	return st
}

func buttonLabels(st State) []string {
	var out []string
	for _, b := range ApprovalButtons(st) {
		out = append(out, b.Label)
	}
	return out
}

// TestApprovalButtonsHonourHermesChoices is R4: Hermes says which choices
// it offers (tui_gateway/server.py _approval_request_payload), and the card
// draws only those. Without choices it derives them the same way Hermes
// does: smart_denied offers once and deny, allow_session false drops
// session, and otherwise all three show. "always" has no button here.
func TestApprovalButtonsHonourHermesChoices(t *testing.T) {
	cases := []struct {
		name, request string
		want          []string
	}{
		{"once and deny", `{"choices":["once","deny"]}`, []string{"Deny", "Allow"}},
		{"all four", `{"choices":["once","session","always","deny"]}`, []string{"Deny", "Allow", "Allow Session"}},
		{"no choices", `{"command":"rm -rf build"}`, []string{"Deny", "Allow", "Allow Session"}},
		{"no session", `{"allow_session":false}`, []string{"Deny", "Allow"}},
		{"smart denied", `{"smart_denied":true,"allow_session":true}`, []string{"Deny", "Allow"}},
		{"empty choices", `{"choices":[]}`, []string{"Deny", "Allow", "Allow Session"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buttonLabels(withRequest(tc.request)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buttons = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestApprovalClickOnAChoiceHermesDoesNotOfferPostsNothing: a click on a
// choice the request does not offer is refused, never sent.
func TestApprovalClickOnAChoiceHermesDoesNotOfferPostsNothing(t *testing.T) {
	remote := newFakeBridge(t, "remote-tok")
	a := pendingApproval()
	a.Request = json.RawMessage(`{"command":"rm -rf build","choices":["once","deny"]}`)
	h, st := approvalHarness(t, remote, a)
	h.svc.ApprovalClick("approval:" + ApprovalCardID(st) + ":" + ApprovalAllowSession)
	h.waitFor("changed message", func(v View) bool { return v.ActionError == MsgApprovalChanged })
	time.Sleep(80 * time.Millisecond)
	if n := remote.count("/approval"); n != 0 {
		t.Fatalf("a choice Hermes does not offer posted %d approvals", n)
	}
	h.svc.ApprovalClick("approval:" + ApprovalCardID(st) + ":" + ApprovalAllow)
	waitUntil(t, "offered choice posted", func() bool { return remote.count("/approval") == 1 })
}
