package minidocker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/v1"
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

func TestCLIDiagnosesFailures(t *testing.T) {
	t.Run("daemon down reads as daemon down", func(t *testing.T) {
		bin := t.TempDir()
		writeFakeDocker(t, bin, "#!/bin/sh\necho 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock' >&2\nexit 1\n")
		_, err := CLI{}.List(context.Background())
		if err == nil || err.Error() != "Docker daemon not running" {
			t.Fatalf("err = %v, want the daemon diagnosis", err)
		}
	})

	t.Run("missing binary reads as missing binary", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := CLI{}.List(context.Background())
		if err == nil || err.Error() != "docker command not found" {
			t.Fatalf("err = %v, want the not-found diagnosis", err)
		}
	})

	t.Run("action errors carry stderr", func(t *testing.T) {
		bin := t.TempDir()
		writeFakeDocker(t, bin, "#!/bin/sh\necho 'Error response from daemon: no such container' >&2\nexit 1\n")
		err := CLI{}.Start(context.Background(), "a1")
		if err == nil || !strings.Contains(err.Error(), "no such container") {
			t.Fatalf("err = %v, want stderr carried through", err)
		}
	})

	t.Run("hung action is capped", func(t *testing.T) {
		oldCap := actionTimeout
		actionTimeout = 300 * time.Millisecond
		defer func() { actionTimeout = oldCap }()

		bin := t.TempDir()
		// A docker that never answers (pure-shell spin: PATH holds nothing
		// else). The action must come back with a readable timeout error.
		writeFakeDocker(t, bin, "#!/bin/sh\ni=0\nwhile [ $i -lt 100000000 ]; do i=$((i+1)); done\n")
		start := time.Now()
		err := CLI{}.Stop(context.Background(), "a1")
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("err = %v, want the timeout diagnosis", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("action took %v, timeout cap not applied", elapsed)
		}
	})
}

func writeFakeDocker(t *testing.T, dir, script string) {
	t.Helper()
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
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
	_, available, loading, listErr, _, _ := s.Snapshot()
	if available || loading {
		t.Fatalf("available=%v loading=%v", available, loading)
	}
	if listErr == "" {
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

func TestActErrorSurvivesItsOwnRefresh(t *testing.T) {
	// A failed action must not be erased by the refresh that follows it:
	// the daemon is up, the List succeeds, and the old code cleared the
	// error anyway - the user clicked stop and saw nothing.
	fd := &failStartCLI{failStart: true, list: []Container{{ID: "b2", Names: "db", State: "exited"}}}
	s := NewSession(fd)
	s.Act(context.Background(), "start", "b2")
	_, _, _, _, actErr, _ := s.Snapshot()
	if actErr == "" {
		t.Fatal("actErr empty after a failed action")
	}
}

func TestActErrorClearsOnNextSuccess(t *testing.T) {
	fd := &failStartCLI{list: []Container{{ID: "b2", Names: "db", State: "exited"}}}
	s := NewSession(fd)
	s.Act(context.Background(), "start", "b2")
	fd.failStart = false
	s.Act(context.Background(), "start", "b2")
	_, _, _, _, actErr, _ := s.Snapshot()
	if actErr != "" {
		t.Fatalf("actErr = %q, want cleared by the next successful action", actErr)
	}
}

func TestPanelDisablesButtonsWhileActing(t *testing.T) {
	s := NewSession(&slowActionCLI{
		failStartCLI: failStartCLI{list: []Container{
			{ID: "a1", Names: "web", State: "running"},
			{ID: "b2", Names: "db", State: "exited"},
		}},
		delay: 150 * time.Millisecond,
	})
	go s.Act(context.Background(), "stop", "a1")

	// While the action is in flight, its container's buttons are disabled
	// and the other container's are not.
	deadline := time.Now().Add(2 * time.Second)
	var disabled []string
	for time.Now().Before(deadline) {
		containers, _, _, _, _, actingID := s.Snapshot()
		if actingID != "" {
			tree := PanelTree(true, false, "", "", actingID, containers)
			disabled = nil
			walkNodes(tree, func(n *v1.Node) {
				if n.Disabled {
					disabled = append(disabled, n.ID)
				}
			})
			for _, id := range disabled {
				if !strings.HasPrefix(id, "stop:a1") && !strings.HasPrefix(id, "restart:a1") {
					t.Fatalf("disabled node %q is not the acting container's", id)
				}
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actingID never became visible during the action")
}

func walkNodes(n *v1.Node, fn func(*v1.Node)) {
	fn(n)
	for _, c := range n.Children {
		walkNodes(c, fn)
	}
}

// failStartCLI fails Start until told otherwise; List always works, which is
// exactly the shape that exposed the erasure bug.
type failStartCLI struct {
	list      []Container
	failStart bool
}

func (f *failStartCLI) List(context.Context) ([]Container, error) { return f.list, nil }

func (f *failStartCLI) Start(context.Context, string) error {
	if f.failStart {
		return errors.New("Error response from daemon: conflict")
	}
	return nil
}

func (f *failStartCLI) Stop(context.Context, string) error   { return nil }
func (f *failStartCLI) Restart(context.Context, string) error { return nil }

// slowActionCLI makes any action take delay, so a test can observe the
// in-flight window.
type slowActionCLI struct {
	failStartCLI
	delay time.Duration
}

func (f slowActionCLI) Start(ctx context.Context, id string) error {
	time.Sleep(f.delay)
	return f.failStartCLI.Start(ctx, id)
}

func (f slowActionCLI) Stop(ctx context.Context, id string) error {
	time.Sleep(f.delay)
	return f.failStartCLI.Stop(ctx, id)
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

func TestBarTreeUnavailableTone(t *testing.T) {
	// The unavailable pill must carry ToneError: hidden == zero-running ==
	// unavailable is how the bar lies today.
	tree := BarTree("docker", true)
	if tree.Children[0].Tone != v1.ToneError {
		t.Fatalf("tone = %v, want error", tree.Children[0].Tone)
	}
	tree = BarTree("docker 2", false)
	if tree.Children[0].Tone != "" {
		t.Fatalf("tone = %v, want default when available", tree.Children[0].Tone)
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

func TestBarTreeValidate(t *testing.T) {
	bar := BarTree("docker 2", false)
	if err := v1.Validate(bar, v1.ViewBar); err != nil {
		t.Fatal(err)
	}
	// The pill must be an activatable button so the host can route the click
	// to the plugin; a bare text pill leaves the panel unreachable.
	if bar.Kind != v1.KindRow || len(bar.Children) != 1 {
		t.Fatalf("bar root = %+v", bar)
	}
	btn := bar.Children[0]
	if btn.Kind != v1.KindButton || btn.ID != "open" {
		t.Fatalf("bar child = %+v", btn)
	}
	activates := false
	for _, e := range btn.Events {
		if e == v1.EventActivate {
			activates = true
		}
	}
	if !activates {
		t.Fatalf("open button events = %v", btn.Events)
	}
}

func TestPanelTreeValidate(t *testing.T) {
	containers := []Container{
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up 2 hours"},
		{ID: "b2", Names: "db", Image: "postgres:16", State: "exited", Status: "Exited (0)"},
	}
	if err := v1.Validate(PanelTree(true, false, "", "", "", containers), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(false, false, "daemon down", "", "", nil), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(PanelTree(true, true, "", "", "", nil), v1.ViewPanel); err != nil {
		t.Fatal(err)
	}
	if err := v1.Validate(BarTree("docker 2", false), v1.ViewBar); err != nil {
		t.Fatal(err)
	}
}
