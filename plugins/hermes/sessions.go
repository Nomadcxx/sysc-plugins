package hermes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	maxSessionRows    = 10
	maxSessionIDBytes = 200
	sessionRowHeight  = 28
	sessionTimeW      = 40
	sessionResumeW    = 12 + 10*len("Resume") + 20 + 8
	sectionGap        = 4
	sectionButtonH    = 32
	// scrollbarInset keeps list rows clear of the host's scrollbar.
	scrollbarInset = 12

	sessionIconResume = "play_arrow"
	refreshIcon       = "refresh"
)

// Session is one row of the bridge's GET /sessions, as noctalia's
// SessionPopup reads it. StartedAt is in unix seconds.
type Session struct {
	ID        string
	Title     string
	Preview   string
	StartedAt float64
}

// SessionsSnapshot is what the panel knows about session history.
// Unsupported means the bridge answered 404: it has no history endpoint.
type SessionsSnapshot struct {
	Loaded      bool
	Unsupported bool
	Err         string
	Sessions    []Session
}

// ResumeRequest is the body of POST /session/resume.
type ResumeRequest struct {
	SessionID string `json:"session_id"`
}

// RelativeTime is SessionPopup's age label: now, Nm, Nh or Nd. A missing
// start time gives no label.
func RelativeTime(ts float64, now time.Time) string {
	if ts <= 0 {
		return ""
	}
	age := now.Unix() - int64(ts)
	switch {
	case age < 60:
		return "now"
	case age < 3600:
		return fmt.Sprintf("%dm", age/60)
	case age < 86400:
		return fmt.Sprintf("%dh", age/3600)
	}
	return fmt.Sprintf("%dd", age/86400)
}

// sessionLabel follows SessionPopup: title, else preview, else the first
// eight characters of the id, else "Session".
func sessionLabel(s Session) string {
	for _, v := range []string{s.Title, s.Preview} {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	if id := strings.TrimSpace(s.ID); id != "" {
		if r := []rune(id); len(r) > 8 {
			return string(r[:8])
		}
		return id
	}
	return "Session"
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func plainSessionID(id string) bool {
	if id == "" || len(id) > maxSessionIDBytes {
		return false
	}
	for _, r := range id {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// SessionNodeID names a session's resume button by the session's id, never
// its position, so a list that reorders between revisions still resumes the
// session the user pressed. An id that is too long or carries whitespace is
// hashed instead.
func SessionNodeID(id string) string {
	if plainSessionID(id) {
		return "session:" + id
	}
	return "session:" + shortHash(id)
}

// ResumeFor maps a pressed session button back to the session it names.
func ResumeFor(nodeID string, snap SessionsSnapshot) (ResumeRequest, bool) {
	if !strings.HasPrefix(nodeID, "session:") {
		return ResumeRequest{}, false
	}
	for _, s := range snap.Sessions {
		if SessionNodeID(s.ID) == nodeID {
			return ResumeRequest{SessionID: s.ID}, true
		}
	}
	return ResumeRequest{}, false
}

// sessionsNaturalHeight is the height the section wants for snap.
func sessionsNaturalHeight(snap SessionsSnapshot) int {
	rows := len(snap.Sessions)
	if rows > maxSessionRows {
		rows = maxSessionRows
	}
	if rows == 0 || snap.Unsupported || !snap.Loaded {
		return sectionButtonH + sectionGap + lineHeight
	}
	return sectionButtonH + sectionGap + rows*sessionRowHeight
}

const sessionsMinHeight = sectionButtonH + sectionGap + lineHeight

// SessionsSection lists up to ten recent sessions, newest first as the
// bridge orders them, each with its age and a Resume button, under a
// Refresh button. The empty states say why there is nothing to list.
func SessionsSection(snap SessionsSnapshot, now time.Time, width, height int) *v1.Node {
	head := &v1.Node{
		Kind: v1.KindRow, Width: width, Height: sectionButtonH, PinEnd: true,
		Children: []*v1.Node{
			{Kind: v1.KindText, Text: "Recent sessions", Bold: true, Size: "label"},
			button("sessions-refresh", "Refresh", "Refresh sessions", refreshIcon, 12+10*len("Refresh")+28, sectionButtonH),
		},
	}
	col := &v1.Node{Kind: v1.KindColumn, Width: width, Height: height, Gap: sectionGap, Children: []*v1.Node{head}}
	note := func(text string, tone v1.Tone) *v1.Node {
		return &v1.Node{Kind: v1.KindText, Text: clip(text, width/8), Size: "caption", Tone: tone, Height: lineHeight}
	}
	switch {
	case snap.Unsupported:
		col.Children = append(col.Children, note("This bridge does not expose session history", v1.ToneSubtle))
		return col
	case strings.TrimSpace(snap.Err) != "":
		col.Children = append(col.Children, note("Could not load sessions: "+snap.Err, v1.ToneError))
		return col
	case !snap.Loaded:
		col.Children = append(col.Children, note("Loading sessions…", v1.ToneSubtle))
		return col
	case len(snap.Sessions) == 0:
		col.Children = append(col.Children, note("No recent sessions", v1.ToneSubtle))
		return col
	}
	listH := height - sectionButtonH - sectionGap
	if listH < sessionRowHeight {
		listH = sessionRowHeight
	}
	rowW := width - scrollbarInset
	labelW := rowW - sessionTimeW - sessionResumeW - 2*8
	list := &v1.Node{Kind: v1.KindList, Key: "sessions-list", Width: width, Height: listH}
	for i, s := range snap.Sessions {
		if i == maxSessionRows {
			break
		}
		id := SessionNodeID(s.ID)
		label := sessionLabel(s)
		list.Children = append(list.Children, &v1.Node{
			Kind: v1.KindRow, Key: "row:" + id, Width: rowW, Height: sessionRowHeight, Gap: 8, PinEnd: true,
			Children: []*v1.Node{
				{Kind: v1.KindRow, Gap: 8, Height: sessionRowHeight, Children: []*v1.Node{
					{Kind: v1.KindText, Text: clip(fmt.Sprintf("%d. %s", i+1, label), labelW/8), Size: "caption"},
					{Kind: v1.KindText, Text: RelativeTime(s.StartedAt, now), Size: "caption", Tone: v1.ToneSubtle},
				}},
				button(id, "Resume", "Resume "+label, sessionIconResume, sessionResumeW, sessionRowHeight),
			},
		})
	}
	col.Children = append(col.Children, list)
	return col
}

// button is an activatable button with its accessible name.
func button(id, text, name, icon string, width, height int) *v1.Node {
	return &v1.Node{
		Kind: v1.KindButton, ID: id, Icon: icon, Text: text, Name: name, Role: "button",
		Width: width, Height: height, Padding: 6, Events: []v1.EventKind{v1.EventActivate},
	}
}
