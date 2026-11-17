package hermes

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func decodeTestState(t *testing.T, body string) State {
	t.Helper()
	var s State
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return s
}

func TestStateMirrorsTheBridgeSnapshot(t *testing.T) {
	s := decodeTestState(t, `{
		"bridge":{"status":"online","error":""},
		"hermes":{"status":"idle","gateway_pid":"4242","model":"example-model","provider":"example"},
		"session":{"id":"s1","stored_id":"st1","title":"Release notes","running":false,"cwd":"/home/fixture"},
		"messages":[{"id":"m1","role":"user","text":"hi","streaming":false,"ts":1762860000.5}],
		"events":[{"id":"e1","type":"tool.start","name":"terminal","text":"go test","ts":1762860001}],
		"approval":{"pending":true,"message":"Run go test?","tool_name":"terminal","request":{"request_id":"r1","command":"go test"}},
		"usage":{"input":10,"output":20,"total":30,"cost_usd":0.25},
		"summary":{
			"model":{"name":"example-model","provider":"example"},
			"models":[{"provider":"example","model":"example-model","name":"Example"}],
			"mcp":{"enabled":1,"total":2,"status":"ok"},
			"cron":{"active":1,"total":1,"next_run":"2026-11-12T00:00:00Z","jobs":[{"id":"j1","name":"Nightly","active":true,"state":"scheduled","schedule":"0 0 * * *","next_run":"2026-11-12T00:00:00Z","last_status":"ok"}]},
			"activity":{"tool_events":2,"running_tools":1,"last_tool":"terminal"},
			"gateway":{"status":"running","platforms":{"telegram":"connected"}}
		},
		"updated_at":1762860002
	}`)
	if s.Bridge.Status != "online" || s.Hermes.GatewayPID != "4242" || s.Hermes.Provider != "example" {
		t.Fatalf("bridge/hermes = %+v %+v", s.Bridge, s.Hermes)
	}
	if s.Session.StoredID != "st1" || s.Session.Title != "Release notes" || s.Session.CWD != "/home/fixture" {
		t.Fatalf("session = %+v", s.Session)
	}
	if len(s.Messages) != 1 || s.Messages[0].ID != "m1" || s.Messages[0].Role != "user" || s.Messages[0].Text != "hi" || s.Messages[0].TS != 1762860000.5 {
		t.Fatalf("messages = %+v", s.Messages)
	}
	if len(s.Events) != 1 || s.Events[0].Type != "tool.start" || s.Events[0].Name != "terminal" {
		t.Fatalf("events = %+v", s.Events)
	}
	if !s.Approval.Pending || s.Approval.ToolName != "terminal" || !strings.Contains(string(s.Approval.Request), `"request_id":"r1"`) {
		t.Fatalf("approval = %+v", s.Approval)
	}
	if s.Usage.Total != 30 || s.Usage.CostUSD == nil || *s.Usage.CostUSD != 0.25 {
		t.Fatalf("usage = %+v", s.Usage)
	}
	sum := s.Summary
	if sum.Model.Name != "example-model" || len(sum.Models) != 1 || sum.Models[0].Model != "example-model" ||
		sum.MCP.Total != 2 || sum.Cron.Jobs[0].LastStatus != "ok" || sum.Activity.RunningTools != 1 ||
		sum.Gateway.Status != "running" || sum.Gateway.Platforms["telegram"] != "connected" {
		t.Fatalf("summary = %+v", sum)
	}
	if s.UpdatedAt != 1762860002 {
		t.Fatalf("updated_at = %v", s.UpdatedAt)
	}
}

