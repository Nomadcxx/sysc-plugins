package airquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// airHealthServer answers the location and dashboard routes from fixtures and
// records the paths it saw.
func airHealthServer(t *testing.T, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		switch {
		case strings.HasPrefix(r.URL.Path, "/locations/"):
			_, _ = w.Write([]byte(locationsFixture))
		default:
			_, _ = w.Write([]byte(dashboardFixture))
		}
	}))
}

func TestFetchMergesAirHealthOnly(t *testing.T) {
	var paths []string
	srv := airHealthServer(t, &paths)
	defer srv.Close()

	f := Fetcher{
		AirHealth: AirHealth{Base: srv.URL},
		Now:       func() time.Time { return time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC) },
	}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true, Latitude: -37.8136, Longitude: 144.9631})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if report.Stale {
		t.Error("a successful fetch should not be stale")
	}
	if len(report.Errors) != 0 {
		t.Errorf("errors = %v", report.Errors)
	}
	if _, ok := report.Snapshot.Measure(MeasureGrass); !ok {
		t.Error("grass missing")
	}
	if _, ok := report.Snapshot.Measure(MeasureAirQuality); !ok {
		t.Error("air quality missing")
	}
	if report.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not set")
	}
}

func TestSortMeasuresDropsUnknownAndKeepsPresentationOrder(t *testing.T) {
	got := sortMeasures([]Measure{
		{Name: "New upstream measure", Level: Level{Name: "High", Severity: SeverityHigh}},
		{Name: MeasureUV, Level: Level{Name: "Low", Severity: SeverityGood}},
		{Name: MeasureGrass, Level: Level{Name: "Low", Severity: SeverityGood}},
	})
	if len(got) != 2 || got[0].Name != MeasureGrass || got[1].Name != MeasureUV {
		t.Fatalf("sorted measures = %+v, want grass then UV only", got)
	}
}

func TestFetchRoutesAirHealthByConfiguredAustralianLocation(t *testing.T) {
	cases := []struct {
		name, suburb, state, sal, sa3 string
		lat, lon                      float64
	}{
		{"Melbourne", "Melbourne", "VIC", "21640", "20604", -37.8136, 144.9631},
		{"Canberra", "Canberra City", "ACT", "80038", "80105", -35.2809, 149.1300},
		{"Sydney", "Sydney", "NSW", "13730", "11703", -33.8688, 151.2093},
		{"Perth", "Perth", "WA", "51230", "50302", -31.9505, 115.8605},
	}
	byCoordinate := make(map[string]struct {
		suburb, state, sal, sa3 string
	})
	for _, tc := range cases {
		byCoordinate[fmt.Sprintf("%g,%g", tc.lat, tc.lon)] = struct {
			suburb, state, sal, sa3 string
		}{tc.suburb, tc.state, tc.sal, tc.sa3}
	}
	var dashboardPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("AirHealth request unexpectedly needs credentials: %v", r.Header)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/locations/"):
			place, ok := byCoordinate[r.URL.Query().Get("coordinate")]
			if !ok {
				t.Errorf("unknown configured coordinate %q", r.URL.Query().Get("coordinate"))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"locations": []map[string]any{{
				"suburb": place.suburb, "state": place.state, "SAL": place.sal, "SA3": place.sa3,
			}}})
		case strings.Contains(r.URL.Path, "/details/"):
			w.WriteHeader(http.StatusNotFound) // optional enrichment may be absent
		case strings.HasPrefix(r.URL.Path, "/dashboard/metricdash/SAL/"):
			dashboardPaths = append(dashboardPaths, r.URL.Path)
			var place struct{ suburb, state, sal, sa3 string }
			for _, candidate := range byCoordinate {
				if strings.HasSuffix(r.URL.Path, "/"+candidate.sal+"/") {
					place = candidate
					break
				}
			}
			body := strings.Replace(dashboardFixture, `"sals":["21640"]`, `"sals":["`+place.sal+`"]`, 1)
			oldLocation := `"sal":"21640","suburb":"Melbourne","state":"VIC"`
			newLocation := `"sal":"` + place.sal + `","suburb":"` + place.suburb + `","state":"` + place.state + `"`
			body = strings.Replace(body, oldLocation, newLocation, 1)
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected AirHealth route %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	f := Fetcher{AirHealth: AirHealth{Base: srv.URL}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := f.Fetch(context.Background(), Sources{
				AirHealth: true, Latitude: tc.lat, Longitude: tc.lon,
			})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got := report.Snapshot.Location; got.SAL != tc.sal || got.Suburb != tc.suburb || got.State != tc.state {
				t.Fatalf("location = %+v, want %s/%s SAL %s", got, tc.suburb, tc.state, tc.sal)
			}
			want := "/dashboard/metricdash/SAL/" + tc.sal + "/"
			found := false
			for _, path := range dashboardPaths {
				if path == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("dashboard routes = %v, missing %q", dashboardPaths, want)
			}
			if _, ok := report.Snapshot.Measure(MeasureGrass); !ok {
				t.Fatal("regional report has no grass pollen category")
			}
		})
	}
}

