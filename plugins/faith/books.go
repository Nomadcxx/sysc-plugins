package faith

import (
	"fmt"
	"strconv"
	"strings"
)

// Book is one book of the 66-book Protestant canon. Code is the USFM
// identifier the bundled data uses, OSIS is the identifier OpenBible.info's
// cross-references use, and Slug is the path segment biblehub.com uses.
type Book struct {
	Code string
	OSIS string
	Name string
	// RefName names the book inside a reference when it differs from Name:
	// one reads "Psalm 23:1", not "Psalms 23:1".
	RefName string
	Slug    string
}

// Books is the canon in order. The KJV source lists its books in this same
// order, which is how the generator maps it.
var Books = []Book{
	{"GEN", "Gen", "Genesis", "", "genesis"},
	{"EXO", "Exod", "Exodus", "", "exodus"},
	{"LEV", "Lev", "Leviticus", "", "leviticus"},
	{"NUM", "Num", "Numbers", "", "numbers"},
	{"DEU", "Deut", "Deuteronomy", "", "deuteronomy"},
	{"JOS", "Josh", "Joshua", "", "joshua"},
	{"JDG", "Judg", "Judges", "", "judges"},
	{"RUT", "Ruth", "Ruth", "", "ruth"},
	{"1SA", "1Sam", "1 Samuel", "", "1_samuel"},
	{"2SA", "2Sam", "2 Samuel", "", "2_samuel"},
	{"1KI", "1Kgs", "1 Kings", "", "1_kings"},
	{"2KI", "2Kgs", "2 Kings", "", "2_kings"},
	{"1CH", "1Chr", "1 Chronicles", "", "1_chronicles"},
	{"2CH", "2Chr", "2 Chronicles", "", "2_chronicles"},
	{"EZR", "Ezra", "Ezra", "", "ezra"},
	{"NEH", "Neh", "Nehemiah", "", "nehemiah"},
	{"EST", "Esth", "Esther", "", "esther"},
	{"JOB", "Job", "Job", "", "job"},
	{"PSA", "Ps", "Psalms", "Psalm", "psalms"},
	{"PRO", "Prov", "Proverbs", "", "proverbs"},
	{"ECC", "Eccl", "Ecclesiastes", "", "ecclesiastes"},
	{"SNG", "Song", "Song of Solomon", "", "songs"},
	{"ISA", "Isa", "Isaiah", "", "isaiah"},
	{"JER", "Jer", "Jeremiah", "", "jeremiah"},
	{"LAM", "Lam", "Lamentations", "", "lamentations"},
	{"EZK", "Ezek", "Ezekiel", "", "ezekiel"},
	{"DAN", "Dan", "Daniel", "", "daniel"},
	{"HOS", "Hos", "Hosea", "", "hosea"},
	{"JOL", "Joel", "Joel", "", "joel"},
	{"AMO", "Amos", "Amos", "", "amos"},
	{"OBA", "Obad", "Obadiah", "", "obadiah"},
	{"JON", "Jonah", "Jonah", "", "jonah"},
	{"MIC", "Mic", "Micah", "", "micah"},
	{"NAM", "Nah", "Nahum", "", "nahum"},
	{"HAB", "Hab", "Habakkuk", "", "habakkuk"},
	{"ZEP", "Zeph", "Zephaniah", "", "zephaniah"},
	{"HAG", "Hag", "Haggai", "", "haggai"},
	{"ZEC", "Zech", "Zechariah", "", "zechariah"},
	{"MAL", "Mal", "Malachi", "", "malachi"},
	{"MAT", "Matt", "Matthew", "", "matthew"},
	{"MRK", "Mark", "Mark", "", "mark"},
	{"LUK", "Luke", "Luke", "", "luke"},
	{"JHN", "John", "John", "", "john"},
	{"ACT", "Acts", "Acts", "", "acts"},
	{"ROM", "Rom", "Romans", "", "romans"},
	{"1CO", "1Cor", "1 Corinthians", "", "1_corinthians"},
	{"2CO", "2Cor", "2 Corinthians", "", "2_corinthians"},
	{"GAL", "Gal", "Galatians", "", "galatians"},
	{"EPH", "Eph", "Ephesians", "", "ephesians"},
	{"PHP", "Phil", "Philippians", "", "philippians"},
	{"COL", "Col", "Colossians", "", "colossians"},
	{"1TH", "1Thess", "1 Thessalonians", "", "1_thessalonians"},
	{"2TH", "2Thess", "2 Thessalonians", "", "2_thessalonians"},
	{"1TI", "1Tim", "1 Timothy", "", "1_timothy"},
	{"2TI", "2Tim", "2 Timothy", "", "2_timothy"},
	{"TIT", "Titus", "Titus", "", "titus"},
	{"PHM", "Phlm", "Philemon", "", "philemon"},
	{"HEB", "Heb", "Hebrews", "", "hebrews"},
	{"JAS", "Jas", "James", "", "james"},
	{"1PE", "1Pet", "1 Peter", "", "1_peter"},
	{"2PE", "2Pet", "2 Peter", "", "2_peter"},
	{"1JN", "1John", "1 John", "", "1_john"},
	{"2JN", "2John", "2 John", "", "2_john"},
	{"3JN", "3John", "3 John", "", "3_john"},
	{"JUD", "Jude", "Jude", "", "jude"},
	{"REV", "Rev", "Revelation", "", "revelation"},
}

