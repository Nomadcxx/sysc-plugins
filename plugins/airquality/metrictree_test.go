package airquality

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The tree nests three levels on the Air Quality branch (measure → group →
// metric) but only two on the others. A decoder that assumes one shape loses
// every air quality breakpoint.
func TestParseTreeReadsNestedAirQualityBranch(t *testing.T) {
	tree, err := ParseTree([]byte(metricTreeFixture))
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	pm25, ok := tree.Lookup(MeasureAirQuality, "PM2.5")
	if !ok {
		t.Fatal("PM2.5 scale missing")
	}
	if len(pm25) != 6 {
		t.Fatalf("PM2.5 classes = %d, want 6", len(pm25))
	}
	if pm25[0].Name != "Very Good" || pm25[0].UpperBound != 5 {
		t.Errorf("first class = %+v", pm25[0])
	}
	if pm25[3].Name != "Poor" || pm25[3].WarnLevel != 1 {
		t.Errorf("Poor class = %+v", pm25[3])
	}
}

func TestParseTreeSingleScaleMeasures(t *testing.T) {
	tree, err := ParseTree([]byte(metricTreeFixture))
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	for _, measure := range []string{MeasureGrass, MeasureThunderstorm, MeasureWoodsmoke} {
		classes, ok := tree.Lookup(measure, "")
		if !ok || len(classes) == 0 {
			t.Errorf("%s: no scale", measure)
		}
	}
}

// upper_bound is an exclusive ceiling: 12.5 belongs to "Fair", not "Good".
// Treating it as inclusive is the off-by-one the official client still ships.
func TestTreeClassifyUsesExclusiveCeiling(t *testing.T) {
	tree, err := ParseTree([]byte(metricTreeFixture))
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{0, "Very Good"},
		{4.99, "Very Good"},
		{5, "Good"},
		{12.49, "Good"},
		{12.5, "Fair"},
		{25, "Poor"},
		{150, "Extremely Poor"},
		{9999, "Extremely Poor"},
	} {
		got, ok := tree.Classify(MeasureAirQuality, "PM2.5", tc.value)
		if !ok {
			t.Fatalf("Classify(%g) found no class", tc.value)
		}
		if got.Name != tc.want {
			t.Errorf("Classify(%g) = %q, want %q", tc.value, got.Name, tc.want)
		}
	}
}

// The pollen scales are not the air quality scale: warn_level rises with the
// category, and a raw index of 2 is a warning rather than a good reading.
func TestTreeClassifyPollenSeverity(t *testing.T) {
	tree, err := ParseTree([]byte(metricTreeFixture))
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	class, ok := tree.Classify(MeasureGrass, "", 120)
	if !ok {
		t.Fatal("no class for 120 grains")
	}
	if class.Name != "Extreme" {
		t.Errorf("class = %q, want Extreme", class.Name)
	}
	if SeverityOf(class.WarnLevel) != SeverityHigh {
		t.Errorf("severity = %v, want high", SeverityOf(class.WarnLevel))
	}
}

func TestTreeClassifyUnknownMeasure(t *testing.T) {
	tree, err := ParseTree([]byte(metricTreeFixture))
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	if _, ok := tree.Classify("Radon", "", 1); ok {
		t.Error("unknown measure should not classify")
	}
}

func TestParseTreeRejectsEmpty(t *testing.T) {
	if _, err := ParseTree([]byte(`{}`)); err == nil {
		t.Fatal("want error for an empty tree")
	}
	if _, err := ParseTree([]byte(`not json`)); err == nil {
		t.Fatal("want error for malformed json")
	}
}

func TestNilTreeLookupIsSafe(t *testing.T) {
	var tree *Tree
	if _, ok := tree.Lookup(MeasureGrass, ""); ok {
		t.Error("nil tree should not look up")
	}
	if _, ok := tree.Classify(MeasureGrass, "", 1); ok {
		t.Error("nil tree should not classify")
	}
	if got := tree.Measures(); got != nil {
		t.Errorf("nil tree measures = %v", got)
	}
}

