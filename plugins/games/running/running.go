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
// Stop resolves the process group before signalling, so the match does not
// have to be the group leader.
// ponytail: argv match — a false positive needs a process naming the game dir
// in argv; upgrade path is exe-path + ppid checks per runner.
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
		args := bytes.Split(raw, []byte{0})
		info, err := e.Info()
		if err != nil {
			continue
		}
		for dir := range pending {
			if matchesDir(args, dir) {
				out[dir] = Match{PID: pid, Start: info.ModTime()}
				delete(pending, dir)
			}
		}
	}
	return out, nil
}

// matchesDir reports whether any argv entry is the install dir itself or a
// path inside it. Matching per argument (not substring) keeps sibling
// installs like "Hades" and "Hades II" from sharing a process.
func matchesDir(args [][]byte, dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return false
	}
	prefix := dir + "/"
	for _, arg := range args {
		if string(arg) == dir || strings.HasPrefix(string(arg), prefix) {
			return true
		}
	}
	return false
}

// Stop signals the whole process group: TERM, then KILL after grace. The
// matched pid may be a helper rather than the group leader, so resolve the
// group first.
func Stop(pid int, grace time.Duration) error {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("resolve process group: %w", err)
	}
	if own, err := syscall.Getpgid(0); err == nil && pgid == own {
		return fmt.Errorf("process %d shares this plugin's process group; refusing to signal it", pid)
	}
	pid = pgid
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
