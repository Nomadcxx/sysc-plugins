package thinkpad

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests talk to a real ThinkPad over ssh. They are skipped unless
// SYSC_THINKPAD_SSH is set, so `go test ./...` stays hermetic:
//
//	SYSC_THINKPAD_SSH=nomadx@192.168.0.64:7777 go test ./plugins/thinkpad/ -run Live -v
//
// The plugin's answer to "can I write to this?" comes from opening each sysfs
// attribute for writing, which depends on file *ownership*. Copying the real
// tree to a scratch directory therefore lies: the copy belongs to the test
// runner and always looks writable, which is exactly the answer being tested.
// So these tests build cmd/sysc-thinkpad-probe, copy it to the machine, and run
// it there. It only ever reads.

const sshEnv = "SYSC_THINKPAD_SSH"

type fanReport struct {
	Present    bool   `json:"present"`
	Status     string `json:"status"`
	RPM        int    `json:"rpm"`
	RPMValid   bool   `json:"rpm_valid"`
	Level      string `json:"level"`
	LevelValid bool   `json:"level_valid"`
	Writable   bool   `json:"writable"`
}

type ledReport struct {
	Name          string  `json:"name"`
	Present       bool    `json:"present"`
	Brightness    int     `json:"brightness"`
	MaxBrightness int     `json:"max_brightness"`
	Writable      bool    `json:"writable"`
	HWChangedOK   bool    `json:"hw_changed_valid"`
	Note          string  `json:"note"`
	LevelFraction float64 `json:"level_fraction"`
}

type probeReport struct {
	Fan        fanReport    `json:"fan"`
	Backlight  ledReport    `json:"backlight"`
	Lid        ledReport    `json:"lid"`
	CPUTempC   float64      `json:"cpu_temp_c"`
	CPUTempOK  bool         `json:"cpu_temp_valid"`
	Support    Support      `json:"support"`
	Any        bool         `json:"any"`
	NeedsSetup bool         `json:"needs_setup"`
	Notes      []string     `json:"notes"`
	Writes     []writeCheck `json:"writes"`
}

// writeCheck mirrors cmd/sysc-thinkpad-probe. RestoredOK is asserted
// separately because a test that only checked OK would happily leave the
// testbench's lights on.
type writeCheck struct {
	Target     string `json:"target"`
	Skipped    string `json:"skipped"`
	From       string `json:"from"`
	To         string `json:"to"`
	ReadBack   string `json:"read_back"`
	Restored   string `json:"restored_to"`
	Moved      bool   `json:"moved"`
	OK         bool   `json:"ok"`
	RestoredOK bool   `json:"restored_ok"`
	Error      string `json:"error"`
}

// sshBase parses the env var into the flag set that reaches the testbench.
func sshBase(t *testing.T) (target string, base []string) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(sshEnv))
	if raw == "" {
		t.Skipf("set %s=user@host:port to run against a real ThinkPad", sshEnv)
	}
	user, rest, ok := strings.Cut(raw, "@")
	if !ok {
		user, rest = "", raw
	}
	host, port, hasPort := strings.Cut(rest, ":")
	if !hasPort {
		port = "22"
	}
	target = user + "@" + host
	return target, []string{
		"-p", port,
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		target,
	}
}

func sshRun(t *testing.T, base []string, command string) (string, error) {
	t.Helper()
	out, err := exec.Command("ssh", append(append([]string{}, base...), command)...).Output()
	return string(out), err
}

