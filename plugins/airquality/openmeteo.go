package airquality

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// OpenMeteoBase is the keyless global air-quality endpoint. It needs no
// registration or key and supplies the model fallback, UV index and gaseous
// pollutants that EPA Victoria's mirror omits.
const OpenMeteoBase = "https://air-quality-api.open-meteo.com/v1/air-quality"

// OpenMeteoAttribution is what the model data requires on screen. CAMS is the
// Copernicus Atmosphere Monitoring Service that supplies the forecast.
const OpenMeteoAttribution = "Air quality and UV: CAMS ENSEMBLE via Open-Meteo"

// UV categories. Open-Meteo publishes the number; the bands are the WHO /
// Bureau of Meteorology exposure scale, which is what an Australian reader
// recognises. They are not in the AirHealth metric tree because AirHealth does
// not publish UV at all.
const (
	uvLow        = "Low"
	uvModerate   = "Moderate"
	uvHigh       = "High"
	uvVeryHigh   = "Very High"
	uvExtremeUV  = "Extreme"
	uvLowCeiling = 3.0
	uvModCeiling = 6.0
	uvHighCeil   = 8.0
	uvVeryCeil   = 11.0
)

// OpenMeteo fetches model air quality and UV for a coordinate.
type OpenMeteo struct {
	// Base overrides the endpoint in tests.
	Base string
	// Do performs the request; nil means http.DefaultClient.
	Do Doer
	// Now supplies the clock in tests.
	Now func() time.Time
}

// Current and daily variables requested. The daily maximum makes a same-day UV
// peak available even though the current reading is only a point in time.
const (
	openMeteoCurrent = "pm10,pm2_5,carbon_monoxide,nitrogen_dioxide,sulphur_dioxide,ozone,uv_index,us_aqi"
	openMeteoDaily   = "uv_index_max"
)

// Fetch returns the model measures for a location.
//
// The result is deliberately partial: it carries air quality and UV, and
// nothing about pollen, because Open-Meteo's pollen layers are Europe-only.
// Merging is the coordinator's job.
func (o OpenMeteo) Fetch(ctx context.Context, lat, lon float64) ([]Measure, error) {
	q := url.Values{}
	q.Set("latitude", trimFloat(lat))
	q.Set("longitude", trimFloat(lon))
	q.Set("current", openMeteoCurrent)
	q.Set("daily", openMeteoDaily)
	q.Set("timezone", "auto")
	q.Set("forecast_days", "4")

	base := o.Base
	if base == "" {
		base = OpenMeteoBase
	}
	endpoint := base + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	do := o.Do
	if do == nil {
		do = http.DefaultClient
	}
	resp, err := do.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusError("open-meteo", resp); err != nil {
		return nil, err
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	return ParseOpenMeteo(body)
}

// wireOpenMeteo is the response shape. Every number is a pointer because the
// model omits variables it cannot resolve, and a missing reading must not be
// read as zero — a UV index of 0 and an absent UV index mean very different
// things at 9am.
type wireOpenMeteo struct {
	// Reason carries the human text of an Open-Meteo error; the companion
	// "error" field is a bool, not an object.
	Reason  string `json:"reason"`
	Failed  bool   `json:"error"`
	Current struct {
		Time            string   `json:"time"`
		PM10            *float64 `json:"pm10"`
		PM25            *float64 `json:"pm2_5"`
		CarbonMonoxide  *float64 `json:"carbon_monoxide"`
		NitrogenDioxide *float64 `json:"nitrogen_dioxide"`
		SulphurDioxide  *float64 `json:"sulphur_dioxide"`
		Ozone           *float64 `json:"ozone"`
		UVIndex         *float64 `json:"uv_index"`
		USAQI           *float64 `json:"us_aqi"`
	} `json:"current"`
	Daily struct {
		Time      []string   `json:"time"`
		UVIndexIn []*float64 `json:"uv_index_max"`
	} `json:"daily"`
}

// ParseOpenMeteo turns a response body into measures.
//
// Air quality is reported as the US AQI, because that is the only consolidated
// index Open-Meteo publishes and it is the one its documentation defines. The
// individual concentrations ride along as the breakdown, which is where the
// detail actually is: the AQI says "moderate", the breakdown says it is PM2.5
// and not ozone.
func ParseOpenMeteo(body []byte) ([]Measure, error) {
	var w wireOpenMeteo
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("open-meteo: %w", err)
	}
	if w.Failed {
		reason := w.Reason
		if reason == "" {
			reason = "request rejected"
		}
		return nil, fmt.Errorf("open-meteo: %s", reason)
	}

	var out []Measure

	if m, ok := openMeteoAir(w); ok {
		out = append(out, m)
	}
	if m, ok := openMeteoUV(w); ok {
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: open-meteo returned no readings", ErrUnavailable)
	}
	return out, nil
}

