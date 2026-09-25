// Package cat is a bar cat whose behaviour follows CPU load: it walks as
// load picks up and gallops when the machine is busy; below the sleep line
// it sits, grooms, scratches and stretches, and after a while it naps.
// Behaviour was ported from the noctalia Cat and DMS Cat Widget plugins and
// widened past them; the artwork is the shell's own catalogue glyphs,
// painted by the host in a theme tone and animated on the host's clock.
package cat

import (
	"math"
	"math/rand/v2"
	"strconv"
	"time"
)

// Act is what the cat is doing.
type Act int

const (
	Sit Act = iota
	Groom
	Scratch
	Stretch
	Sleep
	Walk
	Run
)

// idle reports whether an act belongs to the cat at rest.
func (a Act) idle() bool { return a != Walk && a != Run }

// script is one act's pose cycle: the catalogue poses in the order the host
// steps through them, repeating a pose where the motion returns through it,
// and how long one pass takes when the act has a fixed pace.
type script struct {
	frames []string
	cycle  time.Duration
}

func poses(act string, order ...int) []string {
	out := make([]string, len(order))
	for i, n := range order {
		out[i] = "cat-" + act + "-" + strconv.Itoa(n)
	}
	return out
}

// scripts are built once; a Motion hands the same slices out every time.
var scripts = map[Act]script{
	// A slow tail swish, blinking once every other swish.
	Sit: {poses("sit", 0, 1, 2, 1, 0, 1, 2, 1, 0, 3), 5 * time.Second},
	// Paw to the mouth, two licks, then a wash over the face.
	Groom: {poses("groom", 0, 1, 2, 1, 2, 1, 3, 4, 3, 0), 3 * time.Second},
	// The hind foot rakes behind the ear.
	Scratch: {poses("scratch", 0, 1, 2, 1, 2, 1, 2, 1, 2, 0), 1600 * time.Millisecond},
	// Down into a bow, a yawn, and back up.
	Stretch: {poses("stretch", 0, 1, 2, 3, 3, 2, 1, 0), 2800 * time.Millisecond},
	// Breathing.
	Sleep: {poses("sleep", 0, 1, 2, 3), 3200 * time.Millisecond},
	// One pass is one step: a walk's silhouette repeats every half stride.
	Walk: {poses("walk", 0, 1, 2, 3, 4, 5, 6, 7), 0},
	// One pass is one gallop stride.
	Run: {poses("run", 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11), 0},
}

// The locomotion paces. A walk's step runs from an amble to a brisk walk
// across the bottom of the load band; the gallop takes over above it and
// reaches a stride every 0.4 s at top speed, thirty poses a second.
const (
	SlowestStep   = 1200 * time.Millisecond
	FastestStep   = 550 * time.Millisecond
	SlowestStride = 1000 * time.Millisecond
	FastestStride = 400 * time.Millisecond
)

// The walk and the gallop meet at runFrom of the load band; the switch
// back sits a little lower so a machine hovering at the boundary does not
// make the cat change gait at every sample.
const (
	runFrom  = 0.38
	walkFrom = 0.32
)

// hysteresis is how many points below the sleep line the load has to fall
// before an active cat settles down again.
const hysteresis = 3

// easeStep and easeShare pace a speed change: the published cycle closes
// half its gap to the target every quarter second, so a load spike reads
// as the cat accelerating over a second rather than as a cut between two
// speeds. The host keeps the pose on screen across each retarget.
const (
	easeStep  = 250 * time.Millisecond
	easeShare = 0.5
)

// How long the cat sits between idle acts, and how many passes each act
// plays before it sits back down.
const (
	minSit = 6 * time.Second
	maxSit = 14 * time.Second
)

// Thresholds are the user's load bands in whole percent. The cat is idle
// below SleepBelow (zero means it is never idle) and reaches top speed at
// TopAt.
type Thresholds struct {
	SleepBelow int
	TopAt      int
}

// normalized clamps the bands into a usable order: a top speed at or below
// the sleep line would leave no range to move in.
func (t Thresholds) normalized() Thresholds {
	t.SleepBelow = min(max(t.SleepBelow, 0), 99)
	t.TopAt = min(max(t.TopAt, t.SleepBelow+1), 100)
	return t
}

