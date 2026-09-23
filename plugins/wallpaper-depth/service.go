// Package wallpaperdepth is a stub port of the Noctalia official plugin
// "wallpaper_depth". The helper that generates depth masks from a wallpaper
// (Depth Anything V2 Small via ONNX) is vendored unchanged and driven by this
// plugin; what is not possible yet is the payoff — compositing desktop
// widgets behind the wallpaper foreground — because the shell's plugin
// protocol has no wallpaper or surface API. This plugin therefore manages
// helper setup, status, and mask generation, and documents the rest.
package wallpaperdepth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"sync"
)

// HelperStatus is the parsed output of the helper's status/setup commands.
type HelperStatus struct {
	Ready        bool   `json:"ready"`
	RuntimeReady bool   `json:"runtimeReady"`
	ModelReady   bool   `json:"modelReady"`
	ModelSha     string `json:"modelSha256"`
	MaskPath     string `json:"maskPath"`
	CacheHit     bool   `json:"cacheHit"`
	ElapsedMs    int64  `json:"elapsedMs"`
}

// Runner executes the vendored Python helper. It is an interface so tests can
// fake it.
type Runner interface {
	Run(ctx context.Context, args ...string) (json.RawMessage, error)
}

// Helper runs depth_helper.py with the plugin's python3 dependency.
type Helper struct {
	// ScriptPath is the absolute path to depth_helper.py.
	ScriptPath string
	// DataDir is the helper's --data-dir (models, venv, and mask cache).
	DataDir string
}

func (h Helper) Run(ctx context.Context, args ...string) (json.RawMessage, error) {
	argv := append([]string{h.ScriptPath, "--data-dir", h.DataDir}, args...)
	cmd := exec.CommandContext(ctx, "python3", argv...)
	var stdout, stderr syncBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}
	return json.RawMessage(stdout.String()), nil
}

type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// DataDir derives the plugin's helper data directory from the user state
// root, mirroring where the shell stores per-plugin state. There is no
// os.UserStateDir in the standard library, so XDG is resolved by hand.
func DataDir(pluginID string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "sysc-shell", "plugins", pluginID)
}

// ScriptPath derives the vendored helper location from the running binary:
// the manifest exec lives in <plugin dir>/bin, so the plugin dir is the
// binary's parent's parent.
func ScriptPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "depth_helper.py"
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "depth_helper.py")
}

type Settings struct {
	AutoGenerate bool
	Threshold    int
	Feather      int
}

type Output struct {
	Name, State, WallpaperPath string
}

type Job struct {
	Output, WallpaperPath, Key string
	Threshold, Feather         int
}

type OutputRow struct {
	Output        string
	State         string
	WallpaperPath string
	Status        string
	MaskPath      string
	Error         string
	CacheHit      bool
	ElapsedMs     int64
}

type ControllerSnapshot struct {
	Helper   HelperStatus
	Settings Settings
	Error    string
	Busy     bool
	Rows     []OutputRow
}

// Registrar is the shell boundary that gives a generated mask to one output.
type Registrar interface {
	SetMask(ctx context.Context, output, wallpaperPath, maskPath string) error
}

type operation string

const (
	operationCheck      operation = "check"
	operationSetup      operation = "setup"
	operationGenerate   operation = "generate"
	operationClearCache operation = "clear-cache"
)

type workItem struct {
	operation operation
	job       Job
	revision  uint64
	manual    bool
}

type workResult struct {
	item workItem
	raw  json.RawMessage
	err  error
}

type controllerEvent struct {
	kind     string
	outputs  []Output
	settings Settings
	output   string
}

type outputState struct {
	output   Output
	row      OutputRow
	revision uint64
}

type controllerState struct {
	settings Settings
	helper   HelperStatus
	err      string
	rows     map[string]*outputState
	queue    []workItem
	queued   map[string]struct{}
	active   *workItem
	revision uint64
}

