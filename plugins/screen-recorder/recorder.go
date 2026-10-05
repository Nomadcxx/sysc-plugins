package recorder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Mode string

const (
	Unavailable  Mode = "unavailable"
	Idle         Mode = "idle"
	Recording    Mode = "recording"
	ReplayActive Mode = "replay-active"
	Stopping     Mode = "stopping"
	Failed       Mode = "failed"
	Adopted      Mode = "adopted"
)

type Snapshot struct {
	Mode     Mode
	Artifact string
	Err      string
	Logs     string
	Elapsed  time.Duration
}

type Ownership struct {
	PID  int
	Exe  string
	Args []string
}

type Options struct {
	Exe      string
	LookPath func(string) (string, error)
	Start    func(ctx context.Context, path string, args, env []string) (*Proc, error)
	Scan     Scanner
	Env      []string
	StopWait time.Duration
	Now      func() time.Time
}

type command struct {
	kind   int
	output string
	own    Ownership
	cfg    Config
}

const (
	cmdRecord = iota
	cmdReplay
	cmdSave
	cmdRecover
	cmdRetry
	cmdReconfig
)

type Recorder struct {
	cfg  Config
	opt  Options
	path string

	mu      sync.Mutex
	snap    Snapshot
	own     Ownership
	closed  bool
	started time.Time

	proc      *Proc
	dest      string
	replayDir string

	cmds    chan command
	quit    chan struct{}
	done    chan struct{}
	updates chan Snapshot
}

func New(cfg Config, opt Options) *Recorder {
	if opt.LookPath == nil {
		opt.LookPath = exec.LookPath
	}
	if opt.Start == nil {
		opt.Start = Start
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.StopWait <= 0 {
		opt.StopWait = 2 * time.Second
	}
	r := &Recorder{
		cfg:     cfg,
		opt:     opt,
		cmds:    make(chan command, 1),
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
		updates: make(chan Snapshot, 1),
		snap:    Snapshot{Mode: Idle},
	}
	path, err := opt.LookPath("gpu-screen-recorder")
	if opt.Exe != "" {
		path = opt.Exe
		err = nil
	}
	if err != nil || path == "" {
		r.snap.Mode = Unavailable
		r.snap.Err = "gpu-screen-recorder is not installed or not on PATH"
	} else {
		r.path = path
	}
	go r.loop()
	return r
}

func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snap
	if !r.started.IsZero() {
		s.Elapsed = r.opt.Now().Sub(r.started)
	}
	return s
}

func (r *Recorder) Ownership() Ownership {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.own
}

func (r *Recorder) Updates() <-chan Snapshot { return r.updates }

func (r *Recorder) ToggleRecord(output string) { r.send(command{kind: cmdRecord, output: output}) }
func (r *Recorder) ToggleReplay(output string) { r.send(command{kind: cmdReplay, output: output}) }
func (r *Recorder) SaveReplay()                { r.send(command{kind: cmdSave}) }
func (r *Recorder) Recover(own Ownership)      { r.send(command{kind: cmdRecover, own: own}) }
func (r *Recorder) Retry()                     { r.send(command{kind: cmdRetry}) }
func (r *Recorder) Reconfigure(cfg Config)     { r.send(command{kind: cmdReconfig, cfg: cfg}) }

func (r *Recorder) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.quit)
	r.mu.Unlock()
	<-r.done
}

func (r *Recorder) send(c command) {
	select {
	case r.cmds <- c:
	case <-r.quit:
	}
}

func (r *Recorder) loop() {
	defer close(r.done)
	for {
		var wait <-chan struct{}
		if r.proc != nil {
			wait = r.proc.Done()
		}
		select {
		case <-r.quit:
			r.halt()
			return
		case <-wait:
			r.onExit()
		case c := <-r.cmds:
			r.handle(c)
			if c.kind == cmdRecord || c.kind == cmdReplay {
				r.drain()
			}
		}
	}
}

func (r *Recorder) drain() {
	for {
		select {
		case <-r.cmds:
		default:
			return
		}
	}
}

func (r *Recorder) handle(c command) {
	switch c.kind {
	case cmdRecord:
		r.toggleRecord(c.output)
	case cmdReplay:
		r.toggleReplay(c.output)
	case cmdSave:
		r.saveReplay()
	case cmdRecover:
		r.recover(c.own)
	case cmdRetry:
		if r.mode() == Failed {
			r.set(Snapshot{Mode: Idle})
		}
	case cmdReconfig:
		r.cfg = c.cfg
		if r.mode() == Failed {
			r.set(Snapshot{Mode: Idle})
		}
	}
}

func (r *Recorder) toggleRecord(output string) {
	switch r.mode() {
	case Recording, Adopted:
		r.stopRecord()
	case Idle, Failed:
		r.startRecord(output)
	}
}

