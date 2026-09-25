package faith

import "testing"

func TestBookTableIsTheProtestantCanon(t *testing.T) {
	if len(Books) != 66 {
		t.Fatalf("books = %d, want 66", len(Books))
	}
	seen := map[string]bool{}
	for i, b := range Books {
		if b.Code == "" || b.OSIS == "" || b.Name == "" || b.Slug == "" {
			t.Fatalf("book %d has an empty field: %+v", i, b)
		}
		for _, k := range []string{"u:" + b.Code, "o:" + b.OSIS} {
			if seen[k] {
				t.Fatalf("duplicate %s", k)
			}
			seen[k] = true
		}
	}
	if Books[0].Code != "GEN" || Books[39].Code != "MAT" || Books[65].Code != "REV" {
		t.Fatalf("canonical order broken: %s %s %s", Books[0].Code, Books[39].Code, Books[65].Code)
	}
	if i, ok := BookByOSIS("1Thess"); !ok || Books[i].Code != "1TH" {
		t.Fatalf("BookByOSIS(1Thess) = %d, %v", i, ok)
	}
}

func TestParseRef(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Ref
	}{
		{"JHN 3:16", Ref{Book: 42, Chapter: 3, Verse: 16}},
		{"ROM 8:38-39", Ref{Book: 44, Chapter: 8, Verse: 38, End: 39}},
		{"GEN 1:1", Ref{Book: 0, Chapter: 1, Verse: 1}},
		{"1TH 5:16-18", Ref{Book: 51, Chapter: 5, Verse: 16, End: 18}},
	} {
		got, err := ParseRef(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseRef(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
		if got.Key() != tc.in {
			t.Fatalf("Key() = %q, want %q", got.Key(), tc.in)
		}
	}
	for _, bad := range []string{"", "XYZ 1:1", "JHN 3:0", "JHN 0:1", "JHN 3:17-16", "JHN 3:16-16", "JHN 3", "JHN3:16", "JHN 3:16-", "JHN a:1"} {
		if _, err := ParseRef(bad); err == nil {
			t.Fatalf("ParseRef(%q) accepted", bad)
		}
	}
}

func TestRefString(t *testing.T) {
	for in, want := range map[string]string{
		"JHN 3:16":    "John 3:16",
		"ROM 8:38-39": "Romans 8:38–39",
		"1TH 5:16-18": "1 Thessalonians 5:16–18",
		"PSA 23:1":    "Psalm 23:1",
		"SNG 2:4":     "Song of Solomon 2:4",
	} {
		r, err := ParseRef(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.String(); got != want {
			t.Fatalf("%s.String() = %q, want %q", in, got, want)
		}
	}
}
