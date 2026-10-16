// Package thinkpad drives the ThinkPad-specific hardware the shell has no
// generic view of: the ACPI fan, the lid logo light and the keyboard
// backlight.
//
// Everything here is written straight to sysfs or /proc, never through a
// shell, and every value that reaches a file is checked against a whitelist
// first. Hardware that does not exist is reported as absent, never as an
// error, so the plugin stays quiet on a laptop that is not a ThinkPad.
package thinkpad

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Nomadcxx/sysc-metrics"
)

// LED names thinkpad_acpi registers under /sys/class/leds.
const (
	LEDKeyboardBacklight = "tpacpi::kbd_backlight" // most T/X/P series
	LEDThinkLight        = "tpacpi::thinklight"    // older X-series "ThinkLight"
	LEDLidLogoDot        = "tpacpi::lid_logo_dot"  // models with the separate dot
	LEDLidLogoStrip      = "tpacpi::lid_logo"      // models with the full strip
)

// keyboardLEDNames and lidLEDNames are tried in order, first match wins, so a
// plugin written for a modern machine still finds the light on an older one.
var (
	keyboardLEDNames = []string{LEDKeyboardBacklight, LEDThinkLight}
	lidLEDNames      = []string{LEDLidLogoDot, LEDLidLogoStrip}
)

// Fan levels the kernel accepts.
const (
	// FanLevelAuto hands fan control back to the BIOS.
	FanLevelAuto = "auto"
	// FanLevelDisengaged runs the fan flat out regardless of temperature.
	FanLevelDisengaged = "disengaged"
	// MaxNumericFanLevel is the highest numbered fixed step thinkpad_acpi offers.
	MaxNumericFanLevel = 7
)

// Errors callers surface verbatim in the panel.
var (
	ErrNoBacklight = errors.New("this machine has no controllable keyboard backlight")
	ErrNoLidLight  = errors.New("this machine has no controllable lid logo light")
)

// Roots names the filesystem paths the hardware layer touches. It is injected
// rather than hardcoded so the whole layer can be tested against a fake sysfs
// tree on a machine that is not a ThinkPad.
type Roots struct {
	FanPath  string // /proc/acpi/ibm/fan
	LEDsRoot string // /sys/class/leds
}

// DefaultRoots returns the paths a real ThinkPad exposes.
func DefaultRoots() Roots {
	return Roots{FanPath: "/proc/acpi/ibm/fan", LEDsRoot: "/sys/class/leds"}
}

// FanLevels lists the accepted modes, with firmware control and full speed first.
func FanLevels() []string {
	levels := make([]string, 0, MaxNumericFanLevel+3)
	levels = append(levels, FanLevelAuto, FanLevelDisengaged)
	for i := range MaxNumericFanLevel + 1 {
		levels = append(levels, strconv.Itoa(i))
	}
	return levels
}

// FanLevelFullSpeed is the kernel's legacy spelling of FanLevelDisengaged. Its
// own help text advertises it — "level <level> (<level> is 0-7, auto,
// disengaged, full-speed)" — but reading the file back never returns it, since
// the EC reports the disengaged state. Normalising on the way in is what keeps
// a stored profile and the hardware from disagreeing forever.
const FanLevelFullSpeed = "full-speed"

// ValidFanLevel reports whether the kernel would accept level. Anything else is
// rejected here rather than written blindly. Note this deliberately excludes
// the fan's own "enable" and "disable" commands: stopping the fan outright is
// not a level, and nothing in this plugin should be one keystroke from a
// machine that cannot cool itself.
func ValidFanLevel(level string) bool {
	if level == FanLevelAuto || level == FanLevelDisengaged || level == FanLevelFullSpeed {
		return true
	}
	n, err := strconv.Atoi(level)
	return err == nil && n >= 0 && n <= MaxNumericFanLevel
}

// NormalizeFanLevel folds the spellings the kernel accepts but never reports
// back into the canonical form it does report, so what we store matches what
// the next read returns.
func NormalizeFanLevel(level string) string {
	if level == FanLevelFullSpeed {
		return FanLevelDisengaged
	}
	return level
}

// FanLevelNumber maps a level to its step, or -1 for the symbolic levels that
// carry no number.
func FanLevelNumber(level string) int {
	if level == FanLevelAuto || level == FanLevelDisengaged || level == FanLevelFullSpeed {
		return -1
	}
	n, err := strconv.Atoi(level)
	if err != nil || n < 0 || n > MaxNumericFanLevel {
		return -1
	}
	return n
}