// Motion is what the host animates: the act's poses and one pass's length.
type Motion struct {
	Act    Act
	Frames []string
	Cycle  time.Duration
}

// Cat is the behaviour state. It is not safe for concurrent use; the plugin
// drives it from its one event loop.
type Cat struct {
	bands    Thresholds
	napAfter time.Duration
	rng      *rand.Rand

	load  float64
	known bool

	act      Act
	actUntil time.Time // when an idle act ends; zero means open-ended
	idleFrom time.Time // when the load last fell below the sleep line

	cycle  time.Duration // the cycle last published
	target time.Duration // the cycle the load asks for
	easeAt time.Time     // when the next ease step is due; zero means none
}

// New returns a sitting cat that has not seen a reading. seed makes the idle
// acts' timing reproducible in tests; the plugin seeds from the clock.
func New(bands Thresholds, napAfter time.Duration, seed uint64) *Cat {
	return &Cat{
		bands:    bands.normalized(),
		napAfter: napAfter,
		rng:      rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		act:      Sit,
		cycle:    scripts[Sit].cycle,
		target:   scripts[Sit].cycle,
	}
}

// SetThresholds applies new bands and re-decides the act against the
// current load.
func (c *Cat) SetThresholds(bands Thresholds, napAfter time.Duration, now time.Time) {
	c.bands = bands.normalized()
	c.napAfter = napAfter
	if c.known {
		c.Observe(c.load, now)
	}
}

// Load reports the latest reading and whether there has been one.
func (c *Cat) Load() (float64, bool) { return c.load, c.known }

// Percent is the latest reading rounded to a whole percent.
func (c *Cat) Percent() int { return int(math.Round(c.load * 100)) }

// Act is what the cat is doing now.
func (c *Cat) Act() Act { return c.act }

// speed is where the load sits between the sleep line and top speed, zero
// through one.
func (c *Cat) speed() float64 {
	lo, hi := float64(c.bands.SleepBelow), float64(c.bands.TopAt)
	return min(max((c.load*100-lo)/(hi-lo), 0), 1)
}

// geometric interpolates from slow to fast so each step of load changes the
// pace by the same visible proportion.
func geometric(slow, fast time.Duration, u float64) time.Duration {
	return time.Duration(float64(slow) * math.Pow(float64(fast)/float64(slow), min(max(u, 0), 1)))
}

// locomotion is the gait and pace the load asks for, given the current gait
// for hysteresis.
func (c *Cat) locomotion() (Act, time.Duration) {
	u := c.speed()
	act := Walk
	if u >= runFrom || (c.act == Run && u >= walkFrom) {
		act = Run
	}
	if act == Walk {
		return Walk, geometric(SlowestStep, FastestStep, u/runFrom)
	}
	return Run, geometric(SlowestStride, FastestStride, (u-walkFrom)/(1-walkFrom))
}

// Observe records a load reading, zero through one, and decides what the
// cat does about it.
func (c *Cat) Observe(load float64, now time.Time) {
	if math.IsNaN(load) {
		return
	}
	c.load = min(max(load, 0), 1)
	c.known = true
	pct := c.load * 100
	line := float64(c.bands.SleepBelow)
	busy := c.bands.SleepBelow == 0 || pct >= line
	if c.act.idle() && !busy {
		if c.idleFrom.IsZero() {
			c.settle(now) // the first idle reading starts the idle schedule
		}
		return // still idle: the schedule runs from Tick
	}
	if !c.act.idle() && c.bands.SleepBelow > 0 && pct < line-hysteresis {
		c.settle(now)
		return
	}
	if c.act.idle() {
		// Work arrived. A napping cat stretches before it moves off; one
		// already up goes straight into its gait.
		if c.act == Sleep {
			c.begin(Stretch, now, 1)
			return
		}
		if c.act == Stretch {
			return // the stretch finishes first
		}
	}
	act, cycle := c.locomotion()
	if act != c.act {
		c.move(act, cycle)
		return
	}
	if cycle != c.target {
		c.target = cycle
		c.easeAt = now
	}
}

