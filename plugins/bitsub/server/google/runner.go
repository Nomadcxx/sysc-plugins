// Package google mirrors subscriptions to a dedicated Google Calendar and
// sends mail, both through the gog CLI.
package google

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Runner runs one gog command and returns its stdout.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
	RunStdin(ctx context.Context, stdin string, args ...string) ([]byte, error)
}

const (
	defaultTimeout = 60 * time.Second
	stderrLimit    = 2 << 10
)

// ExecRunner runs gog with argv only (no shell), always as an explicit
// account in JSON mode, never prompting.
type ExecRunner struct {
	Bin, Account string
	Timeout      time.Duration
}

func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.run(ctx, "", false, args)
}

func (r ExecRunner) RunStdin(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	return r.run(ctx, stdin, true, args)
}

func (r ExecRunner) run(ctx context.Context, stdin string, withStdin bool, args []string) ([]byte, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append([]string{"--account", r.Account, "--json", "--no-input"}, args...)
	cmd := exec.CommandContext(ctx, r.Bin, argv...)
	// On timeout kill the whole process group, and stop waiting on pipes a
	// stray child might still hold open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if withStdin {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if len(msg) > stderrLimit {
			msg = msg[:stderrLimit]
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("gog %s: %w: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(msg))
	}
	return stdout.Bytes(), nil
}
