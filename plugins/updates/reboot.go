package updates

import (
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// RebootNeeded reports whether a kernel or core package installed on the
// system is newer than what the running system loaded, so a reboot would
// finish the upgrade. fsys is rooted at /; tests pass an fstest.MapFS.
// bootTime is the system boot time; a zero boot time disables the log rule.
func RebootNeeded(fsys fs.FS, uname string, bootTime time.Time) (bool, string) {
	if reason, ok := modulesReboot(fsys, uname); ok {
		return true, reason
	}
	if reason, ok := logReboot(fsys, bootTime); ok {
		return true, reason
	}
	return false, ""
}

// modulesReboot compares the running kernel against the module trees on
// disk. When the running release has no tree left, or a tree for the same
// kernel package is newer, the kernel was upgraded under the running system.
func modulesReboot(fsys fs.FS, uname string) (string, bool) {
	if uname == "" {
		return "", false
	}
	entries, err := fs.ReadDir(fsys, "usr/lib/modules")
	if err != nil {
		return "", false
	}
	runningPkgbase := ""
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == uname {
			runningPkgbase = pkgbase(fsys, entry.Name())
		}
	}
	if runningPkgbase == "" {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := pkgbase(fsys, entry.Name())
			if name == "" {
				name = "linux"
			}
			return fmt.Sprintf("%s %s is installed, %s is running", name, entry.Name(), uname), true
		}
		return "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == uname {
			continue
		}
		if pkgbase(fsys, entry.Name()) == runningPkgbase {
			return fmt.Sprintf("%s %s is installed, %s is running", runningPkgbase, entry.Name(), uname), true
		}
	}
	return "", false
}

func pkgbase(fsys fs.FS, moduleDir string) string {
	data, err := fs.ReadFile(fsys, "usr/lib/modules/"+moduleDir+"/pkgbase")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// logReboot scans /var/log/pacman.log for a core package upgraded after the
// system booted: the running kernel or session still holds the old files.
func logReboot(fsys fs.FS, bootTime time.Time) (string, bool) {
	if bootTime.IsZero() {
		return "", false
	}
	data, err := fs.ReadFile(fsys, "var/log/pacman.log")
	if err != nil {
		return "", false
	}
	newest := ""
	newestAt := time.Time{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		// [2026-10-08T05:00:00+0000] [ALPM] upgraded linux (6.16.9 -> 6.17.1)
		if len(fields) < 4 || fields[1] != "[ALPM]" || fields[2] != "upgraded" {
			continue
		}
		when, err := time.Parse("2006-01-02T15:04:05-0700", strings.Trim(fields[0], "[]"))
		if err != nil || !when.After(bootTime) {
			continue
		}
		if !isCore(fields[3]) {
			continue
		}
		if when.After(newestAt) {
			newest, newestAt = fields[3], when
		}
	}
	if newest == "" {
		return "", false
	}
	return fmt.Sprintf("%s was upgraded after boot", newest), true
}