// FanLevelLabel names a level for the UI.
func FanLevelLabel(level string) string {
	switch level {
	case FanLevelAuto:
		return "Auto"
	case FanLevelDisengaged, FanLevelFullSpeed:
		return "Full"
	}
	if n := FanLevelNumber(level); n >= 0 {
		return strconv.Itoa(n)
	}
	return "—"
}

// LEDState is one controllable light as it stands right now.
type LEDState struct {
	Name          string
	Present       bool
	Brightness    int
	MaxBrightness int
	// HWChanged is the kernel's "someone changed this outside software"
	// counter. When it moves, the user pressed the brightness key, and the UI
	// re-syncs to them instead of overwriting a deliberate keypress.
	HWChanged      int
	HWChangedValid bool
	Writable       bool
	Note           string
}

// On reports whether the light is lit. An absent light is never on.
func (l LEDState) On() bool { return l.Present && l.Brightness > 0 }

// Level is the brightness as a 0..1 fraction of maximum, for gauges.
func (l LEDState) Level() float64 {
	if !l.Present || l.MaxBrightness <= 0 {
		return 0
	}
	f := float64(l.Brightness) / float64(l.MaxBrightness)
	return min(max(f, 0), 1)
}

// Label names the light in plain language.
func (l LEDState) Label() string {
	switch l.Name {
	case LEDKeyboardBacklight, LEDThinkLight:
		return "Keyboard backlight"
	case LEDLidLogoDot, LEDLidLogoStrip:
		return "Lid logo"
	}
	if l.Name == "" {
		return ""
	}
	return l.Name
}

// ChangedOnHardware reports whether the light moved outside our control since
// the previous read.
func (l LEDState) ChangedOnHardware(previous LEDState) bool {
	return l.HWChangedValid && previous.HWChangedValid && l.HWChanged != previous.HWChanged
}

// State is one complete read of everything the plugin drives.
type State struct {
	HaveFan       bool
	FanStatus     string
	FanRPM        int
	FanValid      bool
	FanLevel      string
	FanLevelValid bool
	FanWritable   bool

	Keyboard LEDState
	Lid      LEDState

	CPUTempC     float64
	CPUTempValid bool

	// Notes are human-readable problems worth surfacing in the panel.
	Notes []string
}

// Hot reports whether the CPU has reached the overheat threshold.
func (s State) Hot(limit int) bool {
	return s.CPUTempValid && limit > 0 && s.CPUTempC >= float64(limit)
}

// Support describes what the machine actually exposes, so the panel can
// explain itself instead of showing dead controls.
type Support struct {
	Fan       bool
	Backlight bool
	Lid       bool
	Writable  bool // every present control can be changed
}

// Support reports which controls this machine actually has.
func (s State) Support() Support {
	hasControl, writable := false, true
	if s.HaveFan {
		hasControl = true
		writable = writable && s.FanWritable && s.FanLevelValid
	}
	if s.Keyboard.Present {
		hasControl = true
		writable = writable && s.Keyboard.Writable
	}
	if s.Lid.Present {
		hasControl = true
		writable = writable && s.Lid.Writable
	}
	return Support{
		Fan:       s.HaveFan,
		Backlight: s.Keyboard.Present,
		Lid:       s.Lid.Present,
		Writable:  hasControl && writable,
	}
}

// Any reports whether the machine is a ThinkPad at all. Without this the panel
// shows a "not supported" state instead of pretending.
func (s State) Any() bool { return s.HaveFan || s.Keyboard.Present || s.Lid.Present }

// NeedsSetup reports whether any present control cannot currently be changed.
func (s State) NeedsSetup() bool {
	if !s.Any() {
		return false
	}
	return !s.Support().Writable
}

// Hardware reads and writes the ThinkPad controls. It is safe for concurrent
// use.
type Hardware struct {
	roots Roots

	readFanSnapshot func() (metrics.FanSnapshot, error)
	readLEDSnapshot func() (metrics.LEDSnapshot, error)

	// readTemp is swapped out in tests. The default asks sysc-metrics for the
	// CPU package temperature, which only feeds the overheat warning.
	readTemp func() (float64, bool)

	mu sync.Mutex
	// writable caches the result of the write probe. An entry is dropped
	// whenever a real write is refused, so a permission fix made after the
	// plugin started lands on the next read without a restart.
	writable map[string]bool
}

