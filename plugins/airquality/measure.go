// Package airquality fetches worldwide model air quality and UV, plus regional
// pollen and station data for sysc-shell.
//
// The data comes from three providers behind one snapshot type: the
// AirHealth service behind several regional pollen apps, EPA Victoria's air
// monitoring network, and Open-Meteo's global model. AirHealth supplies pollen
// categories; EPA is the authority on Victorian particulate readings.
package airquality

import "strings"

// Severity is the normalised warning axis shared by every measure. It is the
// only axis that is comparable across measures: the Air Quality scale runs
// Very Good..Extremely Poor, the pollen scales run Low..Extreme, and the raw
// value indexes are not comparable at all (Air Quality is inverted, so a
// higher raw value is *better*).
//
// AirHealth publishes warn_level per category in its metric tree, and that is
// what this maps onto. Three steps are enough for a bar colour and a dial: a
// measure above SeverityHigh has nothing more to say by climbing further.
type Severity int

const (
	// SeverityGood is the "nothing to act on" step.
	SeverityGood Severity = 0
	// SeverityModerate is the "sensitive people should notice" step.
	SeverityModerate Severity = 1
	// SeverityHigh is the "act on this" step.
	SeverityHigh Severity = 2
)

// String names a severity for tests and tooltips.
func (s Severity) String() string {
	switch s {
	case SeverityGood:
		return "good"
	case SeverityModerate:
		return "moderate"
	case SeverityHigh:
		return "high"
	default:
		return "unknown"
	}
}

// Canonical measure names. Every payload name is mapped onto one of these, so
// lookups never depend on how a particular endpoint happens to spell a row.
const (
	MeasureGrass        = "Grass"
	MeasureAllergens    = "Other Allergens"
	MeasureAirQuality   = "Air Quality"
	MeasureUV           = "UV Index"
	MeasureThunderstorm = "Thunderstorm Asthma"
	MeasureWoodsmoke    = "Woodsmoke"
)

// MeasureOrder is the order measures are presented in. It is roughly
// "what the user came for" first: grass pollen is the headline for a hay
// fever sufferer, then the air they are breathing, then sun exposure, then
// the seasonal risks.
var MeasureOrder = []string{
	MeasureGrass,
	MeasureAllergens,
	MeasureAirQuality,
	MeasureUV,
	MeasureThunderstorm,
	MeasureWoodsmoke,
}

// CanonicalMeasure maps the names a payload uses onto a canonical name.
//
// The forecast series spells thunderstorm asthma "Thunderstorm Asthma Alert"
// while the current series spells it "Thunderstorm Asthma". A lookup keyed on
// the raw name silently drops every forecast thunderstorm row, so every
// payload name goes through here first.
func CanonicalMeasure(name string) string {
	switch strings.TrimSpace(name) {
	case "Thunderstorm Asthma", "Thunderstorm Asthma Alert":
		return MeasureThunderstorm
	case "Other Allergens", "Other Allergen", "OtherAllergens":
		return MeasureAllergens
	case "Air Quality", "AirQuality":
		return MeasureAirQuality
	case "UV Index", "UVIndex", "UV":
		return MeasureUV
	case "Grass", "Grass Pollen", "GrassPollen":
		return MeasureGrass
	case "Woodsmoke", "Wood Smoke":
		return MeasureWoodsmoke
	default:
		return strings.TrimSpace(name)
	}
}

// Title is the human label for a canonical measure.
func Title(measure string) string {
	switch measure {
	case MeasureGrass:
		return "Grass pollen"
	case MeasureAllergens:
		return "Other allergens"
	case MeasureAirQuality:
		return "Air quality"
	case MeasureUV:
		return "UV index"
	case MeasureThunderstorm:
		return "Thunderstorm asthma"
	case MeasureWoodsmoke:
		return "Woodsmoke"
	default:
		return measure
	}
}

// Level is one reported category: the words the source used, the raw index it
// carried, and the normalised severity.
type Level struct {
	// Name is the category as published, such as "High" or "Very Good".
	Name string
	// Index is the raw value from the payload. It orders categories within
	// one measure and means nothing across measures.
	Index int
	// Severity is the shared warning axis.
	Severity Severity
}

// Known reports whether this level carries a published category.
func (l Level) Known() bool {
	switch l.Name {
	case "", "Unknown", "Unavailable", "No Data", "N/A":
		return false
	default:
		return true
	}
}

// Metric is the numeric reading behind a category, when the source supplies
// one. Air Quality reports PM2.5 alongside its category; the pollen measures
// report only a category.
type Metric struct {
	// Subcat names the pollutant, such as "PM2.5".
	Subcat string
	// Value is the concentration.
	Value float64
	// Units is the source's own unit string, such as "ug/m3".
	Units string
}

// Breakdown is one constituent of a measure: a single pollutant within air
// quality, or a single species within the allergen mix. Both sources publish
// these and the official apps show them, so a panel that hides them is
// withholding the most actionable part of the report — "grass is high" is less
// useful than "grass is high, but it is Alternaria that is driving it".
type Breakdown struct {
	// Name is the constituent, such as "PM10", "Birch" or "Alternaria".
	Name string
	// Level is the constituent's own category.
	Level Level
	// Metric is the constituent's numeric reading, or nil.
	Metric *Metric
}

