package thinkpad

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// controllerRig wires a controller to a fake sysfs tree, a real store file and
// a notification sink, so the whole apply/persist loop can be driven.
type controllerRig struct {
	t     *testing.T
	hw    *Hardware
	store *Store
	notes []Notify
}

func newRig(t *testing.T, fan string, leds map[string]string, temp float64, hasTemp bool) *controllerRig {
	t.Helper()
	hw := newTestHardware(t, fan, leds)
	hw.readTemp = func() (float64, bool) { return temp, hasTemp }
	return &controllerRig{t: t, hw: hw, store: OpenStore(tempStorePath(t))}
}

func (r *controllerRig) controller() *Controller {
	return NewController(r.hw, r.store, func(n Notify) { r.notes = append(r.notes, n) })
}

func (r *controllerRig) led(name string) string {
	return readFile(r.t, r.hw.Roots().LEDsRoot+"/"+name+"/brightness")
}

func (r *controllerRig) fanFile() string { return readFile(r.t, r.hw.Roots().FanPath) }

func (r *controllerRig) notified(substr string) bool {
	for _, n := range r.notes {
		if strings.Contains(n.Summary, substr) || strings.Contains(n.Body, substr) {
			return true
		}
	}
	return false
}

func (r *controllerRig) brightnessPath(name string) string {
	return r.hw.Roots().LEDsRoot + "/" + name + "/brightness"
}

func chmodReadOnly(t *testing.T, path string) error {
	t.Helper()
	return os.Chmod(path, 0o444)
}

func chmodWritable(t *testing.T, path string) error {
	t.Helper()
	return os.Chmod(path, 0o644)
}

func fullLEDs() map[string]string {
	return map[string]string{
		LEDKeyboardBacklight: "brightness=1;max_brightness=3",
		LEDLidLogoDot:        "brightness=0;max_brightness=1",
	}
}

// The headline behaviour: the profile the host remembers is what the machine
// looks like after a boot, so nobody has to re-tune it by hand.
func TestStartUpRestoresTheConfiguredProfile(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	// Leave the store on "bright" so a restart has something to restore.
	if err := rig.store.Remember("bright"); err != nil {
		t.Fatal(err)
	}
	c := rig.controller()

	cfg := DefaultConfig()
	cfg.ActiveProfile = "bright"
	cfg.ApplyOnStart = true
	c.Reconfigure(cfg)

	if !strings.HasPrefix(rig.fanFile(), "level disengaged") {
		t.Errorf("fan file = %q, want the bright profile's full-speed level", rig.fanFile())
	}
	if got := rig.led(LEDKeyboardBacklight); got != "2" {
		t.Errorf("keyboard = %q, want the bright profile's level 2", got)
	}
	if got := rig.led(LEDLidLogoDot); got != "1" {
		t.Errorf("lid = %q, want the bright profile's lid lit", got)
	}
	if c.ActiveName() != "bright" {
		t.Errorf("ActiveName() = %q, want bright", c.ActiveName())
	}
	if c.Snapshot().Busy {
		t.Error("Busy is stuck after the start-up apply")
	}
}

func TestApplyOnStartOffLeavesTheHardwareAlone(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()

	cfg := DefaultConfig()
	cfg.ActiveProfile = "bright"
	cfg.ApplyOnStart = false
	c.Reconfigure(cfg)

	if !strings.HasPrefix(rig.fanFile(), "status:\tenabled") {
		t.Errorf("fan file = %q, want the stock reading untouched", rig.fanFile())
	}
	// It still knows which profile is meant to be in effect.
	if c.ActiveName() != "bright" {
		t.Errorf("ActiveName() = %q, want bright even with apply off", c.ActiveName())
	}
	if rig.store.LastUsed() != "bright" {
		t.Errorf("LastUsed = %q, want bright recorded", rig.store.LastUsed())
	}
}

