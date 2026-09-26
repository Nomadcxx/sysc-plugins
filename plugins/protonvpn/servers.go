package protonvpn

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// Feature bits from the official client's server list
// (proton.vpn.session.servers.enums).
const (
	FeatSecureCore = 1 << iota
	FeatTor
	FeatP2P
	FeatStreaming
)

// Server is one logical server from the client's cache.
type Server struct {
	Name     string
	City     string
	Country  string // ISO code
	Load     int    // 0..100
	Tier     int    // 0 = free
	Features int
	Up       bool
	Score    float64
}

// Country is the per-exit-country view consumed by the Connections tab.
type Country struct {
	Code        string
	Name        string
	Servers     []Server
	Load        int // rounded mean of up servers
	Maintenance bool
	Features    int // union
}

// rawServer mirrors the keys the official client writes to
// ~/.cache/Proton/VPN/serverlist.json (same file noctalia's servers.py reads).
type rawServer struct {
	Name        string  `json:"Name"`
	City        string  `json:"City"`
	ExitCountry string  `json:"ExitCountry"`
	Load        int     `json:"Load"`
	Tier        int     `json:"Tier"`
	Features    int     `json:"Features"`
	Status      int     `json:"Status"`
	Score       float64 `json:"Score"`
}

// LoadServers reads a serverlist.json. The real file is an object wrapping the
// array under "LogicalServers"; a bare array is accepted too.
func LoadServers(path string) ([]Server, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("serverlist: %w", err)
	}
	var wrapper struct {
		LogicalServers []rawServer `json:"LogicalServers"`
	}
	var list []rawServer
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.LogicalServers != nil {
		list = wrapper.LogicalServers
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("serverlist: %w", err)
	}
	servers := make([]Server, 0, len(list))
	for _, r := range list {
		servers = append(servers, Server{
			Name:     r.Name,
			City:     r.City,
			Country:  strings.ToUpper(r.ExitCountry),
			Load:     r.Load,
			Tier:     r.Tier,
			Features: r.Features,
			Up:       r.Status == 1,
			Score:    r.Score,
		})
	}
	return servers, nil
}

// Aggregate groups servers by exit country. Load is the rounded mean over up
// servers; a country with no up servers is under maintenance. Countries sort
// by name (free-tier countries first when freeTier), servers by tier then load.
func Aggregate(servers []Server, countryNames map[string]string, freeTier bool) []Country {
	byCode := map[string]*Country{}
	var order []string
	for _, s := range servers {
		c := byCode[s.Country]
		if c == nil {
			c = &Country{Code: s.Country, Name: s.Country}
			if n := countryNames[s.Country]; n != "" {
				c.Name = n
			}
			byCode[s.Country] = c
			order = append(order, s.Country)
		}
		c.Servers = append(c.Servers, s)
		c.Features |= s.Features
	}

	free := map[string]bool{}
	for _, code := range order {
		c := byCode[code]
		sum, n, minTier := 0, 0, -1
		for _, s := range c.Servers {
			if s.Up {
				sum += s.Load
				n++
			}
			if minTier == -1 || s.Tier < minTier {
				minTier = s.Tier
			}
		}
		if n > 0 {
			c.Load = int(math.Round(float64(sum) / float64(n)))
		} else {
			c.Maintenance = true
		}
		free[code] = minTier == 0
		sort.Slice(c.Servers, func(i, j int) bool {
			if c.Servers[i].Tier != c.Servers[j].Tier {
				return c.Servers[i].Tier < c.Servers[j].Tier
			}
			return c.Servers[i].Load < c.Servers[j].Load
		})
	}

	countries := make([]Country, 0, len(order))
	for _, code := range order {
		countries = append(countries, *byCode[code])
	}
	sort.Slice(countries, func(i, j int) bool {
		a, b := countries[i], countries[j]
		if freeTier && free[a.Code] != free[b.Code] {
			return free[a.Code]
		}
		return a.Name < b.Name
	})
	return countries
}

var fallbackList = []struct{ Code, Name string }{
	{"US", "United States"}, {"GB", "United Kingdom"}, {"DE", "Germany"},
	{"FR", "France"}, {"NL", "Netherlands"}, {"CA", "Canada"},
	{"JP", "Japan"}, {"AU", "Australia"}, {"IT", "Italy"},
	{"ES", "Spain"}, {"SE", "Sweden"}, {"CH", "Switzerland"},
}

// FallbackCountries is the built-in list shown when no cache exists yet.
func FallbackCountries() []Country {
	out := make([]Country, 0, len(fallbackList))
	for _, f := range fallbackList {
		out = append(out, Country{Code: f.Code, Name: f.Name})
	}
	return out
}
