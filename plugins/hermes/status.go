package hermes

import (
	"strings"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// StatusKey is the one UI status derived from a bridge snapshot.
type StatusKey string

const (
	StatusOffline   StatusKey = "offline"
	StatusIdle      StatusKey = "idle"
	StatusBusy      StatusKey = "busy"
	StatusAttention StatusKey = "attention"
	StatusDegraded  StatusKey = "degraded"
	StatusError     StatusKey = "error"
	StatusUnknown   StatusKey = "unknown"
)

// AllStatusKeys lists every status, in the design's table order.
var AllStatusKeys = []StatusKey{
	StatusOffline, StatusIdle, StatusBusy, StatusAttention, StatusDegraded, StatusError, StatusUnknown,
}

// Status is how one status looks: Text is Noctalia's English string,
// BarText the bar label (empty for idle and unknown, "!" for attention).
type Status struct {
	Key     StatusKey
	Text    string
	BarText string
	Icon    string
	Tone    v1.Tone
}

var statuses = map[StatusKey]Status{
	StatusOffline:   {StatusOffline, "Offline", "Offline", "power_settings_new", v1.ToneError},
	StatusIdle:      {StatusIdle, "Online", "", "check", v1.ToneAccent},
	StatusBusy:      {StatusBusy, "Working", "Working", "refresh", v1.ToneAccent},
	StatusAttention: {StatusAttention, "Needs You", "!", "notifications-active", v1.ToneWarning},
	StatusDegraded:  {StatusDegraded, "Degraded", "Degraded", "gpp_maybe", v1.ToneWarning},
	StatusError:     {StatusError, "Error", "Error", "cancel", v1.ToneError},
	StatusUnknown:   {StatusUnknown, "Unknown", "", "ai-usage", v1.ToneNormal},
}

// StatusFor is the presentation of key; an unknown key reads as unknown.
func StatusFor(key StatusKey) Status {
	if st, ok := statuses[key]; ok {
		return st
	}
	return statuses[StatusUnknown]
}

// DeriveStatus applies the design's nine steps, in v5's order with the
// GitHub main fallback:
//
//  1. bridge.status "error" (set by the plugin on auth or protocol failure) → error
//  2. bridge.status not "online" → offline
//  3. approval.pending or hermes.status "attention" → attention
//  4. hermes.status "busy" or session.running → busy
//  5. hermes.status "error" → error
//  6. hermes.status "idle" → idle
//  7. hermes.status "degraded" → degraded
//  8. Hermes silent and summary.gateway.status "running" → idle
//  9. otherwise → unknown
func DeriveStatus(s State) Status {
	bridge := norm(s.Bridge.Status)
	hermes := norm(s.Hermes.Status)
	switch {
	case bridge == "error":
		return StatusFor(StatusError)
	case bridge != "online":
		return StatusFor(StatusOffline)
	case s.Approval.Pending || hermes == "attention":
		return StatusFor(StatusAttention)
	case hermes == "busy" || s.Session.Running:
		return StatusFor(StatusBusy)
	case hermes == "error":
		return StatusFor(StatusError)
	case hermes == "idle":
		return StatusFor(StatusIdle)
	case hermes == "degraded":
		return StatusFor(StatusDegraded)
	case (hermes == "" || hermes == "unknown") && norm(s.Summary.Gateway.Status) == "running":
		return StatusFor(StatusIdle)
	default:
		return StatusFor(StatusUnknown)
	}
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
