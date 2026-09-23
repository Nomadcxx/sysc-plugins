package minidocker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

type fakeDocker struct {
	listErr        error
	containers     []Container
	skippedLines   int
	images         []Image
	imagesErr      error
	imageSkipped   int
	volumes        []Volume
	volumesErr     error
	volumeSkipped  int
	networks       []Network
	networksErr    error
	networkSkipped int
	actions        []string
}

func (f *fakeDocker) List(ctx context.Context) ([]Container, int, error) {
	return f.containers, f.skippedLines, f.listErr
}

func (f *fakeDocker) Images(context.Context) ([]Image, int, error) {
	return f.images, f.imageSkipped, f.imagesErr
}
func (f *fakeDocker) Volumes(context.Context) ([]Volume, int, error) {
	return f.volumes, f.volumeSkipped, f.volumesErr
}
func (f *fakeDocker) Networks(context.Context) ([]Network, int, error) {
	return f.networks, f.networkSkipped, f.networksErr
}
func (*fakeDocker) ImageExposedPorts(context.Context, string) ([]int, error) { return nil, nil }

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

func (f *fakeDocker) Remove(ctx context.Context, id string) error {
	f.actions = append(f.actions, "remove:"+id)
	return nil
}

func (f *fakeDocker) Rmi(ctx context.Context, id string) error {
	f.actions = append(f.actions, "rmi:"+id)
	return nil
}

func (f *fakeDocker) VolRm(ctx context.Context, name string) error {
	f.actions = append(f.actions, "volrm:"+name)
	return nil
}

func (f *fakeDocker) NetRm(ctx context.Context, id string) error {
	f.actions = append(f.actions, "netrm:"+id)
	return nil
}

func (f *fakeDocker) Run(ctx context.Context, opts RunOpts) error {
	f.actions = append(f.actions, "run:"+opts.Image)
	return nil
}

