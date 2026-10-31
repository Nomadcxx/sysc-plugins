package airquality

import (
	"encoding/json"
	"time"
)

// diskVersion guards the persisted shape. A stored report from an older
// version is discarded rather than migrated: it is a cache, and refetching
// costs one request.
const diskVersion = 1

// diskReport is the persisted form of a report. It is deliberately a separate
// type from the domain structs so that a field rename in the domain does not
// silently change what is on disk.
type diskReport struct {
	Version     int           `json:"version"`
	UpdatedUnix int64         `json:"updated_unix"`
	Location    diskLocation  `json:"location"`
	Date        string        `json:"date"`
	Title       string        `json:"title"`
	Advice      string        `json:"advice"`
	Attribution string        `json:"attribution"`
	Measures    []diskMeasure `json:"measures"`
	Alert       *diskAlert    `json:"alert,omitempty"`
	Forecast    []diskDay     `json:"forecast,omitempty"`
}

type diskLocation struct {
	Suburb   string `json:"suburb"`
	State    string `json:"state"`
	Postcode string `json:"postcode"`
	SAL      string `json:"sal"`
	SA3      string `json:"sa3"`
}

type diskMeasure struct {
	Name      string          `json:"name"`
	Level     string          `json:"level"`
	Index     int             `json:"index"`
	Severity  int             `json:"severity"`
	Subcat    string          `json:"subcat,omitempty"`
	Value     float64         `json:"value,omitempty"`
	Units     string          `json:"units,omitempty"`
	Source    string          `json:"source,omitempty"`
	Max       float64         `json:"max,omitempty"`
	Bands     []diskBand      `json:"bands,omitempty"`
	Breakdown []diskBreakdown `json:"breakdown,omitempty"`
}

type diskBand struct {
	Label    string  `json:"label"`
	Until    float64 `json:"until"`
	Ceiling  float64 `json:"ceiling,omitempty"`
	Severity int     `json:"severity"`
}

type diskBreakdown struct {
	Name     string  `json:"name"`
	Level    string  `json:"level"`
	Index    int     `json:"index"`
	Severity int     `json:"severity"`
	Subcat   string  `json:"subcat,omitempty"`
	Value    float64 `json:"value,omitempty"`
	Units    string  `json:"units,omitempty"`
}

type diskAlert struct {
	Title    string `json:"title"`
	Text     string `json:"text"`
	Level    string `json:"level"`
	Severity int    `json:"severity"`
}

type diskDay struct {
	Date     string        `json:"date"`
	Title    string        `json:"title"`
	MaxC     float64       `json:"max_c"`
	MinC     float64       `json:"min_c"`
	Measures []diskMeasure `json:"measures,omitempty"`
}

// EncodeReport serialises a report for plugin state.
func EncodeReport(report Report) ([]byte, error) {
	if report.UpdatedAt.IsZero() {
		report.UpdatedAt = time.Now()
	}
	disk := diskReport{
		Version:     diskVersion,
		UpdatedUnix: report.UpdatedAt.Unix(),
		Location: diskLocation{
			Suburb: report.Snapshot.Location.Suburb, State: report.Snapshot.Location.State,
			Postcode: report.Snapshot.Location.Postcode,
			SAL:      report.Snapshot.Location.SAL, SA3: report.Snapshot.Location.SA3,
		},
		Date:        report.Snapshot.Date,
		Title:       report.Snapshot.Title,
		Advice:      report.Snapshot.Advice,
		Attribution: report.Snapshot.Attribution,
	}
	for _, m := range report.Snapshot.Measures {
		disk.Measures = append(disk.Measures, toDiskMeasure(m))
	}
	if report.Snapshot.Alert != nil {
		disk.Alert = &diskAlert{
			Title: report.Snapshot.Alert.Title, Text: report.Snapshot.Alert.Text,
			Level: report.Snapshot.Alert.Level.Name, Severity: int(report.Snapshot.Alert.Level.Severity),
		}
	}
	for _, d := range report.Snapshot.Forecast {
		day := diskDay{Date: d.Date, Title: d.Title, MaxC: d.MaxC, MinC: d.MinC}
		for _, name := range MeasureOrder {
			if m, ok := d.Measures[name]; ok {
				day.Measures = append(day.Measures, toDiskMeasure(m))
			}
		}
		disk.Forecast = append(disk.Forecast, day)
	}
	return json.Marshal(disk)
}

