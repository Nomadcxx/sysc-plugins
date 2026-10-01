package notes

import (
	"os"
	"testing"
)

func TestEmptyArtShipsAsAPNG(t *testing.T) {
	b, err := os.ReadFile("assets/empty-notes.png")
	if err != nil || len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatalf("assets/empty-notes.png missing or not a PNG: %v", err)
	}
	if path := EmptyArtPath(); path != "" {
		t.Fatalf("a test binary has no assets beside it, got %q", path)
	}
}
