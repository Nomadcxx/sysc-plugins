package faith

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func mustBible(t *testing.T, id string) *Bible {
	t.Helper()
	b, err := LoadBible(id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustRef(t *testing.T, s string) Ref {
	t.Helper()
	r, err := ParseRef(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBundledTranslationsAreComplete(t *testing.T) {
	for _, id := range Translations {
		b := mustBible(t, id)
		books := map[int]bool{}
		for _, v := range b.verses {
			books[v.book] = true
			if strings.TrimSpace(v.text) == "" {
				t.Fatalf("%s %s %d:%d is empty", id, Books[v.book].Code, v.chapter, v.verse)
			}
		}
		if len(books) != 66 {
			t.Fatalf("%s covers %d books", id, len(books))
		}
		if b.Count() < 31000 {
			t.Fatalf("%s has %d verses", id, b.Count())
		}
	}
	if n := mustBible(t, "KJV").Count(); n != 31102 {
		t.Fatalf("KJV has %d verses, want 31102", n)
	}
}

func TestLoadBibleDecompressesOnce(t *testing.T) {
	mustBible(t, "WEB")
	biblesMu.Lock()
	before := loads
	biblesMu.Unlock()
	mustBible(t, "WEB")
	biblesMu.Lock()
	after := loads
	biblesMu.Unlock()
	if after != before {
		t.Fatalf("second load decompressed again (%d -> %d)", before, after)
	}
	if _, err := LoadBible("NIV"); err == nil {
		t.Fatal("loaded a translation that is not bundled")
	}
}

func TestTextJoinsARange(t *testing.T) {
	b := mustBible(t, "BSB")
	got, ok := b.Text(mustRef(t, "ROM 8:38-39"))
	if !ok || !strings.HasPrefix(got, "For I am convinced") || !strings.HasSuffix(got, "Christ Jesus our Lord.") {
		t.Fatalf("Romans 8:38–39 = %q, %v", got, ok)
	}
	if _, ok := b.Text(mustRef(t, "JHN 22:1")); ok {
		t.Fatal("John 22:1 resolved")
	}
}

func TestNavigationCrossesBoundariesAndStopsAtTheEnds(t *testing.T) {
	for _, id := range Translations {
		b := mustBible(t, id)
		if next, ok := b.Next(mustRef(t, "MAL 4:6")); !ok || next.Key() != "MAT 1:1" {
			t.Fatalf("%s: after Malachi 4:6 = %v, %v", id, next, ok)
		}
		if prev, ok := b.Prev(mustRef(t, "JHN 4:1")); !ok || prev.Book != 42 || prev.Chapter != 3 {
			t.Fatalf("%s: before John 4:1 = %v, %v", id, prev, ok)
		}
		if next, ok := b.Next(mustRef(t, "ROM 8:38-39")); !ok || next.Key() != "ROM 9:1" {
			t.Fatalf("%s: after Romans 8:38–39 = %v, %v", id, next, ok)
		}
		if _, ok := b.Prev(b.First()); ok || b.First().Key() != "GEN 1:1" {
			t.Fatalf("%s: moved before %v", id, b.First())
		}
		if _, ok := b.Next(b.Last()); ok || b.Last().Book != 65 {
			t.Fatalf("%s: moved after %v", id, b.Last())
		}
	}
}

func TestResolveStepsOverOmittedVerses(t *testing.T) {
	web := mustBible(t, "WEB")
	got, ok := web.Resolve(mustRef(t, "LUK 17:36"))
	if !ok || got.Key() != "LUK 17:37" {
		t.Fatalf("WEB Luke 17:36 resolved to %v, %v", got, ok)
	}
	if next, ok := web.Next(mustRef(t, "LUK 17:35")); !ok || next.Key() != "LUK 17:37" {
		t.Fatalf("WEB after Luke 17:35 = %v, %v", next, ok)
	}
	if got, ok := web.Resolve(mustRef(t, "PSA 23:9")); !ok || got.Key() != "PSA 23:6" {
		t.Fatalf("Psalm 23:9 clamped to %v, %v", got, ok)
	}
	if _, ok := web.Resolve(mustRef(t, "JUD 2:1")); ok {
		t.Fatal("Jude 2 resolved")
	}
}

func TestRandomStaysInTheCanon(t *testing.T) {
	b := mustBible(t, "BSB")
	rng := rand.New(rand.NewPCG(1, 2))
	for range 10000 {
		r := b.Random(rng)
		if _, ok := b.Text(r); !ok {
			t.Fatalf("random verse %v has no text", r)
		}
	}
}