var (
	byCode = map[string]int{}
	byOSIS = map[string]int{}
)

func init() {
	for i, b := range Books {
		byCode[b.Code] = i
		byOSIS[b.OSIS] = i
	}
}

// BookByCode resolves a USFM book code to its index in Books.
func BookByCode(code string) (int, bool) {
	i, ok := byCode[code]
	return i, ok
}

// BookByOSIS resolves an OSIS book code to its index in Books.
func BookByOSIS(osis string) (int, bool) {
	i, ok := byOSIS[osis]
	return i, ok
}

// Ref addresses one verse, or a range of verses within one chapter when End is
// set. Book indexes Books.
type Ref struct {
	Book    int `json:"book"`
	Chapter int `json:"chapter"`
	Verse   int `json:"verse"`
	End     int `json:"end,omitempty"`
}

// ParseRef reads the data-file form: "JHN 3:16" or "ROM 8:38-39".
func ParseRef(s string) (Ref, error) {
	code, rest, ok := strings.Cut(s, " ")
	if !ok {
		return Ref{}, fmt.Errorf("reference %q: want BOOK C:V", s)
	}
	book, ok := BookByCode(code)
	if !ok {
		return Ref{}, fmt.Errorf("reference %q: unknown book %q", s, code)
	}
	ch, vs, ok := strings.Cut(rest, ":")
	if !ok {
		return Ref{}, fmt.Errorf("reference %q: want BOOK C:V", s)
	}
	chapter, err := positive(ch)
	if err != nil {
		return Ref{}, fmt.Errorf("reference %q: chapter: %w", s, err)
	}
	first, last, ranged := strings.Cut(vs, "-")
	verse, err := positive(first)
	if err != nil {
		return Ref{}, fmt.Errorf("reference %q: verse: %w", s, err)
	}
	r := Ref{Book: book, Chapter: chapter, Verse: verse}
	if ranged {
		end, err := positive(last)
		if err != nil {
			return Ref{}, fmt.Errorf("reference %q: end verse: %w", s, err)
		}
		if end <= verse {
			return Ref{}, fmt.Errorf("reference %q: range ends before it starts", s)
		}
		r.End = end
	}
	return r, nil
}

func positive(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < 1 {
		return 0, fmt.Errorf("%d is not positive", n)
	}
	return n, nil
}

// Key is the data-file form ParseRef reads.
func (r Ref) Key() string {
	s := fmt.Sprintf("%s %d:%d", Books[r.Book].Code, r.Chapter, r.Verse)
	if r.End > 0 {
		s += fmt.Sprintf("-%d", r.End)
	}
	return s
}

// String is the reference as a reader writes it: "Romans 8:38–39".
func (r Ref) String() string {
	b := Books[r.Book]
	name := b.Name
	if b.RefName != "" {
		name = b.RefName
	}
	s := fmt.Sprintf("%s %d:%d", name, r.Chapter, r.Verse)
	if r.End > 0 {
		s += fmt.Sprintf("–%d", r.End)
	}
	return s
}

// Last is the final verse the reference covers.
func (r Ref) Last() int {
	if r.End > 0 {
		return r.End
	}
	return r.Verse
}
