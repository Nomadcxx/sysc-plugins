package airquality

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Sources selects which providers a refresh uses.
type Sources struct {
	// AirHealth enables the Australian regional pollen app service.
	AirHealth bool
	// EPA enables the keyless EPA Victoria mirror for particulate readings.
	EPA bool
	// OpenMeteo enables the global CAMS model, which supplies the fallback AQI,
	// UV index and gaseous pollutants. It needs no key.
	OpenMeteo bool
	// Latitude and Longitude are the location the report covers.
	Latitude  float64
	Longitude float64
	// Site is an optional EPA station name to prefer over the nearest one.
	Site string
	// RadiusKM bounds the EPA station search.
	RadiusKM float64
}

// Defaults for the Melbourne CBD, used when a setting is absent.
const (
	DefaultLatitude  = -37.8136
	DefaultLongitude = 144.9631
	DefaultRadiusKM  = 15.0
)

// Report is the merged result of one refresh.
type Report struct {
	// Snapshot is the current reading.
	Snapshot Snapshot
	// UpdatedAt is when the data was fetched.
	UpdatedAt time.Time
	// Stale reports that this is a previous successful reading kept because
	// the latest refresh failed. A stale report is worth showing: yesterday's
	// grass count still beats a blank panel.
	Stale bool
	// Errors names the sources that failed, so the panel can say which part
	// of the reading is old rather than implying the whole thing is.
	Errors []string
}

// Fetcher runs one refresh against the configured sources.
type Fetcher struct {
	AirHealth AirHealth
	EPA       EPAArcGIS
	OpenMeteo OpenMeteo
	Now       func() time.Time
}

// Fetch builds a report from the enabled sources.
//
// Air quality precedence is AirHealth, then EPA, then Open-Meteo wherever
// those regional sources cover the configured coordinates. Open-Meteo remains
// the global fallback and is consulted for UV and gaseous pollutants too.
func (f Fetcher) Fetch(ctx context.Context, src Sources) (Report, error) {
	now := f.now()
	var (
		snap     Snapshot
		problems []string
		haveAir  bool
		haveUV   bool
	)

	if src.AirHealth {
		airSnap, err := f.fetchAirHealth(ctx, src)
		switch {
		case err == nil:
			snap = airSnap
			_, haveAir = snap.Measure(MeasureAirQuality)
			_, haveUV = snap.Measure(MeasureUV)
		default:
			if !errors.Is(err, ErrNoCoverage) {
				problems = append(problems, describe("AirHealth pollen", err))
			}
		}
	}

	if src.EPA && !haveAir {
		measure, site, err := f.fetchEPA(ctx, src)
		if err != nil {
			if !errors.Is(err, ErrNoCoverage) {
				problems = append(problems, describe("EPA Victoria", err))
			}
		} else {
			snap.Attribution = joinAttribution(snap.Attribution, EPAAttribution)
			snap.Measures = append(snap.Measures, measure)
			haveAir = true
			if snap.Location.Suburb == "" {
				snap.Location.Suburb = site
			}
		}
	}

	if src.OpenMeteo {
		model, err := f.OpenMeteo.Fetch(ctx, src.Latitude, src.Longitude)
		if err != nil {
			problems = append(problems, describe("Open-Meteo", err))
		} else {
			snap.Attribution = joinAttribution(snap.Attribution, OpenMeteoAttribution)
			snap.Measures = mergeMeasures(snap.Measures, model, haveAir, haveUV)
			if snap.Location.Suburb == "" {
				snap.Location.Suburb = fmt.Sprintf("%.2f, %.2f", src.Latitude, src.Longitude)
			}
		}
	}

	if len(snap.Measures) == 0 {
		if len(problems) == 0 {
			problems = append(problems, "no sources enabled")
		}
		return Report{Stale: true, Errors: problems}, fmt.Errorf("air quality: %s", strings.Join(problems, "; "))
	}
	snap.Measures = sortMeasures(snap.Measures)
	return Report{Snapshot: snap, UpdatedAt: now, Errors: problems}, nil
}

// AirHealthSupportsCoordinates filters out locations outside Australia before
// the regional app lookup. The service still resolves the exact supported area.
// ponytail: this mainland/Tasmania envelope omits external territories; use
// boundary polygons if those become supported.
func AirHealthSupportsCoordinates(lat, lon float64) bool {
	return lat >= -44 && lat <= -10 && lon >= 112 && lon <= 154
}

// EPAVictoriaMayCoverCoordinates filters out global coordinates before
// querying the Victorian station mirror. Its configured radius determines
// whether a station actually covers the requested Australian location.
func EPAVictoriaMayCoverCoordinates(lat, lon float64) bool {
	return AirHealthSupportsCoordinates(lat, lon)
}

