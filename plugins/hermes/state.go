package hermes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Normalization limits, from the design's State and status section.
const (
	MaxMessages     = 200
	MaxEvents       = 8
	MaxSessions     = 10
	MaxMessageBytes = 16 << 10
	maxBridgeError  = 200

	TruncatedMarker        = "… [truncated]"
	BridgeErrorReplacement = "Bridge error (see bridge log)"
)

// State mirrors one bridge snapshot. Decoding is lenient per field: a field
// of the wrong type is left zero instead of failing the whole snapshot.
type State struct {
	Bridge    BridgeInfo  `json:"bridge"`
	Hermes    HermesInfo  `json:"hermes"`
	Session   SessionInfo `json:"session"`
	Messages  []Message   `json:"messages"`
	Events    []Event     `json:"events"`
	Approval  Approval    `json:"approval"`
	Usage     Usage       `json:"usage"`
	Summary   Summary     `json:"summary"`
	UpdatedAt float64     `json:"updated_at"`

	// Sessions is the plugin's extension from GET /sessions.
	Sessions []SessionSummary `json:"-"`
	// MessageCount is how many messages the bridge sent before Normalize
	// kept the newest MaxMessages.
	MessageCount int `json:"-"`
}

// BridgeInfo is bridge{status,error}. The plugin itself sets status "error"
// for an auth or protocol failure.
type BridgeInfo struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

// HermesInfo is hermes{status,gateway_pid,model,provider}.
type HermesInfo struct {
	Status     string `json:"status"`
	GatewayPID string `json:"gateway_pid"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
}

// SessionInfo is session{id,stored_id,title,running,cwd}.
type SessionInfo struct {
	ID       string `json:"id"`
	StoredID string `json:"stored_id"`
	Title    string `json:"title"`
	Running  bool   `json:"running"`
	CWD      string `json:"cwd"`
}

// Message is one transcript entry. Decoding accepts role or type, and text
// or content.
type Message struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	Text      string  `json:"text"`
	Streaming bool    `json:"streaming"`
	TS        float64 `json:"ts"`
}

// Event is one status or tool event.
type Event struct {
	ID   string  `json:"id"`
	Type string  `json:"type"`
	Name string  `json:"name"`
	Text string  `json:"text"`
	TS   float64 `json:"ts"`
}

// Approval is approval{pending,message,tool_name,request} plus the bundled
// bridge's request_id (Task 3a). Request is kept as sent; the approval
// mapping reads its identity. RequestID is empty against a stock bridge.
type Approval struct {
	Pending   bool            `json:"pending"`
	Message   string          `json:"message"`
	ToolName  string          `json:"tool_name"`
	Request   json.RawMessage `json:"request"`
	RequestID string          `json:"request_id"`
	// Nonce names a request that came without a request_id. The service
	// gives it when it first sees the card and keeps it while the card
	// stays up, so two identical id-less requests get distinct cards. It
	// never leaves the plugin.
	Nonce string `json:"-"`
}

// Usage is usage{input,output,total,cost_usd}; CostUSD is nil when unknown.
type Usage struct {
	Input   int64    `json:"input"`
	Output  int64    `json:"output"`
	Total   int64    `json:"total"`
	CostUSD *float64 `json:"cost_usd"`
}

// Summary is summary{model,models,mcp,cron,activity,gateway}.
type Summary struct {
	Model    SummaryModel    `json:"model"`
	Models   []ModelOption   `json:"models"`
	MCP      SummaryMCP      `json:"mcp"`
	Cron     SummaryCron     `json:"cron"`
	Activity SummaryActivity `json:"activity"`
	Gateway  SummaryGateway  `json:"gateway"`
}

type SummaryModel struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// ModelOption is one model-picker entry.
type ModelOption struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name"`
}

type SummaryMCP struct {
	Enabled int    `json:"enabled"`
	Total   int    `json:"total"`
	Status  string `json:"status"`
}

