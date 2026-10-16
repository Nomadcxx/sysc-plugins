package thinkpad

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realFanFile is the verbatim contents of /proc/acpi/ibm/fan from the ThinkPad
// testbench (Arch, kernel 7.2.7-arch1-1, thinkpad_acpi with fan_control=Y).
// It is a golden fixture, not a guess: the "commands:" lines below are the
// kernel's own help text, and they are the reason this file is a fixture at all
// rather than a trimmed three-line invention.
const realFanFile = "status:\t\tenabled\n" +
	"speed:\t\t3685\n" +
	"level:\t\tauto\n" +
	"commands:\tlevel <level> (<level> is 0-7, auto, disengaged, full-speed)\n" +
	"commands:\tenable, disable\n" +
	"commands:\twatchdog <timeout> (<timeout> is 0 (off), 1-120 (seconds))\n"

// TestParseRealKernelFanFile pins the parser to what a live thinkpad_acpi
// actually emits. Earlier fixtures were hand-written and omitted the repeated
// "commands:" keys, which is exactly the shape most likely to break a
// key/value splitter.
func TestParseRealKernelFanFile(t *testing.T) {
	got := newTestHardware(t, realFanFile, nil).Read()

	if !got.HaveFan {
		t.Fatal("HaveFan = false, want true for a live fan file")
	}
	if got.FanStatus != "enabled" {
		t.Errorf("FanStatus = %q, want %q", got.FanStatus, "enabled")
	}
	if !got.FanValid || got.FanRPM != 3685 {
		t.Errorf("FanRPM = %d (valid %v), want 3685 (valid true)", got.FanRPM, got.FanValid)
	}
	if !got.FanLevelValid || got.FanLevel != FanLevelAuto {
		t.Errorf("FanLevel = %q (valid %v), want %q (valid true)",
			got.FanLevel, got.FanLevelValid, FanLevelAuto)
	}
	// The commands lines are help text, not state. If any of them leaked into
	// a note the panel would nag about a command line the kernel printed itself.
	if len(got.Notes) != 0 {
		t.Errorf("Notes = %v, want none: the commands lines are help text", got.Notes)
	}
}

// TestRealFanFileHasNoFanControlNote pins the converse. Because the testbench
// has fan_control=Y, the file carries a level line and the plugin must not
// accuse the module of lacking the parameter.
func TestRealFanFileHasNoFanControlNote(t *testing.T) {
	got := newTestHardware(t, realFanFile, nil).Read()
	if containsNote(got.Notes, "fan_control=1") {
		t.Errorf("Notes = %v; a fan_control=Y machine reported a missing level line", got.Notes)
	}
}

// TestFanDisabledIsReportedNotHidden covers the fan's own "disable" command. If
// something else switched the fan off, the panel must not imply that a chosen
// level is doing anything.
func TestFanDisabledIsReportedNotHidden(t *testing.T) {
	for _, tc := range []struct {
		status   string
		wantNote bool
	}{
		{"enabled", false},
		{"disabled", true},
	} {
		body := "status:\t\t" + tc.status + "\nspeed:\t\t3000\nlevel:\t\tauto\n"
		got := newTestHardware(t, body, nil).Read()
		note := containsNote(got.Notes, "switched off")
		if tc.wantNote && !note {
			t.Errorf("status %q: expected the disabled-fan note, got %v", tc.status, got.Notes)
		}
		if !tc.wantNote && note {
			t.Errorf("status %q: unexpected disabled-fan note in %v", tc.status, got.Notes)
		}
	}
}

// TestFullSpeedIsAcceptedButStoredCanonically covers the alias the kernel's own
// help text advertises. The EC never reports "full-speed" back — it reports
// "disengaged" — so writing the alias verbatim would make a saved profile and
// the hardware disagree on every read, forever.
func TestFullSpeedIsAcceptedButStoredCanonically(t *testing.T) {
	if !ValidFanLevel(FanLevelFullSpeed) {
		t.Error("full-speed rejected, but the kernel advertises it in its level help")
	}
	if got := NormalizeFanLevel(FanLevelFullSpeed); got != FanLevelDisengaged {
		t.Errorf("NormalizeFanLevel(full-speed) = %q, want %q", got, FanLevelDisengaged)
	}
	if FanLevelNumber(FanLevelFullSpeed) != -1 {
		t.Error("full-speed must carry no step number, like disengaged")
	}
	if got, want := FanLevelLabel(FanLevelFullSpeed), FanLevelLabel(FanLevelDisengaged); got != want {
		t.Errorf("FanLevelLabel(full-speed) = %q, want %q", got, want)
	}

	h := newTestHardware(t, realFanFile, nil)
	if err := h.SetFanLevel(FanLevelFullSpeed); err != nil {
		t.Fatalf("SetFanLevel(full-speed): %v", err)
	}
	if body := readFile(t, h.roots.FanPath); !strings.HasPrefix(body, "level disengaged\n") {
		t.Errorf("fan file = %q, want it to start with %q", body, "level disengaged\n")
	}
}