func TestTreeMeasuresFollowPresentationOrder(t *testing.T) {
	tree, err := ParseTree([]byte(metricTreeFixture))
	if err != nil {
		t.Fatalf("ParseTree: %v", err)
	}
	want := []string{MeasureGrass, MeasureAirQuality, MeasureThunderstorm, MeasureWoodsmoke}
	got := tree.Measures()
	if len(got) != len(want) {
		t.Fatalf("measures = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("measures[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPhraseLooksUpCaseInsensitively(t *testing.T) {
	phrases := Phrases{MeasureGrass: {"high": "Airborne grass allergens are elevated."}}
	if got := phrases.Phrase(MeasureGrass, "High"); got != "Airborne grass allergens are elevated." {
		t.Errorf("phrase = %q", got)
	}
	if got := phrases.Phrase(MeasureAirQuality, "High"); got != "" {
		t.Errorf("phrase = %q, want empty", got)
	}
}

func TestParseTreeSendsNoCredentials(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization") + r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(metricTreeFixture))
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	if _, err := client.MetricTree(context.Background()); err != nil {
		t.Fatalf("MetricTree: %v", err)
	}
	if auth != "" {
		t.Errorf("request carried credentials: %q", auth)
	}
}

// The metric tree is a trailing-slash-sensitive route: with the slash the
// service answers 404, which would silently demote every measure to the
// built-in fallback scale.
func TestMetricTreePathHasNoTrailingSlash(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(metricTreeFixture))
	}))
	defer srv.Close()

	client := AirHealth{Base: srv.URL}
	if _, err := client.MetricTree(context.Background()); err != nil {
		t.Fatalf("MetricTree: %v", err)
	}
	if path != "/dashboard/metrictree" {
		t.Errorf("path = %q, want /dashboard/metrictree", path)
	}
}

// A trimmed copy of the live tree captured on 2026-10-31: every measure,
// with the air quality branch nested one level deeper than the rest.
const metricTreeFixture = `{
 "Air Quality":{
  "Gasses":{
   "NO2":{"total_boxes":6,"classes":[
     {"name":"Very Good","upper_bound":0.0,"lower_bound":0.0,"warn_level":0},
     {"name":"Good","upper_bound":30.0,"lower_bound":0.0,"warn_level":0},
     {"name":"Fair","upper_bound":120.0,"lower_bound":30.0,"warn_level":1},
     {"name":"Poor","upper_bound":180.0,"lower_bound":120.0,"warn_level":1},
     {"name":"Very Poor","upper_bound":360.0,"lower_bound":180.0,"warn_level":2},
     {"name":"Extremely Poor","upper_bound":10000.0,"lower_bound":360.0,"warn_level":2}]}
  },
  "Particles":{
   "PM2.5":{"total_boxes":6,"classes":[
     {"name":"Very Good","upper_bound":5.0,"lower_bound":0.0,"warn_level":0},
     {"name":"Good","upper_bound":12.5,"lower_bound":5.0,"warn_level":0},
     {"name":"Fair","upper_bound":25.0,"lower_bound":12.5,"warn_level":1},
     {"name":"Poor","upper_bound":50.0,"lower_bound":25.0,"warn_level":1},
     {"name":"Very Poor","upper_bound":150.0,"lower_bound":50.0,"warn_level":2},
     {"name":"Extremely Poor","upper_bound":1000000.0,"lower_bound":150.0,"warn_level":2}]}
  }
 },
 "Grass":{"Grass":{"Grass":{"total_boxes":4,"classes":[
   {"name":"Low","upper_bound":20.0,"lower_bound":0.0,"warn_level":0},
   {"name":"Moderate","upper_bound":50.0,"lower_bound":20.0,"warn_level":1},
   {"name":"High","upper_bound":100.0,"lower_bound":50.0,"warn_level":2},
   {"name":"Extreme","upper_bound":2000000.0,"lower_bound":100.0,"warn_level":2}]}}},
 "Thunderstorm Asthma":{"Thunderstorm Asthma":{"Thunderstorm Asthma":{"total_boxes":3,"classes":[
   {"name":"Low","upper_bound":0.5,"lower_bound":0.0,"warn_level":0},
   {"name":"Moderate","upper_bound":1.5,"lower_bound":0.5,"warn_level":1},
   {"name":"High","upper_bound":2.5,"lower_bound":1.5,"warn_level":2}]}}},
 "Woodsmoke":{"Woodsmoke":{"Woodsmoke":{"total_boxes":3,"classes":[
   {"name":"Low","upper_bound":350.0,"lower_bound":0.0,"warn_level":0},
   {"name":"Moderate","upper_bound":450.0,"lower_bound":350.0,"warn_level":0},
   {"name":"High","upper_bound":550.0,"lower_bound":450.0,"warn_level":1}]}}}
}`

