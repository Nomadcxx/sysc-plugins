package faith

import (
	"bufio"
	"compress/gzip"
	"embed"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
)

//go:embed data/BSB.tsv.gz data/WEB.tsv.gz data/KJV.tsv.gz data/xrefs.tsv.gz data/pool.txt
var dataFS embed.FS

// Translations are the bundled texts, default first.
var Translations = []string{"BSB", "WEB", "KJV"}

// TranslationNames are the full names for captions and the README.
var TranslationNames = map[string]string{
	"BSB": "Berean Standard Bible",
	"WEB": "World English Bible",
	"KJV": "King James Version",
}

// ValidTranslation reports whether id is bundled.
func ValidTranslation(id string) bool {
	_, ok := TranslationNames[id]
	return ok
}

type verse struct {
	book, chapter, verse int
	text                 string
}

// Bible is one decompressed translation: every verse in canonical order.
// It is read-only once loaded and safe to share.
type Bible struct {
	ID     string
	verses []verse
	index  map[int]int
}

func pack(book, chapter, v int) int { return book<<20 | chapter<<10 | v }

var (
	biblesMu sync.Mutex
	bibles   = map[string]*Bible{}
	// loads counts decompressions, so a test can prove each happens once.
	loads int
)

// LoadBible returns the named translation, decompressing it on first use.
func LoadBible(id string) (*Bible, error) {
	biblesMu.Lock()
	defer biblesMu.Unlock()
	if b, ok := bibles[id]; ok {
		return b, nil
	}
	if !ValidTranslation(id) {
		return nil, fmt.Errorf("faith: no bundled translation %q", id)
	}
	b, err := decodeBible(id)
	if err != nil {
		return nil, err
	}
	loads++
	bibles[id] = b
	return b, nil
}

func decodeBible(id string) (*Bible, error) {
	f, err := dataFS.Open("data/" + id + ".tsv.gz")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("faith: %s: %w", id, err)
	}
	b := &Bible{ID: id, verses: make([]verse, 0, 31200), index: make(map[int]int, 31200)}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		cols := strings.SplitN(sc.Text(), "\t", 4)
		if len(cols) != 4 {
			return nil, fmt.Errorf("faith: %s: malformed line %q", id, sc.Text())
		}
		book, ok := BookByCode(cols[0])
		ch, err1 := strconv.Atoi(cols[1])
		v, err2 := strconv.Atoi(cols[2])
		if !ok || err1 != nil || err2 != nil {
			return nil, fmt.Errorf("faith: %s: malformed reference in %q", id, sc.Text())
		}
		b.index[pack(book, ch, v)] = len(b.verses)
		b.verses = append(b.verses, verse{book, ch, v, cols[3]})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("faith: %s: %w", id, err)
	}
	return b, nil
}

// Count is the number of verses the translation carries.
func (b *Bible) Count() int { return len(b.verses) }

// Has reports whether the translation carries the verse.
func (b *Bible) Has(book, chapter, v int) bool {
	_, ok := b.index[pack(book, chapter, v)]
	return ok
}

// Text returns a reference's text, a range joined with spaces. It fails when
// the first verse is missing; a range stops at the chapter's end.
func (b *Bible) Text(r Ref) (string, bool) {
	i, ok := b.index[pack(r.Book, r.Chapter, r.Verse)]
	if !ok {
		return "", false
	}
	parts := []string{b.verses[i].text}
	for j := i + 1; j < len(b.verses); j++ {
		v := b.verses[j]
		if v.book != r.Book || v.chapter != r.Chapter || v.verse > r.Last() {
			break
		}
		parts = append(parts, v.text)
	}
	return strings.Join(parts, " "), true
}

// Resolve moves a reference onto a verse this translation carries: the verse
// itself, else the next one in its chapter, else the chapter's last verse. A
// chapter the translation lacks does not resolve. A range keeps its length
// only while its first verse is unchanged.
func (b *Bible) Resolve(r Ref) (Ref, bool) {
	if b.Has(r.Book, r.Chapter, r.Verse) {
		return r, true
	}
	last := 0
	for v := 1; v <= 200; v++ {
		if !b.Has(r.Book, r.Chapter, v) {
			continue
		}
		if v > r.Verse {
			return Ref{Book: r.Book, Chapter: r.Chapter, Verse: v}, true
		}
		last = v
	}
	if last == 0 {
		return Ref{}, false
	}
	return Ref{Book: r.Book, Chapter: r.Chapter, Verse: last}, true
}

// Next is the verse after the reference's last verse, crossing chapters and
// books. It fails at the end of the canon.
func (b *Bible) Next(r Ref) (Ref, bool) {
	i, ok := b.index[pack(r.Book, r.Chapter, r.Last())]
	if !ok {
		rr, ok := b.Resolve(Ref{Book: r.Book, Chapter: r.Chapter, Verse: r.Last()})
		if !ok || rr.Verse <= r.Last() {
			return Ref{}, false
		}
		return rr, true
	}
	if i+1 >= len(b.verses) {
		return Ref{}, false
	}
	return b.ref(i + 1), true
}

// Prev is the verse before the reference's first verse. It fails at
// Genesis 1:1.
func (b *Bible) Prev(r Ref) (Ref, bool) {
	i, ok := b.index[pack(r.Book, r.Chapter, r.Verse)]
	if !ok || i == 0 {
		return Ref{}, false
	}
	return b.ref(i - 1), true
}

// First and Last are the ends of the canon in this translation.
func (b *Bible) First() Ref { return b.ref(0) }
func (b *Bible) Last() Ref  { return b.ref(len(b.verses) - 1) }

// Random picks one verse uniformly.
func (b *Bible) Random(rng *rand.Rand) Ref { return b.ref(rng.IntN(len(b.verses))) }

func (b *Bible) ref(i int) Ref {
	v := b.verses[i]
	return Ref{Book: v.book, Chapter: v.chapter, Verse: v.verse}
}
