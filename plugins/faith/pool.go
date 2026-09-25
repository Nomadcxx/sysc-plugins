package faith

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// Pool is the curated devotional list: one reference per calendar day.
type Pool []Ref

// LoadPool reads the embedded data/pool.txt.
func LoadPool() (Pool, error) {
	raw, err := dataFS.ReadFile("data/pool.txt")
	if err != nil {
		return nil, err
	}
	return ParsePool(string(raw))
}

// ParsePool reads one reference per line; blank lines and # comments are
// ignored.
func ParsePool(s string) (Pool, error) {
	var p Pool
	sc := bufio.NewScanner(strings.NewReader(s))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, err := ParseRef(line)
		if err != nil {
			return nil, fmt.Errorf("pool line %d: %w", n, err)
		}
		p = append(p, r)
	}
	return p, sc.Err()
}

// epoch anchors the daily cycle. Any fixed date works; this one keeps the
// arithmetic readable.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Daily is the pool entry for now's local calendar date. It changes at local
// midnight and cycles through the whole pool before repeating.
func (p Pool) Daily(now time.Time) Ref {
	y, m, d := now.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	n := int(day.Sub(epoch).Hours() / 24)
	i := n % len(p)
	if i < 0 {
		i += len(p)
	}
	return p[i]
}

// Random picks a pool entry other than current.
func (p Pool) Random(rng *rand.Rand, current Ref) Ref {
	if len(p) < 2 {
		return p[0]
	}
	for {
		r := p[rng.IntN(len(p))]
		if r != current {
			return r
		}
	}
}
