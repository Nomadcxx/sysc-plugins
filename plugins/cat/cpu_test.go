package cat

import (
	"bytes"
	"math"
	"os"
	"strings"
	"testing"
)

func TestParseCPULine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          string
		total, idle uint64
		wantErr     bool
	}{
		{"modern kernel", "cpu  10 20 30 400 50 6 7 8 9 10\ncpu0 1 2 3 4 5 6 7 8 9 10\n", 531, 450, false},
		{"no trailing newline", "cpu  1 2 3 4 5 6 7 8", 36, 9, false},
		{"four fields", "cpu 1 1 1 7\n", 10, 7, false},
		{"per-core line first", "cpu0 1 2 3 4\n", 0, 0, true},
		{"too few fields", "cpu 1 2 3\n", 0, 0, true},
		{"garbage", "cpu  1 x 3 4\n", 0, 0, true},
		{"empty", "", 0, 0, true},
	}
	for _, tc := range cases {
		total, idle, err := parseCPULine([]byte(tc.in))
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err = %v, want error %v", tc.name, err, tc.wantErr)
		}
		if err == nil && (total != tc.total || idle != tc.idle) {
			t.Fatalf("%s: total, idle = %d, %d; want %d, %d", tc.name, total, idle, tc.total, tc.idle)
		}
	}
}

// fakeStat serves whichever reading the test sets, as procfs does on each
// read from offset zero.
type fakeStat struct{ text string }

func (f *fakeStat) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader([]byte(f.text)).ReadAt(p, off)
}

func TestSamplerMeasuresTheIntervalBetweenReads(t *testing.T) {
	t.Parallel()
	stat := &fakeStat{text: "cpu  100 0 100 800 0 0 0 0 0 0\n"}
	s := &Sampler{f: stat}
	if _, ok, err := s.Sample(); ok || err != nil {
		t.Fatalf("baseline read: ok=%v err=%v, want no figure yet", ok, err)
	}
	// 100 more busy jiffies out of 400 is a quarter load.
	stat.text = "cpu  150 0 150 1100 0 0 0 0 0 0\n"
	load, ok, err := s.Sample()
	if !ok || err != nil || math.Abs(load-0.25) > 1e-9 {
		t.Fatalf("load = %v ok=%v err=%v, want 0.25", load, ok, err)
	}
	// No accounted time is no figure, not a division by zero.
	if _, ok, _ := s.Sample(); ok {
		t.Fatal("an unchanged reading produced a load figure")
	}
	// Counters that run backwards are skipped, then measuring resumes.
	stat.text = "cpu  10 0 10 100 0 0 0 0 0 0\n"
	if _, ok, _ := s.Sample(); ok {
		t.Fatal("a backwards reading produced a load figure")
	}
	stat.text = "cpu  10 0 10 200 0 0 0 0 0 0\n"
	if load, ok, _ := s.Sample(); !ok || load != 0 {
		t.Fatalf("idle interval load = %v ok=%v, want 0", load, ok)
	}
}

// A sample runs every few seconds for as long as the shell does, so it must
// not feed the garbage collector. Not parallel: AllocsPerRun needs the
// process to itself.
func TestSamplerReadsTheLiveKernelFileWithoutAllocating(t *testing.T) {
	if _, err := os.Stat(ProcStat); err != nil {
		t.Skipf("no %s: %v", ProcStat, err)
	}
	s, err := OpenSampler(ProcStat)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	load, ok, err := s.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if ok && (load < 0 || load > 1) {
		t.Fatalf("live load %v is outside zero through one", load)
	}
	allocs := testing.AllocsPerRun(20, func() { _, _, _ = s.Sample() })
	if allocs != 0 {
		t.Fatalf("a live sample allocates %.0f times", allocs)
	}
}

func TestSamplerErrorsNameTheFileRead(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/stat"
	if err := os.WriteFile(path, []byte("intr 1 2 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenSampler(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}
}