// Controller serializes helper work and owns the current per-output rows.
type Controller struct {
	runner     Runner
	registrar  Registrar
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan controllerEvent
	work       chan workItem
	results    chan workResult
	changed    chan struct{}
	done       chan struct{}
	workerDone chan struct{}
	closeOnce  sync.Once

	mu       sync.RWMutex
	snapshot ControllerSnapshot
}

func NewController(parent context.Context, runner Runner, registrar Registrar) *Controller {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Controller{
		runner: runner, registrar: registrar, ctx: ctx, cancel: cancel,
		events: make(chan controllerEvent, 32), work: make(chan workItem, 1),
		results: make(chan workResult, 1), changed: make(chan struct{}, 1),
		done: make(chan struct{}), workerDone: make(chan struct{}),
		snapshot: ControllerSnapshot{Settings: Settings{AutoGenerate: true, Threshold: 30, Feather: 8}},
	}
	go c.runWorker()
	go c.runController()
	return c
}

func (c *Controller) Snapshot() ControllerSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := c.snapshot
	snap.Rows = slices.Clone(snap.Rows)
	return snap
}

func (c *Controller) Changed() <-chan struct{} { return c.changed }

func (c *Controller) Poll(outputs []Output) {
	c.send(controllerEvent{kind: "poll", outputs: slices.Clone(outputs)})
}

func (c *Controller) SetSettings(settings Settings) {
	c.send(controllerEvent{kind: "settings", settings: settings})
}

func (c *Controller) Check()      { c.send(controllerEvent{kind: string(operationCheck)}) }
func (c *Controller) Setup()      { c.send(controllerEvent{kind: string(operationSetup)}) }
func (c *Controller) ClearCache() { c.send(controllerEvent{kind: string(operationClearCache)}) }
func (c *Controller) Generate(output string) {
	c.send(controllerEvent{kind: string(operationGenerate), output: output})
}

func (c *Controller) send(event controllerEvent) {
	select {
	case c.events <- event:
	case <-c.ctx.Done():
	case <-c.done:
	}
}

func (c *Controller) Close() {
	c.closeOnce.Do(c.cancel)
	<-c.done
	<-c.workerDone
}

func (c *Controller) runWorker() {
	defer close(c.workerDone)
	for {
		select {
		case <-c.ctx.Done():
			return
		case item := <-c.work:
			var args []string
			switch item.operation {
			case operationCheck:
				args = []string{"status"}
			case operationSetup:
				args = []string{"setup"}
			case operationClearCache:
				args = []string{"clear-cache"}
			case operationGenerate:
				args = []string{
					"generate", "--wallpaper", item.job.WallpaperPath,
					"--threshold", fmt.Sprintf("%.2f", float64(item.job.Threshold)/100),
					"--feather", fmt.Sprintf("%.2f", float64(item.job.Feather)/50),
				}
			}
			var raw json.RawMessage
			var err error
			if c.runner == nil {
				err = errors.New("wallpaper depth helper is not available")
			} else {
				raw, err = c.runner.Run(c.ctx, args...)
			}
			select {
			case c.results <- workResult{item: item, raw: raw, err: err}:
			case <-c.ctx.Done():
				return
			}
		}
	}
}

func (c *Controller) runController() {
	defer close(c.done)
	s := controllerState{
		settings: Settings{AutoGenerate: true, Threshold: 30, Feather: 8},
		rows:     make(map[string]*outputState), queued: make(map[string]struct{}),
	}
	c.publish(&s)
	for {
		select {
		case <-c.ctx.Done():
			return
		case event := <-c.events:
			s.handle(c, event)
			s.dispatch(c)
			c.publish(&s)
		case result := <-c.results:
			s.finish(c, result)
			s.dispatch(c)
			c.publish(&s)
		}
	}
}

func (s *controllerState) handle(c *Controller, event controllerEvent) {
	switch event.kind {
	case "poll":
		s.poll(c, event.outputs)
	case "settings":
		s.setSettings(c, normalizeSettings(event.settings))
	case string(operationCheck):
		s.enqueueControl(operationCheck)
	case string(operationSetup):
		s.enqueueControl(operationSetup)
	case string(operationGenerate):
		s.generate(c, event.output)
	case string(operationClearCache):
		s.clearCache(c)
	}
}

