package worldclock

import (
	"testing"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/world-clock/screenshot.png when CAPTURE=1:
// five well-known cities, with the clock reading 12:04 in UTC.
func TestCapturePanel(t *testing.T) {
	capture.Panel(t, "world-clock", Panel(PanelState{Readings: []Reading{
		{Zone: "America/New_York", Label: "New York", Clock: "08:04", Offset: "UTC-4", Relative: "−4h", Daytime: true},
		{Zone: "Europe/London", Label: "London", Clock: "13:04", Offset: "UTC+1", Relative: "+1h", Daytime: true, OnBar: true},
		{Zone: "Asia/Tokyo", Label: "Tokyo", Clock: "21:04", Offset: "UTC+9", Relative: "+9h", Daytime: false, OnBar: true},
		{Zone: "Australia/Sydney", Label: "Sydney", Clock: "22:04", Offset: "UTC+10", Relative: "+10h", Daytime: false},
		{Zone: "Pacific/Auckland", Label: "Auckland", Clock: "00:04", Offset: "UTC+12", Relative: "+12h", DayShift: 1, Daytime: false},
	}}))
}
