package cat

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Unix(1_000_000, 0)

func newCat(sleepBelow int, nap time.Duration) *Cat {
	return New(Thresholds{SleepBelow: sleepBelow, TopAt: 80}, nap, 1)
}

// run advances the cat's own schedule to until, as the plugin's behaviour
// timer does, and returns the acts it passed through.
func run(c *Cat, from, until time.Time) []Act {
	var acts []Act
	now := from
	for {
		d := c.Next(now)
		if d == 0 || now.Add(d).After(until) {
			return acts
		}
		now = now.Add(d)
		if c.Tick(now) {
			acts = append(acts, c.Act())
		}
	}
}

func TestEveryScriptNamesCataloguePosesInItsAct(t *testing.T) {
	t.Parallel()
	for act, s := range scripts {
		if len(s.frames) < 2 || len(s.frames) > 32 {
			t.Fatalf("act %d has %d poses", act, len(s.frames))
		}
		prefix := strings.TrimSuffix(s.frames[0], "-0")
		for _, f := range s.frames {
			if !strings.HasPrefix(f, prefix+"-") {
				t.Fatalf("act %d mixes %s into %s", act, f, prefix)
			}
		}
		if act.idle() && s.cycle <= 0 {
			t.Fatalf("idle act %d has no fixed pace", act)
		}
	}
}

func TestCatSitsUntilItHasAReading(t *testing.T) {
	t.Parallel()
	c := newCat(10, time.Minute)
	if c.Act() != Sit || c.Label() != "Sitting" {
		t.Fatalf("new cat: %v %s", c.Act(), c.Label())
	}
	if _, known := c.Load(); known {
		t.Fatal("a new cat claims a reading")
	}
}

// Idle, the cat does more than sit: over a few minutes it grooms, scratches
// or stretches, returns to sitting between acts, and finally naps.
func TestIdleCatFillsItsTimeThenNaps(t *testing.T) {
	t.Parallel()
	c := newCat(10, 2*time.Minute)
	c.Observe(0.02, t0)
	acts := run(c, t0, t0.Add(3*time.Minute))
	seen := map[Act]bool{}
	for i, a := range acts {
		seen[a] = true
		if i > 0 && a != Sit && acts[i-1] != Sit && acts[i-1] != a {
			t.Fatalf("went from %v straight to %v without sitting down", acts[i-1], a)
		}
	}
	if !seen[Groom] && !seen[Scratch] && !seen[Stretch] {
		t.Fatalf("three idle minutes held no idle act: %v", acts)
	}
	if c.Act() != Sleep {
		t.Fatalf("after three idle minutes with a two-minute nap: %v", c.Act())
	}
}

func TestZeroNapSleepsStraightAway(t *testing.T) {
	t.Parallel()
	c := newCat(10, 0)
	c.Observe(0.02, t0)
	if c.Act() != Sleep {
		t.Fatalf("act = %v, want asleep at once", c.Act())
	}
}

// A napping cat stretches before it moves off; one already up does not.
func TestWakingCatStretchesFirst(t *testing.T) {
	t.Parallel()
	c := newCat(10, 0)
	c.Observe(0.02, t0)
	c.Observe(0.30, t0.Add(time.Second))
	if c.Act() != Stretch {
		t.Fatalf("woke into %v, want a stretch", c.Act())
	}
	run(c, t0.Add(time.Second), t0.Add(10*time.Second))
	if c.Act() != Walk {
		t.Fatalf("after the stretch: %v, want a walk at 30%%", c.Act())
	}

	up := newCat(10, time.Hour)
	up.Observe(0.02, t0)
	up.Observe(0.30, t0.Add(time.Second))
	if up.Act() != Walk {
		t.Fatalf("a sitting cat went into %v, want straight into a walk", up.Act())
	}
}

// A machine idling at the line must not make the cat fidget between sitting
// and walking at every sample.
func TestSettlingHasHysteresis(t *testing.T) {
	t.Parallel()
	c := newCat(10, time.Hour)
	now := t0
	for i, s := range []struct {
		load   float64
		moving bool
	}{
		{0.05, false}, {0.10, true}, {0.09, true}, {0.075, true}, {0.069, false}, {0.09, false}, {0.10, true},
	} {
		now = now.Add(2 * time.Second)
		c.Observe(s.load, now)
		if moving := !c.Act().idle(); moving != s.moving {
			t.Fatalf("step %d load %.3f: act %v", i, s.load, c.Act())
		}
	}
}