// runProbeOnTestbench builds the probe, ships it to the machine's temp
// directory, runs it with the given flags, and parses the JSON it prints.
//
// It is deliberately one function for both the read and write tiers: the write
// flags only take effect on the remote invocation, and a separate code path is
// exactly where a test would forget to pass them.
func runProbeOnTestbench(t *testing.T, base []string, flags ...string) probeReport {
	t.Helper()

	local := filepath.Join(t.TempDir(), "sysc-thinkpad-probe")
	// Tests run with the package directory as the working directory, and the
	// probe lives two levels up in the same module.
	build := exec.Command("go", "build", "-o", local, "./cmd/sysc-thinkpad-probe")
	build.Dir = filepath.Join("..", "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build the probe: %v", err)
	}

	remote := "/tmp/sysc-thinkpad-probe-" + filepath.Base(t.TempDir())
	if _, err := sshRun(t, base, "mkdir -p "+remote); err != nil {
		t.Skipf("cannot create %s on the testbench: %v", remote, err)
	}
	if out, err := exec.Command("scp", append(
		append([]string{"-P", base[1], "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"},
			local), base[len(base)-1]+":"+remote+"/probe")...).CombinedOutput(); err != nil {
		t.Skipf("cannot scp the probe to the testbench: %v\n%s", err, out)
	}
	defer func() {
		_, _ = sshRun(t, base, "rm -rf "+remote)
	}()

	out, err := sshRun(t, base, remote+"/probe "+strings.Join(flags, " "))
	if err != nil {
		t.Skipf("cannot run the probe on the testbench: %v\n%s", err, out)
	}
	var rep probeReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("probe output was not JSON (%v):\n%s", err, out)
	}
	return rep
}

// TestLiveProbeReadsRealThinkpad runs the plugin's reader, on the machine, over
// the real sysfs tree.
func TestLiveProbeReadsRealThinkpad(t *testing.T) {
	_, base := sshBase(t)
	rep := runProbeOnTestbench(t, base)

	if !rep.Fan.Present {
		t.Skip("no /proc/acpi/ibm/fan on the testbench; it is not a ThinkPad")
	}
	if !rep.Any {
		t.Error(`"any" = false: a live ThinkPad should expose at least the fan`)
	}
	if !rep.Fan.RPMValid {
		t.Errorf("the live fan speed did not parse; notes: %v", rep.Notes)
	}
	for _, note := range rep.Notes {
		if strings.Contains(note, "unreadable") || strings.Contains(note, "not a number") {
			t.Errorf("live parse produced %q; notes: %v", note, rep.Notes)
		}
	}
	if rep.Fan.LevelValid && !ValidFanLevel(rep.Fan.Level) {
		t.Errorf("live fan level %q is not one this plugin can re-apply", rep.Fan.Level)
	}
	if rep.Fan.LevelValid && NormalizeFanLevel(rep.Fan.Level) != rep.Fan.Level {
		t.Errorf("the kernel reported the non-canonical level %q", rep.Fan.Level)
	}

	for _, led := range []struct {
		what string
		led  ledReport
		want bool
	}{
		{"backlight", rep.Backlight, true},
		{"lid", rep.Lid, true},
	} {
		if !led.led.Present {
			t.Logf("no %s LED on this machine", led.what)
			continue
		}
		if led.led.MaxBrightness < 1 {
			t.Errorf("%s (%s) advertises max_brightness %d", led.what, led.led.Name, led.led.MaxBrightness)
		}
		if led.led.LevelFraction < 0 || led.led.LevelFraction > 1 {
			t.Errorf("%s level_fraction = %v, outside 0..1", led.what, led.led.LevelFraction)
		}
		t.Logf("%s: %s brightness %d/%d writable=%v note=%q",
			led.what, led.led.Name, led.led.Brightness, led.led.MaxBrightness, led.led.Writable, led.led.Note)
	}

	t.Logf("fan: status=%q rpm=%d level=%q levelValid=%v writable=%v",
		rep.Fan.Status, rep.Fan.RPM, rep.Fan.Level, rep.Fan.LevelValid, rep.Fan.Writable)
	t.Logf("support=%+v needsSetup=%v cpuTemp=%.1f/%v", rep.Support, rep.NeedsSetup, rep.CPUTempC, rep.CPUTempOK)
	for _, note := range rep.Notes {
		t.Logf("note: %s", note)
	}
}

