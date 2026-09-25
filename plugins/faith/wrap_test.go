package faith

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		max  int
		want []string
	}{
		{"empty", "", 10, nil},
		{"spaces only", "   \n  ", 10, nil},
		{"fits", "In the beginning", 20, []string{"In the beginning"}},
		{"breaks at spaces", "In the beginning was the Word", 12, []string{"In the", "beginning", "was the Word"}},
		{"honours newlines", "one\ntwo three", 20, []string{"one", "two three"}},
		{"drops blank lines", "one\n\ntwo", 20, []string{"one", "two"}},
		{"hard-splits a long word", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"never splits an en dash", "8:38–39", 5, []string{"8:38", "–39"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Wrap(tc.in, tc.max); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Wrap(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestWrapKeepsEveryLineValidAndInBounds(t *testing.T) {
	text := strings.Repeat("Ἐν ἀρχῇ ἦν ὁ λόγος, καὶ ὁ λόγος ἦν πρὸς τὸν θεόν — ", 20)
	for _, max := range []int{4, 5, 7, 33, 50} {
		for _, line := range Wrap(text, max) {
			if len(line) > max || !utf8.ValidString(line) || line == "" {
				t.Fatalf("max %d: bad line %q", max, line)
			}
		}
	}
}
