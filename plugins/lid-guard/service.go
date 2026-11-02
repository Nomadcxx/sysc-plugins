package lidguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	unitName          = "noctalia-lid-guard.service"
	failedUnitMessage = "The Lid Guard service failed; enable it to recover."
)

var (
	ErrBusy        = errors.New("a Lid Guard action is already running")
	ErrUnavailable = errors.New("Lid Guard dependencies are unavailable")
)

var requiredCommands = []string{"systemctl", "systemd-run", "systemd-inhibit", "sleep"}

// Snapshot is the service state consumed by the bar, panel and host status
// message. State comes from systemd, not from the last requested action.
type Snapshot struct {
	State     v1.ServiceState
	Available bool
	// Busy marks a requested state change. Read-only status checks use
	// Refreshing so polling does not look like a toggle to the UI or host.
	Busy       bool
	Refreshing bool
	Message    string
	Revision   uint64
}

// Options keeps the system command boundary replaceable in tests.
type Options struct {
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) (stdout, stderr string, err error)
}

// Service serializes user requests and publishes only observed systemd state.
type Service struct {
	mu             sync.Mutex
	status         Snapshot
	commands       map[string]string
	depsOK         bool
	lookPath       func(string) (string, error)
	run            func(context.Context, string, ...string) (string, string, error)
	updates        chan Snapshot
	pendingAction  string
	pendingContext context.Context
}

// New checks whether the required systemd and coreutils commands are present.
// The user service manager itself is checked asynchronously by refresh.
func New(opts Options) *Service {
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Run == nil {
		opts.Run = runCommand
	}
	s := &Service{
		commands: make(map[string]string, len(requiredCommands)),
		lookPath: opts.LookPath,
		run:      opts.Run,
		updates:  make(chan Snapshot, 1),
		depsOK:   true,
		status: Snapshot{
			State:     v1.ServiceStateUnknown,
			Available: true,
		},
	}
	commands, missing := locateCommands(s.lookPath)
	s.commands = commands
	s.depsOK = len(missing) == 0
	if len(missing) > 0 {
		s.status.State = v1.ServiceStateUnavailable
		s.status.Available = false
		s.status.Message = "Missing required commands: " + strings.Join(missing, ", ")
	}
	s.status.Revision = 1
	return s
}

func locateCommands(lookPath func(string) (string, error)) (map[string]string, []string) {
	commands := make(map[string]string, len(requiredCommands))
	var missing []string
	for _, name := range requiredCommands {
		path, err := lookPath(name)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		commands[name] = path
	}
	return commands, missing
}

func runCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Service) Updates() <-chan Snapshot { return s.updates }

// Request accepts one of the declared service actions and runs it off the
// plugin's message loop. One mutation can queue behind a read-only refresh;
// concurrent mutations return ErrBusy to avoid racing the transient unit.
func (s *Service) Request(ctx context.Context, action string) error {
	switch action {
	case "enable", "disable", "toggle", "refresh", "status":
	default:
		return fmt.Errorf("unknown Lid Guard action %q", action)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	readOnly := action == "refresh" || action == "status"
	s.mu.Lock()
	if s.status.Busy {
		s.mu.Unlock()
		return ErrBusy
	}
	if !s.depsOK && !readOnly {
		s.mu.Unlock()
		return ErrUnavailable
	}
	if s.status.Refreshing {
		if readOnly || s.pendingAction != "" {
			s.mu.Unlock()
			return ErrBusy
		}
		s.pendingAction = action
		s.pendingContext = ctx
		s.status.Busy = true
		s.status.Message = ""
		s.publishLocked()
		s.mu.Unlock()
		return nil
	}
	if readOnly {
		s.status.Refreshing = true
	} else {
		s.status.Busy = true
		s.status.Message = ""
		s.publishLocked()
	}
	s.mu.Unlock()
	go s.perform(ctx, action)
	return nil
}

func (s *Service) perform(parent context.Context, action string) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	state, message, err := s.apply(ctx, action)
	if err != nil && state != v1.ServiceStateUnavailable {
		state = v1.ServiceStateError
		message = cleanMessage(err.Error())
	}
	s.mu.Lock()
	s.status.State = state
	s.status.Available = state != v1.ServiceStateUnavailable
	s.status.Message = cleanMessage(message)
	s.status.Refreshing = false
	if s.pendingAction != "" && state != v1.ServiceStateUnavailable {
		action, nextContext := s.pendingAction, s.pendingContext
		s.pendingAction, s.pendingContext = "", nil
		s.status.Busy = true
		s.status.Message = ""
		s.publishLocked()
		s.mu.Unlock()
		go s.perform(nextContext, action)
		return
	}
	s.pendingAction, s.pendingContext = "", nil
	s.status.Busy = false
	s.publishLocked()
	s.mu.Unlock()
}

