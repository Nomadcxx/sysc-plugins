// Package running answers "is this game live, and how do I stop it" from
// /proc, with no cooperation asked of the game.
package running

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Match struct {
	PID   int
	Start time.Time
}

// Scan maps gameDir -> a live process whose cmdline mentions that directory.
// Games start as group leaders (Lutris, wine, native alike), so Stop can
// signal the whole group.
// ponytail: cmdline-substring match — a false positive needs a process naming
// the game dir in argv; upgrade path is exe-path + ppid checks per runner.
func Scan(gameDirs []string, procRoot string) (map[string]Match, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Match{}, nil
		}
		return nil, err
	}
	out := map[string]Match{}
	pending := make(map[string]bool, len(gameDirs))
	for _, d := range gameDirs {
		if d != "" {
			pending[d] = true
		}
	}
	for _, e := range entries {
		if len(pending) == 0 {
			break
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		line := string(bytes.ReplaceAll(raw, []byte{0}, []byte(" ")))
		info, err := e.Info()
		if err != nil {
			continue
		}
		for dir := range pending {
			if strings.Contains(line, dir) {
				out[dir] = Match{PID: pid, Start: info.ModTime()}
				delete(pending, dir)
			}
		}
	}
	return out, nil
}

// Stop signals the whole process group: TERM, then KILL after grace.
func Stop(pid int, grace time.Duration) error {
	if err := kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal game group: %w", err)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if kill(-pid, 0) != nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return kill(-pid, syscall.SIGKILL)
}

var kill = syscall.Kill