// TestFanEnableDisableAreNotLevels locks down the dangerous pair. The kernel
// accepts "enable" and "disable" on the same file as its "level" command, and
// "disable" stops the fan outright. Nothing may turn that into a UI option.
func TestFanEnableDisableAreNotLevels(t *testing.T) {
	for _, bad := range []string{"enable", "disable", "watchdog", "level", "", "on", "off"} {
		if ValidFanLevel(bad) {
			t.Errorf("ValidFanLevel(%q) = true; only auto, disengaged, full-speed and 0-7 may pass", bad)
		}
	}
	h := newTestHardware(t, realFanFile, nil)
	before := readFile(t, h.roots.FanPath)
	if err := h.SetFanLevel("disable"); err == nil {
		t.Error(`SetFanLevel("disable") succeeded; that stops the fan`)
	}
	if after := readFile(t, h.roots.FanPath); after != before {
		t.Errorf("the fan file changed to %q; no command should have reached it", after)
	}
}

// TestRealKeyboardBacklightShape pins the values the testbench's
// tpacpi::kbd_backlight actually reports: a three-step light (0, 1, 2), which
// matches /proc/acpi/ibm/kbdlight's "commands: 0, 1, 2".
func TestRealKeyboardBacklightShape(t *testing.T) {
	leds := map[string]string{
		"tpacpi::kbd_backlight": "brightness=0;max_brightness=2",
	}
	state := newTestHardware(t, "", leds).Read()

	if !state.Keyboard.Present {
		t.Fatal("kbd_backlight not present; tpacpi::kbd_backlight leads the candidate list")
	}
	if state.Keyboard.Name != "tpacpi::kbd_backlight" {
		t.Errorf("resolved %q, want tpacpi::kbd_backlight", state.Keyboard.Name)
	}
	if state.Keyboard.MaxBrightness != 2 {
		t.Errorf("MaxBrightness = %d, want 2", state.Keyboard.MaxBrightness)
	}
	if state.Keyboard.On() {
		t.Error("On() = true at brightness 0")
	}

	h := newTestHardware(t, "", leds)
	if err := h.SetKeyboard(9); err != nil {
		t.Fatalf("SetKeyboard(9): %v", err)
	}
	assertSysfsWrite(t, h.roots.LEDsRoot+"/tpacpi::kbd_backlight/brightness", "2")
}

// TestRealLidLogoDotShape pins the lid dot: 255 steps, not the 2 the backlight
// uses, and 0 when off.
func TestRealLidLogoDotShape(t *testing.T) {
	leds := map[string]string{
		"tpacpi::lid_logo_dot": "brightness=0;max_brightness=255",
	}
	state := newTestHardware(t, "", leds).Read()
	if !state.Lid.Present {
		t.Fatal("lid_logo_dot not present")
	}
	if state.Lid.MaxBrightness != 255 {
		t.Errorf("MaxBrightness = %d, want 255", state.Lid.MaxBrightness)
	}

	h := newTestHardware(t, "", leds)
	if err := h.SetLid(true); err != nil {
		t.Fatalf("SetLid(true): %v", err)
	}
	assertSysfsWrite(t, h.roots.LEDsRoot+"/tpacpi::lid_logo_dot/brightness", "255")

	if err := h.SetLid(false); err != nil {
		t.Fatalf("SetLid(false): %v", err)
	}
	assertSysfsWrite(t, h.roots.LEDsRoot+"/tpacpi::lid_logo_dot/brightness", "0")
}

// assertSysfsWrite checks the bytes the kernel would have parsed. A regular
// file cannot model sysfs, where writing replaces the whole attribute and the
// kernel then regenerates the contents on the next read. The plugin opens
// attributes without O_TRUNC on purpose — truncation is refused outright on
// sysfs — so on this fixture a shorter write leaves the tail of the old value
// behind and the file reads back as e.g. "055". Only the leading bytes are
// meaningful here, and they are exactly what the kernel's store handler sees.
func assertSysfsWrite(t *testing.T, path, want string) {
	t.Helper()
	got := readFile(t, path)
	if !strings.HasPrefix(got, want) {
		t.Errorf("%s = %q, want it to begin with %q", filepath.Base(path), got, want)
	}
}

// TestUnreadableHWChangedIsNotAProblem covers the real kernel's quirk. On the
// testbench tpacpi::kbd_backlight has a brightness_hw_changed file, but reading
// it fails with EIO until the user has pressed the brightness key once since
// boot. That must leave external-change detection dormant, not raise a note
// every ThinkPad owner would see forever.
//
// A directory standing in for the attribute reproduces "the open fails" the
// same way a 0400 file owned by root does when tests are not run as root.
func TestUnreadableHWChangedIsNotAProblem(t *testing.T) {
	leds := map[string]string{
		"tpacpi::kbd_backlight": "brightness=1;max_brightness=2;brightness_hw_changed=7",
	}
	h := newTestHardware(t, "", leds)
	counter := filepath.Join(h.roots.LEDsRoot, "tpacpi::kbd_backlight", "brightness_hw_changed")
	if err := os.Remove(counter); err != nil {
		t.Fatalf("remove counter: %v", err)
	}
	if err := os.Mkdir(counter, 0o755); err != nil {
		t.Fatalf("replace counter with a directory: %v", err)
	}

	state := h.Read()
	if !state.Keyboard.Present {
		t.Fatal("a failing brightness_hw_changed must not make the light look absent")
	}
	if state.Keyboard.HWChangedValid {
		t.Error("HWChangedValid = true from an unreadable counter")
	}
	if state.Keyboard.Note != "" {
		t.Errorf("Note = %q, want empty: the light itself is fine", state.Keyboard.Note)
	}
	if containsNote(state.Notes, "hw_changed") {
		t.Errorf("Notes = %v; an EIO counter before the first keypress is not worth saying", state.Notes)
	}
}
