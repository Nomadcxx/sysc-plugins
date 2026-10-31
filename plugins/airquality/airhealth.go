package airquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AirHealthBase is the app service shared by the Australian regional pollen sites.
const AirHealthBase = "https://app-api.airhealthservices.au"

// AirHealthAttribution is the credit the service requires wherever its data
// is shown. It is not optional: the dashboard response carries the same
// string in its attribution field and the official apps render it as a
// footer.
const AirHealthAttribution = "Data provided in collaboration with the University of Tasmania and University of Melbourne"

// AirHealth serves the free app data without credentials; coordinates select
// the regional location before the dashboard request.
type AirHealth struct {
	// Base overrides the service root; empty means AirHealthBase.
	Base string
	// Do sends requests; nil means http.DefaultClient.
	Do Doer
	// Now supplies the clock for cache bookkeeping; nil means time.Now.
	Now func() time.Time
}

// Doer is the injectable HTTP seam, satisfied by *http.Client.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

func (a AirHealth) base() string {
	if a.Base != "" {
		return strings.TrimSuffix(a.Base, "/")
	}
	return AirHealthBase
}

func (a AirHealth) doer() Doer {
	if a.Do != nil {
		return a.Do
	}
	return http.DefaultClient
}

// get performs one GET and returns the body. It does not retry: the service is
// CDN-cached and a failure is better shown as stale data than as a retry loop.
func (a AirHealth) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.doer().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusError("airhealth", resp); err != nil {
		return nil, err
	}
	return readLimited(resp.Body)
}

// Location resolves coordinates to the statistical area the dashboard routes
// take.
//
// The coordinate parameter is latitude first. Passing longitude first does not
// fail: the service resolves the swapped pair to a different Australian region.
func (a AirHealth) Location(ctx context.Context, lat, lon float64) (Location, error) {
	query := url.Values{}
	query.Set("coordinate", fmt.Sprintf("%g,%g", lat, lon))
	body, err := a.get(ctx, "/locations/?"+query.Encode())
	if err != nil {
		return Location{}, err
	}
	return parseLocation(body)
}

