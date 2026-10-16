package thinkpad

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-metrics"
)

// newTestHardware builds a Hardware rooted at a throwaway directory so the
// whole layer can be exercised on a machine that is not a ThinkPad.
func newTestHardware(t *testing.T, fan string, leds map[string]string) *Hardware {
	t.Helper()
	dir := t.TempDir()
	roots := Roots{FanPath: filepath.Join(dir, "fan"), LEDsRoot: filepath.Join(dir, "leds")}
	if fan != "" {
		if err := os.WriteFile(roots.FanPath, []byte(fan), 0o644); err != nil {
			t.Fatalf("write fake fan: %v", err)
		}
	}
	for name, body := range leds {
		ledDir := filepath.Join(roots.LEDsRoot, name)
		if err := os.MkdirAll(ledDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		// Every attribute the reader looks for is created with the contents of
		// the map, so a fixture can leave max_brightness out on purpose.
		for attr, value := range splitLED(body) {
			if err := os.WriteFile(filepath.Join(ledDir, attr), []byte(value), attrPerm(attr)); err != nil {
				t.Fatalf("write %s/%s: %v", name, attr, err)
			}
		}
	}
	h := NewHardware(roots)
	// The CPU temperature is not what these tests are about; keep it out so
	// assertions do not depend on the machine running them.
	h.readTemp = func() (float64, bool) { return 0, false }
	return h
}

// splitLED parses the "attr=value;attr=value" fixture form.
func splitLED(body string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(body, ";") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = value
	}
	return out
}

