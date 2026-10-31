package airquality

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEPANearestSortsByDistance(t *testing.T) {
	srv := epaServer(t, epaFixture)
	defer srv.Close()

	sites, err := EPAArcGIS{Base: srv.URL}.Nearest(context.Background(), -37.8136, 144.9631, 8)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(sites) != 4 {
		t.Fatalf("sites = %d, want 4", len(sites))
	}
	if sites[0].Name != "Melbourne CBD" {
		t.Errorf("nearest = %q, want Melbourne CBD", sites[0].Name)
	}
	if sites[0].DistanceKM > 1.5 {
		t.Errorf("distance = %.2f km, want about 0.9", sites[0].DistanceKM)
	}
	for i := 1; i < len(sites); i++ {
		if sites[i].DistanceKM < sites[i-1].DistanceKM {
			t.Errorf("site %d (%s) is closer than its predecessor", i, sites[i].Name)
		}
	}
}

// An indicative sensor sitting beside a reference-grade station must not win:
// the standard station is the honest number for the same air.
func TestEPANearestPrefersStandardStation(t *testing.T) {
	body := `{"features":[
	 {"attributes":{"siteName":"Sensor","siteType":"Sensor","healthParameter":"Particles","healthAdvice":"Good","averageValue":6.58,"since":1764910800000,"until":1764914400000},
	  "geometry":{"x":144.9640,"y":-37.8136}},
	 {"attributes":{"siteName":"Standard","siteType":"Standard","healthParameter":"PM2.5","healthAdvice":"Fair","averageValue":19.9,"since":1764910800000,"until":1764914400000},
	  "geometry":{"x":144.9660,"y":-37.8140}}
	]}`
	srv := epaServer(t, body)
	defer srv.Close()

	sites, err := EPAArcGIS{Base: srv.URL}.Nearest(context.Background(), -37.8136, 144.9631, 8)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if sites[0].Name != "Standard" {
		t.Errorf("nearest = %q, want the standard station", sites[0].Name)
	}
}

func TestEPANearestSendsLongitudeAsX(t *testing.T) {
	var geometry string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geometry = r.URL.Query().Get("geometry")
		_, _ = w.Write([]byte(epaFixture))
	}))
	defer srv.Close()

	if _, err := (EPAArcGIS{Base: srv.URL}).Nearest(context.Background(), -37.8136, 144.9631, 8); err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	// The layer is in WGS84, so x is longitude and y is latitude.
	if geometry != `{"x":144.9631,"y":-37.8136}` {
		t.Errorf("geometry = %q", geometry)
	}
}

func TestEPANearestConvertsDistanceToMetres(t *testing.T) {
	var distance, units string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		distance = r.URL.Query().Get("distance")
		units = r.URL.Query().Get("units")
		_, _ = w.Write([]byte(epaFixture))
	}))
	defer srv.Close()

	if _, err := (EPAArcGIS{Base: srv.URL}).Nearest(context.Background(), -37.8136, 144.9631, 8); err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if distance != "8000" {
		t.Errorf("distance = %q, want 8000", distance)
	}
	if units != "esriSRUnit_Meter" {
		t.Errorf("units = %q", units)
	}
}

func TestEPANearestReportsNoStations(t *testing.T) {
	srv := epaServer(t, `{"features":[]}`)
	defer srv.Close()

	_, err := EPAArcGIS{Base: srv.URL}.Nearest(context.Background(), -37.8136, 144.9631, 8)
	if !errors.Is(err, ErrNoCoverage) {
		t.Errorf("err = %v, want ErrNoCoverage", err)
	}
}

func TestEPANearestReportsServerErrorUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := EPAArcGIS{Base: srv.URL}.Nearest(context.Background(), -37.8136, 144.9631, 8)
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestEPANearestReportsServiceError(t *testing.T) {
	srv := epaServer(t, `{"error":{"message":"Invalid geometry"}}`)
	defer srv.Close()

	_, err := EPAArcGIS{Base: srv.URL}.Nearest(context.Background(), -37.8136, 144.9631, 8)
	if err == nil || !strings.Contains(err.Error(), "Invalid geometry") {
		t.Errorf("err = %v, want the service message", err)
	}
}