func TestChangeAfterApplyOnStartOffPreservesLiveLightSettings(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	cfg := DefaultConfig()
	cfg.ActiveProfile = "bright"
	cfg.ApplyOnStart = false
	c.Reconfigure(cfg)

	before := c.Snapshot().State
	if err := c.SetFanLevel("5"); err != nil {
		t.Fatalf("SetFanLevel: %v", err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != strconv.Itoa(before.Keyboard.Brightness) {
		t.Errorf("keyboard after fan change = %q, want live brightness %d", got, before.Keyboard.Brightness)
	}
	wantLid := "0"
	if before.Lid.On() {
		wantLid = "1"
	}
	if got := rig.led(LEDLidLogoDot); got != wantLid {
		t.Errorf("lid after fan change = %q, want live state %q", got, wantLid)
	}
}

func TestKeyboardChangeWorksAndPersistsWhenFanIsReadOnly(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	if err := chmodReadOnly(t, rig.hw.Roots().FanPath); err != nil {
		t.Fatal(err)
	}
	rig.hw.ForgetWriteProbes()

	cfg := DefaultConfig()
	cfg.ApplyOnStart = false
	c := rig.controller()
	c.Reconfigure(cfg)

	if err := c.SetKeyboard(true, 3); err != nil {
		t.Fatalf("SetKeyboard with unrelated read-only fan: %v", err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "3" {
		t.Errorf("keyboard brightness = %q, want 3", got)
	}
	profile, ok := rig.store.Profile(c.ActiveName())
	if !ok {
		t.Fatalf("active profile %q is missing", c.ActiveName())
	}
	if !profile.Settings.KeyboardOn || profile.Settings.KeyboardLevel != 3 {
		t.Errorf("saved keyboard settings = %+v, want on at level 3", profile.Settings)
	}
}

func TestApplyOnStartOffRetainsRememberedKeyboardLevelWhenHardwareIsOff(t *testing.T) {
	rig := newRig(t, fullFan, map[string]string{
		LEDKeyboardBacklight: "brightness=0;max_brightness=3",
		LEDLidLogoDot:        "brightness=0;max_brightness=1",
	}, 0, false)
	c := rig.controller()
	cfg := DefaultConfig()
	cfg.ActiveProfile = "bright"
	cfg.ApplyOnStart = false
	c.Reconfigure(cfg)

	if err := c.SetFanLevel("5"); err != nil {
		t.Fatalf("SetFanLevel: %v", err)
	}
	profile, ok := rig.store.Profile("bright")
	if !ok {
		t.Fatal("active bright profile is missing")
	}
	if profile.Settings.KeyboardOn || profile.Settings.KeyboardLevel != 2 {
		t.Errorf("saved keyboard settings = %+v, want off with remembered level 2", profile.Settings)
	}
}

func TestStartUpFallsBackWhenTheHostNamesADeletedProfile(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	if err := rig.store.Remember("night"); err != nil {
		t.Fatal(err)
	}
	c := rig.controller()

	cfg := DefaultConfig()
	cfg.ActiveProfile = "a-profile-the-user-deleted"
	c.Reconfigure(cfg)

	// "night" has the backlight off, so a lit light means it did not get there.
	if got := rig.led(LEDKeyboardBacklight); got != "0" {
		t.Errorf("keyboard = %q, want the fallback profile's backlight off", got)
	}
	if c.ActiveName() != "night" {
		t.Errorf("ActiveName() = %q, want the fallback to night", c.ActiveName())
	}
}

// A direct toggle has to outlive the process, even though the host will not
// accept settings written by a plugin. The profile is the persistence layer.
func TestDirectTogglesSurviveARestart(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)

	first := rig.controller()
	first.Reconfigure(DefaultConfig())
	if err := first.SetLid(true); err != nil {
		t.Fatalf("SetLid: %v", err)
	}
	if err := first.SetKeyboardStep("3"); err != nil {
		t.Fatalf("SetKeyboardStep: %v", err)
	}
	if err := first.SetFanLevel("5"); err != nil {
		t.Fatalf("SetFanLevel: %v", err)
	}

	// A restart: fresh controller, same store, default settings (so the active
	// profile is whatever the last run folded the live values into).
	rig.notes = nil
	second := rig.controller()
	second.Reconfigure(DefaultConfig())

	if got := rig.led(LEDLidLogoDot); got != "1" {
		t.Errorf("lid = %q after restart, want 1", got)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "3" {
		t.Errorf("keyboard = %q after restart, want 3", got)
	}
	if !strings.HasPrefix(rig.fanFile(), "level 5") {
		t.Errorf("fan file = %q after restart, want the pinned level 5", rig.fanFile())
	}

	// And the profile on disk now describes the machine as it is.
	p, ok := rig.store.Profile("default")
	if !ok {
		t.Fatal("the active profile is missing from the store")
	}
	want := Settings{FanLevel: "5", KeyboardOn: true, KeyboardLevel: 3, LidOn: true}
	if !p.Settings.Equal(want) {
		t.Errorf("stored profile = %+v, want %+v", p.Settings, want)
	}
}

// The host pushes settings on every change; replaying the profile each time
// would fight the user mid-session.
func TestOnlyTheFirstReconfigureReplaysTheProfile(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())
	if err := c.SetKeyboardStep("3"); err != nil {
		t.Fatal(err)
	}
	before := rig.led(LEDKeyboardBacklight)

	cfg := DefaultConfig()
	cfg.ActiveProfile = "night" // would turn the backlight off
	cfg.ApplyOnStart = true
	c.Reconfigure(cfg)

	if got := rig.led(LEDKeyboardBacklight); got != before {
		t.Errorf("keyboard = %q, want the live choice %q left alone", got, before)
	}
}

func TestDefaultStartupReplaysTheLastProfileAppliedInThePanel(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	cfg := DefaultConfig()
	first := rig.controller()
	first.Reconfigure(cfg)
	if err := first.ApplyProfile("bright"); err != nil {
		t.Fatalf("ApplyProfile(bright): %v", err)
	}

	// A reboot resets the hardware, while the plugin's profile store survives.
	if err := os.WriteFile(rig.hw.Roots().FanPath, []byte(fullFan), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		LEDKeyboardBacklight: "1\n",
		LEDLidLogoDot:        "0\n",
	} {
		if err := os.WriteFile(rig.brightnessPath(name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	second := rig.controller()
	second.Reconfigure(cfg)
	if second.ActiveName() != "bright" {
		t.Errorf("active profile after restart = %q, want bright", second.ActiveName())
	}
	if !strings.HasPrefix(rig.fanFile(), "level disengaged") {
		t.Errorf("fan after restart = %q, want bright profile's full-speed level", rig.fanFile())
	}
	if got := strings.TrimSpace(rig.led(LEDKeyboardBacklight)); got != "2" {
		t.Errorf("keyboard after restart = %q, want bright profile's level 2", got)
	}
	if got := strings.TrimSpace(rig.led(LEDLidLogoDot)); got != "1" {
		t.Errorf("lid after restart = %q, want the bright profile's light on", got)
	}
}

func TestTogglesFlipAndPersist(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())

	if got := rig.led(LEDKeyboardBacklight); got != "1" {
		t.Fatalf("keyboard = %q, want the default profile's level 1", got)
	}
	if err := c.ToggleKeyboard(); err != nil {
		t.Fatalf("ToggleKeyboard: %v", err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "0" {
		t.Errorf("keyboard = %q after one toggle, want 0", got)
	}
	if err := c.ToggleKeyboard(); err != nil {
		t.Fatalf("ToggleKeyboard back: %v", err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "1" {
		t.Errorf("keyboard = %q after two toggles, want 1", got)
	}
	if snap := c.Snapshot(); snap.Want.KeyboardOn != true || snap.Want.KeyboardLevel != 1 {
		t.Errorf("Want = %+v, want back to on at level 1", snap.Want)
	}

	if err := c.ToggleLid(); err != nil {
		t.Fatalf("ToggleLid: %v", err)
	}
	if got := rig.led(LEDLidLogoDot); got != "1" {
		t.Errorf("lid = %q, want lit", got)
	}
	if err := c.ToggleLid(); err != nil {
		t.Fatalf("ToggleLid off: %v", err)
	}
	if got := rig.led(LEDLidLogoDot); got != "0" {
		t.Errorf("lid = %q, want off", got)
	}
}

func TestSetKeyboardStepAcceptsOffAndDigitsOnly(t *testing.T) {
	rig := newRig(t, "", fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())

	for _, tc := range []struct{ step, want string }{
		{"off", "0"},
		{"0", "0"},
		{"2", "2"},
		{" 3 ", "3"},
	} {
		if err := c.SetKeyboardStep(tc.step); err != nil {
			t.Fatalf("SetKeyboardStep(%q): %v", tc.step, err)
		}
		if got := rig.led(LEDKeyboardBacklight); got != tc.want {
			t.Errorf("SetKeyboardStep(%q) wrote %q, want %q", tc.step, got, tc.want)
		}
	}
	for _, step := range []string{"", "abc", "-1", "999", "2; rm -rf /", "1.5"} {
		if err := c.SetKeyboardStep(step); err == nil {
			t.Errorf("SetKeyboardStep(%q) = nil, want an error", step)
		}
	}
	// Clamped to what the light actually offers.
	if err := c.SetKeyboardStep("7"); err != nil {
		t.Fatal(err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "3" {
		t.Errorf("keyboard = %q, want it clamped to the advertised 3", got)
	}
}

func TestSetFanLevelRefusesAnythingOffTheWhitelist(t *testing.T) {
	rig := newRig(t, fullFan, nil, 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())

	before := rig.fanFile()
	for _, level := range []string{"8", "-1", "auto;", "rm -rf /", "0 1"} {
		if err := c.SetFanLevel(level); err == nil {
			t.Errorf("SetFanLevel(%q) = nil, want an error", level)
		}
	}
	if got := rig.fanFile(); got != before {
		t.Errorf("a rejected fan level reached the file: %q", got)
	}
	// Case and padding are user input, not an attack surface.
	if err := c.SetFanLevel("  AUTO  "); err != nil {
		t.Errorf("SetFanLevel with padding: %v", err)
	}
}

func TestSaveProfileCreatesAndMakesItActive(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())

	if err := c.SetKeyboardStep("3"); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveProfile("afternoon"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	p, ok := rig.store.Profile("afternoon")
	if !ok {
		t.Fatal("the new profile is not in the store")
	}
	if p.Settings.KeyboardLevel != 3 {
		t.Errorf("saved KeyboardLevel = %d, want the live 3", p.Settings.KeyboardLevel)
	}
	if c.ActiveName() != "afternoon" {
		t.Errorf("ActiveName() = %q, want afternoon", c.ActiveName())
	}

	// Applying it again is a no-op on the hardware but must move the pointer.
	if err := c.ApplyProfile("default"); err != nil {
		t.Fatal(err)
	}
	if c.ActiveName() != "default" {
		t.Errorf("ActiveName() = %q, want default", c.ActiveName())
	}
	if err := c.ApplyProfile("nope"); err == nil {
		t.Error("ApplyProfile of an unknown name = nil, want an error")
	}
}

// An empty name is not an error: it means "save over the active profile",
// which is what the panel's Save button does when the field is left blank.
func TestSaveProfileRejectsUnusableNames(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())
	for _, name := range []string{strings.Repeat("n", 41), "x\x00", "x\x07"} {
		if err := c.SaveProfile(name); err == nil {
			t.Errorf("SaveProfile(%q) = nil, want an error", name)
		}
	}
	if err := c.SaveProfile(""); err != nil {
		t.Errorf("SaveProfile with a blank name should fall back to the active profile: %v", err)
	}
	if n := len(rig.store.Profiles()); n != 3 {
		t.Errorf("len(Profiles()) = %d, want no new profiles", n)
	}
}

func TestRenameAndDeleteThroughTheController(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())

	if err := c.RenameProfile("night", "dark"); err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}
	if _, ok := rig.store.Profile("dark"); !ok {
		t.Error("the renamed profile is not in the store")
	}
	if err := c.DeleteProfile("bright"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, ok := rig.store.Profile("bright"); ok {
		t.Error("the deleted profile is still in the store")
	}
	if err := c.DeleteProfile("default"); err != nil {
		t.Fatal(err)
	}
	// Never leave the store empty.
	if err := c.DeleteProfile("dark"); err == nil {
		t.Error("deleting the last profile = nil, want an error")
	}
	if n := len(rig.store.Profiles()); n != 1 {
		t.Errorf("len(Profiles()) = %d, want the last profile kept", n)
	}
}

func TestDeletingActiveProfileDoesNotRecreateItAfterAControlChange(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())
	if c.ActiveName() != "default" {
		t.Fatalf("ActiveName() = %q, want default", c.ActiveName())
	}

	if err := c.DeleteProfile("default"); err != nil {
		t.Fatalf("DeleteProfile(default): %v", err)
	}
	if c.ActiveName() != "" {
		t.Fatalf("ActiveName() after deleting the active profile = %q, want empty", c.ActiveName())
	}
	if err := c.ToggleLid(); err != nil {
		t.Fatalf("ToggleLid: %v", err)
	}
	if _, ok := rig.store.Profile("default"); ok {
		t.Fatal("the next control change recreated the deleted active profile")
	}
}

func TestAWritableRefusalTellsTheUserHowToFixIt(t *testing.T) {
	rig := newRig(t, "", fullLEDs(), 0, false)
	// Simulate the udev rule not being in place yet.
	if err := chmodReadOnly(t, rig.brightnessPath(LEDKeyboardBacklight)); err != nil {
		t.Fatal(err)
	}
	rig.hw.ForgetWriteProbes()

	cfg := DefaultConfig()
	cfg.NotifyOnWriteError = true
	c := rig.controller()
	c.Reconfigure(cfg)

	if !rig.notified("setup steps") {
		t.Errorf("notes = %+v, want the write refusal to explain the fix", rig.notes)
	}
	if !rig.notified("Re-check") {
		t.Error("the refusal should point at the Re-check button")
	}

	// Fixing it and pressing Re-check must clear the state without a restart.
	if err := chmodWritable(t, rig.brightnessPath(LEDKeyboardBacklight)); err != nil {
		t.Fatal(err)
	}
	c.Recheck()
	if snap := c.Snapshot(); snap.State.Keyboard.Writable != true {
		t.Error("Recheck did not pick up the new permissions")
	}
}

func TestNotificationsCanBeSilenced(t *testing.T) {
	rig := newRig(t, "", fullLEDs(), 0, false)
	if err := chmodReadOnly(t, rig.brightnessPath(LEDKeyboardBacklight)); err != nil {
		t.Fatal(err)
	}
	rig.hw.ForgetWriteProbes()

	cfg := DefaultConfig()
	cfg.NotifyOnWriteError = false
	c := rig.controller()
	c.Reconfigure(cfg)
	if len(rig.notes) != 0 {
		t.Errorf("notes = %+v, want silence when notifications are off", rig.notes)
	}
}

func TestRunningHotWarnsOnceAndMentionsThePinnedLevel(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 92, true)
	c := rig.controller()
	cfg := DefaultConfig()
	cfg.WarnThermal = true
	cfg.ThermalLimitC = 85
	c.Reconfigure(cfg)

	if !c.Snapshot().State.Hot(85) {
		t.Fatalf("Hot(85) = false at %.0f°C", c.Snapshot().State.CPUTempC)
	}
	// A low pinned level is the case worth spelling out.
	if err := c.SetFanLevel("1"); err != nil {
		t.Fatal(err)
	}
	c.Refresh()
	if !rig.notified("running hot") {
		t.Errorf("notes = %+v, want a thermal warning", rig.notes)
	}
	if !rig.notified("pinned to level 1") {
		t.Error("the warning should name the pinned fan level")
	}

	// It is a warning, not a lock: the level still went through.
	if !strings.HasPrefix(rig.fanFile(), "level 1") {
		t.Errorf("fan file = %q, want the level applied despite the heat", rig.fanFile())
	}

	// Once per hot episode, not once per poll.
	before := len(rig.notes)
	for i := 0; i < 5; i++ {
		c.Refresh()
	}
	if len(rig.notes) != before {
		t.Errorf("thermal warning repeated %d times", len(rig.notes)-before)
	}

	// Cooling down rearms it.
	rig.hw.readTemp = func() (float64, bool) { return 40, true }
	c.Refresh()
	before = len(rig.notes)
	if err := c.SetFanLevel("7"); err != nil {
		t.Fatal(err)
	}
	rig.hw.readTemp = func() (float64, bool) { return 92, true }
	c.Refresh()
	if len(rig.notes) == before {
		t.Error("the warning never rearmed after the machine cooled down")
	}
}

func TestThermalWarningIsOffWhenTheUserTurnsItOff(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 99, true)
	c := rig.controller()
	cfg := DefaultConfig()
	cfg.WarnThermal = false
	c.Reconfigure(cfg)
	c.Refresh()
	if rig.notified("running hot") {
		t.Errorf("notes = %+v, want no warning with warn_thermal off", rig.notes)
	}
}

// The kernel counts presses of the brightness key. When that counter moves,
// the UI should say the user did it rather than looking stuck.
func TestHardwareKeypressesAreReportedAsExternal(t *testing.T) {
	leds := map[string]string{
		LEDKeyboardBacklight: "brightness=1;max_brightness=3;brightness_hw_changed=1",
	}
	rig := newRig(t, "", leds, 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())
	c.Refresh()
	if c.Snapshot().External {
		t.Error("External = true before anything moved")
	}

	writeFixture(t, rig.hw.Roots().LEDsRoot+"/"+LEDKeyboardBacklight+"/brightness_hw_changed", "2")
	c.Refresh()
	if !c.Snapshot().External {
		t.Error("External = false after the hardware counter moved")
	}

	// One report per move, not one per poll.
	c.Refresh()
	c.Refresh()
	if n := countNotes(rig.notes); n != 0 {
		// A refresh alone does not notify; only the panel mentions it.
		t.Errorf("Refresh raised %d notifications, want 0", n)
	}
}

func TestExternalLightChangesSurviveAnUnrelatedControlAction(t *testing.T) {
	leds := map[string]string{
		LEDKeyboardBacklight: "brightness=1;max_brightness=3;brightness_hw_changed=1",
		LEDLidLogoDot:        "brightness=0;max_brightness=1;brightness_hw_changed=1",
	}
	rig := newRig(t, fullFan, leds, 0, false)
	c := rig.controller()
	cfg := DefaultConfig()
	cfg.ActiveProfile = "default"
	cfg.ApplyOnStart = false
	c.Reconfigure(cfg)

	writeFixture(t, rig.brightnessPath(LEDKeyboardBacklight), "2")
	writeFixture(t, rig.brightnessPath(LEDKeyboardBacklight)+"_hw_changed", "2")
	writeFixture(t, rig.brightnessPath(LEDLidLogoDot), "1")
	writeFixture(t, rig.brightnessPath(LEDLidLogoDot)+"_hw_changed", "2")
	c.Refresh()
	if !c.Snapshot().External {
		t.Fatal("External = false after both hardware light counters moved")
	}

	if err := c.SetFanLevel("5"); err != nil {
		t.Fatalf("SetFanLevel: %v", err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "2" {
		t.Errorf("keyboard after fan change = %q, want external key setting 2", got)
	}
	if got := rig.led(LEDLidLogoDot); got != "1" {
		t.Errorf("lid after fan change = %q, want external key setting 1", got)
	}
}

func TestAbsentHardwareNeverRaisesWriteErrors(t *testing.T) {
	rig := newRig(t, "", nil, 0, false)
	c := rig.controller()
	cfg := DefaultConfig()
	cfg.NotifyOnWriteError = true
	c.Reconfigure(cfg)

	// Every control is a no-op on a machine that has none of them.
	if err := c.SetFanLevel("4"); err != nil {
		t.Errorf("SetFanLevel: %v", err)
	}
	if err := c.SetKeyboard(true, 2); err != nil {
		t.Errorf("SetKeyboard: %v", err)
	}
	if err := c.SetLid(true); err != nil {
		t.Errorf("SetLid: %v", err)
	}
	if err := c.ToggleKeyboard(); err != nil {
		t.Errorf("ToggleKeyboard: %v", err)
	}
	if err := c.ToggleLid(); err != nil {
		t.Errorf("ToggleLid: %v", err)
	}
	for _, n := range rig.notes {
		if n.Critical {
			t.Errorf("unexpected critical notice on absent hardware: %+v", n)
		}
	}
	if snap := c.Snapshot(); snap.State.Any() {
		t.Error("Any() = true with no hardware")
	}
}

func TestAPartialMachineOnlyDrivesWhatItHas(t *testing.T) {
	// A fan but no lid light at all.
	rig := newRig(t, fullFan, map[string]string{LEDKeyboardBacklight: "brightness=1;max_brightness=3"}, 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())
	if err := c.SetLid(true); err != nil {
		t.Fatalf("SetLid on a machine with no lid light: %v", err)
	}
	if got := rig.led(LEDKeyboardBacklight); got != "1" {
		t.Errorf("keyboard = %q, want the other controls unaffected", got)
	}
	for _, n := range rig.notes {
		if n.Critical {
			t.Errorf("critical notice for a light the machine does not have: %+v", n)
		}
	}
}

func TestSnapshotIsSafeToReadWhileApplying(t *testing.T) {
	rig := newRig(t, fullFan, fullLEDs(), 0, false)
	c := rig.controller()
	c.Reconfigure(DefaultConfig())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = c.Snapshot()
			c.Refresh()
		}
	}()
	for i := 0; i < 25; i++ {
		if err := c.SetKeyboardStep("2"); err != nil {
			t.Fatalf("SetKeyboardStep: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	<-done

	// Race detector territory; this only has to not deadlock or panic.
	if c.Snapshot().Busy {
		t.Error("Busy is stuck")
	}
}

func countNotes(notes []Notify) int { return len(notes) }

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	// The fixture attributes are created read-only, so reopen before writing.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o444); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
