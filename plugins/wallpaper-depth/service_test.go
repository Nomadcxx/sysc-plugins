package wallpaperdepth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestControllerSerializesOutputsAndDeduplicatesPolls(t *testing.T) {
	runner := newBlockingRunner()
	gate := make(chan struct{})
	runner.Block("/wall-a.jpg", gate)
	c, registrar := newReadyController(t, runner)
	c.Poll([]Output{
		{Name: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg"},
		{Name: "DP-2", State: "image", WallpaperPath: "/wall-b.jpg"},
	})
	waitRunnerCall(t, runner, 2)
	callsBefore := runner.Count()
	for range 4 {
		c.Poll([]Output{
			{Name: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg"},
			{Name: "DP-2", State: "image", WallpaperPath: "/wall-b.jpg"},
		})
	}
	if !waitSnapshot(t, c, func(s ControllerSnapshot) bool { return s.Busy && len(s.Rows) == 2 }) {
		t.Fatal("controller did not publish both outputs while the first job was blocked")
	}
	close(gate)
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return !s.Busy && rowStatus(s, "DP-1") == "ready" && rowStatus(s, "DP-2") == "ready"
	})
	if got := runner.Count(); got != callsBefore+1 {
		t.Fatalf("calls after repeated polls = %d, want %d", got, callsBefore+1)
	}
	if got := runner.MaxActive(); got != 1 {
		t.Fatalf("concurrent helper calls = %d, want 1", got)
	}
	if got := len(registrar.Calls()); got != 2 {
		t.Fatalf("registered masks = %d, want one per output", got)
	}
}

func TestControllerWallpaperChangeClearsAndQueuesNewImage(t *testing.T) {
	runner := newBlockingRunner()
	c, registrar := newReadyController(t, runner)
	c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg"}})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "ready" })
	c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall-b.jpg"}})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		row, ok := findRow(s, "DP-1")
		return ok && row.WallpaperPath == "/wall-b.jpg" && row.Status == "ready"
	})
	calls := registrar.Calls()
	if len(calls) != 3 || calls[0].mask == "" || calls[1].mask != "" || calls[2].mask == "" {
		t.Fatalf("registrar calls = %+v, want set, clear, set", calls)
	}
	if calls[1].wallpaper != "/wall-b.jpg" || calls[2].wallpaper != "/wall-b.jpg" {
		t.Fatalf("new wallpaper path was not used: %+v", calls)
	}
}

func TestControllerUnsupportedStatesClearWithoutInference(t *testing.T) {
	for _, state := range []string{"video", "none", "transitioning", "covered"} {
		t.Run(state, func(t *testing.T) {
			runner := newBlockingRunner()
			c, registrar := newReadyController(t, runner)
			c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall.jpg"}})
			waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "ready" })
			before := runner.Count()
			path := ""
			if state == "video" {
				path = "/movie.mp4"
			}
			c.Poll([]Output{{Name: "DP-1", State: state, WallpaperPath: path}})
			waitSnapshot(t, c, func(s ControllerSnapshot) bool {
				row, ok := findRow(s, "DP-1")
				return ok && row.State == state && row.Status == "unsupported" && row.MaskPath == ""
			})
			calls := registrar.Calls()
			if calls[len(calls)-1].mask != "" {
				t.Fatalf("unsupported state left a registered mask: %+v", calls)
			}
			if got := runner.Count(); got != before {
				t.Fatalf("helper calls for %s = %d, want unchanged at %d", state, got, before)
			}
		})
	}
}

func TestControllerManualGenerateWorksWhenAutomaticGenerationIsOff(t *testing.T) {
	runner := newBlockingRunner()
	c, _ := newReadyController(t, runner)
	c.SetSettings(Settings{AutoGenerate: false, Threshold: 30, Feather: 8})
	c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall.jpg"}})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "waiting" })
	if got := runner.Count(); got != 1 {
		t.Fatalf("automatic generation ran with the setting off: %d helper calls", got)
	}
	c.Generate("DP-1")
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "ready" })
	if got := runner.Count(); got != 2 {
		t.Fatalf("manual generation helper calls = %d, want status plus one generate", got)
	}
}

