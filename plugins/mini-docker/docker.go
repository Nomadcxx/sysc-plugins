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
	"sort"
	"strconv"
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

// Image mirrors one JSON line of `docker images --format {{json .}}`.
type Image struct {
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	ID           string `json:"ID"`
	CreatedSince string `json:"CreatedSince"`
	Size         string `json:"Size"`
	Containers   int    `json:"Containers"`
}

// Volume mirrors one JSON line of `docker volume ls --format {{json .}}`.
type Volume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Scope      string `json:"Scope"`
	Mountpoint string `json:"Mountpoint"`
}

// Network mirrors one JSON line of `docker network ls --format {{json .}}`.
type Network struct {
	Name   string `json:"Name"`
	ID     string `json:"ID"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
}

// RunOpts contains validated run-form values. Each value remains a separate
// argv item; the CLI never builds a shell command.
type RunOpts struct {
	Image       string
	Name        string
	Environment []string
	Port        string
	Publish     bool
	Network     string
}

// Docker runs the docker CLI. It is an interface so the service can be tested
// without a docker daemon.
type Docker interface {
	List(ctx context.Context) ([]Container, int, error)
	Images(ctx context.Context) ([]Image, int, error)
	Volumes(ctx context.Context) ([]Volume, int, error)
	Networks(ctx context.Context) ([]Network, int, error)
	ImageExposedPorts(ctx context.Context, image string) ([]int, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	Rmi(ctx context.Context, id string) error
	VolRm(ctx context.Context, name string) error
	NetRm(ctx context.Context, id string) error
	Run(ctx context.Context, opts RunOpts) error
}

// CLI implements Docker by shelling out.
type CLI struct{}

const listTimeout = 30 * time.Second

// actionTimeout caps mutating docker commands. A command that never answers
// must become a readable error, not a pill button disabled forever.
// ponytail: one cap for all mutations (a var so tests can shorten it);
// per-action tuning only if a real workload ever shows it matters.
var actionTimeout = 15 * time.Second

// maxOutputBytes bounds stdout from Docker list and inspect commands.
// ponytail: fixed ceiling; stream results if real Docker output ever exceeds it.
const maxOutputBytes = 1 << 20

func (CLI) List(ctx context.Context) ([]Container, int, error) {
	out, err := runDockerOutput(ctx, "list", "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, 0, err
	}
	containers, skipped := parseContainers(out)
	return containers, skipped, nil
}

func (CLI) Images(ctx context.Context) ([]Image, int, error) {
	out, err := runDockerOutput(ctx, "list", "images", "--format", "{{json .}}")
	if err != nil {
		return nil, 0, err
	}
	items, skipped := parseImages(out)
	return items, skipped, nil
}

func (CLI) Volumes(ctx context.Context) ([]Volume, int, error) {
	out, err := runDockerOutput(ctx, "list", "volume", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, 0, err
	}
	items, skipped := parseVolumes(out)
	return items, skipped, nil
}

func (CLI) Networks(ctx context.Context) ([]Network, int, error) {
	out, err := runDockerOutput(ctx, "list", "network", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, 0, err
	}
	items, skipped := parseNetworks(out)
	return items, skipped, nil
}

func (CLI) ImageExposedPorts(ctx context.Context, image string) ([]int, error) {
	out, err := runDockerOutput(ctx, "image inspect", "image", "inspect", "--format", "{{json .Config.ExposedPorts}}", image)
	if err != nil {
		return nil, err
	}
	return parseExposedPorts(out)
}

func parseContainers(data []byte) ([]Container, int) {
	return parseJSONLines[Container](data)
}

func parseImages(data []byte) ([]Image, int) {
	return parseJSONLines[Image](data)
}

func parseVolumes(data []byte) ([]Volume, int) {
	return parseJSONLines[Volume](data)
}

func parseNetworks(data []byte) ([]Network, int) {
	return parseJSONLines[Network](data)
}

func parseJSONLines[T any](data []byte) ([]T, int) {
	var items []T
	skipped := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			skipped++
			continue
		}
		items = append(items, item)
	}
	return items, skipped
}

func parseExposedPorts(data []byte) ([]int, error) {
	var exposed map[string]json.RawMessage
	if err := json.Unmarshal(data, &exposed); err != nil {
		return nil, err
	}
	ports := make([]int, 0, len(exposed))
	for key := range exposed {
		portText, protocol, ok := strings.Cut(key, "/")
		if !ok || protocol != "tcp" || portText == "" {
			continue
		}
		valid := true
		for _, digit := range portText {
			if digit < '0' || digit > '9' {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err == nil && port >= 1 && port <= 65535 {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports, nil
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

func (CLI) Remove(ctx context.Context, id string) error {
	return runDocker(ctx, "rm", id)
}

func (CLI) Rmi(ctx context.Context, id string) error {
	return runDocker(ctx, "rmi", id)
}

func (CLI) VolRm(ctx context.Context, name string) error {
	return runDocker(ctx, "volume", "rm", name)
}

func (CLI) NetRm(ctx context.Context, id string) error {
	return runDocker(ctx, "network", "rm", id)
}

func (CLI) Run(ctx context.Context, opts RunOpts) error {
	return runDocker(ctx, runArgs(opts)...)
}

func runArgs(opts RunOpts) []string {
	args := []string{"run", "-d"}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	for _, env := range opts.Environment {
		args = append(args, "-e", env)
	}
	if opts.Publish && opts.Port != "" {
		args = append(args, "-p", opts.Port+":"+opts.Port)
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}
	if opts.Image != "" {
		args = append(args, opts.Image)
	}
	return args
}

func runDockerOutput(ctx context.Context, operation string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, diagnose(err, stderr.String())
	}
	if err := cmd.Start(); err != nil {
		return nil, diagnose(err, stderr.String())
	}
	// Read one extra byte to detect truncation. Stop an oversized writer before
	// Wait; otherwise it can block forever on the full pipe.
	out, readErr := io.ReadAll(io.LimitReader(pipe, maxOutputBytes+1))
	if readErr != nil {
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		if waitErr != nil {
			return nil, outputFailure(ctx, waitErr, stderr.String(), operation)
		}
		return nil, outputFailure(ctx, readErr, stderr.String(), operation)
	}
	if len(out) > maxOutputBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("docker %s output too large (limit %d bytes)", operation, maxOutputBytes)
	}
	if err := cmd.Wait(); err != nil {
		return nil, outputFailure(ctx, err, stderr.String(), operation)
	}
	return out, nil
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

// outputFailure prefers our own timeout over the process's kill signal.
func outputFailure(ctx context.Context, err error, stderr, operation string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("docker %s timed out", operation)
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
