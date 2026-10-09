package main

import (
	"image/color"
	"strconv"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/panel"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
	"github.com/Nomadcxx/sysc-plugins/plugins/games/store"
)

// TestCapturePanel writes plugins/games/screenshot.png when CAPTURE=1: a
// library of invented games with generated cover art, one running and two
// favourited.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	hues := []color.NRGBA{
		{R: 220, G: 90, B: 60, A: 255}, {R: 60, G: 150, B: 210, A: 255}, {R: 110, G: 190, B: 100, A: 255},
		{R: 180, G: 90, B: 190, A: 255}, {R: 230, G: 180, B: 60, A: 255}, {R: 70, G: 200, B: 190, A: 255},
		{R: 200, G: 90, B: 130, A: 255}, {R: 110, G: 120, B: 230, A: 255},
	}
	names := []string{"Starfall Drift", "Hollow Lantern", "Ember Tide", "Quiet Orchard", "Iron Meridian", "Paper Skies", "Tidepool Kings", "Lunar Freight"}
	var games []source.Game
	for i, name := range names {
		games = append(games, source.Game{
			ID: strconv.Itoa(i + 1), Name: name, Slug: "game-" + strconv.Itoa(i+1),
			Runner: "wine", Platform: "Linux", Year: strconv.Itoa(2016 + i), Installed: true,
			PlaytimeSec: float64((i + 1) * 5400), LastPlayed: now.Add(-time.Duration(i+1) * 26 * time.Hour),
			CoverPath: capture.Gradient(t, 180, 240, hues[i], color.NRGBA{R: 20, G: 20, B: 40, A: 255}),
			Source:    "lutris",
		})
	}
	prefs := store.Prefs{Sort: "recent", View: "library", Favorites: map[string]bool{"1": true, "3": true}, Hidden: map[string]bool{}}
	capture.Panel(t, "games", panel.BuildTree(panel.State{
		Now: now, All: games, Prefs: prefs, Selected: "1",
		Running: map[string]time.Time{"1": now.Add(-47 * time.Minute)},
	}))
}
