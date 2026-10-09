package notes

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/notes/screenshot.png when CAPTURE=1: a
// library of invented notes with one pinned, and the packing list open.
func TestCapturePanel(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	note := func(name, title, preview string, ago time.Duration, favorite bool, words int) Summary {
		return Summary{Name: name, Title: title, Preview: preview, Modified: now.Add(-ago), Favorite: favorite, Words: words}
	}
	body := "# Packing list\n\n- Passport and tickets\n- Phone charger\n- Rain jacket\n- Notebook\n\nLeave for the airport by 07:30."
	capture.Panel(t, "notes", PanelTree(Snapshot{
		Notes: []Summary{
			note("shopping", "Shopping list", "Oat milk, coffee beans, tomatoes", 90*time.Minute, true, 18),
			note("packing", "Packing list", "Passport and tickets, phone charger", 20*time.Minute, false, 22),
			note("book", "Book ideas", "A field guide to local birds", 26*time.Hour, false, 140),
			note("recipes", "Weeknight recipes", "Lentil soup with lemon and herbs", 3*24*time.Hour, false, 260),
			note("trip", "Trip itinerary", "Day 1: arrive, walk the old town", 5*24*time.Hour, false, 310),
			note("meeting", "Meeting notes", "Agree the release date and owners", 8*24*time.Hour, false, 95),
		},
		Selected: "packing", Title: "Packing list", Body: body, Words: 22,
		Modified: now.Add(-20 * time.Minute), Now: now,
	}, false))
}
