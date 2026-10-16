package thinkpad

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Notify is a one-shot user-visible message. The controller does not own the
// shell connection, so the process wires this to a notify call.
type Notify struct {
	Summary  string
	Body     string
	Critical bool
}

// Result reports what an action actually achieved. A refused write is an
// error rather than a silent no-op, but the other controls are still pushed, so
// a missing permission on one light does not block the rest.
type Result struct {
	Applied []string
	Failed  []error
}

// writeMask keeps direct controls independent; applying a profile selects all.
type writeMask uint8

const (
	writeFan writeMask = 1 << iota
	writeKeyboard
	writeLid
	writeAll = writeFan | writeKeyboard | writeLid
)

// OK reports whether everything asked for was written.
func (r Result) OK() bool { return len(r.Failed) == 0 }

// Error flattens the failures into one message.
func (r Result) Error() error {
	if r.OK() {
		return nil
	}
	parts := make([]string, 0, len(r.Failed))
	for _, err := range r.Failed {
		parts = append(parts, err.Error())
	}
	return errors.New(strings.Join(parts, "; "))
}

// Snapshot is everything a view needs to draw one frame, captured under one
// lock so the tree never mixes state from two different reads.
type Snapshot struct {
	State      State
	Config     Config
	Active     string
	Want       Settings
	Profiles   []Profile
	StoreIssue string
	StorePath  string
	Busy       bool
	// External reports that the user changed a light with the hardware key
	// since the last frame, so the panel can say so rather than look stuck.
	External bool
}

// Controller is the plugin's brain. It owns the hardware and the profile store
// and is the only thing that writes to the machine, which keeps every change
// going through one path: apply, re-read, then trust what the kernel stored.
type Controller struct {
	hw     *Hardware
	store  *Store
	notify func(Notify)

	mu     sync.Mutex
	cfg    Config
	active string
	want   Settings
	state  State
	busy   bool
	warmed bool
	warned bool
	// external latches when the kernel reports a light was changed outside
	// software, and clears on the next change we make ourselves.
	external bool
	hwSeen   struct{ keyboard, lid int }
	hwValid  struct{ keyboard, lid bool }
}

// NewController wires a controller to its hardware and profile store. The
// notify callback may be nil.
func NewController(hw *Hardware, store *Store, notify func(Notify)) *Controller {
	// Put the starter profiles on disk on first run so the file exists for the
	// user to find and edit. A store that cannot be written still works, it
	// just will not remember.
	_ = store.seedSeeded()
	return &Controller{hw: hw, store: store, notify: notify, cfg: DefaultConfig()}
}

// Reconfigure takes new host settings. The first call is also the moment the
// start-up replay happens, because that is when the host has finished saying
// what the user wants.
func (c *Controller) Reconfigure(cfg Config) {
	c.mu.Lock()
	first := !c.warmed
	c.cfg = cfg
	c.mu.Unlock()

	if !first {
		return
	}
	c.warmed = true
	state := c.hw.Read()
	c.mu.Lock()
	c.external = c.detectExternalLocked(state)
	c.state = state
	c.want = settingsFromState(state)
	cfg = c.cfg
	c.mu.Unlock()
	c.checkThermal(state, cfg)
	c.startOnce()
}

// Snapshot captures the current state for the views.
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		State:      c.state,
		Config:     c.cfg,
		Active:     c.active,
		Want:       c.want,
		Profiles:   c.store.Profiles(),
		StoreIssue: c.store.LoadIssue(),
		StorePath:  c.store.Path(),
		Busy:       c.busy,
		External:   c.external,
	}
}

// Refresh re-reads the hardware and checks the thermal guard.
func (c *Controller) Refresh() {
	state := c.hw.Read()

	c.mu.Lock()
	c.external = c.detectExternalLocked(state)
	c.state = state
	cfg := c.cfg
	c.mu.Unlock()

	c.checkThermal(state, cfg)
}