func (s *controllerState) poll(c *Controller, outputs []Output) {
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Name < outputs[j].Name })
	incoming := make(map[string]Output, len(outputs))
	for _, output := range outputs {
		if output.Name != "" {
			incoming[output.Name] = output
		}
	}
	for name, current := range s.rows {
		if _, ok := incoming[name]; ok {
			continue
		}
		s.clearMask(c, current, current.output.WallpaperPath)
		delete(s.rows, name)
		s.removeQueuedOutput(name)
	}
	for _, output := range outputs {
		if output.Name == "" {
			continue
		}
		current := s.rows[output.Name]
		if current == nil {
			current = &outputState{revision: s.nextRevision()}
			s.rows[output.Name] = current
			current.output = output
			current.row = OutputRow{Output: output.Name, State: output.State, WallpaperPath: output.WallpaperPath}
			s.resetRow(current)
		} else if current.output.State != output.State || current.output.WallpaperPath != output.WallpaperPath {
			s.clearMask(c, current, output.WallpaperPath)
			current.revision = s.nextRevision()
			current.output = output
			current.row.State = output.State
			current.row.WallpaperPath = output.WallpaperPath
			current.row.MaskPath = ""
			current.row.CacheHit = false
			current.row.ElapsedMs = 0
			current.row.Error = ""
			s.resetRow(current)
		}
		if current.output.State == "image" && current.row.Status == "waiting" && s.settings.AutoGenerate && s.helper.Ready {
			s.enqueueGenerate(current, false)
		}
	}
}

func (s *controllerState) setSettings(c *Controller, next Settings) {
	previous := s.settings
	if previous == next {
		return
	}
	s.settings = next
	parametersChanged := previous.Threshold != next.Threshold || previous.Feather != next.Feather
	if parametersChanged {
		s.removeQueuedGenerations()
		for _, row := range s.rows {
			if row.output.State != "image" {
				continue
			}
			row.revision = s.nextRevision()
			s.clearMask(c, row, row.output.WallpaperPath)
			row.row.MaskPath, row.row.CacheHit, row.row.ElapsedMs = "", false, 0
			row.row.Error = ""
			row.row.Status = "waiting"
			if next.AutoGenerate && s.helper.Ready {
				s.enqueueGenerate(row, false)
			}
		}
		return
	}
	if previous.AutoGenerate == next.AutoGenerate {
		return
	}
	if !next.AutoGenerate {
		s.removeQueuedAutomatic()
		for _, row := range s.rows {
			if row.output.State != "image" || row.row.MaskPath != "" {
				continue
			}
			if s.active != nil && s.active.operation == operationGenerate && s.active.job.Output == row.output.Name && s.active.manual {
				continue
			}
			row.revision = s.nextRevision()
			row.row.Status = "waiting"
		}
		return
	}
	for _, row := range s.rows {
		if row.output.State == "image" && row.row.MaskPath == "" && s.helper.Ready {
			row.row.Status = "waiting"
			s.enqueueGenerate(row, false)
		}
	}
}

func (s *controllerState) generate(c *Controller, name string) {
	row := s.rows[name]
	if row == nil || row.output.State != "image" || row.output.WallpaperPath == "" {
		if row != nil {
			row.row.Status = "error"
			row.row.Error = "no current image wallpaper"
		}
		return
	}
	row.revision = s.nextRevision()
	s.clearMask(c, row, row.output.WallpaperPath)
	row.row.MaskPath, row.row.CacheHit, row.row.ElapsedMs = "", false, 0
	row.row.Error = ""
	row.row.Status = "waiting"
	s.enqueueGenerate(row, true)
}

