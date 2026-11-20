package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// launcherAskDirName is the runtime-dir folder the hand-off lives in.
	launcherAskDirName = "sysc-hermes"
	launcherAskFile    = "ask"
	// launcherAskMax is the plan's 1 MB cap on one pending prompt.
	launcherAskMax = 1 << 20
	// launcherQueryMax keeps the Ask row well inside sysc-launch's 4 KiB
	// row limit and the launcher's own width.
	launcherQueryMax = 160
)

// launcherRow is one command-provider row: a label and its subtitle. The
// whole line, tab included, is what sysc-launch hands back as {selection}.
type launcherRow struct {
	name    string
	comment string
}

// launcherRows mirrors Noctalia's `>hermes` provider: ask (or a hint when
// nothing is typed), open the panel, start a session, resume the latest.
func launcherRows(query string) []string {
	q := launcherQueryLine(query)
	first := launcherRow{name: "Ask Hermes", comment: q}
	if q == "" {
		first = launcherRow{name: "Type a prompt for Hermes", comment: "Type after /hermes, then choose this row"}
	}
	rows := []launcherRow{
		first,
		{name: "Open Hermes panel", comment: "Show the chat panel"},
		{name: "Start Hermes session", comment: "Create a new session"},
		{name: "Resume latest Hermes session", comment: "Reopen the most recent session"},
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.comment == "" {
			out = append(out, row.name)
			continue
		}
		out = append(out, row.name+"\t"+row.comment)
	}
	return out
}

// launcherQueryLine flattens a typed prompt into one row-comment line:
// control characters become spaces, whitespace runs collapse, and a long
// prompt is cut so the row stays readable.
func launcherQueryLine(query string) string {
	flat := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, query)
	flat = strings.Join(strings.Fields(flat), " ")
	if utf8.RuneCountInString(flat) <= launcherQueryMax {
		return flat
	}
	runes := []rune(flat)
	return string(runes[:launcherQueryMax-1]) + "…"
}

// launcherSelectionAction maps the selected row back to the manifest action.
// ok is false for a row this provider did not print; an empty action is the
// hint row, which does nothing.
func launcherSelectionAction(selection string) (action string, ok bool) {
	name, _, _ := strings.Cut(selection, "\t")
	switch name {
	case "Ask Hermes":
		return "launcher-ask", true
	case "Type a prompt for Hermes":
		return "", true
	case "Open Hermes panel":
		return "open", true
	case "Start Hermes session":
		return "new-session", true
	case "Resume latest Hermes session":
		return "resume-latest", true
	}
	return "", false
}

// runLauncher serves the two subcommands the README's provider snippet runs.
// It never contacts the bridge and never starts the plugin protocol; the
// only process it runs is sysc-shell ipc plugin.action.
func runLauncher(args []string, stdout, stderr io.Writer, getenv func(string) string, run func([]string) error) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "sysc-plugin-hermes: missing launcher subcommand")
		return 2
	}
	switch args[0] {
	case "launcher-list":
		query := ""
		if len(args) > 1 {
			query = args[1]
		}
		for _, row := range launcherRows(query) {
			fmt.Fprintln(stdout, row)
		}
		return 0
	case "launcher-select":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "sysc-plugin-hermes: launcher-select needs the selected row")
			return 2
		}
		query := ""
		if len(args) > 2 {
			query = args[2]
		}
		return launcherSelect(args[1], query, stderr, getenv, run)
	}
	fmt.Fprintf(stderr, "sysc-plugin-hermes: unknown launcher subcommand %q\n", args[0])
	return 2
}

