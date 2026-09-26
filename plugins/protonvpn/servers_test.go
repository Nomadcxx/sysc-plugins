package protonvpn

import "testing"

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
	}
}