// AirHealth already reports a category for the location, so the EPA lookup is
// skipped entirely rather than issuing a request whose answer would be ignored.
func TestFetchSkipsEPAWhenAirHealthHasAirQuality(t *testing.T) {
	var paths []string
	ah := airHealthServer(t, &paths)
	defer ah.Close()

	epa := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("EPA queried although AirHealth supplied air quality")
	})
	defer epa.Close()

	f := Fetcher{AirHealth: AirHealth{Base: ah.URL}, EPA: EPAArcGIS{Base: epa.URL}}
	if _, err := f.Fetch(context.Background(), Sources{AirHealth: true, EPA: true}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

// With AirHealth off, air quality comes from EPA and carries EPA's attribution.
func TestFetchUsesEPAWhenAirHealthDisabled(t *testing.T) {
	epa := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(epaFixture))
	})
	defer epa.Close()

	f := Fetcher{EPA: EPAArcGIS{Base: epa.URL}}
	report, err := f.Fetch(context.Background(), Sources{EPA: true, Latitude: -37.8136, Longitude: 144.9631})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m, ok := report.Snapshot.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("air quality missing")
	}
	if m.Source != "epa" {
		t.Errorf("source = %q, want epa", m.Source)
	}
	if !strings.Contains(report.Snapshot.Attribution, "EPA Victoria") {
		t.Errorf("attribution = %q", report.Snapshot.Attribution)
	}
	if report.Snapshot.Location.Suburb != "Melbourne CBD" {
		t.Errorf("suburb = %q, want the station name", report.Snapshot.Location.Suburb)
	}
}

// When AirHealth is enabled but its dashboard has no air quality row, EPA
// fills the gap and both attributions appear.
func TestFetchFallsBackToEPAForSparseAirHealth(t *testing.T) {
	sparse := strings.Replace(dashboardFixture,
		`{"name":"Air Quality","text":"Good","value":1,"colour":"#ffffff","background_colour":"#4bd964","image":"https://app-api.airhealthservices.au/media/resourceimg/Air_Quality_1.svg","show_metric":{"subcat":"PM2.5","value":0.0,"monitor_is_close":true,"units":"ug/m3"}},`,
		"", 1)
	ah := mustServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/locations/") {
			_, _ = w.Write([]byte(locationsFixture))
			return
		}
		_, _ = w.Write([]byte(sparse))
	})
	defer ah.Close()
	epa := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(epaFixture))
	})
	defer epa.Close()

	f := Fetcher{AirHealth: AirHealth{Base: ah.URL}, EPA: EPAArcGIS{Base: epa.URL}}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true, EPA: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m, ok := report.Snapshot.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("air quality missing after fallback")
	}
	if m.Source != "epa" {
		t.Errorf("source = %q, want epa", m.Source)
	}
	if !strings.Contains(report.Snapshot.Attribution, "University of Melbourne") ||
		!strings.Contains(report.Snapshot.Attribution, "EPA Victoria") {
		t.Errorf("attribution = %q, want both sources", report.Snapshot.Attribution)
	}
	if _, ok := report.Snapshot.Measure(MeasureGrass); !ok {
		t.Error("grass missing")
	}
}

