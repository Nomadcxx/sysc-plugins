package hermes

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

var sessionNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func tsAgo(d time.Duration) float64 {
	return float64(sessionNow.Add(-d).Unix())
}

func tenSessions() []Session {
	out := []Session{
		{ID: "20261111_115900_abcd", Title: "Tidy the release notes", StartedAt: tsAgo(30 * time.Second)},
		{ID: "20261111_110000_ef01", Preview: "Why is the build red?", StartedAt: tsAgo(59 * time.Minute)},
		{ID: "0123456789abcdef", StartedAt: tsAgo(5 * time.Hour)},
		{ID: strings.Repeat("x", 201), Title: "Long id", StartedAt: tsAgo(49 * time.Hour)},
		{ID: "has space", Title: "Odd id", StartedAt: 0},
	}
	for i := len(out); i < 12; i++ {
		out = append(out, Session{ID: "sess-" + string(rune('a'+i)), Title: "Session " + string(rune('A'+i)), StartedAt: tsAgo(time.Duration(i) * time.Hour)})
	}
	return out
}

func TestRelativeTimeFollowsDev(t *testing.T) {
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "now"}, {59 * time.Second, "now"}, {time.Minute, "1m"}, {59 * time.Minute, "59m"},
		{time.Hour, "1h"}, {23 * time.Hour, "23h"}, {24 * time.Hour, "1d"}, {75 * time.Hour, "3d"},
	} {
		if got := RelativeTime(tsAgo(c.ago), sessionNow); got != c.want {
			t.Errorf("RelativeTime(%v ago) = %q, want %q", c.ago, got, c.want)
		}
	}
	if got := RelativeTime(0, sessionNow); got != "" {
		t.Errorf("RelativeTime(0) = %q, want empty", got)
	}
}

func TestSessionLabelFallsBackLikeDev(t *testing.T) {
	cases := map[Session]string{
		{ID: "abc", Title: "T", Preview: "P"}: "T",
		{ID: "abc", Preview: "P"}:             "P",
		{ID: "0123456789abcdef"}:              "01234567",
		{ID: "abc"}:                           "abc",
		{}:                                    "Session",
	}
	for s, want := range cases {
		if got := sessionLabel(s); got != want {
			t.Errorf("sessionLabel(%+v) = %q, want %q", s, got, want)
		}
	}
}

func TestSessionNodeIDsUseIDsNotIndexes(t *testing.T) {
	hashed := regexp.MustCompile(`^session:[0-9a-f]{12}$`)
	if got := SessionNodeID("20261111_115900_abcd"); got != "session:20261111_115900_abcd" {
		t.Errorf("plain id = %q", got)
	}
	for _, id := range []string{strings.Repeat("x", 201), "has space", "tab\tid", ""} {
		if got := SessionNodeID(id); !hashed.MatchString(got) {
			t.Errorf("SessionNodeID(%q) = %q, want session:<sha256-12>", id, got)
		}
	}
	if SessionNodeID("has space") == SessionNodeID("has  space") {
		t.Error("distinct ids must not share a node id")
	}
	sessions := tenSessions()
	snap := SessionsSnapshot{Loaded: true, Sessions: sessions}
	for _, s := range sessions[:10] {
		req, ok := ResumeFor(SessionNodeID(s.ID), snap)
		if !ok || req.SessionID != s.ID {
			t.Errorf("ResumeFor(%q) = %+v %v, want %q", SessionNodeID(s.ID), req, ok, s.ID)
		}
	}
	// Reordering the list must not change which session a node resumes.
	reversed := SessionsSnapshot{Loaded: true}
	for i := len(sessions) - 1; i >= 0; i-- {
		reversed.Sessions = append(reversed.Sessions, sessions[i])
	}
	if req, ok := ResumeFor(SessionNodeID(sessions[3].ID), reversed); !ok || req.SessionID != sessions[3].ID {
		t.Errorf("resume after reorder = %+v %v", req, ok)
	}
	if _, ok := ResumeFor("session:1", snap); ok {
		t.Error("an index-shaped id must not resolve")
	}
	raw, _ := json.Marshal(ResumeRequest{SessionID: "abc"})
	if string(raw) != `{"session_id":"abc"}` {
		t.Errorf("resume body = %s", raw)
	}
}

func TestSessionsSectionStates(t *testing.T) {
	unsupported := SessionsSection(SessionsSnapshot{Loaded: true, Unsupported: true}, sessionNow, 612, 300)
	if !strings.Contains(strings.Join(texts(unsupported), "|"), "This bridge does not expose session history") {
		t.Errorf("404 state = %v", texts(unsupported))
	}
	empty := SessionsSection(SessionsSnapshot{Loaded: true}, sessionNow, 612, 300)
	if !strings.Contains(strings.Join(texts(empty), "|"), "No recent sessions") {
		t.Errorf("empty state = %v", texts(empty))
	}
	full := SessionsSection(SessionsSnapshot{Loaded: true, Sessions: tenSessions()}, sessionNow, 612, 300)
	var rows []string
	walk(full, func(n *v1.Node) {
		if strings.HasPrefix(n.ID, "session:") {
			rows = append(rows, n.ID)
		}
	})
	if len(rows) != 10 {
		t.Fatalf("sessions section shows %d rows, want 10: %v", len(rows), rows)
	}
	for i, s := range tenSessions()[:10] {
		if rows[i] != SessionNodeID(s.ID) {
			t.Errorf("row %d id = %q, want %q", i, rows[i], SessionNodeID(s.ID))
		}
	}
	joined := strings.Join(texts(full), "|")
	for _, want := range []string{"1. Tidy the release notes", "now", "2. Why is the build red?", "59m", "3. 01234567", "5h", "2d"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sessions section is missing %q in %q", want, joined)
		}
	}
	if findID(full, "sessions-refresh") == nil {
		t.Error("sessions section has no refresh button")
	}
	lintView(t, "sessions section", &v1.Node{Kind: v1.KindColumn, Width: 640, Height: 520, Padding: 14, Children: []*v1.Node{full}}, v1.ViewPanel, 640, 520)
}
