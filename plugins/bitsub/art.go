// Package bitsub is the sysc-shell plugin for bitsub: views, the bitsubd
// client and the notification relay.
package bitsub

import (
	_ "embed"
	"strconv"
	"strings"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

//go:embed assets/bitsub.ans
var artSource string

// Span is a run of header art in one tone.
type Span struct {
	Text string
	Tone v1.Tone
}

// ParseArt turns ANSI art into rows of tone spans. Only SGR sequences are
// read: foreground 90 (dark grey) is the subtle tone and every other
// foreground, including a reset, is the accent; backgrounds are ignored, so
// the shaded ▒▓ glyphs draw in the accent. Rows are padded with spaces to
// the widest so centring moves them together and the strokes still meet.
func ParseArt(src string) [][]Span {
	src = strings.TrimRight(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var rows [][]Span
	width := 0
	for _, line := range strings.Split(src, "\n") {
		var row []Span
		tone := v1.ToneAccent
		for len(line) > 0 {
			if strings.HasPrefix(line, "\x1b[") {
				end := strings.IndexByte(line, 'm')
				if end < 0 {
					break
				}
				tone = sgrTone(line[2:end], tone)
				line = line[end+1:]
				continue
			}
			next := strings.IndexByte(line, '\x1b')
			if next < 0 {
				next = len(line)
			}
			row = appendSpan(row, Span{line[:next], tone})
			line = line[next:]
		}
		width = max(width, runes(row))
		rows = append(rows, row)
	}
	for i, row := range rows {
		if pad := width - runes(row); pad > 0 {
			if len(row) == 0 {
				row = []Span{{Tone: v1.ToneAccent}}
			}
			row[len(row)-1].Text += strings.Repeat(" ", pad)
			rows[i] = row
		}
	}
	return rows
}

func sgrTone(params string, tone v1.Tone) v1.Tone {
	for _, p := range strings.Split(params, ";") {
		n, err := strconv.Atoi(p)
		switch {
		case err != nil && p != "":
			continue
		case p == "" || n == 0:
			tone = v1.ToneAccent
		case n == 90:
			tone = v1.ToneSubtle
		case (n >= 30 && n <= 37) || (n >= 91 && n <= 97) || n == 39:
			tone = v1.ToneAccent
		}
	}
	return tone
}

func appendSpan(row []Span, s Span) []Span {
	if s.Text == "" {
		return row
	}
	if n := len(row); n > 0 && row[n-1].Tone == s.Tone {
		row[n-1].Text += s.Text
		return row
	}
	return append(row, s)
}

func runes(row []Span) int {
	n := 0
	for _, s := range row {
		n += utf8.RuneCountInString(s.Text)
	}
	return n
}

// ArtRows is the embedded header art as tone spans.
func ArtRows() [][]Span { return ParseArt(artSource) }

// PlainRows is the header art as single-tone text, for hosts without spans.
func PlainRows() []string {
	var out []string
	for _, row := range ArtRows() {
		var b strings.Builder
		for _, s := range row {
			b.WriteString(s.Text)
		}
		out = append(out, b.String())
	}
	return out
}
