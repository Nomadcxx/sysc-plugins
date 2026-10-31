package airquality

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseOpenMeteoAirAndUV(t *testing.T) {
	measures, err := ParseOpenMeteo([]byte(openMeteoFixture))
	if err != nil {
		t.Fatalf("ParseOpenMeteo: %v", err)
	}
	if len(measures) != 2 {
		t.Fatalf("measures = %d, want air quality and UV", len(measures))
	}

	air := measures[0]
	if air.Name != MeasureAirQuality {
		t.Fatalf("first measure = %q", air.Name)
	}
	if air.Level.Name != "Good" {
		t.Errorf("AQI 28 = %q, want Good", air.Level.Name)
	}
	if air.Metric == nil || air.Metric.Value != 28 {
		t.Errorf("metric = %+v, want US AQI 28", air.Metric)
	}
	// The constituents are the reason this provider exists: EPA's mirror
	// carries particulates only, so the gases have nowhere else to come from.
	names := breakdownNames(air.Breakdown)
	for _, want := range []string{"PM2.5", "PM10", "O3", "NO2", "SO2", "CO"} {
		if !contains(names, want) {
			t.Errorf("breakdown = %v, missing %q", names, want)
		}
	}
	if names[0] != "PM2.5" {
		t.Errorf("breakdown order = %v, want particulates first", names)
	}

	uv := measures[1]
	if uv.Name != MeasureUV {
		t.Fatalf("second measure = %q", uv.Name)
	}
	// The headline is today's peak, not the 0.1 at 7pm.
	if uv.Metric == nil || uv.Metric.Value != 4.9 {
		t.Errorf("metric = %+v, want today's peak 4.9", uv.Metric)
	}
	if uv.Level.Name != uvModerate {
		t.Errorf("UV 4.9 = %q, want Moderate", uv.Level.Name)
	}
	if len(uv.Breakdown) != 1 || uv.Breakdown[0].Name != "now" {
		t.Errorf("breakdown = %+v, want a single 'now' row", uv.Breakdown)
	}
}

func TestOpenMeteoAQIUsesItsOwnHalfGaugeScale(t *testing.T) {
	body := `{"current":{"time":"2026-11-01T13:00","us_aqi":64,"uv_index":0.2,"pm2_5":10.1},"daily":{"time":["2026-11-01"],"uv_index_max":[1.0]}}`
	measures, err := ParseOpenMeteo([]byte(body))
	if err != nil {
		t.Fatalf("ParseOpenMeteo: %v", err)
	}
	air, ok := (Snapshot{Measures: measures}).Measure(MeasureAirQuality)
	if !ok || air.Max != 500 || len(air.Bands) != 3 {
		t.Fatalf("global air measure = %+v, want three US AQI severity bands through 500", air)
	}
	if air.Bands[0].Ceiling != 50 || air.Bands[1].Ceiling != 100 || air.Bands[2].Ceiling != 500 {
		t.Fatalf("US AQI band ceilings = %+v", air.Bands)
	}
	report := Report{Snapshot: Snapshot{Measures: measures}}
	gauge := Dial(report, 25)
	if gauge == nil || gauge.Name != Title(MeasureAirQuality) || gauge.ValueText != "64 US AQI Moderate" {
		t.Fatalf("global gauge = %+v, want labelled AQI 64 Moderate", gauge)
	}
	if got, want := measureValueText(air), "Moderate · US AQI 64"; got != want {
		t.Errorf("global air row = %q, want %q", got, want)
	}
	if got := strings.Join(tooltipLines(air), " "); !strings.Contains(got, "US AQI") {
		t.Errorf("global air tooltip = %q, missing scale", got)
	}
}

// TestParseOpenMeteoKeepsAbsentDistinctFromZero is the reason every wire field
// is a pointer. A UV index of null at night and a UV index of 0.0 both look
// like zero once decoded into a float, but only one of them means "the sun is
// down and there is nothing to report".
func TestParseOpenMeteoKeepsAbsentDistinctFromZero(t *testing.T) {
	body := `{
	 "current":{"time":"2026-10-31T23:00","pm2_5":4.3,"uv_index":null,"us_aqi":28},
	 "daily":{"time":[],"uv_index_max":[]}
	}`
	measures, err := ParseOpenMeteo([]byte(body))
	if err != nil {
		t.Fatalf("ParseOpenMeteo: %v", err)
	}
	for _, m := range measures {
		if m.Name == MeasureUV {
			t.Fatal("an absent UV index produced a UV measure")
		}
	}

	body = `{
	 "current":{"time":"2026-10-31T12:00","pm2_5":4.3,"uv_index":0.0,"us_aqi":28},
	 "daily":{"time":[],"uv_index_max":[]}
	}`
	measures, err = ParseOpenMeteo([]byte(body))
	if err != nil {
		t.Fatalf("ParseOpenMeteo: %v", err)
	}
	found := false
	for _, m := range measures {
		if m.Name == MeasureUV {
			found = true
			if m.Level.Name != uvLow {
				t.Errorf("UV 0.0 = %q, want Low", m.Level.Name)
			}
		}
	}
	if !found {
		t.Error("a real UV index of 0.0 was dropped as if absent")
	}
}

