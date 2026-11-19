package vpn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const readTimeout = 5 * time.Second
const mutationTimeout = 30 * time.Second
const readLimit = 1 << 20

// CLI contains resolved executable paths. An empty path means not installed.
type CLI struct {
	MullvadBin, ObscuraBin string
	Obscura                ObscuraCaps
}

// ObscuraCaps is what the installed obscura CLI can do beyond the upstream
// connect, disconnect and status. Newer builds add settings, location
// targets and a status read that does not refresh the account.
type ObscuraCaps struct{ Settings, Locations, Local bool }

func obscuraCommand(help, name string) bool {
	return regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(name) + `\s`).MatchString(help)
}

// ProbeObscura reads the CLI's own help. A CLI that cannot answer gets the
// upstream feature set, so nothing is offered that it would refuse.
func (c CLI) ProbeObscura(ctx context.Context) ObscuraCaps {
	help, err := c.run(ctx, Obscura, readTimeout, readLimit, "--help")
	if err != nil {
		return ObscuraCaps{}
	}
	status, _ := c.run(ctx, Obscura, readTimeout, readLimit, "status", "--help")
	connect, _ := c.run(ctx, Obscura, readTimeout, readLimit, "connect", "--help")
	h := string(help)
	return ObscuraCaps{
		Settings:  obscuraCommand(h, "auto-connect") && obscuraCommand(h, "lan") && obscuraCommand(h, "dns"),
		Locations: obscuraCommand(h, "locations") && strings.Contains(string(connect), "--country"),
		Local:     strings.Contains(string(status), "--local"),
	}
}

// statusArgs reads Obscura without refreshing the account where it can: a
// refresh bumps the status version and wakes every other long-polling client.
func (c CLI) statusArgs(p Provider) []string {
	if p == Obscura && c.Obscura.Local {
		return []string{"status", "--local", "--json"}
	}
	return []string{"status", "--json"}
}

func (c CLI) Installed(p Provider) bool {
	return p == Mullvad && c.MullvadBin != "" || p == Obscura && c.ObscuraBin != ""
}

type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func (c CLI) run(ctx context.Context, p Provider, timeout time.Duration, limit int, args ...string) ([]byte, error) {
	var bin string
	switch p {
	case Mullvad:
		bin = c.MullvadBin
	case Obscura:
		bin = c.ObscuraBin
	default:
		return nil, fmt.Errorf("unknown VPN provider")
	}
	if bin == "" {
		return nil, fmt.Errorf("%s CLI not found", p.Name())
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 250 * time.Millisecond
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if key != "LC_ALL" && key != "LANG" && key != "NO_COLOR" {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "LANG=C", "NO_COLOR=1")
	stdout, stderr := &cappedBuffer{limit: limit}, &cappedBuffer{limit: readLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s command interrupted; check VPN status: %w", p.Name(), ctx.Err())
	}
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("%s command output exceeded its limit", p.Name())
	}
	if err != nil {
		// Raw CLI output can contain account IDs or keys. Only fixed notices
		// cross the process boundary into the UI; never include err's path.
		notice := "command failed; check the official app"
		detail := strings.ToLower(stderr.buf.String())
		switch {
		case strings.Contains(detail, "permission"), strings.Contains(detail, "access denied"):
			notice = "permission denied; check operator access"
		case strings.Contains(detail, "not logged"), strings.Contains(detail, "revoked"):
			notice = "sign in using the official app or CLI"
		case strings.Contains(detail, "not running"), strings.Contains(detail, "daemon"), strings.Contains(detail, "service"):
			notice = "service unavailable; check the official app"
		}
		return nil, fmt.Errorf("%s: %s", p.Name(), notice)
	}
	return stdout.buf.Bytes(), nil
}

func (c CLI) Status(ctx context.Context, p Provider) (Observation, error) {
	raw, err := c.run(ctx, p, readTimeout, readLimit, c.statusArgs(p)...)
	if err != nil {
		return Observation{}, err
	}
	if p == Obscura {
		return ParseObscuraStatus(raw)
	}
	return ParseMullvadStatus(raw)
}

func (c CLI) Connect(ctx context.Context, p Provider) error {
	_, err := c.run(ctx, p, mutationTimeout, readLimit, "connect")
	return err
}

func (c CLI) Disconnect(ctx context.Context, p Provider) error {
	_, err := c.run(ctx, p, mutationTimeout, readLimit, "disconnect")
	return err
}

func checkOther(p Provider, o Observation) error {
	if o.Phase != Disconnected {
		return fmt.Errorf("Disconnect %s before connecting another VPN", p.Name())
	}
	if o.Traffic == TrafficBlocked {
		return fmt.Errorf("Disable %s lockdown before connecting another VPN", p.Name())
	}
	return nil
}

// Mutate is the single typed mutation entry point. Preflight cannot lock an
// external app or a second daemon; it is a fresh observation, not arbitration.
func (c CLI) Mutate(ctx context.Context, p Provider, a Action, locations []Location) error {
	if !p.Valid() || !c.Installed(p) {
		return errors.New("VPN provider is unavailable")
	}
	switch a.Kind {
	case "connect", "disconnect":
	case "location":
		if p == Obscura && !c.Obscura.Locations {
			return errors.New("This Obscura CLI cannot choose a location")
		}
		check := LocationArgs
		if p == Obscura {
			check = ObscuraConnectArgs
		}
		if _, err := check(a.Location, locations); err != nil {
			return err
		}
	case "setting":
		if p == Obscura && (!c.Obscura.Settings || a.Key == "lockdown-mode") {
			return errors.New("This Obscura CLI cannot change that setting")
		}
		if a.Key != "auto-connect" && a.Key != "lan" && a.Key != "lockdown-mode" {
			if len(a.Key) != 5 || !strings.HasPrefix(a.Key, "dns:") || a.Key[4] < '0' || a.Key[4] > '5' {
				return errors.New("unknown VPN setting")
			}
		}
	default:
		return errors.New("unknown VPN action")
	}
	if a.Kind == "connect" || a.Kind == "location" {
		other := p.Other()
		if c.Installed(other) {
			o, err := c.Status(ctx, other)
			if err != nil {
				return fmt.Errorf("Cannot check %s; use its official app before connecting", other.Name())
			}
			if err := checkOther(other, o); err != nil {
				return err
			}
		}
	}
	switch a.Kind {
	case "connect":
		return c.Connect(ctx, p)
	case "disconnect":
		return c.Disconnect(ctx, p)
	case "location":
		// Obscura's location command is itself a connect.
		if err := c.SelectLocation(ctx, p, a.Location, locations); err != nil || !a.Connect || p == Obscura {
			return err
		}
		return c.Connect(ctx, p)
	default:
		return c.Set(ctx, p, a.Key, a.Enabled)
	}
}
