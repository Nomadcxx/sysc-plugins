// Package store holds the plugin's own JSON blobs for host state: user prefs
// and the session log the sparkline reads. Schema-free on purpose — these
// values are only ever read back by this same binary.
package store

import (
	"encoding/json"
	"time"
)

const maxSessionsPerGame = 30

type Prefs struct {
	Sort      string          `json:"sort"`      // name|playtime|recent
	View      string          `json:"view"`      // library|favorites|playing|hidden
	Favorites map[string]bool `json:"favorites"`
	Hidden    map[string]bool `json:"hidden"`
}

func (p *Prefs) UnmarshalJSON(data []byte) error {
	type alias Prefs
	a := alias{Favorites: map[string]bool{}, Hidden: map[string]bool{}}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*p = Prefs(a)
	if p.Favorites == nil {
		p.Favorites = map[string]bool{}
	}
	if p.Hidden == nil {
		p.Hidden = map[string]bool{}
	}
	return nil
}

type Session struct {
	Start time.Time `json:"s"`
	End   time.Time `json:"e,omitzero"` // zero while the game is still live
}

type Log map[string][]Session

func (l *Log) Start(id string, at time.Time) {
	sessions := (*l)[id]
	if len(sessions) > 0 && sessions[len(sessions)-1].End.IsZero() {
		return // already open
	}
	*l = setSessions(*l, id, append(sessions, Session{Start: at}))
}

func (l *Log) End(id string, at time.Time) {
	sessions := (*l)[id]
	if len(sessions) == 0 {
		return
	}
	last := &sessions[len(sessions)-1]
	if !last.End.IsZero() {
		return
	}
	last.End = at
	*l = setSessions(*l, id, sessions)
}

func setSessions(l Log, id string, sessions []Session) Log {
	if len(sessions) > maxSessionsPerGame {
		sessions = sessions[len(sessions)-maxSessionsPerGame:]
	}
	if l == nil {
		l = Log{}
	}
	l[id] = sessions
	return l
}

// DailyMinutes buckets the log into `days` calendar-day totals ending at now
// (last bucket = today), oldest first — one bar per day for KindGraph.
func DailyMinutes(l Log, days int, now time.Time) []float64 {
	out := make([]float64, days)
	dayStart := startOfDay(now)
	for _, sessions := range l {
		for _, s := range sessions {
			end := s.End
			if end.IsZero() {
				end = now
			}
			for day := range days {
				from := dayStart.AddDate(0, 0, -(days - 1 - day))
				to := from.AddDate(0, 0, 1)
				lo, hi := s.Start, end
				if lo.Before(from) {
					lo = from
				}
				if hi.After(to) {
					hi = to
				}
				if hi.After(lo) {
					out[day] += hi.Sub(lo).Minutes()
				}
			}
		}
	}
	return out
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