func TestUVLevelBands(t *testing.T) {
	cases := []struct {
		uv       float64
		want     string
		severity Severity
	}{
		{0, uvLow, SeverityGood},
		{2.9, uvLow, SeverityGood},
		{3, uvModerate, SeverityModerate},
		{5.9, uvModerate, SeverityModerate},
		{6, uvHigh, SeverityHigh},
		{7.9, uvHigh, SeverityHigh},
		{8, uvVeryHigh, SeverityHigh},
		{10.9, uvVeryHigh, SeverityHigh},
		{11, uvExtremeUV, SeverityHigh},
		{14, uvExtremeUV, SeverityHigh},
	}
	for _, c := range cases {
		got := UVLevel(c.uv)
		if got.Name != c.want || got.Severity != c.severity {
			t.Errorf("UVLevel(%v) = %q/%v, want %q/%v", c.uv, got.Name, got.Severity, c.want, c.severity)
		}
	}
}

func TestUSAQIBands(t *testing.T) {
	cases := []struct {
		aqi      float64
		category string
		severity Severity
	}{
		{0, "Good", SeverityGood},
		{50, "Good", SeverityGood},
		{51, "Moderate", SeverityModerate},
		{100, "Moderate", SeverityModerate},
		{101, "Unhealthy for Sensitive Groups", SeverityHigh},
		{150, "Unhealthy for Sensitive Groups", SeverityHigh},
		{151, "Unhealthy", SeverityHigh},
		{301, "Hazardous", SeverityHigh},
	}
	for _, c := range cases {
		if got := USAQICategory(c.aqi); got != c.category {
			t.Errorf("USAQICategory(%v) = %q, want %q", c.aqi, got, c.category)
		}
		if got := USAQISeverity(c.aqi); got != c.severity {
			t.Errorf("USAQISeverity(%v) = %v, want %v", c.aqi, got, c.severity)
		}
	}
}

func TestOpenMeteoFetchBuildsQuery(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = w.Write([]byte(openMeteoFixture))
	}))
	defer srv.Close()

	client := OpenMeteo{Base: srv.URL}
	if _, err := client.Fetch(context.Background(), -37.8136, 144.9631); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	q, err := parseQuery(got)
	if err != nil {
		t.Fatal(err)
	}
	if q["latitude"] != "-37.8136" || q["longitude"] != "144.9631" {
		t.Errorf("coords = %q,%q", q["latitude"], q["longitude"])
	}
	if q["timezone"] != "auto" {
		t.Errorf("timezone = %q", q["timezone"])
	}
	// UV and the gases must both be requested, or the provider has no reason
	// to exist.
	for _, want := range []string{"uv_index", "nitrogen_dioxide", "ozone", "us_aqi"} {
		if !contains(splitComma(q["current"]), want) {
			t.Errorf("current = %q, missing %q", q["current"], want)
		}
	}
	if q["daily"] != "uv_index_max" {
		t.Errorf("daily = %q", q["daily"])
	}
}

func TestOpenMeteoErrorSurfacesReason(t *testing.T) {
	body := `{"error":true,"reason":"Invalid value: Cannot initialize ForecastVariableDaily from invalid String value pm2_5_max"}`
	_, err := ParseOpenMeteo([]byte(body))
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := "pm2_5_max"; !containsString(err.Error(), want) {
		t.Errorf("err = %v, want it to name %q", err, want)
	}
}

func TestOpenMeteoNoReadingsIsUnavailable(t *testing.T) {
	_, err := ParseOpenMeteo([]byte(`{"current":{"time":"2026-10-31T19:00"}}`))
	if err == nil {
		t.Fatal("expected an error for an empty response")
	}
}

func TestOpenMeteoEmptyResponseIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := OpenMeteo{Base: srv.URL}.Fetch(context.Background(), -37.8, 144.9)
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestFormatUV(t *testing.T) {
	cases := map[float64]string{0: "0.0", 4.9: "4.9", 9.95: "10", 11: "11", 14.2: "14"}
	for uv, want := range cases {
		if got := formatUV(uv); got != want {
			t.Errorf("formatUV(%v) = %q, want %q", uv, got, want)
		}
	}
}

// parseQuery splits an encoded query string without pulling in net/url's
// stricter parsing, which would reject the deliberately odd test values.
func parseQuery(raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range splitAmp(raw) {
		if pair == "" {
			continue
		}
		key, value, ok := cutEq(pair)
		if !ok {
			continue
		}
		out[decode(key)] = decode(value)
	}
	return out, nil
}

func splitAmp(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func cutEq(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func decode(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			out = append(out, ' ')
		case '%':
			if i+2 < len(s) {
				out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
				i += 2
			}
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func containsString(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
