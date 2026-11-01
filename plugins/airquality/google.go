package airquality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GooglePollenBase is the Pollen API forecast endpoint. It needs a Google
// Cloud key with the Pollen API enabled; the plugin calls it only when the
// user has entered one.
const GooglePollenBase = "https://pollen.googleapis.com/v1/forecast:lookup"

// GoogleAttribution is the wording Google's Pollen API policies require on
// or next to its data.
const GoogleAttribution = "Source: Includes pollen data from Google."

// GooglePollen fetches the Universal Pollen Index forecast. Google's terms
// prohibit caching and storing the data, so its rows are drawn from the live
// answer only: EncodeReport drops them and a failed refresh never carries
// them over.
type GooglePollen struct {
	// Key is the user's API key; empty disables the source.
	Key string
	// Base overrides the endpoint in tests.
	Base string
	// Do sends requests; nil means http.DefaultClient.
	Do Doer
}

// Fetch returns today's grass and allergen rows and the following days.
func (g GooglePollen) Fetch(ctx context.Context, lat, lon float64) ([]Measure, []Day, error) {
	q := url.Values{}
	q.Set("location.latitude", trimFloat(lat))
	q.Set("location.longitude", trimFloat(lon))
	q.Set("days", "4")
	q.Set("plantsDescription", "false")
	base := g.Base
	if base == "" {
		base = GooglePollenBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	// The key travels in a header so it never appears in a URL, and so never
	// in an error message the panel might show.
	req.Header.Set("X-Goog-Api-Key", g.Key)
	do := g.Do
	if do == nil {
		do = http.DefaultClient
	}
	resp, err := do.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(string(body)), "not supported") {
		// The API answers 400 for a region it does not cover.
		return nil, nil, fmt.Errorf("%w: google pollen does not cover this location", ErrNoCoverage)
	}
	if err := statusError("google pollen", resp); err != nil {
		return nil, nil, err
	}
	return ParseGooglePollen(body)
}

type wireGoogleIndex struct {
	Value    int    `json:"value"`
	Category string `json:"category"`
}

type wireGoogleEntry struct {
	Code        string           `json:"code"`
	DisplayName string           `json:"displayName"`
	InSeason    bool             `json:"inSeason"`
	IndexInfo   *wireGoogleIndex `json:"indexInfo"`
}

type wireGooglePollen struct {
	DailyInfo []struct {
		Date struct {
			Year  int `json:"year"`
			Month int `json:"month"`
			Day   int `json:"day"`
		} `json:"date"`
		PollenTypeInfo []wireGoogleEntry `json:"pollenTypeInfo"`
		PlantInfo      []wireGoogleEntry `json:"plantInfo"`
	} `json:"dailyInfo"`
}

// ParseGooglePollen maps the GRASS type onto the grass row and the worse of
// TREE and WEED onto the allergen row, with the in-season plants as its
// breakdown.
func ParseGooglePollen(body []byte) ([]Measure, []Day, error) {
	var wire wireGooglePollen
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, fmt.Errorf("google pollen: %w", err)
	}
	if len(wire.DailyInfo) == 0 {
		return nil, nil, fmt.Errorf("%w: google pollen returned no days", ErrUnavailable)
	}
	var today []Measure
	var forecast []Day
	for i, info := range wire.DailyInfo {
		var measures []Measure
		var allergens *Measure
		for _, entry := range info.PollenTypeInfo {
			level := upiLevel(entry.IndexInfo)
			switch entry.Code {
			case "GRASS":
				measures = append(measures, Measure{
					Name: MeasureGrass, Title: Title(MeasureGrass), Level: level,
					Metric: &Metric{Subcat: "UPI", Value: float64(level.Index)},
					Bands:  upiBands(), Max: upiDialMax, Source: "google",
				})
			case "TREE", "WEED":
				if allergens == nil || level.Index > allergens.Level.Index {
					allergens = &Measure{
						Name: MeasureAllergens, Title: Title(MeasureAllergens), Level: level,
						Metric: &Metric{Subcat: "UPI", Value: float64(level.Index)}, Source: "google",
					}
				}
			}
		}
		if allergens != nil {
			for _, plant := range info.PlantInfo {
				if plant.IndexInfo == nil || plant.IndexInfo.Value == 0 {
					continue
				}
				allergens.Breakdown = append(allergens.Breakdown, Breakdown{
					Name: plant.DisplayName, Level: upiLevel(plant.IndexInfo),
				})
			}
			measures = append(measures, *allergens)
		}
		if i == 0 {
			today = measures
			continue
		}
		date := fmt.Sprintf("%04d-%02d-%02d", info.Date.Year, info.Date.Month, info.Date.Day)
		day := Day{Date: date, Title: weekday(date), Measures: map[string]Measure{}}
		for _, m := range measures {
			day.Measures[m.Name] = m
		}
		forecast = append(forecast, day)
	}
	return today, forecast, nil
}

// upiLevel names a Universal Pollen Index reading. Google omits the index for
// a type or plant that is out of season with a low count, which is "None".
func upiLevel(index *wireGoogleIndex) Level {
	if index == nil {
		return Level{Name: "None", Index: 0, Severity: SeverityGood}
	}
	name := strings.TrimSpace(index.Category)
	if name == "" {
		name = "None"
	}
	severity := SeverityGood
	switch {
	case index.Value >= 4:
		severity = SeverityHigh
	case index.Value == 3:
		severity = SeverityModerate
	}
	return Level{Name: name, Index: index.Value, Severity: severity}
}

// upiDialMax puts the integer index values in the middle of their zones.
const upiDialMax = 5.5

// upiBands fold the index's six steps onto the plugin's three severities:
// None to Low is nothing to act on, Moderate is a sensitive person's step,
// and High or Very High is act on it.
func upiBands() []Band {
	return []Band{
		{Label: "Low", Until: 1.0 / 3, Ceiling: 2.5, Severity: SeverityGood},
		{Label: "Moderate", Until: 2.0 / 3, Ceiling: 3.5, Severity: SeverityModerate},
		{Label: "High", Until: 1, Ceiling: upiDialMax, Severity: SeverityHigh},
	}
}
