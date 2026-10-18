package store

import (
	"testing"
	"time"
)

// State saved before the deck listed several libraries keyed games by bare
// Lutris ids. Those keys must move to the namespaced form exactly once, so
// favorites and session history survive the upgrade.
func TestMigrateLegacyKeys(t *testing.T) {
	p := Prefs{
		Favorites: map[string]bool{"7": true, "lutris:9": true, "faugus:abc": false},
		Hidden:    map[string]bool{"7": true},
	}
	if !p.MigrateLegacyKeys("lutris") {
		t.Fatal("bare keys must report a change")
	}
	if !p.Favorites["lutris:7"] || p.Favorites["7"] || !p.Favorites["lutris:9"] || p.Favorites["faugus:abc"] != false {
		t.Fatalf("favorites after migration = %v", p.Favorites)
	}
	if !p.Hidden["lutris:7"] || p.Hidden["7"] {
		t.Fatalf("hidden after migration = %v", p.Hidden)
	}
	if p.MigrateLegacyKeys("lutris") {
		t.Fatal("migration must be idempotent")
	}
}

func TestMigrateLegacySessionKeys(t *testing.T) {
	now := time.Now()
	l := Log{
		"7":           {{Start: now.Add(-time.Hour), End: now}},
		"lutris:9":    {{Start: now.Add(-2 * time.Hour), End: now}},
		"steam:730":   {{Start: now.Add(-3 * time.Hour)}},
		"heroic:Some": {{Start: now.Add(-4 * time.Hour)}},
	}
	if !l.MigrateLegacyKeys("lutris") {
		t.Fatal("bare keys must report a change")
	}
	if len(l["lutris:7"]) != 1 || len(l["7"]) != 0 {
		t.Fatalf("log after migration = %+v", l)
	}
	if len(l["lutris:9"]) != 1 || len(l["steam:730"]) != 1 || len(l["heroic:Some"]) != 1 {
		t.Fatalf("namespaced keys were touched: %+v", l)
	}
	if l.MigrateLegacyKeys("lutris") {
		t.Fatal("migration must be idempotent")
	}
}
