package faith

import (
	"math/rand/v2"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Bag deals prayers without repeats: every prayer in the pool once, in a
// shuffled order, before any comes round again. It is plain data so the
// plugin can keep it in host state across restarts.
type Bag struct {
	Tradition string   `json:"tradition"`
	Order     []string `json:"order"`
	Pos       int      `json:"pos"`
	Last      string   `json:"last"`
}

// Draw returns the next prayer for a tradition. It refills when the bag is
// empty, when the tradition changes, or when the order names a prayer that no
// longer exists; a refill never starts with the prayer just drawn.
func (b *Bag) Draw(rng *rand.Rand, tradition string) Prayer {
	pool := PrayerPool(tradition)
	if b.Tradition != tradition || b.Pos >= len(b.Order) || !b.valid(pool) {
		b.refill(rng, tradition, pool)
	}
	id := b.Order[b.Pos]
	b.Pos++
	b.Last = id
	p, _ := PrayerByID(id)
	return p
}

func (b *Bag) valid(pool []Prayer) bool {
	in := map[string]bool{}
	for _, p := range pool {
		in[p.ID] = true
	}
	for _, id := range b.Order {
		if !in[id] {
			return false
		}
	}
	return len(b.Order) == len(pool)
}

func (b *Bag) refill(rng *rand.Rand, tradition string, pool []Prayer) {
	b.Tradition = tradition
	b.Order = b.Order[:0]
	for _, p := range pool {
		b.Order = append(b.Order, p.ID)
	}
	rng.Shuffle(len(b.Order), func(i, j int) { b.Order[i], b.Order[j] = b.Order[j], b.Order[i] })
	if len(b.Order) > 1 && b.Order[0] == b.Last {
		b.Order[0], b.Order[len(b.Order)-1] = b.Order[len(b.Order)-1], b.Order[0]
	}
	b.Pos = 0
}

// NotifyFor builds the prayer notification: the title as its summary, the
// words and a source line as its body. seconds of zero leaves the timeout to
// the notification service.
func NotifyFor(title, text, source string, seconds int) v1.NotifyParams {
	return v1.NotifyParams{
		Summary:   title,
		Body:      text + "\n\n— " + source,
		Urgency:   v1.UrgencyLow,
		TimeoutMS: int32(seconds) * 1000,
	}
}
