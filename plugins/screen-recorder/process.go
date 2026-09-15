package recorder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxLogBytes = 64 << 10

// Proc is one owned recorder process. Signalling uses this PID only.
type Proc struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	path    string
	args    []string
	pid     int
	logs    []byte
	done    chan struct{}
	waitErr error
	running bool
}

// ProcInfo is one observed process, used by adoption.
type ProcInfo struct {
	PID  int
	Exe  string
	Args []string
}

// Scanner lists candidate processes. Tests inject a fake table.
type Scanner func() ([]ProcInfo, error)

func Start(ctx context.Context, path string, args []string, env []string) (*Proc, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	if env != nil {
		cmd.Env = env
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
		return nil, fmt.Errorf("recorder: start: %w", err)
	}
	p := &Proc{
		cmd:     cmd,
		path:    path,
		args:    append([]string{}, args...),
		pid:     cmd.Process.Pid,
		done:    make(chan struct{}),
		running: true,
	}
	go p.drain(stdout)
	go p.drain(stderr)
	go p.reap()
	return p, nil
}

func (p *Proc) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func (p *Proc) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Proc) Logs() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]byte, len(p.logs))
	copy(out, p.logs)
	return out
}

func (p *Proc) Done() <-chan struct{} { return p.done }

func (p *Proc) Save() error {
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	return syscall.Kill(pid, syscall.SIGUSR1)
}

func (p *Proc) Wait() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *Proc) Stop(wait time.Duration) error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	pid := p.pid
	cmd := p.cmd
	p.mu.Unlock()

	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	_ = syscall.Kill(pid, syscall.SIGINT)
	if cmd == nil {
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) {
			if syscall.Kill(pid, 0) != nil {
				p.mu.Lock()
				p.running = false
				p.mu.Unlock()
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = proc.Kill()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		return nil
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(wait):
		if err := proc.Kill(); err != nil {
			return err
		}
		<-p.done
		return nil
	}
}

func (p *Proc) reap() {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.waitErr = err
	p.running = false
	p.mu.Unlock()
	close(p.done)
}

func (p *Proc) drain(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.appendLog(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (p *Proc) appendLog(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logs = append(p.logs, b...)
	if len(p.logs) > maxLogBytes {
		p.logs = append([]byte{}, p.logs[len(p.logs)-maxLogBytes:]...)
	}
}

func listProcs() ([]ProcInfo, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []ProcInfo
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		exe, err := os.Readlink("/proc/" + e.Name() + "/exe")
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		raw, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil || len(raw) == 0 {
			continue
		}
		raw = bytes.TrimRight(raw, "\x00")
		parts := bytes.Split(raw, []byte{0})
		args := make([]string, 0, len(parts))
		for i, p := range parts {
			if i == 0 {
				continue
			}
			args = append(args, string(p))
		}
		out = append(out, ProcInfo{PID: pid, Exe: exe, Args: args})
	}
	return out, nil
}

func Adopt(scan Scanner, exe string, args []string) (*Proc, error) {
	list, err := scan()
	if err != nil {
		return nil, err
	}
	var hits []ProcInfo
	for _, info := range list {
		if sameExe(info.Exe, exe) && sameArgs(info.Args, args) {
			hits = append(hits, info)
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("recorder: no matching process")
	}
	if len(hits) > 1 {
		return nil, fmt.Errorf("recorder: %d matching processes", len(hits))
	}
	return &Proc{
		path:    hits[0].Exe,
		args:    append([]string{}, hits[0].Args...),
		pid:     hits[0].PID,
		done:    make(chan struct{}),
		running: true,
	}, nil
}

func sameExe(got, want string) bool {
	return canonPath(got) == canonPath(want)
}

func canonPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func sameArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