// attrPerm makes brightness owner-writable and everything else read-only, which
// mirrors what thinkpad_acpi leaves behind.
func attrPerm(attr string) os.FileMode {
	if attr == "brightness" {
		return 0o644
	}
	return 0o444
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

const fullFan = "status:\tenabled\nspeed:\t4432\nlevel:\tauto\n"

func TestReadParsesEveryFanField(t *testing.T) {
	state := newTestHardware(t, fullFan, nil).Read()
	if !state.HaveFan {
		t.Fatal("HaveFan = false, want true")
	}
	if state.FanStatus != "enabled" {
		t.Errorf("FanStatus = %q, want enabled", state.FanStatus)
	}
	if !state.FanValid || state.FanRPM != 4432 {
		t.Errorf("fan = %d (valid %v), want 4432 valid", state.FanRPM, state.FanValid)
	}
	if !state.FanLevelValid || state.FanLevel != FanLevelAuto {
		t.Errorf("level = %q (valid %v), want auto valid", state.FanLevel, state.FanLevelValid)
	}
	if len(state.Notes) != 0 {
		t.Errorf("Notes = %v, want none", state.Notes)
	}
	if state.Any() != true {
		t.Error("Any() = false with a fan present")
	}
}

func TestDefaultHardwareMapsMetricsSnapshots(t *testing.T) {
	h := NewHardware(DefaultRoots())
	h.readFanSnapshot = func() (metrics.FanSnapshot, error) {
		return metrics.FanSnapshot{
			Present: true, Status: "enabled", SpeedRPM: 3200, SpeedValid: true,
			Level: "4", LevelValid: true,
		}, nil
	}
	h.readLEDSnapshot = func() (metrics.LEDSnapshot, error) {
		return metrics.LEDSnapshot{LEDs: []metrics.LED{
			{Name: LEDKeyboardBacklight, Brightness: 2, BrightnessValid: true,
				MaxBrightness: 3, HWChanged: 7, HWChangedValid: true, Writable: true},
			{Name: LEDLidLogoDot, Brightness: 255, BrightnessValid: true,
				MaxBrightness: 255, Writable: true},
		}}, nil
	}
	h.mu.Lock()
	h.writable[DefaultRoots().FanPath] = true
	h.mu.Unlock()
	h.readTemp = func() (float64, bool) { return 0, false }

	state := h.Read()
	if !state.HaveFan || !state.FanValid || state.FanRPM != 3200 ||
		!state.FanLevelValid || state.FanLevel != "4" || !state.FanWritable {
		t.Errorf("fan state = %+v", state)
	}
	if state.Keyboard.Name != LEDKeyboardBacklight || !state.Keyboard.Present ||
		state.Keyboard.Brightness != 2 || state.Keyboard.MaxBrightness != 3 ||
		!state.Keyboard.Writable || !state.Keyboard.HWChangedValid || state.Keyboard.HWChanged != 7 {
		t.Errorf("keyboard state = %+v", state.Keyboard)
	}
	if state.Lid.Name != LEDLidLogoDot || !state.Lid.Present ||
		state.Lid.Brightness != 255 || state.Lid.MaxBrightness != 255 || !state.Lid.Writable {
		t.Errorf("lid state = %+v", state.Lid)
	}
}

func TestParkedFanIsAValidZeroSpeed(t *testing.T) {
	state := newTestHardware(t, "status:\tenabled\nspeed:\t0\nlevel:\t3\n", nil).Read()
	if !state.FanValid {
		t.Fatal("a stopped fan reported an invalid speed")
	}
	if state.FanRPM != 0 {
		t.Errorf("FanRPM = %d, want 0", state.FanRPM)
	}
	if state.FanLevel != "3" {
		t.Errorf("FanLevel = %q, want 3", state.FanLevel)
	}
}

func TestUnparsableFanSpeedKeepsTheOtherFields(t *testing.T) {
	state := newTestHardware(t, "status:\tenabled\nspeed:\tunknown\nlevel:\t2\n", nil).Read()
	if !state.HaveFan || state.FanStatus != "enabled" {
		t.Error("a bad speed line cost us the rest of the file")
	}
	if state.FanValid {
		t.Error("FanValid = true for an unparsable speed")
	}
	if !containsNote(state.Notes, "fan speed is unreadable") {
		t.Errorf("Notes = %v, want the unreadable-speed note", state.Notes)
	}
}

func TestUnsupportedReportedFanLevelIsNotedNotTrusted(t *testing.T) {
	state := newTestHardware(t, "status:\tenabled\nspeed:\t100\nlevel:\t99\n", nil).Read()
	if state.FanLevelValid {
		t.Error("FanLevelValid = true for level 99")
	}
	if !containsNote(state.Notes, "unsupported fan level reported: 99") {
		t.Errorf("Notes = %v, want the unsupported-level note", state.Notes)
	}
}

func TestMissingFanPathIsNotAnError(t *testing.T) {
	state := newTestHardware(t, "", nil).Read()
	if state.HaveFan {
		t.Error("HaveFan = true with no fan file at all")
	}
	if state.Any() {
		t.Error("Any() = true on a machine with no ThinkPad hardware")
	}
	if state.NeedsSetup() {
		t.Error("NeedsSetup() = true when there is simply no hardware to set up")
	}
}

func TestNeedsSetupFindsAnyPresentControlThatCannotChange(t *testing.T) {
	ready := State{
		HaveFan: true, FanLevelValid: true, FanWritable: true,
		Keyboard: LEDState{Present: true, Writable: true},
		Lid:      LEDState{Present: true, Writable: true},
	}
	if ready.NeedsSetup() || !ready.Support().Writable {
		t.Fatalf("fully writable state = %+v, want no setup", ready.Support())
	}

	for name, state := range map[string]State{
		"fan control disabled": func() State { s := ready; s.FanLevelValid = false; return s }(),
		"fan is read-only":     func() State { s := ready; s.FanWritable = false; return s }(),
		"keyboard is read-only": func() State {
			s := ready
			s.Keyboard.Writable = false
			return s
		}(),
		"lid is read-only": func() State { s := ready; s.Lid.Writable = false; return s }(),
	} {
		t.Run(name, func(t *testing.T) {
			if !state.NeedsSetup() || state.Support().Writable {
				t.Fatalf("partially writable state = %+v, want setup required", state.Support())
			}
		})
	}
}

// A fan file with no level line is the signature of thinkpad_acpi loaded
// without fan_control=1: reads work, writes are refused. That is worth saying
// out loud, because otherwise the panel just looks unresponsive.
func TestMissingLevelLineOnAWritableFanExplainsFanControl(t *testing.T) {
	state := newTestHardware(t, "status:\tenabled\nspeed:\t3100\n", nil).Read()
	if !state.HaveFan || !state.FanValid {
		t.Fatal("the readable fields should still come through")
	}
	if state.FanLevelValid {
		t.Fatal("FanLevelValid = true with no level line")
	}
	if !containsNote(state.Notes, "fan_control=1") {
		t.Errorf("Notes = %v, want the fan_control hint", state.Notes)
	}
}

func TestValidFanLevelWhitelist(t *testing.T) {
	for _, level := range []string{"auto", "disengaged", "0", "1", "2", "3", "4", "5", "6", "7"} {
		if !ValidFanLevel(level) {
			t.Errorf("ValidFanLevel(%q) = false, want true", level)
		}
	}
	// Anything the kernel would not accept has to be refused here rather than
	// written into the proc file.
	for _, level := range []string{"", "8", "-1", "AUTO", "auto\n", " auto", "auto ", "max", "1 2"} {
		if ValidFanLevel(level) {
			t.Errorf("ValidFanLevel(%q) = true, want false", level)
		}
	}
}

func TestFanLevelsAreTheTenTheKernelAccepts(t *testing.T) {
	levels := FanLevels()
	if len(levels) != 10 {
		t.Fatalf("len(FanLevels()) = %d, want 10", len(levels))
	}
	if levels[0] != FanLevelAuto || levels[1] != FanLevelDisengaged {
		t.Errorf("FanLevels() starts %v, want auto then disengaged", levels[:2])
	}
	for i, want := range levels {
		if !ValidFanLevel(want) {
			t.Errorf("FanLevels()[%d] = %q is not itself valid", i, want)
		}
	}
}

func TestFanLevelNumberAndLabel(t *testing.T) {
	if got := FanLevelNumber("5"); got != 5 {
		t.Errorf("FanLevelNumber(\"5\") = %d, want 5", got)
	}
	if got := FanLevelNumber(FanLevelAuto); got != -1 {
		t.Errorf("FanLevelNumber(auto) = %d, want -1", got)
	}
	if got := FanLevelLabel(FanLevelAuto); got != "Auto" {
		t.Errorf("FanLevelLabel(auto) = %q, want Auto", got)
	}
	if got := FanLevelLabel(FanLevelDisengaged); got != "Full" {
		t.Errorf("FanLevelLabel(disengaged) = %q, want Full", got)
	}
}

func TestSetFanLevelWritesTheExpectedBytes(t *testing.T) {
	h := newTestHardware(t, fullFan, nil)
	if err := h.SetFanLevel("3"); err != nil {
		t.Fatalf("SetFanLevel: %v", err)
	}
	// The fixture is a regular file and the write is deliberately not
	// truncating, so the command has to appear at the front. A real proc
	// attribute handles the write itself.
	if got := readFile(t, h.Roots().FanPath); !strings.HasPrefix(got, "level 3\n") {
		t.Errorf("fan file = %q, want it to start with %q", got, "level 3\n")
	}
}

func TestSetFanLevelRefusesValuesOutsideTheWhitelist(t *testing.T) {
	h := newTestHardware(t, fullFan, nil)
	before := readFile(t, h.Roots().FanPath)
	for _, level := range []string{"", "8", "-1", "AUTO", "auto; rm -rf /"} {
		if err := h.SetFanLevel(level); err == nil {
			t.Errorf("SetFanLevel(%q) = nil, want an error", level)
		}
	}
	if got := readFile(t, h.Roots().FanPath); got != before {
		t.Errorf("a rejected write reached the fan file: %q", got)
	}
}

func TestReadLEDReportsBrightnessMaximumAndWriteAccess(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{
		LEDKeyboardBacklight: "brightness=2;max_brightness=3",
	})
	state := h.Read()
	led := state.Keyboard
	if !led.Present {
		t.Fatal("Present = false for an LED that exists on disk")
	}
	if led.Name != LEDKeyboardBacklight {
		t.Errorf("Name = %q, want %q", led.Name, LEDKeyboardBacklight)
	}
	if led.Brightness != 2 || led.MaxBrightness != 3 {
		t.Errorf("brightness %d of %d, want 2 of 3", led.Brightness, led.MaxBrightness)
	}
	if !led.Writable {
		t.Error("Writable = false for an owner-writable brightness attribute")
	}
	if led.HWChangedValid {
		t.Error("HWChangedValid = true without a brightness_hw_changed file")
	}
	if !led.On() {
		t.Error("On() = false at brightness 2")
	}
	if led.Level() < 0.66 || led.Level() > 0.67 {
		t.Errorf("Level() = %v, want about 0.67", led.Level())
	}
	if !state.Support().Backlight || !state.Support().Writable {
		t.Errorf("Support() = %+v, want Backlight and Writable", state.Support())
	}
	if state.NeedsSetup() {
		t.Error("NeedsSetup() = true with write access already in place")
	}
}

