package moonbit

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

// DefaultSocket is where the moonbit daemon listens when its unit passes
// --socket. SYSC_MOONBIT_SOCKET overrides it (tests, custom units); the shell
// forwards only SYSC_* variables to plugins.
const DefaultSocket = "/run/moonbit/panel.sock"

func SocketPath() string {
	if p := os.Getenv("SYSC_MOONBIT_SOCKET"); p != "" {
		return p
	}
	return DefaultSocket
}

// Runner talks the daemon's one-request-per-connection protocol. Cancelling
// an operation means ending the request side: moonbit watches the request
// reader for EOF and aborts cooperatively, which is the only cancel that
// works on a root process.
type Runner struct {
	Path string
}

func (r Runner) path() string {
	if r.Path != "" {
		return r.Path
	}
	return SocketPath()
}

// Op is a live operation stream. Events closes when the daemon ends the
// stream (terminal event or connection loss).
type Op struct {
	Events chan Event
	conn   net.Conn
}

const (
	// cancelGrace bounds the wait for the daemon's terminal event after
	// Cancel, so a wedged daemon still ends the stream.
	cancelGrace = 10 * time.Second
	// statusTimeout bounds one status round-trip.
	statusTimeout = 5 * time.Second
	// maxEventLine fits clean_done, whose errors list is unbounded.
	maxEventLine = 4 << 20
)

// Cancel half-closes the connection: the daemon sees EOF on its request
// reader, aborts, and still writes cancelled (or its terminal event) on the
// open read side, which the stream delivers before it closes.
func (o *Op) Cancel() {
	if uc, ok := o.conn.(*net.UnixConn); ok && uc.CloseWrite() == nil {
		_ = o.conn.SetReadDeadline(time.Now().Add(cancelGrace))
		return
	}
	_ = o.conn.Close()
}

// request is the daemon's line protocol header.
type request struct {
	Cmd        string   `json:"cmd"`
	Mode       string   `json:"mode,omitempty"`
	Force      bool     `json:"force,omitempty"`
	Categories []string `json:"categories,omitempty"`
	ScannedAt  string   `json:"scanned_at,omitempty"`
}

func (r Runner) start(req request) (*Op, error) {
	conn, err := net.DialTimeout("unix", r.path(), 2*time.Second)
	if err != nil {
		return nil, err
	}
	line, err := json.Marshal(req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		_ = conn.Close()
		return nil, err
	}
	op := &Op{conn: conn, Events: make(chan Event, 32)}
	go func() {
		defer close(op.Events)
		defer conn.Close()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 64<<10), maxEventLine)
		for sc.Scan() {
			b := sc.Bytes()
			if len(b) == 0 {
				continue
			}
			var ev Event
			if json.Unmarshal(b, &ev) == nil && ev.T != "" {
				op.Events <- ev
			}
		}
	}()
	return op, nil
}

// Scan starts a scan stream; mode is "" (daemon config), "quick" or "deep".
func (r Runner) Scan(mode string, cats []string) (*Op, error) {
	return r.start(request{Cmd: "scan", Mode: mode, Categories: cats})
}

// Clean starts a clean stream; force=false only ever dry-runs. scannedAt,
// from the reviewed scan's done event, makes the daemon refuse a cache a
// later scan replaced; empty skips that check (daemons before 1.6).
func (r Runner) Clean(force bool, cats []string, scannedAt string) (*Op, error) {
	return r.start(request{Cmd: "clean", Force: force, Categories: cats, ScannedAt: scannedAt})
}

// Status performs one synchronous status round-trip. The connection is
// always closed on return; the daemon ends non-stream commands itself, and a
// lingering open read end would hold a daemon goroutine otherwise.
func (r Runner) Status() (*Event, error) {
	op, err := r.start(request{Cmd: "status"})
	if err != nil {
		return nil, err
	}
	_ = op.conn.SetReadDeadline(time.Now().Add(statusTimeout))
	defer op.conn.Close()
	var last *Event
	for ev := range op.Events {
		e := ev
		switch e.T {
		case "status":
			last = &e
		case "error":
			return nil, errors.New(e.Msg)
		}
	}
	if last == nil {
		return nil, errors.New("moonbit: no status reply")
	}
	return last, nil
}
