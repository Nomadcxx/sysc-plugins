package faith

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Xrefs maps a verse to its bundled cross-references, best first.
type Xrefs struct {
	refs map[int]string
}

var (
	xrefsOnce sync.Once
	xrefsVal  *Xrefs
	xrefsErr  error
)

// LoadXrefs decompresses the bundled cross-references on first use.
func LoadXrefs() (*Xrefs, error) {
	xrefsOnce.Do(func() { xrefsVal, xrefsErr = decodeXrefs() })
	return xrefsVal, xrefsErr
}

func decodeXrefs() (*Xrefs, error) {
	f, err := dataFS.Open("data/xrefs.tsv.gz")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	x := &Xrefs{refs: make(map[int]string, 30000)}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		cols := strings.SplitN(sc.Text(), "\t", 4)
		if len(cols) != 4 {
			return nil, fmt.Errorf("faith: xrefs: malformed line %q", sc.Text())
		}
		book, ok := BookByCode(cols[0])
		ch, err1 := strconv.Atoi(cols[1])
		v, err2 := strconv.Atoi(cols[2])
		if !ok || err1 != nil || err2 != nil {
			return nil, fmt.Errorf("faith: xrefs: malformed reference %q", sc.Text())
		}
		x.refs[pack(book, ch, v)] = cols[3]
	}
	return x, sc.Err()
}

// For returns the cross-references of a reference's first verse.
func (x *Xrefs) For(r Ref) []Ref {
	raw, ok := x.refs[pack(r.Book, r.Chapter, r.Verse)]
	if !ok {
		return nil
	}
	var out []Ref
	for _, s := range splitRefs(raw) {
		if ref, err := ParseRef(s); err == nil {
			out = append(out, ref)
		}
	}
	return out
}

func splitRefs(raw string) []string { return strings.Split(raw, ";") }