// TestLiveProbeAgreesWithTheNeedsSetupBranch pins the one answer the plugin's
// whole first-run experience hangs on. The read path may be entirely happy
// while nothing is writable, and the panel has to say so rather than offer
// controls that silently fail.
//
// The expectations are derived from the probe's own facts, not hardcoded,
// because a testbench that has already been set up should still pass.
func TestLiveProbeAgreesWithTheNeedsSetupBranch(t *testing.T) {
	_, base := sshBase(t)
	rep := runProbeOnTestbench(t, base)
	if !rep.Any {
		t.Skip("nothing to set up on this machine")
	}

	anyWritable := rep.Fan.Writable || rep.Backlight.Writable || rep.Lid.Writable
	if anyWritable {
		if rep.NeedsSetup {
			t.Error(`needs_setup = true although something is writable`)
		}
		if !rep.Support.Writable {
			t.Error("support.writable = false although something is writable")
		}
		t.Logf("machine is already set up (%+v): needs_setup is correctly false", rep.Support)
		return
	}

	if !rep.NeedsSetup {
		t.Errorf(`needs_setup = false on a read-only machine; support=%+v notes=%v`,
			rep.Support, rep.Notes)
	}
	// An unwritable control must come with a reason. The panel shows these
	// notes next to the control, and a bare disabled toggle teaches nothing.
	if len(rep.Notes) == 0 {
		t.Errorf("nothing is writable but there are no notes to explain why: %+v", rep.Support)
	}
	for _, led := range []struct {
		what string
		led  ledReport
	}{{"backlight", rep.Backlight}, {"lid", rep.Lid}} {
		if led.led.Present && !led.led.Writable && led.led.Note == "" {
			t.Errorf("%s (%s) is present but unwritable with no note", led.what, led.led.Name)
		}
	}
	if !rep.Fan.Writable && rep.Fan.LevelValid && len(rep.Notes) == 0 {
		t.Error("the fan is unwritable and says nothing about it")
	}
	for _, note := range rep.Notes {
		t.Logf("note: %s", note)
	}
}

// TestLiveProbeStaysConsistentWhenRunTwice guards against the probe reporting
// something different on an identical second run. Anything cached wrongly — a
// write probe result, a stale previous sample — shows up here.
func TestLiveProbeStaysConsistentWhenRunTwice(t *testing.T) {
	_, base := sshBase(t)
	first := runProbeOnTestbench(t, base)
	second := runProbeOnTestbench(t, base)

	for _, tc := range []struct {
		what        string
		a, b        any
		explanation string
	}{
		{"fan present", first.Fan.Present, second.Fan.Present, "a fan cannot appear"},
		{"fan level", first.Fan.Level, second.Fan.Level, "a stable machine keeps its level"},
		{"fan writable", first.Fan.Writable, second.Fan.Writable, "permissions do not drift"},
		{"backlight max", first.Backlight.MaxBrightness, second.Backlight.MaxBrightness, "hardware constant"},
		{"backlight writable", first.Backlight.Writable, second.Backlight.Writable, "permissions do not drift"},
		{"lid max", first.Lid.MaxBrightness, second.Lid.MaxBrightness, "hardware constant"},
		{"needs setup", first.NeedsSetup, second.NeedsSetup, "a derived answer must be stable"},
	} {
		if tc.a != tc.b {
			t.Errorf("%s changed between two identical reads: %v then %v (%s)",
				tc.what, tc.a, tc.b, tc.explanation)
		}
	}
	if first.Fan.RPMValid && second.Fan.RPMValid &&
		second.Fan.RPM-first.Fan.RPM > 4000 {
		t.Errorf("fan speed jumped from %d to %d between reads",
			first.Fan.RPM, second.Fan.RPM)
	}
}