// mergeMeasures adds the model's measures alongside what the other sources
// already produced.
//
// Air quality is skipped when a preferred source supplied it, but the model's
// breakdown is still worth keeping: it is the only place the gaseous
// pollutants appear, and they explain why the category is what it is. So when
// both exist, the model's constituents are attached to the winner's row rather
// than replacing it.
func mergeMeasures(have []Measure, model []Measure, haveAir, haveUV bool) []Measure {
	out := have
	for _, m := range model {
		switch m.Name {
		case MeasureAirQuality:
			if haveAir {
				attachModelBreakdown(out, m)
				continue
			}
		case MeasureUV:
			if haveUV {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// attachModelBreakdown copies the model's pollutant readings onto the existing
// air quality row, keeping the authoritative category and its numeric value
// and appending only the constituents the row does not already have.
//
// The row's own metric is seeded first. EPA reports PM2.5 as the headline
// value, and the model reports its own PM2.5 separately; without seeding, the
// panel would show "Fair · 19.9 µg/m³" above "PM2.5 4.8", which reads as a
// contradiction even though both numbers are correct for their own source.
func attachModelBreakdown(measures []Measure, model Measure) {
	for i := range measures {
		if measures[i].Name != MeasureAirQuality {
			continue
		}
		seen := make(map[string]bool, len(measures[i].Breakdown)+1)
		for _, part := range measures[i].Breakdown {
			seen[part.Name] = true
		}
		if metric := measures[i].Metric; metric != nil && metric.Subcat != "" {
			if !seen[metric.Subcat] {
				measures[i].Breakdown = append([]Breakdown{{
					Name:   metric.Subcat,
					Level:  measures[i].Level,
					Metric: metric,
				}}, measures[i].Breakdown...)
				seen[metric.Subcat] = true
			}
		}
		for _, part := range model.Breakdown {
			if !seen[part.Name] {
				measures[i].Breakdown = append(measures[i].Breakdown, part)
			}
		}
		return
	}
}

// joinAttribution appends a credit without duplicating one already present.
func joinAttribution(existing, add string) string {
	if add == "" || strings.Contains(existing, add) {
		return existing
	}
	if existing == "" {
		return add
	}
	return existing + " · " + add
}

func (f Fetcher) fetchAirHealth(ctx context.Context, src Sources) (Snapshot, error) {
	loc, err := f.AirHealth.Location(ctx, src.Latitude, src.Longitude)
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := f.AirHealth.Dashboard(ctx, loc)
	if err != nil {
		return Snapshot{}, err
	}
	if snap.Location.Suburb == "" {
		snap.Location.Suburb = loc.Suburb
	}
	if snap.Location.State == "" {
		snap.Location.State = loc.State
	}
	return snap, nil
}

// fetchEPA returns the air quality measure and the station name it came from.
func (f Fetcher) fetchEPA(ctx context.Context, src Sources) (Measure, string, error) {
	radius := src.RadiusKM
	if radius <= 0 {
		radius = DefaultRadiusKM
	}
	sites, err := f.EPA.Nearest(ctx, src.Latitude, src.Longitude, radius)
	if err != nil {
		return Measure{}, "", err
	}
	// A configured station wins over proximity, so a user who knows their
	// local station is the representative one keeps it even when a closer
	// sensor appears.
	if want := strings.TrimSpace(src.Site); want != "" {
		for _, site := range sites {
			if strings.EqualFold(site.Name, want) {
				if m, ok := site.AirQualityMeasure(); ok {
					return m, site.Name, nil
				}
			}
		}
	}
	for _, site := range sites {
		if m, ok := site.AirQualityMeasure(); ok {
			return m, site.Name, nil
		}
	}
	return Measure{}, "", fmt.Errorf("%w: no EPA station reported a category", ErrUnavailable)
}

func (f Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// describe turns a source error into panel text. It keeps the cause short:
// the panel has one line for it, and the full error goes to the log.
func describe(source string, err error) string {
	return source + ": " + shortError(err)
}

func shortError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg)-i < 60 {
		msg = msg[i+2:]
	}
	if len(msg) > 60 {
		msg = msg[:57] + "…"
	}
	return msg
}

// sortMeasures puts measures in presentation order and drops any the plugin
// does not know how to show, so a new source row cannot push the layout
// around.
func sortMeasures(measures []Measure) []Measure {
	rank := make(map[string]int, len(MeasureOrder))
	for i, name := range MeasureOrder {
		rank[name] = i
	}
	kept := measures[:0]
	for _, m := range measures {
		if _, ok := rank[m.Name]; !ok {
			continue
		}
		kept = append(kept, m)
	}
	measures = kept
	for i := 1; i < len(measures); i++ {
		for j := i; j > 0 && rank[measures[j].Name] < rank[measures[j-1].Name]; j-- {
			measures[j], measures[j-1] = measures[j-1], measures[j]
		}
	}
	return measures
}

// Cache keeps the last good report so a refresh failure shows stale data
// rather than an empty panel, and so a restart does not begin blank.
type Cache struct {
	mu     sync.Mutex
	report Report
	seen   bool
}

// Put stores a successful report.
func (c *Cache) Put(report Report) {
	c.mu.Lock()
	defer c.mu.Unlock()
	report.Stale = false
	c.report, c.seen = report, true
}

// Keep marks the stored report stale and records why, returning it. It is
// what a failed refresh calls so the panel can show the last reading with its
// age and the reason it did not update.
func (c *Cache) Keep(errors []string) (Report, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.seen {
		return Report{}, false
	}
	c.report.Stale = true
	c.report.Errors = errors
	return c.report, true
}

// Get returns the stored report.
func (c *Cache) Get() (Report, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.report, c.seen
}

// Restore seeds the cache from a persisted report, which is what lets the
// panel show the last known reading immediately on start.
func (c *Cache) Restore(report Report) {
	c.mu.Lock()
	defer c.mu.Unlock()
	report.Stale = true
	c.report, c.seen = report, true
}