// NewHardware returns hardware access rooted at the given paths.
func NewHardware(roots Roots) *Hardware {
	h := &Hardware{roots: roots, writable: map[string]bool{}}
	defaults := DefaultRoots()
	if roots.FanPath == defaults.FanPath {
		h.readFanSnapshot = metrics.ReadFan
	}
	if roots.LEDsRoot == defaults.LEDsRoot {
		h.readLEDSnapshot = metrics.ReadLEDs
	}
	h.readTemp = func() (float64, bool) {
		snap, err := metrics.ReadThermal()
		if err != nil {
			return 0, false
		}
		return snap.Celsius, snap.Valid
	}
	return h
}

// Roots reports the paths this hardware is reading.
func (h *Hardware) Roots() Roots { return h.roots }

// Read samples the hardware. Absent hardware is reported rather than returned
// as an error: a machine that is not a ThinkPad simply has nothing to show.
func (h *Hardware) Read() State {
	st := State{}
	if h.readFanSnapshot != nil {
		h.readMetricsFan(&st)
	} else {
		h.readFan(&st)
	}
	if h.readLEDSnapshot != nil {
		h.readMetricsLEDs(&st)
	} else {
		h.readLED(&st.Keyboard, keyboardLEDNames)
		h.readLED(&st.Lid, lidLEDNames)
	}
	if celsius, ok := h.readTemp(); ok {
		st.CPUTempC, st.CPUTempValid = celsius, true
	}
	// A light that reads but cannot be written gets its reason on its own
	// LEDState, where the card for that light can show it. Lift it into the
	// shared notes as well: on a stock machine every target is unwritable at
	// once, and the panel's setup card needs to say why rather than render a
	// row of controls that quietly refuse.
	if st.Keyboard.Present && !st.Keyboard.Writable {
		st.Notes = append(st.Notes, "the keyboard backlight is read-only: "+st.Keyboard.Note)
	}
	if st.Lid.Present && !st.Lid.Writable {
		st.Notes = append(st.Notes, "the lid logo light is read-only: "+st.Lid.Note)
	}
	return st
}

// SetFanLevel pins the fan. Auto hands control back to the BIOS, Disengaged
// runs it flat out, and 0-7 are fixed steps. A level the kernel would not
// accept is rejected here rather than written blindly.
func (h *Hardware) SetFanLevel(level string) error {
	if !ValidFanLevel(level) {
		return fmt.Errorf("unsupported fan level %q", level)
	}
	// Write the spelling the kernel echoes back, not the one the user typed.
	canonical := NormalizeFanLevel(level)
	if err := writeAttr(h.roots.FanPath, "level "+canonical+"\n"); err != nil {
		h.forget(h.roots.FanPath)
		return err
	}
	return nil
}

// SetKeyboard sets the keyboard backlight, clamped to whatever the machine
// reports as its maximum.
func (h *Hardware) SetKeyboard(level int) error {
	name := resolveLED(h.roots, keyboardLEDNames)
	if name == "" {
		return ErrNoBacklight
	}
	led := h.readLEDOne(name)
	if !led.Present {
		return ErrNoBacklight
	}
	return h.writeLED(led, clampLevel(level, led.MaxBrightness))
}

// SetLid lights or extinguishes the lid logo.
func (h *Hardware) SetLid(on bool) error {
	if !on {
		return h.SetLidBrightness(0)
	}
	name := resolveLED(h.roots, lidLEDNames)
	if name == "" {
		return ErrNoLidLight
	}
	led := h.readLEDOne(name)
	if !led.Present {
		return ErrNoLidLight
	}
	// The dot and the strip both treat any non-zero value as lit; the sysfs
	// maximum is the correct one where it is readable.
	value := led.MaxBrightness
	if value <= 0 {
		value = 255
	}
	return h.SetLidBrightness(value)
}

// SetLidBrightness writes an explicit lid brightness, clamped to what the LED
// advertises. SetLid is the on/off shorthand over this.
func (h *Hardware) SetLidBrightness(level int) error {
	name := resolveLED(h.roots, lidLEDNames)
	if name == "" {
		return ErrNoLidLight
	}
	led := h.readLEDOne(name)
	if !led.Present {
		return ErrNoLidLight
	}
	return h.writeLED(led, level)
}

