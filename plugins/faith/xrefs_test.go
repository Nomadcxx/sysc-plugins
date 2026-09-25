package faith

import "testing"

func TestCrossReferencesForJohn316(t *testing.T) {
	x, err := LoadXrefs()
	if err != nil {
		t.Fatal(err)
	}
	got := x.For(mustRef(t, "JHN 3:16"))
	if len(got) == 0 || len(got) > 5 {
		t.Fatalf("John 3:16 has %d cross-references", len(got))
	}
	if got[0].Key() != "ROM 5:8" {
		t.Fatalf("best cross-reference for John 3:16 = %s, want ROM 5:8", got[0].Key())
	}
	if refs := x.For(mustRef(t, "JHN 3:16-17")); len(refs) != len(got) {
		t.Fatal("a range does not use its first verse's references")
	}
}

func TestEveryCrossReferenceResolvesInKJV(t *testing.T) {
	x, err := LoadXrefs()
	if err != nil {
		t.Fatal(err)
	}
	kjv := mustBible(t, "KJV")
	n := 0
	for _, raw := range x.refs {
		for _, s := range splitRefs(raw) {
			r, err := ParseRef(s)
			if err != nil {
				t.Fatalf("%q: %v", s, err)
			}
			if !kjv.Has(r.Book, r.Chapter, r.Verse) {
				t.Fatalf("%s is not in the KJV", s)
			}
			n++
		}
	}
	if n < 100000 {
		t.Fatalf("only %d cross-references", n)
	}
}
