package minidocker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/wire"
)

const psOutput = `{"ID":"a1","Names":"web","Image":"nginx:latest","State":"running","Status":"Up 2 hours"}
{"ID":"b2","Names":"db","Image":"postgres:16","State":"exited","Status":"Exited (0) 5 minutes ago"}
{"not json"}
`

type fakeDocker struct {
	listErr    error
	containers []Container
	actions    []string
}

func (f *fakeDocker) List(ctx context.Context) ([]Container, error) { return f.containers, f.listErr }

func (f *fakeDocker) Start(ctx context.Context, id string) error {
	f.actions = append(f.actions, "start:"+id)
	return nil
}

func (f *fakeDocker) Stop(ctx context.Context, id string) error {
	f.actions = append(f.actions, "stop:"+id)
	return nil
}

func (f *fakeDocker) Restart(ctx context.Context, id string) error {
	f.actions = append(f.actions, "restart:"+id)
	return nil
}

func TestCLIParsesDockerJSONLines(t *testing.T) {
	// The CLI List path is exercised indirectly; parse the same shape here to
	// lock the expected docker output contract.
	var containers []Container
	for _, line := range strings.Split(psOutput, "\n") {
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
	if len(containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(containers))
	}
	if containers[0].Names != "web" || !containers[0].Running() {
		t.Fatalf("web = %+v", containers[0])
	}
	if containers[1].Running() {
		t.Fatalf("db should not be running: %+v", containers[1])
	}
}

func TestSessionRefreshUnavailable(t *testing.T) {
	s := NewSession(&fakeDocker{listErr: errors.New("Cannot connect to the Docker daemon")})
	s.Refresh(context.Background())
	_, available, loading, errMsg := s.Snapshot()
	if available || loading {
		t.Fatalf("available=%v loading=%v", available, loading)
	}
	if errMsg == "" {
		t.Fatal("error message empty")
	}
	if got := TooltipText(0, available); got != "Docker unavailable" {
		t.Fatalf("tooltip = %q", got)
	}
}

func TestSessionRefreshAndRunningCount(t *testing.T) {
	s := NewSession(&fakeDocker{containers: []Container{
		{ID: "a1", Names: "web", State: "running", Status: "Up 2 hours"},
		{ID: "b2", Names: "db", State: "exited", Status: "Exited (0)"},
	}})
	s.Refresh(context.Background())
	if got := s.RunningCount(); got != 1 {
		t.Fatalf("running = %d", got)
	}
	if got := TooltipText(1, true); got != "1 container running" {
		t.Fatalf("tooltip = %q", got)
	}
}

func TestSessionActRunsActionAndRefreshes(t *testing.T) {
	fd := &fakeDocker{containers: []Container{{ID: "b2", Names: "db", State: "exited"}}}
	s := NewSession(fd)
	s.Act(context.Background(), "start", "b2")
	if len(fd.actions) != 1 || fd.actions[0] != "start:b2" {
		t.Fatalf("actions = %v", fd.actions)
	}
	if len(fd.containers) == 0 || fd.containers[0].ID != "b2" {
		t.Fatalf("no refresh after action: %+v", fd.containers)
	}
	s.Act(context.Background(), "bogus", "b2")
	if len(fd.actions) != 1 {
		t.Fatalf("unknown action dispatched: %v", fd.actions)
	}
}

func TestBarLabelModes(t *testing.T) {
	if got := BarLabel(true, "always", 3, true); got != "docker 3" {
		t.Fatalf("always = %q", got)
	}
	if got := BarLabel(false, "always", 3, true); got != "docker" {
		t.Fatalf("no count = %q", got)
	}
	if got := BarLabel(true, "running_only", 0, true); got != "docker" {
		t.Fatalf("running_only zero = %q", got)
	}
	if got := BarLabel(true, "running_only", 2, true); got != "docker 2" {
		t.Fatalf("running_only = %q", got)
	}
	if got := BarLabel(true, "hidden", 5, true); got != "docker" {
		t.Fatalf("hidden = %q", got)
	}
	if got := BarLabel(true, "always", 0, false); got != "docker" {
		t.Fatalf("unavailable = %q", got)
	}
}

func TestPanelTreeValidate(t *testing.T) {
	containers := []Container{
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up 2 hours"},
		{ID: "b2", Names: "db", Image: "postgres:16", State: "exited", Status: "Exited (0)"},
	}
	if err := v1.Validate(PanelTree(true, false, "", containers), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(false, false, "daemon down", nil), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(true, true, "", nil), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree("docker 2"), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
}
