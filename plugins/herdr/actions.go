package herdr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Default action timeouts. Timeout bounds socket calls and one-shot commands;
// StopTimeout bounds session stop/delete, which waits on a graceful shutdown.
const (
	DefaultActionTimeout = 6 * time.Second
	DefaultStopTimeout   = 20 * time.Second
)

// defaultTerminals is the fallback order tried when $TERMINAL is unset or
// unusable. Actions.Terminals overrides it.
var defaultTerminals = []string{"alacritty", "foot", "kitty", "xfce4-terminal"}

// terminalSeparator maps a terminal's basename to the flag that introduces the
// command to run. Verified against each terminal's own --help:
//
//	alacritty        -e   alacritty -e <cmd> [args...]   (-e/--command takes the rest)
//	ghostty          -e   ghostty -e <cmd> [args...]
//	kitty            --   kitty -- <cmd> [args...]       (-- ends kitty's options)
//	foot             --   foot -- <cmd> [args...]        (foot takes [command args]; -e is a no-op)
//	xfce4-terminal   -x   xfce4-terminal -x <cmd> [args...] (-x/--execute passes multi-arg;
//	                                                        -e/--command takes one shell string)
//
// Unknown terminals default to -e, the xterm-compatible convention most
// emulators accept.
var terminalSeparator = map[string]string{
	"alacritty":      "-e",
	"ghostty":        "-e",
	"kitty":          "--",
	"foot":           "--",
	"xfce4-terminal": "-x",
}

// Actions performs the user-facing session and agent operations: opening a
// terminal attached to a session, focusing/reading panes, and stop/delete.
type Actions struct {
	Bin         string
	Terminals   []string
	Timeout     time.Duration
	StopTimeout time.Duration

	// Test seams. NewActions wires the real implementations.
	Run      func(ctx context.Context, bin string, args ...string) ([]byte, error)
	Spawn    func(cmd string, args ...string) error
	LookPath func(string) (string, error)
}

// NewActions returns an Actions bound to bin (which may be empty, in which case
// the binary is resolved from PATH at call time).
func NewActions(bin string) *Actions {
	a := &Actions{
		Bin:         bin,
		Timeout:     DefaultActionTimeout,
		StopTimeout: DefaultStopTimeout,
		LookPath:    exec.LookPath,
	}
	a.Run = a.defaultRun
	a.Spawn = defaultSpawn
	return a
}

// bin resolves the herdr binary, falling back to PATH once at call time.
func (a *Actions) bin() (string, error) {
	if a.Bin != "" {
		return a.Bin, nil
	}
	if p := DefaultHerdrBin(); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%w: not on PATH", ErrHerdrMissing)
}

// terminalArgs builds the argv that launches herdr inside term. The separator
// is per-terminal (see terminalSeparator); unknown terminals use -e.
func terminalArgs(term string, herdr []string) []string {
	sep, ok := terminalSeparator[filepath.Base(term)]
	if !ok {
		sep = "-e"
	}
	out := make([]string, 0, len(herdr)+2)
	out = append(out, term, sep)
	return append(out, herdr...)
}

// pickTerminal resolves a terminal using $TERMINAL first, then the built-in
// fallback list. It is the pure selection seam.
func pickTerminal(lookPath func(string) (string, error), env string) (string, error) {
	return selectTerminal(lookPath, env, defaultTerminals)
}

func selectTerminal(lookPath func(string) (string, error), env string, list []string) (string, error) {
	if env != "" {
		if p, err := lookPath(env); err == nil && p != "" {
			return p, nil
		}
	}
	for _, t := range list {
		if p, err := lookPath(t); err == nil && p != "" {
			return p, nil
		}
	}
	return "", errors.New("no terminal found")
}

// selectTerminal picks the terminal for this Actions, honouring a custom list.
func (a *Actions) selectTerminal() (string, error) {
	lp := a.LookPath
	if lp == nil {
		lp = exec.LookPath
	}
	list := a.Terminals
	if len(list) == 0 {
		list = defaultTerminals
	}
	return selectTerminal(lp, os.Getenv("TERMINAL"), list)
}

