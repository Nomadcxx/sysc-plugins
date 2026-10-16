// Command sysc-thinkpad-probe reports what the ThinkPad plugin sees on this
// machine, as JSON.
//
// It exists for the case the plugin's own UI cannot help with: deciding whether
// the plugin can write to your fan and lights. The plugin detects write access
// by attempting to open each sysfs attribute for writing, and that answer
// depends on file ownership — so it cannot be reproduced by copying the files
// somewhere else and testing there. Run this on the machine itself:
//
//	sysc-thinkpad-probe
//
// It is read-only by default. -write-selftest additionally exercises the write
// path against real hardware and restores every value it changed; see
// writeSelfTest for what each flag is allowed to touch.
//
// Exit status is 0 whenever the plugin would render a panel at all, including a
// machine with no ThinkPad hardware. It is a diagnostic, not a gate.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/thinkpad"
)

var (
	// writeSelfTest drives each writable light to a different value, reads it
	// back, and puts it back where it found it. The lights are reversible in
	// microseconds and carry no thermal risk, so this is the default tier.
	writeSelfTest = flag.Bool("write-selftest", false,
		"exercise the write path for the lights, restoring every value changed")

	// writeFan rewrites the fan's *current* level back to itself. That is a
	// no-op for the hardware but proves the kernel's store handler accepts the
	// exact byte sequence the plugin sends, which the read path cannot show.
	writeFan = flag.Bool("write-fan", false,
		"with -write-selftest: rewrite the fan's current level back to itself")

	// writeFanChange actually moves the fan. Only ever in the safe direction —
	// to level 7, the loudest and coolest setting — and always restored, so a
	// crash between the write and the restore leaves the machine cooler, not
	// warmer. Opt-in separately because it is the only irreversible-feeling
	// thing this program can do.
	writeFanChange = flag.Bool("write-fan-change", false,
		"with -write-selftest: move the fan to level 7 and restore it")

	fanLadder = flag.Bool("fan-ladder", false,
		"measure advertised numeric fan levels, then restore the starting level")
)

const fanLadderSettle = 2 * time.Second

type ledReport struct {
	Name           string  `json:"name"`
	Present        bool    `json:"present"`
	Brightness     int     `json:"brightness"`
	MaxBrightness  int     `json:"max_brightness"`
	Writable       bool    `json:"writable"`
	HWChangedValid bool    `json:"hw_changed_valid"`
	HWChanged      int     `json:"hw_changed"`
	LevelFraction  float64 `json:"level_fraction"`
	Note           string  `json:"note,omitempty"`
}

type fanReport struct {
	Present    bool   `json:"present"`
	Status     string `json:"status"`
	RPM        int    `json:"rpm"`
	RPMValid   bool   `json:"rpm_valid"`
	Level      string `json:"level"`
	LevelValid bool   `json:"level_valid"`
	Writable   bool   `json:"writable"`
}

type report struct {
	Fan        fanReport        `json:"fan"`
	Backlight  ledReport        `json:"backlight"`
	Lid        ledReport        `json:"lid"`
	CPUTempC   float64          `json:"cpu_temp_c"`
	CPUTempOK  bool             `json:"cpu_temp_valid"`
	Support    thinkpad.Support `json:"support"`
	Any        bool             `json:"any"`
	NeedsSetup bool             `json:"needs_setup"`
	Notes      []string         `json:"notes"`
	Writes     []writeCheck     `json:"writes,omitempty"`
}

