package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPrefsRoundTrip(t *testing.T) {
	p := Prefs{Sort: "playtime", View: "favorites",
		Favorites: map[string]bool{"7": true}, Hidden: map[string]bool{"3": true}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Prefs
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Sort != "playtime" || !got.Favorites["7"] || !got.Hidden["3"] {
		t.Fatalf("round trip: %+v", got)
	}
	var empty Prefs
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Favorites == nil || empty.Hidden == nil {
		t.Fatalf("maps must be non-nil: %+v", empty)
	}
}

func TestSessionLog(t *testing.T) {
	now := time.Now()
	var log Log
	log.Start("7", now.Add(-2*time.Hour))
	if len(log["7"]) != 1 || !log["7"][0].End.IsZero() {
		t.Fatalf("open session: %+v", log["7"])
	}
	log.Start("7", now.Add(-time.Hour)) // duplicate open is ignored
	if len(log["7"]) != 1 {
		t.Fatalf("double open: %+v", log["7"])
	}
	log.End("7", now.Add(-30*time.Minute))
	if len(log["7"]) != 1 || log["7"][0].End.IsZero() {
		t.Fatalf("close: %+v", log["7"])
	}
	log.End("9", now) // closing an unknown game is a no-op
	if len(log["9"]) != 0 {
		t.Fatal("phantom session")
	}
}

func TestLogIsCapped(t *testing.T) {
	var log Log
	base := time.Now()
	for i := range 40 {
		log.Start("7", base.Add(time.Duration(i)*time.Hour))
		log.End("7", base.Add(time.Duration(i)*time.Hour+time.Minute))
	}
	if len(log["7"]) != maxSessionsPerGame {
		t.Fatalf("cap: %d", len(log["7"]))
	}
	if log["7"][len(log["7"])-1].Start.Before(base.Add(39 * time.Hour)) {
		t.Fatal("newest session must survive the cap")
	}
}

func TestDailyMinutes(t *testing.T) {
	fixed := func(day, hour, min int) time.Time {
		return time.Date(2026, time.September, day, hour, min, 0, 0, time.Local)
	}
	log := Log{"7": {
		{Start: fixed(25, 10, 0), End: fixed(25, 11, 30)}, // 90
		{Start: fixed(26, 23, 0), End: fixed(27, 0, 30)},  // crosses midnight
	}}
	now := fixed(27, 12, 0)
	days := DailyMinutes(log, 3, now)
	if len(days) != 3 {
		t.Fatalf("want 3 buckets, got %v", days)
	}
	if days[0] != 90 || days[1] != 60 || days[2] != 30 {
		t.Fatalf("daily minutes: %v", days)
	}
}
