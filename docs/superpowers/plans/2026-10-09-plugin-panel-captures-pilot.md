# Plugin panel captures: helper and pilot scenes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A tested capture helper, `make capture` / `make captures`, and three pilot scenes (Timer, World Clock, GitHub Notifications) that turn a plugin's real panel tree plus fictional fixture data into `screenshot.png` through sysc-shell's `sysc-panel-preview`.

**Architecture:** `internal/capture.Panel(t, dir, tree)` reads the panel size from the plugin's manifest, refuses a tree the host cannot lay out (using `plugin/lint`), pipes the tree as JSON to `sysc-panel-preview`, and checks that a PNG came back. Each plugin's `capture_test.go` builds its real panel tree from invented data and calls it; it does nothing unless `CAPTURE=1`. No `go.mod` change.

**Tech Stack:** Go; `github.com/Nomadcxx/sysc-shell/plugin/lint` and `plugin/v1` (already dependencies); the `sysc-panel-preview` command from sysc-shell (merged as sysc-shell#159, `0a70d144`).

**Spec:** `docs/superpowers/specs/2026-10-09-plugin-panel-captures-design.md` (this branch). This plan covers the helper, the Makefile targets, docs, and three pilot scenes. The other 13 scenes are planned after the pilots prove the pattern, because each depends on reading that plugin's state types.

## Global Constraints

- **Fictional data only.** The repository is public: invented names, titles, numbers, hosts and repositories; never real accounts, devices, paths or message text.
- Scenes call each plugin's **real** panel builder with its real state types; never hand-built wire nodes.
- A scene does nothing unless `CAPTURE=1`; a normal `go test` must write no files.
- Capture size is the first panel's `width`×`height` from the plugin's own `manifest.json`; the command's default scale (150%) applies.
- No `go.mod` / `go.sum` change. No generated `screenshot.png` or `thumbnail.webp` is committed by this plan: the validator forbids a grandfathered plugin from having them, and the backfill PR adds them together with removing the grandfather lines.
- The helper does not import the shell's renderer; it runs `sysc-panel-preview` as a subprocess (`$SYSC_PANEL_PREVIEW`, else `$PATH`).
- Go commands must be capped on this machine: `go test -count=1 -p 2 <one package>`; never an uncapped `./...`.
- Commit messages contain **no** AI attribution (the repo's hook rejects it). The bd pre-commit hook fails in fresh worktrees: commit with a scratch DB: `cp /home/nomadx/sysc-plugins/.beads/beads.db "$SCRATCH/beads.db" && sqlite3 "$SCRATCH/beads.db" 'delete from dirty_issues;'` then `BEADS_DB="$SCRATCH/beads.db" git commit ...`.
- Work in a worktree off the spec branch: `git worktree add -b feat/plugin-captures ~/worktrees/sysc-plugins-captures-impl docs/plugin-panel-captures-spec`. Paths below are relative to it.
- `gofmt -l .` prints nothing before each commit.
- **Prerequisite:** `sysc-panel-preview` built from sysc-shell `main` (`0a70d144` or later): `cd <sysc-shell checkout of origin/main> && go build -o "$SCRATCH/bin/sysc-panel-preview" ./cmd/sysc-panel-preview`. Use `SYSC_PANEL_PREVIEW="$SCRATCH/bin/sysc-panel-preview"` for the capture runs below.

## Review Focus

1. **`sysc-panel-preview` missing or not on PATH:** fail with the `go install ...@latest` line and the override variable, not a bare exec error. Task 1 (`TestFindCommandPrefersTheEnvironmentThenPathThenExplainsHowToInstall`); `make captures` also checks first (Task 2).
2. **A scene whose tree the host cannot lay out:** fail naming the node path, and run no process. Task 1 (`TestRenderRefusesATreeThatDoesNotLayOutBeforeRunningAnything`).
3. **A normal `go test` must never write screenshots.** Task 1 (`TestCaptureIsOffUnlessAskedFor`); Task 2 Step 4 checks `git status` after a plain run.
4. **The command's stderr is noisy** (font-scan log lines on every run): a failure message must show the real reason, not the log. Task 1 (`TestRenderReportsTheCommandsFailureWithoutFontScanNoise`).
5. **The command "succeeds" but the output is not a usable PNG:** fail rather than leave a bad `screenshot.png`. Task 1 (`TestRenderRejectsAnOutputThatIsNotAPNG`).

## File Structure

| Path | Responsibility |
|---|---|
| `internal/capture/capture.go` | `Panel`, `render`, manifest panel size, command lookup, repo root, stderr filtering |
| `internal/capture/capture_test.go` | Tests using a stand-in command script |
| `plugins/timer/capture_test.go`, `plugins/world-clock/capture_test.go`, `plugins/github-notifications/capture_test.go` | The three pilot scenes |
| `Makefile` | `capture-check`, `capture`, `captures` |
| `docs/publishing.md`, `docs/writing-plugins.md` | The capture workflow and the fictional-data rule |

---

### Task 1: The capture helper

**Files:**
- Create: `internal/capture/capture.go`
- Test: `internal/capture/capture_test.go`

**Interfaces:**
- Produces: `func Panel(t testing.TB, dir string, tree *v1.Node)`; unexported `render(root, dir string, tree *v1.Node, command string) (string, error)`, `panelSize`, `findCommand`, `repoRoot`, `enabled`, `withoutFontScan`. Scenes (Task 2) call only `Panel`.

- [ ] **Step 1: Write the failing tests**

Create `internal/capture/capture_test.go`:

```go
package capture

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// repoWith builds a minimal repository with one plugin, "demo", whose first
// panel is 300x200.
func repoWith(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(modulePath+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "plugins", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const demoManifest = `{"panels":[{"id":"panel","width":300,"height":200}]}`

// standIn writes a shell script that plays sysc-panel-preview. Its body runs
// after the argument loop, which sets $out to the -o value and logs the
// arguments and standard input under $CAPTURE_LOG.
func standIn(t *testing.T, body string) (command, logDir string) {
	t.Helper()
	logDir = t.TempDir()
	t.Setenv("CAPTURE_LOG", logDir)
	fixture := filepath.Join(logDir, "fixture.png")
	f, err := os.Create(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Setenv("CAPTURE_FIXTURE", fixture)

	script := `#!/bin/sh
printf '%s\n' "$@" > "$CAPTURE_LOG/args"
cat > "$CAPTURE_LOG/stdin"
while [ $# -gt 0 ]; do if [ "$1" = "-o" ]; then out="$2"; fi; shift; done
` + body + "\n"
	command = filepath.Join(t.TempDir(), "sysc-panel-preview")
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return command, logDir
}

func okTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: 8, Children: []*v1.Node{{Kind: v1.KindText, Text: "hello"}}}
}

func TestRenderRunsTheCommandAtTheManifestSize(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, logs := standIn(t, `cp "$CAPTURE_FIXTURE" "$out"`)

	out, err := render(root, "demo", okTree(), command)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "plugins", "demo", "screenshot.png")
	if out != want {
		t.Fatalf("wrote %s, want %s", out, want)
	}
	args, _ := os.ReadFile(filepath.Join(logs, "args"))
	for _, w := range []string{"-width\n300", "-height\n200", "-o\n" + want} {
		if !strings.Contains(string(args), w) {
			t.Errorf("arguments %q lack %q", args, w)
		}
	}
	stdin, _ := os.ReadFile(filepath.Join(logs, "stdin"))
	var sent v1.Node
	if err := json.Unmarshal(stdin, &sent); err != nil || sent.Kind != v1.KindColumn || len(sent.Children) != 1 {
		t.Fatalf("the command was sent %q, want the panel tree as JSON (err %v)", stdin, err)
	}
}

func TestRenderRefusesATreeThatDoesNotLayOutBeforeRunningAnything(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, logs := standIn(t, `cp "$CAPTURE_FIXTURE" "$out"`)
	// An 8-padded row 28 tall leaves 12 px for a 20 px icon: the host refuses it.
	bad := &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 28, Padding: 8, Children: []*v1.Node{{Kind: v1.KindIcon, Icon: "battery_full", IconSize: 20}}},
	}}
	_, err := render(root, "demo", bad, command)
	if err == nil || !strings.Contains(err.Error(), "does not lay out at 300x200") || !strings.Contains(err.Error(), "root.children[0]") {
		t.Fatalf("err = %v, want a layout refusal naming the node", err)
	}
	if _, statErr := os.Stat(filepath.Join(logs, "args")); statErr == nil {
		t.Error("the command ran for a tree that cannot lay out")
	}
}

func TestRenderReportsTheCommandsFailureWithoutFontScanNoise(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, _ := standIn(t, `echo "fontscan 2026/10/09 using system font dirs" >&2; echo "sysc-panel-preview: boom" >&2; exit 1`)
	_, err := render(root, "demo", okTree(), command)
	if err == nil || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "fontscan") {
		t.Fatalf("err = %v, want the command's own message and none of the font-scan log", err)
	}
}

func TestRenderRejectsAnOutputThatIsNotAPNG(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, _ := standIn(t, `echo hello > "$out"`)
	if _, err := render(root, "demo", okTree(), command); err == nil || !strings.Contains(err.Error(), "not a PNG") {
		t.Fatalf("err = %v, want a not-a-PNG failure", err)
	}
}

func TestRenderNeedsAPanelWithASize(t *testing.T) {
	for name, manifest := range map[string]string{
		"no panels":  `{"panels":[]}`,
		"zero width": `{"panels":[{"id":"p","width":0,"height":200}]}`,
		"bad json":   `{`,
	} {
		t.Run(name, func(t *testing.T) {
			root := repoWith(t, manifest)
			command, _ := standIn(t, `cp "$CAPTURE_FIXTURE" "$out"`)
			if _, err := render(root, "demo", okTree(), command); err == nil {
				t.Fatal("rendered without a usable panel size")
			}
		})
	}
}

func TestFindCommandPrefersTheEnvironmentThenPathThenExplainsHowToInstall(t *testing.T) {
	t.Setenv(envCommand, "/opt/custom/sysc-panel-preview")
	if got, err := findCommand(); err != nil || got != "/opt/custom/sysc-panel-preview" {
		t.Fatalf("findCommand = %q, %v; want the environment's path", got, err)
	}

	t.Setenv(envCommand, "")
	t.Setenv("PATH", t.TempDir())
	_, err := findCommand()
	if err == nil || !strings.Contains(err.Error(), installHint) || !strings.Contains(err.Error(), envCommand) {
		t.Fatalf("err = %v, want the install line and the override variable", err)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, commandName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got, err := findCommand(); err != nil || got != filepath.Join(bin, commandName) {
		t.Fatalf("findCommand = %q, %v; want the one on PATH", got, err)
	}
}

func TestRepoRootWalksUpToTheModule(t *testing.T) {
	root := repoWith(t, demoManifest)
	t.Chdir(filepath.Join(root, "plugins", "demo"))
	got, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(root); got != want && got != root {
		t.Fatalf("repoRoot = %s, want %s", got, root)
	}

	t.Chdir(t.TempDir())
	if _, err := repoRoot(); err == nil {
		t.Fatal("repoRoot found a module outside the repository")
	}
}

func TestCaptureIsOffUnlessAskedFor(t *testing.T) {
	t.Setenv(envCapture, "")
	if enabled() {
		t.Error("capture is on without CAPTURE=1")
	}
	t.Setenv(envCapture, "0")
	if enabled() {
		t.Error("capture is on for CAPTURE=0")
	}
	t.Setenv(envCapture, "1")
	if !enabled() {
		t.Error("capture is off for CAPTURE=1")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -count=1 -p 2 ./internal/capture/ 2>&1 | head -6`
Expected: FAIL to build, `undefined: modulePath`, `undefined: render`, `undefined: findCommand`.

- [ ] **Step 3: Implement `internal/capture/capture.go`**

```go
// Package capture turns a plugin's panel view tree into the screenshot.png its
// catalog thumbnail is made from. A plugin's capture_test.go builds the real
// panel tree from fictional fixture data and calls Panel; with CAPTURE=1 the
// tree is rendered by sysc-shell's sysc-panel-preview command at the panel size
// the plugin's own manifest declares and written next to the plugin.
package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	envCapture  = "CAPTURE"
	envCommand  = "SYSC_PANEL_PREVIEW"
	commandName = "sysc-panel-preview"
	installHint = "go install github.com/Nomadcxx/sysc-shell/cmd/sysc-panel-preview@latest"
	modulePath  = "module github.com/Nomadcxx/sysc-plugins"
)

// Panel renders tree as the first panel of plugins/<dir> and writes
// plugins/<dir>/screenshot.png. It skips the test unless CAPTURE=1, so a normal
// test run never writes files.
func Panel(t testing.TB, dir string, tree *v1.Node) {
	t.Helper()
	if !enabled() {
		t.Skip("set CAPTURE=1 to write plugins/" + dir + "/screenshot.png")
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	command, err := findCommand()
	if err != nil {
		t.Fatal(err)
	}
	out, err := render(root, dir, tree, command)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}

// enabled reports whether this run should write screenshots.
func enabled() bool { return os.Getenv(envCapture) == "1" }

// render does the work of Panel and returns the path it wrote.
func render(root, dir string, tree *v1.Node, command string) (string, error) {
	width, height, err := panelSize(filepath.Join(root, "plugins", dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	// The host refuses a view that cannot lay out at the manifest's size, and the
	// plugin's own fit test can pass while real layout fails; say so here, with
	// the offending node, before any process runs.
	if findings := shelllint.Tree(tree, v1.ViewPanel, width, height); len(findings) > 0 {
		var b strings.Builder
		for _, f := range findings {
			b.WriteString("\n  " + f.String())
		}
		return "", fmt.Errorf("plugins/%s: the panel does not lay out at %dx%d:%s", dir, width, height, b.String())
	}
	data, err := json.Marshal(tree)
	if err != nil {
		return "", err
	}

	out := filepath.Join(root, "plugins", dir, "screenshot.png")
	cmd := exec.Command(command, "-width", strconv.Itoa(width), "-height", strconv.Itoa(height), "-o", out)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("plugins/%s: %s failed: %w\n%s", dir, filepath.Base(command), err, withoutFontScan(stderr.String()))
	}

	f, err := os.Open(out)
	if err != nil {
		return "", fmt.Errorf("plugins/%s: %s reported success but wrote no file: %w", dir, filepath.Base(command), err)
	}
	defer f.Close()
	if _, err := png.DecodeConfig(f); err != nil {
		return "", fmt.Errorf("plugins/%s: %s wrote a file that is not a PNG: %w", dir, filepath.Base(command), err)
	}
	return out, nil
}

// panelSize reads the first panel's declared size from a plugin manifest.
func panelSize(manifestPath string) (width, height int, err error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return 0, 0, err
	}
	var m struct {
		Panels []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return 0, 0, fmt.Errorf("%s: %w", manifestPath, err)
	}
	if len(m.Panels) == 0 || m.Panels[0].Width <= 0 || m.Panels[0].Height <= 0 {
		return 0, 0, fmt.Errorf("%s declares no panel with a size", manifestPath)
	}
	return m.Panels[0].Width, m.Panels[0].Height, nil
}

// findCommand locates sysc-panel-preview: $SYSC_PANEL_PREVIEW, else $PATH.
func findCommand() (string, error) {
	if p := os.Getenv(envCommand); p != "" {
		return p, nil
	}
	p, err := exec.LookPath(commandName)
	if err != nil {
		return "", fmt.Errorf("%s is not installed (or set %s to its path); install it with:\n  %s", commandName, envCommand, installHint)
	}
	return p, nil
}

// repoRoot walks up from the working directory to the sysc-plugins go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(data), modulePath) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("capture: not inside the sysc-plugins repository")
		}
		dir = parent
	}
}

// withoutFontScan drops the font scanner's log lines, which the command prints
// on every run and which would bury the real reason in a failure message.
func withoutFontScan(stderr string) string {
	var keep []string
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if !strings.Contains(line, "fontscan") && strings.TrimSpace(line) != "" {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `gofmt -l internal; go vet -p 2 ./internal/capture/ && go test -race -count=1 -p 2 -v ./internal/capture/ 2>&1 | grep -E "^(--- |\s+--- FAIL|ok|FAIL)"`
Expected: eight `--- PASS` lines, then `ok`. (This code was prototyped and all eight tests passed against it.)

- [ ] **Step 5: Commit**

```bash
git add internal/capture
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(capture): render a plugin panel tree to screenshot.png with sysc-panel-preview"
```

---

### Task 2: Pilot scenes and Makefile targets

**Files:**
- Create: `plugins/timer/capture_test.go`, `plugins/world-clock/capture_test.go`, `plugins/github-notifications/capture_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `capture.Panel(t testing.TB, dir string, tree *v1.Node)` (Task 1); each plugin's `PanelTree` / `Panel` builder and state types.
- Produces: `make capture PLUGIN=<dir>`, `make captures`, `make capture-check`. The scene test name is `TestCapturePanel` in every plugin; `make captures` finds scenes by the `capture_test.go` file name.

- [ ] **Step 1: Write the three scenes**

`plugins/timer/capture_test.go`:

```go
package timer

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/timer/screenshot.png when CAPTURE=1: a work
// session in progress, two of four pomodoros done.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "timer", PanelTree("18:42", StateRunning, 0.75, ModeWork, 2, 4))
}
```

`plugins/world-clock/capture_test.go`:

```go
package worldclock

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/world-clock/screenshot.png when CAPTURE=1:
// five well-known cities, with the clock reading 12:04 in UTC.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "world-clock", Panel(PanelState{Readings: []Reading{
		{Zone: "America/New_York", Label: "New York", Clock: "08:04", Offset: "UTC-4", Relative: "−4h", Daytime: true},
		{Zone: "Europe/London", Label: "London", Clock: "13:04", Offset: "UTC+1", Relative: "+1h", Daytime: true, OnBar: true},
		{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "21:04", Offset: "UTC+9", Relative: "+9h", Daytime: false, OnBar: true},
		{Zone: "Australia/Sydney", Label: "Sydney", Clock: "22:04", Offset: "UTC+10", Relative: "+10h", Daytime: false},
		{Zone: "Pacific/Auckland", Label: "Auckland", Clock: "00:04", Offset: "UTC+12", Relative: "+12h", DayShift: 1, Daytime: false},
	}}))
}
```

`plugins/github-notifications/capture_test.go`:

```go
package githubnotifications

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/github-notifications/screenshot.png when
// CAPTURE=1: an inbox of eight invented notifications.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	item := func(id, title, kind, reason, repo, updated string, failure bool) Item {
		return Item{
			ID: id, Title: title, Type: kind, Reason: reason, ReasonLabel: FormatReason(reason),
			Repo: repo, URL: "https://github.com/" + repo, UpdatedAt: updated,
			RelativeTime: FormatRelative(updated, now), IsFailure: failure,
		}
	}
	items := []Item{
		item("101", "Retry failed uploads with backoff", "PullRequest", "review_requested", "acme/widgets", "2026-10-09T11:48:00Z", false),
		item("102", "Panel overflows on narrow displays", "Issue", "mention", "acme/dashboard", "2026-10-09T10:20:00Z", false),
		item("103", "CI failed on main", "CheckSuite", "ci_activity", "acme/widgets", "2026-10-09T09:05:00Z", true),
		item("104", "Add pagination to the activity feed", "PullRequest", "author", "acme/dashboard", "2026-10-08T17:30:00Z", false),
		item("105", "Release 2.4.0 is available", "Release", "subscribed", "acme/toolkit", "2026-10-08T08:00:00Z", false),
		item("106", "Bump the image decoder to 1.9", "PullRequest", "assign", "acme/toolkit", "2026-10-07T15:10:00Z", false),
		item("107", "Settings page loses scroll position", "Issue", "comment", "acme/dashboard", "2026-10-07T09:45:00Z", false),
		item("108", "Document the export format", "PullRequest", "mention", "acme/widgets", "2026-10-06T13:20:00Z", false),
	}
	capture.Panel(t, "github-notifications", PanelTreeForState(PanelState{
		Mode: ModeInbox, StatusLine: "8 unread",
		Inbox: InboxSnapshot{Status: StatusReady, Items: items},
	}))
}
```

- [ ] **Step 2: Verify the scenes are inert without `CAPTURE=1`**

Run:

```bash
go test -count=1 -p 2 -v -run TestCapturePanel ./plugins/timer/ ./plugins/world-clock/ ./plugins/github-notifications/ 2>&1 | grep -E "SKIP|--- |ok"
git status --short | grep -E "screenshot.png|thumbnail.webp"; echo "files written: $?"
```

Expected: each test `--- SKIP` with the `set CAPTURE=1` message; `ok` per package; the `grep` finds no new files (prints nothing; `files written: 1`).

- [ ] **Step 3: Run the captures against the real command**

Run (with `SYSC_PANEL_PREVIEW` set to the prerequisite build):

```bash
for p in timer world-clock github-notifications; do
  CAPTURE=1 go test -count=1 -p 2 -run TestCapturePanel -v ./plugins/$p/ 2>&1 | grep -E "wrote|FAIL|ok"
done
ls -l plugins/timer/screenshot.png plugins/world-clock/screenshot.png plugins/github-notifications/screenshot.png
```

Expected: a `wrote .../screenshot.png` line and `ok` for each; three PNG files (about 60–110 KB each). Open one: a dark shell panel with the plugin's real UI (Timer ring at 18:42; five clocks with sun and moon icons; an inbox of eight notifications). Then check the whole card pipeline for one: `go run ./tools/thumbnail -plugin plugins/timer` and open `plugins/timer/thumbnail.webp` (`magick plugins/timer/thumbnail.webp "$SCRATCH/timer-card.png"`).

If a scene fails with `does not lay out`, the message names the node path and the size; fix the fixture (shorter text, fewer rows), never the helper.

- [ ] **Step 4: Remove the generated files**

These plugins are grandfathered, and the validator forbids a grandfathered plugin from having these files, so none of them is committed here:

```bash
rm -f plugins/timer/screenshot.png plugins/timer/thumbnail.webp plugins/world-clock/screenshot.png plugins/world-clock/thumbnail.webp plugins/github-notifications/screenshot.png plugins/github-notifications/thumbnail.webp
go run ./tools/validate-manifests >/dev/null && echo validate-ok
git status --short
```

Expected: `validate-ok`; `git status` lists only the three `capture_test.go` files (and `internal/capture`, if not yet committed).

- [ ] **Step 5: Add the Makefile targets**

In `Makefile`, change `.PHONY` to include `capture-check capture captures`, and add after the `thumbnails:` target:

```make
# Screenshots come from fixture-driven scenes (plugins/*/capture_test.go and
# cmd/*/capture_test.go) rendered by sysc-shell's sysc-panel-preview command.
CAPTURE_PKGS := $(patsubst %/capture_test.go,./%,$(wildcard plugins/*/capture_test.go cmd/*/capture_test.go))

capture-check:
	@bin="$${SYSC_PANEL_PREVIEW:-$$(command -v sysc-panel-preview)}"; \
	[ -n "$$bin" ] || { echo "sysc-panel-preview is not installed (or set SYSC_PANEL_PREVIEW); run:" >&2; \
		echo "  go install github.com/Nomadcxx/sysc-shell/cmd/sysc-panel-preview@latest" >&2; exit 1; }; \
	echo "using $$bin"

# make capture PLUGIN=timer: one plugin's screenshot and thumbnail.
capture: capture-check
	@[ -n "$(PLUGIN)" ] || { echo "usage: make capture PLUGIN=<dir>" >&2; exit 2; }
	CAPTURE=1 go test -count=1 -p 2 -run '^TestCapturePanel$$' ./plugins/$(PLUGIN)/
	go run ./tools/thumbnail -plugin plugins/$(PLUGIN)

# make captures: every plugin that has a scene, then every thumbnail.
captures: capture-check
	@set -e; for pkg in $(CAPTURE_PKGS); do \
		echo "capture $$pkg"; \
		CAPTURE=1 go test -count=1 -p 2 -run '^TestCapturePanel$$' "$$pkg"; \
	done
	go run ./tools/thumbnail
```

- [ ] **Step 6: Verify the targets**

Run:

```bash
make capture-check                      # prints "using <path>" with the command set, exits 0
env -u SYSC_PANEL_PREVIEW PATH=/usr/bin make capture-check; echo "exit=$?"    # install hint, exit 1
make capture                            # usage message, exit 2
make capture PLUGIN=timer               # writes plugins/timer/screenshot.png and thumbnail.webp
rm -f plugins/timer/screenshot.png plugins/timer/thumbnail.webp
```

Expected: as commented. (`make captures` is exercised by the backfill PR, when every plugin has a scene; here it would run the three pilots and then `go run ./tools/thumbnail`, which processes only non-grandfathered plugins, so it is not run here.)

- [ ] **Step 7: Commit**

```bash
git add plugins/timer/capture_test.go plugins/world-clock/capture_test.go plugins/github-notifications/capture_test.go Makefile
BEADS_DB="$SCRATCH/beads.db" git commit -m "feat(capture): pilot scenes for Timer, World Clock and GitHub Notifications"
```

---

### Task 3: Documentation, checks and PR

**Files:**
- Modify: `docs/publishing.md`, `docs/writing-plugins.md`

- [ ] **Step 1: Document the workflow**

In `docs/publishing.md`, replace the paragraph that begins `Capture the plugin in a representative state:` (through `wide one is centred whole.`) **and** the line `Then generate the thumbnail:` that follows it (the `go run ./tools/thumbnail -plugin plugins/<dir>` code block after that line stays) with:

```markdown
Capture the plugin in a representative state: a panel with real-looking data,
not an empty or loading view. The capture must be a PNG, at least 320 px wide
and at most 4 MiB; a tall panel bleeds off the card with a fade, a short or
wide one is centred whole.

Captures are generated from fixture data, not taken by hand. Each plugin has a
`capture_test.go` that builds its real panel tree from invented data and, with
`CAPTURE=1`, renders it through sysc-shell's `sysc-panel-preview` at the panel
size its manifest declares (see `plugins/timer/capture_test.go`). Install the
command once per machine, then:

```sh
go install github.com/Nomadcxx/sysc-shell/cmd/sysc-panel-preview@latest
make capture PLUGIN=<dir>     # screenshot.png and thumbnail.webp for one plugin
make captures                 # every plugin that has a scene, then every thumbnail
```

Scenes use fictional data only: the repository is public, so never put real
accounts, devices, paths, hosts or message text in a fixture. A scene calls the
plugin's real panel builder, so the screenshot changes when the UI does; rerun
`make capture` after a visual change. Captures use the fonts installed on the
machine, so they are not byte-reproducible; commit the result. A plain
`go test` runs no scene.

To use a hand-made `screenshot.png` instead, generate the thumbnail yourself:
```

The existing code block (`go run ./tools/thumbnail -plugin plugins/<dir>`) follows this text directly.

In `docs/writing-plugins.md`, replace item 4 of "Adding a plugin" with:

```markdown
4. Add a scene, `plugins/<name>/capture_test.go` (copy `plugins/timer/capture_test.go`),
   that builds your real panel from invented data, then run
   `make capture PLUGIN=<name>` and commit the `screenshot.png` and
   `thumbnail.webp` it writes. CI fails a new plugin without them. See
   "Thumbnails" in [publishing.md](publishing.md).
```

- [ ] **Step 2: Verify the whole change**

Run, one at a time:

```bash
gofmt -l .
go vet -p 2 ./internal/capture/ ./plugins/timer/ ./plugins/world-clock/ ./plugins/github-notifications/
go test -race -count=1 -p 2 ./internal/capture/
go test -count=1 -p 2 ./plugins/timer/
go test -count=1 -p 2 ./plugins/world-clock/
go test -count=1 -p 2 ./plugins/github-notifications/
go run ./tools/validate-manifests >/dev/null && echo validate-ok
go run ./tools/thumbnail -check && echo thumbnails-ok
git diff origin/main --stat -- go.mod go.sum
```

Expected: no gofmt output; vet clean; every package `ok` (the scenes report as skipped); `validate-ok`; `thumbnails-ok`; and the last command prints nothing.

- [ ] **Step 3: Commit, push, open the PR**

```bash
git add docs
BEADS_DB="$SCRATCH/beads.db" git commit -m "docs: describe generated captures"
git push -u origin feat/plugin-captures
gh pr create --repo Nomadcxx/sysc-plugins --base main --head feat/plugin-captures \
  --title "feat: generated panel captures (helper and pilot scenes)" --body "..."
```

PR body: why (the 16 plugins need screenshots; hand captures on a laptop do not scale and go stale), what (`internal/capture`, `make capture` / `captures`, three pilot scenes, docs), the dependency on sysc-shell#159's `sysc-panel-preview` (no `go.mod` change; a plain `go test` runs no scene), what is not here (the other 13 scenes and the generated images; the backfill PR adds them with the grandfather removals), and the verification run. Link the spec and this plan. End with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`. The PR includes the spec, which is already on this branch's base. Do not merge without the user's go-ahead.

---

## Self-Review

**Spec coverage** (spec components 2 to 4 and the delivery order): the capture helper with `CAPTURE=1`, manifest size, `plugin/lint`, stand-in-command tests (Task 1); scenes beside each plugin in the plugin's own package, fictional data, real builders (Task 2, three of sixteen); `make captures` and `make capture PLUGIN=` (Task 2); `make captures` prints the binary it found (`capture-check`); docs including the fictional-data rule (Task 3); no `go.mod` change and no generated images in this PR (Global Constraints, Task 2 Step 4, Task 3 Step 2). Not covered here, by design: the other 13 scenes and the backfill PR.

**Placeholder scan:** none. The PR body is described, not quoted, because it depends on what was run.

**Type consistency:** `capture.Panel(t testing.TB, dir string, tree *v1.Node)` is used identically by the three scenes; `render(root, dir, tree, command)` is the same in the helper and its tests; `enabled`, `findCommand`, `repoRoot`, `panelSize`, `withoutFontScan` names match across Task 1.

**Risks the executor should know**
- Screenshots depend on installed fonts; they are committed inputs, and only the thumbnails made from them are byte-checked.
- Wide panels (Notes 760 px, Moonbit 758 px) shrink to about 0.4× on the card; judge them when their scenes are written.
- The helper needs a Unix shell (`sh`) for its tests' stand-in command; CI is Ubuntu.
- Some plugins' panel builders may depend on state that is awkward to construct (images, running sessions); the pilots avoid those, and the follow-up plan handles them one by one.
