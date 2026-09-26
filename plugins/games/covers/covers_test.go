package covers

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/games/source"
)

func TestGenerateTileDeterministic(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := GenerateTile("Hades", a); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTile("Hades", b); err != nil {
		t.Fatal(err)
	}
	ra, _ := os.ReadFile(a)
	rb, _ := os.ReadFile(b)
	if string(ra) != string(rb) {
		t.Fatal("same name must produce the same tile")
	}
	img, err := png.Decode(strings.NewReader(string(ra)))
	if err != nil {
		t.Fatalf("not a decodable png: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 200 || b.Dy() < 250 {
		t.Fatalf("tile too small: %v", b)
	}
	other := filepath.Join(dir, "c.png")
	if err := GenerateTile("Celeste", other); err != nil {
		t.Fatal(err)
	}
	rc, _ := os.ReadFile(other)
	if string(rc) == string(ra) {
		t.Fatal("different names must differ (color or letters)")
	}
}

func TestResolveChain(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	local := filepath.Join(dir, "hades.jpg")
	if err := os.WriteFile(local, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := Resolve(source.Game{Slug: "hades", Name: "Hades", CoverPath: local}, cache); p != local {
		t.Fatalf("local cover must win: %q", p)
	}
	p := Resolve(source.Game{Slug: "celeste", Name: "Celeste"}, cache)
	if p == "" || !strings.HasPrefix(p, cache) {
		t.Fatalf("fallback must be a cached generated tile: %q", p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("tile not written: %v", err)
	}
	// Second call must reuse the cached file (mtime stable).
	info1, _ := os.Stat(p)
	if p2 := Resolve(source.Game{Slug: "celeste", Name: "Celeste"}, cache); p2 != p {
		t.Fatal("resolve must be stable")
	}
	if info2, _ := os.Stat(p); !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("tile regenerated instead of cached")
	}
}

func TestStaleLocalCoverIgnored(t *testing.T) {
	p := Resolve(source.Game{Slug: "x", Name: "X", CoverPath: "/nonexistent/x.jpg"}, t.TempDir())
	if _, err := os.Stat(p); err != nil {
		t.Fatal("must fall back to a real file")
	}
}

func TestNamePart(t *testing.T) {
	cases := map[string]string{
		"Clair Obscur: Expedition 33": "CO",
		"The Witcher 3":               "TW",
		"hades":                       "H",
	}
	for in, want := range cases {
		if got := initials(in); got != want {
			t.Fatalf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}

var _ = image.Rect