func (s *controllerState) clearCache(c *Controller) {
	if s.active != nil && s.active.operation == operationClearCache {
		s.queue = nil
		s.queued = make(map[string]struct{})
		return
	}
	s.queue = nil
	s.queued = make(map[string]struct{})
	for _, row := range s.rows {
		row.revision = s.nextRevision()
		s.clearMask(c, row, row.output.WallpaperPath)
		row.row.MaskPath, row.row.CacheHit, row.row.ElapsedMs = "", false, 0
		row.row.Error = ""
		s.resetRow(row)
	}
	s.enqueueControl(operationClearCache)
}

func (s *controllerState) resetRow(row *outputState) {
	if row.output.State == "image" {
		row.row.Status = "waiting"
		return
	}
	row.row.Status = "unsupported"
}

func (s *controllerState) clearMask(c *Controller, row *outputState, wallpaperPath string) {
	if row.row.MaskPath == "" || c.registrar == nil {
		return
	}
	if err := c.registrar.SetMask(c.ctx, row.output.Name, wallpaperPath, ""); err != nil {
		row.row.Error = err.Error()
		s.err = err.Error()
	}
	row.row.MaskPath = ""
}

func (s *controllerState) enqueueControl(op operation) {
	if s.active != nil && s.active.operation == op {
		return
	}
	for _, item := range s.queue {
		if item.operation == op {
			return
		}
	}
	s.queue = append(s.queue, workItem{operation: op})
}

func (s *controllerState) enqueueGenerate(row *outputState, manual bool) {
	if row.output.State != "image" || row.output.WallpaperPath == "" || (!manual && !s.helper.Ready) {
		return
	}
	job := Job{
		Output: row.output.Name, WallpaperPath: row.output.WallpaperPath,
		Threshold: s.settings.Threshold, Feather: s.settings.Feather,
	}
	job.Key = fmt.Sprintf("%q:%d:%d", job.WallpaperPath, job.Threshold, job.Feather)
	item := workItem{operation: operationGenerate, job: job, revision: row.revision, manual: manual}
	key := workKey(item)
	if _, exists := s.queued[key]; exists {
		return
	}
	if s.active != nil && s.active.operation == operationGenerate && workKey(*s.active) == key {
		return
	}
	s.queue = append(s.queue, item)
	s.queued[key] = struct{}{}
	row.row.Status = "processing"
	row.row.Error = ""
}

func (s *controllerState) dispatch(c *Controller) {
	if s.active != nil {
		return
	}
	for len(s.queue) != 0 {
		item := s.queue[0]
		s.queue = s.queue[1:]
		delete(s.queued, workKey(item))
		if item.operation == operationGenerate {
			row := s.rows[item.job.Output]
			if row == nil || row.revision != item.revision || row.output.State != "image" || row.output.WallpaperPath != item.job.WallpaperPath {
				continue
			}
		}
		s.active = &item
		c.work <- item
		return
	}
}

func (s *controllerState) finish(c *Controller, result workResult) {
	item := result.item
	s.active = nil
	if result.err != nil {
		s.finishError(item, result.err)
		if item.operation == operationClearCache && s.settings.AutoGenerate && s.helper.Ready {
			s.enqueueWaitingImages()
		}
		return
	}
	switch item.operation {
	case operationCheck:
		status, err := parseHelperStatus(result.raw)
		if err != nil {
			s.err = err.Error()
			return
		}
		s.helper = status
		s.err = ""
		if s.settings.AutoGenerate && status.Ready {
			s.enqueueWaitingImages()
		}
	case operationSetup:
		status, err := parseHelperStatus(result.raw)
		if err != nil {
			s.err = err.Error()
			return
		}
		if status.Ready {
			status.RuntimeReady, status.ModelReady = true, true
		}
		s.helper = status
		s.err = ""
		if s.settings.AutoGenerate && status.Ready {
			s.enqueueWaitingImages()
		}
	case operationClearCache:
		s.err = ""
		if s.settings.AutoGenerate && s.helper.Ready {
			s.enqueueWaitingImages()
		}
	case operationGenerate:
		s.finishGenerate(c, item, result.raw)
	}
}