// move puts the cat into a gait at its target pace. A new gait is a new
// pose list, which the host starts from its first pose, so there is no
// speed to ease from.
func (c *Cat) move(act Act, cycle time.Duration) {
	c.act, c.actUntil, c.idleFrom = act, time.Time{}, time.Time{}
	c.cycle, c.target, c.easeAt = cycle, cycle, time.Time{}
}

// settle is the load dropping below the sleep line.
func (c *Cat) settle(now time.Time) {
	c.idleFrom = now
	if c.napAfter <= 0 {
		c.begin(Sleep, now, 0)
		return
	}
	c.begin(Sit, now, 0)
}

// begin starts an act. passes bounds a one-off act to that many cycles; zero
// leaves it open-ended, except that a sitting cat picks its next idle act
// after a random spell.
func (c *Cat) begin(act Act, now time.Time, passes int) {
	c.act = act
	c.cycle, c.target, c.easeAt = scripts[act].cycle, scripts[act].cycle, time.Time{}
	switch {
	case passes > 0:
		c.actUntil = now.Add(time.Duration(passes) * scripts[act].cycle)
	case act == Sit:
		c.actUntil = now.Add(minSit + time.Duration(c.rng.Int64N(int64(maxSit-minSit))))
	default:
		c.actUntil = time.Time{}
	}
}

// Tick advances whatever is due at now and reports whether the motion
// changed, which is when the plugin republishes.
func (c *Cat) Tick(now time.Time) bool {
	changed := false
	if !c.easeAt.IsZero() && !now.Before(c.easeAt) {
		next := c.cycle + time.Duration(float64(c.target-c.cycle)*easeShare)
		if d := next - c.target; math.Abs(float64(d)) < 0.02*float64(c.target) {
			next = c.target
		}
		changed = next != c.cycle
		c.cycle = next
		c.easeAt = time.Time{}
		if c.cycle != c.target {
			c.easeAt = now.Add(easeStep)
		}
	}
	if c.actUntil.IsZero() || now.Before(c.actUntil) {
		return changed
	}
	switch c.act {
	case Stretch:
		// Out of the stretch into whatever the load now asks for.
		if c.busy() {
			c.move(c.locomotion())
			return true
		}
		c.begin(Sit, now, 0)
	case Sit:
		if c.napAfter > 0 && !c.idleFrom.IsZero() && now.Sub(c.idleFrom) >= c.napAfter {
			c.begin(Sleep, now, 0)
			return true
		}
		c.begin(c.pickIdle(), now, 0)
		if c.act != Sit {
			c.actUntil = now.Add(2 * scripts[c.act].cycle)
			if c.act == Stretch {
				c.actUntil = now.Add(scripts[Stretch].cycle)
			}
		}
	default:
		c.begin(Sit, now, 0)
	}
	return true
}

func (c *Cat) busy() bool {
	return c.known && (c.bands.SleepBelow == 0 || c.load*100 >= float64(c.bands.SleepBelow))
}

// pickIdle chooses what a sitting cat does next: mostly it keeps sitting.
func (c *Cat) pickIdle() Act {
	switch r := c.rng.IntN(100); {
	case r < 50:
		return Sit
	case r < 72:
		return Groom
	case r < 88:
		return Scratch
	}
	return Stretch
}

// Next is how long until Tick has work, or zero when nothing is scheduled.
func (c *Cat) Next(now time.Time) time.Duration {
	var next time.Time
	for _, t := range []time.Time{c.easeAt, c.actUntil} {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	if next.IsZero() {
		return 0
	}
	return max(next.Sub(now), time.Millisecond)
}

// Motion is the current act's poses and pass length.
func (c *Cat) Motion() Motion {
	return Motion{Act: c.act, Frames: scripts[c.act].frames, Cycle: c.cycle}
}

// Label names what the cat is doing, for the panel and the tooltip.
func (c *Cat) Label() string {
	switch c.act {
	case Groom:
		return "Grooming"
	case Scratch:
		return "Scratching"
	case Stretch:
		return "Stretching"
	case Sleep:
		return "Asleep"
	case Walk:
		if c.speed() < 0.15 {
			return "Strolling"
		}
		return "Walking"
	case Run:
		switch u := c.speed(); {
		case u >= 1:
			return "Zooming"
		case u >= 0.7:
			return "Sprinting"
		}
		return "Running"
	}
	return "Sitting"
}