func TestOlderThinkLightAndLidStripAreFoundAsFallbacks(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{
		LEDThinkLight:   "brightness=1;max_brightness=1",
		LEDLidLogoStrip: "brightness=0;max_brightness=1",
	})
	state := h.Read()
	if state.Keyboard.Name != LEDThinkLight {
		t.Errorf("keyboard LED = %q, want %q", state.Keyboard.Name, LEDThinkLight)
	}
	if state.Lid.Name != LEDLidLogoStrip {
		t.Errorf("lid LED = %q, want %q", state.Lid.Name, LEDLidLogoStrip)
	}
	if state.Lid.On() {
		t.Error("lid On() = true at brightness 0")
	}
}

func TestReadOnlyLEDIsPresentButNotWritable(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{LEDKeyboardBacklight: "brightness=1;max_brightness=3"})
	if err := os.Chmod(filepath.Join(h.Roots().LEDsRoot, LEDKeyboardBacklight, "brightness"), 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	state := h.Read()
	if !state.Keyboard.Present {
		t.Fatal("Present = false; reads do not need write access")
	}
	if state.Keyboard.Writable {
		t.Error("Writable = true for a 0444 attribute")
	}
	if state.Keyboard.Note == "" {
		t.Error("Note is empty; the panel needs to say why writes fail")
	}
	if !state.NeedsSetup() {
		t.Error("NeedsSetup() = false with hardware present but not writable")
	}
}

