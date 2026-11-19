package vpn

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

type Location struct{ Country, CountryName, City, CityName, Host string }

func (l Location) ID() string { return l.Country + ":" + l.City + ":" + l.Host }

var countryLine = regexp.MustCompile(`^([^\t].+) \(([a-z]{2})\)$`)
var cityLine = regexp.MustCompile(`^\t(.+) \(([a-z]{3})\) @ .+$`)
var relayLine = regexp.MustCompile(`^\t\t([a-z]{2}-[a-z]{3}-wg-[0-9]+) \([^)]+\) - hosted by .+ \((?:Mullvad-owned|rented)\).*$`)

func ParseMullvadRelays(raw string) ([]Location, error) {
	var locations []Location
	var current Location
	bad := errors.New("Mullvad relay list format changed")
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := countryLine.FindStringSubmatch(line); m != nil {
			current = Location{Country: m[2], CountryName: display(m[1])}
		} else if m := cityLine.FindStringSubmatch(line); m != nil && current.Country != "" {
			current.City, current.CityName = m[2], display(m[1])
		} else if m := relayLine.FindStringSubmatch(line); m != nil && current.City != "" {
			if !strings.HasPrefix(m[1], current.Country+"-"+current.City+"-wg-") {
				return nil, bad
			}
			l := current
			l.Host = m[1]
			locations = append(locations, l)
		} else {
			return nil, bad
		}
	}
	return locations, nil
}

func LocationArgs(id string, locations []Location) ([]string, error) {
	for _, l := range locations {
		switch id {
		case l.Country + "::":
			return []string{"relay", "set", "location", l.Country}, nil
		case l.Country + ":" + l.City + ":":
			return []string{"relay", "set", "location", l.Country, l.City}, nil
		case l.ID():
			return []string{"relay", "set", "location", l.Country, l.City, l.Host}, nil
		}
	}
	return nil, errors.New("location is not in the current relay list")
}

// ObscuraConnectArgs connects to a country, city or exit from the parsed
// catalogue; obscura matches codes without regard to case.
func ObscuraConnectArgs(id string, locations []Location) ([]string, error) {
	for _, l := range locations {
		switch id {
		case l.Country + "::":
			return []string{"connect", "--country", strings.ToUpper(l.Country)}, nil
		case l.Country + ":" + l.City + ":":
			return []string{"connect", "--country", strings.ToUpper(l.Country), "--city", l.City}, nil
		case l.ID():
			return []string{"connect", "--exit", l.Host}, nil
		}
	}
	return nil, errors.New("location is not in the current catalogue")
}

func (c CLI) Locations(ctx context.Context, p Provider) ([]Location, error) {
	if p == Obscura {
		if !c.Obscura.Locations {
			return nil, errors.New("this Obscura CLI cannot list locations")
		}
		raw, err := c.run(ctx, Obscura, 15*time.Second, 4<<20, "locations", "list", "--json")
		if err != nil {
			return nil, err
		}
		return ParseObscuraLocations(raw)
	}
	raw, err := c.run(ctx, Mullvad, 15*time.Second, 4<<20, "relay", "list")
	if err != nil {
		return nil, err
	}
	return ParseMullvadRelays(string(raw))
}

// SelectLocation saves a Mullvad location, or connects Obscura to one: the
// obscura CLI has no saved location, only a connect target.
func (c CLI) SelectLocation(ctx context.Context, p Provider, id string, locations []Location) error {
	args, err := LocationArgs(id, locations)
	if p == Obscura {
		args, err = ObscuraConnectArgs(id, locations)
	}
	if err != nil {
		return err
	}
	_, err = c.run(ctx, p, mutationTimeout, readLimit, args...)
	return err
}
