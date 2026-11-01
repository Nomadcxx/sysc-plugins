package airquality

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// europePollenBody is two local days of hourly CAMS pollen, shaped like an
// Open-Meteo answer for London: grass averages 35 (Moderate) today, birch 400
// (Very High) and mugwort 2 (Low); tomorrow everything is quiet.
const europePollenBody = `{
 "current":{"pm2_5":4.3,"us_aqi":28},
 "daily":{"time":["2026-05-20"],"uv_index_max":[5.1]},
 "hourly":{
  "time":["2026-05-20T00:00","2026-05-20T12:00","2026-05-21T00:00","2026-05-21T12:00"],
  "grass_pollen":[30,40,0,0.5],
  "birch_pollen":[400,400,null,null],
  "alder_pollen":[0,0,0,0],
  "olive_pollen":[null,null,null,null],
  "mugwort_pollen":[2,2,0,0],
  "ragweed_pollen":[0,0,0,0]
 }
}`

const usPollenBody = `{"current":{"us_aqi":40},"hourly":{"time":["2026-05-20T00:00"],"grass_pollen":[null],"birch_pollen":[null]}}`

func TestParseOpenMeteoPollenUsesDailyMeansAndLoadClasses(t *testing.T) {
	today, forecast, err := ParseOpenMeteoPollen([]byte(europePollenBody))
	if err != nil {
		t.Fatal(err)
	}
	grass, ok := Snapshot{Measures: today}.Measure(MeasureGrass)
	if !ok || grass.Level.Name != "Moderate" || grass.Metric.Value != 35 || grass.Metric.Units != pollenUnits || grass.Max <= 0 {
		t.Fatalf("grass = %+v metric %+v", grass, grass.Metric)
	}
	allergens, ok := Snapshot{Measures: today}.Measure(MeasureAllergens)
	if !ok || allergens.Level.Name != "Very High" || allergens.Metric.Subcat != "Birch" || allergens.Level.Severity != SeverityHigh {
		t.Fatalf("allergens = %+v", allergens)
	}
	if len(allergens.Breakdown) != 4 {
		t.Fatalf("allergen breakdown = %+v, want the four species with values (olive is null)", allergens.Breakdown)
	}
	if len(forecast) != 1 || forecast[0].Title != "Thursday" || forecast[0].Measures[MeasureGrass].Level.Name != "None" {
		t.Fatalf("forecast = %+v", forecast)
	}
}

func TestParseOpenMeteoPollenOutsideEuropeIsEmpty(t *testing.T) {
	today, forecast, err := ParseOpenMeteoPollen([]byte(usPollenBody))
	if err != nil || len(today) != 0 || len(forecast) != 0 {
		t.Fatalf("US pollen = %+v %+v %v, want nothing", today, forecast, err)
	}
}

const googlePollenBody = `{"regionCode":"us","dailyInfo":[
 {"date":{"year":2026,"month":5,"day":20},
  "pollenTypeInfo":[
   {"code":"GRASS","displayName":"Grass","inSeason":true,"indexInfo":{"code":"UPI","value":4,"category":"High"}},
   {"code":"TREE","displayName":"Tree","inSeason":true,"indexInfo":{"code":"UPI","value":2,"category":"Low"}},
   {"code":"WEED","displayName":"Weed","inSeason":false}],
  "plantInfo":[
   {"code":"OAK","displayName":"Oak","inSeason":true,"indexInfo":{"code":"UPI","value":2,"category":"Low"}},
   {"code":"RAGWEED","displayName":"Ragweed","inSeason":false},
   {"code":"GRAMINALES","displayName":"Grasses","inSeason":true,"indexInfo":{"code":"UPI","value":4,"category":"High"}}]},
 {"date":{"year":2026,"month":5,"day":21},
  "pollenTypeInfo":[{"code":"GRASS","displayName":"Grass","indexInfo":{"code":"UPI","value":3,"category":"Moderate"}}]}
]}`

