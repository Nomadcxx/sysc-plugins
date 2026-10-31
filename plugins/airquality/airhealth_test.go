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

func TestParseDashboardNormalisesMeasures(t *testing.T) {
	snap, err := ParseDashboard([]byte(dashboardFixture))
	if err != nil {
		t.Fatalf("ParseDashboard: %v", err)
	}
	if snap.Location.Suburb != "Melbourne" || snap.Location.SAL != "21640" {
		t.Errorf("location = %+v, want Melbourne/21640", snap.Location)
	}
	if snap.Date != "2026-10-31" || snap.Title != "Later today" {
		t.Errorf("date/title = %q/%q", snap.Date, snap.Title)
	}
	if snap.Attribution != AirHealthAttribution {
		t.Errorf("attribution = %q", snap.Attribution)
	}
	grass, ok := snap.Measure(MeasureGrass)
	if !ok {
		t.Fatal("grass measure missing")
	}
	if grass.Level.Name != "High" || grass.Level.Index != 2 {
		t.Errorf("grass level = %+v", grass.Level)
	}
	if grass.Level.Severity != SeverityHigh {
		t.Errorf("grass severity = %v, want high", grass.Level.Severity)
	}
	if grass.Metric != nil || grass.Max != 0 || len(grass.Bands) != 4 {
		t.Errorf("category-only grass scale = metric %v max %v bands %+v", grass.Metric, grass.Max, grass.Bands)
	}
	aq, ok := snap.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("air quality measure missing")
	}
	if aq.Metric == nil || aq.Metric.Subcat != "PM2.5" || aq.Metric.Value != 0 {
		t.Errorf("air quality metric = %+v", aq.Metric)
	}
	// Air Quality's index counts upward from "Very Good", so index 1 is the
	// second-best band and must not read as a warning.
	if aq.Level.Severity != SeverityGood {
		t.Errorf("air quality severity = %v, want good", aq.Level.Severity)
	}
}

func TestUnmatchedAirHealthCategoryFailsSafe(t *testing.T) {
	got := normaliseMeasure(wireMeasure{Name: "Grass", Text: "New Risk Category", Value: 0}, WireScale)
	if got.Level.Severity != SeverityHigh {
		t.Fatalf("unmatched category severity = %v, want high until the published scale is updated", got.Level.Severity)
	}
}

// The forecast spells thunderstorm asthma differently from the current block.
// A decoder keyed on the raw name drops the forecast row entirely.
func TestParseDashboardCanonicalisesForecastNames(t *testing.T) {
	snap, err := ParseDashboard([]byte(dashboardFixture))
	if err != nil {
		t.Fatalf("ParseDashboard: %v", err)
	}
	if len(snap.Forecast) == 0 {
		t.Fatal("no forecast days")
	}
	first := snap.Forecast[0]
	got, ok := first.Measures[MeasureThunderstorm]
	if !ok {
		t.Fatalf("forecast thunderstorm row missing; got keys %v", dayKeys(first))
	}
	if got.Name != MeasureThunderstorm {
		t.Errorf("forecast measure name = %q", got.Name)
	}
	if got.Level.Name != "Moderate" {
		t.Errorf("forecast thunderstorm level = %q, want Moderate", got.Level.Name)
	}
}

// A day with no air quality forecast must not gain a row defaulting to the
// best category: absent means "not forecast", not "good news".
func TestParseDashboardKeepsForecastSparse(t *testing.T) {
	snap, err := ParseDashboard([]byte(dashboardFixture))
	if err != nil {
		t.Fatalf("ParseDashboard: %v", err)
	}
	last := snap.Forecast[len(snap.Forecast)-1]
	if _, ok := last.Measures[MeasureAirQuality]; ok {
		t.Errorf("air quality present for %s but the fixture does not forecast it", last.Date)
	}
	if len(last.Measures) != 2 {
		t.Errorf("forecast measures = %v, want only grass and allergens", dayKeys(last))
	}
}

func TestParseDashboardBuildsAlert(t *testing.T) {
	snap, err := ParseDashboard([]byte(dashboardFixture))
	if err != nil {
		t.Fatalf("ParseDashboard: %v", err)
	}
	if snap.Alert == nil {
		t.Fatal("alert missing")
	}
	if snap.Alert.Title != "Thunderstorm Asthma" {
		t.Errorf("alert title = %q", snap.Alert.Title)
	}
	if snap.Alert.Level.Name != "Moderate" {
		t.Errorf("alert level = %q, want the current thunderstorm category", snap.Alert.Level.Name)
	}
}