func TestControllerGenerateAllQueuesCurrentImagesInOutputOrder(t *testing.T) {
	runner := newBlockingRunner()
	c, _ := newReadyController(t, runner)
	c.SetSettings(Settings{AutoGenerate: false, Threshold: 30, Feather: 8})
	c.Poll([]Output{
		{Name: "DP-3", State: "image", WallpaperPath: "/wall-c.jpg"},
		{Name: "DP-2", State: "video", WallpaperPath: "/movie.mp4"},
		{Name: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg"},
		{Name: "DP-4", State: "image"},
	})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return !s.Busy && len(s.Rows) == 4 && rowStatus(s, "DP-1") == "waiting" && rowStatus(s, "DP-3") == "waiting"
	})

	before := runner.Count()
	c.GenerateAll()
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return !s.Busy && rowStatus(s, "DP-1") == "ready" && rowStatus(s, "DP-3") == "ready" &&
			rowStatus(s, "DP-2") == "unsupported" && rowStatus(s, "DP-4") == "waiting"
	})

	var paths []string
	for _, call := range runner.Calls()[before:] {
		if len(call) != 0 && call[0] == "generate" {
			paths = append(paths, pathArg(call))
		}
	}
	if !slices.Equal(paths, []string{"/wall-a.jpg", "/wall-c.jpg"}) {
		t.Fatalf("GenerateAll paths = %v, want current image paths in output order", paths)
	}
	if runner.MaxActive() != 1 {
		t.Fatalf("concurrent helper calls = %d, want 1", runner.MaxActive())
	}
}

func TestControllerParameterChangeClearsMaskAndRegenerates(t *testing.T) {
	runner := newBlockingRunner()
	c, registrar := newReadyController(t, runner)
	c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall.jpg"}})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "ready" })
	c.SetSettings(Settings{AutoGenerate: true, Threshold: 40, Feather: 9})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return s.Settings.Threshold == 40 && s.Settings.Feather == 9 && rowStatus(s, "DP-1") == "ready"
	})
	calls := registrar.Calls()
	if len(calls) != 3 || calls[1].mask != "" || !strings.Contains(calls[2].mask, "0.40-0.18") {
		t.Fatalf("parameter change registrations = %+v", calls)
	}
	for _, call := range runner.Calls() {
		if call[0] == "clear-cache" {
			t.Fatal("parameter change cleared the helper's depth cache")
		}
	}
}

func TestControllerDiscardsStalePathAndParameterResults(t *testing.T) {
	for _, tc := range []string{"wallpaper path", "parameters"} {
		t.Run(tc, func(t *testing.T) {
			runner := newBlockingRunner()
			gate := make(chan struct{})
			runner.Block("/wall-a.jpg", gate)
			c, registrar := newReadyController(t, runner)
			c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg"}})
			waitRunnerCall(t, runner, 2)
			if tc == "wallpaper path" {
				c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall-b.jpg"}})
				waitSnapshot(t, c, func(s ControllerSnapshot) bool {
					row, ok := findRow(s, "DP-1")
					return ok && row.WallpaperPath == "/wall-b.jpg"
				})
			} else {
				c.SetSettings(Settings{AutoGenerate: true, Threshold: 40, Feather: 8})
				waitSnapshot(t, c, func(s ControllerSnapshot) bool { return s.Settings.Threshold == 40 })
			}
			close(gate)
			waitSnapshot(t, c, func(s ControllerSnapshot) bool {
				return !s.Busy && rowStatus(s, "DP-1") == "ready"
			})
			calls := registrar.Calls()
			if len(calls) != 1 || !strings.Contains(calls[0].mask, "0.40") && tc == "parameters" {
				t.Fatalf("stale helper result was registered: %+v", calls)
			}
			if tc == "wallpaper path" && calls[0].wallpaper != "/wall-b.jpg" {
				t.Fatalf("stale wallpaper result was registered: %+v", calls)
			}
		})
	}
}

