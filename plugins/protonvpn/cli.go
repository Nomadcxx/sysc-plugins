// Package protonvpn wraps the official ProtonVPN Linux CLI
// (ProtonVPN/proton-vpn-cli, stable branch) behind parseable Go types.
package protonvpn

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Phase is the connection lifecycle state reported by `protonvpn status`.
type Phase int

const (
	PhaseDisconnected Phase = iota
	PhaseConnecting
	PhaseConnected
	PhaseDisconnecting
	PhaseError
)

// Status is the parsed output of `protonvpn status`.
type Status struct {
	Phase    Phase
	Server   string // "US-NY#1"
	Location string // "New York, United States" (or "Country", or "City, via EntryCountry")
	Country  string // ISO code parsed from the server-name prefix
	Load     int    // percent
	Protocol string // "wireguard"
}

// Info is the parsed output of `protonvpn info`.
type Info struct {
	Username string // from `Account: '{name}'`
}

// Config is the parsed output of `protonvpn config list`.
type Config struct {
	KillSwitch     string // "standard" | "off"
	NetShield      string // "off" | "malware-only" | "malware-ads-trackers"
	PortForwarding bool
}

// CLI runs the protonvpn binary.
type CLI struct {
	Bin     string
	Timeout time.Duration
	Now     func() time.Time
}

func (c *CLI) bin() string {
	if c.Bin == "" {
		return "protonvpn"
	}
	return c.Bin
}

func (c *CLI) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 10 * time.Second
	}
	return c.Timeout
}

func (c *CLI) run(ctx context.Context, timeout time.Duration, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Run executes the protonvpn binary with the given args under c.Timeout
// (default 10s), returning captured stdout and stderr alongside any error.
func (c *CLI) Run(ctx context.Context, args ...string) (string, string, error) {
	return c.run(ctx, c.timeout(), args...)
}

// wrapErr attaches the first stderr line to err, matching the repo pattern
// in plugins/github-notifications/gh.go.
func wrapErr(err error, stderr string) error {
	if d := ErrorDetail(stderr); d != "" {
		return fmt.Errorf("%w: %s", err, d)
	}
	return err
}

// Status runs `protonvpn status` and parses the output.
func (c *CLI) Status(ctx context.Context) (Status, error) {
	out, stderr, err := c.Run(ctx, "status")
	if err != nil {
		return Status{}, wrapErr(err, stderr)
	}
	return ParseStatus(out)
}

// Info runs `protonvpn info` and parses the output.
func (c *CLI) Info(ctx context.Context) (Info, error) {
	out, stderr, err := c.Run(ctx, "info")
	if err != nil {
		return Info{}, wrapErr(err, stderr)
	}
	return ParseInfo(out)
}

// Config runs `protonvpn config list` and parses the output.
func (c *CLI) Config(ctx context.Context) (Config, error) {
	out, stderr, err := c.Run(ctx, "config", "list")
	if err != nil {
		return Config{}, wrapErr(err, stderr)
	}
	return ParseConfig(out)
}

// connectArgs builds the argv for `protonvpn connect`: "" for the fastest
// server, "random"/"p2p"/"tor" for the matching flag, a 2-letter country code
// via --country, or a raw server name.
func connectArgs(target string) []string {
	args := []string{"connect"}
	switch target {
	case "":
	case "random":
		args = append(args, "--random")
	case "p2p":
		args = append(args, "--p2p")
	case "tor":
		args = append(args, "--tor")
	default:
		if len(target) == 2 {
			args = append(args, "--country", target)
		} else {
			args = append(args, target)
		}
	}
	return args
}

// Connect runs `protonvpn connect` with the given target (see connectArgs).
// Uses a 15s timeout.
func (c *CLI) Connect(ctx context.Context, target string) (string, string, error) {
	return c.run(ctx, 15*time.Second, connectArgs(target)...)
}

// Disconnect runs `protonvpn disconnect`.
func (c *CLI) Disconnect(ctx context.Context) (string, string, error) {
	return c.Run(ctx, "disconnect")
}

// ParseStatus parses `protonvpn status` output.
func ParseStatus(stdout string) (Status, error) {
	var s Status
	haveStatus := false
	for _, line := range strings.Split(stdout, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch strings.ToLower(key) {
		case "status":
			haveStatus = true
			switch strings.ToLower(val) {
			case "connected":
				s.Phase = PhaseConnected
			case "connecting":
				s.Phase = PhaseConnecting
			case "disconnected":
				s.Phase = PhaseDisconnected
			case "disconnecting":
				s.Phase = PhaseDisconnecting
			default:
				return Status{}, fmt.Errorf("protonvpn: unknown status %q", val)
			}
		case "server":
			name, loc, _ := strings.Cut(val, " in ")
			s.Server = name
			s.Location = loc
			s.Country = serverCountry(name)
		case "load":
			n, err := strconv.Atoi(strings.TrimSuffix(val, "%"))
			if err != nil {
				return Status{}, fmt.Errorf("protonvpn: bad Load %q", val)
			}
			s.Load = n
		case "protocol":
			s.Protocol = val
		}
	}
	if !haveStatus {
		return Status{}, fmt.Errorf("protonvpn: no Status key in output")
	}
	return s, nil
}

// serverCountry extracts the leading ISO code from a server name like
// "US-NY#1" or "US#1" (everything before the first '-' or '#').
func serverCountry(name string) string {
	if i := strings.IndexAny(name, "-#"); i > 0 {
		return name[:i]
	}
	return name
}

// ParseInfo parses `protonvpn info` output for the account name.
func ParseInfo(stdout string) (Info, error) {
	for _, line := range strings.Split(stdout, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Account") {
			continue
		}
		return Info{Username: strings.Trim(strings.TrimSpace(val), "'")}, nil
	}
	return Info{}, fmt.Errorf("protonvpn: no Account key in output")
}