func TestParseDashboardWithoutAlert(t *testing.T) {
	body := strings.Replace(dashboardFixture, `"alert":{"title":"Thunderstorm Asthma","text":"The Thunderstorm Asthma forecast for this location is currently: Moderate. Take care.","icon_image_url":"https://app-api.airhealthservices.au/media/resourceimg/esta_icon_alert.svg","colour":"#ffffff","background_colour":"#fbc503"}`, `"alert":null`, 1)
	snap, err := ParseDashboard([]byte(body))
	if err != nil {
		t.Fatalf("ParseDashboard: %v", err)
	}
	if snap.Alert != nil {
		t.Errorf("alert = %+v, want nil", snap.Alert)
	}
}

func TestParseDashboardRejectsEmpty(t *testing.T) {
	if _, err := ParseDashboard([]byte(`{"locations":[]}`)); err == nil {
		t.Fatal("want error for a payload with no locations")
	}
}

func TestParseLocationReadsSal(t *testing.T) {
	loc, err := parseLocation([]byte(locationsFixture))
	if err != nil {
		t.Fatalf("parseLocation: %v", err)
	}
	if loc.SAL != "21640" || loc.SA3 != "20604" {
		t.Errorf("location = %+v", loc)
	}
	if loc.Suburb != "Melbourne" || loc.State != "VIC" || loc.Postcode != "3000" {
		t.Errorf("location = %+v", loc)
	}
}

func TestParseLocationRejectsEmpty(t *testing.T) {
	if _, err := parseLocation([]byte(`{"locations":[]}`)); err == nil {
		t.Fatal("want error for no matching location")
	}
}

func TestLocationDashboardPathPrefersSAL(t *testing.T) {
	if got := (Location{SAL: "21640", SA3: "20604"}).DashboardPath(); got != "/dashboard/metricdash/SAL/21640/" {
		t.Errorf("path = %q", got)
	}
	if got := (Location{SA3: "20604"}).DashboardPath(); got != "/dashboard/metricdash/SA3/20604/" {
		t.Errorf("path = %q", got)
	}
}

// A coordinate lookup must send latitude first. The service accepts the
// reversed pair and resolves a different place rather than failing.
func TestAirHealthLocationSendsLatitudeFirst(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("coordinate")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(locationsFixture))
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	if _, err := client.Location(context.Background(), -37.8136, 144.9631); err != nil {
		t.Fatalf("Location: %v", err)
	}
	if got != "-37.8136,144.9631" {
		t.Errorf("coordinate = %q, want latitude,longitude", got)
	}
}

func TestAirHealthLocationRoutesAustralianCitiesByUserCoordinates(t *testing.T) {
	cases := []struct {
		name, suburb, state, postcode, sal, sa3 string
		lat, lon                                float64
	}{
		{"Melbourne", "Melbourne", "VIC", "3000", "21640", "20604", -37.8136, 144.9631},
		{"Canberra", "Canberra City", "ACT", "2601", "80038", "80105", -35.2809, 149.1300},
		{"Sydney", "Sydney", "NSW", "2000", "13730", "11703", -33.8688, 151.2093},
		{"Perth", "Perth", "WA", "6000", "51230", "50302", -31.9505, 115.8605},
	}
	byCoordinate := make(map[string]struct {
		suburb, state, postcode, sal, sa3 string
	})
	for _, tc := range cases {
		byCoordinate[fmt.Sprintf("%g,%g", tc.lat, tc.lon)] = struct {
			suburb, state, postcode, sal, sa3 string
		}{tc.suburb, tc.state, tc.postcode, tc.sal, tc.sa3}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/locations/" {
			t.Errorf("request path = %q, want the shared location service", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("location request unexpectedly needs credentials: %v", r.Header)
		}
		place, ok := byCoordinate[r.URL.Query().Get("coordinate")]
		if !ok {
			t.Errorf("unknown coordinate %q", r.URL.Query().Get("coordinate"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"locations": []map[string]any{{
			"suburb": place.suburb, "state": place.state, "postcode": place.postcode,
			"SAL": place.sal, "SA3": place.sa3, "use_sal": true,
		}}})
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := client.Location(context.Background(), tc.lat, tc.lon)
			if err != nil {
				t.Fatalf("Location: %v", err)
			}
			if loc.Suburb != tc.suburb || loc.State != tc.state || loc.SAL != tc.sal || loc.SA3 != tc.sa3 {
				t.Fatalf("location = %+v, want %s/%s SAL %s", loc, tc.suburb, tc.state, tc.sal)
			}
			if got, want := loc.DashboardPath(), "/dashboard/metricdash/SAL/"+tc.sal+"/"; got != want {
				t.Fatalf("dashboard route = %q, want %q", got, want)
			}
		})
	}
}

