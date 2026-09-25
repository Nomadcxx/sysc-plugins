package main

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/plugins/faith"
)

// MaxRefs is how many cross-references each verse keeps.
const MaxRefs = 5

type xref struct {
	to    faith.Ref
	votes int
}

// ParseXrefs reads OpenBible.info's cross_references.txt and returns, for
// each source verse key, its best references in data-file form: positive
// votes only, most votes first, ties in canonical order, at most MaxRefs.
// A target range that crosses a chapter keeps its first verse.
func ParseXrefs(r io.Reader) (map[string][]string, error) {
	all := map[string][]xref{}
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if strings.HasPrefix(line, "From Verse") {
				continue
			}
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 3 {
			return nil, fmt.Errorf("cross-reference line %q: want three columns", line)
		}
		votes, err := strconv.Atoi(strings.TrimSpace(cols[2]))
		if err != nil {
			return nil, fmt.Errorf("cross-reference line %q: votes: %w", line, err)
		}
		if votes < 1 {
			continue
		}
		from, err := osisRange(cols[0])
		if err != nil {
			return nil, err
		}
		to, err := osisRange(cols[1])
		if err != nil {
			return nil, err
		}
		from.End = 0
		key := from.Key()
		all[key] = append(all[key], xref{to: to, votes: votes})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(all))
	for key, refs := range all {
		sort.SliceStable(refs, func(i, j int) bool {
			if refs[i].votes != refs[j].votes {
				return refs[i].votes > refs[j].votes
			}
			return less(refs[i].to, refs[j].to)
		})
		seen := map[string]bool{}
		for _, x := range refs {
			k := x.to.Key()
			if seen[k] || k == key {
				continue
			}
			seen[k] = true
			out[key] = append(out[key], k)
			if len(out[key]) == MaxRefs {
				break
			}
		}
	}
	return out, nil
}

func less(a, b faith.Ref) bool {
	if a.Book != b.Book {
		return a.Book < b.Book
	}
	if a.Chapter != b.Chapter {
		return a.Chapter < b.Chapter
	}
	return a.Verse < b.Verse
}

// osisRange reads "Prov.8.22" or "Prov.8.22-Prov.8.30".
func osisRange(s string) (faith.Ref, error) {
	start, end, ranged := strings.Cut(strings.TrimSpace(s), "-")
	r, err := osisVerse(start)
	if err != nil {
		return faith.Ref{}, err
	}
	if ranged {
		e, err := osisVerse(end)
		if err != nil {
			return faith.Ref{}, err
		}
		if e.Book == r.Book && e.Chapter == r.Chapter && e.Verse > r.Verse {
			r.End = e.Verse
		}
	}
	return r, nil
}

func osisVerse(s string) (faith.Ref, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return faith.Ref{}, fmt.Errorf("osis %q: want Book.C.V", s)
	}
	book, ok := faith.BookByOSIS(parts[0])
	if !ok {
		return faith.Ref{}, fmt.Errorf("osis %q: unknown book", s)
	}
	ch, err1 := strconv.Atoi(parts[1])
	v, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || ch < 1 || v < 1 {
		return faith.Ref{}, fmt.Errorf("osis %q: bad chapter or verse", s)
	}
	return faith.Ref{Book: book, Chapter: ch, Verse: v}, nil
}
