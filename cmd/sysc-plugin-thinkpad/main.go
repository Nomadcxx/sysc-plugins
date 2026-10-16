// Command sysc-plugin-thinkpad is the ThinkPad plugin process: it speaks the
// plugin/v1 wire protocol, owns the fan and LED writes, keeps the profile store,
// and publishes the bar, tooltip and panel views.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/thinkpad"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in, out *os.File) error {
	return runWith(in, out, thinkpad.DefaultRoots(), thinkpad.DefaultStorePath())
}

// runWith keeps the process loop testable against a throwaway sysfs tree and
// profile store without ever writing the developer's ThinkPad.
func runWith(in, out *os.File, roots thinkpad.Roots, storePath string) error {
	c := v1.NewClient(in, out)
	hello, err := c.Handshake(identity.FromManifest(v1.Identity{
		ID: "org.sysc.thinkpad", Name: "ThinkPad", Version: "0.1.0",
	}))
	if err != nil {
		return err
	}
	minor := negotiate(hello.Supported)

	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)

	// OpenStore never fails: a missing file is seeded and an unreadable one is
	// reported through LoadIssue so the panel can say so instead of dying.
	store := thinkpad.OpenStore(storePath)
	if issue := store.LoadIssue(); issue != "" {
		fmt.Fprintf(os.Stderr, "thinkpad: profile store problem: %s\n", issue)
	}

	controller := thinkpad.NewController(thinkpad.NewHardware(roots), store,
		func(n thinkpad.Notify) {
			urgency := v1.UrgencyNormal
			if n.Critical {
				urgency = v1.UrgencyCritical
			}
			_, _ = c.Call(context.Background(), v1.CallNotify, v1.NotifyParams{
				Summary: n.Summary, Body: n.Body, Urgency: urgency, TimeoutMS: 20000,
			})
		})

	views := map[string]view{}
	ctx, cancel := context.WithCancel(context.Background())
	incoming := make(chan v1.Message, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-readerDone
	}()

	var (
		settings    = newSettingsState(minor)
		selected    string
		pendingName string
	)

	publish := func() {
		if controller == nil {
			return
		}
		snap := controller.Snapshot()
		cfg := settings.config
		logo := thinkpad.LogoPath(cfg, exeDir)
		for id, v := range views {
			var root *v1.Node
			switch v.kind {
			case v1.ViewBar:
				root = thinkpad.BarTree(snap, settings.instance(v.instance), cfg, v.width)
			case v1.ViewTooltip:
				// The shell owns this view; a text-only tree is all it paints.
				root = thinkpad.TooltipTree(snap, cfg)
			default:
				if v.entry == "settings" {
					root = thinkpad.SettingsPanelTree()
					break
				}
				wantHeight := thinkpad.PanelPreferredHeight(snap, selected)
				if v.height > 0 && v.width >= 64 && wantHeight != v.resizeTarget {
					callCtx, stop := context.WithTimeout(ctx, 2*time.Second)
					reply, err := c.Call(callCtx, v1.CallPanelResize,
						v1.PanelResizeParams{Width: v.width, Height: wantHeight})
					stop()
					if err == nil && reply.OK {
						// Legacy hosts answer with null, so only trust a
						// concrete height and let the panes scroll otherwise.
						var size struct {
							Width  int `json:"width"`
							Height int `json:"height"`
						}
						if json.Unmarshal(reply.Result, &size) == nil && size.Height >= 64 {
							v.height = size.Height
						}
						v.resizeTarget = wantHeight
					}
				}
				if v.height <= 0 {
					v.height = wantHeight
				}
				root = thinkpad.PanelTree(snap, settings.instance(v.instance), cfg, logo, selected)
			}
			v.rev++
			views[id] = v
			_ = c.Snapshot(id, v.rev, root)
		}
	}

	poll := time.NewTimer(5 * time.Second)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				// Read the machine for the first snapshot without warming the
				// controller: the host can send the saved settings after ViewOpen,
				// and those settings choose which profile to restore.
				if controller != nil {
					controller.Refresh()
				}
				views[m.ViewID] = view{
					kind: m.View, entry: m.Entry, instance: m.Instance,
					width: m.Width, height: m.Height, resizeTarget: m.Height,
				}
				publish()
			case *v1.ViewClose:
				closed := views[m.ViewID]
				delete(views, m.ViewID)
				if closed.instance != "" {
					stillOpen := false
					for _, v := range views {
						if v.instance == closed.instance {
							stillOpen = true
							break
						}
					}
					if !stillOpen {
						settings.forgetInstance(closed.instance)
					}
				}
			case *v1.ViewResync:
				if v, ok := views[m.ViewID]; ok {
					v.rev = 0
					views[m.ViewID] = v
				}
				publish()
			case *v1.InputEvent:
				handleInput(ctx, c, controller, m, views, &selected, &pendingName)
				publish()
			case *v1.SettingsChanged:
				if settings.apply(m, minor) {
					controller.Reconfigure(settings.config)
					poll.Reset(settings.config.PollInterval)
				} else if m.Scope == v1.ScopeInstance {
					publish()
				}
			}
		case <-poll.C:
			controller.Refresh()
			publish()
			poll.Reset(settings.config.PollInterval)
		}
	}
}