func TestAirHealthDashboardFollowsSALRoute(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(dashboardFixture))
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	if _, err := client.Dashboard(context.Background(), Location{SAL: "21640"}); err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(paths) == 0 || paths[0] != "/dashboard/metricdash/SAL/21640/" {
		t.Fatalf("first request = %v, want the dashboard route", paths)
	}
}

func TestAirHealthAttachesBreakdowns(t *testing.T) {
	var detailPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/details/Air_Quality/"):
			detailPaths = append(detailPaths, r.URL.Path)
			_, _ = w.Write([]byte(airQualityDetailFixture))
		case strings.HasSuffix(r.URL.Path, "/details/Allergens/"):
			detailPaths = append(detailPaths, r.URL.Path)
			_, _ = w.Write([]byte(allergenDetailFixture))
		default:
			_, _ = w.Write([]byte(dashboardFixture))
		}
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	snap, err := client.Dashboard(context.Background(), Location{SAL: "21640"})
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(detailPaths) != 2 {
		t.Fatalf("detail paths = %v, want air quality and allergen data", detailPaths)
	}
	for _, path := range detailPaths {
		if !strings.HasPrefix(path, "/dashboard/metricdash/free/SAL/21640/details/") || strings.Contains(path, "premium") {
			t.Errorf("detail request = %q, want the free app route", path)
		}
	}

	air, ok := snap.Measure(MeasureAirQuality)
	if !ok {
		t.Fatal("no air quality measure")
	}
	// The summary category must survive the merge: enrichment adds detail, it
	// does not replace the authoritative reading.
	if air.Level.Name != "Good" {
		t.Errorf("air level = %q, want Good", air.Level.Name)
	}
	names := breakdownNames(air.Breakdown)
	for _, want := range []string{"PM10", "O3"} {
		if !contains(names, want) {
			t.Errorf("air breakdown = %v, want %q", names, want)
		}
	}
	// "combined" duplicates the row the dashboard already showed.
	if contains(names, "combined") || contains(names, "Air Quality") {
		t.Errorf("air breakdown kept the summary series: %v", names)
	}

	alg, ok := snap.Measure(MeasureAllergens)
	if !ok {
		t.Fatal("no allergen measure")
	}
	if got := breakdownNames(alg.Breakdown); !contains(got, "Alternaria") || !contains(got, "Eucalypts") {
		t.Errorf("allergen breakdown = %v", got)
	}
}

func TestAirHealthBreakdownFailureIsNotFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/details/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(dashboardFixture))
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	snap, err := client.Dashboard(context.Background(), Location{SAL: "21640"})
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(snap.Measures) == 0 {
		t.Fatal("a failed enrichment dropped the whole reading")
	}
	for _, m := range snap.Measures {
		if len(m.Breakdown) != 0 {
			t.Errorf("%s kept a breakdown from a failed request", m.Name)
		}
	}
}

func TestAirHealthParsesWeatherContext(t *testing.T) {
	snap, err := ParseDashboard([]byte(dashboardFixture))
	if err != nil {
		t.Fatalf("ParseDashboard: %v", err)
	}
	w := snap.Weather
	if w == nil {
		t.Fatal("weather context dropped")
	}
	if w.MaxC != 23 || w.MinC != 16 {
		t.Errorf("range = %v..%v, want 16..23", w.MinC, w.MaxC)
	}
	if w.RainRange != "14.3 mm" {
		t.Errorf("rain = %q", w.RainRange)
	}
	// The source sends null, which must not be read as a 0% chance.
	if w.RainChance != -1 {
		t.Errorf("rain chance = %v, want -1 for null", w.RainChance)
	}
}

// TestAirHealthFlatDetailShape covers thunderstorm asthma, whose detail
// response is a flat array rather than an object.
func TestAirHealthFlatDetailShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/details/Thunderstorm_Asthma/") {
			_, _ = w.Write([]byte(thunderstormDetailFixture))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	body, err := AirHealth{Base: srv.URL}.get(context.Background(),
		"/dashboard/metricdash/free/SAL/21640/details/Thunderstorm_Asthma/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// A flat array has no named constituents, so it yields no breakdown; the
	// point is that it must decode without panicking rather than be rejected.
	if got := parseBreakdowns(MeasureThunderstorm, body); len(got) != 0 {
		t.Errorf("breakdown = %v, want none for a flat series", got)
	}
}