func (s *Service) apply(ctx context.Context, action string) (v1.ServiceState, string, error) {
	if action == "refresh" || action == "status" {
		commands, missing := locateCommands(s.lookPath)
		s.mu.Lock()
		s.commands = commands
		s.depsOK = len(missing) == 0
		s.mu.Unlock()
		if len(missing) > 0 {
			return v1.ServiceStateUnavailable, "Missing required commands: " + strings.Join(missing, ", "), nil
		}
	}
	state, message := s.check(ctx)
	if action == "refresh" || action == "status" {
		return state, message, nil
	}
	if state == v1.ServiceStateUnknown {
		return state, "Lid Guard is changing state; wait for systemd to settle before changing it.", nil
	}
	if state == v1.ServiceStateUnavailable {
		return state, message, errors.New(message)
	}
	failedUnit := isRecoverableFailedUnit(state, message)
	if state == v1.ServiceStateError && !failedUnit {
		return state, message, errors.New(message)
	}

	active := state == v1.ServiceStateActive
	wantActive := action == "enable" || (action == "toggle" && !active)
	if (action == "enable" && active) || (action == "disable" && !active && !failedUnit) {
		return state, message, nil
	}
	if wantActive {
		// reset-failed is cleanup; a first start has no loaded unit to reset.
		_ = s.command(ctx, "systemctl", "--user", "reset-failed", unitName)
		if err := s.command(ctx, "systemd-run",
			"--user", "--quiet", "--collect", "--unit="+unitName,
			"--description=Sysc Lid Guard", "--",
			s.commands["systemd-inhibit"],
			"--what=handle-lid-switch",
			"--who=Sysc Lid Guard",
			"--why=Keep background jobs running while the laptop lid is closed",
			"--mode=block", s.commands["sleep"], "infinity"); err != nil {
			return v1.ServiceStateError, "", commandError("Starting the Lid Guard service", err)
		}
		return s.waitFor(ctx, v1.ServiceStateActive)
	}
	if err := s.command(ctx, "systemctl", "--user", "stop", unitName); err != nil {
		return v1.ServiceStateError, "", commandError("Stopping the Lid Guard service", err)
	}
	return s.waitFor(ctx, v1.ServiceStateInactive)
}

func (s *Service) waitFor(ctx context.Context, want v1.ServiceState) (v1.ServiceState, string, error) {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, message := s.check(ctx)
		if state == want {
			return state, message, nil
		}
		if state == v1.ServiceStateUnavailable || state == v1.ServiceStateError {
			return state, message, errors.New(message)
		}
		select {
		case <-ctx.Done():
			return v1.ServiceStateError, "", ctx.Err()
		case <-deadline.C:
			return v1.ServiceStateError, "", fmt.Errorf("Lid Guard did not become %s", want)
		case <-ticker.C:
		}
	}
}

func isRecoverableFailedUnit(state v1.ServiceState, message string) bool {
	return state == v1.ServiceStateError && message == failedUnitMessage
}

func (s *Service) check(ctx context.Context) (v1.ServiceState, string) {
	stdout, stderr, err := s.commandOutput(ctx, "systemctl", "--user", "is-active", unitName)
	activeState := strings.TrimSpace(stdout)
	switch activeState {
	case "active":
		return v1.ServiceStateActive, ""
	case "inactive":
		return v1.ServiceStateInactive, ""
	case "failed":
		return v1.ServiceStateError, failedUnitMessage
	case "activating", "deactivating", "reloading":
		return v1.ServiceStateUnknown, "Lid Guard is changing state."
	}
	if userManagerUnavailable(stderr) {
		return v1.ServiceStateUnavailable, "The systemd user service manager is unavailable."
	}
	if err == nil {
		return v1.ServiceStateError, "systemd returned an unknown Lid Guard service state."
	}
	if detail := cleanMessage(stderr); detail != "" {
		return v1.ServiceStateError, detail
	}
	return v1.ServiceStateError, "Could not read Lid Guard service status: " + err.Error()
}

func userManagerUnavailable(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, needle := range []string{"failed to connect to bus", "no medium found", "user manager is not running", "system has not been booted with systemd"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (s *Service) command(ctx context.Context, name string, args ...string) error {
	_, stderr, err := s.commandOutput(ctx, name, args...)
	if err == nil {
		return nil
	}
	if detail := cleanMessage(stderr); detail != "" {
		return errors.New(detail)
	}
	return err
}

func (s *Service) commandOutput(parent context.Context, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return s.run(ctx, s.commands[name], args...)
}

func commandError(action string, err error) error { return fmt.Errorf("%s: %w", action, err) }

func cleanMessage(message string) string {
	message = strings.ToValidUTF8(message, "�")
	var safe strings.Builder
	for _, r := range message {
		if unicode.IsControl(r) {
			safe.WriteByte(' ')
		} else {
			safe.WriteRune(r)
		}
	}
	message = strings.Join(strings.Fields(safe.String()), " ")
	for len(message) > v1.MaxServiceStatusMessageBytes {
		_, size := utf8.DecodeLastRuneInString(message)
		message = message[:len(message)-size]
	}
	return message
}

func (s *Service) publishLocked() {
	s.status.Revision++
	select {
	case s.updates <- s.status:
	default:
		select {
		case <-s.updates:
		default:
		}
		select {
		case s.updates <- s.status:
		default:
		}
	}
}
