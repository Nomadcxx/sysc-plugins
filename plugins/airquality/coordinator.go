package airquality

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
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
	// GoogleKey enables Google pollen when AirHealth and Open-Meteo have no
	// pollen; empty leaves it off.
	GoogleKey string
	// Label names the location when no regional source does, such as the
	// shell's weather location "London".
	Label string
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
	// Origin is the coordinate pair the report was requested for, or nil for a
	// report persisted before origins were recorded. It keeps one place's
	// reading from being shown, or carried forward, as another's.
	Origin *Coordinates
	// PollenNote explains a report with no pollen rows, such as a location
	// that needs a Google Pollen API key.
	PollenNote string
}

// Coordinates is a requested location in decimal degrees.
type Coordinates struct {
	Latitude  float64
	Longitude float64
}

// Same reports whether two pairs name the same configured place. Settings
// arrive as JSON numbers, so equal settings decode to equal floats; the
// tolerance only absorbs a persisted round trip.
func (c Coordinates) Same(other Coordinates) bool {
	const epsilon = 1e-6
	return math.Abs(c.Latitude-other.Latitude) < epsilon && math.Abs(c.Longitude-other.Longitude) < epsilon
}

// Fetcher runs one refresh against the configured sources.
type Fetcher struct {
	AirHealth AirHealth
	EPA       EPAArcGIS
	OpenMeteo OpenMeteo
	// Google carries the transport; the key comes from Sources.
	Google GooglePollen
	Now    func() time.Time
}

// Fetch builds a report from the enabled sources.
//
// Air quality precedence is AirHealth, then EPA, then Open-Meteo wherever
// those regional sources cover the configured coordinates. Open-Meteo remains
// the global fallback and is consulted for UV and gaseous pollutants too.
//
// Pollen precedence is AirHealth, Open-Meteo, then Google when earlier sources
// have no pollen. The first that answers supplies the rows and forecast.
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

	_, havePollen := snap.Measure(MeasureGrass)
	if _, ok := snap.Measure(MeasureAllergens); ok {
		havePollen = true
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
			snap.Measures = mergeMeasures(snap.Measures, model.Measures, haveAir, haveUV)
			if !havePollen && len(model.Pollen) > 0 {
				snap.Measures = append(snap.Measures, model.Pollen...)
				if len(snap.Forecast) == 0 {
					snap.Forecast = model.Forecast
				}
				havePollen = true
			}
		}
	}
	if !havePollen && src.GoogleKey != "" {
		google := f.Google
		google.Key = src.GoogleKey
		measures, forecast, err := google.Fetch(ctx, src.Latitude, src.Longitude)
		switch {
		case err == nil && len(measures) > 0:
			snap.Measures = append(snap.Measures, measures...)
			snap.Attribution = joinAttribution(snap.Attribution, GoogleAttribution)
			if len(snap.Forecast) == 0 {
				snap.Forecast = forecast
			}
			havePollen = true
		case err != nil && !errors.Is(err, ErrNoCoverage):
			problems = append(problems, describe("Google Pollen", err))
		}
	}

	// AirHealth names its suburb; otherwise the shell's weather label beats an
	// EPA station name or bare coordinates.
	if snap.Location.SAL == "" && src.Label != "" {
		snap.Location.Suburb = src.Label
	}
	if snap.Location.Suburb == "" && len(snap.Measures) > 0 {
		snap.Location.Suburb = FormatCoordinates(src.Latitude, src.Longitude)
	}
	note := ""
	if !havePollen && !AirHealthSupportsCoordinates(src.Latitude, src.Longitude) {
		note = "No pollen forecast is published for this location."
		if src.GoogleKey == "" {
			note = "Pollen here needs a Google Pollen API key; add one in this plugin's settings."
		}
	}

	if len(snap.Measures) == 0 {
		if len(problems) == 0 {
			problems = append(problems, "no sources enabled")
		}
		return Report{Stale: true, Errors: problems}, fmt.Errorf("air quality: %s", strings.Join(problems, "; "))
	}
	snap.Measures = sortMeasures(snap.Measures)
	for i := range snap.Measures {
		snap.Measures[i].Updated = now
	}
	origin := Coordinates{Latitude: src.Latitude, Longitude: src.Longitude}
	return Report{Snapshot: snap, UpdatedAt: now, Errors: problems, Origin: &origin, PollenNote: note}, nil
}