func breakdownNames(parts []Breakdown) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.Name
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func TestAirHealthDashboardReportsUnavailableOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	_, err := client.Dashboard(context.Background(), Location{SAL: "21640"})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

// An SA3 payload carries every suburb in the area; the requested SAL must be
// the one returned, not whichever happens to be first.
func TestAirHealthDashboardSelectsRequestedSALFromSA3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sa3Fixture))
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	snap, err := client.Dashboard(context.Background(), Location{SAL: "21640", SA3: "20604"})
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if snap.Location.SAL != "21640" {
		t.Errorf("sal = %q, want 21640", snap.Location.SAL)
	}
	if snap.Location.Suburb != "Melbourne" {
		t.Errorf("suburb = %q, want Melbourne", snap.Location.Suburb)
	}
}

func TestSeverityOfClamps(t *testing.T) {
	for _, tc := range []struct {
		warn int
		want Severity
	}{{0, SeverityGood}, {1, SeverityModerate}, {2, SeverityHigh}, {9, SeverityHigh}, {-1, SeverityGood}} {
		if got := SeverityOf(tc.warn); got != tc.want {
			t.Errorf("SeverityOf(%d) = %v, want %v", tc.warn, got, tc.want)
		}
	}
}

func TestLevelKnown(t *testing.T) {
	for _, name := range []string{"High", "Good", "Extreme"} {
		if !(Level{Name: name}).Known() {
			t.Errorf("%q should be known", name)
		}
	}
	for _, name := range []string{"", "Unknown", "Unavailable", "No Data", "N/A"} {
		if (Level{Name: name}).Known() {
			t.Errorf("%q should be unknown", name)
		}
	}
}

func TestSnapshotWorstPrefersHighestSeverity(t *testing.T) {
	snap := Snapshot{Measures: []Measure{
		{Name: MeasureGrass, Level: Level{Name: "Low", Severity: SeverityGood}},
		{Name: MeasureAirQuality, Level: Level{Name: "Poor", Severity: SeverityModerate}},
		{Name: MeasureAllergens, Level: Level{Name: "Extreme", Severity: SeverityHigh}},
	}}
	got, ok := snap.Worst()
	if !ok || got.Name != MeasureAllergens {
		t.Errorf("worst = %+v, want allergens", got)
	}
}

// Ties keep MeasureOrder so the headline is stable frame to frame; a tie that
// flipped would make the bar text alternate on every refresh.
func TestSnapshotWorstBreaksTiesByOrder(t *testing.T) {
	snap := Snapshot{Measures: []Measure{
		{Name: MeasureAirQuality, Level: Level{Name: "High", Severity: SeverityHigh}},
		{Name: MeasureGrass, Level: Level{Name: "High", Severity: SeverityHigh}},
	}}
	got, ok := snap.Worst()
	if !ok || got.Name != MeasureGrass {
		t.Errorf("worst = %+v, want grass", got)
	}
}

func TestSnapshotWorstSkipsUnknown(t *testing.T) {
	snap := Snapshot{Measures: []Measure{
		{Name: MeasureGrass, Level: Level{Name: "Unavailable", Severity: SeverityHigh}},
	}}
	if _, ok := snap.Worst(); ok {
		t.Error("an unknown level should not win worst")
	}
}