func TestGooglePollenSendsKeyInHeaderAndParses(t *testing.T) {
	var gotKey, gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotURL = r.Header.Get("X-Goog-Api-Key"), r.URL.String()
		_, _ = w.Write([]byte(googlePollenBody))
	}))
	defer srv.Close()
	today, forecast, err := GooglePollen{Key: "secret-key", Base: srv.URL}.Fetch(context.Background(), 40.71, -74.01)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "secret-key" || strings.Contains(gotURL, "secret-key") {
		t.Fatalf("key header %q, url %q: the key must travel only in the header", gotKey, gotURL)
	}
	snap := Snapshot{Measures: today}
	grass, _ := snap.Measure(MeasureGrass)
	if grass.Level.Name != "High" || grass.Level.Severity != SeverityHigh || grass.Source != "google" || metricScaleLabel(grass) != "UPI" {
		t.Fatalf("grass = %+v", grass)
	}
	if d := dialFraction(grass); d <= 2.0/3 || d >= 1 {
		t.Fatalf("UPI 4 dial fraction = %v, want inside the High zone", d)
	}
	allergens, _ := snap.Measure(MeasureAllergens)
	if allergens.Level.Name != "Low" || len(allergens.Breakdown) != 2 {
		t.Fatalf("allergens = %+v, want the worse of tree and weed with in-season plants", allergens)
	}
	if len(forecast) != 1 || forecast[0].Measures[MeasureGrass].Level.Name != "Moderate" {
		t.Fatalf("forecast = %+v", forecast)
	}
}

func TestGooglePollenRefusalNeverEchoesTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer srv.Close()
	_, _, err := GooglePollen{Key: "secret-key", Base: srv.URL}.Fetch(context.Background(), 40.71, -74.01)
	if err == nil || strings.Contains(err.Error(), "secret-key") || shortError(err) != "service unavailable (HTTP 403)" && shortError(err) != "request refused (HTTP 403)" {
		t.Fatalf("refusal = %v (%q)", err, shortError(err))
	}

	uncovered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Information is not supported for this location."}}`))
	}))
	defer uncovered.Close()
	if _, _, err := (GooglePollen{Key: "k", Base: uncovered.URL}).Fetch(context.Background(), 0, 0); !errors.Is(err, ErrNoCoverage) {
		t.Fatalf("unsupported region = %v, want no coverage", err)
	}
}