// openMeteoAir builds the air quality row from the AQI plus its constituents.
func openMeteoAir(w wireOpenMeteo) (Measure, bool) {
	var parts []Breakdown
	add := func(name string, v *float64, units string) {
		if v == nil {
			return
		}
		parts = append(parts, Breakdown{
			Name:   name,
			Level:  Level{Name: fmt.Sprintf("%.1f", *v), Severity: SeverityGood},
			Metric: &Metric{Subcat: name, Value: *v, Units: units},
		})
	}
	// Particulates lead the breakdown; gases follow in the order the EPA reports them.
	add("PM2.5", w.Current.PM25, "µg/m³")
	add("PM10", w.Current.PM10, "µg/m³")
	add("O3", w.Current.Ozone, "µg/m³")
	add("NO2", w.Current.NitrogenDioxide, "µg/m³")
	add("SO2", w.Current.SulphurDioxide, "µg/m³")
	add("CO", w.Current.CarbonMonoxide, "µg/m³")

	aqi := w.Current.USAQI
	if aqi == nil && len(parts) == 0 {
		return Measure{}, false
	}

	m := Measure{
		Name:      MeasureAirQuality,
		Title:     Title(MeasureAirQuality),
		Breakdown: parts,
		Source:    "open-meteo",
	}
	if aqi != nil {
		name := USAQICategory(*aqi)
		m.Level = Level{Name: name, Index: USAQIIndex(*aqi), Severity: USAQISeverity(*aqi)}
		m.Metric = &Metric{Subcat: "US AQI", Value: *aqi, Units: ""}
		m.Max = 500
		m.Bands = usaqiBands()
	} else if len(parts) > 0 {
		// No AQI but concentrations present: report the worst particulate so
		// the row still ranks, rather than inventing an index.
		m.Level = Level{Name: parts[0].Level.Name, Severity: SeverityGood}
		m.Metric = parts[0].Metric
	}
	return m, true
}

// usaqiBands puts the US AQI severity thresholds on the same three-step arc as
// the other measures. The row and gauge centre retain the full AQI category.
func usaqiBands() []Band {
	return []Band{
		{Label: "Good", Until: 1.0 / 3, Ceiling: 50, Severity: SeverityGood},
		{Label: "Moderate", Until: 2.0 / 3, Ceiling: 100, Severity: SeverityModerate},
		{Label: "High", Until: 1, Ceiling: 500, Severity: SeverityHigh},
	}
}

// openMeteoUV builds the UV row, preferring today's peak over the current
// instantaneous value.
//
// A current UV reading is mostly a statement about the clock: it is near zero
// at breakfast and after dinner whatever the day holds, so a panel that shows
// only "now" tells a reader nothing about whether to take a hat. The daily
// maximum is the number the Bureau of Meteorology publishes and the number
// people act on, so it is the headline, with "now" kept beside it.
func openMeteoUV(w wireOpenMeteo) (Measure, bool) {
	peak, peakOK := todaysUVMax(w)
	now := w.Current.UVIndex

	switch {
	case peakOK:
		m := Measure{
			Name:   MeasureUV,
			Title:  Title(MeasureUV),
			Level:  UVLevel(peak),
			Metric: &Metric{Subcat: "today's peak", Value: peak, Units: ""},
			Source: "open-meteo",
			Max:    uvDialMax,
			Bands:  UVBands(),
		}
		if now != nil {
			m.Breakdown = []Breakdown{{
				Name:   "now",
				Level:  UVLevel(*now),
				Metric: &Metric{Subcat: "now", Value: *now, Units: ""},
			}}
		}
		return m, true
	case now != nil:
		return Measure{
			Max:    uvDialMax,
			Bands:  UVBands(),
			Name:   MeasureUV,
			Title:  Title(MeasureUV),
			Level:  UVLevel(*now),
			Metric: &Metric{Subcat: "now", Value: *now, Units: ""},
			Source: "open-meteo",
		}, true
	default:
		return Measure{}, false
	}
}

