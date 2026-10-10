package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	requestID    = "c1"
	maxLineBytes = 4 << 20
)

// APIError is an error reply from the herdr server.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return "herdr " + e.Code + ": " + e.Message }

// Event is one unsolicited line from a subscribed connection.
type Event struct {
	Type string          `json:"event"`
	Data json.RawMessage `json:"data"`
}

// SubSpec is one entry in an events.subscribe request.
type SubSpec struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *APIError       `json:"error"`
}

// Subscription is a live event stream. It is only safe for one reader.
type Subscription struct {
	conn    net.Conn
	scanner *bufio.Scanner
	events  chan Event
	done    chan struct{}
	once    sync.Once
}

// armContext applies the context deadline to the conn and returns a stop func.
// stop disarms the watcher and joins it before returning, so no late
// SetDeadline(now) can land after the caller clears the deadline. A context
// without a Done channel (e.g. Background) starts no watcher.
func armContext(ctx context.Context, conn net.Conn) func() {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-watcherDone
	}
}

func writeRequest(w io.Writer, method string, params any) error {
	if params == nil {
		params = struct{}{}
	}
	line, err := json.Marshal(request{ID: requestID, Method: method, Params: params})
	if err != nil {
		return err
	}
	return writeLine(w, line)
}

func writeLine(w io.Writer, line []byte) error {
	buf := make([]byte, len(line)+1)
	copy(buf, line)
	buf[len(line)] = '\n'
	_, err := w.Write(buf)
	return err
}

func newScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64<<10), maxLineBytes)
	return s
}

// decodeReply returns the reply's error if present, else decodes result. A
// reply with neither error nor result is ok with a zero-value result (herdr
// always sends one of the two).
func decodeReply(line []byte, result any) error {
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr: bad reply: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}

// CallOn writes one request and reads one reply on an already-dialed connection.
func CallOn(ctx context.Context, conn net.Conn, method string, params, result any) error {
	stop := armContext(ctx, conn)
	defer stop()
	if err := writeRequest(conn, method, params); err != nil {
		return err
	}
	scanner := newScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	return decodeReply(scanner.Bytes(), result)
}

// Call dials a one-shot connection, sends one request, and closes it. It has
// no built-in timeout: pass a ctx with a deadline to bound the call.
func Call(ctx context.Context, path, method string, params, result any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	return CallOn(ctx, conn, method, params, result)
}

// SubscribeTo opens the single events.subscribe connection for path. ctx bounds
// the dial and the subscribe ack only; it does not terminate the stream, so the
// caller must Close when done.
func SubscribeTo(ctx context.Context, path string, subs []SubSpec) (*Subscription, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	sub, err := subscribeConn(ctx, conn, subs)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return sub, nil
}

// subscribeConn writes the one allowed subscribe request, reads the ack, then
// starts the reader. Tests use it directly with a net.Pipe. ctx bounds the ack
// phase only: the deadline is cleared so a deadline ctx cannot kill the stream.
func subscribeConn(ctx context.Context, conn net.Conn, subs []SubSpec) (*Subscription, error) {
	stop := armContext(ctx, conn)
	if err := writeRequest(conn, "events.subscribe", map[string]any{"subscriptions": subs}); err != nil {
		stop()
		return nil, err
	}
	scanner := newScanner(conn)
	if !scanner.Scan() {
		stop()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.ErrUnexpectedEOF
	}
	if err := decodeReply(scanner.Bytes(), nil); err != nil {
		stop()
		return nil, err
	}
	stop()
	_ = conn.SetDeadline(time.Time{})
	s := &Subscription{conn: conn, scanner: scanner, events: make(chan Event), done: make(chan struct{})}
	go s.read()
	return s, nil
}

// canonicalEventType normalizes the two naming families the server puts on
// one stream: global events arrive underscore-named ("pane_created") while
// pane-scoped subscription frames already use dots ("pane.agent_status_changed").
// Consumers work in the dotted form used by events.subscribe requests.
func canonicalEventType(t string) string {
	if strings.Contains(t, ".") {
		return t
	}
	if i := strings.Index(t, "_"); i >= 0 {
		return t[:i] + "." + t[i+1:]
	}
	return t
}

// read never writes to conn; it only pushes events until the stream ends.
func (s *Subscription) read() {
	defer close(s.events)
	for s.scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(s.scanner.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Type == "" {
			continue
		}
		ev.Type = canonicalEventType(ev.Type)
		select {
		case s.events <- ev:
		case <-s.done:
			return
		}
	}
}

// Next blocks for the next event. It returns false when the stream is closed.
func (s *Subscription) Next() (Event, bool) {
	ev, ok := <-s.events
	return ev, ok
}

// Close stops the reader and closes the connection.
func (s *Subscription) Close() error {
	s.once.Do(func() { close(s.done) })
	return s.conn.Close()
}
