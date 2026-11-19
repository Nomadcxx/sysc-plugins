package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

type DNS struct {
	Mode   string // default | custom | system
	Blocks [6]bool
}

type Settings struct {
	AutoConnect, LAN, Lockdown *bool
	DNS                        *DNS
	Tailscale, WireGuard       *bool
	Constraints                string
	SavedLocation              string // Mullvad relay location as a Location ID; "" when any, a custom list, or unknown
}

var savedLocationLine = regexp.MustCompile(`(?m)^ {4}Location: +(?:country ([a-z]{2})|city ([a-z]{3}), ([a-z]{2})(?:, hostname ([a-z]{2}-[a-z]{3}-wg-[0-9]+))?)$`)

// ParseSavedLocation reads the location constraint from mullvad relay get.
func ParseSavedLocation(raw string) string {
	m := savedLocationLine.FindStringSubmatch(raw)
	switch {
	case m == nil:
		return ""
	case m[1] != "":
		return m[1] + "::"
	}
	return m[3] + ":" + m[2] + ":" + m[4]
}

var BlockerNames = [...]string{"Ads", "Trackers", "Malware", "Adult content", "Gambling", "Social media"}
var dnsFlags = [...]string{"--block-ads", "--block-trackers", "--block-malware", "--block-adult-content", "--block-gambling", "--block-social-media"}

func ParsePolicy(raw, name, on, off string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case name + ": " + on:
		return true, nil
	case name + ": " + off:
		return false, nil
	}
	return false, errors.New("Mullvad setting format changed")
}

func ParseMullvadDNS(raw string) (DNS, error) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	bad := errors.New("Mullvad DNS format changed")
	if len(lines) >= 2 && lines[0] == "Custom DNS: yes" && lines[1] == "Servers:" {
		for _, line := range lines[2:] {
			if net.ParseIP(strings.TrimSpace(line)) == nil {
				return DNS{}, bad
			}
		}
		return DNS{Mode: "custom"}, nil
	}
	if len(lines) != 7 || lines[0] != "Custom DNS: no" {
		return DNS{}, bad
	}
	dns := DNS{Mode: "default"}
	for i, name := range []string{"Block ads", "Block trackers", "Block malware", "Block adult content", "Block gambling", "Block social media"} {
		v, err := ParsePolicy(lines[i+1], name, "true", "false")
		if err != nil {
			return DNS{}, bad
		}
		dns.Blocks[i] = v
	}
	return dns, nil
}

func DNSArgs(dns DNS, index int, enabled bool) ([]string, error) {
	if dns.Mode != "default" || index < 0 || index >= len(dns.Blocks) {
		return nil, errors.New("DNS blockers require complete default DNS settings")
	}
	dns.Blocks[index] = enabled
	args := []string{"dns", "set", "default"}
	for i, on := range dns.Blocks {
		if on {
			args = append(args, dnsFlags[i])
		}
	}
	return args, nil
}

