package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/thinkpad"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// TestSetupScriptIsValidShell parses the setup script the panel hands to the
// clipboard. Nothing on a development machine can execute it end to end, so a
// syntax error here would only surface on a user's laptop, at the exact moment
// they need the fix to work.
func TestSetupScriptIsValidShell(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		shell, err = exec.LookPath("sh")
		if err != nil {
			t.Skip("no bash or sh available to parse the script")
		}
	}
	out, err := exec.Command(shell, "-n", "-c", setupScript).CombinedOutput()
	if err != nil {
		t.Fatalf("%s -n setupScript: %v\n%s", shell, err, out)
	}
}

// TestSetupScriptCoversEveryRequirement guards the one thing a rewrite can
// quietly lose. Each fragment is a step the panel promises; dropping one leaves
// a user with a plugin that reads hardware it cannot change.
func TestSetupScriptCoversEveryRequirement(t *testing.T) {
	for _, want := range []string{
		"fan_control=1",            // the level control is refused without it
		"/etc/modprobe.d/",         // persists across boots
		"groupadd",                 // a group the user can belong to
		"usermod",                  // membership actually granted
		"sysc-thinkpad",            // the name the udev rules hand nodes to
		"/etc/udev/rules.d/",       // the driver resets modes on every load
		"udevadm control --reload", // or the rules are never read
		"systemctl --user exit",    // refreshes a lingering user manager
		"only programs launched from that terminal", // newgrp does not refresh systemd services
		"Re-check", // how they confirm it worked
	} {
		if !strings.Contains(setupScript, want) {
			t.Errorf("setupScript no longer mentions %q", want)
		}
	}
	if strings.Contains(setupScript, "or: newgrp sysc-thinkpad") {
		t.Error("newgrp must not be presented as refreshing a lingering sysc-shell service")
	}
}

// TestFanUdevRuleRunsWhenTheModuleAppears pins the two facts that are easy to get wrong
// and impossible to test here: /proc/acpi/ibm/fan emits no uevent, so a rule
// matched on its path or on a "ibm_acpi" subsystem would never fire; and %k
// inside the run line has to be the LED kernel name or the glob is useless.
func TestFanUdevRuleRunsWhenTheModuleAppears(t *testing.T) {
	if !strings.Contains(setupScript, `KERNEL=="thinkpad_acpi"`) ||
		!strings.Contains(setupScript, `ACTION=="add"`) {
		t.Error("the fan permission rule must run when thinkpad_acpi is added")
	}
	if strings.Contains(setupScript, `ACTION=="bind"`) {
		t.Error("module bind events do not restore the fan permissions")
	}
	if strings.Contains(setupScript, `SUBSYSTEM=="ibm_acpi"`) {
		t.Error("no udev event carries an ibm_acpi subsystem; this rule cannot fire")
	}
	if !strings.Contains(setupScript, "/sys/class/leds/%k/brightness") {
		t.Error("the LED rule lost its %k substitution")
	}
}

func TestSetupScriptDoesNotTreatBuiltinDriverAsReloadableModule(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		shell, err = exec.LookPath("sh")
		if err != nil {
			t.Skip("no bash or sh available to execute the setup script")
		}
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	logDir := filepath.Join(root, "log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStub := func(name, script string) {
		t.Helper()
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeStub("modinfo", "#!/bin/sh\nprintf '(builtin)\\n'\n")
	writeStub("cat", "#!/bin/sh\nprintf 'N\\n'\n")
	writeStub("sudo", `#!/bin/sh
printf '%s\n' "$*" >> "$THINKPAD_TEST_LOG/sudo-calls"
case "$1:$2" in
  tee:/etc/modprobe.d/99-sysc-thinkpad.conf)
    while IFS= read -r line; do printf '%s\n' "$line"; done > "$THINKPAD_TEST_LOG/modprobe.conf";;
  tee:/etc/udev/rules.d/99-sysc-thinkpad.rules)
    while IFS= read -r line; do printf '%s\n' "$line"; done > "$THINKPAD_TEST_LOG/udev.rules";;
esac
`)

	env := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "PATH=") || strings.HasPrefix(value, "THINKPAD_TEST_LOG=") {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "PATH="+bin+":"+os.Getenv("PATH"), "THINKPAD_TEST_LOG="+logDir)
	cmd := exec.Command(shell, "-c", setupScript)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("setupScript: %v\n%s", err, output)
	}

	calls, err := os.ReadFile(filepath.Join(logDir, "sudo-calls"))
	if err != nil {
		t.Fatalf("read sudo calls: %v", err)
	}
	if strings.Contains(string(calls), "modprobe -r thinkpad_acpi") ||
		strings.Contains(string(calls), "modprobe thinkpad_acpi") {
		t.Fatalf("built-in driver was reloaded: %s", calls)
	}
	if _, err := os.Stat(filepath.Join(logDir, "modprobe.conf")); err == nil {
		t.Fatal("built-in driver received a modprobe.d setting that it cannot read")
	}
	if !strings.Contains(string(output), "thinkpad_acpi.fan_control=1") ||
		!strings.Contains(strings.ToLower(string(output)), "kernel command line") {
		t.Fatalf("built-in driver output = %q, want the persistent kernel command-line setting", output)
	}
}