func TestCanonicalMeasure(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Thunderstorm Asthma Alert", MeasureThunderstorm},
		{"Thunderstorm Asthma", MeasureThunderstorm},
		{"Other Allergens", MeasureAllergens},
		{"Grass", MeasureGrass},
		{" Air Quality ", MeasureAirQuality},
		{"Mystery", "Mystery"},
	} {
		if got := CanonicalMeasure(tc.in); got != tc.want {
			t.Errorf("CanonicalMeasure(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEpochMillisZeroStaysZero(t *testing.T) {
	if got := epochMillis(0); !got.IsZero() {
		t.Errorf("epochMillis(0) = %v, want zero", got)
	}
	if got := epochMillis(1764914400000); !got.Equal(time.Date(2025, 12, 5, 6, 0, 0, 0, time.UTC)) {
		t.Errorf("epochMillis = %v", got)
	}
}

func dayKeys(d Day) []string {
	out := make([]string, 0, len(d.Measures))
	for k := range d.Measures {
		out = append(out, k)
	}
	return out
}

// Fixtures are trimmed from live responses captured on 2026-10-31, keeping
// the fields and quirks the decoder must handle.

const locationsFixture = `{"locations":[{"id":863,"postcode":"3000","Postcode Name":"Melbourne","suburb":"Melbourne","state":"VIC","SAL":"21640","SAL Name":"Melbourne","SA2":"206041503","SA2 Name":"Melbourne CBD - East","SA3":"20604","SA3 Name":"Melbourne City","IAR":"20101201","IAR Name":"Melbourne - Central","District":"VIC_PW007","District Name":"Central","latitude":-37.825023,"longitude":144.97158,"use_sal":false}]}`

const dashboardFixture = `{
 "sals":["21640"],
 "locations":[{
  "location":{"id":27593,"sal":"21640","suburb":"Melbourne","state":"VIC","sa3":["21640"],"sa3_name":[""],"district":[""],"district_name":[""]},
  "current_conditions":{"date":"2026-10-31","day_title":"Later today","text":"Take care","colour":"#ffffff","background_colour":"#b5090a","image":"https://app-api.airhealthservices.au/media/resourceimg/Scene_2010.svg","weather":{"rain_range":"14.3 mm","temperature_min":16.0,"temperature_max":23.0,"rain_probability":null}},
  "measures":[
   {"name":"Grass","text":"High","value":2,"colour":"#ffffff","background_colour":"#fe9a1f","image":"https://app-api.airhealthservices.au/media/resourceimg/Allergens_3.svg"},
   {"name":"Other Allergens","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503","image":"https://app-api.airhealthservices.au/media/resourceimg/Other_Alg_2.svg"},
   {"name":"Air Quality","text":"Good","value":1,"colour":"#ffffff","background_colour":"#4bd964","image":"https://app-api.airhealthservices.au/media/resourceimg/Air_Quality_1.svg","show_metric":{"subcat":"PM2.5","value":0.0,"monitor_is_close":true,"units":"ug/m3"}},
   {"name":"Thunderstorm Asthma","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503","image":"https://app-api.airhealthservices.au/media/resourceimg/Thunderstorm_Asthma_1.svg"}
  ],
  "alert":{"title":"Thunderstorm Asthma","text":"The Thunderstorm Asthma forecast for this location is currently: Moderate. Take care.","icon_image_url":"https://app-api.airhealthservices.au/media/resourceimg/esta_icon_alert.svg","colour":"#ffffff","background_colour":"#fbc503"},
  "forecast":[
   {"date":"2026-10-31","day_title":"Later today","temperature_max":23.0,"temperature_min":16.0,"measures":[
     {"name":"Grass","text":"High","value":2,"colour":"#ffffff","background_colour":"#fe9a1f"},
     {"name":"Other Allergens","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503"},
     {"name":"Air Quality","text":"Very Good","value":0,"colour":"#ffffff","background_colour":"#56e0ff"},
     {"name":"Thunderstorm Asthma Alert","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503"}
   ]},
   {"date":"2026-11-01","day_title":"Sunday, 1 Nov 2026","temperature_max":19.0,"temperature_min":13.0,"measures":[
     {"name":"Grass","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503"},
     {"name":"Other Allergens","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503"}
   ]}
  ]
 }],
 "ads":{},"news":[],"factsheets":[],"podcasts":[],
 "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"
}`

const sa3Fixture = `{
 "sals":["22038","21640"],
 "locations":[
  {"location":{"id":27991,"sal":"22038","suburb":"Parkville","state":"VIC","sa3":["20604"],"sa3_name":[""],"district":[""],"district_name":[""]},
   "current_conditions":{"date":"2026-10-31","day_title":"Later today","text":"Take care","weather":{"temperature_min":16.0,"temperature_max":23.0}},
   "measures":[{"name":"Grass","text":"High","value":2,"colour":"#ffffff","background_colour":"#fe9a1f"}],
   "alert":null,"forecast":[]},
  {"location":{"id":27593,"sal":"21640","suburb":"Melbourne","state":"VIC","sa3":["20604"],"sa3_name":[""],"district":[""],"district_name":[""]},
   "current_conditions":{"date":"2026-10-31","day_title":"Later today","text":"Take care","weather":{"temperature_min":16.0,"temperature_max":23.0}},
   "measures":[{"name":"Grass","text":"High","value":2,"colour":"#ffffff","background_colour":"#fe9a1f"}],
   "alert":null,"forecast":[]}
 ],
 "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"
}`

// airQualityDetailFixture is a trimmed live response. Note that "combined"
// carries only two entries while PM2.5 carries three: the series are not the
// same length, which is exactly why the decoder keys on date and not index.
const airQualityDetailFixture = `{
 "sals":["21640"],
 "locations":[{
  "location":{"id":27593,"sal":"21640","suburb":"Melbourne","state":"VIC"},
  "current":{"name":"Air Quality","text":"Good","value":1,"colour":"#ffffff","background_colour":"#4bd964","show_metric":{"subcat":"PM2.5","value":8.2,"monitor_is_close":true,"units":"ug/m3"}},
  "data":{
   "combined":[
    {"name":"Air Quality","forecast_date":"2026-10-31","day_title":"Later today","text":"Very Good","value":0},
    {"name":"Air Quality","forecast_date":"2026-11-01","day_title":"Sunday, 1 Nov 2026","text":"Good","value":1}
   ],
   "PM2.5":[
    {"name":"PM2.5","forecast_date":"2026-10-31","day_title":"Later today","text":"Good","value":1},
    {"name":"PM2.5","forecast_date":"2026-11-01","day_title":"Sunday, 1 Nov 2026","text":"Fair","value":2},
    {"name":"PM2.5","forecast_date":"2026-11-02","day_title":"Monday, 2 Nov 2026","text":"Good","value":1}
   ],
   "PM10":[{"name":"PM10","forecast_date":"2026-10-31","day_title":"Later today","text":"Very Good","value":0}],
   "O3":[{"name":"O3","forecast_date":"2026-10-31","day_title":"Later today","text":"Good","value":1}]
  },
  "map":{"has_map":false,"map_image":null,"map_desc":null}
 }],
 "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"
}`

// allergenDetailFixture exercises the species breakdown.
const allergenDetailFixture = `{
 "sals":["21640"],
 "locations":[{
  "location":{"id":27593,"sal":"21640","suburb":"Melbourne","state":"VIC"},
  "current":{"name":"Other Allergens","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503"},
  "data":{
   "combined":[{"name":"Other Allergens","forecast_date":"2026-10-31","day_title":"Later today","text":"Moderate","value":1}],
   "Birch":[{"name":"Birch","forecast_date":"2026-10-31","day_title":"Later today","text":"Moderate","value":1}],
   "Eucalypts":[{"name":"Eucalypts","forecast_date":"2026-10-31","day_title":"Later today","text":"Moderate","value":1}],
   "Plantain":[{"name":"Plantain","forecast_date":"2026-10-31","day_title":"Later today","text":"Low","value":0}],
   "Alternaria":[{"name":"Alternaria","forecast_date":"2026-10-31","day_title":"Later today","text":"Moderate","value":1}]
  }
 }],
 "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"
}`

// thunderstormDetailFixture is the flat-array shape: "data" is a list, not an
// object with named series.
const thunderstormDetailFixture = `{
 "sals":["21640"],
 "locations":[{
  "location":{"id":27593,"sal":"21640","suburb":"Melbourne","state":"VIC"},
  "current":{"name":"Thunderstorm Asthma","text":"Moderate","value":1,"colour":"#ffffff","background_colour":"#fbc503"},
  "data":[
   {"name":"Thunderstorm Asthma","forecast_date":"2026-10-31","day_title":"Later today","text":"Moderate","value":1},
   {"name":"Thunderstorm Asthma","forecast_date":"2026-11-01","day_title":"Sunday, 1 Nov 2026","text":"Low","value":0}
  ]
 }],
 "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"
}`

const openMeteoFixture = `{
 "latitude":-37.8,"longitude":145.0,"generationtime_ms":0.55,
 "utc_offset_seconds":39600,"timezone":"Australia/Melbourne","timezone_abbreviation":"GMT+11","elevation":19.0,
 "current_units":{"pm10":"μg/m³","pm2_5":"μg/m³","carbon_monoxide":"μg/m³","nitrogen_dioxide":"μg/m³","sulphur_dioxide":"μg/m³","ozone":"μg/m³","uv_index":"","us_aqi":"USAQI"},
 "current":{"time":"2026-10-31T19:00","interval":3600,"pm10":7.0,"pm2_5":4.3,"carbon_monoxide":105.0,"nitrogen_dioxide":3.1,"sulphur_dioxide":1.0,"ozone":57.0,"uv_index":0.1,"us_aqi":28},
 "daily_units":{"time":"iso8601","uv_index_max":""},
 "daily":{"time":["2026-10-31","2026-11-01","2026-11-02"],"uv_index_max":[4.9,4.55,3.2]}
}`