type SummaryCron struct {
	Active  int       `json:"active"`
	Total   int       `json:"total"`
	NextRun string    `json:"next_run"`
	Jobs    []CronJob `json:"jobs"`
}

type CronJob struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	State      string `json:"state"`
	Schedule   string `json:"schedule"`
	NextRun    string `json:"next_run"`
	LastStatus string `json:"last_status"`
}

type SummaryActivity struct {
	ToolEvents   int    `json:"tool_events"`
	RunningTools int    `json:"running_tools"`
	LastTool     string `json:"last_tool"`
}

type SummaryGateway struct {
	Status    string            `json:"status"`
	Platforms map[string]string `json:"platforms"`
}

// UnmarshalJSON decodes a snapshot object field by field.
func (s *State) UnmarshalJSON(data []byte) error {
	var out State
	top, err := objectFields(data)
	if err != nil {
		return err
	}
	decodeFields(top, map[string]any{
		"bridge": &out.Bridge, "hermes": &out.Hermes, "session": &out.Session,
		"events": &out.Events, "approval": &out.Approval, "usage": &out.Usage,
		"summary": &out.Summary, "updated_at": &out.UpdatedAt,
	})
	if raw, ok := top["messages"]; ok {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil {
			for _, item := range items {
				if m, ok := decodeMessage(item); ok {
					out.Messages = append(out.Messages, m)
				}
			}
		}
	}
	*s = out
	return nil
}

// UnmarshalJSON decodes the summary field by field, so one odd section does
// not blank the others.
func (s *Summary) UnmarshalJSON(data []byte) error {
	var out Summary
	top, err := objectFields(data)
	if err != nil {
		return err
	}
	decodeFields(top, map[string]any{
		"model": &out.Model, "models": &out.Models, "mcp": &out.MCP,
		"cron": &out.Cron, "activity": &out.Activity, "gateway": &out.Gateway,
	})
	*s = out
	return nil
}

// objectFields splits a JSON object; null is an empty object.
func objectFields(data []byte) (map[string]json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	return top, nil
}

// decodeFields decodes each present key into its destination pointer and
// leaves the destination zero when that key has the wrong type.
func decodeFields(top map[string]json.RawMessage, fields map[string]any) {
	for key, dst := range fields {
		raw, ok := top[key]
		if !ok {
			continue
		}
		fresh := reflect.New(reflect.TypeOf(dst).Elem())
		if json.Unmarshal(raw, fresh.Interface()) == nil {
			reflect.ValueOf(dst).Elem().Set(fresh.Elem())
		}
	}
}

// decodeMessage reads one message, accepting role or type and text or
// content. Anything that is not an object is skipped.
func decodeMessage(raw json.RawMessage) (Message, bool) {
	top, err := objectFields(raw)
	if err != nil || top == nil {
		return Message{}, false
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			var v string
			if r, ok := top[k]; ok && json.Unmarshal(r, &v) == nil && v != "" {
				return v
			}
		}
		return ""
	}
	m := Message{ID: str("id"), Role: str("role", "type"), Text: str("text", "content")}
	if r, ok := top["streaming"]; ok {
		_ = json.Unmarshal(r, &m.Streaming)
	}
	if r, ok := top["ts"]; ok {
		_ = json.Unmarshal(r, &m.TS)
	}
	return m, true
}