// writeCheck records one write/read-back/restore cycle. The values are strings
// because a light is a number and a fan level is a word, and a diagnostic that
// cannot represent both is a diagnostic that misreports one of them.
//
// RestoredOK is separate from OK on purpose: a pass that leaves the machine
// changed is not a pass, and the distinction is invisible if they share a
// field. Moved is separate again, because "wrote the level it already had" is a
// weaker — though still useful — claim than "changed the fan".
type writeCheck struct {
	Target     string `json:"target"`
	Skipped    string `json:"skipped,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
	ReadBack   string `json:"read_back"`
	Restored   string `json:"restored_to"`
	Moved      bool   `json:"moved"`
	OK         bool   `json:"ok"`
	RestoredOK bool   `json:"restored_ok"`
	Error      string `json:"error,omitempty"`
}

func ledToReport(led thinkpad.LEDState) ledReport {
	return ledReport{
		Name:           led.Name,
		Present:        led.Present,
		Brightness:     led.Brightness,
		MaxBrightness:  led.MaxBrightness,
		Writable:       led.Writable,
		HWChangedValid: led.HWChangedValid,
		HWChanged:      led.HWChanged,
		LevelFraction:  led.Level(),
		Note:           led.Note,
	}
}

// selftest runs the write path for every writable control and restores what it
// changed. Restoration is deferred rather than left to the end of the happy
// path: if a write is refused halfway through, the values already touched must
// still go back.
func selftest(hw *thinkpad.Hardware, before thinkpad.State) []writeCheck {
	checks := []writeCheck{
		lightWrite(hw, "keyboard", before.Keyboard, hw.SetKeyboard),
		lightWrite(hw, "lid", before.Lid, hw.SetLidBrightness),
	}
	if *writeFan || *writeFanChange {
		checks = append(checks, fanWrite(hw, before))
	}
	return checks
}

// lightWrite drives one light to a different brightness, proves the kernel
// stored it, and puts the original back.
func lightWrite(hw *thinkpad.Hardware, target string, before thinkpad.LEDState,
	set func(int) error) writeCheck {

	check := writeCheck{
		Target: target,
		From:   strconv.Itoa(before.Brightness),
		Moved:  true,
	}
	switch {
	case !before.Present:
		check.Skipped = "not present on this machine"
		return check
	case !before.Writable:
		check.Skipped = "not writable: " + before.Note
		return check
	case before.MaxBrightness < 1:
		check.Skipped = "advertises no usable brightness range"
		return check
	}

	// Off is the natural opposite of "on"; on is the natural opposite of "off".
	value := 0
	if before.Brightness == 0 {
		value = 1
	}
	check.To = strconv.Itoa(value)

	restore := func() { _ = set(before.Brightness) }
	defer restore()

	if err := set(value); err != nil {
		check.Error = err.Error()
		return check
	}
	check.ReadBack = strconv.Itoa(selftestBrightness(hw, target))
	check.OK = check.ReadBack == check.To

	// Restore here rather than leaving it to the defer, so RestoredOK reports
	// what actually happened.
	restore()
	check.Restored = strconv.Itoa(selftestBrightness(hw, target))
	check.RestoredOK = check.Restored == check.From
	if !check.RestoredOK {
		check.Error = fmt.Sprintf("did not restore %s: wanted %s, got %s",
			target, check.From, check.Restored)
	}
	return check
}

// selftestBrightness re-reads one light through the hardware layer, so
// verification uses the same read the panel would perform.
func selftestBrightness(hw *thinkpad.Hardware, target string) int {
	after := hw.Read()
	if target == "lid" {
		return after.Lid.Brightness
	}
	return after.Keyboard.Brightness
}

// fanWrite proves the fan accepts what the plugin sends. The default tier
// rewrites the current level to itself: the hardware does not move, but the
// kernel's store handler has still parsed the exact bytes, which no read can
// demonstrate. The opt-in tier moves to level 7 and restores.
func fanWrite(hw *thinkpad.Hardware, before thinkpad.State) writeCheck {
	check := writeCheck{Target: "fan"}
	switch {
	case !before.HaveFan:
		check.Skipped = "not present on this machine"
		return check
	case !before.FanWritable:
		check.Skipped = "not writable"
		return check
	case !before.FanLevelValid:
		check.Skipped = "the kernel reports no settable level (fan_control=1 not loaded?)"
		return check
	}

	original := thinkpad.NormalizeFanLevel(before.FanLevel)
	if !thinkpad.ValidFanLevel(original) {
		check.Skipped = "the kernel reported an unusable level " + before.FanLevel
		return check
	}

	check.From = original
	// The default tier writes the level the fan already has. The hardware does
	// not move, but the kernel's store handler still has to parse it.
	target := original
	if *writeFanChange {
		target = strconv.Itoa(thinkpad.MaxNumericFanLevel)
		check.Moved = true
	}
	check.To = target

	defer func() { _ = hw.SetFanLevel(original) }()

	if err := hw.SetFanLevel(target); err != nil {
		check.Error = err.Error()
		return check
	}
	check.ReadBack = hw.Read().FanLevel
	check.OK = check.ReadBack == target

	_ = hw.SetFanLevel(original)
	check.Restored = hw.Read().FanLevel
	check.RestoredOK = check.Restored == original
	if !check.RestoredOK {
		check.Error = fmt.Sprintf("did not restore the fan level: wanted %q, got %q",
			original, check.Restored)
	}
	return check
}

// advertisedFanLevels reads the numeric levels from thinkpad_acpi's own help
// line. Symbolic modes are intentionally excluded: this is a speed curve, not
// a test of Auto or Full mode.
func advertisedFanLevels(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read advertised fan levels: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if !strings.HasPrefix(line, "commands:") || !strings.Contains(line, "level <level>") {
			continue
		}
		_, choices, ok := strings.Cut(line, " is ")
		if !ok {
			return nil, errors.New("fan help line does not list supported levels")
		}
		choices = strings.TrimRight(strings.TrimSpace(choices), ").")
		levels := make([]string, 0, thinkpad.MaxNumericFanLevel+1)
		seen := map[string]bool{}
		for _, choice := range strings.Split(choices, ",") {
			choice = strings.TrimSpace(choice)
			if low, high, rangeValue := strings.Cut(choice, "-"); rangeValue {
				first, firstErr := strconv.Atoi(strings.TrimSpace(low))
				last, lastErr := strconv.Atoi(strings.TrimSpace(high))
				if firstErr != nil || lastErr != nil {
					continue // e.g. the advertised symbolic level "full-speed".
				}
				if first < 0 || last < first || last > thinkpad.MaxNumericFanLevel {
					return nil, fmt.Errorf("kernel advertises unsupported fan range %q", choice)
				}
				for level := first; level <= last; level++ {
					value := strconv.Itoa(level)
					if !seen[value] {
						levels = append(levels, value)
						seen[value] = true
					}
				}
				continue
			}
			level, parseErr := strconv.Atoi(choice)
			if parseErr != nil {
				continue // Auto, disengaged and full-speed are not ladder steps.
			}
			if !thinkpad.ValidFanLevel(choice) || level < 0 {
				return nil, fmt.Errorf("kernel advertises unsupported fan level %q", choice)
			}
			if !seen[choice] {
				levels = append(levels, choice)
				seen[choice] = true
			}
		}
		if len(levels) != 0 {
			return levels, nil
		}
		return nil, errors.New("kernel did not advertise numeric fan levels")
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read advertised fan levels: %w", err)
	}
	return nil, errors.New("kernel did not advertise numeric fan levels")
}

// walkFanLevels measures each advertised numeric step and restores the level
// it found before starting, even when a write fails or the signal context ends.
func walkFanLevels(
	ctx context.Context,
	out io.Writer,
	levels []string,
	read func() thinkpad.State,
	set func(string) error,
	wait func(context.Context, time.Duration) error,
) (err error) {
	before := read()
	switch {
	case !before.HaveFan:
		return errors.New("no ThinkPad fan interface is available")
	case !before.FanWritable:
		return errors.New("fan is not writable; check the ThinkPad setup instructions")
	case !before.FanLevelValid || !thinkpad.ValidFanLevel(before.FanLevel):
		return errors.New("the fan has no readable level to restore")
	case len(levels) == 0:
		return errors.New("the kernel advertised no numeric fan levels")
	}
	original := thinkpad.NormalizeFanLevel(before.FanLevel)
	fmt.Fprintln(out, "level rpm")
	defer func() {
		if restoreErr := set(original); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore fan level %q: %w", original, restoreErr))
			return
		}
		if settleErr := wait(context.Background(), fanLadderSettle); settleErr != nil {
			err = errors.Join(err, fmt.Errorf("wait for fan restoration: %w", settleErr))
			return
		}
		restored := read()
		if !restored.FanLevelValid || thinkpad.NormalizeFanLevel(restored.FanLevel) != original {
			err = errors.Join(err, fmt.Errorf("fan did not return to %q", original))
			return
		}
		fmt.Fprintf(out, "restored %s\n", original)
	}()

	for _, level := range levels {
		if !thinkpad.ValidFanLevel(level) || thinkpad.FanLevelNumber(level) < 0 {
			return fmt.Errorf("unsafe numeric fan level %q", level)
		}
		if ctx.Err() != nil {
			fmt.Fprintln(out, "interrupted; restoring the starting level")
			return nil
		}
		if err := set(level); err != nil {
			return fmt.Errorf("set fan level %q: %w", level, err)
		}
		if err := wait(ctx, fanLadderSettle); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				fmt.Fprintln(out, "interrupted; restoring the starting level")
				return nil
			}
			return fmt.Errorf("wait at fan level %q: %w", level, err)
		}
		after := read()
		if !after.FanLevelValid || thinkpad.NormalizeFanLevel(after.FanLevel) != level {
			return fmt.Errorf("fan did not accept level %q", level)
		}
		if after.FanValid {
			fmt.Fprintf(out, "%s %d RPM\n", level, after.FanRPM)
		} else {
			fmt.Fprintf(out, "%s  unavailable\n", level)
		}
	}
	return nil
}

func waitForFan(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runFanLadder(hw *thinkpad.Hardware, fanPath string, out io.Writer) error {
	levels, err := advertisedFanLevels(fanPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return walkFanLevels(ctx, out, levels, hw.Read, hw.SetFanLevel, waitForFan)
}

func main() {
	flag.Parse()
	roots := thinkpad.DefaultRoots()
	hw := thinkpad.NewHardware(roots)
	if *fanLadder {
		if *writeSelfTest || *writeFan || *writeFanChange {
			fmt.Fprintln(os.Stderr, "sysc-thinkpad-probe: -fan-ladder runs on its own; omit the write-selftest flags")
			os.Exit(2)
		}
		if err := runFanLadder(hw, roots.FanPath, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "sysc-thinkpad-probe: %v\n", err)
			os.Exit(1)
		}
		return
	}
	state := hw.Read()

	out := report{
		Fan: fanReport{
			Present:    state.HaveFan,
			Status:     state.FanStatus,
			RPM:        state.FanRPM,
			RPMValid:   state.FanValid,
			Level:      state.FanLevel,
			LevelValid: state.FanLevelValid,
			Writable:   state.FanWritable,
		},
		Backlight:  ledToReport(state.Keyboard),
		Lid:        ledToReport(state.Lid),
		CPUTempC:   state.CPUTempC,
		CPUTempOK:  state.CPUTempValid,
		Support:    state.Support(),
		Any:        state.Any(),
		NeedsSetup: state.NeedsSetup(),
		Notes:      state.Notes,
	}
	if out.Notes == nil {
		out.Notes = []string{}
	}

	// Report the pre-write state, then exercise the writes against the same
	// snapshot the caller just saw, so the JSON describes the machine as it
	// was found and what the plugin did about it.
	if *writeSelfTest {
		out.Writes = selftest(hw, state)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "sysc-thinkpad-probe: %v\n", err)
		os.Exit(1)
	}
}
