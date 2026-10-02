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
	pending := make(map[string]bool, len(gameDirs))
	for _, d := range gameDirs {
		if d != "" {
			pending[d] = true
		}
	}
	out := map[string]Match{}
	err := eachProc(procRoot, func(m Match, args [][]byte) bool {
		for dir := range pending {
			if matchesDir(args, dir) {
				out[dir] = m
				delete(pending, dir)
			}
		}
		return len(pending) > 0
	})
	return out, err
}

// wrapperPrefix is the title lutris-wrapper gives itself:
// setproctitle("lutris-wrapper: " + game name).
const wrapperPrefix = "lutris-wrapper: "

// ScanWrappers maps a Lutris game name to the lutris-wrapper process running
// it. Lutris starts every game under the wrapper and it lives as long as
// Lutris counts the game as running, whatever the runner, so it finds Wine
// and Steam games whose argv never names the install directory.
func ScanWrappers(names []string, procRoot string) (map[string]Match, error) {
	pending := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			pending[n] = true
		}
	}
	out := map[string]Match{}
	err := eachProc(procRoot, func(m Match, args [][]byte) bool {
		if name, ok := wrapperTitle(args); ok && pending[name] {
			out[name] = m
			delete(pending, name)
		}
		return len(pending) > 0
	})
	return out, err
}

// wrapperTitle reads the game name off a lutris-wrapper process: the
// retitled argv[0], or argv[2] when setproctitle is not installed and the
// wrapper still reads "python3 .../lutris-wrapper <name> ...".
func wrapperTitle(args [][]byte) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if first := strings.TrimRight(string(args[0]), " \x00"); strings.HasPrefix(first, wrapperPrefix) {
		return strings.TrimPrefix(first, wrapperPrefix), true
	}
	if len(args) >= 3 && filepath.Base(string(args[1])) == "lutris-wrapper" {
		return string(args[2]), true
	}
	return "", false
}

// eachProc calls fn with every live process's argv and start time until fn
// returns false. The start time is the /proc entry's mtime.
func eachProc(procRoot string, fn func(Match, [][]byte) bool) error {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !fn(Match{PID: pid, Start: info.ModTime()}, bytes.Split(raw, []byte{0})) {
			return nil
		}
	}
	return nil
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

// StopWrapper ends a game the way Lutris does: SIGTERM to its lutris-wrapper,
// which forwards it to every process it has reaped. A second SIGTERM after
// grace makes the wrapper SIGKILL them; SIGKILL to the wrapper itself is the
// last resort. Unlike Stop it never signals a process group: the wrapper may
// share Lutris's group, and Lutris must survive its game.
func StopWrapper(pid int, grace time.Duration) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGTERM} {
		if err := kill(pid, sig); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return fmt.Errorf("signal lutris-wrapper: %w", err)
		}
		if goneWithin(pid, grace) {
			return nil
		}
	}
	if err := kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill lutris-wrapper: %w", err)
	}
	return nil
}

func goneWithin(pid int, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if kill(pid, 0) != nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}