func (r *Recorder) toggleReplay(output string) {
	switch r.mode() {
	case ReplayActive:
		r.stopReplay()
	case Idle, Failed:
		if r.cfg.ReplayEnabled {
			r.startReplay(output)
		}
	}
}

func (r *Recorder) startRecord(output string) {
	dir := expandHome(r.cfg.Directory)
	dest, err := destPath(dir, r.cfg.FilenamePattern, r.opt.Now())
	if err != nil {
		r.fail(err)
		return
	}
	args, err := r.cfg.RecordArgs(output, dest)
	if err != nil {
		r.fail(err)
		return
	}
	proc, err := r.opt.Start(context.Background(), r.path, args, r.opt.Env)
	if err != nil {
		r.fail(err)
		return
	}
	r.proc = proc
	r.dest = dest
	if !r.waitReady(proc) {
		r.fail(fmt.Errorf("recorder: process never became ready"))
		return
	}
	r.remember()
	r.set(Snapshot{Mode: Recording})
}

func (r *Recorder) startReplay(output string) {
	dir := expandHome(r.cfg.Directory)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.fail(err)
		return
	}
	args, err := r.cfg.ReplayArgs(output, dir)
	if err != nil {
		r.fail(err)
		return
	}
	proc, err := r.opt.Start(context.Background(), r.path, args, r.opt.Env)
	if err != nil {
		r.fail(err)
		return
	}
	r.proc = proc
	r.replayDir = dir
	if !r.waitReady(proc) {
		r.fail(fmt.Errorf("recorder: process never became ready"))
		return
	}
	r.remember()
	r.set(Snapshot{Mode: ReplayActive})
}

func (r *Recorder) stopRecord() {
	prev := r.Snapshot()
	r.set(Snapshot{Mode: Stopping, Artifact: prev.Artifact, Err: prev.Err})
	r.halt()
	if err := verifyArtifact(r.dest); err != nil {
		r.fail(err)
		return
	}
	r.set(Snapshot{Mode: Idle, Artifact: r.dest})
}

func (r *Recorder) stopReplay() {
	r.set(Snapshot{Mode: Stopping, Artifact: r.snap.Artifact})
	r.halt()
	r.set(Snapshot{Mode: Idle, Artifact: r.snap.Artifact})
}