// AirHealthSupportsCoordinates filters out locations outside Australia before
// the regional app lookup. The service still resolves the exact supported area.
// ponytail: this mainland/Tasmania envelope omits external territories; use
// boundary polygons if those become supported.
func AirHealthSupportsCoordinates(lat, lon float64) bool {
	return lat >= -44 && lat <= -10 && lon >= 112 && lon <= 154
}

// victoria outlines the state as (longitude, latitude) pairs, following the
// Murray to its source, the straight line to Cape Howe, the coast and the
// South Australian border. It is coarse, a little outside the border, which
// is all a station search needs: the radius decides the rest.
var victoria = [][2]float64{
	{140.95, -33.95}, {142.16, -34.05}, {143.55, -35.25}, {144.75, -36.05},
	{146.92, -35.98}, {147.9, -35.95}, {148.25, -36.75}, {150.05, -37.5},
	{146.4, -39.25}, {141.6, -38.5}, {140.95, -38.1},
}

// EPAVictoriaMayCoverCoordinates reports whether coordinates fall in
// Victoria, the only place the EPA Victoria network has stations. Its
// configured radius then decides whether one is in range.
func EPAVictoriaMayCoverCoordinates(lat, lon float64) bool {
	inside := false
	for i, j := 0, len(victoria)-1; i < len(victoria); j, i = i, i+1 {
		a, b := victoria[i], victoria[j]
		if (a[1] > lat) != (b[1] > lat) && lon < (b[0]-a[0])*(lat-a[1])/(b[1]-a[1])+a[0] {
			inside = !inside
		}
	}
	return inside
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

// EPAMaxAge is how old a station's averaging window may be and still count as
// the current reading.
const EPAMaxAge = 3 * time.Hour

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
	// EPA publishes hourly averages, and the keyless mirror stopped updating
	// on 2025-12-05. A station is current only if its averaging window ended
	// recently; anything older would pass for today's air.
	now := f.now()
	current := sites[:0]
	var newest time.Time
	for _, site := range sites {
		if site.Until.After(newest) {
			newest = site.Until
		}
		if !site.Until.IsZero() && now.Sub(site.Until) <= EPAMaxAge {
			current = append(current, site)
		}
	}
	if len(current) == 0 {
		if newest.IsZero() {
			return Measure{}, "", fmt.Errorf("%w: no current station reading", ErrUnavailable)
		}
		return Measure{}, "", fmt.Errorf("%w: no current station reading (latest %s)", ErrUnavailable, newest.Format("2 Jan 2006"))
	}
	sites = current
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

// FormatCoordinates names a place with no suburb, such as a location outside
// the regional services, with hemispheres rather than bare signed numbers.
func FormatCoordinates(lat, lon float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns = "S"
	}
	if lon < 0 {
		ew = "W"
	}
	return fmt.Sprintf("%.2f°%s, %.2f°%s", math.Abs(lat), ns, math.Abs(lon), ew)
}

// describe turns a source error into panel text. It keeps the cause short:
// the panel has one line for it, and the full error goes to the log.
func describe(source string, err error) string {
	return source + ": " + shortError(err)
}