func TestControllerContinuesAfterOneOutputFails(t *testing.T) {
	runner := newBlockingRunner()
	runner.Fail("/bad.jpg", errors.New("inference failed"))
	c, registrar := newReadyController(t, runner)
	c.Poll([]Output{
		{Name: "DP-1", State: "image", WallpaperPath: "/bad.jpg"},
		{Name: "DP-2", State: "image", WallpaperPath: "/good.jpg"},
	})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		row, ok := findRow(s, "DP-1")
		return ok && row.Status == "error" && row.Error != "" && rowStatus(s, "DP-2") == "ready"
	})
	if got := len(registrar.Calls()); got != 1 {
		t.Fatalf("registered masks = %d, want only the successful output", got)
	}
}

func TestControllerClearCacheDropsPendingJobsThenRequeuesImages(t *testing.T) {
	runner := newBlockingRunner()
	gate := make(chan struct{})
	runner.Block("/wall-a.jpg", gate)
	c, registrar := newReadyController(t, runner)
	c.Poll([]Output{{Name: "DP-2", State: "image", WallpaperPath: "/wall-b.jpg"}})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-2") == "ready" })
	c.Poll([]Output{
		{Name: "DP-1", State: "image", WallpaperPath: "/wall-a.jpg"},
		{Name: "DP-2", State: "image", WallpaperPath: "/wall-b.jpg"},
		{Name: "DP-3", State: "image", WallpaperPath: "/wall-c.jpg"},
	})
	waitRunnerCall(t, runner, 3)
	c.ClearCache()
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return s.Busy && s.Rows[0].Status == "waiting"
	})
	close(gate)
	clearCall := waitRunnerCall(t, runner, 4)
	if clearCall[0] != "clear-cache" {
		t.Fatalf("next helper operation after active stale job = %v, want clear-cache", clearCall)
	}
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return !s.Busy && rowStatus(s, "DP-1") == "ready" && rowStatus(s, "DP-2") == "ready" && rowStatus(s, "DP-3") == "ready"
	})
	clearCount, generated := 0, map[string]int{}
	for _, args := range runner.Calls() {
		switch args[0] {
		case "clear-cache":
			clearCount++
		case "generate":
			generated[pathArg(args)]++
		}
	}
	if clearCount != 1 || generated["/wall-a.jpg"] != 2 || generated["/wall-b.jpg"] != 2 || generated["/wall-c.jpg"] != 1 {
		t.Fatalf("clear-cache=%d generated=%v, want one clear and one post-clear job per image", clearCount, generated)
	}
	calls := registrar.Calls()
	foundClear := false
	for _, call := range calls {
		if call.output == "DP-2" && call.mask == "" {
			foundClear = true
		}
	}
	if !foundClear {
		t.Fatalf("clear-cache did not clear the existing DP-2 mask: %+v", calls)
	}
}

func TestControllerRemovesOutputMasksWhenOutputDisappears(t *testing.T) {
	runner := newBlockingRunner()
	c, registrar := newReadyController(t, runner)
	c.Poll([]Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall.jpg"}})
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "ready" })
	c.Poll(nil)
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return len(s.Rows) == 0 && !s.Busy })
	calls := registrar.Calls()
	if len(calls) != 2 || calls[1].mask != "" {
		t.Fatalf("output removal registrations = %+v, want mask clear", calls)
	}
}

func TestControllerChangedSignalSkipsIdenticalPolls(t *testing.T) {
	runner := newBlockingRunner()
	c, _ := newReadyController(t, runner)
	drainChanged(c)
	outputs := []Output{{Name: "DP-1", State: "image", WallpaperPath: "/wall.jpg"}}
	c.Poll(outputs)
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return rowStatus(s, "DP-1") == "ready" })
	drainChanged(c)
	c.Poll(outputs)
	select {
	case <-c.Changed():
		t.Fatal("identical poll published a view change")
	case <-time.After(40 * time.Millisecond):
	}
}