func TestForgetWriteProbesSeesAFixAppliedAfterStartup(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{LEDKeyboardBacklight: "brightness=1;max_brightness=3"})
	path := filepath.Join(h.Roots().LEDsRoot, LEDKeyboardBacklight, "brightness")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if h.Read().Keyboard.Writable {
		t.Fatal("Writable = true before the fix")
	}
	// The udev rule lands after the plugin already started.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	if h.Read().Keyboard.Writable {
		t.Error("the probe was cached; Recheck has to be able to clear it")
	}
	h.ForgetWriteProbes()
	if !h.Read().Keyboard.Writable {
		t.Error("Writable = false after ForgetWriteProbes and the fix")
	}
}

func TestLEDWithoutABrightnessFileIsSkippedNotFatal(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{LEDKeyboardBacklight: "max_brightness=3"})
	state := h.Read()
	if state.Keyboard.Present {
		t.Error("Present = true without a brightness attribute to read")
	}
	if state.Lid.Present {
		t.Error("the lid LED should be absent, not inherited from the keyboard")
	}
}

func TestSetKeyboardClampsToWhatTheLightAdvertises(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{LEDKeyboardBacklight: "brightness=1;max_brightness=2"})
	if err := h.SetKeyboard(9); err != nil {
		t.Fatalf("SetKeyboard: %v", err)
	}
	path := filepath.Join(h.Roots().LEDsRoot, LEDKeyboardBacklight, "brightness")
	if got := readFile(t, path); got != "2" {
		t.Errorf("brightness = %q, want the clamped 2", got)
	}
	if err := h.SetKeyboard(-4); err != nil {
		t.Fatalf("SetKeyboard: %v", err)
	}
	if got := readFile(t, path); got != "0" {
		t.Errorf("brightness = %q, want the clamped 0", got)
	}
}

