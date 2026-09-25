package faith

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestPoolFollowsTheCurationRules(t *testing.T) {
	p, err := LoadPool()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 365 {
		t.Fatalf("pool has %d entries, want 365", len(p))
	}
	seen := map[string]bool{}
	perBook := map[string]int{}
	for _, r := range p {
		if seen[r.Key()] {
			t.Fatalf("%s appears twice", r.Key())
		}
		seen[r.Key()] = true
		perBook[Books[r.Book].Code]++
		if r.Last()-r.Verse >= 4 {
			t.Fatalf("%s is longer than four verses", r.Key())
		}
		for _, id := range Translations {
			b := mustBible(t, id)
			for v := r.Verse; v <= r.Last(); v++ {
				if !b.Has(r.Book, r.Chapter, v) {
					t.Fatalf("%s: %s lacks verse %d", r.Key(), id, v)
				}
			}
		}
	}
	if perBook["PSA"]*4 > len(p) {
		t.Fatalf("Psalms are %d of %d, more than a quarter", perBook["PSA"], len(p))
	}
	for _, g := range []string{"MAT", "MRK", "LUK", "JHN"} {
		if perBook[g] < 10 {
			t.Fatalf("%s appears %d times, want at least 10", g, perBook[g])
		}
	}
}

func TestDailyIsStableWithinADayAndCycles(t *testing.T) {
	p, err := LoadPool()
	if err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("AEST", 10*3600)
	morning := time.Date(2026, 9, 25, 0, 5, 0, 0, loc)
	night := time.Date(2026, 9, 25, 23, 55, 0, 0, loc)
	next := time.Date(2026, 9, 26, 0, 5, 0, 0, loc)
	if p.Daily(morning) != p.Daily(night) {
		t.Fatal("the daily verse changed within one local day")
	}
	if p.Daily(night) == p.Daily(next) {
		t.Fatal("the daily verse did not change at midnight")
	}
	if p.Daily(morning) != p.Daily(morning.AddDate(0, 0, 365)) {
		t.Fatal("the daily cycle is not 365 days")
	}
	seen := map[Ref]bool{}
	for d := range 365 {
		seen[p.Daily(morning.AddDate(0, 0, d))] = true
	}
	if len(seen) != 365 {
		t.Fatalf("a year of days reached %d entries", len(seen))
	}
}

func TestPoolRandomNeverRepeatsTheCurrentVerse(t *testing.T) {
	p, err := LoadPool()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(3, 4))
	cur := p[0]
	for range 2000 {
		next := p.Random(rng, cur)
		if next == cur {
			t.Fatalf("repeated %s", cur.Key())
		}
		cur = next
	}
}
