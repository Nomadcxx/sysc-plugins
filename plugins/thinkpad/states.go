package thinkpad

// ExampleStates returns one Snapshot per branch the panel can take.
//
// It is the single source of truth for the states the view has to survive: the
// view tests lint every one of them, and cmd/sysc-thinkpad-screenshot paints
// each of them to a PNG so the layout can be looked at rather than only
// measured. The shell reports geometry violations as diagnosable errors, and a
// plugin that only renders correctly on a fully-equipped ThinkPad is useless
// everywhere else.
//
// The "full" entry is the realistic T14 in a healthy, fully writable state, and
// its fan and backlight numbers are taken from the real machine.
func ExampleStates() map[string]Snapshot {
	full := Snapshot{
		State: State{
			HaveFan: true, FanStatus: "enabled", FanRPM: 4432, FanValid: true,
			FanLevel: FanLevelAuto, FanLevelValid: true, FanWritable: true,
			Keyboard: LEDState{Name: LEDKeyboardBacklight, Present: true, Brightness: 2,
				MaxBrightness: 3, Writable: true},
			Lid:      LEDState{Name: LEDLidLogoDot, Present: true, Brightness: 255, MaxBrightness: 255, Writable: true},
			CPUTempC: 48, CPUTempValid: true,
		},
		Active: "default",
		Want:   Settings{FanLevel: FanLevelAuto, KeyboardOn: true, KeyboardLevel: 2},
		Profiles: []Profile{
			{Name: "default", Settings: Settings{FanLevel: FanLevelAuto, KeyboardOn: true, KeyboardLevel: 1}},
			{Name: "night", Settings: Settings{FanLevel: FanLevelAuto}},
			{Name: "bright", Settings: Settings{FanLevel: FanLevelDisengaged, KeyboardOn: true, KeyboardLevel: 2, LidOn: true}},
		},
	}

	absent := Snapshot{State: State{}, Active: "default",
		Profiles: []Profile{{Name: "default", Settings: Settings{FanLevel: FanLevelAuto}}}}

	readOnly := full
	readOnly.State.FanWritable = false
	readOnly.State.Keyboard.Writable = false
	readOnly.State.Lid.Writable = false
	readOnly.State.FanLevelValid = false

	hot := full
	hot.State.CPUTempC = 93

	maxed := full
	maxed.State.Keyboard = LEDState{Name: LEDKeyboardBacklight, Present: true,
		Brightness: 3, MaxBrightness: 3, Writable: true}

	noLid := full
	noLid.State.Lid = LEDState{}

	brokenStore := full
	brokenStore.StoreIssue = "thinkpad.json is not valid JSON"
	brokenStore.Profiles = nil

	many := full
	many.Profiles = nil
	for i := 0; i < 9; i++ {
		many.Profiles = append(many.Profiles, Profile{
			Name:     "profile-" + string(rune('a'+i)),
			Settings: Settings{FanLevel: FanLevelAuto},
		})
	}

	return map[string]Snapshot{
		"full": full, "absent": absent, "readonly": readOnly, "hot": hot,
		"maxed": maxed, "noled": noLid, "brokenstore": brokenStore, "many": many,
	}
}