// view is the per-view bookkeeping the process keeps: what it is, which manifest
// entry opened it, and the revision counter the host compares snapshots on.
type view struct {
	kind         v1.ViewKind
	entry        string
	rev          uint64
	instance     string
	width        int
	height       int
	resizeTarget int
}

func handleInput(
	ctx context.Context,
	c *v1.Client,
	controller *thinkpad.Controller,
	m *v1.InputEvent,
	views map[string]view,
	selected, pendingName *string,
) {
	if m == nil || controller == nil {
		return
	}
	source := views[m.ViewID]

	// The text field is the one place the user types, so its live value is
	// remembered here: the host owns the buffer, the plugin owns the meaning.
	switch {
	case m.Node == "profile-name" && (m.Event == v1.EventChange || m.Event == v1.EventSubmit):
		*pendingName = strings.TrimSpace(m.Text)
		if m.Event == v1.EventSubmit && *pendingName != "" {
			if *selected == "" {
				_ = controller.SaveProfile(*pendingName)
			} else {
				_ = controller.RenameProfile(*selected, *pendingName)
			}
			*selected = *pendingName
		}
		return
	}
	if m.Event == v1.EventChange {
		state := controller.Snapshot().State
		switch m.Node {
		case "fan-slider":
			if state.HaveFan && state.FanWritable && state.FanLevelValid {
				if level, ok := sliderInt(m.Value, 0, thinkpad.MaxNumericFanLevel); ok {
					report(controller.SetFanLevel(strconv.Itoa(level)))
				}
			}
		case "kbd-slider":
			maxLevel := state.Keyboard.MaxBrightness
			if maxLevel < 1 {
				maxLevel = 1
			}
			if maxLevel > thinkpad.MaxKeyboardLevel {
				maxLevel = thinkpad.MaxKeyboardLevel
			}
			if state.Keyboard.Present && state.Keyboard.Writable {
				if level, ok := sliderInt(m.Value, 0, maxLevel); ok {
					step := "off"
					if level > 0 {
						step = strconv.Itoa(level)
					}
					report(controller.SetKeyboardStep(step))
				}
			}
		}
		return
	}
	if m.Node == "open" && (m.Event == v1.EventActivate ||
		(m.Event == v1.EventPointer && m.Button == v1.ButtonSecondary)) {
		if c != nil {
			_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{
				Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: source.instance,
			})
		}
		return
	}
	if m.Event != v1.EventActivate {
		return
	}

	switch {
	case m.Node == "settings" || m.Node == "back":
		if err := switchPanel(ctx, c.Call, m.Node, v1.PanelParams{
			Entry: source.entry, Output: m.Output,
			Generation: m.Generation, Instance: source.instance,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "thinkpad: panel action %q failed: %v\n", m.Node, err)
		}
	case m.Node == "close":
		if err := callPanel(ctx, c.Call, v1.CallPanelClose, v1.PanelParams{
			Entry: source.entry, Output: m.Output,
			Generation: m.Generation, Instance: source.instance,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "thinkpad: close settings popup failed: %v\n", err)
		}
	case m.Node == "recheck":
		controller.Recheck()
	case m.Node == "setup:copy":
		_, _ = c.Call(ctx, v1.CallClipboardWrite, v1.ClipboardWriteParams{Text: setupScript})
	case strings.HasPrefix(m.Node, "fan:"):
		report(controller.SetFanLevel(strings.TrimPrefix(m.Node, "fan:")))
	case strings.HasPrefix(m.Node, "kbd:"):
		report(controller.SetKeyboardStep(strings.TrimPrefix(m.Node, "kbd:")))
	case m.Node == "lid":
		report(controller.ToggleLid())
	case strings.HasPrefix(m.Node, "profile:apply:"):
		name := strings.TrimPrefix(m.Node, "profile:apply:")
		report(controller.ApplyProfile(name))
		*selected = name
	case strings.HasPrefix(m.Node, "profile:delete:"):
		name := strings.TrimPrefix(m.Node, "profile:delete:")
		report(controller.DeleteProfile(name))
		if *selected == name {
			*selected = ""
		}
	case strings.HasPrefix(m.Node, "profile:pick:"):
		*selected = strings.TrimPrefix(m.Node, "profile:pick:")
	case m.Node == "profile:save":
		// An empty field saves over the active profile, which is the gesture
		// people reach for after nudging a level by hand.
		name := *pendingName
		if name == "" {
			name = controller.ActiveName()
		}
		report(controller.SaveProfile(name))
		*selected = name
	case m.Node == "profile:rename":
		if *selected != "" && *pendingName != "" {
			report(controller.RenameProfile(*selected, *pendingName))
			*selected = *pendingName
		}
	}
}

