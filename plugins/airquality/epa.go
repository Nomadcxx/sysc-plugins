package airquality

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// EPAArcGIS is the public ArcGIS feature service that mirrors EPA Victoria's
// air monitoring readings. It needs no key and no registration, and its
// licence is CC BY 4.0.
//
// The service is an unofficial mirror: it republishes the same readings as
// EPA's own API, which needs a registered key. The plugin prefers this source
// for air quality because it works out of the box, and the official API can be
// swapped in by registering a key.
type EPAArcGIS struct {
	// Base overrides the service root; empty means the published layer.
	Base string
	// Do sends requests; nil means http.DefaultClient.
	Do Doer
}

// EPAArcGISBase is the published feature layer.
const EPAArcGISBase = "https://services1.arcgis.com/vHnIGBHHqDR6y0CR/arcgis/rest/services/Air_Quality_Latest_Readings/FeatureServer/0/query"

// EPAAttribution is the credit EPA's licence requires.
const EPAAttribution = "Air quality data: EPA Victoria (CC BY 4.0)"

func (e EPAArcGIS) base() string {
	if e.Base != "" {
		return e.Base
	}
	return EPAArcGISBase
}

func (e EPAArcGIS) doer() Doer {
	if e.Do != nil {
		return e.Do
	}
	return http.DefaultClient
}

// EPASite is one monitoring station near the requested point.
type EPASite struct {
	// Name is the station name, such as "Melbourne CBD".
	Name string
	// Type is "Standard", "Sensor" or "Camera". Standard stations are the
	// reference-grade ones; Sensor readings are indicative.
	Type string
	// Parameter is the pollutant the reading covers, such as "PM2.5".
	Parameter string
	// Advice is EPA's own category, such as "Good" or "Fair".
	Advice string
	// Value is the reading in EPA's units for the parameter.
	Value float64
	// Since and Until bound the averaging window.
	Since time.Time
	Until time.Time
	// DistanceKM is the great-circle distance from the requested point.
	DistanceKM float64
	// Latitude and Longitude are the station's coordinates.
	Latitude  float64
	Longitude float64
}

