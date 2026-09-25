package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Verse is one verse of generated text.
type Verse struct {
	Chapter, Verse int
	Text           string
}

// skipLine lists the line-leading markers whose whole line is not verse
// text: identification, titles, headings, introductions, and the Psalm
// superscriptions (\d), which belong to no verse number.
var skipLine = map[string]bool{
	"id": true, "ide": true, "h": true, "toc1": true, "toc2": true, "toc3": true,
	"mt1": true, "mt2": true, "mt3": true, "ms": true, "ms1": true, "mr": true,
	"s1": true, "s2": true, "r": true, "sp": true, "d": true, "cl": true,
	"ip": true, "is1": true, "ili": true, "qa": true, "rem": true,
}

var (
	lineMarker = regexp.MustCompile(`^\\([a-z]+[0-9]*)(?:\s|$)`)
	notes      = regexp.MustCompile(`\\(f|x) .*?\\(f|x)\*`)
	attrs      = regexp.MustCompile(`\|[^\\|]*(\\\+?w\*)`)
	openMarker = regexp.MustCompile(`\\\+?[a-z]+[0-9]* ?`)
	closeMark  = regexp.MustCompile(`\\\+?[a-z]+[0-9]*\*`)
	spaces     = regexp.MustCompile(`\s+`)
)

// ParseUSFM reads one book's USFM and returns its verses in order. Words
// keep their text and lose their Strong's attributes; footnotes, cross-
// reference notes, headings, and superscriptions are dropped; poetry and
// paragraph breaks become single spaces.
func ParseUSFM(r io.Reader) ([]Verse, error) {
	var (
		out      []Verse
		chapter  int
		cur      *Verse
		buf      strings.Builder
		implicit bool
		seen     = map[[2]int]bool{}
	)
	flush := func() {
		if cur != nil {
			cur.Text = clean(buf.String())
			seen[[2]int{cur.Chapter, cur.Verse}] = true
			out = append(out, *cur)
			cur = nil
		}
		buf.Reset()
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		m := lineMarker.FindStringSubmatch(line)
		if m == nil {
			if cur != nil {
				buf.WriteString(" " + line)
			}
			continue
		}
		marker, rest := m[1], line[len(m[0]):]
		switch {
		case skipLine[marker]:
			continue
		case marker == "c":
			flush()
			n, err := strconv.Atoi(strings.Fields(rest + " x")[0])
			if err != nil {
				return nil, fmt.Errorf("chapter marker %q: %w", line, err)
			}
			chapter = n
			implicit = true
		case marker == "v":
			flush()
			rest = strings.TrimLeft(rest, " ")
			digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
			n, err := strconv.Atoi(rest[:digits])
			text := rest[digits:]
			if err != nil {
				return nil, fmt.Errorf("verse marker %q: %w", line, err)
			}
			if chapter == 0 {
				return nil, fmt.Errorf("verse %d before any chapter", n)
			}
			if seen[[2]int{chapter, n}] {
				return nil, fmt.Errorf("verse %d:%d appears twice", chapter, n)
			}
			cur = &Verse{Chapter: chapter, Verse: n}
			implicit = false
			buf.WriteString(text)
		default:
			// A paragraph, poetry, or list marker continues the current verse.
			// Text before a chapter's first verse marker is verse 1: the BSB
			// source omits "\v 1" where a Psalm opens a book of the Psalter.
			if cur == nil && chapter > 0 && implicit && strings.TrimSpace(rest) != "" {
				cur = &Verse{Chapter: chapter, Verse: 1}
				implicit = false
			}
			if cur != nil {
				buf.WriteString(" " + rest)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return out, nil
}

func clean(s string) string {
	s = notes.ReplaceAllString(s, "")
	s = attrs.ReplaceAllString(s, "$1")
	s = closeMark.ReplaceAllString(s, "")
	s = openMarker.ReplaceAllString(s, "")
	s = spaces.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	// Removing a note or a marker can strand a space before punctuation.
	for _, p := range []string{",", ".", ";", ":", "?", "!", "’", "”"} {
		s = strings.ReplaceAll(s, " "+p, p)
	}
	return s
}