func TestNegotiateUsesTheHighestSharedProtocolMinor(t *testing.T) {
	if got := negotiate([]v1.Version{
		{Major: 1, Minor: 17}, {Major: 1, Minor: 12}, {Major: 2, Minor: 90},
	}); got != v1.ProtocolMinor {
		t.Errorf("highest shared minor = %d, want plugin protocol minor %d", got, v1.ProtocolMinor)
	}
	if got := negotiate([]v1.Version{{Major: 1, Minor: 12}, {Major: 2, Minor: 90}}); got != 12 {
		t.Errorf("older host minor = %d, want 12", got)
	}
}

// TestSetupScriptDoesNotBlindlyReloadTheDriver pins a fix for a bug found on
// real hardware. thinkpad_acpi is built into many kernels, and modprobe has no
// module file to load or unload for those: `modprobe -r` prints
// "Module thinkpad_acpi not found" and fails, which left the setup script
// reporting a FATAL error on a machine whose fan control already worked.
// Reloading a live driver is also disruptive when it does exist, so the script
// must ask first and skip the whole step when fan_control is already on.
func TestSetupScriptDoesNotBlindlyReloadTheDriver(t *testing.T) {
	if !strings.Contains(setupScript, "modinfo -F filename thinkpad_acpi") {
		t.Error("the reload must be guarded by a built-in-versus-modular check")
	}
	if !strings.Contains(setupScript, `/sys/module/thinkpad_acpi/parameters/fan_control`) {
		t.Error("the script must read the live fan_control value before deciding to reload")
	}

	reload := strings.Index(setupScript, "modprobe -r thinkpad_acpi")
	builtIn := strings.Index(setupScript, "modinfo -F filename thinkpad_acpi")
	already := strings.Index(setupScript, `= "Y"`)
	if reload < 0 {
		t.Fatal("the reload path disappeared entirely")
	}
	if builtIn < 0 || already < 0 {
		t.Fatal("the reload is unguarded: it can fire before the built-in check")
	}
	if already > reload {
		t.Error("the already-enabled check must come before the reload")
	}
	if builtIn > reload {
		t.Error("the built-in check must come before the reload, or it reloads regardless")
	}
	if !strings.Contains(setupScript, "kernel command line") ||
		!strings.Contains(setupScript, "thinkpad_acpi.fan_control=1") {
		t.Error("a built-in driver needs the fan_control kernel command-line setting")
	}
}

func TestBarOpenAcceptsLeftAndRightClicks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		event  v1.EventKind
		button v1.PointerButton
	}{
		{name: "left click", event: v1.EventActivate},
		{name: "right click", event: v1.EventPointer, button: v1.ButtonSecondary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			toPlugin, hostToPlugin := io.Pipe()
			fromPlugin, hostFromPlugin := io.Pipe()
			defer toPlugin.Close()
			defer hostToPlugin.Close()
			defer fromPlugin.Close()
			defer hostFromPlugin.Close()
			client := v1.NewClient(toPlugin, hostFromPlugin)
			controller := thinkpad.NewController(
				thinkpad.NewHardware(thinkpad.Roots{}),
				thinkpad.OpenStore(filepath.Join(t.TempDir(), "profiles.json")), nil,
			)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			selected, pending := "", ""
			views := map[string]view{"bar-view": {entry: "bar", instance: "bar-placement", rev: 1}}
			done := make(chan struct{})
			go func() {
				handleInput(ctx, client, controller, &v1.InputEvent{
					ViewID: "bar-view", Revision: 1, Node: "open", Event: tc.event, Button: tc.button,
					Output: "DP-1",
				}, views, &selected, &pending)
				close(done)
			}()
			calls := make(chan v1.HostCall, 1)
			go func() {
				var call v1.HostCall
				if err := json.NewDecoder(fromPlugin).Decode(&call); err == nil {
					calls <- call
				}
			}()
			select {
			case call := <-calls:
				if call.Call != v1.CallPanelOpen {
					t.Errorf("bar click called %q, want panel.open", call.Call)
				}
				var params v1.PanelParams
				if err := json.Unmarshal(call.Params, &params); err != nil {
					t.Fatal(err)
				}
				if params.Entry != "panel" || params.Output != "DP-1" || params.Instance != "bar-placement" {
					t.Errorf("panel.open params = %+v", params)
				}
				cancel()
			case <-done:
				t.Fatal("bar click did not request the panel")
			case <-time.After(time.Second):
				t.Fatal("bar click did not reach the panel handler")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("panel.open did not return after cancellation")
			}
		})
	}
}

