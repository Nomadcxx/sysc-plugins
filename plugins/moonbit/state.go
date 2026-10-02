package moonbit

import (
	"fmt"
	"strings"
)

// Phase mirrors the moonbit TUI screens: welcome(idle), scan progress,
// results/select(review), confirm, clean progress, complete.
type Phase int

const (
	PhaseIdle Phase = iota
	PhaseScanning
	PhaseReview
	PhaseConfirm
	PhaseCleaning
	PhaseDone
	PhaseError
)

// State is the plugin's whole view model: the last status reading plus the
// folded stream of the operation currently on the wire. Only the event loop
// writes it.
type State struct {
	Phase Phase
	Err   string

	// Status is the daemon's last status event; nil until the first reply.
	Status *Event

	// Scan progress.
	ScanCat   string
	ScanDir   string
	ScanIdx   int
	ScanTotal int
	ScanFiles int
	ScanBytes uint64
	ScanCats  []CategoryStat
	ScanErrs  []string
	// ScanSkipped names categories the daemon cannot clean, such as home
	// caches its unit mounts read-only.
	ScanSkipped []string
	// ScannedAt stamps the scan under review, sent back with the clean.
	ScannedAt string

	// Review selection over the finished scan's categories.
	Review   []CategoryStat
	Selected map[string]bool

	// Clean progress.
	CleanTotal int
	CleanDone  int
	CleanFreed uint64
	CleanFile  string
	CleanDry   bool

	// Completion summary.
	Deleted     int
	Freed       uint64
	CleanErrs   []string
	LastFreed   uint64
	LastDeleted int
}

// Fold applies one daemon event. It reports whether anything changed, so the
// loop can skip publishing identical revisions during a throttled stream.
func (s *State) Fold(ev Event) bool {
	switch ev.T {
	case "status":
		s.Status = &ev
		return true
	case "category":
		s.Phase = PhaseScanning
		s.ScanCat, s.ScanIdx, s.ScanTotal = ev.Name, ev.I, ev.Total
		return true
	case "scan":
		s.ScanFiles, s.ScanBytes, s.ScanDir = ev.Files, ev.Bytes, ev.Dir
		return true
	case "category_done":
		s.ScanCats = append(s.ScanCats, CategoryStat{Name: ev.Name, Files: ev.Files, Bytes: ev.Bytes, Truncate: ev.Truncate})
		return true
	case "category_error":
		s.ScanErrs = append(s.ScanErrs, ev.Name+": "+ev.Msg)
		return true
	case "category_skipped":
		s.ScanSkipped = append(s.ScanSkipped, ev.Name)
		return true
	case "done":
		s.ScannedAt = ev.ScannedAt
		s.Review = nil
		for _, c := range s.ScanCats {
			if c.Files > 0 {
				s.Review = append(s.Review, c)
			}
		}
		s.Selected = map[string]bool{}
		for _, c := range s.Review {
			s.Selected[c.Name] = c.Files > 0
		}
		s.Phase = PhaseReview
		return true
	case "clean_begin":
		s.Phase = PhaseCleaning
		s.CleanTotal, s.CleanDone, s.CleanFreed, s.CleanFile = ev.Files, 0, 0, ""
		s.CleanDry = ev.DryRun
		return true
	case "clean":
		s.CleanDone, s.CleanFreed, s.CleanFile = ev.Done, ev.Freed, ev.File
		return true
	case "clean_done":
		s.Deleted, s.Freed, s.CleanErrs = ev.Deleted, ev.Freed, ev.Errors
		s.LastFreed, s.LastDeleted = ev.Freed, ev.Deleted
		s.Phase = PhaseDone
		return true
	case "cancelled":
		s.Phase, s.Err = PhaseIdle, ""
		return true
	case "error":
		s.Err = daemonMessage(ev.Msg)
		s.Phase = PhaseError
		return true
	}
	return false
}

// StartScan marks the intent so the view flips before the first event lands.
func (s *State) StartScan() {
	s.Phase, s.Err = PhaseScanning, ""
	s.ScanCat, s.ScanIdx, s.ScanTotal = "", 0, 0
	s.ScanFiles, s.ScanBytes, s.ScanDir = 0, 0, ""
	s.ScanCats, s.ScanErrs, s.ScanSkipped, s.ScannedAt = nil, nil, nil, ""
}

// StartClean is the confirm-side flip; clean_begin later refines the totals.
func (s *State) StartClean() {
	s.Phase, s.Err = PhaseCleaning, ""
	s.CleanTotal, s.CleanDone, s.CleanFreed, s.CleanFile = 0, 0, 0, ""
}

// StreamEnded recovers an active operation whose stream closed without a
// terminal event. The error phase renders the idle actions so the user can
// start again after a daemon disconnect.
func (s *State) StreamEnded() bool {
	if s.Phase != PhaseScanning && s.Phase != PhaseCleaning {
		return false
	}
	s.Phase, s.Err = PhaseError, "operation stream ended unexpectedly"
	s.ScanCat, s.ScanDir = "", ""
	s.ScanIdx, s.ScanTotal, s.ScanFiles, s.ScanBytes = 0, 0, 0, 0
	s.ScanCats, s.ScanErrs, s.ScanSkipped = nil, nil, nil
	s.CleanTotal, s.CleanDone, s.CleanFreed, s.CleanFile = 0, 0, 0, ""
	s.CleanDry = false
	return true
}

// UnreachableMessage is what the panel says when the daemon socket cannot be
// dialled: what to do about it, not the raw dial error.
const UnreachableMessage = "Moonbit's daemon isn't reachable. Install moonbit 1.6 or newer and enable moonbit-daemon.service."

// daemonMessage turns the daemon's error strings into panel copy.
func daemonMessage(msg string) string {
	switch {
	case strings.Contains(msg, "another operation in progress"):
		return "Moonbit is busy with a scheduled scan or clean. Try again in a moment."
	case strings.Contains(msg, "replaced by a newer"):
		return "A scheduled scan replaced the results you reviewed. Scan again before cleaning."
	}
	return msg
}

// SelectedStats are the review rows the user kept checked.
func (s *State) SelectedStats() []CategoryStat {
	var out []CategoryStat
	for _, c := range s.Review {
		if s.Selected[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

func (s *State) selectedFiles() int {
	var n int
	for _, c := range s.SelectedStats() {
		n += c.Files
	}
	return n
}

// truncatedFiles counts the selected files cleaned by truncation.
func (s *State) truncatedFiles() int {
	var n int
	for _, c := range s.SelectedStats() {
		if c.Truncate {
			n += c.Files
		}
	}
	return n
}

func (s *State) selectedBytes() uint64 {
	var b uint64
	for _, c := range s.SelectedStats() {
		b += c.Bytes
	}
	return b
}

// ScanValue is the progress fraction: settled categories (done, failed or
// skipped) over total, capped below one so completion only reads as full on
// the done event.
func (s *State) ScanValue() float64 {
	settled := len(s.ScanCats) + len(s.ScanErrs) + len(s.ScanSkipped)
	if s.ScanTotal <= 0 || settled == 0 {
		return 0
	}
	v := float64(settled) / float64(s.ScanTotal)
	if v > 0.99 {
		v = 0.99
	}
	return v
}

func (s *State) CleanValue() float64 {
	if s.CleanTotal <= 0 {
		return 0
	}
	v := float64(s.CleanDone) / float64(s.CleanTotal)
	if v > 0.99 {
		v = 0.99
	}
	return v
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