// The write tiers are opt-in and separate from SYSC_THINKPAD_SSH, because they
// change real hardware:
//
//	SYSC_THINKPAD_SSH=nomadx@192.168.0.64:7777 SYSC_THINKPAD_SSH_WRITE=leds \
//		go test ./plugins/thinkpad/ -run LiveWrite -v
//
// "leds" touches only the two lights. "all" also rewrites the fan's current
// level — which leaves the cooling unchanged but proves the kernel accepts the
// bytes the plugin sends. Nothing here ever writes a *different* fan level,
// because that is the one change on this machine that could matter if the test
// died midway; -write-fan-change exists for a human to run on purpose.
func writeTier(t *testing.T) []string {
	t.Helper()
	switch strings.TrimSpace(os.Getenv("SYSC_THINKPAD_SSH_WRITE")) {
	case "":
		t.Skip("set SYSC_THINKPAD_SSH_WRITE=leds (or all) to let these tests write to the machine")
		return nil
	case "leds":
		return []string{"-write-selftest"}
	case "all":
		return []string{"-write-selftest", "-write-fan"}
	default:
		t.Fatalf("SYSC_THINKPAD_SSH_WRITE must be leds or all, got %q", os.Getenv("SYSC_THINKPAD_SSH_WRITE"))
		return nil
	}
}

// TestLiveWriteLightsRoundTripAndRestore is the one test that proves the plugin
// can actually *do* something on this machine. Every other live test can pass on
// a machine where all writes are refused, because reads still work.
//
// It asserts restoration independently of success on purpose. A test that only
// checked ok would leave the testbench's keyboard lit after a failure.
func TestLiveWriteLightsRoundTripAndRestore(t *testing.T) {
	_, base := sshBase(t)
	flags := writeTier(t)

	before := runProbeOnTestbench(t, base)
	if !before.Any {
		t.Skip("nothing to write to on this machine")
	}

	rep := runProbeOnTestbench(t, base, flags...)
	if len(rep.Writes) == 0 {
		t.Fatal("the write self-test reported no checks at all")
	}

	for _, check := range rep.Writes {
		if check.Skipped != "" {
			t.Logf("%s: skipped (%s)", check.Target, check.Skipped)
			continue
		}
		t.Logf("%s: %s -> %s, read back %s, restored to %s (ok=%v restored=%v moved=%v)",
			check.Target, check.From, check.To, check.ReadBack, check.Restored,
			check.OK, check.RestoredOK, check.Moved)

		if !check.OK {
			t.Errorf("%s: wrote %s, the kernel reports %s (%s)",
				check.Target, check.To, check.ReadBack, check.Error)
		}
		// Checked even when OK is false: a refused write that still moved the
		// light leaves the machine changed, and that is the worse outcome.
		if !check.RestoredOK {
			t.Errorf("%s LEFT CHANGED: wanted %s, machine reports %s (%s)",
				check.Target, check.From, check.Restored, check.Error)
		}

		// Movement is only promised for the lights, and for the fan only under
		// the change tier. The default fan tier rewrites the level the fan
		// already has on purpose, so "moved=false" there is the correct answer
		// and asking for anything else would make the test wrong.
		wantMoved := check.Target != "fan"
		if check.Target == "fan" && strings.Contains(strings.Join(flags, " "), "-write-fan-change") {
			wantMoved = true
		}
		if check.Moved != wantMoved {
			t.Errorf("%s: moved=%v but this tier promised moved=%v", check.Target, check.Moved, wantMoved)
		}
	}
}

// TestLiveWriteRestoresTheMachineIndependently re-reads the box after the write
// self-test and compares it against the read taken before. The self-test
// restores in its own process; this catches the case where it claimed to but
// did not, which on a machine nobody is sitting at would otherwise go unnoticed.
func TestLiveWriteRestoresTheMachineIndependently(t *testing.T) {
	_, base := sshBase(t)
	flags := writeTier(t)

	before := runProbeOnTestbench(t, base)
	if !before.Any {
		t.Skip("nothing to write to on this machine")
	}
	runProbeOnTestbench(t, base, flags...)
	after := runProbeOnTestbench(t, base)

	for _, tc := range []struct {
		what string
		got  int
		want int
	}{
		{"keyboard backlight", after.Backlight.Brightness, before.Backlight.Brightness},
		{"lid light", after.Lid.Brightness, before.Lid.Brightness},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %d after the self-test but was %d before it", tc.what, tc.got, tc.want)
		}
	}
	if before.Fan.LevelValid && after.Fan.LevelValid && after.Fan.Level != before.Fan.Level {
		t.Errorf("fan level is %q after the self-test but was %q before it",
			after.Fan.Level, before.Fan.Level)
	}
}