func toDiskMeasure(m Measure) diskMeasure {
	out := diskMeasure{
		Name: m.Name, Level: m.Level.Name, Index: m.Level.Index,
		Severity: int(m.Level.Severity), Source: m.Source, Max: m.Max,
	}
	if m.Metric != nil {
		out.Subcat, out.Value, out.Units = m.Metric.Subcat, m.Metric.Value, m.Metric.Units
	}
	for _, band := range m.Bands {
		out.Bands = append(out.Bands, diskBand{
			Label: band.Label, Until: band.Until, Ceiling: band.Ceiling, Severity: int(band.Severity),
		})
	}
	for _, part := range m.Breakdown {
		diskPart := diskBreakdown{
			Name: part.Name, Level: part.Level.Name, Index: part.Level.Index,
			Severity: int(part.Level.Severity),
		}
		if part.Metric != nil {
			diskPart.Subcat, diskPart.Value, diskPart.Units = part.Metric.Subcat, part.Metric.Value, part.Metric.Units
		}
		out.Breakdown = append(out.Breakdown, diskPart)
	}
	return out
}

// DecodeReport restores a persisted report. A report from a newer version, or
// one with no usable measures, is rejected so the caller refetches instead of
// showing a half-decoded cache.
func DecodeReport(raw []byte) (Report, bool) {
	var disk diskReport
	if err := json.Unmarshal(raw, &disk); err != nil {
		return Report{}, false
	}
	if disk.Version != diskVersion || len(disk.Measures) == 0 {
		return Report{}, false
	}
	report := Report{
		UpdatedAt: time.Unix(disk.UpdatedUnix, 0),
		Stale:     true,
		Snapshot: Snapshot{
			Location: Location{
				Suburb: disk.Location.Suburb, State: disk.Location.State,
				Postcode: disk.Location.Postcode, SAL: disk.Location.SAL, SA3: disk.Location.SA3,
			},
			Date:        disk.Date,
			Title:       disk.Title,
			Advice:      disk.Advice,
			Attribution: disk.Attribution,
		},
	}
	for _, m := range disk.Measures {
		report.Snapshot.Measures = append(report.Snapshot.Measures, fromDiskMeasure(m))
	}
	if disk.Alert != nil {
		report.Snapshot.Alert = &Alert{
			Title: disk.Alert.Title, Text: disk.Alert.Text,
			Level: fromDiskLevel(disk.Alert.Level, 0, disk.Alert.Severity),
		}
	}
	for _, d := range disk.Forecast {
		day := Day{Date: d.Date, Title: d.Title, MaxC: d.MaxC, MinC: d.MinC, Measures: map[string]Measure{}}
		for _, m := range d.Measures {
			measure := fromDiskMeasure(m)
			day.Measures[measure.Name] = measure
		}
		report.Snapshot.Forecast = append(report.Snapshot.Forecast, day)
	}
	return report, true
}

func fromDiskMeasure(m diskMeasure) Measure {
	out := Measure{
		Name: m.Name, Title: Title(m.Name), Source: m.Source, Max: m.Max,
		Level: fromDiskLevel(m.Level, m.Index, m.Severity),
	}
	if m.Subcat != "" || m.Units != "" {
		out.Metric = &Metric{Subcat: m.Subcat, Value: m.Value, Units: m.Units}
	}
	for _, band := range m.Bands {
		out.Bands = append(out.Bands, Band{
			Label: band.Label, Until: band.Until, Ceiling: band.Ceiling, Severity: fromDiskSeverity(band.Severity),
		})
	}
	for _, part := range m.Breakdown {
		diskPart := Breakdown{
			Name: part.Name, Level: fromDiskLevel(part.Level, part.Index, part.Severity),
		}
		if part.Subcat != "" || part.Units != "" {
			diskPart.Metric = &Metric{Subcat: part.Subcat, Value: part.Value, Units: part.Units}
		}
		out.Breakdown = append(out.Breakdown, diskPart)
	}
	return out
}

func fromDiskLevel(name string, index, severity int) Level {
	return Level{Name: name, Index: index, Severity: fromDiskSeverity(severity)}
}

func fromDiskSeverity(severity int) Severity {
	if severity < 0 || severity > int(SeverityHigh) {
		return SeverityGood
	}
	return Severity(severity)
}
