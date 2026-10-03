package moonbit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Runner drives `moonbit panel` through sudo, one request per run: the same
// elevation as starting the TUI with sudo. sudo -k asks for the password every
// time, and the run uses the user's own config and scan cache, reaching every
// category the TUI reaches.
type Runner struct {
	// Sudo and Moonbit name the programs; empty means "sudo" from PATH and
	// "moonbit", which sudo resolves on its own secure_path.
	Sudo, Moonbit string
}

// Request is one panel command for `moonbit panel`.
type Request struct {
	Cmd        string   `json:"cmd"`
	Mode       string   `json:"mode,omitempty"`
	Force      bool     `json:"force,omitempty"`
	Categories []string `json:"categories,omitempty"`
	ScannedAt  string   `json:"scanned_at,omitempty"`
	Op         string   `json:"op,omitempty"`
	Target     string   `json:"target,omitempty"`
	Action     string   `json:"action,omitempty"`
}

const (
	// promptMarker is the -p prompt sudo prints when it wants the password.
	// The runner answers the first; a second means the password was wrong.
	promptMarker = "[moonbit-panel-password]"
	// readyTimeout bounds sudo's check and moonbit's start.
	readyTimeout = 20 * time.Second
	// maxEventLine fits clean_done, whose errors list is unbounded.
	maxEventLine = 4 << 20
)

// cancelGrace bounds the wait for moonbit's terminal event after Cancel.
var cancelGrace = 10 * time.Second

// killWait covers sudo's terminate_command, which sleeps two seconds
// between the catchable signals and SIGKILL.
const killWait = 3 * time.Second

// ErrWrongPassword means sudo asked again: the password was not accepted.
var ErrWrongPassword = errors.New("wrong password")

// Op is a live operation. Events closes when moonbit ends the run.
type Op struct {
	Events chan Event
	stdin  io.WriteCloser
	kill   func()
	once   sync.Once
}

// Cancel ends the request side. moonbit cancels on EOF and still writes its
// terminal event; a run still going after cancelGrace is stopped, child
// included. The stream stays open for that grace so a terminal event is not
// dropped, and StreamEnded still runs if the stream then closes without one.
func (o *Op) Cancel() {
	o.once.Do(func() {
		_ = o.stdin.Close()
		time.AfterFunc(cancelGrace, o.kill)
	})
}

func (r Runner) sudo() string {
	if r.Sudo != "" {
		return r.Sudo
	}
	return "sudo"
}

func (r Runner) moonbit() string {
	if r.Moonbit != "" {
		return r.Moonbit
	}
	return "moonbit"
}

// Start runs one request as root. It returns once moonbit is listening and
// has the request, or with ErrWrongPassword or sudo's own complaint. The
// password is zeroed before Start returns.
func (r Runner) Start(password []byte, req Request) (*Op, error) {
	defer clear(password)
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(r.sudo(), "-S", "-k", "-p", promptMarker, r.moonbit(), "panel")
	// Own process group, so the grace kill can signal sudo's group without
	// signalling this plugin.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Setpgid makes sudo the group leader; keep its group ID even if sudo exits
	// before Cancel's grace timer fires.
	pgid := cmd.Process.Pid
	kill := func() { stopCommand(cmd, stdout, pgid) }

	prompts := make(chan struct{}, 4)
	tail := &lastLine{}
	go watchStderr(stderr, prompts, tail)

	events := make(chan Event, 32)
	ready := make(chan struct{})
	go func() {
		defer close(events)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), maxEventLine)
		started := false
		for sc.Scan() {
			var ev Event
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.T == "" {
				continue
			}
			if !started {
				if ev.T == "ready" {
					started = true
					close(ready)
				}
				continue
			}
			events <- ev
		}
		_ = cmd.Wait()
	}()

	asked := false
	deadline := time.NewTimer(readyTimeout)
	defer deadline.Stop()
	for {
		select {
		case <-prompts:
			if asked {
				kill()
				return nil, ErrWrongPassword
			}
			asked = true
			if _, err := stdin.Write(append(password, '\n')); err != nil {
				kill()
				return nil, err
			}
		case <-ready:
			if _, err := stdin.Write(append(line, '\n')); err != nil {
				kill()
				return nil, err
			}
			return &Op{Events: events, stdin: stdin, kill: kill}, nil
		case _, open := <-events:
			if !open {
				return nil, startError(tail.get())
			}
		case <-deadline.C:
			kill()
			return nil, errors.New("moonbit did not start in time")
		}
	}
}

// stopCommand lets sudo finish terminate_command before stopping its process
// group. Killing sudo first aborts that cleanup and can orphan its root child.
// Closing stdout still ends the event stream if a same-user descendant remains.
func stopCommand(cmd *exec.Cmd, stdout io.Closer, pgid int) {
	if cmd != nil && cmd.Process != nil {
		if own, err := syscall.Getpgid(0); err != nil || pgid == own {
			pgid = 0
		}
		if err := cmd.Process.Signal(syscall.SIGALRM); err == nil {
			time.Sleep(killWait)
		}
		if pgid != 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		_ = cmd.Process.Kill()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
}

// startError turns sudo's or moonbit's last words into the reason a run
// never started.
func startError(msg string) error {
	switch {
	case msg == "":
		return errors.New("moonbit exited before it started")
	case strings.Contains(msg, "not in the sudoers"), strings.Contains(msg, "not allowed to execute"):
		return errors.New("this account may not run moonbit with sudo")
	case strings.Contains(msg, "command not found"), strings.Contains(msg, "unknown command"):
		return errors.New("moonbit 1.7 or newer is needed for the panel")
	}
	return errors.New(msg)
}

// watchStderr signals each password prompt and keeps the last line sudo or
// moonbit printed. The prompt has no newline, so it is matched in the byte
// stream rather than per line.
func watchStderr(r io.Reader, prompts chan<- struct{}, tail *lastLine) {
	var pending []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		pending = append(pending, buf[:n]...)
		for {
			i := bytes.Index(pending, []byte(promptMarker))
			if i < 0 {
				break
			}
			tail.add(pending[:i])
			pending = pending[i+len(promptMarker):]
			select {
			case prompts <- struct{}{}:
			default:
			}
		}
		if j := bytes.LastIndexByte(pending, '\n'); j >= 0 {
			tail.add(pending[:j])
			pending = pending[j+1:]
		}
		if err != nil {
			tail.add(pending)
			return
		}
	}
}

// lastLine keeps the last non-empty stderr line, shared between goroutines.
type lastLine struct {
	mu   sync.Mutex
	line string
}

func (l *lastLine) add(b []byte) {
	for _, s := range strings.Split(string(b), "\n") {
		if s = strings.TrimSpace(s); s != "" {
			l.mu.Lock()
			l.line = s
			l.mu.Unlock()
		}
	}
}

func (l *lastLine) get() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimPrefix(l.line, "Error: ")
}

// RequestLabel names a request for the password prompt.
func RequestLabel(req Request) string {
	switch req.Cmd {
	case "scan":
		return map[string]string{"quick": "a quick scan", "deep": "a deep scan"}[req.Mode]
	case "clean":
		return "the clean"
	case "docker":
		return map[string]string{"images": "the Docker image cleanup", "all": "the Docker cleanup"}[req.Op]
	case "schedule":
		what := map[string]string{"daemon": "daemon mode", "timers": "the scan and clean timers"}[req.Target]
		return fmt.Sprintf("%s %s", strings.TrimSuffix(req.Action, "e")+"ing", what)
	}
	return req.Cmd
}