// The published tree cannot be painted directly: grass tops out at a sentinel
// two million grains, and nitrogen dioxide opens with a zero-width class. A
// dial built naively would put every reading in one zone.
func TestTreeDialIgnoresSentinelCeilings(t *testing.T) {
	t.Parallel()

	tree := WireScale
	bands, max, ok := tree.Dial(MeasureGrass, "")
	if !ok {
		t.Fatal("grass has no dial")
	}
	// Published thresholds are 20, 50, 100 and then an open interval.
	if max < 100 || max > 1000 {
		t.Fatalf("grass dial top = %v, want a scale just above the last threshold", max)
	}
	if len(bands) != 4 {
		t.Fatalf("grass dial has %d zones, want 4: %+v", len(bands), bands)
	}
	for i, want := range []string{"Low", "Moderate", "High", "Extreme"} {
		if bands[i].Label != want {
			t.Errorf("grass band %d label = %q, want %q", i, bands[i].Label, want)
		}
	}
	if bands[len(bands)-1].Until != 1 {
		t.Fatalf("last zone ends at %v, want the end of the arc", bands[len(bands)-1].Until)
	}
	for i := 1; i < len(bands); i++ {
		if bands[i].Until <= bands[i-1].Until {
			t.Fatalf("zones are not increasing: %+v", bands)
		}
	}
}

func TestTreeDialKeepsSeverityInOrder(t *testing.T) {
	t.Parallel()

	for _, measure := range []string{MeasureGrass, MeasureAirQuality} {
		metric := ""
		if measure == MeasureAirQuality {
			metric = "PM2.5"
		}
		bands, _, ok := WireScale.Dial(measure, metric)
		if !ok {
			t.Fatalf("%s has no dial", measure)
		}
		for i := 1; i < len(bands); i++ {
			if bands[i].Severity < bands[i-1].Severity {
				t.Fatalf("%s severity falls from %v to %v at zone %d",
					measure, bands[i-1].Severity, bands[i].Severity, i)
			}
		}
	}
}

// Air quality counts upward from "Very Good", so its numeric reading is
// inverted relative to every other measure. The dial has to place it the same
// way the category does, or a good reading would paint as a red zone.
func TestTreeDialPlacesAirQualityCorrectly(t *testing.T) {
	t.Parallel()

	bands, max, ok := WireScale.Dial(MeasureAirQuality, "PM2.5")
	if !ok {
		t.Fatal("air quality has no dial")
	}
	fraction := 4.0 / max // a very good PM2.5 reading, well under 5
	for _, band := range bands {
		if fraction < band.Until {
			if band.Severity != SeverityGood {
				t.Fatalf("a reading of 4 µg/m³ lands in a %v zone", band.Severity)
			}
			return
		}
	}
	t.Fatal("no zone covers 4 µg/m³")
}

func TestAttachDialSkipsThunderstormAsthma(t *testing.T) {
	t.Parallel()

	m := Measure{
		Name:   MeasureThunderstorm,
		Level:  Level{Name: "Moderate", Severity: SeverityModerate},
		Metric: &Metric{Subcat: "Thunderstorm Asthma", Value: 1},
	}
	WireScale.AttachDial(&m)
	if m.Max != 0 || m.Bands != nil {
		t.Fatalf("thunderstorm asthma got a dial: %+v", m)
	}
}

func TestAttachDialSkipsUSAQI(t *testing.T) {
	t.Parallel()

	m := Measure{
		Name:   MeasureAirQuality,
		Metric: &Metric{Subcat: "US AQI", Value: 28},
	}
	WireScale.AttachDial(&m)
	if m.Max != 0 || m.Bands != nil {
		t.Fatalf("US AQI borrowed the EPA scale: %+v", m)
	}
}