// spawnHerdr launches the herdr binary with herdrArgs inside a fresh terminal.
func (a *Actions) spawnHerdr(herdrArgs []string) error {
	bin, err := a.bin()
	if err != nil {
		return err
	}
	term, err := a.selectTerminal()
	if err != nil {
		return err
	}
	argv := terminalArgs(term, append([]string{bin}, herdrArgs...))
	return a.Spawn(argv[0], argv[1:]...)
}

// Attach opens a terminal attached to s. The default session (or an unnamed
// one) is opened without --session; a named session passes --session NAME.
func (a *Actions) Attach(_ context.Context, s SessionInfo) error {
	if s.Name != "" && !ValidSessionName(s.Name) {
		return fmt.Errorf("herdr: invalid session name %q", s.Name)
	}
	var herdrArgs []string
	if s.Name != "" && !s.Default {
		herdrArgs = []string{"--session", s.Name}
	}
	return a.spawnHerdr(herdrArgs)
}

// New creates (or attaches to) a named session in a fresh terminal.
func (a *Actions) New(_ context.Context, name string) error {
	if !ValidSessionName(name) {
		return fmt.Errorf("herdr: invalid session name %q", name)
	}
	return a.spawnHerdr([]string{"--session", name})
}

// Focus focuses the pane identified by paneID in the session on sock.
func (a *Actions) Focus(ctx context.Context, sock, paneID string) error {
	ctx, cancel := a.withTimeout(ctx, a.Timeout)
	defer cancel()
	return Call(ctx, sock, "agent.focus", map[string]any{"target": paneID}, nil)
}

// Read returns the recent output of paneID, with ANSI stripped. lines is
// clamped to 1..200.
func (a *Actions) Read(ctx context.Context, sock, paneID string, lines int) (string, error) {
	params := map[string]any{
		"target":     paneID,
		"source":     "recent",
		"lines":      clamp(lines, 1, 200),
		"strip_ansi": true,
	}
	var res struct {
		Text string `json:"text"`
	}
	ctx, cancel := a.withTimeout(ctx, a.Timeout)
	defer cancel()
	if err := Call(ctx, sock, "agent.read", params, &res); err != nil {
		return "", err
	}
	return res.Text, nil
}

// Stop stops the named session.
func (a *Actions) Stop(ctx context.Context, name string) error {
	if !ValidSessionName(name) {
		return fmt.Errorf("herdr: invalid session name %q", name)
	}
	bin, err := a.bin()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(ctx, a.StopTimeout)
	defer cancel()
	_, err = a.Run(ctx, bin, "session", "stop", name, "--json")
	return err
}

// Delete deletes the named session. The default session cannot be deleted.
func (a *Actions) Delete(ctx context.Context, name string) error {
	if name == "default" {
		return errors.New("herdr: cannot delete the default session")
	}
	if !ValidSessionName(name) {
		return fmt.Errorf("herdr: invalid session name %q", name)
	}
	bin, err := a.bin()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(ctx, a.StopTimeout)
	defer cancel()
	_, err = a.Run(ctx, bin, "session", "delete", name, "--json")
	return err
}

// withTimeout bounds ctx unless the caller already set a deadline.
func (a *Actions) withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// defaultRun executes bin, capturing capped stdout. A non-zero exit is an error
// that includes a capped stderr snippet. A call without a deadline gets
// a.Timeout.
func (a *Actions) defaultRun(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := a.withTimeout(ctx, a.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	stdout := &capWriter{limit: 2 << 20}
	stderr := &capWriter{limit: 8 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s: %w", ErrHerdrMissing, bin, err)
		}
		invocation := strings.Join(append([]string{bin}, args...), " ")
		if msg := strings.TrimSpace(stderr.buf.String()); msg != "" {
			return nil, fmt.Errorf("herdr: %s: %w: %s", invocation, err, msg)
		}
		return nil, fmt.Errorf("herdr: %s: %w", invocation, err)
	}
	return stdout.buf.Bytes(), nil
}

// defaultSpawn starts cmd in its own session and detaches it, so the terminal
// outlives this process.
func defaultSpawn(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// capWriter buffers up to limit bytes and silently drops the rest, so a chatty
// child cannot exhaust memory or fail the run.
type capWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if rem := w.limit - w.buf.Len(); rem > 0 {
		w.buf.Write(p[:min(len(p), rem)])
	}
	return len(p), nil
}
