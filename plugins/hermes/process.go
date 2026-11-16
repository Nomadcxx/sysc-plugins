package hermes

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// MaxBridgeLogBytes caps bridge.log; one older copy is kept as bridge.log.1.
const MaxBridgeLogBytes = 1 << 20

// DefaultStopGrace is how long a SIGTERM'd bridge gets before SIGKILL.
const DefaultStopGrace = 3 * time.Second

// SpawnSpec is everything needed to start the bundled bridge in local mode.
type SpawnSpec struct {
	Python        string
	Script        string
	StateFile     string
	HermesHome    string
	HermesCommand string
	// LogPath receives the child's stdout and stderr, never the protocol pipe.
	LogPath string
}

// Argv is the exec argv. There is no shell, so nothing in it is interpreted.
func (s SpawnSpec) Argv() []string {
	return []string{
		s.Python, s.Script,
		"--host", LocalHost, "--port", strconv.Itoa(LocalPort),
		"--state-file", s.StateFile,
		"--hermes-home", s.HermesHome,
		"--hermes-command", s.HermesCommand,
	}
}

// Process is a bridge child the plugin started and therefore owns.
type Process interface {
	Pid() int
	Signal(os.Signal) error
	// Done closes when the process has exited and been reaped.
	Done() <-chan struct{}
	// ExitCode is valid after Done: the exit status, or 128+signal.
	ExitCode() int
}

// Starter starts bridge children. Tests substitute a fake.
type Starter interface {
	Start(SpawnSpec) (Process, error)
}

// ExecStarter starts the bridge with os/exec. The child stays in the
// plugin's process group (no setsid, no Setpgid), so the shell's forced
// stop, which signals the whole group, reaches it too.
type ExecStarter struct{}

func (ExecStarter) Start(spec SpawnSpec) (Process, error) {
	if spec.Python == "" || spec.Script == "" {
		return nil, errors.New("hermes: no python command or bridge script")
	}
	argv := spec.Argv()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = nil
	logPath := spec.LogPath
	if logPath == "" {
		logPath = filepath.Join(CacheHome(), "sysc-hermes", "bridge.log")
	}
	log, err := openBridgeLog(logPath, MaxBridgeLogBytes)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	p := &execProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		log.Close()
		p.code = exitCodeOf(cmd.ProcessState, err)
		close(p.done)
	}()
	return p, nil
}

type execProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
}

func (p *execProcess) Pid() int              { return p.cmd.Process.Pid }
func (p *execProcess) Done() <-chan struct{} { return p.done }
func (p *execProcess) ExitCode() int {
	<-p.done
	return p.code
}

func (p *execProcess) Signal(sig os.Signal) error {
	select {
	case <-p.done:
		return os.ErrProcessDone
	default:
	}
	return p.cmd.Process.Signal(sig)
}

func exitCodeOf(state *os.ProcessState, err error) int {
	if state == nil {
		if err != nil {
			return -1
		}
		return 0
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return state.ExitCode()
}

// StopProcess sends SIGTERM, waits up to grace, then SIGKILL and waits for
// the reap. Only ever call it on a process this plugin started.
func StopProcess(p Process, grace time.Duration) {
	if p == nil {
		return
	}
	select {
	case <-p.Done():
		return
	default:
	}
	_ = p.Signal(syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.Done():
		return
	case <-timer.C:
	}
	_ = p.Signal(syscall.SIGKILL)
	<-p.Done()
}

// bridgeLog is a size-capped log file rotated once to <path>.1.
type bridgeLog struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func openBridgeLog(path string, max int64) (*bridgeLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &bridgeLog{path: path, max: max}
	if info, err := os.Stat(path); err == nil && info.Size() >= max {
		if err := l.rotate(); err != nil {
			return nil, err
		}
	}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *bridgeLog) open() error {
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// The file may predate us with a looser mode.
	_ = f.Chmod(0o600)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

func (l *bridgeLog) rotate() error {
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Chmod(l.path+".1", 0o600)
	return nil
}

func (l *bridgeLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	if l.size > 0 && l.size+int64(len(b)) > l.max {
		if err := l.rotate(); err != nil {
			return 0, err
		}
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	if int64(len(b)) > l.max {
		// A single oversized write keeps only its tail.
		b = b[int64(len(b))-l.max:]
	}
	n, err := l.f.Write(b)
	l.size += int64(n)
	return n, err
}

func (l *bridgeLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
