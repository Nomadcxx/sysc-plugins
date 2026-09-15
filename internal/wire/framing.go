package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxMessageBytes bounds one framed line, newline included. The reader
// enforces it before decoding, so a plugin cannot make the host allocate or
// parse an unbounded value.
const MaxMessageBytes = 1 << 20

var (
	// ErrLineTooLong reports a frame at or over MaxMessageBytes. It is a
	// framing failure: the stream is no longer trustworthy, because the reader
	// cannot tell where the next message begins without consuming the rest.
	ErrLineTooLong = errors.New("plugin/v1: message exceeds the framing limit")
	// ErrUnknownType reports a name this reader does not accept, including a
	// well-known name travelling the wrong way.
	ErrUnknownType = errors.New("plugin/v1: unknown message type")
)

// Direction says which side is reading. It is part of the reader rather than a
// later check because a host that receives host.hello is not talking to a v1
// plugin at all, and saying so at the frame is clearer than failing later on a
// field.
type Direction uint8

const (
	// ToHost reads messages a plugin sends.
	ToHost Direction = iota
	// ToPlugin reads messages the host sends.
	ToPlugin
)

// registry holds the constructor for every name each side accepts.
var registry = map[Direction]map[string]func() Message{
	ToHost: {
		TypePluginHello:  func() Message { return &PluginHello{} },
		TypeViewSnapshot: func() Message { return &ViewSnapshot{} },
		TypeViewPatch:    func() Message { return &ViewPatch{} },
		TypeHostCall:     func() Message { return &HostCall{} },
		TypePluginStatus: func() Message { return &PluginStatus{} },
	},
	ToPlugin: {
		TypeHostHello:       func() Message { return &HostHello{} },
		TypeHostShutdown:    func() Message { return &HostShutdown{} },
		TypeViewOpen:        func() Message { return &ViewOpen{} },
		TypeViewClose:       func() Message { return &ViewClose{} },
		TypeViewResync:      func() Message { return &ViewResync{} },
		TypeInputEvent:      func() Message { return &InputEvent{} },
		TypeSettingsChanged: func() Message { return &SettingsChanged{} },
		TypeHostReply:       func() Message { return &HostReply{} },
	},
}

// TypeOf returns a message's wire name. It exists because the interface method
// is unexported: a host outside this package still has to be able to say which
// message it received when reporting a protocol fault.
func TypeOf(m Message) string {
	if m == nil {
		return ""
	}
	return m.messageType()
}

// Encoder writes framed messages to a stream.
type Encoder struct {
	w io.Writer
}

// NewEncoder returns an encoder writing to w. It performs no buffering of its
// own: one Encode is one write, which is what keeps a message atomic against a
// pipe reader on the other side.
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Encode writes m as one line.
//
// It stamps the wire name from the Go type, so a caller cannot ship a message
// labelled as something it is not, and it refuses to write a frame the peer
// would be obliged to reject as oversized.
func (e *Encoder) Encode(m Message) error {
	if m == nil {
		return errors.New("plugin/v1: encode nil message")
	}
	if err := stamp(m); err != nil {
		return err
	}
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("plugin/v1: encode %s: %w", m.messageType(), err)
	}
	if len(body)+1 > MaxMessageBytes {
		return fmt.Errorf("plugin/v1: encode %s: %w", m.messageType(), ErrLineTooLong)
	}
	line := make([]byte, 0, len(body)+1)
	line = append(line, body...)
	line = append(line, '\n')
	if _, err := e.w.Write(line); err != nil {
		return fmt.Errorf("plugin/v1: write %s: %w", m.messageType(), err)
	}
	return nil
}

// stamp sets the Type field from the concrete type's registered name.
func stamp(m Message) error {
	name := m.messageType()
	switch v := m.(type) {
	case *HostHello:
		v.Type = name
	case *HostShutdown:
		v.Type = name
	case *ViewOpen:
		v.Type = name
	case *ViewClose:
		v.Type = name
	case *ViewResync:
		v.Type = name
	case *InputEvent:
		v.Type = name
	case *SettingsChanged:
		v.Type = name
	case *HostReply:
		v.Type = name
	case *PluginHello:
		v.Type = name
	case *ViewSnapshot:
		v.Type = name
	case *ViewPatch:
		v.Type = name
	case *HostCall:
		v.Type = name
	case *PluginStatus:
		v.Type = name
	default:
		return fmt.Errorf("plugin/v1: %T is not a version-one message", m)
	}
	return nil
}

// Decoder reads framed messages from a stream.
type Decoder struct {
	r    io.Reader
	dir  Direction
	buf  []byte
	head int
	eof  bool
}

// NewDecoder returns a decoder reading messages travelling in dir.
func NewDecoder(r io.Reader, dir Direction) *Decoder {
	return &Decoder{r: r, dir: dir}
}

// Decode reads and returns the next message.
//
// It reads a bounded line, resolves the name against this direction's set, and
// then decodes strictly: unknown fields and trailing values are errors, so a
// plugin cannot smuggle content past a v1 reader by appending to a line it
// otherwise understands.
func (d *Decoder) Decode() (Message, error) {
	line, err := d.readLine()
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(line)) == 0 {
		return nil, errors.New("plugin/v1: empty message")
	}

	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("plugin/v1: malformed message: %w", err)
	}
	build, ok := registry[d.dir][envelope.Type]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, envelope.Type)
	}

	msg := build()
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(msg); err != nil {
		return nil, fmt.Errorf("plugin/v1: decode %s: %w", envelope.Type, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("plugin/v1: decode %s: trailing content after the message", envelope.Type)
	}
	return msg, nil
}

// readLine returns the next newline-terminated frame without its newline.
//
// The buffer never grows past the framing limit: an over-long or unterminated
// line fails as soon as the limit is passed, rather than after the peer
// finishes sending it.
func (d *Decoder) readLine() ([]byte, error) {
	for {
		if i := bytes.IndexByte(d.buf[d.head:], '\n'); i >= 0 {
			line := d.buf[d.head : d.head+i]
			d.head += i + 1
			if d.head == len(d.buf) {
				d.buf, d.head = d.buf[:0], 0
			}
			return line, nil
		}
		if pending := len(d.buf) - d.head; pending >= MaxMessageBytes {
			return nil, fmt.Errorf("%w of %d bytes", ErrLineTooLong, MaxMessageBytes)
		}
		if d.eof {
			if len(d.buf) > d.head {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, io.EOF
		}
		if err := d.fill(); err != nil {
			return nil, err
		}
	}
}

// fill reads one chunk, compacting consumed bytes first so a long stream of
// small messages does not grow the buffer.
func (d *Decoder) fill() error {
	if d.head > 0 {
		d.buf = append(d.buf[:0], d.buf[d.head:]...)
		d.head = 0
	}
	const chunk = 64 << 10
	start := len(d.buf)
	d.buf = append(d.buf, make([]byte, chunk)...)
	n, err := d.r.Read(d.buf[start:])
	d.buf = d.buf[:start+n]
	switch {
	case errors.Is(err, io.EOF):
		d.eof = true
	case err != nil:
		return err
	}
	return nil
}
