package aiusage

import "strings"

// ErrSetup reports a missing credential. It is a distinct type, never a
// message to sniff: the taxonomy is structured, and every path the collector
// tried is carried so the setup card can name them.
type ErrSetup struct{ Tried []string }

func (e *ErrSetup) Error() string {
	return "setup required: " + strings.Join(e.Tried, "; ")
}
