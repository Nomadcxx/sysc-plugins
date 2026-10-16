package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/thinkpad"
)

func TestAdvertisedFanLevelsUsesTheKernelNumericRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fan")
	if err := os.WriteFile(path, []byte("status: enabled\ncommands: level <level> (<level> is 0-7, auto, disengaged, full-speed)\ncommands: enable, disable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := advertisedFanLevels(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0", "1", "2", "3", "4", "5", "6", "7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advertised levels = %v, want %v", got, want)
	}
}

func TestAdvertisedFanLevelsRejectsUnsupportedRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fan")
	if err := os.WriteFile(path, []byte("commands: level <level> (<level> is 0-8, auto, disengaged, full-speed)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := advertisedFanLevels(path); err == nil {
		t.Fatal("advertisedFanLevels accepted a range the hardware layer cannot safely write")
	}
}

func TestFanLadderRestoresTheStartingLevel(t *testing.T) {
	current := thinkpad.FanLevelAuto
	var writes []string
	var waits []time.Duration
	read := func() thinkpad.State {
		rpm := map[string]int{"0": 3300, "1": 3100, "2": 2900}[current]
		return thinkpad.State{
			HaveFan: true, FanWritable: true, FanLevelValid: true,
			FanLevel: current, FanValid: current != thinkpad.FanLevelAuto, FanRPM: rpm,
		}
	}
	set := func(level string) error {
		writes = append(writes, level)
		current = level
		return nil
	}
	wait := func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	var output bytes.Buffer
	err := walkFanLevels(context.Background(), &output, []string{"0", "1", "2"}, read, set, wait)
	if err != nil {
		t.Fatal(err)
	}
	if current != thinkpad.FanLevelAuto {
		t.Fatalf("fan level after ladder = %q, want restored auto", current)
	}
	if want := []string{"0", "1", "2", thinkpad.FanLevelAuto}; !reflect.DeepEqual(writes, want) {
		t.Errorf("fan writes = %v, want %v", writes, want)
	}
	if len(waits) != 4 || waits[0] != fanLadderSettle || waits[3] != fanLadderSettle {
		t.Errorf("settle waits = %v, want one %s wait per step and restore", waits, fanLadderSettle)
	}
	for _, want := range []string{"0 3300", "1 3100", "2 2900", "restored auto"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output %q does not contain %q", output.String(), want)
		}
	}
}

func TestFanLadderRestoresAfterCancellationAndWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failAt   string
		cancelAt int
	}{
		{name: "cancelled", cancelAt: 1},
		{name: "write failure", failAt: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := thinkpad.FanLevelDisengaged
			var writes []string
			read := func() thinkpad.State {
				return thinkpad.State{HaveFan: true, FanWritable: true, FanLevelValid: true, FanLevel: current}
			}
			set := func(level string) error {
				writes = append(writes, level)
				if level == tc.failAt {
					return errors.New("write refused")
				}
				current = level
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waits := 0
			wait := func(ctx context.Context, _ time.Duration) error {
				waits++
				if tc.cancelAt == waits {
					cancel()
				}
				return ctx.Err()
			}
			var output bytes.Buffer
			err := walkFanLevels(ctx, &output, []string{"0", "1", "2"}, read, set, wait)
			if tc.cancelAt > 0 {
				if err != nil {
					t.Fatalf("walkFanLevels after cancellation = %v, want a clean restore", err)
				}
				if !strings.Contains(output.String(), "interrupted") {
					t.Errorf("cancellation output = %q, want an interruption message", output.String())
				}
			} else if err == nil || !strings.Contains(err.Error(), "write refused") {
				t.Fatalf("walkFanLevels error = %v, want write refusal", err)
			}
			if current != thinkpad.FanLevelDisengaged {
				t.Errorf("fan level after %s = %q, want restored disengaged", tc.name, current)
			}
			wantWrites := []string{"0", thinkpad.FanLevelDisengaged}
			if tc.failAt != "" {
				wantWrites = []string{"0", "1", thinkpad.FanLevelDisengaged}
			}
			if !reflect.DeepEqual(writes, wantWrites) {
				t.Errorf("fan writes = %v, want %v", writes, wantWrites)
			}
		})
	}
}