func TestCLIDiagnosesFailures(t *testing.T) {
	t.Run("daemon down reads as daemon down", func(t *testing.T) {
		bin := t.TempDir()
		writeFakeDocker(t, bin, "#!/bin/sh\necho 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock' >&2\nexit 1\n")
		_, _, err := CLI{}.List(context.Background())
		if err == nil || err.Error() != "Docker daemon not running" {
			t.Fatalf("err = %v, want the daemon diagnosis", err)
		}
	})

	t.Run("missing binary reads as missing binary", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, _, err := CLI{}.List(context.Background())
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

func TestParseContainers(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "containers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	containers, skipped := parseContainers(data)
	if len(containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(containers))
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 malformed record (blank lines are ignored)", skipped)
	}
	if containers[0].ID != "a1" {
		t.Fatalf("first container = %+v, want a1", containers[0])
	}
	if containers[0].Names != "web" || !containers[0].Running() {
		t.Fatalf("web = %+v", containers[0])
	}
	if containers[1].ID != "b2" || containers[1].Running() {
		t.Fatalf("db should not be running: %+v", containers[1])
	}
}

func TestSessionRefreshStoresSkippedLineCount(t *testing.T) {
	s := NewSession(&fakeDocker{skippedLines: 2})
	s.Refresh(context.Background())
	if got := s.SkippedLines(); got != 2 {
		t.Fatalf("skipped lines = %d, want 2", got)
	}
}

func TestSessionStartsWithContainerLoading(t *testing.T) {
	state := NewSession(&fakeDocker{}).State()
	if state.Scope != ScopeContainers || !state.ContainerTab.Loading {
		t.Fatalf("initial state = %+v, want container scope loading", state)
	}
	if state.ImageTab.Loading || state.VolumeTab.Loading || state.NetworkTab.Loading {
		t.Fatalf("inactive tabs should start idle: %+v", state)
	}
}

func TestSessionKeepsSnapshotsPerTabAndReplacesIdenticalData(t *testing.T) {
	fd := &fakeDocker{
		containers: []Container{{ID: "a1", Names: "web", State: "running"}},
		images:     []Image{{ID: "img1", Repository: "nginx", Tag: "latest", Containers: 1}},
	}
	s := NewSession(fd)
	s.Refresh(context.Background())
	s.RefreshTab(context.Background(), ScopeImages)
	s.RefreshTab(context.Background(), ScopeImages)
	state := s.State()
	if len(state.Containers) != 1 || state.Containers[0].ID != "a1" {
		t.Fatalf("container snapshot = %+v", state.Containers)
	}
	if len(state.Images) != 1 || state.Images[0].ID != "img1" {
		t.Fatalf("image snapshot = %+v", state.Images)
	}
	if !state.ContainerTab.Available || !state.ImageTab.Available || state.ImageTab.Loading {
		t.Fatalf("tab states = container %+v, image %+v", state.ContainerTab, state.ImageTab)
	}
}

func TestSessionRetainsKnownRowsWhileRefreshRuns(t *testing.T) {
	s := NewSession(&fakeDocker{containers: []Container{{ID: "a1", Names: "web"}}})
	s.Refresh(context.Background())
	blocked := &blockedListDocker{
		fakeDocker: &fakeDocker{containers: []Container{{ID: "b2", Names: "db"}}},
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	s.docker = blocked
	done := make(chan struct{})
	go func() {
		s.Refresh(context.Background())
		close(done)
	}()
	<-blocked.entered

	stateRead := make(chan SessionSnapshot, 1)
	go func() { stateRead <- s.State() }()
	select {
	case state := <-stateRead:
		if !state.ContainerTab.Loading || len(state.Containers) != 1 || state.Containers[0].ID != "a1" {
			t.Fatalf("refresh state = %+v, want loading with cached a1", state)
		}
	case <-time.After(time.Second):
		t.Fatal("Session lock held while Docker.List was blocked")
	}
	close(blocked.release)
	<-done
	if got := s.State().Containers[0].ID; got != "b2" {
		t.Fatalf("completed refresh ID = %q, want b2", got)
	}
}

func TestSessionPrimaryFailureKeepsCacheButRejectsMutation(t *testing.T) {
	fd := &fakeDocker{containers: []Container{{ID: "b2", Names: "db", State: "exited"}}}
	s := NewSession(fd)
	s.Refresh(context.Background())
	fd.listErr = errors.New("Docker daemon not running")
	s.Refresh(context.Background())
	state := s.State()
	if state.ContainerTab.Available || state.ContainerTab.ListError == "" {
		t.Fatalf("primary failure state = %+v", state.ContainerTab)
	}
	if len(state.Containers) != 1 || state.Containers[0].ID != "b2" {
		t.Fatalf("cached containers lost: %+v", state.Containers)
	}
	s.Act(context.Background(), "start", "b2")
	if len(fd.actions) != 0 {
		t.Fatalf("mutated while primary list unavailable: %v", fd.actions)
	}
}

func TestSessionSecondaryFailureRetainsLastSuccessfulRows(t *testing.T) {
	fd := &fakeDocker{images: []Image{{ID: "img1", Repository: "nginx", Tag: "latest"}}}
	s := NewSession(fd)
	s.RefreshTab(context.Background(), ScopeImages)
	fd.imagesErr = errors.New("registry unavailable")
	s.RefreshTab(context.Background(), ScopeImages)
	state := s.State()
	if !state.ImageTab.Available || state.ImageTab.ListError == "" {
		t.Fatalf("secondary failure state = %+v", state.ImageTab)
	}
	if len(state.Images) != 1 || state.Images[0].ID != "img1" {
		t.Fatalf("last successful image rows lost: %+v", state.Images)
	}
}

func TestSessionRejectsStaleScopeEntityAndIneligibleActions(t *testing.T) {
	fd := &fakeDocker{containers: []Container{
		{ID: "a1", Names: "web", State: "running"},
		{ID: "b2", Names: "db", State: "exited"},
	}}
	s := NewSession(fd)
	s.Refresh(context.Background())
	s.Act(context.Background(), "start", "gone")
	s.Act(context.Background(), "start", "a1")
	s.SetScope(ScopeImages)
	s.Act(context.Background(), "start", "b2")
	if len(fd.actions) != 0 {
		t.Fatalf("stale or ineligible actions reached Docker: %v", fd.actions)
	}
}

type blockedListDocker struct {
	*fakeDocker
	entered chan struct{}
	release chan struct{}
}

func (d *blockedListDocker) List(ctx context.Context) ([]Container, int, error) {
	close(d.entered)
	select {
	case <-d.release:
		return d.fakeDocker.List(ctx)
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
}

func TestParseImages(t *testing.T) {
	images, skipped := parseImages(readFixture(t, "images.jsonl"))
	if len(images) != 2 || skipped != 1 {
		t.Fatalf("images=%d skipped=%d, want 2 and 1", len(images), skipped)
	}
	if images[0].Repository != "nginx" || images[0].Tag != "latest" || images[0].Containers != 2 {
		t.Fatalf("first image = %+v", images[0])
	}
	if images[1].Repository != "<none>" || images[1].Containers != -1 {
		t.Fatalf("second image = %+v", images[1])
	}
}

func TestParseVolumes(t *testing.T) {
	volumes, skipped := parseVolumes(readFixture(t, "volumes.jsonl"))
	if len(volumes) != 1 || skipped != 1 {
		t.Fatalf("volumes=%d skipped=%d, want 1 and 1", len(volumes), skipped)
	}
	if volumes[0].Name != "db-data" || volumes[0].Driver != "local" || volumes[0].Scope != "local" {
		t.Fatalf("volume = %+v", volumes[0])
	}
}

func TestParseNetworks(t *testing.T) {
	networks, skipped := parseNetworks(readFixture(t, "networks.jsonl"))
	if len(networks) != 1 || skipped != 1 {
		t.Fatalf("networks=%d skipped=%d, want 1 and 1", len(networks), skipped)
	}
	if networks[0].Name != "bridge" || networks[0].ID != "net123" || networks[0].Driver != "bridge" {
		t.Fatalf("network = %+v", networks[0])
	}
}

func TestParseExposedPorts(t *testing.T) {
	ports, err := parseExposedPorts(readFixture(t, "exposed-ports.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{80, 8080}; !slices.Equal(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	if ports, err = parseExposedPorts([]byte("null")); err != nil || len(ports) != 0 {
		t.Fatalf("null ports = %v, err = %v; want empty and no error", ports, err)
	}
	if _, err := parseExposedPorts([]byte("{")); err == nil {
		t.Fatal("malformed exposed-port JSON accepted")
	}
}

func TestCLIArgv(t *testing.T) {
	cases := []struct {
		name string
		call func(CLI) error
		want []string
	}{
		{"container remove", func(cli CLI) error { return cli.Remove(context.Background(), "abc123") }, []string{"rm", "abc123"}},
		{"image remove", func(cli CLI) error { return cli.Rmi(context.Background(), "registry.example/team/api:1") }, []string{"rmi", "registry.example/team/api:1"}},
		{"volume remove", func(cli CLI) error { return cli.VolRm(context.Background(), "db-data") }, []string{"volume", "rm", "db-data"}},
		{"network remove", func(cli CLI) error { return cli.NetRm(context.Background(), "net123") }, []string{"network", "rm", "net123"}},
		{"run with empty options", func(cli CLI) error {
			return cli.Run(context.Background(), RunOpts{Image: "nginx:1", Publish: true})
		}, []string{"run", "-d", "nginx:1"}},
		{"run without publish", func(cli CLI) error {
			return cli.Run(context.Background(), RunOpts{Image: "nginx:1", Port: "8080"})
		}, []string{"run", "-d", "nginx:1"}},
		{"run with options", func(cli CLI) error {
			return cli.Run(context.Background(), RunOpts{
				Image: "registry.example/team/api:1@sha256:abc", Name: "api-1",
				Environment: []string{"A=1", "B=two words"}, Port: "8080", Publish: true, Network: "bridge",
			})
		}, []string{"run", "-d", "--name", "api-1", "-e", "A=1", "-e", "B=two words", "-p", "8080:8080", "--network", "bridge", "registry.example/team/api:1@sha256:abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCLIArgv(t, tc.call, tc.want) })
	}
}

func TestCLIRejectsOversizedOutput(t *testing.T) {
	bin := t.TempDir()
	writeFakeDocker(t, bin, "#!/bin/sh\nexec /usr/bin/head -c 2097152 /dev/zero\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := (CLI{}).List(ctx); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("List error = %v, want oversized-output diagnosis", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCLIArgv(t *testing.T, call func(CLI) error, want []string) {
	t.Helper()
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	writeFakeDocker(t, dir, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGV_FILE\"\n")
	t.Setenv("ARGV_FILE", argvFile)
	if err := call(CLI{}); err != nil {
		t.Fatalf("CLI call: %v", err)
	}
	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
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
	s.Refresh(context.Background())
	s.Act(context.Background(), "start", "b2")
	_, _, _, _, actErr, _ := s.Snapshot()
	if actErr == "" {
		t.Fatal("actErr empty after a failed action")
	}
}

func TestActErrorClearsOnNextSuccess(t *testing.T) {
	fd := &failStartCLI{list: []Container{{ID: "b2", Names: "db", State: "exited"}}}
	s := NewSession(fd)
	s.Refresh(context.Background())
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
	s.Refresh(context.Background())
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

func (f *failStartCLI) List(context.Context) ([]Container, int, error) { return f.list, 0, nil }
func (*failStartCLI) Images(context.Context) ([]Image, int, error)     { return nil, 0, nil }
func (*failStartCLI) Volumes(context.Context) ([]Volume, int, error)   { return nil, 0, nil }
func (*failStartCLI) Networks(context.Context) ([]Network, int, error) { return nil, 0, nil }
func (*failStartCLI) ImageExposedPorts(context.Context, string) ([]int, error) {
	return nil, nil
}

func (f *failStartCLI) Start(context.Context, string) error {
	if f.failStart {
		return errors.New("Error response from daemon: conflict")
	}
	return nil
}

func (f *failStartCLI) Stop(context.Context, string) error    { return nil }
func (f *failStartCLI) Restart(context.Context, string) error { return nil }
func (f *failStartCLI) Remove(context.Context, string) error  { return nil }
func (f *failStartCLI) Rmi(context.Context, string) error     { return nil }
func (f *failStartCLI) VolRm(context.Context, string) error   { return nil }
func (f *failStartCLI) NetRm(context.Context, string) error   { return nil }
func (f *failStartCLI) Run(context.Context, RunOpts) error    { return nil }

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
	s.Refresh(context.Background())
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

// show_count is gone (D1): it duplicated status_mode. The mode labels are
// honest now - "Never" really means no count, not a hidden pill.
func TestBarLabelModes(t *testing.T) {
	if got := BarLabel("always", 3, true); got != "docker 3" {
		t.Fatalf("always = %q", got)
	}
	if got := BarLabel("running_only", 0, true); got != "docker" {
		t.Fatalf("running_only zero = %q", got)
	}
	if got := BarLabel("running_only", 2, true); got != "docker 2" {
		t.Fatalf("running_only = %q", got)
	}
	if got := BarLabel("hidden", 5, true); got != "docker" {
		t.Fatalf("hidden = %q", got)
	}
	if got := BarLabel("always", 0, false); got != "docker" {
		t.Fatalf("unavailable = %q", got)
	}
}

// Action node IDs minted by actionButton are "verb:containerID"; the ID is
// echoed into a docker argv, so it must survive a strict allow-list before
// dispatch.
func TestParseAction(t *testing.T) {
	cases := []struct {
		node, action, id string
		ok               bool
	}{
		{"start:abc123def456", "start", "abc123def456", true},
		{"stop:abc123def456", "stop", "abc123def456", true},
		{"restart:abc123def456", "restart", "abc123def456", true},
		{"open", "", "", false},
		{"refresh", "", "", false},
		{"start:", "", "", false},
		{"start", "", "", false},
		{"start:bad id", "", "", false},
		{"start:;rm -rf /", "", "", false},
		{"start:-lead", "", "", false},
	}
	for _, tc := range cases {
		action, id, ok := ParseAction(tc.node)
		if ok != tc.ok || action != tc.action || id != tc.id {
			t.Errorf("ParseAction(%q) = %q, %q, %v; want %q, %q, %v",
				tc.node, action, id, ok, tc.action, tc.id, tc.ok)
		}
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

func TestPanelListScrollsAndCaps(t *testing.T) {
	// Rows live in a scrolled list; past the cap the panel still validates
	// instead of blowing past MaxNodes and being silently dropped.
	var containers []Container
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("c%03d", i)
		containers = append(containers, Container{ID: id, Names: id, Image: "img", State: "running", Status: "Up"})
	}
	panel := PanelTree(true, false, "", "", "", containers)
	if err := v1.Validate(panel, v1.ViewPanel); err != nil {
		t.Fatalf("300 containers: %v", err)
	}
	var list *v1.Node
	walkNodes(panel, func(n *v1.Node) {
		if n.Kind == v1.KindList {
			list = n
		}
	})
	if list == nil {
		t.Fatal("panel has no list; container rows cannot scroll")
	}
	if len(list.Children) != maxPanelRows {
		t.Fatalf("list rows = %d, want %d", len(list.Children), maxPanelRows)
	}
	more := false
	walkNodes(panel, func(n *v1.Node) {
		if n.Text == fmt.Sprintf("+%d more", len(containers)-maxPanelRows) {
			more = true
		}
	})
	if !more {
		t.Fatalf("no +%d more line", len(containers)-maxPanelRows)
	}
	// A handful of containers must not gain the cap line.
	small := PanelTree(true, false, "", "", "", containers[:3])
	walkNodes(small, func(n *v1.Node) {
		if strings.HasPrefix(n.Text, "+") {
			t.Fatalf("unexpected overflow line %q with 3 containers", n.Text)
		}
	})
}

func TestTooltipTreeIsReadOnlyColumn(t *testing.T) {
	// The tooltip view rejects interactive nodes; the old shape published the
	// bar's button there and the host silently dropped the view.
	tip := TooltipTree("2 containers running")
	if err := v1.Validate(tip, v1.ViewTooltip); err != nil {
		t.Fatal(err)
	}
	if tip.Kind != v1.KindColumn || len(tip.Children) != 1 || tip.Children[0].Text != "2 containers running" {
		t.Fatalf("tooltip tree = %+v", tip)
	}
}

func TestPanelOrdersRunningFirstWithAccentState(t *testing.T) {
	containers := []Container{
		{ID: "b2", Names: "db", Image: "postgres:16", State: "exited", Status: "Exited (0)"},
		{ID: "a1", Names: "web", Image: "nginx:latest", State: "running", Status: "Up 2 hours"},
		{ID: "c3", Names: "cache", Image: "redis:7", State: "exited", Status: "Exited (137)"},
		{ID: "d4", Names: "api", Image: "api:1", State: "running", Status: "Up 5 hours"},
	}
	panel := PanelTree(true, false, "", "", "", containers)
	var list *v1.Node
	walkNodes(panel, func(n *v1.Node) {
		if n.Kind == v1.KindList {
			list = n
		}
	})
	if list == nil {
		t.Fatal("no list")
	}
	// Running containers surface first (stable within each group); exited
	// ones must not bury the actionable rows.
	var names []string
	for _, row := range list.Children {
		names = append(names, row.Children[0].Text)
	}
	want := []string{"web · Up 2 hours", "api · Up 5 hours", "db · Exited (0)", "cache · Exited (137)"}
	if !slices.Equal(names, want) {
		t.Fatalf("row order = %v, want %v", names, want)
	}
	for _, row := range list.Children {
		running := strings.Contains(row.Children[0].Text, "Up ")
		tone := row.Children[0].Tone
		if running && tone != v1.ToneAccent {
			t.Fatalf("running row %q tone = %q, want accent", row.Children[0].Text, tone)
		}
		if !running && tone != v1.ToneNormal {
			t.Fatalf("stopped row %q tone = %q, want normal", row.Children[0].Text, tone)
		}
	}
}

func TestPanelCraftPass(t *testing.T) {
	panel := PanelTree(true, false, "", "", "", nil)
	if panel.Padding != 16 {
		t.Fatalf("panel padding = %d, want 16 (timer precedent)", panel.Padding)
	}
	header := panel.Children[0]
	if !header.PinEnd {
		t.Fatal("header row must right-pin the refresh button")
	}
	title := header.Children[0]
	if !title.Bold || title.Size != "title" {
		t.Fatalf("title Bold=%v Size=%q, want bold title", title.Bold, title.Size)
	}
}

func TestBarTreeCountIsTabular(t *testing.T) {
	var buttons []*v1.Node
	walkNodes(BarTree("docker 12", false), func(n *v1.Node) {
		if n.Kind == v1.KindButton {
			buttons = append(buttons, n)
		}
	})
	if len(buttons) != 1 {
		t.Fatalf("bar buttons = %d, want 1", len(buttons))
	}
	// The running count changes every refresh; tabular figures keep the
	// pill from wobbling in width.
	if !buttons[0].Tabular {
		t.Fatal("bar count must be Tabular")
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

// panelSize reads the box the host opens this plugin's panel with. The
// manifest is the only declaration of it, so a test that hardcoded 480x560
// would drift the day the manifest moves.
func panelSize(t *testing.T) (int, int) {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Panels []struct {
			ID            string `json:"id"`
			Width, Height int
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Panels) == 0 {
		t.Fatal("manifest declares no panels")
	}
	return m.Panels[0].Width, m.Panels[0].Height
}

// TestViewsFitTheirHostSlots lays every view the plugin can build out with the
// host's own rules, at the sizes the host uses. v1.Validate is geometry-blind
// and the host's layout stops at the first rejection, so this matrix is the
// panel's only whole check before a user sees it.
func TestViewsFitTheirHostSlots(t *testing.T) {
	panelW, panelH := panelSize(t)
	row := func(i int, state, status string) Container {
		return Container{ID: fmt.Sprintf("c%03d", i), Names: fmt.Sprintf("container-%03d", i),
			Image: "ghcr.io/example/some-service:latest", State: state, Status: status}
	}
	full := make([]Container, maxPanelRows+10)
	for i := range full {
		full[i] = row(i+1, "running", "Up 3 hours")
	}
	states := map[string]struct {
		available, loading        bool
		listErr, actErr, actingID string
		containers                []Container
	}{
		"list":        {available: true, containers: []Container{row(1, "running", "Up 3 hours"), row(2, "exited", "Exited (0) 2 days ago")}},
		"full":        {available: true, containers: full},
		"acting":      {available: true, actingID: "c001", containers: []Container{row(1, "running", "Up 3 hours")}},
		"errors":      {available: true, listErr: "Docker daemon not running", actErr: "docker action timed out"},
		"empty":       {available: true},
		"loading":     {available: true, loading: true},
		"unavailable": {listErr: "Docker daemon not running"},
	}
	for name, s := range states {
		bar := BarTree(BarLabel("always", 1, s.available), !s.available)
		for _, f := range shelllint.Tree(bar, v1.ViewBar, shelllint.BarWidth, shelllint.BarHeight) {
			t.Errorf("%s bar: %s", name, f)
		}
		tip := TooltipTree(TooltipText(1, s.available))
		for _, f := range shelllint.Tree(tip, v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight) {
			t.Errorf("%s tooltip: %s", name, f)
		}
		panel := PanelTree(s.available, s.loading, s.listErr, s.actErr, s.actingID, s.containers)
		for _, f := range shelllint.Tree(panel, v1.ViewPanel, panelW, panelH) {
			t.Errorf("%s panel: %s", name, f)
		}
	}
}
