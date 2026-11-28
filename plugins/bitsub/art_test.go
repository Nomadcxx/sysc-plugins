package bitsub

import (
	"strings"
	"testing"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestParseArtTonesAndWidth(t *testing.T) {
	rows := ArtRows()
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	width := -1
	for i, row := range rows {
		var b strings.Builder
		for j, sp := range row {
			if strings.ContainsRune(sp.Text, '\x1b') || strings.Contains(sp.Text, "[0;") {
				t.Fatalf("row %d keeps escape bytes: %q", i, sp.Text)
			}
			if j > 0 && row[j-1].Tone == sp.Tone {
				t.Errorf("row %d: adjacent spans %d and %d share tone %q", i, j-1, j, sp.Tone)
			}
			b.WriteString(sp.Text)
		}
		n := utf8.RuneCountInString(b.String())
		if width >= 0 && n != width {
			t.Errorf("row %d is %d runes, row 0 is %d", i, n, width)
		}
		width = n
	}
	if rows[0][0].Tone != v1.ToneAccent || !strings.HasPrefix(rows[0][0].Text, "▒▓") {
		t.Errorf("first span %+v", rows[0][0])
	}
	var subtleRuns int
	for _, row := range rows {
		for _, sp := range row {
			if sp.Tone == v1.ToneSubtle {
				subtleRuns++
				if strings.Trim(sp.Text, "▄▀ ") != "" {
					t.Errorf("subtle span holds more than the dark ▄/▀ runs: %q", sp.Text)
				}
			}
		}
	}
	if subtleRuns == 0 {
		t.Error("no subtle (ANSI 90) spans")
	}
	plain := PlainRows()
	if len(plain) != 3 || utf8.RuneCountInString(plain[0]) != width {
		t.Fatalf("plain rows %q", plain)
	}
}

func TestParseArtCodes(t *testing.T) {
	rows := ParseArt("\x1b[0;36;47m▒\x1b[0;90m▄▄\x1b[0;35mX\x1b[0mY\x1b[1;90mZ\r\nab\n")
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	want := []Span{{"▒", v1.ToneAccent}, {"▄▄", v1.ToneSubtle}, {"XY", v1.ToneAccent}, {"Z", v1.ToneSubtle}}
	if len(rows[0]) != len(want) {
		t.Fatalf("%+v", rows[0])
	}
	for i := range want {
		if rows[0][i] != want[i] {
			t.Errorf("span %d %+v want %+v", i, rows[0][i], want[i])
		}
	}
	if len(rows[1]) != 1 || rows[1][0] != (Span{"ab    ", v1.ToneAccent}) {
		t.Errorf("padded short row %+v", rows[1])
	}
}