func TestControllerSnapshotReportsActiveHelperOperation(t *testing.T) {
	runner := newBlockingRunner()
	gate := make(chan struct{})
	runner.Block("", gate)
	c := NewController(context.Background(), runner, &fakeRegistrar{})
	t.Cleanup(c.Close)
	c.Check()
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return s.Busy && s.Operation == string(operationCheck) && !s.Checked
	})
	close(gate)
	waitSnapshot(t, c, func(s ControllerSnapshot) bool {
		return !s.Busy && s.Checked && s.Operation == ""
	})
}

type blockingRunner struct {
	mu       sync.Mutex
	calls    [][]string
	active   int
	max      int
	gates    map[string]chan struct{}
	failures map[string]error
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{gates: make(map[string]chan struct{}), failures: make(map[string]error)}
}

func (r *blockingRunner) Block(path string, gate chan struct{}) {
	r.mu.Lock()
	r.gates[path] = gate
	r.mu.Unlock()
}

func (r *blockingRunner) Fail(path string, err error) {
	r.mu.Lock()
	r.failures[path] = err
	r.mu.Unlock()
}

func (r *blockingRunner) Run(ctx context.Context, args ...string) (json.RawMessage, error) {
	args = append([]string(nil), args...)
	path := pathArg(args)
	r.mu.Lock()
	r.calls = append(r.calls, args)
	r.active++
	r.max = max(r.max, r.active)
	gate := r.gates[path]
	delete(r.gates, path)
	err := r.failures[path]
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "status", "setup", "clear-cache":
		return json.RawMessage(`{"ready":true,"runtimeReady":true,"modelReady":true}`), nil
	case "generate":
		base := filepath.Base(path)
		threshold, feather := flagArg(args, "--threshold"), flagArg(args, "--feather")
		return json.RawMessage(fmt.Sprintf(`{"ready":true,"maskPath":"/mask/%s-%s-%s.png"}`, base, threshold, feather)), nil
	default:
		return nil, fmt.Errorf("unexpected helper operation %q", args[0])
	}
}

func (r *blockingRunner) Calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i, call := range r.calls {
		out[i] = append([]string(nil), call...)
	}
	return out
}

func (r *blockingRunner) Count() int { return len(r.Calls()) }

func (r *blockingRunner) MaxActive() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.max
}

type maskCall struct{ output, wallpaper, mask string }

type fakeRegistrar struct {
	mu    sync.Mutex
	calls []maskCall
}

func (r *fakeRegistrar) SetMask(_ context.Context, output, wallpaperPath, maskPath string) error {
	r.mu.Lock()
	r.calls = append(r.calls, maskCall{output: output, wallpaper: wallpaperPath, mask: maskPath})
	r.mu.Unlock()
	return nil
}

func (r *fakeRegistrar) Calls() []maskCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]maskCall(nil), r.calls...)
}

func newReadyController(t *testing.T, runner Runner) (*Controller, *fakeRegistrar) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	registrar := &fakeRegistrar{}
	c := NewController(ctx, runner, registrar)
	t.Cleanup(func() {
		c.Close()
		cancel()
	})
	c.Check()
	waitSnapshot(t, c, func(s ControllerSnapshot) bool { return !s.Busy && s.Helper.Ready })
	return c, registrar
}

func waitSnapshot(t *testing.T, c *Controller, done func(ControllerSnapshot) bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if done(c.Snapshot()) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("controller snapshot did not reach the expected state: %+v", c.Snapshot())
	return false
}

func waitRunnerCall(t *testing.T, runner *blockingRunner, count int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		calls := runner.Calls()
		if len(calls) >= count {
			return calls[count-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper calls = %v, want at least %d", runner.Calls(), count)
	return nil
}

func rowStatus(s ControllerSnapshot, output string) string {
	row, _ := findRow(s, output)
	return row.Status
}

func findRow(s ControllerSnapshot, output string) (OutputRow, bool) {
	for _, row := range s.Rows {
		if row.Output == output {
			return row, true
		}
	}
	return OutputRow{}, false
}

func drainChanged(c *Controller) {
	for {
		select {
		case <-c.Changed():
		default:
			return
		}
	}
}

func pathArg(args []string) string { return flagArg(args, "--wallpaper") }

func flagArg(args []string, flag string) string {
	for i := range args {
		if args[i] == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
