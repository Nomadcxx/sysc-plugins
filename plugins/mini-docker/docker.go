// Package minidocker ports the Noctalia community plugin "mini-docker":
// running-container count in the bar and a container management panel driven
// by the docker CLI.
package minidocker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Container mirrors one JSON line of `docker ps -a --format {{json .}}`.
type Container struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

// Running reports whether the container is up.
func (c Container) Running() bool { return c.State == "running" }

// Docker runs the docker CLI. It is an interface so the service can be tested
// without a docker daemon.
type Docker interface {
	List(ctx context.Context) ([]Container, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
}

// CLI implements Docker by shelling out.
type CLI struct{}

const listTimeout = 30 * time.Second

// actionTimeout caps start/stop/restart. A docker action that never answers
// must become a readable error, not a pill button disabled forever.
// ponytail: single cap for all three actions (a var so tests can shorten
// it); per-action tuning only if a real workload ever shows it matters.
var actionTimeout = 15 * time.Second

// maxListBytes bounds the stdout read at the trust boundary: docker ps -a
// with hundreds of containers is well under 1 MiB, and an unbounded read of
// an external process's output has no ceiling worth defending.
// ponytail: ceiling for a per-5s poll; if exotic outputs ever appear, stream
// instead of buffer.
const maxListBytes = 1 << 20

func (CLI) List(ctx context.Context) ([]Container, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, diagnose(err, stderr.String())
	}
	if err := cmd.Start(); err != nil {
		return nil, diagnose(err, stderr.String())
	}
	out, readErr := io.ReadAll(io.LimitReader(pipe, maxListBytes))
	if err := cmd.Wait(); err != nil {
		return nil, listFailure(ctx, err, stderr.String())
	}
	if readErr != nil {
		return nil, listFailure(ctx, readErr, stderr.String())
	}
	var containers []Container
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c Container
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		containers = append(containers, c)
	}
	return containers, nil
}

func (CLI) Start(ctx context.Context, id string) error {
	return runDocker(ctx, "start", id)
}

func (CLI) Stop(ctx context.Context, id string) error {
	return runDocker(ctx, "stop", id)
}

func (CLI) Restart(ctx context.Context, id string) error {
	return runDocker(ctx, "restart", id)
}

func runDocker(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The context expiry surfaces as the process's kill signal on some
		// Go versions; our own timeout is the cause worth naming.
		if ctx.Err() != nil {
			return errors.New("docker action timed out")
		}
		return diagnose(err, stderr.String())
	}
	return nil
}

// listFailure names a List failure, preferring our own timeout over the
// process's kill signal.
func listFailure(ctx context.Context, err error, stderr string) error {
	if ctx.Err() != nil {
		return errors.New("docker list timed out")
	}
	return diagnose(err, stderr)
}

// diagnose turns a docker CLI failure into a message a user can act on.
// The bare exec error ("exit status 1") says nothing; the two failures that
// actually happen (daemon down, docker not installed) and the stderr line
// for everything else cover the space.
// ponytail: matches on the stderr text docker actually prints; if a docker
// release changes the wording, the fallback still carries the stderr tail.
func diagnose(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if len(msg) > 500 {
		msg = msg[len(msg)-500:] // tail: the error line is last
	}
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return errors.New("docker command not found")
	case strings.Contains(msg, "Cannot connect to the Docker daemon"):
		return errors.New("Docker daemon not running")
	case msg != "":
		return fmt.Errorf("docker: %s", msg)
	default:
		return fmt.Errorf("docker: %w", err)
	}
}