func TestStateDecodeIsLenientPerField(t *testing.T) {
	// A wrong type in one field must not lose the rest of the snapshot.
	s := decodeTestState(t, `{"bridge":{"status":"online"},"hermes":"not an object","usage":{"total":"many"},
		"summary":{"gateway":{"status":"running"},"models":"nope"},
		"messages":[{"type":"assistant","content":"from content"},"junk",{"role":"user","text":"from text","ts":"soon"}]}`)
	if s.Bridge.Status != "online" || s.Summary.Gateway.Status != "running" {
		t.Fatalf("lost good fields: %+v %+v", s.Bridge, s.Summary.Gateway)
	}
	if s.Hermes != (HermesInfo{}) {
		t.Fatalf("bad hermes object = %+v, want zero", s.Hermes)
	}
	if len(s.Messages) != 2 || s.Messages[0].Role != "assistant" || s.Messages[0].Text != "from content" ||
		s.Messages[1].Role != "user" || s.Messages[1].Text != "from text" || s.Messages[1].TS != 0 {
		t.Fatalf("messages = %+v, want role/type and text/content accepted and junk skipped", s.Messages)
	}
	var bad State
	if err := json.Unmarshal([]byte(`[1,2]`), &bad); err == nil {
		t.Fatal("a non-object snapshot decoded without error")
	}
}

func TestNormalizeFillsMissingAndKeepsNewest(t *testing.T) {
	s := decodeTestState(t, `{"bridge":{"status":"online"}}`)
	s.Normalize()
	if s.Messages == nil || s.Events == nil || s.Sessions == nil || s.Summary.Models == nil ||
		s.Summary.Cron.Jobs == nil || s.Summary.Gateway.Platforms == nil {
		t.Fatalf("Normalize left nil collections: %+v", s)
	}
	if s.MessageCount != 0 {
		t.Fatalf("MessageCount = %d", s.MessageCount)
	}

	s = State{}
	// The transcript pages through every kept message, so the cap is the
	// history Older can reach, not a node budget.
	for i := 0; i < 206; i++ {
		s.Messages = append(s.Messages, Message{ID: fmt.Sprintf("m%03d", i), Role: "user", Text: fmt.Sprint(i)})
	}
	for i := 0; i < 12; i++ {
		s.Events = append(s.Events, Event{ID: fmt.Sprintf("e%02d", i)})
	}
	for i := 0; i < 14; i++ {
		s.Sessions = append(s.Sessions, SessionSummary{ID: fmt.Sprintf("s%02d", i)})
	}
	s.Normalize()
	if len(s.Messages) != 200 || s.MessageCount != 206 || s.Messages[0].ID != "m006" || s.Messages[199].ID != "m205" {
		t.Fatalf("messages: kept %d (%s..%s), count %d; want the newest 200 and 206", len(s.Messages), s.Messages[0].ID, s.Messages[len(s.Messages)-1].ID, s.MessageCount)
	}
	if len(s.Events) != 8 || s.Events[0].ID != "e04" || s.Events[7].ID != "e11" {
		t.Fatalf("events: kept %d starting %s; want the newest 8", len(s.Events), s.Events[0].ID)
	}
	if len(s.Sessions) != 10 || s.Sessions[0].ID != "s00" {
		t.Fatalf("sessions: kept %d; want the bridge's first 10", len(s.Sessions))
	}
	before := fmt.Sprintf("%+v", s)
	s.Normalize()
	if after := fmt.Sprintf("%+v", s); after != before {
		t.Fatal("Normalize is not idempotent")
	}
}

func TestNormalizeCapsAndSanitizesMessages(t *testing.T) {
	big := strings.Repeat("é", 50*1024) // 100 KiB of two-byte runes
	s := State{Messages: []Message{
		{ID: "big", Role: "assistant", Text: big},
		{ID: "dirty", Role: "assistant", Text: "red \x1b[31mtext\x1b[0m\x00 tab\there\nnext \x1b]0;title\x07done \x1b]8;;http://x\x1b\\link"},
	}}
	s.Normalize()
	got := s.Messages[0].Text
	if len(got) > MaxMessageBytes || !strings.HasSuffix(got, TruncatedMarker) || !utf8.ValidString(got) {
		t.Fatalf("100 KiB message: %d bytes, marker %t, valid %t; want at most %d with the marker", len(got), strings.HasSuffix(got, TruncatedMarker), utf8.ValidString(got), MaxMessageBytes)
	}
	if want := "red text tab\there\nnext done link"; s.Messages[1].Text != want {
		t.Fatalf("sanitized = %q, want %q", s.Messages[1].Text, want)
	}
}

