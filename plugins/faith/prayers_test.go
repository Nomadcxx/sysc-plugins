package faith

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestPrayerCorpusIsWellFormed(t *testing.T) {
	want := []string{
		"lords-prayer", "apostles-creed", "nicene-creed", "gloria-patri", "doxology",
		"aaronic-blessing", "the-grace", "jesus-prayer", "st-francis", "st-patrick",
		"collect-purity", "collect-grace", "collect-peace", "collect-aid", "collect-guidance",
		"quiet-confidence", "st-chrysostom", "compline", "grace-meals",
		"psalm-23", "psalm-51", "psalm-19", "psalm-139", "psalm-121",
		"hail-mary", "memorare", "angelus", "anima-christi", "act-contrition", "st-michael",
		"trisagion", "heavenly-king", "st-ephrem",
	}
	if len(Prayers) != len(want) {
		t.Fatalf("corpus has %d prayers, want %d", len(Prayers), len(want))
	}
	for i, p := range Prayers {
		if p.ID != want[i] {
			t.Fatalf("prayer %d is %q, want %q", i, p.ID, want[i])
		}
		if p.Title == "" || !ValidTradition(p.Tradition) {
			t.Fatalf("%s: bad title or tradition", p.ID)
		}
		if (p.Text == "") == (p.Ref == "") {
			t.Fatalf("%s: needs exactly one of Text and Ref", p.ID)
		}
		if p.Text != "" && p.Source == "" {
			t.Fatalf("%s: text without a source", p.ID)
		}
		for _, id := range Translations {
			text, source, err := p.Body(mustBible(t, id))
			if err != nil || text == "" || source == "" {
				t.Fatalf("%s in %s: %q, %q, %v", p.ID, id, text, source, err)
			}
			if n := NotifyFor(p.Title, text, source, 30); len(n.Body) >= 16<<10 {
				t.Fatalf("%s: body is %d bytes", p.ID, len(n.Body))
			}
		}
	}
}

func TestScripturePrayersReadTheChosenTranslation(t *testing.T) {
	p, _ := PrayerByID("psalm-23")
	kjv, source, err := p.Body(mustBible(t, "KJV"))
	if err != nil || !strings.Contains(kjv, "The Lord is my shepherd; I shall not want.") || source != "Psalm 23:1–6 (KJV)" {
		t.Fatalf("KJV Psalm 23 = %q, %q, %v", kjv, source, err)
	}
	bsb, _, _ := p.Body(mustBible(t, "BSB"))
	if !strings.HasPrefix(bsb, "The LORD is my shepherd") || !strings.HasSuffix(bsb, "forever.") {
		t.Fatalf("BSB Psalm 23 = %q", bsb)
	}
}

func TestPrayerPoolsAddToTheEcumenicalSet(t *testing.T) {
	eco := PrayerPool(Ecumenical)
	for _, tr := range []string{Catholic, Orthodox} {
		pool := PrayerPool(tr)
		if len(pool) <= len(eco) {
			t.Fatalf("%s pool is not larger than the ecumenical one", tr)
		}
		ids := map[string]bool{}
		for _, p := range pool {
			ids[p.ID] = true
		}
		for _, p := range eco {
			if !ids[p.ID] {
				t.Fatalf("%s pool lacks %s", tr, p.ID)
			}
		}
	}
	for _, p := range PrayerPool(Catholic) {
		if p.Tradition == Orthodox {
			t.Fatalf("catholic pool contains %s", p.ID)
		}
	}
}

func TestBagNeverRepeatsBackToBack(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	var b Bag
	n := len(PrayerPool(Ecumenical))
	prev := ""
	seen := map[string]int{}
	for i := range 3 * n {
		p := b.Draw(rng, Ecumenical)
		if p.ID == prev {
			t.Fatalf("draw %d repeated %s", i, p.ID)
		}
		prev = p.ID
		seen[p.ID]++
	}
	for id, c := range seen {
		if c != 3 {
			t.Fatalf("%s drawn %d times in three rounds", id, c)
		}
	}
}

func TestBagRefillsOnATraditionChangeAndRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	var b Bag
	b.Draw(rng, Ecumenical)
	b.Draw(rng, Orthodox)
	if b.Tradition != Orthodox || b.Pos != 1 || len(b.Order) != len(PrayerPool(Orthodox)) {
		t.Fatalf("bag after a tradition change: %+v", b)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var back Bag
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Pos != b.Pos || back.Last != b.Last || strings.Join(back.Order, ",") != strings.Join(b.Order, ",") {
		t.Fatalf("round trip changed the bag: %+v vs %+v", back, b)
	}
	back.Order = append(back.Order[:1], "no-such-prayer")
	if p := back.Draw(rng, Orthodox); p.ID == "" {
		t.Fatal("a corrupt bag drew nothing")
	}
}

func TestNotifyFor(t *testing.T) {
	n := NotifyFor("The Grace", "Words.", "Book of Common Prayer, 1928", 30)
	want := v1.NotifyParams{Summary: "The Grace", Body: "Words.\n\n— Book of Common Prayer, 1928", Urgency: v1.UrgencyLow, TimeoutMS: 30000}
	if n.Summary != want.Summary || n.Body != want.Body || n.Urgency != want.Urgency || n.TimeoutMS != want.TimeoutMS {
		t.Fatalf("got %+v", n)
	}
	if NotifyFor("a", "b", "c", 0).TimeoutMS != 0 {
		t.Fatal("zero seconds did not leave the timeout to the service")
	}
}
