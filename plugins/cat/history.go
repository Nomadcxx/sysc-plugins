package cat

// HistoryLen is how many samples the panel's graph shows: two minutes at
// the default two-second sampling, inside the wire's 64-sample ceiling.
const HistoryLen = 60

// History is a fixed ring of load readings. It never allocates after
// construction; Values copies into the caller's slice.
type History struct {
	buf  [HistoryLen]float64
	head int // next write position
	n    int
}

// Push records one reading, dropping the oldest when full.
func (h *History) Push(v float64) {
	h.buf[h.head] = v
	h.head = (h.head + 1) % HistoryLen
	if h.n < HistoryLen {
		h.n++
	}
}

// Len is the number of readings held.
func (h *History) Len() int { return h.n }

// Values appends the readings to dst oldest first, the order a graph draws.
func (h *History) Values(dst []float64) []float64 {
	start := (h.head - h.n + HistoryLen) % HistoryLen
	for i := 0; i < h.n; i++ {
		dst = append(dst, h.buf[(start+i)%HistoryLen])
	}
	return dst
}