// detectExternalLocked notices a light the user changed with the hardware key,
// so the UI can say "you changed this with the key" instead of looking stuck.
func (c *Controller) detectExternalLocked(state State) bool {
	changed := func(st LEDState, seen *int, valid *bool) bool {
		if !st.HWChangedValid {
			return false
		}
		// The first complete sample is the baseline, not a movement: without
		// recording it here the flag could never arm and a real Fn-key press
		// would look identical to the state we already agreed on.
		changed := *valid && *seen != st.HWChanged
		*seen, *valid = st.HWChanged, true
		return changed
	}
	keyboardChanged := changed(state.Keyboard, &c.hwSeen.keyboard, &c.hwValid.keyboard)
	lidChanged := changed(state.Lid, &c.hwSeen.lid, &c.hwValid.lid)
	if keyboardChanged {
		c.want.KeyboardOn = state.Keyboard.On()
		if c.want.KeyboardOn {
			c.want.KeyboardLevel = clampLevel(state.Keyboard.Brightness, MaxKeyboardLevel)
		}
	}
	if lidChanged {
		c.want.LidOn = state.Lid.On()
	}
	return keyboardChanged || lidChanged
}

func settingsFromState(state State) Settings {
	want := Settings{LidOn: state.Lid.On()}
	if state.FanLevelValid {
		want.FanLevel = NormalizeFanLevel(state.FanLevel)
	}
	if state.Keyboard.Present {
		want.KeyboardOn = state.Keyboard.On()
		if want.KeyboardOn {
			want.KeyboardLevel = clampLevel(state.Keyboard.Brightness, MaxKeyboardLevel)
		}
	}
	return want
}

// startOnce restores the saved profile, which is what removes the boot-time
// tedium of re-tuning everything by hand.
func (c *Controller) startOnce() {
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()

	name := strings.TrimSpace(cfg.ActiveProfile)
	if name == "" {
		name = c.store.LastUsed()
	}
	if _, ok := c.store.Profile(name); !ok {
		// The host may name a profile the user has since deleted. Falling back
		// to the last one used keeps the machine in a known state instead of
		// leaving it at whatever the firmware chose.
		name = ""
		if last := c.store.LastUsed(); last != "" {
			if _, ok := c.store.Profile(last); ok {
				name = last
			}
		}
	}
	if name == "" {
		return
	}
	if !cfg.ApplyOnStart {
		profile, _ := c.store.Profile(name)
		c.mu.Lock()
		c.active = name
		if !c.want.KeyboardOn {
			c.want.KeyboardLevel = profile.Settings.KeyboardLevel
		}
		c.mu.Unlock()
		_ = c.store.Remember(name)
		return
	}
	if err := c.ApplyProfile(name); err != nil && cfg.NotifyOnWriteError {
		c.raise("Could not restore the saved profile", err.Error(), true)
	}
}

// ApplyProfile pushes a named profile to the hardware and remembers it as the
// one in effect.
func (c *Controller) ApplyProfile(name string) error {
	p, ok := c.store.Profile(name)
	if !ok {
		return fmt.Errorf("no profile named %q", name)
	}

	c.mu.Lock()
	c.active = p.Name
	c.want = sanitizeSettings(p.Settings)
	c.external = false
	c.mu.Unlock()

	res := c.apply(writeAll)
	if err := res.Error(); err != nil {
		return err
	}
	_ = c.store.Remember(p.Name)
	return nil
}

// SetFanLevel pins the fan directly. The active profile is updated to match, so
// the choice survives a reboot.
func (c *Controller) SetFanLevel(level string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "" && !ValidFanLevel(level) {
		return fmt.Errorf("unsupported fan level %q", level)
	}

	c.mu.Lock()
	want := c.want
	want.FanLevel = level
	c.want = want
	c.external = false
	c.mu.Unlock()

	return c.applyAndPersist(writeFan)
}

