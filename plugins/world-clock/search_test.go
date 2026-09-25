package worldclock

import (
	"os"
	"reflect"
	"testing"
)

func fixtureIndex(t *testing.T) *Index {
	t.Helper()
	open := func(name string) *os.File {
		f, err := os.Open("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	return NewIndex(open("zone.tab"), open("iso3166.tab"), open("tzdata.zi"))
}

func matchIDs(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestSearchCityIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	got := fixtureIndex(t).Search("tokyo", 5)
	if len(got) == 0 || got[0] != (Match{ID: "Asia/Tokyo", City: "Tokyo", Country: "Japan"}) {
		t.Fatalf("tokyo = %+v", got)
	}
}

func TestSearchWordPrefixFindsNewYork(t *testing.T) {
	t.Parallel()
	if got := matchIDs(fixtureIndex(t).Search("york", 5)); len(got) == 0 || got[0] != "America/New_York" {
		t.Fatalf("york = %v", got)
	}
}

func TestSearchAliasCarriesItsName(t *testing.T) {
	t.Parallel()
	got := fixtureIndex(t).Search("san francisco", 5)
	if len(got) == 0 || got[0] != (Match{ID: "America/Los_Angeles", City: "San Francisco", Alias: true}) {
		t.Fatalf("san francisco = %+v", got)
	}
}

func TestSearchRanksExactBeforePrefixBeforeAlias(t *testing.T) {
	t.Parallel()
	// "os": Oslo is a city prefix (rank 2), the alias Osaka -> Asia/Tokyo is an
	// alias prefix (rank 3), and "buenOS aires" / "lOS angeles" only contain it
	// in their ids (rank 5, ordered by city).
	got := matchIDs(fixtureIndex(t).Search("os", 5))
	want := []string{"Europe/Oslo", "Asia/Tokyo", "America/Argentina/Buenos_Aires", "America/Los_Angeles"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("os = %v, want %v", got, want)
	}
}

func TestSearchCountryName(t *testing.T) {
	t.Parallel()
	if got := matchIDs(fixtureIndex(t).Search("norway", 5)); !reflect.DeepEqual(got, []string{"Europe/Oslo"}) {
		t.Fatalf("norway = %v", got)
	}
}

func TestSearchFoldsAccents(t *testing.T) {
	t.Parallel()
	got := fixtureIndex(t).Search("reunion", 5)
	if len(got) == 0 || got[0].ID != "Indian/Reunion" || got[0].Country != "Réunion" {
		t.Fatalf("reunion = %+v", got)
	}
}

func TestSearchExactIDAndLinkCaseInsensitive(t *testing.T) {
	t.Parallel()
	ix := fixtureIndex(t)
	if got := ix.Search("asia/tokyo", 5); len(got) == 0 || got[0].ID != "Asia/Tokyo" {
		t.Fatalf("exact id = %+v", got)
	}
	if got := ix.Search("us/eastern", 5); len(got) == 0 || got[0].ID != "US/Eastern" {
		t.Fatalf("link = %+v", got)
	}
	// Not in any table but a valid id verbatim.
	if got := ix.Search("Europe/Paris", 5); len(got) == 0 || got[0].ID != "Europe/Paris" {
		t.Fatalf("verbatim id = %+v", got)
	}
}

func TestSearchDedupesAndCaps(t *testing.T) {
	t.Parallel()
	ix := fixtureIndex(t)
	got := ix.Search("a", 3)
	if len(got) != 3 {
		t.Fatalf("cap: %d results", len(got))
	}
	seen := map[string]bool{}
	for _, m := range ix.Search("a", 50) {
		if seen[m.ID] {
			t.Fatalf("duplicate %s", m.ID)
		}
		seen[m.ID] = true
	}
	if ix.Search("   ", 5) != nil || ix.Search("tokyo", 0) != nil {
		t.Fatal("empty query or zero limit returned matches")
	}
}

func TestSearchWithoutTablesIsLimited(t *testing.T) {
	t.Parallel()
	ix := NewIndex(nil, nil, nil)
	if !ix.Limited() {
		t.Fatal("index without zone.tab not limited")
	}
	if got := ix.Search("mumbai", 5); len(got) == 0 || got[0].ID != "Asia/Kolkata" {
		t.Fatalf("alias without tables = %+v", got)
	}
	if got := ix.Search("utc", 5); len(got) == 0 || got[0].ID != "UTC" {
		t.Fatalf("utc = %+v", got)
	}
	if fixtureIndex(t).Limited() {
		t.Fatal("fixture index limited")
	}
}

func TestOpenSystemIndexToleratesMissingDir(t *testing.T) {
	t.Parallel()
	if !OpenSystemIndex(t.TempDir()).Limited() {
		t.Fatal("empty dir not limited")
	}
}
