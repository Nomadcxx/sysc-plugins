package worldclock

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Match is one search result. City is the alias when an alias matched, which
// is also what the zone is labelled with when it is added.
type Match struct {
	ID      string
	City    string
	Country string
	Alias   bool
}

type entry struct {
	Match
	cityKey, countryKey, idKey string
}

// Index answers city, country, and id queries from the system tz tables plus
// an embedded alias list. It is built once and read-only afterwards.
type Index struct {
	entries []entry
	ids     map[string]string // folded exact id or link name -> canonical spelling
	country map[string]string // zone id -> country name
	limited bool
}

// aliases are large cities that are not zone names.
var aliases = []struct{ name, id string }{
	{"San Francisco", "America/Los_Angeles"}, {"Seattle", "America/Los_Angeles"},
	{"Portland", "America/Los_Angeles"}, {"Las Vegas", "America/Los_Angeles"},
	{"San Diego", "America/Los_Angeles"},
	{"Boston", "America/New_York"}, {"Washington", "America/New_York"},
	{"Miami", "America/New_York"}, {"Atlanta", "America/New_York"},
	{"Philadelphia", "America/New_York"},
	{"Dallas", "America/Chicago"}, {"Houston", "America/Chicago"}, {"Austin", "America/Chicago"},
	{"Salt Lake City", "America/Denver"},
	{"Montreal", "America/Toronto"},
	{"Rio de Janeiro", "America/Sao_Paulo"},
	{"Mumbai", "Asia/Kolkata"}, {"Delhi", "Asia/Kolkata"}, {"New Delhi", "Asia/Kolkata"},
	{"Bangalore", "Asia/Kolkata"}, {"Bengaluru", "Asia/Kolkata"},
	{"Chennai", "Asia/Kolkata"}, {"Hyderabad", "Asia/Kolkata"},
	{"Beijing", "Asia/Shanghai"}, {"Shenzhen", "Asia/Shanghai"}, {"Guangzhou", "Asia/Shanghai"},
	{"Osaka", "Asia/Tokyo"}, {"Kyoto", "Asia/Tokyo"},
	{"Abu Dhabi", "Asia/Dubai"},
	{"Tel Aviv", "Asia/Jerusalem"},
	{"Saigon", "Asia/Ho_Chi_Minh"},
	{"Canberra", "Australia/Sydney"},
	{"Wellington", "Pacific/Auckland"},
	{"Geneva", "Europe/Zurich"},
	{"Munich", "Europe/Berlin"}, {"Frankfurt", "Europe/Berlin"}, {"Hamburg", "Europe/Berlin"},
	{"Milan", "Europe/Rome"},
	{"Barcelona", "Europe/Madrid"},
	{"Manchester", "Europe/London"}, {"Edinburgh", "Europe/London"},
	{"GMT", "UTC"},
}

// OpenSystemIndex reads zone.tab, iso3166.tab, and tzdata.zi from dir. A
// missing file degrades search instead of failing.
func OpenSystemIndex(dir string) *Index {
	open := func(name string) io.Reader {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return nil
		}
		return strings.NewReader(string(data))
	}
	return NewIndex(open("zone.tab"), open("iso3166.tab"), open("tzdata.zi"))
}

func eachLine(r io.Reader, fn func(string)) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fn(line)
	}
}

func NewIndex(zoneTab, isoTab, tzdata io.Reader) *Index {
	ix := &Index{ids: map[string]string{}, country: map[string]string{}, limited: zoneTab == nil}
	names := map[string]string{}
	eachLine(isoTab, func(line string) {
		if code, name, ok := strings.Cut(line, "\t"); ok {
			names[code] = name
		}
	})
	eachLine(zoneTab, func(line string) {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			return
		}
		id := f[2]
		ix.country[id] = names[f[0]]
		ix.ids[fold(id)] = id
		ix.add(Match{ID: id, City: ShortLabel(id), Country: names[f[0]]})
	})
	eachLine(tzdata, func(line string) {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "L" {
			ix.ids[fold(f[2])] = f[2]
		}
	})
	ix.ids["utc"] = "UTC"
	ix.add(Match{ID: "UTC", City: "UTC"})
	for _, a := range aliases {
		ix.add(Match{ID: a.id, City: a.name, Alias: true})
	}
	return ix
}

func (ix *Index) add(m Match) {
	ix.entries = append(ix.entries, entry{
		Match:      m,
		cityKey:    fold(m.City),
		countryKey: fold(m.Country),
		idKey:      fold(strings.ReplaceAll(m.ID, "_", " ")),
	})
}

// Limited reports that zone.tab was unavailable, so only aliases and exact ids
// can match.
func (ix *Index) Limited() bool { return ix.limited }

// Ranks, best first.
const (
	rankExactID = iota
	rankExactName
	rankCityPrefix
	rankAliasPrefix
	rankCountry
	rankIDSubstring
)

func (e entry) rank(q string) (int, bool) {
	switch {
	case e.cityKey == q:
		return rankExactName, true
	case !e.Alias && wordPrefix(e.cityKey, q):
		return rankCityPrefix, true
	case e.Alias && wordPrefix(e.cityKey, q):
		return rankAliasPrefix, true
	case e.countryKey != "" && wordPrefix(e.countryKey, q):
		return rankCountry, true
	case !e.Alias && strings.Contains(e.idKey, q):
		return rankIDSubstring, true
	}
	return 0, false
}

// Search returns up to limit matches, best first, one per zone id. It does not
// exclude zones already added: the caller decides whether a duplicate is an
// error (submit) or should be hidden (suggestions).
func (ix *Index) Search(query string, limit int) []Match {
	raw := strings.TrimSpace(query)
	q := fold(raw)
	if q == "" || limit <= 0 {
		return nil
	}
	type scored struct {
		m    Match
		rank int
	}
	best := map[string]scored{}
	consider := func(m Match, rank int) {
		if cur, ok := best[m.ID]; !ok || rank < cur.rank {
			best[m.ID] = scored{m, rank}
		}
	}
	if id, ok := ix.ids[q]; ok {
		consider(Match{ID: id, City: ShortLabel(id), Country: ix.country[id]}, rankExactID)
	} else if strings.Contains(raw, "/") && ValidZone(raw) {
		consider(Match{ID: raw, City: ShortLabel(raw)}, rankExactID)
	}
	for _, e := range ix.entries {
		if r, ok := e.rank(q); ok {
			consider(e.Match, r)
		}
	}
	out := make([]scored, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		if a, b := fold(out[i].m.City), fold(out[j].m.City); a != b {
			return a < b
		}
		return out[i].m.ID < out[j].m.ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	ms := make([]Match, len(out))
	for i, s := range out {
		ms[i] = s.m
	}
	return ms
}

func wordPrefix(s, q string) bool {
	if strings.HasPrefix(s, q) {
		return true
	}
	for i := 1; i < len(s); i++ {
		if (s[i-1] == ' ' || s[i-1] == '-') && strings.HasPrefix(s[i:], q) {
			return true
		}
	}
	return false
}

var accentFold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'ç': 'c', 'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ñ': 'n',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ý': 'y', 'ÿ': 'y',
	'’': '\'',
}

// fold lower-cases and strips common Latin accents so "Réunion" matches
// "reunion" without a text-normalisation dependency.
func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		r = unicode.ToLower(r)
		if f, ok := accentFold[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}