// Measure is one row of the current report.
type Measure struct {
	// Name is canonical; use CanonicalMeasure on anything from the wire.
	Name string
	// Title is the display label.
	Title string
	// Level is the reported category.
	Level Level
	// Metric is the numeric reading, or nil when the source has none.
	Metric *Metric
	// Breakdown holds the constituents behind the summary row, in the
	// source's own order. It is empty when the source published none.
	Breakdown []Breakdown
	// Source records which provider produced this row, so a mixed snapshot
	// can attribute each figure.
	Source string
	// Bands are the published categories painted on a dial. For a numeric
	// measure, Ceiling and Max place its reading; category-only grass pollen
	// keeps Max at zero and uses the band name to place the category midpoint.
	Bands []Band
	// Max is the top of the scale the bands divide. Zero means the measure has
	// no numeric axis and must not be shown on a dial.
	Max float64
}

// Band is one severity zone of a measure's published scale.
type Band struct {
	// Label is the category name published for this zone.
	Label string
	// Until is where the zone ends on the dial arc, as a fraction of the
	// semicircle. Zones are equal width, because the scale is qualitative: the
	// arc answers "how bad" and the number answers "how much". Laying the
	// published thresholds out linearly would give the green zone five percent
	// of the arc and the red zone two thirds of it, so a merely "Fair" reading
	// would sit inside a dial that is almost entirely red.
	Until float64
	// Ceiling is the reading at which this zone ends, in the measure's own
	// units. It is what places the needle inside the zone it landed in.
	Ceiling float64
	// Severity is the normalised alarm level, which is the only axis
	// comparable across measures: air quality counts upward from "Very Good"
	// while everything else counts upward from "Low".
	Severity Severity
}

// Weather is the context the pollen service publishes alongside its
// categories: today's temperature range and expected rain. It matters because
// rain suppresses airborne pollen and wind carries it, so the same "High"
// reading means different things on a still dry day and before a storm front.
type Weather struct {
	// MinC and MaxC are the forecast range in Celsius.
	MinC float64
	MaxC float64
	// RainRange is the source's own text, such as "14.3 mm". It is kept as
	// text because the source does not always publish a parseable number.
	RainRange string
	// RainChance is the probability of rain as a percentage, or -1 when the
	// source published null.
	RainChance float64
}

// Day is one forecast day.
//
// Measures is a map rather than a slice because the forecast is sparse: a day
// may omit air quality or thunderstorm asthma entirely, and a missing row
// means "not forecast", which is not the same as "Low". Callers must test
// presence rather than defaulting.
type Day struct {
	// Date is the forecast date as YYYY-MM-DD.
	Date string
	// Title is the source's label, such as "Later today".
	Title string
	// MaxC and MinC are the forecast temperatures in Celsius.
	MaxC float64
	MinC float64
	// Measures holds only the rows the source actually forecast.
	Measures map[string]Measure
}

// Alert is a standing advisory the source publishes for the location, such as
// the thunderstorm asthma notice.
type Alert struct {
	Title string
	Text  string
	Level Level
}

// Location identifies where a snapshot applies.
type Location struct {
	// Suburb is the resolved suburb name, such as "Melbourne".
	Suburb string
	// State is the abbreviated state, such as "VIC".
	State string
	// Postcode is the postal code when the source resolves one.
	Postcode string
	// SAL and SA3 are the source's statistical area identifiers. They are
	// internal to AirHealth and only used to build follow-up requests.
	SAL string
	SA3 string
}

// Snapshot is one complete report for one location.
type Snapshot struct {
	// Location is the resolved place.
	Location Location
	// Date is the report date as YYYY-MM-DD.
	Date string
	// Title is the source's day label, such as "Later today".
	Title string
	// Advice is the source's short instruction, such as "Take care".
	Advice string
	// Weather is the published temperature and rain context, or nil when the
	// source did not send any.
	Weather *Weather
	// Measures holds the current report, in MeasureOrder.
	Measures []Measure
	// Alert is the standing advisory, or nil when none is active.
	Alert *Alert
	// Forecast holds the forecast days, soonest first.
	Forecast []Day
	// Attribution is the credit the source requires to be displayed.
	Attribution string
}

// Measure returns the named current measure.
func (s Snapshot) Measure(name string) (Measure, bool) {
	for _, m := range s.Measures {
		if m.Name == name {
			return m, true
		}
	}
	return Measure{}, false
}

// Worst returns the highest-severity current measure. It is what a bar widget
// should show when it has room for one sentence: the thing most worth acting
// on. Ties keep MeasureOrder, so grass pollen wins a tie against air quality.
func (s Snapshot) Worst() (Measure, bool) {
	var best Measure
	found := false
	for _, name := range MeasureOrder {
		m, ok := s.Measure(name)
		if !ok || !m.Level.Known() {
			continue
		}
		if !found || m.Level.Severity > best.Level.Severity {
			best, found = m, true
		}
	}
	return best, found
}