// todaysUVMax returns the first daily maximum, which is today in the local
// timezone the request asked for.
func todaysUVMax(w wireOpenMeteo) (float64, bool) {
	if len(w.Daily.UVIndexIn) == 0 || w.Daily.UVIndexIn[0] == nil {
		return 0, false
	}
	return *w.Daily.UVIndexIn[0], true
}

// UVLevel maps a UV index onto the standard exposure bands.
func UVLevel(uv float64) Level {
	name, severity := uvLow, SeverityGood
	switch {
	case uv < uvLowCeiling:
		name, severity = uvLow, SeverityGood
	case uv < uvModCeiling:
		name, severity = uvModerate, SeverityModerate
	case uv < uvHighCeil:
		name, severity = uvHigh, SeverityHigh
	case uv < uvVeryCeil:
		name, severity = uvVeryHigh, SeverityHigh
	default:
		name, severity = uvExtremeUV, SeverityHigh
	}
	return Level{Name: name, Index: int(uv), Severity: severity}
}

// USAQICategory names a US AQI band. The bands are the EPA's own, which is
// what Open-Meteo documents for this field.
func USAQICategory(aqi float64) string {
	switch {
	case aqi <= 50:
		return "Good"
	case aqi <= 100:
		return "Moderate"
	case aqi <= 150:
		return "Unhealthy for Sensitive Groups"
	case aqi <= 200:
		return "Unhealthy"
	case aqi <= 300:
		return "Very Unhealthy"
	default:
		return "Hazardous"
	}
}

// USAQIIndex is the zero-based band number, matching how every other measure
// in this plugin indexes its categories.
func USAQIIndex(aqi float64) int {
	switch {
	case aqi <= 50:
		return 0
	case aqi <= 100:
		return 1
	case aqi <= 150:
		return 2
	case aqi <= 200:
		return 3
	case aqi <= 300:
		return 4
	default:
		return 5
	}
}

// USAQISeverity folds the six AQI bands onto the plugin's three-step axis.
// "Unhealthy for Sensitive Groups" is where an asthmatic is told to act, so it
// is already High.
func USAQISeverity(aqi float64) Severity {
	switch {
	case aqi <= 50:
		return SeverityGood
	case aqi <= 100:
		return SeverityModerate
	default:
		return SeverityHigh
	}
}

// formatUV renders a UV index the way the scale is usually quoted: one
// decimal below 10, whole above it.
//
// The rounding happens before the width choice, so 9.97 becomes "10" rather
// than "10.0" -- deciding on the unrounded value would print a decimal for a
// number that has already crossed into the integer range.
func formatUV(uv float64) string {
	rounded := math.Round(uv*10) / 10
	if rounded >= 10 {
		return strconv.FormatFloat(rounded, 'f', 0, 64)
	}
	return strconv.FormatFloat(rounded, 'f', 1, 64)
}

// uvDialMax is the top of the UV dial's scale. The published bands run to 11
// (Extreme), and ultraviolet index does continue above it, so the arc keeps a
// little headroom rather than pinning a 12 at the end stop.
const uvDialMax = uvVeryCeil

// UVBands are the published ultraviolet index thresholds as dial zones.
//
// UV is not the same kind of scale as a concentration: the step from 2 to 3
// matters (it is the step from "no protection needed" to "protection needed")
// while the step from 9 to 10 does not. The zones are uneven for that reason,
// and the third zone absorbs Very High and Extreme because both mean the same
// thing to a person: stay out of the sun.
func UVBands() []Band {
	return []Band{
		{Label: "Low", Until: 1.0 / 3, Ceiling: uvLowCeiling, Severity: SeverityGood},
		{Label: "Moderate", Until: 2.0 / 3, Ceiling: uvModCeiling, Severity: SeverityModerate},
		{Label: "High", Until: 1, Ceiling: uvVeryCeil, Severity: SeverityHigh},
	}
}