// shortError says what went wrong in a reader's words. Transport failures and
// HTTP statuses are named by what they mean; anything else is the error's
// last clause, bounded to one line.
func shortError(err error) string {
	if err == nil {
		return ""
	}
	var status *httpStatusError
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.As(err, &status):
		if status.code >= 500 || status.code == http.StatusTooManyRequests {
			return fmt.Sprintf("service unavailable (HTTP %d)", status.code)
		}
		return fmt.Sprintf("request refused (HTTP %d)", status.code)
	case errors.As(err, &network):
		if network.Timeout() {
			return "timed out"
		}
		return "can't connect; check the network"
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

// CarryLimit is how long a measure from a failing source stays on the panel
// beside fresh readings from the sources that answered. AirHealth publishes its
// pollen forecast once a day, so half a day keeps a morning reading through an
// afternoon outage without presenting yesterday's forecast as today's.
const CarryLimit = 12 * time.Hour

// Cache keeps the last good report so a refresh failure shows stale data
// rather than an empty panel, and so a restart does not begin blank.
type Cache struct {
	mu     sync.Mutex
	report Report
	seen   bool
}

// Put stores a successful report without Google's non-cacheable rows.
func (c *Cache) Put(report Report) {
	c.mu.Lock()
	defer c.mu.Unlock()
	report = WithoutGoogle(report)
	report.Stale = false
	c.report, c.seen = report, true
}

// Merge stores a partial report: the sources that answered replace their own
// rows, and rows from the sources that failed are carried over from the stored
// report for the same place while they are younger than CarryLimit. The merged
// report is stale when it carries anything, and its UpdatedAt is the oldest
// carried row's, so the panel's age never understates what it shows. The return
// value includes this refresh's Google rows for live display; retained state
// does not.
func (c *Cache) Merge(fresh Report) Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	merged := fresh
	merged.Stale = false
	if c.seen && sameOrigin(c.report, fresh) {
		merged = carryOver(fresh, c.report)
	}
	c.report, c.seen = WithoutGoogle(merged), true
	return merged
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
	c.report = WithoutGoogle(c.report)
	c.report.Stale = true
	c.report.Errors = errors
	return c.report, true
}

// Get returns the retained report, without Google's non-cacheable rows.
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
	report = WithoutGoogle(report)
	report.Stale = true
	c.report, c.seen = report, true
}

// Relocate drops the stored report unless it was fetched for at. A report
// from before origins were recorded cannot prove its place, so it is dropped
// too: one refresh is cheaper than another city's pollen count.
func (c *Cache) Relocate(at Coordinates) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen && (c.report.Origin == nil || !c.report.Origin.Same(at)) {
		c.report, c.seen = Report{}, false
	}
}

func sameOrigin(a, b Report) bool {
	return a.Origin != nil && b.Origin != nil && a.Origin.Same(*b.Origin)
}

// carryOver adds previous's still-current rows that fresh lacks. AirHealth is
// the only source of the forecast, the advisory and the suburb, so those come
// along with its rows.
func carryOver(fresh, previous Report) Report {
	merged := fresh
	merged.Snapshot.Measures = append([]Measure(nil), fresh.Snapshot.Measures...)
	oldest := fresh.UpdatedAt
	carried := map[string]bool{}
	for _, m := range previous.Snapshot.Measures {
		if _, ok := fresh.Snapshot.Measure(m.Name); ok {
			continue
		}
		// Google's terms prohibit caching its data, so a Google row is never
		// shown past the refresh that fetched it.
		if m.Source == "google" || m.Updated.IsZero() || fresh.UpdatedAt.Sub(m.Updated) > CarryLimit {
			continue
		}
		merged.Snapshot.Measures = append(merged.Snapshot.Measures, m)
		carried[m.Source] = true
		if m.Updated.Before(oldest) {
			oldest = m.Updated
		}
	}
	if len(carried) == 0 {
		return merged
	}
	merged.Stale = true
	merged.UpdatedAt = oldest
	merged.Snapshot.Measures = sortMeasures(merged.Snapshot.Measures)
	if carried["airhealth"] {
		snap, prev := &merged.Snapshot, previous.Snapshot
		if snap.Location.SAL == "" {
			snap.Location = prev.Location
		}
		if snap.Alert == nil {
			snap.Alert = prev.Alert
		}
		if len(snap.Forecast) == 0 {
			snap.Forecast = prev.Forecast
		}
		if snap.Weather == nil {
			snap.Weather = prev.Weather
		}
		if snap.Date == "" {
			snap.Date, snap.Title, snap.Advice = prev.Date, prev.Title, prev.Advice
		}
	}
	for source := range carried {
		merged.Snapshot.Attribution = joinAttribution(merged.Snapshot.Attribution, sourceCredit(previous.Snapshot.Attribution, source))
	}
	return merged
}

// sourceCredit finds one source's credit in a joined attribution. AirHealth
// sends its own wording, so it is whichever credit is not one of the others.
func sourceCredit(attribution, source string) string {
	switch source {
	case "epa":
		return EPAAttribution
	case "open-meteo":
		return OpenMeteoAttribution
	}
	for _, credit := range strings.Split(attribution, " · ") {
		if credit != EPAAttribution && credit != OpenMeteoAttribution {
			return credit
		}
	}
	return ""
}
