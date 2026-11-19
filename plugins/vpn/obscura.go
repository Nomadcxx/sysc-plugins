package vpn

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

func ParseObscuraStatus(raw []byte) (Observation, error) {
	var envelope struct {
		Status map[string]json.RawMessage `json:"vpnStatus"`
	}
	bad := errors.New("Obscura status format changed")
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Status) != 1 {
		return Observation{}, bad
	}
	s := Observation{Traffic: TrafficUnknown}
	for variant, body := range envelope.Status {
		if !jsonObject(body) {
			return Observation{}, bad
		}
		var d struct {
			Exit struct {
				ID      string
				City    string `json:"city_name"`
				Country string `json:"country_code"`
			}
			Relay        struct{ ID string }
			ConnectError *string `json:"connectError"`
			Transport    json.RawMessage
		}
		if json.Unmarshal(body, &d) != nil {
			return Observation{}, bad
		}
		switch variant {
		case "disconnected":
			s.Phase = Disconnected
		case "connecting":
			s.Phase = Connecting
			if d.ConnectError != nil {
				s.Detail = "Connection is retrying; check the Obscura app."
			}
		case "connected":
			s.Phase = Connected
			s.City, s.Country, s.Exit, s.Entry = display(d.Exit.City), display(d.Exit.Country), display(d.Exit.ID), display(d.Relay.ID)
			var transport string
			if json.Unmarshal(d.Transport, &transport) == nil {
				s.Transport = display(transport)
			}
		default:
			return Observation{}, bad
		}
	}
	return s, nil
}

// countryNames covers the countries Mullvad (and so Obscura's Mullvad exits)
// serve. ponytail: a country added later shows its code until it is listed
// here; golang.org/x/text/language/display is the upgrade path if that bites.
var countryNames = map[string]string{
	"al": "Albania", "ar": "Argentina", "at": "Austria", "au": "Australia", "be": "Belgium",
	"bg": "Bulgaria", "br": "Brazil", "ca": "Canada", "ch": "Switzerland", "cl": "Chile",
	"co": "Colombia", "cy": "Cyprus", "cz": "Czech Republic", "de": "Germany", "dk": "Denmark",
	"ee": "Estonia", "es": "Spain", "fi": "Finland", "fr": "France", "gb": "United Kingdom",
	"gr": "Greece", "hk": "Hong Kong", "hr": "Croatia", "hu": "Hungary", "id": "Indonesia",
	"ie": "Ireland", "il": "Israel", "it": "Italy", "jp": "Japan", "mx": "Mexico",
	"my": "Malaysia", "ng": "Nigeria", "nl": "Netherlands", "no": "Norway", "nz": "New Zealand",
	"pe": "Peru", "ph": "Philippines", "pl": "Poland", "pt": "Portugal", "ro": "Romania",
	"rs": "Serbia", "se": "Sweden", "sg": "Singapore", "si": "Slovenia", "sk": "Slovakia",
	"th": "Thailand", "tr": "Turkey", "ua": "Ukraine", "us": "United States", "za": "South Africa",
}

func countryName(code string) string {
	if name, ok := countryNames[strings.ToLower(code)]; ok {
		return name
	}
	return strings.ToUpper(code)
}

var (
	obscuraCountry = regexp.MustCompile(`^[a-z]{2}$`)
	obscuraCity    = regexp.MustCompile(`^[a-z]{2,8}$`)
	obscuraExit    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
)

// ParseObscuraLocations reads obscura locations list --json. Every code and
// exit ID later becomes a CLI argument, so anything outside a strict shape
// rejects the whole catalogue rather than reaching argv.
func ParseObscuraLocations(raw []byte) ([]Location, error) {
	var v struct {
		Value struct {
			Exits []struct {
				ID       string `json:"id"`
				Country  string `json:"country_code"`
				City     string `json:"city_code"`
				CityName string `json:"city_name"`
			} `json:"exits"`
		} `json:"value"`
	}
	bad := errors.New("Obscura catalogue format changed")
	if json.Unmarshal(raw, &v) != nil {
		return nil, bad
	}
	var out []Location
	for _, e := range v.Value.Exits {
		country, city := strings.ToLower(e.Country), strings.ToLower(e.City)
		if !obscuraCountry.MatchString(country) || !obscuraCity.MatchString(city) || !obscuraExit.MatchString(e.ID) {
			return nil, bad
		}
		out = append(out, Location{Country: country, CountryName: countryName(country), City: city, CityName: display(e.CityName), Host: e.ID})
	}
	return out, nil
}
