package cat

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// ProcStat is the kernel's CPU accounting file.
const ProcStat = "/proc/stat"

// Sampler measures whole-machine CPU load from the aggregate "cpu" line of
// /proc/stat.
//
// It keeps the file open and re-reads it from offset zero, which makes procfs
// regenerate the text, so a sample costs one pread into a fixed buffer: no
// open, no allocation, no child process. Only the first line is read; the
// per-core lines after it are never copied out of the kernel.
type Sampler struct {
	f   io.ReaderAt
	buf [256]byte

	prevTotal, prevIdle uint64
	primed              bool
}

// OpenSampler opens path, normally ProcStat, and takes the baseline reading
// the first load figure is measured against.
func OpenSampler(path string) (*Sampler, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s := &Sampler{f: f}
	if _, _, err := s.Sample(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the file.
func (s *Sampler) Close() error {
	if c, ok := s.f.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Sample returns the busy fraction, zero through one, since the previous
// call. ok is false on the baseline read and when no time has been accounted
// since the last one, because a load figure needs two readings to exist.
func (s *Sampler) Sample() (load float64, ok bool, err error) {
	n, err := s.f.ReadAt(s.buf[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, false, err
	}
	total, idle, err := parseCPULine(s.buf[:n])
	if err != nil {
		return 0, false, err
	}
	prevTotal, prevIdle, primed := s.prevTotal, s.prevIdle, s.primed
	s.prevTotal, s.prevIdle, s.primed = total, idle, true
	if !primed || total <= prevTotal {
		return 0, false, nil
	}
	dt := total - prevTotal
	// A counter that runs backwards (a CPU hot-unplugged between reads)
	// would underflow; the interval is simply not measurable.
	if idle < prevIdle || idle-prevIdle > dt {
		return 0, false, nil
	}
	return float64(dt-(idle-prevIdle)) / float64(dt), true, nil
}

// parseCPULine reads the aggregate line's jiffy counters: user, nice,
// system, idle, iowait, irq, softirq and steal make up the total; idle and
// iowait are the time the machine had nothing to run. Guest time is already
// counted inside user and nice, so it is not added again.
func parseCPULine(b []byte) (total, idle uint64, err error) {
	if !bytes.HasPrefix(b, []byte("cpu ")) {
		return 0, 0, fmt.Errorf("cat: %s does not start with the aggregate cpu line", ProcStat)
	}
	b = b[len("cpu "):]
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	field := 0
	for field < 8 {
		for len(b) > 0 && b[0] == ' ' {
			b = b[1:]
		}
		if len(b) == 0 {
			break
		}
		var v uint64
		digits := 0
		for len(b) > 0 && b[0] >= '0' && b[0] <= '9' {
			v = v*10 + uint64(b[0]-'0')
			b = b[1:]
			digits++
		}
		if digits == 0 {
			return 0, 0, fmt.Errorf("cat: malformed cpu field %d", field)
		}
		total += v
		if field == 3 || field == 4 {
			idle += v
		}
		field++
	}
	// Kernels since 2.6.11 report at least eight fields; fewer than the
	// four that include idle is not a line this sampler can measure.
	if field < 4 {
		return 0, 0, fmt.Errorf("cat: cpu line has %d fields", field)
	}
	return total, idle, nil
}