func (h *Hardware) readFan(st *State) {
	f, err := os.Open(h.roots.FanPath)
	if err != nil {
		// No thinkpad_acpi, or no permission to look. Either way there is no
		// fan to report and nothing to complain about.
		return
	}
	defer f.Close()

	st.HaveFan = true
	st.FanWritable = h.writableFor(h.roots.FanPath)

	levelIssue := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))
		switch key {
		case "status":
			st.FanStatus = value
		case "speed":
			// A parked fan legitimately reports 0, so only an unparsable
			// value invalidates the reading.
			if rpm, err := strconv.Atoi(value); err == nil && rpm >= 0 {
				st.FanRPM, st.FanValid = rpm, true
			} else {
				st.Notes = append(st.Notes, "fan speed is unreadable: "+value)
			}
		case "level":
			if ValidFanLevel(value) {
				st.FanLevel, st.FanLevelValid = value, true
			} else {
				st.Notes = append(st.Notes, "unsupported fan level reported: "+value)
				levelIssue = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		st.Notes = append(st.Notes, "fan read failed: "+err.Error())
	}
	h.noteFanState(st, levelIssue)
}

func (h *Hardware) readMetricsFan(st *State) {
	fan, err := h.readFanSnapshot()
	if err != nil {
		st.Notes = append(st.Notes, "fan read failed: "+err.Error())
		return
	}
	if !fan.Present {
		return
	}
	st.HaveFan = true
	st.FanStatus = fan.Status
	st.FanRPM, st.FanValid = fan.SpeedRPM, fan.SpeedValid
	st.FanLevel, st.FanLevelValid = NormalizeFanLevel(fan.Level), fan.LevelValid
	st.FanWritable = h.writableFor(h.roots.FanPath)
	levelIssue := false
	for _, issue := range fan.Issues {
		if issue.Err != nil {
			switch {
			case strings.HasSuffix(issue.Source, ":speed"):
				st.Notes = append(st.Notes, "fan speed is unreadable: "+issue.Err.Error())
			case strings.HasSuffix(issue.Source, ":level"):
				st.Notes = append(st.Notes, "unsupported fan level reported: "+issue.Err.Error())
				levelIssue = true
			default:
				st.Notes = append(st.Notes, "fan read failed: "+issue.Err.Error())
			}
		}
	}
	h.noteFanState(st, levelIssue)
}

func (h *Hardware) noteFanState(st *State, levelIssue bool) {
	if st.FanStatus == "disabled" {
		// The fan's own "disable" command stops it outright. This plugin never
		// issues it, but if something else did, the panel must not imply that a
		// chosen level is doing anything.
		st.Notes = append(st.Notes,
			"the fan is switched off, so no level will spin it until it is re-enabled.")
	}
	if !st.FanLevelValid && st.FanWritable && !levelIssue {
		// Reads work but there is no level line at all: the module loaded
		// without fan_control=1, so the kernel will refuse every write.
		st.Notes = append(st.Notes,
			"thinkpad_acpi is loaded without fan_control=1, so fan levels cannot be set.")
	} else if !st.FanWritable {
		// The file opens for reading but not for writing. Until the udev rule
		// hands it to a group the user belongs to, every level change fails.
		st.Notes = append(st.Notes,
			"the fan is read-only: write access to "+h.roots.FanPath+" is not granted.")
	}
}

func (h *Hardware) readMetricsLEDs(st *State) {
	snapshot, err := h.readLEDSnapshot()
	if err != nil {
		st.Notes = append(st.Notes, "LED read failed: "+err.Error())
		return
	}
	st.Keyboard = ledFromMetrics(snapshot, keyboardLEDNames)
	st.Lid = ledFromMetrics(snapshot, lidLEDNames)
}

func ledFromMetrics(snapshot metrics.LEDSnapshot, names []string) LEDState {
	for _, name := range names {
		led, ok := metrics.FindLED(snapshot, name)
		if !ok || !led.BrightnessValid || led.Brightness < 0 {
			continue
		}
		state := LEDState{
			Name: led.Name, Present: true, Brightness: led.Brightness,
			MaxBrightness: led.MaxBrightness, Writable: led.Writable,
			HWChanged: led.HWChanged, HWChangedValid: led.HWChangedValid,
		}
		if !state.Writable {
			state.Note = "write access is not granted yet"
		}
		return state
	}
	return LEDState{}
}