func sliderInt(value *float64, min, max int) (int, bool) {
	if value == nil || min > max || math.IsNaN(*value) || math.IsInf(*value, 0) ||
		math.Trunc(*value) != *value || *value < float64(min) || *value > float64(max) {
		return 0, false
	}
	return int(*value), true
}

// report keeps a failed action on stderr: the controller already raised the
// user-facing notification, so a toast about it would be a duplicate.
func report(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "thinkpad: %v\n", err)
	}
}

const setupScript = `# ThinkPad fan and LED control for sysc-shell
# Run these once, then log out and back in so sysc-shell receives the new group.

# 1. Unlock the fan level control. Without this the driver refuses to
#    change the level and only the speed readout works.
FAN_PARAM=/sys/module/thinkpad_acpi/parameters/fan_control
if [ -r "$FAN_PARAM" ] && [ "$(cat "$FAN_PARAM")" = "Y" ]; then
  echo "fan_control is already enabled."
else
  FAN_MODULE=$(modinfo -F filename thinkpad_acpi 2>/dev/null || true)
  if [ -n "$FAN_MODULE" ] && [ "$FAN_MODULE" != "(builtin)" ]; then
    echo 'options thinkpad_acpi fan_control=1' | sudo tee /etc/modprobe.d/99-sysc-thinkpad.conf
    sudo modprobe -r thinkpad_acpi && sudo modprobe thinkpad_acpi
  elif [ "$FAN_MODULE" = "(builtin)" ]; then
    echo "thinkpad_acpi is built into this kernel; /etc/modprobe.d cannot configure it."
    echo "Persist this setting on your kernel command line in the bootloader, then reboot:"
    echo "thinkpad_acpi.fan_control=1"
  else
    echo "Could not determine whether thinkpad_acpi is built in; skipping driver reload."
    echo "Check the module configuration or kernel command line for fan_control=1."
  fi
fi

# 2. Give your user a group that owns the controls.
sudo groupadd -f sysc-thinkpad
sudo usermod -aG sysc-thinkpad "$USER"

# 3. Hand the nodes to that group. The driver sets these back to root-only
#    every time the module loads, so the udev rule below repeats it on boot.
sudo chgrp sysc-thinkpad /proc/acpi/ibm/fan 2>/dev/null
sudo chmod 664 /proc/acpi/ibm/fan
for led in /sys/class/leds/tpacpi::*; do
  sudo chgrp sysc-thinkpad "$led/brightness" 2>/dev/null
  sudo chmod 664 "$led/brightness" 2>/dev/null
done

sudo tee /etc/udev/rules.d/99-sysc-thinkpad.rules >/dev/null <<'RULE'
# Keyboard backlight and lid logo lights arrive as leds-class events.
KERNEL=="tpacpi::*", SUBSYSTEM=="leds", ACTION=="add", RUN+="/bin/sh -c 'chgrp sysc-thinkpad /sys/class/leds/%k/brightness; chmod 664 /sys/class/leds/%k/brightness'"
# /proc/acpi/ibm/fan emits no uevent of its own, so hang the fan off the
# module add event that recreates it.
KERNEL=="thinkpad_acpi", SUBSYSTEM=="module", ACTION=="add", RUN+="/bin/sh -c 'chgrp sysc-thinkpad /proc/acpi/ibm/fan; chmod 664 /proc/acpi/ibm/fan'"
RULE
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=leds

# Done. Log out and back in, then press Re-check in the panel.
# If a lingering user service still reports read-only, save your work, run
# Use systemctl --user exit, then sign in again. This stops your user services.
# newgrp sysc-thinkpad updates only programs launched from that terminal.
`