func (r *Recorder) saveReplay() {
	if r.mode() != ReplayActive || r.proc == nil {
		return
	}
	signaled := time.Now()
	logged := len(r.proc.Logs())
	_ = r.proc.Save()
	dest, err := destPath(r.replayDir, r.cfg.ReplayFilenamePattern, r.opt.Now())
	if err != nil {
		r.fail(err)
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	if r.opt.StopWait > 2*time.Second {
		deadline = time.Now().Add(r.opt.StopWait)
	}
	for time.Now().Before(deadline) {
		logs := r.proc.Logs()
		// The log buffer drops its oldest bytes at maxLogBytes, so a length
		// that stopped growing can still hide the new line. Rescan the whole
		// buffer then; claimReplay's mtime check rejects stale lines.
		if logged > len(logs) || len(logs) == maxLogBytes {
			logged = 0
		}
		if path, err := claimReplay(r.replayDir, logs[logged:], signaled, dest); err == nil {
			r.mu.Lock()
			r.snap.Artifact = path
			r.mu.Unlock()
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	r.fail(fmt.Errorf("recorder: replay save produced no file"))
}

func (r *Recorder) recover(own Ownership) {
	if r.mode() != Idle && r.mode() != Unavailable {
		return
	}
	scan := r.opt.Scan
	if scan == nil {
		scan = listProcs
	}
	proc, err := Adopt(scan, own.Exe, own.Args)
	if err != nil {
		r.fail(err)
		return
	}
	r.proc = proc
	// The adopted backend still writes to the -o path it was started with;
	// restoring it lets a post-restart stop verify and publish the artifact.
	r.dest = argValue(own.Args, "-o")
	r.remember()
	r.set(Snapshot{Mode: Adopted})
}

func (r *Recorder) halt() {
	if r.proc == nil {
		return
	}
	_ = r.proc.Stop(r.opt.StopWait)
	r.proc = nil
	r.mu.Lock()
	r.own = Ownership{}
	r.mu.Unlock()
}

func (r *Recorder) remember() {
	if r.proc == nil {
		return
	}
	r.mu.Lock()
	r.own = Ownership{PID: r.proc.PID(), Exe: r.proc.path, Args: append([]string{}, r.proc.args...)}
	r.mu.Unlock()
}

func (r *Recorder) waitReady(p *Proc) bool {
	deadline := time.Now().Add(2 * time.Second)
	var runningSince time.Time
	for time.Now().Before(deadline) {
		if containsReady(p.Logs()) {
			return true
		}
		if !p.Running() {
			return false
		}
		if runningSince.IsZero() {
			runningSince = time.Now()
		}
		if time.Since(runningSince) >= 100*time.Millisecond {
			// ponytail: gpu-screen-recorder 6.0.1 never prints ready; a live
			// process for 100ms is the handshake. Use an explicit line if a
			// later GSR grows one.
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return p.Running()
}

func containsReady(logs []byte) bool {
	return bytes.Contains(logs, []byte("ready"))
}

func (r *Recorder) onExit() {
	proc := r.proc
	r.proc = nil
	r.mu.Lock()
	r.own = Ownership{}
	r.mu.Unlock()
	if r.mode() == Stopping {
		return
	}
	err := fmt.Errorf("recorder: process exited")
	if proc != nil {
		if waitErr := proc.Wait(); waitErr != nil {
			err = waitErr
		}
	}
	r.fail(err)
}

func (r *Recorder) fail(err error) {
	logs := ""
	if r.proc != nil {
		logs = string(r.proc.Logs())
	}
	r.halt()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	r.set(Snapshot{Mode: Failed, Err: msg, Logs: logs})
}

func (r *Recorder) mode() Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap.Mode
}

func (r *Recorder) set(s Snapshot) {
	r.mu.Lock()
	live := s.Mode == Recording || s.Mode == ReplayActive || s.Mode == Adopted
	wasLive := r.snap.Mode == Recording || r.snap.Mode == ReplayActive || r.snap.Mode == Adopted
	if live && !wasLive {
		r.started = r.opt.Now()
	} else if !live {
		r.started = time.Time{}
	}
	s.Elapsed = 0
	r.snap = s
	r.mu.Unlock()
	select {
	case r.updates <- s:
	default:
		select {
		case <-r.updates:
		default:
		}
		select {
		case r.updates <- s:
		default:
		}
	}
}

func destPath(dir, pattern string, now time.Time) (string, error) {
	dir = expandHome(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := formatPattern(pattern, now)
	if filepath.Ext(base) == "" {
		base += ".mp4"
	}
	base = filepath.Base(base)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	candidate := filepath.Join(dir, name+ext)
	for n := 1; ; n++ {
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, n, ext))
	}
}

func formatPattern(pattern string, t time.Time) string {
	repl := [][2]string{
		{"%Y", t.Format("2006")},
		{"%m", t.Format("01")},
		{"%d", t.Format("02")},
		{"%H", t.Format("15")},
		{"%M", t.Format("04")},
		{"%S", t.Format("05")},
	}
	s := pattern
	for _, r := range repl {
		s = strings.ReplaceAll(s, r[0], r[1])
	}
	return s
}

func expandHome(dir string) string {
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, dir[2:])
		}
	}
	return dir
}

// claimReplay renames the file gpu-screen-recorder just saved onto dest.
// logs are the bytes written after SIGUSR1. The recorder prints that path
// alone on stdout: Replay_YYYY-MM-DD_HH-MM-SS.ext inside the -o directory.
// A line is claimed only when the name matches that pattern, the file is a
// non-empty regular file in dir, and its mtime is not before signaled.
func claimReplay(dir string, logs []byte, signaled time.Time, dest string) (string, error) {
	dir = filepath.Clean(dir)
	var claimed string
	rest := logs
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(rest[:i])
		rest = rest[i+1:]
		if len(line) == 0 {
			continue
		}
		path, ok := replayPath(dir, string(line))
		if !ok || !replayReady(path, signaled) {
			continue
		}
		claimed = path
	}
	if claimed == "" {
		return "", fmt.Errorf("recorder: no new replay file")
	}
	if claimed != dest {
		if err := os.Rename(claimed, dest); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func replayPath(dir, line string) (string, bool) {
	p := filepath.Clean(line)
	if !filepath.IsAbs(p) {
		if filepath.IsAbs(dir) {
			return "", false
		}
		p = filepath.Clean(filepath.Join(dir, p))
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel != filepath.Base(p) || !isReplayName(rel) {
		return "", false
	}
	return p, true
}

func isReplayName(name string) bool {
	const prefix = "Replay_"
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	ext := filepath.Ext(rest)
	if len(ext) < 2 {
		return false
	}
	stamp := strings.TrimSuffix(rest, ext)
	_, err := time.Parse("2006-01-02_15-04-05", stamp)
	return err == nil && stamp != ""
}

func replayReady(path string, signaled time.Time) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return false
	}
	return !st.ModTime().Truncate(time.Second).Before(signaled.Truncate(time.Second))
}

func verifyArtifact(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("recorder: missing artifact: %w", err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("recorder: zero-byte artifact")
	}
	return nil
}

// argValue returns the value following flag in args, or "".
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
