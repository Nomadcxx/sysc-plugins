package protonvpn

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Commands that never name the real app: sandbox runners, shells and
// generic launchers, and distro dispatchers.
var excludedCommands = map[string]bool{
	"flatpak": true, "snap": true, "flatpak-spawn": true,
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "tcsh": true, "env": true, "gtk-launch": true, "xdg-open": true,
	"hyprctl": true, "uwsm": true, "xdg-terminal-exec": true, "dbus-launch": true,
}

func excludedCommand(cmd string) bool {
	return excludedCommands[cmd] ||
		strings.HasPrefix(cmd, "omarchy-launch-") ||
		strings.HasPrefix(cmd, "omarchy-webapp-handler-")
}

// executableFile is a regular runnable file without a setuid/setgid bit —
// those wrap the real app or are privileged tools, never split-tunnel
// candidates (port of noctalia's apps.py).
func executableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 {
		return false
	}
	return fi.Mode()&os.ModeSetuid == 0 && fi.Mode()&os.ModeSetgid == 0
}

// parseDesktop pulls Name and Exec out of a .desktop file's Desktop Entry
// group, rejecting hidden entries and non-applications. Localised keys
// (Name[de]=) miss the exact "Name=" prefix and are ignored by design.
func parseDesktop(path string) (name, exec string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	inGroup, typ := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "[Desktop Entry]":
			inGroup = true
		case strings.HasPrefix(line, "["), strings.HasPrefix(line, "#"):
			if strings.HasPrefix(line, "[") {
				inGroup = false
			}
		case !inGroup:
		case line == "Hidden=true", line == "NoDisplay=true":
			return "", "", false
		case strings.HasPrefix(line, "Type="):
			typ = strings.TrimPrefix(line, "Type=")
		case strings.HasPrefix(line, "Name=") && name == "":
			name = strings.TrimPrefix(line, "Name=")
		case strings.HasPrefix(line, "Exec=") && exec == "":
			exec = strings.TrimPrefix(line, "Exec=")
		}
	}
	return name, exec, typ == "Application" && name != "" && exec != ""
}

// execProgram reduces an Exec line to the executable it really launches:
// field codes and VAR=value/env wrappers stripped, resolved against PATH.
func execProgram(line string, pathEnv []string) (string, bool) {
	var fields []string
	for _, f := range strings.Fields(line) {
		if !strings.HasPrefix(f, "%") {
			fields = append(fields, f)
		}
	}
	for len(fields) > 0 {
		if fields[0] == "env" || strings.Contains(fields[0], "=") && !strings.Contains(fields[0], "/") {
			fields = fields[1:]
			continue
		}
		break
	}
	if len(fields) == 0 {
		return "", false
	}
	cmd := fields[0]
	if excludedCommand(filepath.Base(cmd)) {
		return "", false
	}
	if strings.Contains(cmd, "/") {
		if !filepath.IsAbs(cmd) || !executableFile(cmd) {
			return "", false
		}
		return cmd, true
	}
	for _, dir := range pathEnv {
		cand := filepath.Join(dir, cmd)
		if executableFile(cand) {
			return cand, true
		}
	}
	return "", false
}

// ScanApps collects the launcher executables offered by the .desktop files
// in dataDirs, deduped by resolved path (shortest label wins) and sorted by
// label then path.
func ScanApps(dataDirs []string, pathEnv []string) []App {
	best := make(map[string]App)
	for _, dir := range dataDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			name, exec, ok := parseDesktop(filepath.Join(dir, e.Name()))
			if !ok {
				continue
			}
			path, ok := execProgram(exec, pathEnv)
			if !ok {
				continue
			}
			if cur, seen := best[path]; !seen || len(name) < len(cur.Label) {
				best[path] = App{Value: path, Label: name}
			}
		}
	}
	if len(best) == 0 {
		return nil
	}
	apps := make([]App, 0, len(best))
	for _, a := range best {
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Label != apps[j].Label {
			return apps[i].Label < apps[j].Label
		}
		return apps[i].Value < apps[j].Value
	})
	return apps
}