// SetKeyboard sets the backlight directly. The level is kept even while the
// light is off, so switching it back on restores what you had.
func (c *Controller) SetKeyboard(on bool, level int) error {
	level = clampInt(level, 0, MaxKeyboardLevel)

	c.mu.Lock()
	want := c.want
	want.KeyboardOn = on
	want.KeyboardLevel = level
	c.want = want
	c.external = false
	c.mu.Unlock()

	return c.applyAndPersist(writeKeyboard)
}

// ToggleKeyboard flips the backlight, restoring the remembered level when it
// comes back on.
func (c *Controller) ToggleKeyboard() error {
	c.mu.Lock()
	want := c.want
	if want.KeyboardOn {
		// Level 0 is a real, persisted choice: switching off remembers it.
		want.KeyboardOn = false
	} else {
		want.KeyboardOn = true
		if want.KeyboardLevel < 1 {
			want.KeyboardLevel = 1
		}
	}
	c.want = want
	c.external = false
	c.mu.Unlock()

	return c.applyAndPersist(writeKeyboard)
}

// SetKeyboardStep applies one of the backlight buttons: "off" or a decimal
// level. The panel builds those ids, so an unknown value is a plugin bug rather
// than user input and is rejected loudly.
func (c *Controller) SetKeyboardStep(step string) error {
	step = strings.ToLower(strings.TrimSpace(step))
	if step == "off" || step == "0" {
		// Dark is dark: a caller spelling the step as a zero means the same
		// thing as the word. The remembered level has to survive either form,
		// so the toggle can bring the same brightness back.
		c.mu.Lock()
		want := c.want
		want.KeyboardOn = false
		c.want = want
		c.mu.Unlock()

		return c.applyAndPersist(writeKeyboard)
	}
	level, err := strconv.Atoi(step)
	if err != nil || level < 1 || level > MaxKeyboardLevel {
		// Reject rather than clamp: this is the entry point a host or a stale
		// bar can drive, so a value outside the domain is a mistake worth
		// reporting. The final clamp to what the light advertises belongs to
		// the hardware layer, which is the only place that knows the answer.
		return fmt.Errorf("unsupported backlight step %q", step)
	}
	return c.SetKeyboard(true, level)
}

// ToggleLid flips the lid logo light.
func (c *Controller) ToggleLid() error {
	c.mu.Lock()
	want := c.want
	want.LidOn = !want.LidOn
	c.want = want
	c.external = false
	c.mu.Unlock()

	return c.applyAndPersist(writeLid)
}

// SetLid lights or extinguishes the lid logo directly.
func (c *Controller) SetLid(on bool) error {
	c.mu.Lock()
	want := c.want
	want.LidOn = on
	c.want = want
	c.external = false
	c.mu.Unlock()

	return c.applyAndPersist(writeLid)
}

// SaveProfile stores the current settings under a name and makes it active, so
// "save as" and "switch to" are the same gesture.
func (c *Controller) SaveProfile(name string) error {
	c.mu.Lock()
	p := Profile{Name: strings.TrimSpace(name), Settings: c.want}
	if p.Name == "" {
		p.Name = c.active
	}
	c.mu.Unlock()

	if err := c.store.Save(p); err != nil {
		return err
	}
	c.mu.Lock()
	c.active = p.Name
	c.mu.Unlock()
	return c.store.Remember(p.Name)
}

// DeleteProfile removes a profile.
func (c *Controller) DeleteProfile(name string) error {
	if err := c.store.Delete(name); err != nil {
		return err
	}
	c.mu.Lock()
	if strings.EqualFold(c.active, name) {
		c.active = ""
	}
	c.mu.Unlock()
	return nil
}

// RenameProfile changes a profile's name, keeping its settings.
func (c *Controller) RenameProfile(from, to string) error {
	err := c.store.Rename(from, to)
	if err == nil && strings.EqualFold(from, c.ActiveName()) {
		c.mu.Lock()
		c.active = strings.TrimSpace(to)
		c.mu.Unlock()
	}
	return err
}

