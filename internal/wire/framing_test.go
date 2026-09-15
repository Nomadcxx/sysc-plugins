package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// timerIdentity is the manifest identity every framing fixture speaks for.
var timerIdentity = Identity{ID: "org.sysc.timer", Name: "Timer", Version: "1.0.0"}

func hostHelloFixture() *HostHello {
	return &HostHello{
		Supported:    []Version{{Major: 1, Minor: 0}},
		Plugin:       timerIdentity,
		Capabilities: []string{"notifications", "panels", "settings", "state"},
		Limits:       DefaultLimits,
	}
}

func pluginHelloFixture() *PluginHello {
	return &PluginHello{
		Protocol:     Version{Major: 1, Minor: 0},
		Plugin:       timerIdentity,
		Capabilities: []string{"notifications", "panels", "settings", "state"},
	}
}

// encodeOne encodes a single message and returns the raw bytes written.
func encodeOne(t *testing.T, m Message) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(m); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestHelloExchangeRoundTripsOnOneLine(t *testing.T) {
	t.Parallel()

	// The host writes first and the plugin reads it.
	out := encodeOne(t, hostHelloFixture())
	if n := bytes.Count(out, []byte("\n")); n != 1 {
		t.Fatalf("host.hello wrote %d newlines, want exactly the framing one", n)
	}
	if out[len(out)-1] != '\n' {
		t.Fatalf("host.hello does not end with the framing newline")
	}

	got, err := NewDecoder(bytes.NewReader(out), ToPlugin).Decode()
	if err != nil {
		t.Fatalf("decode host.hello: %v", err)
	}
	hello, ok := got.(*HostHello)
	if !ok {
		t.Fatalf("decoded %T, want *HostHello", got)
	}
	if hello.Type != TypeHostHello {
		t.Errorf("type = %q, want %q", hello.Type, TypeHostHello)
	}
	if hello.Plugin != timerIdentity {
		t.Errorf("identity = %+v, want %+v", hello.Plugin, timerIdentity)
	}
	if !reflect.DeepEqual(hello.Limits, DefaultLimits) {
		t.Errorf("limits = %+v, want %+v", hello.Limits, DefaultLimits)
	}

	// The plugin answers and the host reads it.
	back := encodeOne(t, pluginHelloFixture())
	reply, err := NewDecoder(bytes.NewReader(back), ToHost).Decode()
	if err != nil {
		t.Fatalf("decode plugin.hello: %v", err)
	}
	ph, ok := reply.(*PluginHello)
	if !ok {
		t.Fatalf("decoded %T, want *PluginHello", reply)
	}
	if ph.Protocol != (Version{Major: 1, Minor: 0}) {
		t.Errorf("selected protocol = %+v, want 1.0", ph.Protocol)
	}
}

func TestEncodeStampsTheRegisteredType(t *testing.T) {
	t.Parallel()

	// A caller that leaves Type empty still produces a framed, typed line: the
	// wire name belongs to the Go type, not to the caller.
	out := encodeOne(t, &HostShutdown{})
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Type != TypeHostShutdown {
		t.Fatalf("type = %q, want %q", probe.Type, TypeHostShutdown)
	}
}