func TestKeyboardToggleInputTurnsBacklightOn(t *testing.T) {
	ledPath := filepath.Join(t.TempDir(), "leds", thinkpad.LEDKeyboardBacklight)
	if err := os.MkdirAll(ledPath, 0o755); err != nil {
		t.Fatal(err)
	}
	brightness := filepath.Join(ledPath, "brightness")
	if err := os.WriteFile(brightness, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledPath, "max_brightness"), []byte("3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	controller := thinkpad.NewController(
		thinkpad.NewHardware(thinkpad.Roots{LEDsRoot: filepath.Dir(ledPath)}),
		thinkpad.OpenStore(filepath.Join(t.TempDir(), "profiles.json")), nil,
	)
	controller.Refresh()

	selected, pending := "", ""
	handleInput(context.Background(), nil, controller, &v1.InputEvent{
		ViewID: "panel", Revision: 1, Node: "kbd:toggle", Event: v1.EventActivate,
	}, map[string]view{"panel": {entry: "panel", rev: 1}}, &selected, &pending)

	got, err := os.ReadFile(brightness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "1" {
		t.Fatalf("keyboard brightness after toggle = %q, want 1", strings.TrimSpace(string(got)))
	}
}

func TestInputFromStaleOrClosedViewCannotChangeFan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision uint64
		views    map[string]view
	}{
		{name: "stale revision", revision: 1, views: map[string]view{"panel": {entry: "panel", rev: 2}}},
		{name: "closed view", revision: 1, views: map[string]view{}},
		{name: "unpublished view", revision: 0, views: map[string]view{"panel": {entry: "panel"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fanPath := filepath.Join(t.TempDir(), "fan")
			if err := os.WriteFile(fanPath, []byte("status: enabled\nspeed: 3666\nlevel: auto\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			controller := thinkpad.NewController(
				thinkpad.NewHardware(thinkpad.Roots{FanPath: fanPath}),
				thinkpad.OpenStore(filepath.Join(t.TempDir(), "profiles.json")), nil,
			)
			controller.Refresh()
			before, err := os.ReadFile(fanPath)
			if err != nil {
				t.Fatal(err)
			}

			selected, pending := "", ""
			handleInput(context.Background(), nil, controller, &v1.InputEvent{
				ViewID: "panel", Revision: tc.revision, Node: "fan:5", Event: v1.EventActivate,
			}, tc.views, &selected, &pending)

			after, err := os.ReadFile(fanPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("invalid view input changed hardware from %q to %q", before, after)
			}
		})
	}
}

func TestSliderInputWritesOnlyFiniteIntegerLevelsInRange(t *testing.T) {
	root := t.TempDir()
	fanPath := filepath.Join(root, "fan")
	if err := os.WriteFile(fanPath, []byte("status: enabled\nspeed: 3666\nlevel: auto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ledPath := filepath.Join(root, "leds", thinkpad.LEDKeyboardBacklight)
	if err := os.MkdirAll(ledPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledPath, "brightness"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledPath, "max_brightness"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	controller := thinkpad.NewController(
		thinkpad.NewHardware(thinkpad.Roots{FanPath: fanPath, LEDsRoot: filepath.Join(root, "leds")}),
		thinkpad.OpenStore(filepath.Join(root, "profiles.json")), nil,
	)
	controller.Refresh()

	selected, pending := "", ""
	views := map[string]view{"panel": {entry: "panel", rev: 1}}
	send := func(node string, value *float64) {
		handleInput(context.Background(), nil, controller, &v1.InputEvent{
			ViewID: "panel", Revision: 1, Node: node, Event: v1.EventChange, Value: value,
		}, views, &selected, &pending)
	}
	value := 5.0
	send("fan-slider", &value)
	fanAfterValid, err := os.ReadFile(fanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fanAfterValid), "level 5") {
		t.Fatalf("fan after level 5 = %q", fanAfterValid)
	}

	invalid := []*float64{nil}
	for _, n := range []float64{-1, 8, 1.5, math.NaN(), math.Inf(1)} {
		n := n
		invalid = append(invalid, &n)
	}
	for _, bad := range invalid {
		send("fan-slider", bad)
		fan, err := os.ReadFile(fanPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(fan) != string(fanAfterValid) {
			t.Errorf("invalid fan value %v changed hardware: %q", bad, fan)
		}
	}

	keyboard := 2.0
	send("kbd-slider", &keyboard)
	brightnessPath := filepath.Join(ledPath, "brightness")
	brightnessAfterValid, err := os.ReadFile(brightnessPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(brightnessAfterValid)) != "2" {
		t.Fatalf("keyboard after level 2 = %q", brightnessAfterValid)
	}

	for _, n := range []float64{-1, 3, 1.5, math.NaN(), math.Inf(-1)} {
		n := n
		send("kbd-slider", &n)
		brightness, err := os.ReadFile(brightnessPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(brightness) != string(brightnessAfterValid) {
			t.Errorf("invalid keyboard value %v changed hardware: %q", n, brightness)
		}
	}
}