// ActiveName reports the profile currently in effect.
func (c *Controller) ActiveName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// Recheck drops cached write probes and re-reads, so the panel's "needs setup"
// state clears as soon as a permission fix is actually in place.
func (c *Controller) Recheck() {
	c.hw.ForgetWriteProbes()
	c.Refresh()
}

// apply writes the selected wanted settings one control at a time. The writes
// are followed by Refresh because thinkpad_acpi can accept one and keep the old
// value anyway.
func (c *Controller) applyAndPersist(mask writeMask) error {
	if err := c.apply(mask).Error(); err != nil {
		return err
	}
	return c.persistActive()
}

func (c *Controller) apply(mask writeMask) Result {
	c.mu.Lock()
	want := c.want
	c.busy = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()

	var res Result
	push := func(name string, err error) {
		if err != nil {
			res.Failed = append(res.Failed, fmt.Errorf("%s: %w", name, err))
			return
		}
		res.Applied = append(res.Applied, name)
	}

	// A deliberate change is a fresh decision, so the thermal latch re-arms:
	// pinning a low level while the machine is already hot deserves its own
	// warning rather than being swallowed by the start-up one.
	c.mu.Lock()
	c.warned = false
	// Some drivers bump brightness_hw_changed on a software write as well as
	// on the key. Clearing the flag here keeps our own toggle from coming
	// back to us a moment later as "you changed this with the key".
	c.external = false
	c.mu.Unlock()

	if mask&writeFan != 0 && want.FanLevel != "" && c.hw.fanAvailable() {
		push("fan", c.hw.SetFanLevel(want.FanLevel))
	}
	if mask&writeKeyboard != 0 && c.hw.keyboardAvailable() {
		push("keyboard", c.hw.SetKeyboard(want.KeyboardValue()))
	}
	if mask&writeLid != 0 && c.hw.lidAvailable() {
		push("lid logo", c.hw.SetLid(want.LidOn))
	}

	if !res.OK() && c.cfg.NotifyOnWriteError {
		// One message per failing apply, with the fix, is more use than a
		// silent refusal the user only discovers from an unchanged light.
		c.raise("ThinkPad: change refused", res.Error().Error()+
			"\n\nOpen the panel and follow the setup steps, then press Re-check.", true)
	}
	c.Refresh()
	return res
}

// persistActive folds the live settings back into the active profile, so the
// profile you are using always describes the machine as it actually is. This
// is what makes a direct toggle survive a reboot even though the host will not
// accept settings written by a plugin.
func (c *Controller) persistActive() error {
	c.mu.Lock()
	name := c.active
	want := c.want
	c.mu.Unlock()

	if strings.TrimSpace(name) == "" {
		return nil
	}
	return c.store.Save(Profile{Name: name, Settings: want})
}

// checkThermal warns when the CPU is hot. The case that actually matters is a
// low fan level pinned while the machine is already running hot, so that is
// what the message says; the change is still allowed, because silently
// refusing a setting the user just asked for is worse than warning them.
func (c *Controller) checkThermal(state State, cfg Config) {
	if !cfg.WarnThermal || !state.Hot(cfg.ThermalLimitC) {
		c.mu.Lock()
		c.warned = false
		c.mu.Unlock()
		return
	}

	c.mu.Lock()
	if c.warned {
		c.mu.Unlock()
		return
	}
	c.warned = true
	level := c.want.FanLevel
	c.mu.Unlock()

	body := fmt.Sprintf("CPU is %.0f°C, at or above your %d°C threshold.", state.CPUTempC, cfg.ThermalLimitC)
	if n := FanLevelNumber(level); n >= 0 && n <= 2 {
		body += fmt.Sprintf(" The fan is pinned to level %d; consider Auto or Full.", n)
	}
	c.raise("ThinkPad: running hot", body, true)
}

// raise sends a notification when the caller asked for one.
func (c *Controller) raise(summary, body string, critical bool) {
	if c.notify == nil {
		return
	}
	c.notify(Notify{Summary: summary, Body: body, Critical: critical})
}