// Normalize makes the state safe to render: collections are never nil,
// lists are cut to the newest entries, text is sanitized and capped,
// missing message ids get a stable hash, and a raw multi-line or long
// bridge error is replaced. It is idempotent.
func (s *State) Normalize() {
	if n := len(s.Messages); n > s.MessageCount {
		s.MessageCount = n
	}
	for i := range s.Messages {
		m := &s.Messages[i]
		if m.ID == "" {
			// Index in the bridge's list, so earlier ids survive an append.
			m.ID = messageID(m.Role, m.TS, i)
		}
		m.Text = capText(sanitizeText(m.Text))
	}
	if len(s.Messages) > MaxMessages {
		s.Messages = s.Messages[len(s.Messages)-MaxMessages:]
	}
	for i := range s.Events {
		e := &s.Events[i]
		e.Name = capText(sanitizeText(e.Name))
		e.Text = capText(sanitizeText(e.Text))
	}
	if len(s.Events) > MaxEvents {
		s.Events = s.Events[len(s.Events)-MaxEvents:]
	}
	if len(s.Sessions) > MaxSessions {
		s.Sessions = s.Sessions[:MaxSessions]
	}
	s.Messages = nonNil(s.Messages)
	s.Events = nonNil(s.Events)
	s.Sessions = nonNil(s.Sessions)
	s.Summary.Models = nonNil(s.Summary.Models)
	s.Summary.Cron.Jobs = nonNil(s.Summary.Cron.Jobs)
	if s.Summary.Gateway.Platforms == nil {
		s.Summary.Gateway.Platforms = map[string]string{}
	}
	if e := s.Bridge.Error; strings.ContainsAny(e, "\r\n") || len(e) > maxBridgeError {
		s.Bridge.Error = BridgeErrorReplacement
	} else {
		s.Bridge.Error = sanitizeText(e)
	}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// messageID hashes a message's role, timestamp and list index.
func messageID(role string, ts float64, index int) string {
	sum := sha256.Sum256([]byte(role + "\x00" + strconv.FormatFloat(ts, 'g', -1, 64) + "\x00" + strconv.Itoa(index)))
	return "msg-" + hex.EncodeToString(sum[:8])
}

// sanitizeText removes NUL, ANSI CSI and OSC sequences and other control
// characters, keeping newlines and tabs, and repairs invalid UTF-8.
func sanitizeText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			i += escapeLen(s[i:])
			continue
		case r == 0x9b: // C1 CSI
			i += size + csiLen(s[i+size:])
			continue
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			// dropped
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// escapeLen is the length of the escape sequence that starts s (s[0] is ESC).
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		return 2 + csiLen(s[2:])
	case ']', 'P', '_', '^': // OSC, DCS, APC, PM: up to BEL or ST
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default:
		return 2
	}
}

// csiLen is the length of CSI parameters plus the final byte.
func csiLen(s string) int {
	for j := 0; j < len(s); j++ {
		if s[j] >= 0x40 && s[j] <= 0x7e {
			return j + 1
		}
	}
	return len(s)
}

// capText cuts s to MaxMessageBytes of UTF-8, marker included.
func capText(s string) string {
	if len(s) <= MaxMessageBytes {
		return s
	}
	cut := MaxMessageBytes - len(TruncatedMarker)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + TruncatedMarker
}

// ApprovalDisplay is what the approval card says: a title, a reason and the
// command, all trimmed and ready to clip to the card's width.
type ApprovalDisplay struct {
	Title   string
	Message string
	Command string
}

// approvalRequest is the part of Hermes's request object the card reads.
// pattern_key and choices are deliberately not here: the buttons already
// carry the choices and the pattern is Hermes's own allowlist key.
type approvalRequest struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	ToolName    string `json:"tool_name"`
}

// Display resolves the card's lines. Hermes 835a913 sends message and
// tool_name empty and puts the content in the request object, so those fields
// are read too; an explicit message or tool name still wins, and a missing or
// malformed request falls back to the text as sent. The command is squashed to
// one line, since the card has room for one.
func (a Approval) Display() ApprovalDisplay {
	d := ApprovalDisplay{
		Title:   strings.TrimSpace(a.ToolName),
		Message: strings.TrimSpace(a.Message),
	}
	var req approvalRequest
	if len(a.Request) > 0 {
		_ = json.Unmarshal(a.Request, &req)
	}
	if d.Title == "" {
		d.Title = strings.TrimSpace(req.ToolName)
	}
	if d.Title == "" {
		d.Title = "Approval required"
	}
	if d.Message == "" {
		d.Message = strings.TrimSpace(req.Description)
	}
	d.Command = strings.Join(strings.Fields(req.Command), " ")
	return d
}
