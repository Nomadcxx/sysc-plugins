package store

import (
	"testing"
	"time"
)

func TestLogEndDangling(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	dayAgo := now.Add(-24 * time.Hour)
	closedEnd := now.Add(-48 * time.Hour)

	tests := []struct {
		name  string
		log   Log
		alive map[string]bool
		want  Log
		ids   []string
	}{
		{
			name:  "open session for dead game closes at now",
			log:   Log{"g1": {{Start: dayAgo}}},
			alive: map[string]bool{},
			want:  Log{"g1": {{Start: dayAgo, End: now}}},
			ids:   []string{"g1"},
		},
		{
			name:  "open session for live game stays open",
			log:   Log{"g1": {{Start: dayAgo}}},
			alive: map[string]bool{"g1": true},
			want:  Log{"g1": {{Start: dayAgo}}},
		},
		{
			name:  "already closed session untouched",
			log:   Log{"g1": {{Start: closedEnd.Add(-time.Hour), End: closedEnd}}},
			alive: map[string]bool{},
			want:  Log{"g1": {{Start: closedEnd.Add(-time.Hour), End: closedEnd}}},
		},
		{
			name:  "mixed live and dead",
			log:   Log{"live": {{Start: dayAgo}}, "dead": {{Start: dayAgo}}},
			alive: map[string]bool{"live": true},
			want:  Log{"live": {{Start: dayAgo}}, "dead": {{Start: dayAgo, End: now}}},
			ids:   []string{"dead"},
		},
		{
			name: "nil log is safe",
			log:  nil,
			ids:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.log.EndDangling(tc.alive, now)
			if len(got) != len(tc.ids) {
				t.Fatalf("closed ids = %v, want %v", got, tc.ids)
			}
			for i := range got {
				if got[i] != tc.ids[i] {
					t.Fatalf("closed ids = %v, want %v", got, tc.ids)
				}
			}
			if len(tc.want) > 0 {
				for id, sessions := range tc.want {
					if len(tc.log[id]) != len(sessions) {
						t.Fatalf("sessions for %s = %+v, want %+v", id, tc.log[id], sessions)
					}
					for i, s := range sessions {
						if tc.log[id][i] != s {
							t.Fatalf("session %d of %s = %+v, want %+v", i, id, tc.log[id][i], s)
						}
					}
				}
			}
		})
	}
}
