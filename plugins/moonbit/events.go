package moonbit

import "encoding/json"

// Event is one NDJSON line from `moonbit panel`. The union is flat: every
// moonbit event kind decodes into it, and the fold switches on T. Categories
// is raw because moonbit reuses the key for a count (done) and a rollup list
// (clean_begin).
type Event struct {
	T          string          `json:"t"`
	Name       string          `json:"name,omitempty"`
	I          int             `json:"i,omitempty"`
	Total      int             `json:"total,omitempty"`
	Files      int             `json:"files,omitempty"`
	Bytes      uint64          `json:"bytes,omitempty"`
	Dir        string          `json:"dir,omitempty"`
	Duration   int64           `json:"duration_ms,omitempty"`
	Msg        string          `json:"msg,omitempty"`
	DryRun     bool            `json:"dry_run,omitempty"`
	Done       int             `json:"done,omitempty"`
	Deleted    int             `json:"deleted,omitempty"`
	Freed      uint64          `json:"freed,omitempty"`
	File       string          `json:"file,omitempty"`
	Errors     []string        `json:"errors,omitempty"`
	Categories json.RawMessage `json:"categories,omitempty"`
	// ScannedAt stamps a scan's done event (moonbit 1.6+); a clean sends it
	// back so the daemon refuses a cache a later scan replaced.
	ScannedAt string `json:"scanned_at,omitempty"`
	// Truncate marks a category_done whose files are truncated in place
	// rather than deleted (moonbit 1.6+).
	Truncate bool `json:"truncate,omitempty"`
	// Reclaimed is Docker's own summary of what a cleanup freed.
	Reclaimed string `json:"reclaimed,omitempty"`
	// Target and Action echo a schedule change.
	Target string `json:"target,omitempty"`
	Action string `json:"action,omitempty"`
}

// CategoryStat is moonbit's per-category rollup.
type CategoryStat struct {
	Name     string `json:"name"`
	Files    int    `json:"files"`
	Bytes    uint64 `json:"bytes"`
	Truncate bool   `json:"truncate,omitempty"`
}

// CacheInfo is the last scan, rolled up per category.
type CacheInfo struct {
	Files      int            `json:"files"`
	Bytes      uint64         `json:"bytes"`
	ScannedAt  string         `json:"scanned_at"`
	Categories []CategoryStat `json:"categories,omitempty"`
}
