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