func launcherSelect(selection, query string, stderr io.Writer, getenv func(string) string, run func([]string) error) int {
	action, ok := launcherSelectionAction(selection)
	if !ok {
		name, _, _ := strings.Cut(selection, "\t")
		fmt.Fprintf(stderr, "sysc-plugin-hermes: launcher: unknown row %q\n", name)
		return 2
	}
	if action == "" {
		// The hint row only tells the user what to type.
		return 0
	}
	if action == "launcher-ask" {
		text := query
		if strings.TrimSpace(text) == "" {
			// The typed text is also the row's subtitle; fall back to it
			// when {query} arrives empty.
			_, comment, _ := strings.Cut(selection, "\t")
			text = comment
		}
		if strings.TrimSpace(text) == "" {
			fmt.Fprintln(stderr, "sysc-plugin-hermes: launcher: type a prompt after /hermes")
			return 2
		}
		dir, err := launcherAskDir(getenv)
		if err != nil {
			fmt.Fprintf(stderr, "sysc-plugin-hermes: launcher: %v\n", err)
			return 1
		}
		if err := writeLauncherAsk(dir, text); err != nil {
			fmt.Fprintf(stderr, "sysc-plugin-hermes: launcher: %v\n", err)
			return 1
		}
	}
	if err := run(pluginActionArgv(action)); err != nil {
		fmt.Fprintf(stderr, "sysc-plugin-hermes: launcher: %v\n", err)
		return 1
	}
	return 0
}

// pluginActionArgv is the documented service-action invocation. The payload
// carries no prompt; the launcher-ask action reads the hand-off file.
func pluginActionArgv(action string) []string {
	payload, err := json.Marshal(struct {
		PluginID  string `json:"plugin_id"`
		ServiceID string `json:"service_id"`
		ActionID  string `json:"action_id"`
	}{pluginID, serviceID, action})
	if err != nil {
		// Marshalling three strings cannot fail; keep the argv shape.
		return nil
	}
	return []string{"sysc-shell", "ipc", "plugin.action", string(payload)}
}

// runArgv runs one argv with no shell in between.
func runArgv(argv []string) error {
	if len(argv) == 0 {
		return errors.New("empty argv")
	}
	return exec.Command(argv[0], argv[1:]...).Run()
}

// launcherAskDir resolves $XDG_RUNTIME_DIR/sysc-hermes. The prompt never
// goes to /tmp: it is a one-shot hand-off scoped to this login.
func launcherAskDir(getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	root := getenv("XDG_RUNTIME_DIR")
	if root == "" {
		return "", errors.New("XDG_RUNTIME_DIR is not set; the launcher hand-off needs a runtime dir")
	}
	return filepath.Join(root, launcherAskDirName), nil
}

// writeLauncherAsk replaces the one pending prompt atomically: a 0600 temp
// file in the same 0700 folder, then rename over "ask".
func writeLauncherAsk(dir, text string) error {
	if len(text) > launcherAskMax {
		return errors.New("prompt is larger than the 1 MB hand-off limit")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll keeps an existing folder's mode; the plan wants 0700.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".ask-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	discard := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if err := file.Chmod(0o600); err != nil {
		discard()
		return err
	}
	if _, err := file.WriteString(text); err != nil {
		discard()
		return err
	}
	if err := file.Sync(); err != nil {
		discard()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, launcherAskFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// takeLauncherAsk reads the pending prompt and deletes it. A message is
// returned when there is nothing usable, so the handler can log it and keep
// serving; the hand-off is one item and is never retried.
func takeLauncherAsk(dir string) (string, string) {
	path := filepath.Join(dir, launcherAskFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "no pending launcher prompt"
	}
	if err != nil {
		return "", fmt.Sprintf("launcher hand-off: %v", err)
	}
	if !info.Mode().IsRegular() {
		_ = os.Remove(path)
		return "", "launcher hand-off is not a regular file; ignored"
	}
	if info.Size() > launcherAskMax {
		_ = os.Remove(path)
		return "", "launcher hand-off is larger than 1 MB; ignored"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Sprintf("launcher hand-off: %v", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Sprintf("launcher hand-off: %v", err)
	}
	if len(data) == 0 {
		return "", "launcher hand-off is empty; ignored"
	}
	return string(data), ""
}