// Nearest returns the monitoring stations within radiusKM of a point, nearest
// first. Standard stations sort ahead of indicative sensors at equal distance
// so the reference-grade reading wins.
func (e EPAArcGIS) Nearest(ctx context.Context, lat, lon, radiusKM float64) ([]EPASite, error) {
	query := url.Values{}
	query.Set("where", "1=1")
	query.Set("outFields", "siteName,siteType,healthParameter,healthAdvice,averageValue,since,until")
	query.Set("returnGeometry", "true")
	query.Set("geometry", fmt.Sprintf(`{"x":%s,"y":%s}`, trimFloat(lon), trimFloat(lat)))
	query.Set("geometryType", "esriGeometryPoint")
	query.Set("inSR", "4326")
	query.Set("spatialRel", "esriSpatialRelIntersects")
	query.Set("distance", trimFloat(radiusKM*1000))
	query.Set("units", "esriSRUnit_Meter")
	query.Set("f", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base()+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.doer().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusError("epa arcgis", resp); err != nil {
		return nil, err
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	sites, err := parseEPASites(body, lat, lon)
	if err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return nil, fmt.Errorf("%w: no EPA station within %g km", ErrNoCoverage, radiusKM)
	}
	return sites, nil
}

type wireEPAResponse struct {
	Features []struct {
		Attributes struct {
			SiteID       string  `json:"siteID"`
			SiteName     string  `json:"siteName"`
			SiteType     string  `json:"siteType"`
			HealthParam  string  `json:"healthParameter"`
			HealthAdvice string  `json:"healthAdvice"`
			AverageValue float64 `json:"averageValue"`
			Since        int64   `json:"since"`
			Until        int64   `json:"until"`
		} `json:"attributes"`
		Geometry *struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		} `json:"geometry"`
	} `json:"features"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseEPASites decodes the feature collection and sorts it for selection.
func parseEPASites(body []byte, lat, lon float64) ([]EPASite, error) {
	var wire wireEPAResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("epa arcgis: %w", err)
	}
	if wire.Error != nil && wire.Error.Message != "" {
		return nil, fmt.Errorf("epa arcgis: %s", wire.Error.Message)
	}
	sites := make([]EPASite, 0, len(wire.Features))
	for _, f := range wire.Features {
		site := EPASite{
			Name:      f.Attributes.SiteName,
			Type:      f.Attributes.SiteType,
			Parameter: f.Attributes.HealthParam,
			Advice:    strings.TrimSpace(f.Attributes.HealthAdvice),
			Value:     f.Attributes.AverageValue,
			Since:     epochMillis(f.Attributes.Since),
			Until:     epochMillis(f.Attributes.Until),
		}
		if f.Geometry != nil {
			site.Latitude, site.Longitude = f.Geometry.Y, f.Geometry.X
			site.DistanceKM = HaversineKM(lat, lon, site.Latitude, site.Longitude)
		} else {
			// The non-spatial response omits geometry; keep the row but push it
			// behind anything with a known distance.
			site.DistanceKM = math.Inf(1)
		}
		sites = append(sites, site)
	}
	sortSites(sites)
	return sites, nil
}

// sortSites orders by distance, preferring reference-grade stations when two
// are close enough that the difference is noise.
func sortSites(sites []EPASite) {
	// Candidate order, not a full sort: callers take the head.
	for i := 1; i < len(sites); i++ {
		for j := i; j > 0 && siteBefore(sites[j], sites[j-1]); j-- {
			sites[j], sites[j-1] = sites[j-1], sites[j]
		}
	}
}

// siteBefore reports whether a should come before b. A Standard station is
// preferred over an indicative sensor within a kilometre, because a
// reference-grade reading beside an indicative one is the better number.
func siteBefore(a, b EPASite) bool {
	const preferenceKM = 1.0
	if a.Type == "Standard" && b.Type != "Standard" && a.DistanceKM <= b.DistanceKM+preferenceKM {
		return true
	}
	if b.Type == "Standard" && a.Type != "Standard" && b.DistanceKM <= a.DistanceKM+preferenceKM {
		return false
	}
	return a.DistanceKM < b.DistanceKM
}

// epochMillis converts the layer's millisecond timestamps. A zero stays zero,
// which reads as "no window" rather than as 1970.
func epochMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// trimFloat formats a coordinate without an exponent, which the query parser
// on the other side does not accept.
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// HaversineKM is the great-circle distance between two points in kilometres.
func HaversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371.0088
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	// Clamp before the square root: floating-point error can push a for
	// coincident points a hair above one, and Asin of that is NaN.
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(math.Min(1, a)))
}

// AirQualityMeasure converts the nearest EPA station into a current-measure
// row. It returns false when the row carries nothing usable, so a caller can
// fall through to the next station.
func (s EPASite) AirQualityMeasure() (Measure, bool) {
	if s.Advice == "" {
		return Measure{}, false
	}
	level := Level{Name: s.Advice}
	// EPA's own category is authoritative; the numeric value is only shown as
	// a supporting figure, so severity comes from the category name.
	if classes, ok := WireScale.Lookup(MeasureAirQuality, s.Parameter); ok {
		matched := false
		for _, c := range classes {
			if strings.EqualFold(c.Name, level.Name) {
				level.Severity = SeverityOf(c.WarnLevel)
				matched = true
				break
			}
		}
		if !matched {
			level.Severity = SeverityHigh
		}
	} else {
		level.Severity = epaSeverity(s.Advice)
	}
	out := Measure{
		Name:  MeasureAirQuality,
		Title: Title(MeasureAirQuality),
		Level: level,
		Metric: &Metric{
			Subcat: s.Parameter, Value: s.Value, Units: "µg/m³",
		},
		Source: "epa",
	}
	WireScale.AttachDial(&out)
	return out, true
}

// epaSeverity maps EPA's category vocabulary onto the shared axis. EPA uses
// the same six names as AirHealth's air quality scale, without the numeric
// warn levels, so this is the fallback when no scale is available.
func epaSeverity(advice string) Severity {
	switch strings.ToLower(strings.TrimSpace(advice)) {
	case "very good", "good":
		return SeverityGood
	case "fair", "poor":
		return SeverityModerate
	case "very poor", "extremely poor":
		return SeverityHigh
	default:
		return SeverityHigh
	}
}