func TestUVBandsAreOrderedAndComplete(t *testing.T) {
	t.Parallel()

	bands := UVBands()
	if len(bands) != 3 {
		t.Fatalf("UV dial has %d zones, want 3", len(bands))
	}
	if bands[len(bands)-1].Until != 1 {
		t.Fatal("UV dial does not reach the end of the arc")
	}
	for i := 1; i < len(bands); i++ {
		if bands[i].Until <= bands[i-1].Until || bands[i].Severity < bands[i-1].Severity {
			t.Fatalf("UV zones are not ordered: %+v", bands)
		}
	}
	// Zones are equal thirds of the arc, and each carries the published
	// threshold that ends it so the needle can be placed inside the zone.
	if bands[0].Until != 1.0/3 || bands[1].Until != 2.0/3 {
		t.Fatalf("UV zones are not equal thirds: %+v", bands)
	}
	if got, want := bands[0].Ceiling, 3.0; got != want {
		t.Fatalf("first UV zone ends at reading %v, want 3", got)
	}
	if got, want := bands[1].Ceiling, 6.0; got != want {
		t.Fatalf("second UV zone ends at reading %v, want 6", got)
	}
	for i, want := range []string{"Low", "Moderate", "High"} {
		if bands[i].Label != want {
			t.Errorf("UV band %d label = %q, want %q", i, bands[i].Label, want)
		}
	}
}

// A "Fair" PM2.5 reading must not land in a dial that is mostly red. This is
// the property the first linear mapping broke: 19.9 µg/m³ is 13% of the way to
// the 150 ceiling but the third of six zones, so a linear arc painted it green
// while the zones above it were red.
func TestPlacePutsAReadingInTheZoneItEarned(t *testing.T) {
	t.Parallel()

	bands, max, ok := WireScale.Dial(MeasureAirQuality, "PM2.5")
	if !ok {
		t.Fatal("air quality has no dial")
	}
	if got, want := len(bands), 6; got != want {
		t.Fatalf("PM2.5 has %d zones, want %d", got, want)
	}
	// 19.9 is "Fair", the third zone. Equal thirds means it sits between .333
	// and .5, not at .088 on a linear scale.
	got := Place(bands, 19.9)
	if got < 1.0/3 || got >= 2.0/3 {
		t.Fatalf("19.9 µg/m³ placed at %.3f, want inside the third of six zones (%.3f..%.3f)",
			got, 1.0/3, 2.0/3)
	}
	// The same reading on a linear map would be far lower, which is the bug.
	if linear := 19.9 / max; got <= linear {
		t.Fatalf("placement %.3f is not above the linear %.3f", got, linear)
	}
}

func TestPlaceIsMonotonicAndClamped(t *testing.T) {
	t.Parallel()

	bands, _, _ := WireScale.Dial(MeasureGrass, "")
	previous := -1.0
	for _, value := range []float64{0, 5, 19.9, 20, 49, 50, 99, 100, 500, 1e9} {
		got := Place(bands, value)
		if got < previous {
			t.Fatalf("placement falls from %v to %v at reading %v", previous, got, value)
		}
		if got < 0 || got > 1 {
			t.Fatalf("reading %v placed outside the arc at %v", value, got)
		}
		previous = got
	}
	if got := Place(bands, 1e9); got != 1 {
		t.Fatalf("a reading past every threshold placed at %v, want the end", got)
	}
}

func TestPlaceWithNoBandsIsZero(t *testing.T) {
	t.Parallel()

	if got := Place(nil, 42); got != 0 {
		t.Fatalf("no bands placed at %v, want 0", got)
	}
}

// Every dial must be readable: the needle has to land in a zone whose severity
// matches the category the source published, or the colour contradicts the text.
func TestPlaceAgreesWithPublishedSeverity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		metric string
		value  float64
		want   Severity
	}{
		{"PM2.5", 3.0, SeverityGood},
		{"PM2.5", 19.9, SeverityModerate},
		{"PM2.5", 80, SeverityHigh},
	}
	bands, _, _ := WireScale.Dial(MeasureAirQuality, "PM2.5")
	for _, tc := range cases {
		at := Place(bands, tc.value)
		var landed Severity
		for _, band := range bands {
			if at < band.Until {
				landed = band.Severity
				break
			}
		}
		if landed != tc.want {
			t.Fatalf("%v µg/m³ landed in a %v zone at %.3f, want %v",
				tc.value, landed, at, tc.want)
		}
	}
}
