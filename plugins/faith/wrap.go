package faith

import (
	"strings"
	"unicode/utf8"
)

// Wrap breaks text into lines of at most maxBytes bytes, the host's text
// metric being eight pixels per byte. It breaks at spaces, starts a new line
// at every newline, hard-splits a word longer than a line at a rune boundary,
// and drops empty lines.
func Wrap(text string, maxBytes int) []string {
	if maxBytes < utf8.UTFMax {
		maxBytes = utf8.UTFMax
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for len(word) > maxBytes {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				cut := maxBytes
				for cut > 0 && !utf8.RuneStart(word[cut]) {
					cut--
				}
				if cut == 0 {
					// No rune starts inside the line: the word opens with
					// stray continuation bytes. Cut at the byte limit so
					// the loop always advances.
					cut = maxBytes
				}
				out = append(out, word[:cut])
				word = word[cut:]
			}
			switch {
			case line == "":
				line = word
			case len(line)+1+len(word) <= maxBytes:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