func ParseObscuraSettings(raw []byte) (Settings, error) {
	if _, err := ParseObscuraStatus(raw); err != nil {
		return Settings{}, err
	}
	var v struct {
		AutoConnect *bool            `json:"autoConnect"`
		LAN         *bool            `json:"localNetworkAccess"`
		System      *bool            `json:"useSystemDns"`
		Tailscale   *bool            `json:"tailscaleBypass"`
		WireGuard   *bool            `json:"wireguardBypass"`
		DNS         map[string]*bool `json:"dnsContentBlock"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Settings{}, errors.New("Obscura settings format changed")
	}
	s := Settings{AutoConnect: v.AutoConnect, LAN: v.LAN, Tailscale: v.Tailscale, WireGuard: v.WireGuard}
	if v.System != nil && len(v.DNS) > 0 {
		dns := DNS{Mode: "default"}
		if *v.System {
			dns.Mode = "system"
		}
		for i, key := range []string{"ad", "tracker", "malware", "adult", "gambling", "socialMedia"} {
			if v.DNS[key] == nil {
				return s, nil
			}
			dns.Blocks[i] = *v.DNS[key]
		}
		s.DNS = &dns
	}
	return s, nil
}

func (c CLI) Settings(ctx context.Context, p Provider) (Settings, error) {
	if p == Obscura {
		raw, err := c.run(ctx, p, readTimeout, readLimit, c.statusArgs(p)...)
		if err != nil {
			return Settings{}, err
		}
		return ParseObscuraSettings(raw)
	}
	if p != Mullvad {
		return Settings{}, errors.New("unknown VPN provider")
	}
	s := Settings{}
	for _, q := range []struct {
		cmd, name, on, off string
		target             **bool
	}{
		{"auto-connect", "Autoconnect", "on", "off", &s.AutoConnect},
		{"lan", "Local network sharing setting", "allow", "block", &s.LAN},
		{"lockdown-mode", "Block traffic when the VPN is disconnected", "on", "off", &s.Lockdown},
	} {
		raw, err := c.run(ctx, p, readTimeout, readLimit, q.cmd, "get")
		if err != nil {
			return Settings{}, err
		}
		value, err := ParsePolicy(string(raw), q.name, q.on, q.off)
		if err != nil {
			return Settings{}, err
		}
		*q.target = &value
	}
	raw, err := c.run(ctx, p, readTimeout, readLimit, "dns", "get")
	if err != nil {
		return Settings{}, err
	}
	dns, err := ParseMullvadDNS(string(raw))
	if err != nil {
		return Settings{}, err
	}
	s.DNS = &dns
	raw, err = c.run(ctx, p, readTimeout, readLimit, "relay", "get")
	if err == nil {
		switch {
		case strings.HasPrefix(string(raw), "Generic constraints"):
			s.Constraints = "Provider and multihop constraints stay unchanged."
			s.SavedLocation = ParseSavedLocation(string(raw))
		case strings.HasPrefix(string(raw), "Custom endpoint:"):
			s.Constraints = "Location changes replace the custom endpoint."
		default:
			s.Constraints = "Relay constraints format changed; check the app."
		}
	}
	return s, nil
}

var obscuraBlockFlags = [...]string{"--ads", "--trackers", "--malware", "--adult", "--gambling", "--social-media"}

func onOffArg(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// setObscura maps a setting onto the newer obscura CLI. Blockers are replaced
// together, so the current set is re-read first, as Mullvad's are.
func (c CLI) setObscura(ctx context.Context, key string, enabled bool) error {
	if !c.Obscura.Settings {
		return errors.New("this Obscura CLI cannot change settings")
	}
	var args []string
	switch {
	case key == "auto-connect":
		args = []string{"auto-connect", "set", onOffArg(enabled)}
	case key == "lan":
		args = []string{"lan", "set", map[bool]string{true: "allow", false: "block"}[enabled]}
	case strings.HasPrefix(key, "dns:"):
		index, err := strconv.Atoi(strings.TrimPrefix(key, "dns:"))
		if err != nil || index < 0 || index >= len(obscuraBlockFlags) {
			return errors.New("unknown DNS blocker")
		}
		s, err := c.Settings(ctx, Obscura)
		if err != nil {
			return err
		}
		if s.DNS == nil || s.DNS.Mode != "default" {
			return errors.New("DNS blockers need Obscura's own DNS")
		}
		blocks := s.DNS.Blocks
		blocks[index] = enabled
		args = []string{"dns", "block", "set"}
		for i, flag := range obscuraBlockFlags {
			args = append(args, flag, onOffArg(blocks[i]))
		}
	default:
		return errors.New("this Obscura setting has no CLI")
	}
	_, err := c.run(ctx, Obscura, mutationTimeout, readLimit, args...)
	return err
}

func (c CLI) Set(ctx context.Context, p Provider, key string, enabled bool) error {
	if p == Obscura {
		return c.setObscura(ctx, key, enabled)
	}
	if p != Mullvad {
		return errors.New("unknown VPN provider")
	}
	var args []string
	onoff := "off"
	if enabled {
		onoff = "on"
	}
	switch key {
	case "auto-connect", "lockdown-mode":
		args = []string{key, "set", onoff}
	case "lan":
		value := "block"
		if enabled {
			value = "allow"
		}
		args = []string{"lan", "set", value}
	default:
		if !strings.HasPrefix(key, "dns:") {
			return errors.New("unknown VPN setting")
		}
		index, err := strconv.Atoi(strings.TrimPrefix(key, "dns:"))
		if err != nil || index < 0 || index >= 6 {
			return errors.New("unknown DNS blocker")
		}
		raw, err := c.run(ctx, p, readTimeout, readLimit, "dns", "get")
		if err != nil {
			return err
		}
		dns, err := ParseMullvadDNS(string(raw))
		if err != nil {
			return err
		}
		// ponytail: the CLI cannot atomically patch a DNS bit. Re-read before
		// replacing the full set; use an upstream atomic setter if one ships.
		args, err = DNSArgs(dns, index, enabled)
		if err != nil {
			return err
		}
	}
	_, err := c.run(ctx, p, mutationTimeout, readLimit, args...)
	return err
}

func SettingValue(s Settings, key string) (*bool, error) {
	switch key {
	case "auto-connect":
		return s.AutoConnect, nil
	case "lan":
		return s.LAN, nil
	case "lockdown-mode":
		return s.Lockdown, nil
	}
	if strings.HasPrefix(key, "dns:") && s.DNS != nil && s.DNS.Mode == "default" {
		index, err := strconv.Atoi(strings.TrimPrefix(key, "dns:"))
		if err == nil && index >= 0 && index < 6 {
			return &s.DNS.Blocks[index], nil
		}
	}
	return nil, fmt.Errorf("setting is unavailable")
}
