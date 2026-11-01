package airquality

import (
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/capture"
)

// TestCapturePanel writes plugins/airquality/screenshot.png with regional
// pollen, a thunderstorm alert, air quality and UV readings.
func TestCapturePanel(t *testing.T) {
	report := sampleReport()
	report.UpdatedAt = time.Now().Add(-6 * time.Minute)
	report.Snapshot.Alert.Text = "Elevated risk: follow your asthma action plan."
	for i := range report.Snapshot.Measures {
		if report.Snapshot.Measures[i].Name == MeasureGrass {
			// AirHealth supplies grass as a category, not a grain count.
			report.Snapshot.Measures[i].Metric = nil
			report.Snapshot.Measures[i].Max = 0
			report.Snapshot.Measures[i].Bands = nil
			WireScale.AttachDial(&report.Snapshot.Measures[i])
		}
	}
	report.Snapshot.Measures = append(report.Snapshot.Measures, Measure{
		Name: MeasureUV, Title: Title(MeasureUV), Level: UVLevel(5.8),
		Metric: &Metric{Subcat: "today's peak", Value: 5.8},
		Max:    11, Bands: UVBands(), Source: "open-meteo",
	})
	report.Snapshot.Attribution = joinAttribution(
		joinAttribution(AirHealthAttribution, EPAAttribution), OpenMeteoAttribution,
	)
	capture.Panel(t, "airquality", PanelFor(report, dialBandLabelsProtocolMinor))
}