func TestNormalizeGivesMissingIDsAStableHash(t *testing.T) {
	mk := func() State {
		return State{Messages: []Message{{Role: "user", Text: "a", TS: 1}, {Role: "assistant", Text: "b", TS: 2}, {ID: "kept", Role: "user"}}}
	}
	a, b := mk(), mk()
	a.Normalize()
	b.Normalize()
	if a.Messages[0].ID == "" || a.Messages[0].ID == a.Messages[1].ID || a.Messages[2].ID != "kept" {
		t.Fatalf("ids = %q %q %q", a.Messages[0].ID, a.Messages[1].ID, a.Messages[2].ID)
	}
	if !reflect.DeepEqual(a.Messages, b.Messages) {
		t.Fatal("the same snapshot hashed to different ids")
	}
	// Appending a message does not change the earlier messages' ids.
	c := mk()
	c.Messages = append(c.Messages, Message{Role: "user", Text: "c", TS: 3})
	c.Normalize()
	if c.Messages[0].ID != a.Messages[0].ID || c.Messages[1].ID != a.Messages[1].ID {
		t.Fatal("ids moved when a message was appended")
	}
}

func TestNormalizeReplacesLongBridgeErrors(t *testing.T) {
	secret := "Traceback (most recent call last):\n  File \"hermes\", line 1\nfake-secret-detail"
	for _, raw := range []string{secret, "one\rtwo", strings.Repeat("x", 201)} {
		s := State{Bridge: BridgeInfo{Status: "online", Error: raw}}
		s.Normalize()
		if s.Bridge.Error != BridgeErrorReplacement {
			t.Fatalf("bridge.error %q became %q, want the replacement", raw, s.Bridge.Error)
		}
		if strings.Contains(s.Bridge.Error, "Traceback") || strings.Contains(s.Bridge.Error, "fake-secret") || strings.Contains(s.Bridge.Error, "xxx") {
			t.Fatal("the replacement contains the original text")
		}
	}
	s := State{Bridge: BridgeInfo{Error: "gateway not running \x1b[1m!"}}
	s.Normalize()
	if s.Bridge.Error != "gateway not running !" {
		t.Fatalf("short error = %q, want it kept and sanitized", s.Bridge.Error)
	}
	s = State{Bridge: BridgeInfo{Error: strings.Repeat("y", 200)}}
	s.Normalize()
	if len(s.Bridge.Error) != 200 {
		t.Fatal("a 200-byte one-line error was replaced")
	}
}

// TestApprovalDisplayReadsTheRequest pins D5: Hermes 835a913 sends message and
// tool_name empty and puts command, description and tool_name in the request
// object, so the card must read that object.
func TestApprovalDisplayReadsTheRequest(t *testing.T) {
	a := Approval{Request: json.RawMessage(`{"command":"rm -r /tmp/x","description":"recursive delete","pattern_key":"rm -r","choices":["once","always"]}`)}
	d := a.Display()
	if d.Title != "Approval required" {
		t.Errorf("title = %q, want %q", d.Title, "Approval required")
	}
	if d.Message != "recursive delete" {
		t.Errorf("message = %q, want the request description", d.Message)
	}
	if d.Command != "rm -r /tmp/x" {
		t.Errorf("command = %q, want the request command", d.Command)
	}

	multi := Approval{Request: json.RawMessage(`{"command":"go test ./...\n\t-race"}`)}
	if got := multi.Display().Command; got != "go test ./... -race" {
		t.Errorf("command = %q, want one line", got)
	}

	named := Approval{Request: json.RawMessage(`{"tool_name":"terminal","description":"ls"}`)}
	if got := named.Display().Title; got != "terminal" {
		t.Errorf("title = %q, want the request tool name", got)
	}

	explicit := Approval{Message: "Run go test ./...", ToolName: "terminal",
		Request: json.RawMessage(`{"command":"go test","description":"tests","tool_name":"shell"}`)}
	d = explicit.Display()
	if d.Title != "terminal" || d.Message != "Run go test ./..." || d.Command != "go test" {
		t.Errorf("display = %+v, want the explicit message and tool name to win", d)
	}

	for _, req := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`"flat"`)} {
		d = Approval{Request: req}.Display()
		if d.Title != "Approval required" || d.Message != "" || d.Command != "" {
			t.Errorf("request %q: display = %+v, want the old fallback", req, d)
		}
	}
}