type wireLocations struct {
	Locations []struct {
		ID        int     `json:"id"`
		Postcode  string  `json:"postcode"`
		Suburb    string  `json:"suburb"`
		State     string  `json:"state"`
		SAL       string  `json:"SAL"`
		SALName   string  `json:"SAL Name"`
		SA3       string  `json:"SA3"`
		SA3Name   string  `json:"SA3 Name"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		UseSAL    bool    `json:"use_sal"`
	} `json:"locations"`
}

func parseLocation(body []byte) (Location, error) {
	var wire wireLocations
	if err := json.Unmarshal(body, &wire); err != nil {
		return Location{}, fmt.Errorf("airhealth locations: %w", err)
	}
	if len(wire.Locations) == 0 {
		return Location{}, fmt.Errorf("airhealth locations: no match for coordinates: %w", ErrNoCoverage)
	}
	first := wire.Locations[0]
	loc := Location{
		Suburb: first.Suburb, State: first.State, Postcode: first.Postcode,
		SAL: first.SAL, SA3: first.SA3,
	}
	if loc.Suburb == "" {
		loc.Suburb = first.SALName
	}
	return loc, nil
}

// DashboardPath is the route the report is read from.
//
// The service resolves either a SAL (a suburb) or an SA3 (a statistical area).
// The SAL route returns a single location in about 9 KB; the SA3 route returns
// every suburb in the area, which is 150 KB for Melbourne and is only needed
// when a coordinates lookup resolves to a SAL the service will not serve
// directly.
func (l Location) DashboardPath() string {
	if l.SAL != "" {
		return "/dashboard/metricdash/SAL/" + url.PathEscape(l.SAL) + "/"
	}
	return "/dashboard/metricdash/SA3/" + url.PathEscape(l.SA3) + "/"
}

// Tree fetches the published metric tree, which carries each measure's
// category breakpoints and the warn level for every band.
//
// The route has no trailing slash. With one, the service answers 404 and a
// caller that ignored the error would silently fall back to the built-in
// scale, which is close but not authoritative.
func (a AirHealth) MetricTree(ctx context.Context) (*Tree, error) {
	body, err := a.get(ctx, "/dashboard/metrictree")
	if err != nil {
		return nil, err
	}
	return ParseTree(body)
}

// Dashboard fetches and decodes the report for a location.
func (a AirHealth) Dashboard(ctx context.Context, loc Location) (Snapshot, error) {
	body, err := a.get(ctx, loc.DashboardPath())
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := ParseDashboard(body)
	if err != nil {
		return Snapshot{}, err
	}
	// An SA3 response covers many suburbs; pick the requested one, which the
	// caller resolved by coordinate. Falling back to the first location keeps
	// the snapshot non-empty when the identifiers disagree.
	if snap.Location.SAL != loc.SAL {
		snap = selectSAL(snap, body, loc.SAL)
	}
	a.attachBreakdowns(ctx, &snap)
	return snap, nil
}

// Measure detail slugs. They are not derivable from the measure name: the
// service spells the URL "Thunderstorm_Asthma" while the payload says
// "Thunderstorm Asthma", and it accepts two undocumented aliases. Hard-coding
// the map is the only reliable route.
var detailSlugs = map[string]string{
	MeasureAirQuality:   "Air_Quality",
	MeasureAllergens:    "Allergens",
	MeasureGrass:        "Grass",
	MeasureThunderstorm: "Thunderstorm_Asthma",
	MeasureWoodsmoke:    "Woodsmoke",
}

// attachBreakdowns fills each measure's constituent readings from the detail
// endpoint, and is best-effort by design.
//
// The dashboard summary says "Air Quality: Fair" and "Other Allergens: High".
// The detail endpoint is what says which pollutant and which species produced
// those words, which is the part a reader can act on. It is a second request
// per measure, so failures are swallowed: a reading without its breakdown is
// still a useful reading, and a plugin that blanked the panel because an
// optional enrichment failed would be worse than one that quietly omitted it.
func (a AirHealth) attachBreakdowns(ctx context.Context, snap *Snapshot) {
	if snap == nil || snap.Location.SAL == "" {
		return
	}
	for i := range snap.Measures {
		m := &snap.Measures[i]
		slug, ok := detailSlugs[m.Name]
		if !ok {
			continue
		}
		path := "/dashboard/metricdash/free/SAL/" + url.PathEscape(snap.Location.SAL) + "/details/" + slug + "/"
		body, err := a.get(ctx, path)
		if err != nil {
			continue
		}
		m.Breakdown = parseBreakdowns(m.Name, body)
	}
}

// parseBreakdowns extracts the per-constituent rows from a detail response.
//
// The response shape is inconsistent by design: air quality and allergens put
// their series under "combined" plus one key per constituent, grass carries a
// "combined" and a duplicate "Grass" key, and thunderstorm asthma sends a flat
// array instead of an object at all. Decoding into raw JSON and switching on
// the shape is the only way to handle all three without special-casing at the
// call site.
func parseBreakdowns(measure string, body []byte) []Breakdown {
	var wire struct {
		Locations []struct {
			Data json.RawMessage `json:"data"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || len(wire.Locations) == 0 {
		return nil
	}
	data := wire.Locations[0].Data
	if len(data) == 0 {
		return nil
	}

	series := map[string][]wireDetailEntry{}
	if err := json.Unmarshal(data, &series); err != nil {
		// Not an object: thunderstorm asthma and woodsmoke send a flat array.
		var flat []wireDetailEntry
		if err := json.Unmarshal(data, &flat); err != nil {
			return nil
		}
		series["combined"] = flat
	}

	// "combined" is the summary this measure already carries, so the useful
	// rows are the named constituents beside it.
	var out []Breakdown
	for name, entries := range series {
		if strings.EqualFold(name, "combined") || strings.EqualFold(name, measure) {
			continue
		}
		entry, ok := todayEntry(entries)
		if !ok {
			continue
		}
		out = append(out, Breakdown{
			Name:  name,
			Level: Level{Name: strings.TrimSpace(entry.Text), Index: entry.Value},
		})
	}
	sortBreakdowns(out)
	return out
}

// wireDetailEntry is one painted slot in a detail series. It carries a
// category and, for some measures, a box count rather than a concentration.
type wireDetailEntry struct {
	Name         string `json:"name"`
	ForecastDate string `json:"forecast_date"`
	DayTitle     string `json:"day_title"`
	Text         string `json:"text"`
	Value        int    `json:"value"`
}

// todayEntry picks the entry for the current day.
//
// It keys on forecast_date rather than taking the first element, because the
// series are not the same length: in one live capture the combined air quality
// series had two entries while PM2.5 had three. Zipping them by index would
// silently pair today's summary with tomorrow's pollutant.
func todayEntry(entries []wireDetailEntry) (wireDetailEntry, bool) {
	for _, e := range entries {
		if e.DayTitle == "Now" || e.DayTitle == "Later today" || e.DayTitle == "Later" {
			return e, true
		}
	}
	for _, e := range entries {
		if e.ForecastDate != "" {
			return e, true
		}
	}
	if len(entries) > 0 {
		return entries[0], true
	}
	return wireDetailEntry{}, false
}

// breakdownOrder is the presentation order for constituents: particulates and
// then gases for air quality, the tree species before the spore count for the
// allergen mix. Anything unlisted sorts after, alphabetically.
var breakdownOrder = []string{
	"PM2.5", "PM10", "O3", "NO2", "SO2", "CO",
	"Grass", "Birch", "Cypress", "Eucalypts", "Olive", "Plane", "Plantain", "Alternaria",
}

func sortBreakdowns(parts []Breakdown) {
	rank := make(map[string]int, len(breakdownOrder))
	for i, name := range breakdownOrder {
		rank[name] = i
	}
	key := func(name string) int {
		if r, ok := rank[name]; ok {
			return r
		}
		return len(breakdownOrder)
	}
	for i := 1; i < len(parts); i++ {
		for j := i; j > 0; j-- {
			a, b := parts[j-1], parts[j]
			if key(a.Name) < key(b.Name) || (key(a.Name) == key(b.Name) && a.Name <= b.Name) {
				break
			}
			parts[j-1], parts[j] = b, a
		}
	}
}

// selectSAL re-decodes the SA3 body and returns the snapshot for one suburb.
func selectSAL(_ Snapshot, body []byte, sal string) Snapshot {
	var wire wireDashboard
	if err := json.Unmarshal(body, &wire); err != nil || len(wire.Locations) == 0 {
		return Snapshot{}
	}
	for _, raw := range wire.Locations {
		if raw.Location.SAL != sal {
			continue
		}
		// Re-use the whole decoder on a synthetic single-location body so the
		// normalisation rules live in exactly one place.
		single := wireDashboard{
			SALs:        []string{sal},
			Locations:   []wireLocation{raw},
			Attribution: wire.Attribution,
		}
		if encoded, err := json.Marshal(single); err == nil {
			if snap, err := ParseDashboard(encoded); err == nil {
				return snap
			}
		}
	}
	return Snapshot{}
}

// The dashboard wire shapes. Only the fields the plugin reads are declared;
// the response also carries ads, news, factsheets and podcasts, which a
// reading plugin has no use for.

type wireDashboard struct {
	SALs        []string       `json:"sals"`
	Locations   []wireLocation `json:"locations"`
	Attribution string         `json:"attribution"`
}

type wireLocation struct {
	Location struct {
		ID      int      `json:"id"`
		SAL     string   `json:"sal"`
		Suburb  string   `json:"suburb"`
		State   string   `json:"state"`
		SA3     []string `json:"sa3"`
		SA3Name []string `json:"sa3_name"`
	} `json:"location"`
	CurrentConditions struct {
		Date     string `json:"date"`
		DayTitle string `json:"day_title"`
		Text     string `json:"text"`
		Weather  struct {
			RainRange      string   `json:"rain_range"`
			RainChance     *float64 `json:"rain_probability"`
			TemperatureMin float64  `json:"temperature_min"`
			TemperatureMax float64  `json:"temperature_max"`
		} `json:"weather"`
	} `json:"current_conditions"`
	Measures []wireMeasure `json:"measures"`
	Alert    *wireAlert    `json:"alert"`
	Forecast []struct {
		Date           string        `json:"date"`
		DayTitle       string        `json:"day_title"`
		TemperatureMax float64       `json:"temperature_max"`
		TemperatureMin float64       `json:"temperature_min"`
		Measures       []wireMeasure `json:"measures"`
	} `json:"forecast"`
}

type wireMeasure struct {
	Name       string `json:"name"`
	Text       string `json:"text"`
	Value      int    `json:"value"`
	Background string `json:"background_colour"`
	ShowMetric *struct {
		Subcat string  `json:"subcat"`
		Value  float64 `json:"value"`
		Units  string  `json:"units"`
	} `json:"show_metric"`
}

type wireAlert struct {
	Title      string `json:"title"`
	Text       string `json:"text"`
	Background string `json:"background_colour"`
}

// ParseDashboard decodes a dashboard response into the snapshot the plugin
// uses. It normalises measure names, maps raw indices onto severity using the
// published scale when one is available, and preserves forecast sparsity.
func ParseDashboard(body []byte) (Snapshot, error) {
	var wire wireDashboard
	if err := json.Unmarshal(body, &wire); err != nil {
		return Snapshot{}, fmt.Errorf("airhealth dashboard: %w", err)
	}
	if len(wire.Locations) == 0 {
		return Snapshot{}, errors.New("airhealth dashboard: no locations")
	}
	loc := wire.Locations[0]
	snap := Snapshot{
		Location: Location{
			Suburb: loc.Location.Suburb, State: loc.Location.State, SAL: loc.Location.SAL,
		},
		Date:        loc.CurrentConditions.Date,
		Title:       loc.CurrentConditions.DayTitle,
		Advice:      loc.CurrentConditions.Text,
		Attribution: wire.Attribution,
	}
	if snap.Attribution == "" {
		snap.Attribution = AirHealthAttribution
	}
	if w := loc.CurrentConditions.Weather; w.TemperatureMin != 0 || w.TemperatureMax != 0 || w.RainRange != "" {
		weather := &Weather{
			MinC: w.TemperatureMin, MaxC: w.TemperatureMax, RainRange: w.RainRange,
			RainChance: -1,
		}
		if w.RainChance != nil {
			weather.RainChance = *w.RainChance
		}
		snap.Weather = weather
	}
	if len(loc.Location.SA3) > 0 {
		snap.Location.SA3 = loc.Location.SA3[0]
	}

	for _, m := range loc.Measures {
		snap.Measures = append(snap.Measures, normaliseMeasure(m, WireScale))
	}

	if loc.Alert != nil {
		alert := &Alert{Title: loc.Alert.Title, Text: loc.Alert.Text}
		if byName := matchAlertLevel(snap.Measures, loc.Alert.Title); byName.Ok {
			alert.Level = byName.Level
		} else {
			alert.Level = Level{Name: levelFromText(loc.Alert.Text), Severity: SeverityOf(1)}
		}
		snap.Alert = alert
	}

	for _, f := range loc.Forecast {
		day := Day{
			Date: f.Date, Title: f.DayTitle, MaxC: f.TemperatureMax, MinC: f.TemperatureMin,
			Measures: map[string]Measure{},
		}
		for _, m := range f.Measures {
			measure := normaliseMeasure(m, WireScale)
			day.Measures[measure.Name] = measure
		}
		snap.Forecast = append(snap.Forecast, day)
	}
	return snap, nil
}

// levelMatch is a small optional type so matchAlertLevel can report both the
// level and whether it found one.
type levelMatch struct {
	Level Level
	Ok    bool
}

// matchAlertLevel finds the current measure an alert refers to.
func matchAlertLevel(measures []Measure, title string) levelMatch {
	want := CanonicalMeasure(strings.TrimSuffix(strings.TrimSpace(title), " Alert"))
	for _, m := range measures {
		if m.Name == want {
			return levelMatch{Level: m.Level, Ok: true}
		}
	}
	return levelMatch{}
}

// levelFromText pulls the category out of an advisory sentence such as "The
// Thunderstorm Asthma forecast for this location is currently: Moderate."
func levelFromText(text string) string {
	lower := strings.ToLower(text)
	for _, name := range []string{"Extremely Poor", "Very Poor", "Very Good", "Moderate", "Extreme", "High", "Fair", "Poor", "Low", "Good"} {
		if strings.Contains(lower, strings.ToLower(name)) {
			return name
		}
	}
	return ""
}

// normaliseMeasure converts one wire row into the domain type.
//
// Severity is taken from the raw index by looking up the published scale for
// that measure, because the index alone is not comparable: Air Quality counts
// upward from "Very Good" while the pollen measures count upward from "Low".
// When no scale is known the index is passed through the warn-level mapping,
// which is right for the pollen measures and only approximately right for air
// quality.
func normaliseMeasure(m wireMeasure, scale *Tree) Measure {
	name := CanonicalMeasure(m.Name)
	level := Level{Name: strings.TrimSpace(m.Text), Index: m.Value}
	if classes, ok := scale.Lookup(name, metricFor(m)); ok {
		matched := false
		for _, c := range classes {
			if strings.EqualFold(c.Name, level.Name) {
				level.Severity = SeverityOf(c.WarnLevel)
				matched = true
				break
			}
		}
		if !matched && level.Known() {
			// Fail safe if the provider adds a category before the scale updates.
			level.Severity = SeverityHigh
		}
	} else {
		level.Severity = SeverityOf(m.Value)
	}
	out := Measure{Name: name, Title: Title(name), Level: level, Source: "airhealth"}
	if m.ShowMetric != nil {
		out.Metric = &Metric{
			Subcat: m.ShowMetric.Subcat, Value: m.ShowMetric.Value, Units: m.ShowMetric.Units,
		}
	}
	scale.AttachDial(&out)
	return out
}

// metricFor names the scale a measure's category belongs to, which is the
// pollutant the air quality row reports on.
func metricFor(m wireMeasure) string {
	if m.ShowMetric != nil && m.ShowMetric.Subcat != "" {
		return m.ShowMetric.Subcat
	}
	return ""
}

// WireScale is the fallback scale used by ParseDashboard when the caller has
// not fetched the published tree. It carries only the categories the service
// publishes, with the severities the tree reports.
var WireScale = mustDefaultScale()

func mustDefaultScale() *Tree {
	t := &Tree{scales: map[string]map[string][]Class{
		MeasureGrass: {MeasureGrass: {
			{Name: "Low", UpperBound: 20, WarnLevel: 0},
			{Name: "Moderate", UpperBound: 50, WarnLevel: 1},
			{Name: "High", UpperBound: 100, WarnLevel: 2},
			{Name: "Extreme", UpperBound: 2000000, WarnLevel: 2},
		}},
		MeasureAllergens: {MeasureAllergens: {
			{Name: "Low", UpperBound: 20, WarnLevel: 0},
			{Name: "Moderate", UpperBound: 50, WarnLevel: 0},
			{Name: "High", UpperBound: 100, WarnLevel: 0},
			{Name: "Extreme", UpperBound: 2000000, WarnLevel: 1},
		}},
		MeasureAirQuality: {
			"PM2.5": {
				{Name: "Very Good", UpperBound: 5, WarnLevel: 0},
				{Name: "Good", UpperBound: 12.5, WarnLevel: 0},
				{Name: "Fair", UpperBound: 25, WarnLevel: 1},
				{Name: "Poor", UpperBound: 50, WarnLevel: 1},
				{Name: "Very Poor", UpperBound: 150, WarnLevel: 2},
				{Name: "Extremely Poor", UpperBound: 1000000, WarnLevel: 2},
			},
		},
		MeasureThunderstorm: {MeasureThunderstorm: {
			{Name: "Low", UpperBound: 0.5, WarnLevel: 0},
			{Name: "Moderate", UpperBound: 1.5, WarnLevel: 1},
			{Name: "High", UpperBound: 2.5, WarnLevel: 2},
		}},
		MeasureWoodsmoke: {MeasureWoodsmoke: {
			{Name: "Low", UpperBound: 350, WarnLevel: 0},
			{Name: "Moderate", UpperBound: 450, WarnLevel: 0},
			{Name: "High", UpperBound: 550, WarnLevel: 1},
		}},
	}}
	return t
}
