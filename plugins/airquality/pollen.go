package airquality

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"
)

// openMeteoPollen lists the CAMS European pollen species Open-Meteo serves,
// in the order the allergen breakdown shows them. Outside the European
// domain every value is null, which is how coverage is detected.
var openMeteoPollen = []struct {
	key, name string
	// ceilings are the exclusive upper bounds of Low, Moderate and High in
	// mean daily grains/m³, from MeteoSwiss's per-species load classes
	// ("Belastungsklassen der allergenen Pollenarten"). MeteoSwiss publishes
	// no olive classes, so olive uses birch's, the other wind-pollinated tree.
	ceilings [3]float64
}{
	{"grass_pollen", "Grass", [3]float64{20, 50, 150}},
	{"birch_pollen", "Birch", [3]float64{11, 70, 300}},
	{"alder_pollen", "Alder", [3]float64{11, 70, 250}},
	{"olive_pollen", "Olive", [3]float64{11, 70, 300}},
	{"mugwort_pollen", "Mugwort", [3]float64{6, 15, 50}},
	{"ragweed_pollen", "Ragweed", [3]float64{6, 11, 40}},
}

const openMeteoPollenHourly = "grass_pollen,birch_pollen,alder_pollen,olive_pollen,mugwort_pollen,ragweed_pollen"

const pollenUnits = "grains/m³"

// pollenLevel places a daily mean on a species' load classes. Below one grain
// per cubic metre MeteoSwiss reports no load, which reads as "None".
func pollenLevel(mean float64, ceilings [3]float64) Level {
	switch {
	case mean < 1:
		return Level{Name: "None", Index: 0, Severity: SeverityGood}
	case mean < ceilings[0]:
		return Level{Name: "Low", Index: 1, Severity: SeverityGood}
	case mean < ceilings[1]:
		return Level{Name: "Moderate", Index: 2, Severity: SeverityModerate}
	case mean < ceilings[2]:
		return Level{Name: "High", Index: 3, Severity: SeverityHigh}
	default:
		return Level{Name: "Very High", Index: 4, Severity: SeverityHigh}
	}
}

// pollenBands are a species' load classes as dial zones. The open top class
// gets half a zone of headroom, as the published metric tree's sentinel does.
func pollenBands(ceilings [3]float64) ([]Band, float64) {
	top := ceilings[2] * 1.5
	return []Band{
		{Label: "Low", Until: 0.25, Ceiling: ceilings[0], Severity: SeverityGood},
		{Label: "Moderate", Until: 0.5, Ceiling: ceilings[1], Severity: SeverityModerate},
		{Label: "High", Until: 0.75, Ceiling: ceilings[2], Severity: SeverityHigh},
		{Label: "Very High", Until: 1, Ceiling: top, Severity: SeverityHigh},
	}, top
}

// ParseOpenMeteoPollen turns the hourly pollen series into today's grass and
// allergen rows plus forecast days. Each day is the mean of its local hours,
// which is what the load classes are defined on. It returns nothing where
// the model has no coverage.
func ParseOpenMeteoPollen(body []byte) ([]Measure, []Day, error) {
	var wire struct {
		Hourly map[string]json.RawMessage `json:"hourly"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, fmt.Errorf("open-meteo pollen: %w", err)
	}
	var times []string
	if raw, ok := wire.Hourly["time"]; ok {
		if err := json.Unmarshal(raw, &times); err != nil {
			return nil, nil, fmt.Errorf("open-meteo pollen: %w", err)
		}
	}
	// means[date][species] is the day's mean over the hours that carry a value.
	var dates []string
	means := map[string]map[string]float64{}
	for _, species := range openMeteoPollen {
		raw, ok := wire.Hourly[species.key]
		if !ok {
			continue
		}
		var values []*float64
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, nil, fmt.Errorf("open-meteo pollen: %w", err)
		}
		sums, counts := map[string]float64{}, map[string]int{}
		for i, v := range values {
			if v == nil || i >= len(times) || len(times[i]) < 10 {
				continue
			}
			date := times[i][:10]
			sums[date] += *v
			counts[date]++
		}
		for date, n := range counts {
			if means[date] == nil {
				means[date] = map[string]float64{}
				dates = append(dates, date)
			}
			means[date][species.key] = sums[date] / float64(n)
		}
	}
	if len(dates) == 0 {
		return nil, nil, nil
	}
	slices.Sort(dates)

	today := pollenMeasures(means[dates[0]])
	var forecast []Day
	for _, date := range dates[1:] {
		day := Day{Date: date, Title: weekday(date), Measures: map[string]Measure{}}
		for _, m := range pollenMeasures(means[date]) {
			day.Measures[m.Name] = m
		}
		forecast = append(forecast, day)
	}
	return today, forecast, nil
}

// pollenMeasures builds the grass row and the allergen row, whose level is
// its worst species and whose breakdown lists every species with a value.
func pollenMeasures(day map[string]float64) []Measure {
	var out []Measure
	var allergens *Measure
	for _, species := range openMeteoPollen {
		mean, ok := day[species.key]
		if !ok {
			continue
		}
		mean = math.Round(mean*10) / 10
		level := pollenLevel(mean, species.ceilings)
		metric := &Metric{Subcat: species.name, Value: mean, Units: pollenUnits}
		if species.key == "grass_pollen" {
			bands, top := pollenBands(species.ceilings)
			out = append(out, Measure{
				Name: MeasureGrass, Title: Title(MeasureGrass), Level: level, Metric: metric,
				Bands: bands, Max: top, Source: "open-meteo",
			})
			continue
		}
		if allergens == nil {
			allergens = &Measure{Name: MeasureAllergens, Title: Title(MeasureAllergens), Source: "open-meteo"}
		}
		allergens.Breakdown = append(allergens.Breakdown, Breakdown{Name: species.name, Level: level, Metric: metric})
		// The row reports its worst species; a tie keeps the higher index.
		if allergens.Metric == nil || level.Index > allergens.Level.Index {
			allergens.Level, allergens.Metric = level, metric
		}
	}
	if allergens != nil {
		out = append(out, *allergens)
	}
	return out
}

// weekday labels a forecast date the way AirHealth's forecast does.
func weekday(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Weekday().String()
}