func (s *controllerState) finishError(item workItem, err error) {
	if item.operation == operationGenerate {
		row := s.rows[item.job.Output]
		if row == nil || row.revision != item.revision {
			return
		}
		row.row.Status = "error"
		row.row.Error = err.Error()
		return
	}
	s.err = err.Error()
}

func (s *controllerState) finishGenerate(c *Controller, item workItem, raw json.RawMessage) {
	row := s.rows[item.job.Output]
	if row == nil || row.revision != item.revision || row.output.State != "image" || row.output.WallpaperPath != item.job.WallpaperPath {
		return
	}
	var status HelperStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		row.row.Status, row.row.Error = "error", err.Error()
		return
	}
	if status.MaskPath == "" {
		row.row.Status, row.row.Error = "error", "helper returned no mask path"
		return
	}
	if c.registrar == nil {
		row.row.Status, row.row.Error = "error", "wallpaper mask registrar is not available"
		return
	}
	if err := c.registrar.SetMask(c.ctx, item.job.Output, item.job.WallpaperPath, status.MaskPath); err != nil {
		row.row.Status, row.row.Error = "error", err.Error()
		return
	}
	row.row.Status, row.row.MaskPath = "ready", status.MaskPath
	row.row.Error, row.row.CacheHit, row.row.ElapsedMs = "", status.CacheHit, status.ElapsedMs
	s.err = ""
}

func (s *controllerState) enqueueWaitingImages() {
	names := make([]string, 0, len(s.rows))
	for name := range s.rows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		row := s.rows[name]
		if row.output.State == "image" && row.row.Status == "waiting" {
			s.enqueueGenerate(row, false)
		}
	}
}

func (s *controllerState) removeQueuedOutput(output string) {
	kept := s.queue[:0]
	for _, item := range s.queue {
		if item.operation == operationGenerate && item.job.Output == output {
			delete(s.queued, workKey(item))
			continue
		}
		kept = append(kept, item)
	}
	s.queue = kept
}

func (s *controllerState) removeQueuedGenerations() {
	for i := len(s.queue) - 1; i >= 0; i-- {
		if s.queue[i].operation == operationGenerate {
			delete(s.queued, workKey(s.queue[i]))
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
		}
	}
}

func (s *controllerState) removeQueuedAutomatic() {
	for i := len(s.queue) - 1; i >= 0; i-- {
		if s.queue[i].operation == operationGenerate && !s.queue[i].manual {
			delete(s.queued, workKey(s.queue[i]))
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
		}
	}
}

func (s *controllerState) nextRevision() uint64 {
	s.revision++
	return s.revision
}

func normalizeSettings(settings Settings) Settings {
	settings.Threshold = min(max(settings.Threshold, 0), 100)
	settings.Feather = min(max(settings.Feather, 0), 50)
	return settings
}

func workKey(item workItem) string {
	return fmt.Sprintf("%s\x00%s\x00%d", item.job.Output, item.job.Key, item.revision)
}

func parseHelperStatus(raw json.RawMessage) (HelperStatus, error) {
	var status HelperStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return HelperStatus{}, err
	}
	return status, nil
}

func (s *controllerState) publish(c *Controller) {
	rows := make([]OutputRow, 0, len(s.rows))
	for _, state := range s.rows {
		rows = append(rows, state.row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Output < rows[j].Output })
	snapshot := ControllerSnapshot{
		Helper: s.helper, Settings: s.settings, Error: s.err,
		Busy: s.active != nil || len(s.queue) != 0, Rows: rows,
	}
	c.mu.Lock()
	changed := !sameControllerSnapshot(c.snapshot, snapshot)
	c.snapshot = snapshot
	c.mu.Unlock()
	if changed {
		select {
		case c.changed <- struct{}{}:
		default:
		}
	}
}

func (c *Controller) publish(s *controllerState) { s.publish(c) }

func sameControllerSnapshot(a, b ControllerSnapshot) bool {
	return a.Helper == b.Helper && a.Settings == b.Settings && a.Error == b.Error &&
		a.Busy == b.Busy && slices.Equal(a.Rows, b.Rows)
}
