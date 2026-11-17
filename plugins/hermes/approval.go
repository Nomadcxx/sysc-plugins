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

// ApprovalButton is one card button: the node ID carries the card ID of the
// request it was rendered for, so a click from an older card cannot resolve
// a newer request.
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
	card := ApprovalCardID(st)
	out := make([]ApprovalButton, 0, len(approvalChoices))
	for _, c := range approvalChoices {
		out = append(out, ApprovalButton{ID: approvalActionPrefix + card + ":" + c.Action, Label: c.Label})
	}
	return out
}

// ApprovalCardID names the request a card was rendered for: a digest of
// Hermes's request_id, or of the nonce the service gave a request that came
// without one (O1). The digest keeps an arbitrary bridge-sent id out of the
// node ID. Only a state that never passed through the service has neither,
// and falls back to the content signature.
func ApprovalCardID(st State) string {
	var key string
	switch a := st.Approval; {
	case a.RequestID != "":
		key = "request\x00" + a.RequestID
	case a.Nonce != "":
		key = "nonce\x00" + a.Nonce
	default:
		return ApprovalSig(st)
	}
	sum := sha256.Sum256([]byte(st.Session.ID + "\x00" + key))
	return hex.EncodeToString(sum[:])[:16]
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

// parseApprovalAction splits approval:<card>:<action>.
func parseApprovalAction(id string) (card, action string, ok bool) {
	rest, found := strings.CutPrefix(id, approvalActionPrefix)
	if !found {
		return "", "", false
	}
	card, action, found = strings.Cut(rest, ":")
	if !found || card == "" || action == "" {
		return "", "", false
	}
	return card, action, true
}

// approvalChoice is the table's body value for one button action, and
// whether the table maps that action.
func approvalChoice(action string) (string, bool) {
	for _, c := range approvalChoices {
		if c.Action == action {
			return c.Choice, true
		}
	}
	return "", false
}

// approvalBody is the POST /approval body for one button choice. all is
// always false and an unmapped action becomes deny, so "denied", "approved",
// "approve" and an empty choice can never leave the plugin.
func approvalBody(action string, st State) map[string]any {
	choice, ok := approvalChoice(action)
	if !ok {
		// Unmapped actions ask for deny, read from the table like any other
		// choice.
		choice, _ = approvalChoice(ApprovalDeny)
	}
	body := map[string]any{"choice": choice, "all": false}
	if st.Approval.RequestID != "" {
		body["request_id"] = st.Approval.RequestID
	}
	return body
}

// approvalStillShown reports whether card names the pending approval in st.
func approvalStillShown(st State, card string) bool {
	return st.Approval.Pending && ApprovalCardID(st) == card
}

// approvalClick is one parsed button press, handled on the action worker.
type approvalClick struct {
	card   string
	action string
}

// ApprovalClick routes one approval:<card>:<action> click to the action
// worker. The worker checks the card ID against the cached card, fetches
// /state once more, checks again and only then posts. These checks are
// defense in depth for a bridge that is not the bundled copy.
func (s *Service) ApprovalClick(actionID string) {
	card, action, ok := parseApprovalAction(actionID)
	if !ok {
		return
	}
	s.enqueue(job{approval: &approvalClick{card: card, action: action}})
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
	if !approvalStillShown(cached, cl.card) {
		// The click was rendered for another request (O1).
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
	s.mu.Lock()
	s.stampCardLocked(&fresh)
	s.mu.Unlock()
	if !approvalStillShown(fresh, cl.card) {
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
