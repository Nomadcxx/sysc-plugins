// Package minidocker ports the Noctalia community plugin "mini-docker":
// running-container count in the bar and a container management panel driven
// by the docker CLI.
package minidocker

import (
	"context"
	"encoding/json"
	"fmt"
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

func (CLI) List(ctx context.Context) ([]Container, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, err
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
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}