// pollenServer answers Open-Meteo with body and Google with google, counting
// Google requests.
func pollenServer(t *testing.T, body, google string, googleHits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "pollen") {
			*googleHits++
			_, _ = w.Write([]byte(google))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Pollen prefers AirHealth and Open-Meteo; Google fills coverage gaps when
// the user has a key.
func TestFetchPollenPrecedence(t *testing.T) {
	london := Sources{OpenMeteo: true, Latitude: 51.51, Longitude: -0.13, Label: "London"}
	hits := 0
	srv := pollenServer(t, europePollenBody, googlePollenBody, &hits)
	f := Fetcher{OpenMeteo: OpenMeteo{Base: srv.URL + "/air"}, Google: GooglePollen{Base: srv.URL + "/pollen"}, Now: fixtureNow}

	report, err := f.Fetch(context.Background(), london)
	if err != nil {
		t.Fatal(err)
	}
	grass, _ := report.Snapshot.Measure(MeasureGrass)
	if grass.Source != "open-meteo" || hits != 0 || report.Snapshot.Location.Suburb != "London" || report.PollenNote != "" {
		t.Fatalf("Europe without a key: grass %+v, google hits %d, place %q, note %q", grass, hits, report.Snapshot.Location.Suburb, report.PollenNote)
	}
	if len(report.Snapshot.Forecast) != 1 {
		t.Fatalf("European forecast = %+v", report.Snapshot.Forecast)
	}

	london.GoogleKey = "k"
	report, err = f.Fetch(context.Background(), london)
	if err != nil {
		t.Fatal(err)
	}
	grass, _ = report.Snapshot.Measure(MeasureGrass)
	if grass.Source != "open-meteo" || hits != 0 || strings.Contains(report.Snapshot.Attribution, GoogleAttribution) {
		t.Fatalf("Europe with a key: grass %+v, google hits %d, attribution %q", grass, hits, report.Snapshot.Attribution)
	}

	usSrv := pollenServer(t, usPollenBody, googlePollenBody, &hits)
	us := Sources{OpenMeteo: true, Latitude: 40.71, Longitude: -74.01}
	usFetcher := Fetcher{
		OpenMeteo: OpenMeteo{Base: usSrv.URL + "/air"},
		Google:    GooglePollen{Base: usSrv.URL + "/pollen"},
		Now:       fixtureNow,
	}
	report, err = usFetcher.Fetch(context.Background(), us)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := report.Snapshot.Measure(MeasureGrass); ok || !strings.Contains(report.PollenNote, "Google Pollen API key") || hits != 0 {
		t.Fatalf("US without a key: measures %+v, note %q", report.Snapshot.Measures, report.PollenNote)
	}

	us.GoogleKey = "k"
	report, err = usFetcher.Fetch(context.Background(), us)
	if err != nil {
		t.Fatal(err)
	}
	grass, _ = report.Snapshot.Measure(MeasureGrass)
	if grass.Source != "google" || hits != 1 || !strings.Contains(report.Snapshot.Attribution, GoogleAttribution) {
		t.Fatalf("US with a key: grass %+v, google hits %d, attribution %q", grass, hits, report.Snapshot.Attribution)
	}
}

// Google rows are never stored, carried or served stale.
func TestGoogleRowsAreNeverStored(t *testing.T) {
	report := Report{
		UpdatedAt: fixtureNow(), Origin: &Coordinates{Latitude: 40.71, Longitude: -74.01},
		Snapshot: Snapshot{
			Attribution: OpenMeteoAttribution + " · " + GoogleAttribution,
			Measures: []Measure{
				{Name: MeasureGrass, Level: Level{Name: "High", Severity: SeverityHigh}, Source: "google", Updated: fixtureNow()},
				{Name: MeasureUV, Level: Level{Name: "Low"}, Source: "open-meteo", Updated: fixtureNow()},
			},
			Forecast: []Day{{Date: "2026-05-21", Measures: map[string]Measure{MeasureGrass: {Name: MeasureGrass, Level: Level{Name: "High"}, Source: "google"}}}},
		},
	}
	raw, err := EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"google"`) || strings.Contains(string(raw), GoogleAttribution) {
		t.Fatalf("persisted Google data: %s", raw)
	}

	hasGoogle := func(report Report) bool {
		if strings.Contains(report.Snapshot.Attribution, GoogleAttribution) {
			return true
		}
		for _, measure := range report.Snapshot.Measures {
			if measure.Source == "google" {
				return true
			}
		}
		for _, day := range report.Snapshot.Forecast {
			for _, measure := range day.Measures {
				if measure.Source == "google" {
					return true
				}
			}
		}
		return false
	}
	assertNoGoogle := func(name string, report Report) {
		t.Helper()
		if hasGoogle(report) {
			t.Fatalf("%s retained Google data: %+v", name, report.Snapshot)
		}
	}

	var cache Cache
	cache.Put(report)
	stored, _ := cache.Get()
	assertNoGoogle("Put/Get", stored)

	live := cache.Merge(report)
	if _, ok := live.Snapshot.Measure(MeasureGrass); !ok || !strings.Contains(live.Snapshot.Attribution, GoogleAttribution) {
		t.Fatalf("the current live response lost Google pollen: %+v", live.Snapshot)
	}
	stored, _ = cache.Get()
	assertNoGoogle("Merge/Get", stored)

	kept, _ := cache.Keep([]string{"Open-Meteo: timed out"})
	assertNoGoogle("Keep", kept)

	cache.Restore(report)
	stored, _ = cache.Get()
	assertNoGoogle("Restore/Get", stored)
}

func TestEPAOnlyForVictorianCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"Melbourne", -37.8136, 144.9631, true},
		{"Mildura", -34.19, 142.16, true},
		{"Sydney", -33.8688, 151.2093, false},
		{"Adelaide", -34.93, 138.6, false},
		{"Hobart", -42.88, 147.33, false},
		{"Canberra", -35.28, 149.13, false},
		// Albury shares an airshed with Wodonga across the Murray, whose station
		// is the right reading for it; the outline errs outside the border.
		{"Albury", -36.08, 146.92, true},
		{"Wodonga", -36.12, 146.89, true},
		{"Geelong", -38.15, 144.36, true},
		{"Mallacoota", -37.56, 149.75, true},
	} {
		if got := EPAVictoriaMayCoverCoordinates(tc.lat, tc.lon); got != tc.want {
			t.Errorf("%s: EPA = %v, want %v", tc.name, got, tc.want)
		}
	}
}
