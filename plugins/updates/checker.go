// Package updates checks for pending pacman/AUR/Flatpak updates and decides
// whether the running system needs a reboot to finish an earlier upgrade.
package updates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Source names where an update comes from.
type Source string

// The three backends the plugin knows how to read.
const (
	SourceRepo    Source = "repo"
	SourceAUR     Source = "aur"
	SourceFlatpak Source = "flatpak"
)

// Timeouts bound each backend's command.
const (
	RepoTimeout    = 120 * time.Second
	AURTimeout     = 120 * time.Second
	FlatpakTimeout = 60 * time.Second
)

// Update is one pending package upgrade.
type Update struct {
	Name   string `json:"name"`
	Old    string `json:"old,omitempty"`
	New    string `json:"new"`
	Source Source `json:"source"`
	Core   bool   `json:"core,omitempty"`
}

// Runner executes one command. A non-zero exit is reported in exit, never in
// err; err means the command could not run at all (including a deadline).
type Runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exit int, err error)

// ExecRunner is the Runner the plugin ships with.
func ExecRunner(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exit int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitCode(), nil
		}
		return outBuf.Bytes(), errBuf.Bytes(), -1, err
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0, nil
}

// CheckRepo runs checkupdates. Exit 0 lists updates, 2 means none pending,
// anything else is an error whose stderr carries the reason (mirror sync
// failure, busy database, offline).
func CheckRepo(ctx context.Context, run Runner) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, RepoTimeout)
	defer cancel()
	stdout, stderr, exit, err := run(ctx, "checkupdates")
	if err != nil {
		return nil, fmt.Errorf("checkupdates: %w", err)
	}
	switch exit {
	case 0:
		return parseArrowLines(stdout, SourceRepo), nil
	case 2:
		return nil, nil
	default:
		return nil, fmt.Errorf("checkupdates: %s", failureReason(stderr, exit))
	}
}

// CheckAUR runs the AUR helper's -Qua. A non-zero exit with no output means
// "nothing to report"; helpers exit non-zero on an empty queue too.
func CheckAUR(ctx context.Context, run Runner, helper string) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, AURTimeout)
	defer cancel()
	stdout, stderr, exit, err := run(ctx, helper, "-Qua")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", helper, err)
	}
	if exit != 0 && len(bytes.TrimSpace(stdout)) == 0 {
		return nil, nil
	}
	if exit != 0 {
		return nil, fmt.Errorf("%s: %s", helper, failureReason(stderr, exit))
	}
	return parseArrowLines(stdout, SourceAUR), nil
}

// CheckFlatpak runs flatpak remote-ls --updates. The columns are
// tab-separated and the version is often empty, in particular for runtimes
// such as org.gnome.Platform, so a ref without one still counts.
func CheckFlatpak(ctx context.Context, run Runner) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, FlatpakTimeout)
	defer cancel()
	stdout, stderr, exit, err := run(ctx, "flatpak", "remote-ls", "--updates", "--columns=ref,version")
	if err != nil {
		return nil, fmt.Errorf("flatpak: %w", err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("flatpak: %s", failureReason(stderr, exit))
	}
	type pendingRef struct {
		id, arch, branch, version string
	}
	var refs []pendingRef
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(stdout), "\n") {
		columns := strings.SplitN(line, "\t", 2)
		ref := strings.TrimSpace(columns[0])
		version := ""
		if len(columns) == 2 {
			version = strings.TrimSpace(columns[1])
		} else if fields := strings.Fields(ref); len(fields) > 1 {
			ref, version = fields[0], fields[1]
		}
		id, arch, branch, ok := parseFlatpakRef(ref)
		if !ok {
			continue
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, pendingRef{id: id, arch: arch, branch: branch, version: version})
	}

	counts := make(map[string]int, len(refs))
	for _, ref := range refs {
		counts[ref.id]++
	}
	out := make([]Update, 0, len(refs))
	for _, ref := range refs {
		name := ref.id
		if counts[ref.id] > 1 {
			name += " (" + ref.arch + ", " + ref.branch + ")"
		}
		out = append(out, Update{Name: name, New: ref.version, Source: SourceFlatpak})
	}
	return out, nil
}

func parseFlatpakRef(ref string) (id, arch, branch string, ok bool) {
	parts := strings.Split(ref, "/")
	if len(parts) != 4 || (parts[0] != "app" && parts[0] != "runtime") || !strings.Contains(parts[1], ".") || parts[2] == "" || parts[3] == "" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

// parseArrowLines reads the `name old -> new` listing both checkupdates and
// the AUR helpers print. Helpers append markers such as [ignored] after the
// new version; only the version itself is kept.
func parseArrowLines(data []byte, source Source) []Update {
	var out []Update
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		name, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		oldVersion, newVersion, ok := strings.Cut(rest, " -> ")
		if !ok {
			continue
		}
		oldVersion = strings.TrimSpace(oldVersion)
		if field := strings.IndexByte(newVersion, ' '); field >= 0 {
			newVersion = newVersion[:field]
		}
		newVersion = strings.TrimSpace(newVersion)
		if name == "" || oldVersion == "" || newVersion == "" {
			continue
		}
		out = append(out, Update{Name: name, Old: oldVersion, New: newVersion, Source: source, Core: isCore(name)})
	}
	return out
}

func failureReason(stderr []byte, exit int) string {
	text := strings.TrimSpace(string(stderr))
	if text == "" {
		return fmt.Sprintf("exited %d", exit)
	}
	if line, _, ok := strings.Cut(text, "\n"); ok {
		text = strings.TrimSpace(line)
	}
	return text
}

// isCore reports the package families shown with the core tag in the panel.
// Kernel headers and docs share the linux prefix but never need a reboot, so
// they stay out; the kernel packages, the firmware and the graphics stack are
// in.
func isCore(name string) bool {
	if name == "" || strings.HasSuffix(name, "-headers") || strings.HasSuffix(name, "-docs") {
		return false
	}
	switch name {
	case "linux", "nvidia", "systemd", "glibc", "mesa", "amd-ucode", "intel-ucode":
		return true
	}
	for _, prefix := range []string{"linux-lts", "linux-zen", "linux-hardened", "linux-rt", "linux-cachyos", "linux-firmware", "nvidia-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Counts returns how many updates each source contributes.
func Counts(updates []Update) (repo, aur, flatpak int) {
	for _, update := range updates {
		switch update.Source {
		case SourceRepo:
			repo++
		case SourceAUR:
			aur++
		case SourceFlatpak:
			flatpak++
		}
	}
	return repo, aur, flatpak
}

// CoreCount returns how many updates touch core packages.
func CoreCount(updates []Update) int {
	count := 0
	for _, update := range updates {
		if update.Core {
			count++
		}
	}
	return count
}