func TestSetKeyboardWithoutHardwareSaysSo(t *testing.T) {
	h := newTestHardware(t, "", nil)
	if err := h.SetKeyboard(1); err != ErrNoBacklight {
		t.Errorf("SetKeyboard on a machine with no backlight = %v, want ErrNoBacklight", err)
	}
	if err := h.SetLid(true); err != ErrNoLidLight {
		t.Errorf("SetLid on a machine with no lid light = %v, want ErrNoLidLight", err)
	}
}

func TestSetLidUsesTheAdvertisedMaximumToLightIt(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{LEDLidLogoDot: "brightness=0;max_brightness=1"})
	path := filepath.Join(h.Roots().LEDsRoot, LEDLidLogoDot, "brightness")
	if err := h.SetLid(true); err != nil {
		t.Fatalf("SetLid(true): %v", err)
	}
	if got := readFile(t, path); got != "1" {
		t.Errorf("brightness = %q, want 1", got)
	}
	if err := h.SetLid(false); err != nil {
		t.Fatalf("SetLid(false): %v", err)
	}
	if got := readFile(t, path); got != "0" {
		t.Errorf("brightness = %q, want 0", got)
	}
}

// Some models expose the dot without a readable maximum; any non-zero value
// lights it, so 255 is the safe fallback.
func TestSetLidFallsBackTo255WithoutAReadableMaximum(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{LEDLidLogoDot: "brightness=0"})
	if err := h.SetLid(true); err != nil {
		t.Fatalf("SetLid: %v", err)
	}
	path := filepath.Join(h.Roots().LEDsRoot, LEDLidLogoDot, "brightness")
	if got := readFile(t, path); got != "255" {
		t.Errorf("brightness = %q, want the 255 fallback", got)
	}
}

func TestHardwareChangedIsSurfacedForExternallyPressedKeys(t *testing.T) {
	h := newTestHardware(t, "", map[string]string{
		LEDKeyboardBacklight: "brightness=1;max_brightness=3;brightness_hw_changed=2",
	})
	state := h.Read()
	if !state.Keyboard.HWChangedValid || state.Keyboard.HWChanged != 2 {
		t.Fatalf("HWChanged = %d (valid %v), want 2 valid",
			state.Keyboard.HWChanged, state.Keyboard.HWChangedValid)
	}
	// The controller compares against its own previous read, which is a full
	// sample in both directions.
	before := LEDState{Brightness: 1, HWChanged: 1, HWChangedValid: true}
	if !state.Keyboard.ChangedOnHardware(before) {
		t.Error("ChangedOnHardware = false for a 1 -> 2 move made on the keyboard")
	}
	same := LEDState{Brightness: 2, HWChanged: 2, HWChangedValid: true}
	if state.Keyboard.ChangedOnHardware(same) {
		t.Error("ChangedOnHardware = true for an unchanged brightness")
	}
}

func TestClampLevelTreatsAMissingMaximumAsASingleStep(t *testing.T) {
	for _, tc := range []struct{ level, highest, want int }{
		{0, 0, 0},
		{3, 0, 1},
		{5, 3, 3},
		{-2, 3, 0},
		{2, 8, 2},
	} {
		if got := clampLevel(tc.level, tc.highest); got != tc.want {
			t.Errorf("clampLevel(%d, %d) = %d, want %d", tc.level, tc.highest, got, tc.want)
		}
	}
}

func TestReadLEDIsIndependentPerFeature(t *testing.T) {
	h := newTestHardware(t, fullFan, map[string]string{LEDLidLogoDot: "brightness=1;max_brightness=1"})
	state := h.Read()
	if state.Keyboard.Present {
		t.Error("a keyboard LED appeared without one on disk")
	}
	if !state.Lid.Present {
		t.Error("the lid LED went missing")
	}
	support := state.Support()
	if support.Fan != true || support.Backlight != false || support.Lid != true {
		t.Errorf("Support() = %+v, want Fan and Lid only", support)
	}
}

func containsNote(notes []string, want string) bool {
	for _, note := range notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}
