package protonvpn

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempList(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "serverlist.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func findCountry(t *testing.T, countries []Country, code string) Country {
	t.Helper()
	for _, c := range countries {
		if c.Code == code {
			return c
		}
	}
	t.Fatalf("country %s not found", code)
	return Country{}
}

func TestLoadServers(t *testing.T) {
	servers, err := LoadServers("testdata/serverlist.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 5 {
		t.Fatalf("got %d", len(servers))
	}
	if servers[0].City != "New York" || servers[0].Score != 1.5 {
		t.Fatalf("field mapping: %+v", servers[0])
	}
}

func TestLoadServersBareArray(t *testing.T) {
	path := writeTempList(t, `[{"Name":"NL#1","City":"Amsterdam","ExitCountry":"NL","Load":40,"Tier":0,"Features":0,"Status":1,"Score":3.0}]`)
	servers, err := LoadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "NL#1" || !servers[0].Up {
		t.Fatalf("bare array: %+v", servers)
	}
}

func TestAggregateCountryOrder(t *testing.T) {
	servers, _ := LoadServers("testdata/serverlist.json")
	countries := Aggregate(servers, map[string]string{"US": "United States", "NL": "Netherlands", "SE": "Sweden"}, false)
	var codes []string
	for _, c := range countries {
		codes = append(codes, c.Code)
	}
	want := []string{"NL", "SE", "US"} // Netherlands, Sweden, United States
	if len(codes) != len(want) {
		t.Fatalf("got %v", codes)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("order: got %v, want %v", codes, want)
		}
	}
}

func TestAggregateServerOrder(t *testing.T) {
	servers, _ := LoadServers("testdata/serverlist.json")
	countries := Aggregate(servers, nil, false)
	us := findCountry(t, countries, "US")
	if us.Servers[0].Name != "US-NY#1" || us.Servers[1].Name != "US-CA#1" {
		t.Fatalf("US by load: %+v", us.Servers)
	}
	nl := findCountry(t, countries, "NL")
	if nl.Servers[0].Name != "NL#1" || nl.Servers[1].Name != "NL#2" {
		t.Fatalf("NL by tier: %+v", nl.Servers)
	}
}

func TestLoadServersAllStatusZeroMeansUnknown(t *testing.T) {
	path := writeTempList(t, `{"LogicalServers":[{"Name":"US-NY#1","City":"New York","ExitCountry":"US","Load":30,"Tier":2,"Features":4,"Status":0,"Score":1.5}]}`)
	servers, err := LoadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		if !s.Up {
			t.Fatalf("Status 0 with no up server anywhere is unknown, not down: %+v", s)
		}
	}
}

func TestAggregate(t *testing.T) {
	servers, _ := LoadServers("testdata/serverlist.json")
	countries := Aggregate(servers, map[string]string{"US": "United States", "NL": "Netherlands", "SE": "Sweden"}, false)
	if len(countries) != 3 {
		t.Fatalf("got %d", len(countries))
	}
	us := findCountry(t, countries, "US")
	if len(us.Servers) != 2 || us.Maintenance {
		t.Fatalf("US: %+v", us)
	}
	if us.Load != 63 {
		t.Fatalf("US load %d", us.Load)
	} // round((30+95)/2)
	nl := findCountry(t, countries, "NL")
	if !nl.Maintenance {
		t.Fatal("NL all down → maintenance")
	}
	if nl.Features&FeatTor == 0 {
		t.Fatal("NL carries Tor")
	}
}

func TestAggregateFreeTierSortsFreeFirst(t *testing.T) {
	servers, _ := LoadServers("testdata/serverlist.json")
	countries := Aggregate(servers, nil, true)
	if countries[0].Code != "NL" {
		t.Fatalf("free country first, got %s", countries[0].Code)
	}
	if countries[0].Servers[0].Tier != 0 {
		t.Fatal("free locations first")
	}
}

func TestFallbackCountries(t *testing.T) {
	fb := FallbackCountries()
	if len(fb) != 12 {
		t.Fatalf("got %d", len(fb))
	}
	for _, c := range fb {
		if c.Name == "" || len(c.Code) != 2 {
			t.Fatalf("bad fallback %+v", c)
		}
		if c.Load != 0 || c.Maintenance {
			t.Fatalf("fallback not pristine: %+v", c)
		}
	}
}
