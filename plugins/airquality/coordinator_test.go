package airquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

// A failed source is reported alongside the readings that did arrive, so the
// panel can say which part is old instead of implying all of it is current.
func TestFetchReportsPartialFailure(t *testing.T) {
	ah := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer ah.Close()
	model := bodyServer(t, openMeteoFixture)
	defer model.Close()

	f := Fetcher{AirHealth: AirHealth{Base: ah.URL}, OpenMeteo: OpenMeteo{Base: model.URL}, Now: fixtureNow}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631})
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

	f := Fetcher{AirHealth: AirHealth{Base: bad.URL}, OpenMeteo: OpenMeteo{Base: bad.URL}}
	report, err := f.Fetch(context.Background(), Sources{AirHealth: true, OpenMeteo: true})
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
	model := mustServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"current":{"time":"2026-11-01T12:00","pm2_5":6.2,"us_aqi":28,"uv_index":3.5},"daily":{"time":["2026-11-01"],"uv_index_max":[4.0]}}`))
	})
	defer model.Close()

	f := Fetcher{
		AirHealth: AirHealth{Base: airHealth.URL},
		OpenMeteo: OpenMeteo{Base: model.URL},
	}
	report, err := f.Fetch(context.Background(), Sources{
		AirHealth: true, OpenMeteo: true,
		Latitude: 51.5072, Longitude: -0.1276,
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
					Metric: &Metric{Subcat: "PM2.5", Value: 7.5, Units: "ug/m3"}, Source: "airhealth",
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
	if air.Metric == nil || air.Metric.Value != 7.5 {
		t.Errorf("metric = %+v", air.Metric)
	}
	if restored.Snapshot.Alert == nil || restored.Snapshot.Alert.Level.Severity != SeverityModerate {
		t.Errorf("alert = %+v", restored.Snapshot.Alert)
	}
	day := restored.Snapshot.Forecast[0]
	if got, ok := day.Measures[MeasureGrass]; !ok || got.Level.Name != "Moderate" {
		t.Errorf("forecast = %+v", day.Measures)
	}
}

// EPA Victoria rows that earlier releases saved are dropped on restore, and a
// report holding nothing else is rejected so the plugin refetches.
func TestDecodeReportDropsRemovedEPARows(t *testing.T) {
	mixed := `{"version":1,"updated_unix":1793433600,"measures":[` +
		`{"name":"Air Quality","level":"Fair","index":2,"severity":1,"source":"epa"},` +
		`{"name":"Grass","level":"Low","index":0,"severity":0,"source":"airhealth"}]}`
	report, ok := DecodeReport([]byte(mixed))
	if !ok {
		t.Fatal("a report with a current row was rejected")
	}
	if _, has := report.Snapshot.Measure(MeasureAirQuality); has {
		t.Errorf("the saved EPA row survived: %+v", report.Snapshot.Measures)
	}
	if _, has := report.Snapshot.Measure(MeasureGrass); !has {
		t.Error("the AirHealth row was dropped with it")
	}
	only := `{"version":1,"updated_unix":1793433600,"measures":[{"name":"Air Quality","level":"Fair","source":"epa"}]}`
	if _, ok := DecodeReport([]byte(only)); ok {
		t.Error("a report of EPA rows alone was restored")
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
	f := Fetcher{
		AirHealth: AirHealth{Base: airHealthServer(t, new([]string)).URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
		Now:       fixtureNow,
	}
	report, err := f.Fetch(context.Background(), Sources{
		AirHealth: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
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

	for _, want := range []string{AirHealthAttribution, OpenMeteoAttribution} {
		if !containsString(report.Snapshot.Attribution, want) {
			t.Errorf("attribution = %q, missing %q", report.Snapshot.Attribution, want)
		}
	}
}

func TestOpenMeteoFillsAirQualityWhenAirHealthFails(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()

	f := Fetcher{
		AirHealth: AirHealth{Base: down.URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
		Now:       fixtureNow,
	}
	report, err := f.Fetch(context.Background(), Sources{
		AirHealth: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
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
		t.Error("AirHealth failure was swallowed")
	}
}

func TestAirHealthAirQualityWinsOverModel(t *testing.T) {
	air := airHealthServer(t, new([]string))
	f := Fetcher{
		AirHealth: AirHealth{Base: air.URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
		Now:       fixtureNow,
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
	if got := joinAttribution(AirHealthAttribution, AirHealthAttribution); got != AirHealthAttribution {
		t.Errorf("duplicate attribution = %q", got)
	}
	if got := joinAttribution("", OpenMeteoAttribution); got != OpenMeteoAttribution {
		t.Errorf("first attribution = %q", got)
	}
	if got := joinAttribution(AirHealthAttribution, OpenMeteoAttribution); !containsString(got, " · ") {
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
// headline value that contradicts its own breakdown. AirHealth's station PM2.5
// and the model's PM2.5 are both correct for their own source; putting them on
// the same row without reconciling them reads as a bug in the reading.
func TestAirQualityBreakdownAgreesWithHeadline(t *testing.T) {
	f := Fetcher{
		AirHealth: AirHealth{Base: airHealthServer(t, new([]string)).URL},
		OpenMeteo: OpenMeteo{Base: bodyServer(t, openMeteoFixture).URL},
		Now:       fixtureNow,
	}
	report, err := f.Fetch(context.Background(), Sources{
		AirHealth: true, OpenMeteo: true, Latitude: -37.8136, Longitude: 144.9631,
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

func row(name, source string, updated time.Time) Measure {
	return Measure{Name: name, Source: source, Updated: updated, Level: Level{Name: "High", Severity: SeverityHigh}}
}

// A partial refresh replaces the rows its sources returned and carries the
// failed source's recent rows, with the report dated by the oldest of them.
func TestCacheMergeCarriesRecentRowsFromFailedSource(t *testing.T) {
	melbourne := &Coordinates{Latitude: DefaultLatitude, Longitude: DefaultLongitude}
	earlier := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	now := earlier.Add(3 * time.Hour)
	forecast := []Day{{Date: "2026-11-01", Measures: map[string]Measure{}}}
	cache := &Cache{}
	cache.Put(Report{Origin: melbourne, UpdatedAt: earlier, Snapshot: Snapshot{
		Location:    Location{Suburb: "Melbourne", State: "VIC", SAL: "21640"},
		Forecast:    forecast,
		Attribution: AirHealthAttribution + " · " + OpenMeteoAttribution,
		Measures:    []Measure{row(MeasureGrass, "airhealth", earlier), row(MeasureUV, "open-meteo", earlier)},
	}})

	fresh := Report{Origin: melbourne, UpdatedAt: now, Errors: []string{"AirHealth pollen: down"}, Snapshot: Snapshot{
		Location:    Location{Suburb: "-37.81, 144.96"},
		Attribution: OpenMeteoAttribution,
		Measures:    []Measure{row(MeasureUV, "open-meteo", now)},
	}}
	merged := cache.Merge(fresh)
	if uv, _ := merged.Snapshot.Measure(MeasureUV); !uv.Updated.Equal(now) {
		t.Errorf("UV row was not replaced by the fresh reading: %v", uv.Updated)
	}
	if _, ok := merged.Snapshot.Measure(MeasureGrass); !ok {
		t.Fatal("failed source's recent grass row was dropped")
	}
	if !merged.Stale || !merged.UpdatedAt.Equal(earlier) {
		t.Errorf("merged stale=%v updated=%v, want stale and dated by the carried row", merged.Stale, merged.UpdatedAt)
	}
	if merged.Snapshot.Location.Suburb != "Melbourne" || len(merged.Snapshot.Forecast) != 1 {
		t.Errorf("AirHealth context not carried: %+v", merged.Snapshot)
	}
	if !strings.Contains(merged.Snapshot.Attribution, AirHealthAttribution) {
		t.Errorf("carried rows lost their credit: %q", merged.Snapshot.Attribution)
	}

	// Past the carry limit the old row goes, and the report is current again.
	later := fresh
	later.UpdatedAt = earlier.Add(CarryLimit + time.Minute)
	later.Snapshot.Measures = []Measure{row(MeasureUV, "open-meteo", later.UpdatedAt)}
	merged = cache.Merge(later)
	if _, ok := merged.Snapshot.Measure(MeasureGrass); ok {
		t.Fatal("a row older than the carry limit was still shown")
	}
	if merged.Stale {
		t.Error("a report carrying nothing was marked stale")
	}
}

// Rows from another place are never carried, and relocating clears them.
func TestCacheNeverMixesPlaces(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	melbourne := &Coordinates{Latitude: DefaultLatitude, Longitude: DefaultLongitude}
	london := &Coordinates{Latitude: 51.5072, Longitude: -0.1276}
	cache := &Cache{}
	cache.Put(Report{Origin: melbourne, UpdatedAt: now, Snapshot: Snapshot{Measures: []Measure{row(MeasureGrass, "airhealth", now)}}})
	merged := cache.Merge(Report{Origin: london, UpdatedAt: now, Errors: []string{"x"}, Snapshot: Snapshot{
		Measures: []Measure{row(MeasureUV, "open-meteo", now)},
	}})
	if _, ok := merged.Snapshot.Measure(MeasureGrass); ok {
		t.Fatal("Melbourne grass was carried into a London report")
	}

	cache.Relocate(*london)
	if _, ok := cache.Get(); !ok {
		t.Fatal("relocating to the cached place dropped its report")
	}
	cache.Relocate(*melbourne)
	if _, ok := cache.Get(); ok {
		t.Fatal("relocating kept another place's report")
	}
	cache.Restore(Report{Snapshot: Snapshot{Measures: []Measure{row(MeasureGrass, "airhealth", now)}}})
	cache.Relocate(*melbourne)
	if _, ok := cache.Get(); ok {
		t.Fatal("a report with no recorded origin survived relocation")
	}
}

// The origin and per-row times survive a restart; an older save without them
// still decodes, dating its rows from the report.
func TestEncodeDecodeKeepsOriginAndRowTimes(t *testing.T) {
	updated := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	rowTime := updated.Add(-2 * time.Hour)
	raw, err := EncodeReport(Report{UpdatedAt: updated, Origin: &Coordinates{Latitude: -33.8688, Longitude: 151.2093},
		Snapshot: Snapshot{Measures: []Measure{row(MeasureGrass, "airhealth", rowTime)}}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := DecodeReport(raw)
	if !ok || got.Origin == nil || !got.Origin.Same(Coordinates{Latitude: -33.8688, Longitude: 151.2093}) {
		t.Fatalf("origin lost: %+v ok=%v", got.Origin, ok)
	}
	if !got.Snapshot.Measures[0].Updated.Equal(rowTime) {
		t.Errorf("row time = %v, want %v", got.Snapshot.Measures[0].Updated, rowTime)
	}

	legacy := `{"version":1,"updated_unix":` + strconv.FormatInt(updated.Unix(), 10) + `,"measures":[{"name":"Grass","level":"High","severity":2}]}`
	got, ok = DecodeReport([]byte(legacy))
	if !ok || got.Origin != nil || !got.Snapshot.Measures[0].Updated.Equal(updated) {
		t.Fatalf("legacy report = %+v ok=%v", got, ok)
	}
}

// Transport and status failures read as what they mean, not as Go errors.
func TestShortErrorUsesPlainWords(t *testing.T) {
	resp := &http.Response{StatusCode: 503, Status: "503 Service Unavailable"}
	if got := shortError(statusError("airhealth", resp)); got != "service unavailable (HTTP 503)" {
		t.Errorf("503 = %q", got)
	}
	if got := shortError(statusError("airhealth", &http.Response{StatusCode: 404, Status: "404 Not Found"})); got != "request refused (HTTP 404)" {
		t.Errorf("404 = %q", got)
	}
	dns := &url.Error{Op: "Get", URL: "https://x", Err: &net.DNSError{Err: "no such host", Name: "x"}}
	if got := shortError(dns); got != "can't connect; check the network" {
		t.Errorf("dns = %q", got)
	}
	if got := shortError(fmt.Errorf("fetch: %w", context.DeadlineExceeded)); got != "timed out" {
		t.Errorf("deadline = %q", got)
	}
}

// fixtureNow is a fixed time on the fixtures' day.
func fixtureNow() time.Time { return time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC) }