func TestLoadPicksTheGait(t *testing.T) {
	t.Parallel()
	c := newCat(0, time.Minute)
	for _, tc := range []struct {
		load  float64
		act   Act
		label string
	}{
		{0.05, Walk, "Strolling"}, {0.20, Walk, "Walking"}, {0.40, Run, "Running"}, {0.65, Run, "Sprinting"}, {0.95, Run, "Zooming"},
	} {
		c.Observe(tc.load, t0)
		if c.Act() != tc.act || c.Label() != tc.label {
			t.Fatalf("load %.2f: %v %q, want %v %q", tc.load, c.Act(), c.Label(), tc.act, tc.label)
		}
	}
	// Hysteresis at the gait boundary: dropping just under the switch keeps
	// the gallop.
	c.Observe(0.28, t0)
	if c.Act() != Run {
		t.Fatalf("just under the switch the cat dropped to %v", c.Act())
	}
	c.Observe(0.20, t0)
	if c.Act() != Walk {
		t.Fatalf("well under the switch the cat kept %v", c.Act())
	}
}

// Every extra point of load quickens the gait, from an amble at the idle
// line to the fastest stride at top speed and never past it.
func TestPaceQuickensAcrossTheBand(t *testing.T) {
	t.Parallel()
	c := newCat(10, time.Minute)
	prev := time.Duration(1 << 62)
	prevAct := Walk
	for pct := 10; pct <= 100; pct++ {
		fresh := newCat(10, time.Minute)
		fresh.Observe(float64(pct)/100, t0)
		m := fresh.Motion()
		if m.Act != prevAct {
			prev = 1 << 62 // a new gait has its own pace ladder
			prevAct = m.Act
		}
		if pct <= 80 && m.Cycle >= prev {
			t.Fatalf("%d%%: %v cycle %v not faster than %v", pct, m.Act, m.Cycle, prev)
		}
		if m.Cycle < FastestStride {
			t.Fatalf("%d%%: cycle %v past the fastest stride", pct, m.Cycle)
		}
		prev = m.Cycle
	}
	c.Observe(1, t0)
	if got := c.Motion().Cycle; got != FastestStride {
		t.Fatalf("top speed cycle = %v", got)
	}
}

// A load spike eases the published pace over a few quarter-second steps
// rather than jumping, and lands exactly on the target.
func TestSpeedChangesEase(t *testing.T) {
	t.Parallel()
	c := newCat(0, time.Minute)
	c.Observe(0.45, t0)
	start := c.Motion().Cycle
	c.Observe(0.95, t0.Add(2*time.Second))
	if c.Motion().Cycle != start {
		t.Fatal("the pace jumped on the sample instead of easing")
	}
	now := t0.Add(2 * time.Second)
	var steps []time.Duration
	for i := 0; i < 20 && c.Next(now) > 0; i++ {
		now = now.Add(c.Next(now))
		if c.Tick(now) {
			steps = append(steps, c.Motion().Cycle)
		}
	}
	if len(steps) < 3 {
		t.Fatalf("eased in %d steps: %v", len(steps), steps)
	}
	for i := 1; i < len(steps); i++ {
		if steps[i] > steps[i-1] {
			t.Fatalf("easing slowed down: %v", steps)
		}
	}
	if last := steps[len(steps)-1]; last != FastestStride {
		t.Fatalf("easing settled on %v, want %v", last, FastestStride)
	}
}

func TestNextIsZeroWhenNothingIsDue(t *testing.T) {
	t.Parallel()
	c := newCat(10, 0)
	c.Observe(0.02, t0) // straight to sleep: open-ended
	if d := c.Next(t0); d != 0 {
		t.Fatalf("a sleeping cat schedules a wake in %v", d)
	}
}

func TestThresholdsNormalize(t *testing.T) {
	t.Parallel()
	got := Thresholds{SleepBelow: 60, TopAt: 40}.normalized()
	if got.TopAt <= got.SleepBelow {
		t.Fatalf("normalized %+v leaves no range to move in", got)
	}
	if got := (Thresholds{SleepBelow: -5, TopAt: 500}).normalized(); got.SleepBelow != 0 || got.TopAt != 100 {
		t.Fatalf("normalized = %+v", got)
	}
}

// Every idle line the settings allow must have a load below which a moving
// cat settles; a fixed three-point band left lines of 1 to 3 with none.
func TestEveryIdleLineCanSettle(t *testing.T) {
	t.Parallel()
	for line := 1; line <= 50; line++ {
		c := newCat(line, time.Hour)
		c.Observe(float64(line)/100, t0)
		if c.Act().idle() {
			t.Fatalf("line %d: the cat did not get up at the line", line)
		}
		c.Observe(0, t0.Add(2*time.Second))
		if !c.Act().idle() {
			t.Fatalf("line %d: at 0%% load the cat kept %v", line, c.Act())
		}
	}
}
