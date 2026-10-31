package airquality

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Class is one band of a metric's published scale.
//
// UpperBound is an exclusive ceiling: a value strictly below it is in this
// class. The bands arrive already sorted and contiguous, so a value is placed
// by the first band whose ceiling it is under.
type Class struct {
	// Name is the published category, such as "Fair" or "Extreme".
	Name string
	// LowerBound and UpperBound delimit the band. UpperBound is exclusive.
	LowerBound float64
	UpperBound float64
	// WarnLevel is the source's own normalised severity for this band, which
	// is what lets one severity axis cover scales that invert.
	WarnLevel int
}

// Metric tree wire shapes. The tree is measure → group → metric → scale, and
// the group level exists only on the Air Quality branch (Gasses, Particles),
// so a decoder that assumes two levels drops every air quality breakpoint.
type (
	wiredTree map[string]map[string]map[string]wireScale
	wireScale struct {
		TotalBoxes int         `json:"total_boxes"`
		Classes    []wireClass `json:"classes"`
	}
	wireClass struct {
		Name       string  `json:"name"`
		LowerBound float64 `json:"lower_bound"`
		UpperBound float64 `json:"upper_bound"`
		WarnLevel  int     `json:"warn_level"`
	}
)

// Tree holds every published scale, keyed by canonical measure then metric.
//
// A measure with a single scale uses the measure's own name as the metric key,
// so Lookup("Grass", "Grass") and Lookup("Grass", "") both work.
type Tree struct {
	scales map[string]map[string][]Class
}

// ParseTree decodes the metric tree payload.
func ParseTree(raw []byte) (*Tree, error) {
	var wire wiredTree
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("metric tree: %w", err)
	}
	if len(wire) == 0 {
		return nil, fmt.Errorf("metric tree: no measures")
	}
	t := &Tree{scales: map[string]map[string][]Class{}}
	for measure, groups := range wire {
		name := CanonicalMeasure(measure)
		if t.scales[name] == nil {
			t.scales[name] = map[string][]Class{}
		}
		for _, metrics := range groups {
			for metric, scale := range metrics {
				classes := make([]Class, 0, len(scale.Classes))
				for _, c := range scale.Classes {
					classes = append(classes, Class(c))
				}
				// Sort rather than trust wire order: the Air Quality branch
				// publishes NO2 with a zero-width "Very Good" band first, and
				// placement by first-match needs ascending ceilings.
				sort.SliceStable(classes, func(i, j int) bool {
					return classes[i].UpperBound < classes[j].UpperBound
				})
				key := CanonicalMeasure(metric)
				t.scales[name][key] = classes
			}
		}
	}
	return t, nil
}

// Lookup returns the scale for a measure and metric. An empty metric means
// "the measure's only scale", which is how single-scale measures are stored.
func (t *Tree) Lookup(measure, metric string) ([]Class, bool) {
	if t == nil {
		return nil, false
	}
	byMetric, ok := t.scales[CanonicalMeasure(measure)]
	if !ok {
		return nil, false
	}
	if metric != "" {
		if classes, ok := byMetric[CanonicalMeasure(metric)]; ok {
			return classes, true
		}
	}
	if len(byMetric) == 1 {
		for _, classes := range byMetric {
			return classes, true
		}
	}
	if classes, ok := byMetric[CanonicalMeasure(measure)]; ok {
		return classes, true
	}
	return nil, false
}

// Classify places a value on the scale. It returns the band and whether the
// value is below every published ceiling; a value past the last ceiling takes
// the last band, which the source expresses as a very large upper bound rather
// than an open interval.
func (t *Tree) Classify(measure, metric string, value float64) (Class, bool) {
	classes, ok := t.Lookup(measure, metric)
	if !ok || len(classes) == 0 {
		return Class{}, false
	}
	for _, c := range classes {
		if value < c.UpperBound {
			return c, true
		}
	}
	return classes[len(classes)-1], true
}

// SeverityOf maps a warn level onto the shared axis. Levels above the
// published maximum clamp rather than inventing a fourth step.
func SeverityOf(warnLevel int) Severity {
	switch {
	case warnLevel <= 0:
		return SeverityGood
	case warnLevel == 1:
		return SeverityModerate
	default:
		return SeverityHigh
	}
}