func panelEntryForAction(action string) string {
	switch action {
	case "settings":
		return "settings"
	case "back":
		return "panel"
	default:
		return ""
	}
}

func switchPanel(
	ctx context.Context,
	call func(context.Context, v1.CallKind, any) (v1.HostReply, error),
	action string,
	source v1.PanelParams,
) error {
	target := panelEntryForAction(action)
	if target == "" {
		return fmt.Errorf("unknown panel action %q", action)
	}
	if source.Entry == "" {
		return fmt.Errorf("source panel entry is empty")
	}
	if source.Entry == target {
		return nil
	}
	source.Entry = target
	return callPanel(ctx, call, v1.CallPanelOpen, source)
}

func callPanel(
	ctx context.Context,
	call func(context.Context, v1.CallKind, any) (v1.HostReply, error),
	kind v1.CallKind,
	params v1.PanelParams,
) error {
	reply, err := call(ctx, kind, params)
	if err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("host rejected panel call: %s", reply.Error)
	}
	return nil
}

// settingsState keeps plugin-wide configuration separate from each widget
// placement's presentation preferences.
type settingsState struct {
	config    thinkpad.Config
	instances map[string]thinkpad.Instance
}

func newSettingsState(minor int) *settingsState {
	return &settingsState{
		config:    resolveConfig(nil),
		instances: map[string]thinkpad.Instance{},
	}
}

// apply returns true only when plugin-wide settings changed.
func (s *settingsState) apply(m *v1.SettingsChanged, minor int) bool {
	if m == nil {
		return false
	}
	switch m.Scope {
	case v1.ScopePlugin:
		s.config = resolveConfig(m.Values)
		return true
	case v1.ScopeInstance:
		if m.Instance != "" {
			s.instances[m.Instance] = resolveInstance(m.Values)
		}
	}
	return false
}

func (s *settingsState) instance(id string) thinkpad.Instance {
	if inst, ok := s.instances[id]; ok {
		return inst
	}
	return thinkpad.DefaultInstance()
}

func (s *settingsState) forgetInstance(id string) {
	delete(s.instances, id)
}

// negotiate picks the highest minor the host offers, capped at the newest wire
// this plugin speaks.
func negotiate(supported []v1.Version) int {
	minor := 0
	for _, v := range supported {
		if v.Major == 1 && v.Minor > minor && v.Minor <= v1.ProtocolMinor {
			minor = v.Minor
		}
	}
	return minor
}

func resolveConfig(values map[string]any) thinkpad.Config {
	b := func(key string, d bool) bool {
		if v, ok := values[key].(bool); ok {
			return v
		}
		return d
	}
	s := func(key, d string) string {
		if v, ok := values[key].(string); ok && v != "" {
			return v
		}
		return d
	}
	return thinkpad.Config{
		ActiveProfile:      s("active_profile", thinkpad.DefaultConfig().ActiveProfile),
		ApplyOnStart:       b("apply_on_start", true),
		LogoPath:           s("logo_path", ""),
		PollInterval:       thinkpad.DurationFromMS(intFrom(values, "poll_interval_ms", 2500)),
		WarnThermal:        b("warn_thermal", true),
		ThermalLimitC:      intFrom(values, "thermal_limit_c", 85),
		NotifyOnWriteError: b("notify_on_write_error", true),
	}
}

func resolveInstance(values map[string]any) thinkpad.Instance {
	b := func(key string, d bool) bool {
		if v, ok := values[key].(bool); ok {
			return v
		}
		return d
	}
	inst := thinkpad.DefaultInstance()
	inst.ShowFan = b("show_fan", inst.ShowFan)
	inst.ShowKbd = b("show_kbd", inst.ShowKbd)
	inst.ShowLid = b("show_lid", inst.ShowLid)
	inst.ShowRPM = b("show_rpm", inst.ShowRPM)
	return inst
}

func intFrom(values map[string]any, key string, fallback int) int {
	if v, ok := values[key].(float64); ok {
		return int(v)
	}
	return fallback
}
