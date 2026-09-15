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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// Session tracks the helper's readiness and the last operation result.
type Session struct {
	mu       sync.Mutex
	runner   Runner
	status   HelperStatus
	lastErr  string
	busy     bool
	lastMask string
}

func NewSession(r Runner) *Session {
	return &Session{runner: r}
}

// Snapshot returns the current status, the last error, and whether an
// operation is running.
func (s *Session) Snapshot() (HelperStatus, string, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.lastErr, s.busy, s.lastMask
}

// Check runs the helper's status command.
func (s *Session) Check(ctx context.Context) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()

	raw, err := s.runner.Run(ctx, "status")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if err != nil {
		s.lastErr = err.Error()
		return
	}
	var st HelperStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		s.lastErr = err.Error()
		return
	}
	s.status = st
	s.lastErr = ""
}

// Setup runs the helper's one-time environment bootstrap (venv, packages,
// model download). It can take a while; the panel stays responsive.
func (s *Session) Setup(ctx context.Context) {
	s.run(ctx, "setup")
}

// Generate produces a depth mask for the wallpaper under the current
// threshold and feather settings.
func (s *Session) Generate(ctx context.Context, wallpaper string, threshold, feather int) {
	if wallpaper == "" {
		s.mu.Lock()
		s.lastErr = "no wallpaper configured"
		s.mu.Unlock()
		return
	}
	s.run(ctx, "generate", "--wallpaper", wallpaper,
		"--threshold", fmt.Sprintf("%.2f", float64(threshold)/100.0),
		"--feather", fmt.Sprintf("%.2f", float64(feather)/50.0))
}

// ClearCache drops the helper's depth and mask caches.
func (s *Session) ClearCache(ctx context.Context) {
	s.run(ctx, "clear-cache")
}

func (s *Session) run(ctx context.Context, args ...string) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()

	raw, err := s.runner.Run(ctx, args...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if err != nil {
		s.lastErr = err.Error()
		return
	}
	var st HelperStatus
	if err := json.Unmarshal(raw, &st); err == nil {
		s.status = st
		if st.MaskPath != "" {
			s.lastMask = st.MaskPath
		}
	}
	s.lastErr = ""
}