func (h *Hardware) readLED(dst *LEDState, names []string) {
	for _, name := range names {
		if led := h.readLEDOne(name); led.Present {
			*dst = led
			return
		}
	}
}

func (h *Hardware) readLEDOne(name string) LEDState {
	dir := filepath.Join(h.roots.LEDsRoot, name)
	led := LEDState{Name: name}
	if _, err := os.Stat(dir); err != nil {
		return led // this model does not have it
	}

	brightness := filepath.Join(dir, "brightness")
	raw, err := os.ReadFile(brightness)
	if err != nil {
		// Not a dimmable control, or an attribute we cannot read. Either way it
		// is not something the panel can drive, so report it as absent instead
		// of offering brightness steps that would never land.
		led.Note = "brightness is unreadable"
		return led
	}
	if value, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && value >= 0 {
		led.Brightness = value
		led.Present = true
	} else {
		led.Note = "brightness is not a number"
		return led
	}
	if value, err := readLEDInt(filepath.Join(dir, "max_brightness")); err == nil && value > 0 {
		led.MaxBrightness = value
	}
	if value, err := readLEDInt(filepath.Join(dir, "brightness_hw_changed")); err == nil {
		led.HWChanged, led.HWChangedValid = value, true
	}
	led.Writable = h.writableFor(brightness)
	if !led.Writable {
		led.Note = "write access is not granted yet"
	}
	return led
}

func (h *Hardware) writeLED(led LEDState, value int) error {
	path := filepath.Join(h.roots.LEDsRoot, led.Name, "brightness")
	if err := writeAttr(path, strconv.Itoa(value)); err != nil {
		h.forget(path)
		return fmt.Errorf("%s: %w", led.Name, err)
	}
	return nil
}

// writableFor reports whether a write to path would be accepted. Opening an
// attribute for writing has no effect -- only write(2) touches the hardware --
// so this answers the question exactly, with no guessing at permission bits
// that udev may rewrite at any moment.
func (h *Hardware) writableFor(path string) bool {
	h.mu.Lock()
	cached, known := h.writable[path]
	h.mu.Unlock()
	if known {
		return cached
	}

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	writable := err == nil
	if writable {
		_ = f.Close()
	}

	h.mu.Lock()
	h.writable[path] = writable
	h.mu.Unlock()
	return writable
}

// forget drops one cached probe so the next read re-checks it.
func (h *Hardware) forget(path string) {
	h.mu.Lock()
	delete(h.writable, path)
	h.mu.Unlock()
}

// ForgetWriteProbes clears every cached probe. The panel calls this after the
// user is told to fix permissions, so the "needs setup" state clears the
// moment the fix is actually in place.
func (h *Hardware) ForgetWriteProbes() {
	h.mu.Lock()
	clear(h.writable)
	h.mu.Unlock()
}

// resolveLED returns the first candidate name that exists under the LED root.
func resolveLED(roots Roots, names []string) string {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(roots.LEDsRoot, name)); err == nil {
			return name
		}
	}
	return ""
}

// fanAvailable reports whether this machine exposes a fan control at all, so
// the plugin does not report a failure for a fan that was never there.
func (h *Hardware) fanAvailable() bool {
	f, err := os.Open(h.roots.FanPath)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// keyboardAvailable reports whether this machine has a controllable keyboard
// backlight at all, so the plugin does not report a failure for a light that
// was never there.
func (h *Hardware) keyboardAvailable() bool {
	return resolveLED(h.roots, keyboardLEDNames) != ""
}

// lidAvailable reports whether this machine has a controllable lid logo.
func (h *Hardware) lidAvailable() bool {
	return resolveLED(h.roots, lidLEDNames) != ""
}

// clampLevel keeps a requested brightness inside what the light supports. A
// light that does not report a maximum is treated as a single step rather than
// a zero-width range.
func clampLevel(level, highest int) int {
	if highest <= 0 {
		highest = 1
	}
	return min(max(level, 0), highest)
}

// writeAttr replaces a sysfs or proc attribute. O_WRONLY is used alone:
// sysfs attributes reject O_CREATE and O_TRUNC, and the proc fan file wants
// neither.
func writeAttr(path, data string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readLEDInt(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}
