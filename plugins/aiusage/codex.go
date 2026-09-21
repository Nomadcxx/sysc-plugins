package aiusage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// snapshotCollector reads the session logs the codex CLI writes. Quota
// snapshots ride token_count events as rate_limits objects; the newest
// snapshot by event timestamp across every file is the report. This is a
// local-only collector: it reads session files, never authentication files,
// and never touches the network.
type snapshotCollector struct{ env Env }

// NewSnapshot builds the session-file snapshot collector.
func NewSnapshot(env Env) Collector { return &snapshotCollector{env: env} }

func (c *snapshotCollector) ID() string { return "codex" }

// rawWindow is a rate_limits entry on the wire. used_percent is used, not
// remaining; resets_at is unix seconds.
type rawWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

// snapshotRow is the JSONL record shape the collector consumes.
type snapshotRow struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type       string `json:"type"`
		RateLimits *struct {
			LimitID   string     `json:"limit_id"`
			PlanType  string     `json:"plan_type"`
			Primary   *rawWindow `json:"primary"`
			Secondary *rawWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// valid screens a candidate row: a token_count event carrying a codex quota
// snapshot with a parseable timestamp. A premium limit is a different quota
// and never wins; a truncated final line simply fails to parse.
func (r snapshotRow) valid() (time.Time, bool) {
	if r.Payload.Type != "token_count" || r.Payload.RateLimits == nil {
		return time.Time{}, false
	}
	rl := r.Payload.RateLimits
	if rl.LimitID != "" && rl.LimitID != "codex" {
		return time.Time{}, false
	}
	if rl.Primary == nil && rl.Secondary == nil {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, r.Timestamp)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

const tailChunk = 64 << 10

// lastSnapshot scans a session file backwards in chunks and returns the last
// valid quota row. Only the tail of a session file is ever read, so a large
// history costs its tail, not its whole length.
func lastSnapshot(path string) (snapshotRow, time.Time, bool, error) {
	var out snapshotRow
	var outTime time.Time
	f, err := os.Open(path)
	if err != nil {
		return out, outTime, false, err
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return out, outTime, false, err
	}
	var buf []byte
	scanned := 0 // buf[scanned:] holds complete lines already considered
	for pos := size; pos > 0; {
		chunk := int64(tailChunk)
		if pos < chunk {
			chunk = pos
		}
		pos -= chunk
		head := make([]byte, chunk)
		if _, err := f.ReadAt(head, pos); err != nil {
			return out, outTime, false, err
		}
		buf = append(head, buf...)
		scanned += int(chunk)

		end := scanned
		for end > 0 {
			start := bytes.LastIndexByte(buf[:end], '\n') + 1
			line := buf[start:end]
			if len(bytes.TrimSpace(line)) > 0 && bytes.Contains(line, []byte(`"rate_limits"`)) {
				var row snapshotRow
				if err := json.Unmarshal(line, &row); err == nil {
					if ts, ok := row.valid(); ok {
						return row, ts, true, nil
					}
				}
			}
			if start == 0 {
				// The leading fragment is incomplete unless the buffer now
				// reaches the file's first byte; either way the scan of the
				// buffer stops here.
				if pos == 0 && len(bytes.TrimSpace(buf[:end])) > 0 && bytes.Contains(buf[:end], []byte(`"rate_limits"`)) {
					var row snapshotRow
					if err := json.Unmarshal(buf[:end], &row); err == nil {
						if ts, ok := row.valid(); ok {
							return row, ts, true, nil
						}
					}
				}
				break
			}
			end = start - 1
		}
		scanned = end
	}
	return out, outTime, false, nil
}

// sessionFiles lists the session logs newest-mtime first.
func sessionFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtrees are skipped, not fatal
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		fi, fiErr := os.Stat(files[i])
		fj, fjErr := os.Stat(files[j])
		switch {
		case fiErr != nil || fjErr != nil:
			return false
		default:
			return fj.ModTime().After(fi.ModTime())
		}
	})
	return files, nil
}

func (c *snapshotCollector) sessionRoot() string {
	if dir := c.env.getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	return filepath.Join(c.env.home(), ".codex", "sessions")
}

// Fetch returns the newest snapshot as the provider report. No compatible
// snapshot anywhere is StateNoData, not an error: an idle codex install is a
// truth, not a fault.
func (c *snapshotCollector) Fetch(ctx context.Context) (ProviderReport, error) {
	if err := ctx.Err(); err != nil {
		return ProviderReport{}, err
	}
	rep := ProviderReport{ID: "codex", Name: "Codex", UpdatedAt: c.env.now(), State: StateNoData}
	files, err := sessionFiles(c.sessionRoot())
	if err != nil || len(files) == 0 {
		return rep, nil
	}
	var best snapshotRow
	var bestTime time.Time
	found := false
	for _, path := range files {
		if info, statErr := os.Stat(path); statErr == nil && found && info.ModTime().Before(bestTime) {
			continue // this file cannot hold a newer event than the best one
		}
		row, ts, ok, err := lastSnapshot(path)
		if err != nil || !ok {
			continue
		}
		if !found || ts.After(bestTime) {
			best, bestTime, found = row, ts, true
		}
	}
	if !found {
		return rep, nil
	}
	rep.Windows = normalizeSnapshotWindows(best)
	if plan := strings.TrimSpace(best.Payload.RateLimits.PlanType); plan != "" {
		rep.Plan = planTitle(plan)
	}
	rep.State = StateFresh
	rep.UpdatedAt = bestTime
	return rep, nil
}

// normalizeSnapshotWindows turns a snapshot's primary/secondary entries into
// the display model. The window length names the window, never slot position.
func normalizeSnapshotWindows(row snapshotRow) []Window {
	rl := row.Payload.RateLimits
	var out []Window
	for _, slot := range [2]struct {
		key string
		raw *rawWindow
	}{{"primary", rl.Primary}, {"secondary", rl.Secondary}} {
		if slot.raw == nil {
			continue
		}
		w := Window{Key: slot.key, HasPercent: true, WindowMinutes: slot.raw.WindowMinutes}
		w.UsedPercent = math.Max(0, math.Min(100, slot.raw.UsedPercent))
		w.Label, w.ShortLabel = LabelForMinutes(slot.raw.WindowMinutes)
		if w.Label == "" {
			w.Label = strings.ToUpper(slot.key[:1]) + slot.key[1:]
		}
		if slot.raw.ResetsAt > 0 {
			w.ResetsAt = time.Unix(slot.raw.ResetsAt, 0).UTC()
		}
		out = append(out, w)
	}
	return out
}

// planTitle titles a plan word for the chip: "plus" becomes "Plus".
func planTitle(plan string) string {
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + plan[1:]
}