// The non-spatial response omits geometry. Those rows carry no distance, so
// they must sort behind every located station rather than ahead of it.
func TestParseEPASitesHandlesMissingGeometry(t *testing.T) {
	body := `{"features":[
	 {"attributes":{"siteName":"NoCoords","siteType":"Standard","healthParameter":"PM2.5","healthAdvice":"Good","averageValue":3.1}},
	 {"attributes":{"siteName":"Located","siteType":"Standard","healthParameter":"PM2.5","healthAdvice":"Fair","averageValue":19.9},
	  "geometry":{"x":144.97158,"y":-37.825023}}
	]}`
	sites, err := parseEPASites([]byte(body), -37.8136, 144.9631)
	if err != nil {
		t.Fatalf("parseEPASites: %v", err)
	}
	if sites[0].Name != "Located" {
		t.Errorf("first = %q, want the located station", sites[0].Name)
	}
}

func TestEPASiteAirQualityMeasure(t *testing.T) {
	site := EPASite{
		Name: "Melbourne CBD", Type: "Standard", Parameter: "PM2.5",
		Advice: "Fair", Value: 19.9,
		Until: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC),
	}
	m, ok := site.AirQualityMeasure()
	if !ok {
		t.Fatal("no measure")
	}
	if m.Name != MeasureAirQuality || m.Source != "epa" {
		t.Errorf("measure = %+v", m)
	}
	if m.Level.Name != "Fair" || m.Level.Severity != SeverityModerate {
		t.Errorf("level = %+v", m.Level)
	}
	if m.Metric == nil || m.Metric.Value != 19.9 || m.Metric.Subcat != "PM2.5" {
		t.Errorf("metric = %+v", m.Metric)
	}
}

func TestEPASiteAirQualityMeasureSkipsEmptyAdvice(t *testing.T) {
	if _, ok := (EPASite{Name: "Blank"}).AirQualityMeasure(); ok {
		t.Error("a station with no category should not produce a measure")
	}
}

// EPA uses the same six category names as the AirHealth air quality scale, so
// severity must follow that scale rather than the argv order of the names.
func TestEPASiteAirQualitySeverityFollowsCategory(t *testing.T) {
	for advice, want := range map[string]Severity{
		"Very Good": SeverityGood, "Good": SeverityGood,
		"Fair": SeverityModerate, "Poor": SeverityModerate,
		"Very Poor": SeverityHigh, "Extremely Poor": SeverityHigh,
	} {
		m, ok := EPASite{Advice: advice, Parameter: "PM2.5"}.AirQualityMeasure()
		if !ok {
			t.Fatalf("%s: no measure", advice)
		}
		if m.Level.Severity != want {
			t.Errorf("%s severity = %v, want %v", advice, m.Level.Severity, want)
		}
	}
}

func TestEPASiteUnmatchedCategoryFailsSafe(t *testing.T) {
	m, ok := EPASite{Advice: "New Risk Category", Parameter: "PM2.5"}.AirQualityMeasure()
	if !ok {
		t.Fatal("no measure")
	}
	if m.Level.Severity != SeverityHigh {
		t.Fatalf("unmatched category severity = %v, want high until the published scale is updated", m.Level.Severity)
	}
}

func TestHaversineKM(t *testing.T) {
	// Melbourne CBD to Geelong is about 65 km.
	if got := HaversineKM(-37.8136, 144.9631, -38.1499, 144.3617); got < 60 || got > 75 {
		t.Errorf("distance = %.1f km, want about 65", got)
	}
	if got := HaversineKM(-37.8136, 144.9631, -37.8136, 144.9631); got != 0 {
		t.Errorf("same point = %v, want 0", got)
	}
}

func epaServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

// Trimmed from the live layer captured on 2026-10-31, keeping the fields and
// coordinates the sorting rules depend on.
const epaFixture = `{"features":[
 {"attributes":{"siteID":"1023","siteName":"Melbourne CBD","siteType":"Standard","healthParameter":"PM2.5","healthAdvice":"Fair","averageValue":19.9,"since":1764910800000,"until":1764914400000},"geometry":{"x":144.97158,"y":-37.825023}},
 {"attributes":{"siteID":"1011","siteName":"Alphington","siteType":"Standard","healthParameter":"PM2.5","healthAdvice":"Fair","averageValue":20.75,"since":1764910800000,"until":1764914400000},"geometry":{"x":145.031,"y":-37.787}},
 {"attributes":{"siteID":"1055","siteName":"Spotswood","siteType":"Standard","healthParameter":"PM2.5","healthAdvice":"Good","averageValue":4.08,"since":1764910800000,"until":1764914400000},"geometry":{"x":144.887,"y":-37.829}},
 {"attributes":{"siteID":"1077","siteName":"Footscray","siteType":"Standard","healthParameter":"PM10","healthAdvice":"Good","averageValue":12.64,"since":1764910800000,"until":1764914400000},"geometry":{"x":144.9,"y":-37.8}}
]}`