// A failed source is reported alongside the readings that did arrive, so the
// panel can say which part is old instead of implying all of it is current.
func TestFetchReportsPartialFailure(t *testing.T) {
	ah := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer ah.Close()
	epa := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(epaFixture))
	})
	defer epa.Close()

	f := Fetcher{AirHealth: AirHealth{Base: ah.URL}, EPA: EPAArcGIS{Base: epa.URL}}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true, EPA: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("errors = %v, want one", report.Errors)
	}
	if !strings.Contains(report.Errors[0], "AirHealth pollen") {
		t.Errorf("error = %q, want the failing source named", report.Errors[0])
	}
	if _, ok := report.Snapshot.Measure(MeasureAirQuality); !ok {
		t.Error("air quality missing")
	}
}

func TestFetchFailsWhenEverySourceFails(t *testing.T) {
	bad := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer bad.Close()

	f := Fetcher{AirHealth: AirHealth{Base: bad.URL}, EPA: EPAArcGIS{Base: bad.URL}}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true, EPA: true})
	if err == nil {
		t.Fatal("want an error when every source fails")
	}
	if !report.Stale {
		t.Error("a failed refresh should be marked stale")
	}
	if len(report.Errors) != 2 {
		t.Errorf("errors = %v, want both sources", report.Errors)
	}
}

func TestFetchFailsWithNoSources(t *testing.T) {
	f := Fetcher{}
	if _, err := f.Fetch(context.Background(), Sources{}); err == nil {
		t.Fatal("want an error when no source is enabled")
	}
}

