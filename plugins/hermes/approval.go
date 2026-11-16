package hermes

// Approval handling. Hermes's approval resolver treats a missing choice or
// "deny" as a refusal, so the stock bridge's "denied" (GitHub main) grants
// the tool call once; and "all" means "resolve every queued approval", not
// session scope, so Allow Session with all:true never persists anything. The
// table below is the only place a choice value is written.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Approval button actions, as they appear in the action ID.
const (
	ApprovalAllow        = "allow"
	ApprovalAllowSession = "allow-session"
	ApprovalDeny         = "deny"

	approvalActionPrefix = "approval:"
)

// approvalChoices maps each card button to the body Hermes honors, in
// Noctalia's order (Deny, Allow, Allow Session). Anything unmapped becomes
// deny, and all is always false.
var approvalChoices = []struct {
	Action string
	Label  string
	Choice string
}{
	{ApprovalDeny, "Deny", "deny"},
	{ApprovalAllow, "Allow", "once"},
	{ApprovalAllowSession, "Allow Session", "session"},
}

// ApprovalButton is one card button: the node ID carries the pending
// request's signature, so a click from an older card cannot resolve a newer
// request.
type ApprovalButton struct {
	ID    string
	Label string
}

// ApprovalButtons returns the card's buttons, or none when no approval is
// pending.
func ApprovalButtons(st State) []ApprovalButton {
	if !st.Approval.Pending {
		return nil
	}
	sig := ApprovalSig(st)
	out := make([]ApprovalButton, 0, len(approvalChoices))
	for _, c := range approvalChoices {
		out = append(out, ApprovalButton{ID: approvalActionPrefix + sig + ":" + c.Action, Label: c.Label})
	}
	return out
}

// ApprovalSig is the first 12 hex digits of SHA-256 over the session ID, the
// tool name, the message and the canonical JSON of approval.request.
func ApprovalSig(st State) string { return approvalSig(st.Session.ID, st.Approval) }

func approvalSig(sessionID string, a Approval) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + a.ToolName + "\x00" + a.Message + "\x00" + canonicalRequest(a.Request)))
	return hex.EncodeToString(sum[:])[:12]
}

// canonicalRequest re-encodes the request with sorted keys, so a snapshot
// that re-encoded it keeps the same signature. Anything that is not JSON is
// hashed as sent.
func canonicalRequest(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "null"
	}
	var v any
	if json.Unmarshal(trimmed, &v) != nil {
		return string(trimmed)
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return string(trimmed)
	}
	return string(canonical)
}

// parseApprovalAction splits approval:<sig>:<action>.
func parseApprovalAction(id string) (sig, action string, ok bool) {
	rest, found := strings.CutPrefix(id, approvalActionPrefix)
	if !found {
		return "", "", false
	}
	sig, action, found = strings.Cut(rest, ":")
	if !found || sig == "" || action == "" {
		return "", "", false
	}
	return sig, action, true
}

// approvalBody is the POST /approval body for one button choice. all is
// always false and an unmapped action becomes deny, so "denied", "approved",
// "approve" and an empty choice can never leave the plugin.
func approvalBody(action string, st State) map[string]any {
	choice := "deny"
	for _, c := range approvalChoices {
		if c.Action == action {
			choice = c.Choice
			break
		}
	}
	body := map[string]any{"choice": choice, "all": false}
	if st.Approval.RequestID != "" {
		body["request_id"] = st.Approval.RequestID
	}
	return body
}

// approvalStillShown reports whether sig names the pending approval in st.
func approvalStillShown(st State, sig string) bool {
	return st.Approval.Pending && ApprovalSig(st) == sig
}

// approvalClick is one parsed button press, handled on the action worker.
type approvalClick struct {
	sig    string
	action string
}

// ApprovalClick routes one approval:<sig>:<action> click to the action
// worker. The worker checks the signature against the cached card, fetches
// /state once more, checks again and only then posts. These checks are
// defense in depth for a bridge that is not the bundled copy.
func (s *Service) ApprovalClick(actionID string) {
	sig, action, ok := parseApprovalAction(actionID)
	if !ok {
		return
	}
	s.enqueue(job{approval: &approvalClick{sig: sig, action: action}})
}

// runApproval resolves one approval click on the action worker.
func (s *Service) runApproval(cl approvalClick) {
	b := s.currentBridge()
	if b == nil {
		s.publish(func(v *View) { v.ActionError = MsgOffline })
		return
	}
	s.mu.Lock()
	cached := s.view.State
	s.mu.Unlock()
	if !approvalStillShown(cached, cl.sig) {
		s.publish(func(v *View) { v.ActionError = MsgApprovalChanged })
		return
	}
	fresh, err := b.State(s.ctx)
	if err != nil {
		if s.ctx.Err() != nil {
			return
		}
		s.deps.Logf("hermes: /state: %v", err)
		s.publish(func(v *View) { v.ActionError = actionMessage("/state", err) })
		return
	}
	if !approvalStillShown(fresh, cl.sig) {
		s.publish(func(v *View) { v.ActionError = MsgApprovalChanged })
		return
	}
	snap, err := b.Post(s.ctx, "/approval", approvalBody(cl.action, fresh))
	if err != nil {
		if s.ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrApprovalMismatch) {
			// The bridge refused the request we saw on screen. Show the same
			// message, refresh the card, and never retry the post.
			s.publish(func(v *View) { v.ActionError = MsgApprovalChanged })
			if st, e := b.State(s.ctx); e == nil && s.ctx.Err() == nil {
				s.publishState(st)
			}
			return
		}
		s.deps.Logf("hermes: %s: %v", "/approval", err)
		s.publish(func(v *View) { v.ActionError = actionMessage("/approval", err) })
		return
	}
	s.publish(func(v *View) { v.ActionError = "" })
	if snap.HasState {
		s.publishState(snap.State)
	} else if st, e := b.State(s.ctx); e == nil && s.ctx.Err() == nil {
		s.publishState(st)
	}
}