// Measures lists the canonical measures the tree carries, in MeasureOrder.
func (t *Tree) Measures() []string {
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.scales))
	for _, name := range MeasureOrder {
		if _, ok := t.scales[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// Phrases is the sentence the source pairs with each category, keyed by
// measure then lowercased category. It is empty unless the phrases endpoint
// was fetched; the panel falls back to the category name alone.
type Phrases map[string]map[string]string

// Phrase returns the explanation for a category, or "".
func (p Phrases) Phrase(measure, class string) string {
	byClass, ok := p[CanonicalMeasure(measure)]
	if !ok {
		return ""
	}
	return byClass[strings.ToLower(strings.TrimSpace(class))]
}

// Dial returns the severity zones of a measure's numeric scale.
//
// Two things make the published tree unpaintable as-is. Its final class is an
// open interval expressed as a sentinel ceiling (grass pollen tops out at two
// million grains per cubic metre), and some scales open with a degenerate
// zero-width class (nitrogen dioxide's "Very Good" ends at zero).
//
// The zones are also not laid out linearly along the arc. A PM2.5 dial with
// linear thresholds spends its first five percent on "Very Good" and its last
// two thirds on "Very Poor", so an unremarkable reading sits inside a dial that
// is almost entirely red. The arc is therefore divided into equal zones, one
// per published class, and each zone records the reading at which it ends. The
// needle still interpolates within its zone, so the number and the arc agree.
func (t *Tree) Dial(measure, metric string) ([]Band, float64, bool) {
	classes, ok := t.Lookup(measure, metric)
	if !ok {
		return nil, 0, false
	}
	// Drop a degenerate opening class: a ceiling of zero is not a zone.
	filtered := make([]Class, 0, len(classes))
	for _, c := range classes {
		if c.UpperBound > 0 {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) < 2 {
		return nil, 0, false
	}

	// The final ceiling is either a real threshold or an open interval
	// expressed as a sentinel. A sentinel is recognised by outrunning its
	// neighbour by two orders of magnitude, which works for grains and
	// micrograms alike without hardcoding either unit.
	last := filtered[len(filtered)-1]
	prev := filtered[len(filtered)-2].UpperBound
	openEnded := prev <= 0 || last.UpperBound > prev*100

	ceiling := last.UpperBound
	if openEnded {
		// An open interval needs somewhere finite to measure against. A zone
		// and a half above the last real threshold keeps the needle off the
		// end stop for a reading that is merely past everything.
		ceiling = prev * 1.5
	}
	if ceiling <= 0 {
		return nil, 0, false
	}

	bands := make([]Band, 0, len(filtered))
	for i, c := range filtered {
		band := Band{
			Label:    c.Name,
			Until:    float64(i+1) / float64(len(filtered)),
			Ceiling:  c.UpperBound,
			Severity: SeverityOf(c.WarnLevel),
		}
		if i == len(filtered)-1 && openEnded {
			band.Ceiling = ceiling
		}
		bands = append(bands, band)
	}
	bands[len(bands)-1].Until = 1
	return bands, ceiling, true
}

// Place returns where a reading sits on the dial, as a fraction of the arc.
//
// It interpolates inside the zone the reading landed in rather than treating
// the zone as a step, so two readings in the same category still differ, and
// a reading past the last threshold pins to the end.
func Place(bands []Band, value float64) float64 {
	if len(bands) == 0 {
		return 0
	}
	floorReading, floorArc := 0.0, 0.0
	for _, band := range bands {
		if value < band.Ceiling || band.Until >= 1 {
			span := band.Ceiling - floorReading
			if span <= 0 {
				return band.Until
			}
			within := min(max((value-floorReading)/span, 0), 1)
			return floorArc + within*(band.Until-floorArc)
		}
		floorReading, floorArc = band.Ceiling, band.Until
	}
	return 1
}

// AttachDial adds the published category bands, plus a numeric ceiling when
// the measure carries a reading on that scale. Grass can carry bands alone
// because AirHealth publishes its category without a concentration.
func (t *Tree) AttachDial(m *Measure) {
	if m == nil || m.Max > 0 {
		return
	}
	if m.Name == MeasureThunderstorm {
		// Thunderstorm asthma publishes ordinal descriptors rather than a
		// concentration, so a dial would imply a precision the forecast does
		// not have. It is an alert, and the panel shows it as one.
		return
	}
	metric := ""
	if m.Metric == nil {
		if m.Name != MeasureGrass {
			return
		}
	} else {
		metric = m.Metric.Subcat
		if metric == "" || metric == "US AQI" {
			// A US AQI is its own scale, not the EPA one the tree publishes, so
			// borrowing the tree's PM2.5 zones would put the zones in the wrong
			// place.
			return
		}
	}
	bands, max, ok := t.Dial(m.Name, metric)
	if !ok {
		return
	}
	m.Bands = bands
	if m.Metric != nil {
		m.Max = max
	}
}