func TestFetchUsesGlobalModelWhenRegionalFeedsHaveNoCoverage(t *testing.T) {
	airHealth := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"locations":[]}`))
	})
	defer airHealth.Close()
	epa := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"features":[]}`))
	})
	defer epa.Close()
	model := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"current":{"time":"2026-11-01T12:00","pm2_5":6.2,"us_aqi":28,"uv_index":3.5},"daily":{"time":["2026-11-01"],"uv_index_max":[4.0]}}`))
	})
	defer model.Close()

	f := Fetcher{
		AirHealth: AirHealth{Base: airHealth.URL},
		EPA:       EPAArcGIS{Base: epa.URL},
		OpenMeteo: OpenMeteo{Base: model.URL},
	}
	report, err := f.Fetch(context.Background(), Sources{
		AirHealth: true, EPA: true, OpenMeteo: true,
		Latitude: 51.5072, Longitude: -0.1276, RadiusKM: 15,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("regional coverage gaps reported as failures: %v", report.Errors)
	}
	if air, ok := report.Snapshot.Measure(MeasureAirQuality); !ok || air.Source != "open-meteo" {
		t.Fatalf("air quality = %+v, want global model data", air)
	}
	if _, ok := report.Snapshot.Measure(MeasureUV); !ok {
		t.Fatal("global UV reading missing")
	}
}

func TestFetchOrdersMeasures(t *testing.T) {
	var paths []string
	srv := airHealthServer(t, &paths)
	defer srv.Close()

	f := Fetcher{AirHealth: AirHealth{Base: srv.URL}}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	var got []string
	for _, m := range report.Snapshot.Measures {
		got = append(got, m.Name)
	}
	want := []string{MeasureGrass, MeasureAllergens, MeasureAirQuality, MeasureThunderstorm}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// A configured station beats proximity, which is the whole point of the
// setting: the user's local station is more representative than a closer
// roadside sensor.
func TestFetchPrefersConfiguredSite(t *testing.T) {
	epa := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(epaFixture))
	})
	defer epa.Close()

	f := Fetcher{EPA: EPAArcGIS{Base: epa.URL}}
	report, err := f.Fetch(context.Background(), Sources{
		EPA: true, Latitude: -37.8136, Longitude: 144.9631, Site: "Spotswood",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m, ok := report.Snapshot.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("air quality missing")
	}
	if m.Metric == nil || m.Metric.Value != 4.08 {
		t.Errorf("metric = %+v, want the Spotswood reading", m.Metric)
	}
}

func TestCacheKeepsLastReportOnFailure(t *testing.T) {
	cache := &Cache{}
	if _, ok := cache.Keep([]string{"boom"}); ok {
		t.Error("an empty cache should not serve a report")
	}
	cache.Put(Report{Snapshot: Snapshot{Measures: []Measure{{Name: MeasureGrass}}}})

	stale, ok := cache.Keep([]string{"Melbourne Pollen: timeout"})
	if !ok {
		t.Fatal("cache should serve the last report")
	}
	if !stale.Stale {
		t.Error("served report should be marked stale")
	}
	if len(stale.Errors) != 1 {
		t.Errorf("errors = %v", stale.Errors)
	}
	// A subsequent successful refresh clears the stale mark.
	cache.Put(Report{Snapshot: Snapshot{Measures: []Measure{{Name: MeasureGrass}}}})
	if got, _ := cache.Get(); got.Stale {
		t.Error("storing again should clear stale")
	}
}

func TestCacheRestoreMarksStale(t *testing.T) {
	cache := &Cache{}
	cache.Restore(Report{Snapshot: Snapshot{Measures: []Measure{{Name: MeasureGrass}}}})
	got, ok := cache.Get()
	if !ok || !got.Stale {
		t.Errorf("restored report = %+v, ok=%v", got, ok)
	}
}

func TestEncodeDecodeReportRoundTrips(t *testing.T) {
	original := Report{
		UpdatedAt: time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC),
		Snapshot: Snapshot{
			Location:    Location{Suburb: "Melbourne", State: "VIC", Postcode: "3000", SAL: "21640", SA3: "20604"},
			Date:        "2026-10-31",
			Title:       "Later today",
			Advice:      "Take care",
			Attribution: AirHealthAttribution,
			Measures: []Measure{
				{Name: MeasureGrass, Title: Title(MeasureGrass), Level: Level{Name: "High", Index: 2, Severity: SeverityHigh}, Source: "airhealth",
					Bands: []Band{{Label: "Low", Until: .25, Severity: SeverityGood}, {Label: "Moderate", Until: .5, Severity: SeverityModerate}, {Label: "High", Until: .75, Severity: SeverityHigh}, {Label: "Extreme", Until: 1, Severity: SeverityHigh}}},
				{Name: MeasureAirQuality, Title: Title(MeasureAirQuality), Level: Level{Name: "Good", Index: 1, Severity: SeverityGood},
					Metric: &Metric{Subcat: "PM2.5", Value: 7.5, Units: "ug/m3"}, Source: "epa",
					Breakdown: []Breakdown{{Name: "Alternaria", Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}}}},
			},
			Alert: &Alert{Title: "Thunderstorm Asthma", Text: "Take care.", Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}},
			Forecast: []Day{{
				Date: "2026-11-01", Title: "Sunday, 1 Nov 2026", MaxC: 19, MinC: 13,
				Measures: map[string]Measure{
					MeasureGrass: {Name: MeasureGrass, Level: Level{Name: "Moderate", Index: 1, Severity: SeverityModerate}},
				},
			}},
		},
	}
	encoded, err := EncodeReport(original)
	if err != nil {
		t.Fatalf("EncodeReport: %v", err)
	}
	restored, ok := DecodeReport(encoded)
	if !ok {
		t.Fatal("DecodeReport rejected its own output")
	}
	if !restored.Stale {
		t.Error("a decoded report is a cache and should be stale")
	}
	if restored.Snapshot.Location.Suburb != "Melbourne" || restored.Snapshot.Location.SAL != "21640" {
		t.Errorf("location = %+v", restored.Snapshot.Location)
	}
	if len(restored.Snapshot.Measures) != 2 {
		t.Fatalf("measures = %d", len(restored.Snapshot.Measures))
	}
	grass, _ := restored.Snapshot.Measure(MeasureGrass)
	if len(grass.Bands) != 4 || grass.Bands[3].Label != "Extreme" {
		t.Errorf("restored grass bands = %+v, want four labelled bands", grass.Bands)
	}
	air, _ := restored.Snapshot.Measure(MeasureAirQuality)
	if len(air.Breakdown) != 1 || air.Breakdown[0].Name != "Alternaria" {
		t.Errorf("restored air breakdown = %+v", air.Breakdown)
	}
	epaMeasure := restored.Snapshot.Measures[1]
	if epaMeasure.Metric == nil || epaMeasure.Metric.Value != 7.5 {
		t.Errorf("metric = %+v", epaMeasure.Metric)
	}
	if restored.Snapshot.Alert == nil || restored.Snapshot.Alert.Level.Severity != SeverityModerate {
		t.Errorf("alert = %+v", restored.Snapshot.Alert)
	}
	day := restored.Snapshot.Forecast[0]
	if got, ok := day.Measures[MeasureGrass]; !ok || got.Level.Name != "Moderate" {
		t.Errorf("forecast = %+v", day.Measures)
	}
}

func TestDecodeReportRejectsBadInput(t *testing.T) {
	for _, raw := range []string{
		`not json`,
		`{"version":999,"measures":[{"name":"Grass"}]}`,
		`{"version":1,"measures":[]}`,
		`{}`,
	} {
		if _, ok := DecodeReport([]byte(raw)); ok {
			t.Errorf("DecodeReport(%s) should fail", raw)
		}
	}
}

func TestShortErrorTrimsSourcePrefix(t *testing.T) {
	err := errors.New("airhealth /dashboard/metricdash/SAL/21640/: 503 Service Unavailable")
	if got := shortError(err); got != "503 Service Unavailable" {
		t.Errorf("shortError = %q", got)
	}
	long := errors.New(strings.Repeat("x", 200))
	if got := shortError(long); len(got) > 60 {
		t.Errorf("shortError = %q, want a bounded string", got)
	}
}

func mustServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestOpenMeteoSuppliesUVWhenOthersCannot(t *testing.T) {
	epa := epaServer(t, epaFixture)
	f := Fetcher{
		EPA:       EPAArcGIS{Base: epa.URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
	}
	report, err := f.Fetch(context.Background(), Sources{
		EPA: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	uv, ok := report.Snapshot.Measure(MeasureUV)
	if !ok {
		t.Fatal("UV missing: Open-Meteo is the only source that publishes it")
	}
	if uv.Level.Name != uvModerate {
		t.Errorf("UV = %q, want Moderate", uv.Level.Name)
	}

	// EPA's station category must survive as the authoritative air quality
	// reading, not be replaced by the model's AQI.
	air, ok := report.Snapshot.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("air quality missing")
	}
	if air.Source != "epa" {
		t.Errorf("air source = %q, want epa", air.Source)
	}
	// But the model's gases are attached, because EPA's mirror carries none.
	if !contains(breakdownNames(air.Breakdown), "NO2") {
		t.Errorf("breakdown = %v, want the model's gaseous pollutants", breakdownNames(air.Breakdown))
	}

	for _, want := range []string{EPAAttribution, OpenMeteoAttribution} {
		if !containsString(report.Snapshot.Attribution, want) {
			t.Errorf("attribution = %q, missing %q", report.Snapshot.Attribution, want)
		}
	}
}

func TestOpenMeteoFillsAirQualityWhenEPAFails(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()

	f := Fetcher{
		EPA:       EPAArcGIS{Base: down.URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
	}
	report, err := f.Fetch(context.Background(), Sources{
		EPA: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	air, ok := report.Snapshot.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("no air quality fallback from the model")
	}
	if air.Source != "open-meteo" {
		t.Errorf("air source = %q, want open-meteo", air.Source)
	}
	// The failure is still reported so the panel can say which part is stale.
	if len(report.Errors) == 0 {
		t.Error("EPA failure was swallowed")
	}
}

func TestAirHealthAirQualityWinsOverModel(t *testing.T) {
	air := airHealthServer(t, new([]string))
	f := Fetcher{
		AirHealth: AirHealth{Base: air.URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
	}
	report, err := f.Fetch(context.Background(), Sources{
		AirHealth: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	airMeasure, _ := report.Snapshot.Measure(MeasureAirQuality)
	if airMeasure.Source != "airhealth" {
		t.Errorf("air source = %q, want airhealth", airMeasure.Source)
	}
	// The detail endpoint's constituents win; the model only adds the gaps.
	names := breakdownNames(airMeasure.Breakdown)
	if !contains(names, "PM10") || !contains(names, "NO2") {
		t.Errorf("breakdown = %v, want both sources' constituents", names)
	}
	// No duplicates when both sources name the same pollutant.
	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("%q appears %d times in the breakdown", name, count)
		}
	}
}

func TestMergeMeasuresIsIdempotentForAttribution(t *testing.T) {
	if got := joinAttribution(EPAAttribution, EPAAttribution); got != EPAAttribution {
		t.Errorf("duplicate attribution = %q", got)
	}
	if got := joinAttribution("", OpenMeteoAttribution); got != OpenMeteoAttribution {
		t.Errorf("first attribution = %q", got)
	}
	if got := joinAttribution(EPAAttribution, OpenMeteoAttribution); !containsString(got, " · ") {
		t.Errorf("joined attribution = %q", got)
	}
}

// bodyServer serves one fixed body for every request, which is enough for a
// provider whose single endpoint is all the test exercises.
func bodyServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// TestAirQualityBreakdownAgreesWithHeadline guards against the panel showing a
// headline value that contradicts its own breakdown. EPA's 19.9 µg/m³ and the
// model's 4.8 µg/m³ are both correct for their own source; putting them on the
// same row without reconciling them reads as a bug in the reading.
func TestAirQualityBreakdownAgreesWithHeadline(t *testing.T) {
	f := Fetcher{
		EPA:       EPAArcGIS{Base: epaServer(t, epaFixture).URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
	}
	report, err := f.Fetch(context.Background(), Sources{
		EPA: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	air, ok := report.Snapshot.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("no air quality measure")
	}
	if air.Metric == nil {
		t.Fatal("air quality lost its headline value")
	}
	// The headline pollutant must appear in the breakdown carrying the same
	// number as the headline.
	var headline *Breakdown
	for i, part := range air.Breakdown {
		if part.Name == air.Metric.Subcat {
			headline = &air.Breakdown[i]
			break
		}
	}
	if headline == nil {
		t.Fatalf("headline pollutant %q is absent from breakdown %v",
			air.Metric.Subcat, breakdownNames(air.Breakdown))
	}
	if headline.Metric == nil || headline.Metric.Value != air.Metric.Value {
		t.Errorf("%s in breakdown = %+v, headline = %v",
			air.Metric.Subcat, headline.Metric, air.Metric.Value)
	}
	// And it leads, so the reader sees the number they were shown first.
	if air.Breakdown[0].Name != air.Metric.Subcat {
		t.Errorf("breakdown starts with %q, want the headline %q",
			air.Breakdown[0].Name, air.Metric.Subcat)
	}
}