func TestDecodeRejectsUnknownMessageType(t *testing.T) {
	t.Parallel()

	line := []byte(`{"type":"host.teleport"}` + "\n")
	_, err := NewDecoder(bytes.NewReader(line), ToPlugin).Decode()
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

func TestDecodeRejectsAMessageTravellingTheWrongWay(t *testing.T) {
	t.Parallel()

	// host.hello is a host-to-plugin message. A host that receives one is
	// talking to something that is not a v1 plugin.
	out := encodeOne(t, hostHelloFixture())
	_, err := NewDecoder(bytes.NewReader(out), ToHost).Decode()
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	line := []byte(`{"type":"plugin.status","state":"ok","mood":"cheerful"}` + "\n")
	_, err := NewDecoder(bytes.NewReader(line), ToHost).Decode()
	if err == nil {
		t.Fatal("decode accepted an unknown field")
	}
	if !strings.Contains(err.Error(), "mood") {
		t.Fatalf("err = %v, want it to name the unknown field", err)
	}
}

func TestDecodeRejectsTrailingContentOnOneLine(t *testing.T) {
	t.Parallel()

	line := []byte(`{"type":"host.shutdown"} {"type":"host.shutdown"}` + "\n")
	_, err := NewDecoder(bytes.NewReader(line), ToPlugin).Decode()
	if err == nil {
		t.Fatal("decode accepted two values on one line")
	}
}

func TestDecodeRejectsAnEmptyLine(t *testing.T) {
	t.Parallel()

	_, err := NewDecoder(strings.NewReader("\n"), ToPlugin).Decode()
	if err == nil {
		t.Fatal("decode accepted an empty line")
	}
}

func TestDecodeAcceptsALineAtTheSizeLimit(t *testing.T) {
	t.Parallel()

	// Pad a valid message with text until the encoded line is exactly at the
	// ceiling, proving the limit is inclusive rather than off by one.
	pad := strings.Repeat("a", 16)
	msg := &PluginStatus{State: StatusOK, Message: pad}
	line := encodeOne(t, msg)
	grow := MaxMessageBytes - len(line)
	msg.Message = strings.Repeat("a", 16+grow)
	line = encodeOne(t, msg)
	if len(line) != MaxMessageBytes {
		t.Fatalf("fixture line = %d bytes, want exactly %d", len(line), MaxMessageBytes)
	}

	got, err := NewDecoder(bytes.NewReader(line), ToHost).Decode()
	if err != nil {
		t.Fatalf("decode at the limit: %v", err)
	}
	if st := got.(*PluginStatus); len(st.Message) != 16+grow {
		t.Fatalf("message = %d bytes, want %d", len(st.Message), 16+grow)
	}
}

func TestDecodeRejectsALineOverTheSizeLimit(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	buf.WriteString(`{"type":"plugin.status","state":"ok","message":"`)
	buf.WriteString(strings.Repeat("a", MaxMessageBytes))
	buf.WriteString(`"}` + "\n")

	_, err := NewDecoder(&buf, ToHost).Decode()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong", err)
	}
}

func TestDecodeRejectsAnUnterminatedLineOverTheLimit(t *testing.T) {
	t.Parallel()

	// A plugin that never emits a newline must not be able to grow the host's
	// read buffer without bound.
	r := strings.NewReader(strings.Repeat("a", MaxMessageBytes+1))
	_, err := NewDecoder(r, ToHost).Decode()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong", err)
	}
}

func TestDecodeReadsAStreamOfMessagesInOrder(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for _, m := range []Message{
		pluginHelloFixture(),
		&PluginStatus{State: StatusOK},
		&HostCall{ID: "1", Call: CallStateGet, Params: json.RawMessage(`{"key":"remaining"}`)},
	} {
		if err := enc.Encode(m); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}

	dec := NewDecoder(&buf, ToHost)
	want := []string{TypePluginHello, TypePluginStatus, TypeHostCall}
	for i, w := range want {
		got, err := dec.Decode()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got.messageType() != w {
			t.Errorf("message %d type = %q, want %q", i, got.messageType(), w)
		}
	}
}

func TestSnapshotSurvivesTextThatContainsANewline(t *testing.T) {
	t.Parallel()

	// Newlines inside note text must escape into the JSON string rather than
	// split one message into two frames.
	snap := &ViewSnapshot{
		ViewID:   "v1",
		Revision: 7,
		Root: &Node{Kind: KindColumn, Children: []*Node{
			{Kind: KindText, Text: "first\nsecond"},
		}},
	}
	line := encodeOne(t, snap)
	if n := bytes.Count(line, []byte("\n")); n != 1 {
		t.Fatalf("snapshot wrote %d newlines, want exactly the framing one", n)
	}

	got, err := NewDecoder(bytes.NewReader(line), ToHost).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	back := got.(*ViewSnapshot)
	if back.Revision != 7 {
		t.Errorf("revision = %d, want 7", back.Revision)
	}
	if back.Root.Children[0].Text != "first\nsecond" {
		t.Errorf("text = %q, want the embedded newline preserved", back.Root.Children[0].Text)
	}
}

func TestPatchAndResyncRoundTrip(t *testing.T) {
	t.Parallel()

	line := encodeOne(t, &ViewPatch{
		ViewID: "v1", Base: 1, Revision: 2,
		Replacements: []Replacement{{Key: "time", Node: &Node{Kind: KindText, Key: "time", Text: "09:00", Tabular: true}}},
	})
	got, err := NewDecoder(bytes.NewReader(line), ToHost).Decode()
	if err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	p := got.(*ViewPatch)
	if p.Type != TypeViewPatch || p.Base != 1 || p.Revision != 2 || p.Replacements[0].Key != "time" {
		t.Fatalf("patch = %+v", p)
	}

	back := encodeOne(t, &ViewResync{ViewID: "v1"})
	msg, err := NewDecoder(bytes.NewReader(back), ToPlugin).Decode()
	if err != nil {
		t.Fatalf("decode resync: %v", err)
	}
	if msg.(*ViewResync).Type != TypeViewResync || msg.(*ViewResync).ViewID != "v1" {
		t.Fatalf("resync = %+v", msg)
	}
}
