package hermes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// MaxStateFileBytes caps a state.json read, the same as a /state reply.
const MaxStateFileBytes = stateResponseCap

// DefaultWatchPoll is the stat-poll interval when inotify is unavailable.
const DefaultWatchPoll = 500 * time.Millisecond

// ErrInvalidStateFile is a state file that is not one bounded JSON object.
var ErrInvalidStateFile = errors.New("Invalid state file")

// ReadStateFile reads the bridge's state.json with the 4 MiB cap and runs it
// through the same normalizer as /state. A missing file is os.ErrNotExist;
// anything unreadable as a state is ErrInvalidStateFile.
func ReadStateFile(path string) (State, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
		return State{}, ErrInvalidStateFile
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return State{}, ErrInvalidStateFile
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxStateFileBytes+1))
	if err != nil || len(data) > MaxStateFileBytes {
		return State{}, ErrInvalidStateFile
	}
	var s State
	if err := s.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
		return State{}, ErrInvalidStateFile
	}
	s.Normalize()
	return s, nil
}

// Watcher reports changes to a fixed set of files: inotify on their
// directories, or a stat poll when inotify cannot watch them.
type Watcher struct {
	paths   []string
	poll    time.Duration
	polling bool

	changed chan struct{}
	done    chan struct{}

	mu      sync.Mutex
	pending map[string]bool
}

// WatchFiles starts a watcher that runs until ctx ends. forcePoll skips
// inotify; tests use it to cover the fallback.
func WatchFiles(ctx context.Context, paths []string, poll time.Duration, forcePoll bool) *Watcher {
	if poll <= 0 {
		poll = DefaultWatchPoll
	}
	w := &Watcher{
		poll:    poll,
		changed: make(chan struct{}, 1),
		done:    make(chan struct{}),
		pending: map[string]bool{},
	}
	for _, p := range paths {
		w.paths = append(w.paths, filepath.Clean(p))
	}
	var in *inotify
	if !forcePoll {
		in, _ = newInotify(w.paths)
	}
	w.polling = in == nil
	// Stamp before returning, so a change right after WatchFiles is seen.
	last := w.stamps()
	go func() {
		defer close(w.done)
		if in != nil && in.run(ctx, w) {
			return
		}
		// inotify was unavailable or lost its directory.
		w.runPoll(ctx, last)
	}()
	return w
}

// Polling reports whether the watcher started in stat-poll mode.
func (w *Watcher) Polling() bool { return w.polling }

// Changed signals that Drain has paths to report.
func (w *Watcher) Changed() <-chan struct{} { return w.changed }

// Done closes when the watcher has stopped.
func (w *Watcher) Done() <-chan struct{} { return w.done }

// Drain returns and clears the paths changed since the last Drain.
func (w *Watcher) Drain() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, p := range w.paths {
		if w.pending[p] {
			out = append(out, p)
		}
	}
	w.pending = map[string]bool{}
	return out
}

func (w *Watcher) mark(path string) {
	w.mu.Lock()
	w.pending[path] = true
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

type fileStamp struct {
	exists bool
	size   int64
	mod    time.Time
	ino    uint64
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	st := fileStamp{exists: true, size: info.Size(), mod: info.ModTime()}
	if sys, ok := info.Sys().(*unix.Stat_t); ok {
		st.ino = sys.Ino
	}
	return st
}

func (w *Watcher) stamps() map[string]fileStamp {
	out := map[string]fileStamp{}
	for _, p := range w.paths {
		out[p] = stampOf(p)
	}
	return out
}

func (w *Watcher) runPoll(ctx context.Context, last map[string]fileStamp) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, p := range w.paths {
			if st := stampOf(p); st != last[p] {
				last[p] = st
				w.mark(p)
			}
		}
	}
}

type inotify struct {
	f    *os.File
	dirs map[int32]string
}

const inotifyMask = unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO | unix.IN_MOVED_FROM |
	unix.IN_CREATE | unix.IN_DELETE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

func newInotify(paths []string) (*inotify, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	in := &inotify{dirs: map[int32]string{}}
	seen := map[string]bool{}
	for _, p := range paths {
		dir := filepath.Dir(p)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		wd, err := unix.InotifyAddWatch(fd, dir, inotifyMask)
		if err != nil {
			unix.Close(fd)
			return nil, err
		}
		in.dirs[int32(wd)] = dir
	}
	// A non-blocking fd goes through the runtime poller, so Close wakes a
	// pending Read.
	in.f = os.NewFile(uintptr(fd), "inotify")
	return in, nil
}

// run reads events until ctx ends (true) or a watched directory goes away
// (false, so the caller falls back to polling).
func (in *inotify) run(ctx context.Context, w *Watcher) bool {
	stop := context.AfterFunc(ctx, func() { in.f.Close() })
	defer stop()
	defer in.f.Close()
	buf := make([]byte, 64*1024)
	for {
		n, err := in.f.Read(buf)
		if err != nil {
			return ctx.Err() != nil
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameStart := off + unix.SizeofInotifyEvent
			nameEnd := nameStart + int(ev.Len)
			if nameEnd > n {
				break
			}
			name := string(bytes.TrimRight(buf[nameStart:nameEnd], "\x00"))
			off = nameEnd
			dir, ok := in.dirs[ev.Wd]
			if !ok {
				continue
			}
			if ev.Mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_IGNORED) != 0 {
				for _, p := range w.paths {
					if filepath.Dir(p) == dir {
						w.mark(p)
					}
				}
				return false
			}
			full := filepath.Join(dir, name)
			for _, p := range w.paths {
				if p == full {
					w.mark(p)
				}
			}
		}
	}
}