// ParseConfig parses `protonvpn config list` tabulate output. Rows split on
// the first run of two or more spaces; "Upgrade to enable" means the feature
// is off (free tier).
func ParseConfig(stdout string) (Config, error) {
	settings := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Trim(trimmed, "-") == "" {
			continue
		}
		i := strings.Index(line, "  ")
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		settings[key] = strings.TrimSpace(line[i:])
	}
	get := func(key string) (string, error) {
		v, ok := settings[key]
		if !ok {
			return "", fmt.Errorf("protonvpn: config list missing %q", key)
		}
		if strings.EqualFold(v, "Upgrade to enable") {
			return "off", nil
		}
		return v, nil
	}
	var c Config
	var err error
	if c.KillSwitch, err = get("kill-switch"); err != nil {
		return Config{}, err
	}
	if c.NetShield, err = get("netshield"); err != nil {
		return Config{}, err
	}
	pf, err := get("port-forwarding")
	if err != nil {
		return Config{}, err
	}
	c.PortForwarding = strings.EqualFold(pf, "on")
	return c, nil
}

// ParseConnectIP extracts the IP from "Your new IP address is X." or "".
func ParseConnectIP(stdout string) string {
	const marker = "Your new IP address is "
	for _, line := range strings.Split(stdout, "\n") {
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		ip := strings.TrimSpace(line[i+len(marker):])
		return strings.TrimSuffix(ip, ".")
	}
	return ""
}

// cliNoise marks stderr lines emitted by the CLI's wrappers, not the CLI
// itself: the packaged binary prints a sentry/eventlet deprecation block
// (verified live against proton-vpn-cli 1.0.3) before real output.
var cliNoise = []string{
	"warning", ".py", "sentry", "eventlet", "deprecated",
	"recommend", "framework", "https://", "import ",
}

// ErrorDetail returns the first non-empty stderr line that is not wrapper
// noise, trimmed. ponytail: blocklist of the one known noisy wrapper; swap
// for structured stderr parsing if the CLI grows more.
func ErrorDetail(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		noisy := false
		for _, n := range cliNoise {
			if strings.Contains(low, n) {
				noisy = true
				break
			}
		}
		if !noisy {
			return line
		}
	}
	return ""
}
