package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/httpapi"
)

// Config is ~/.config/bitsub/bitsubd.json. It holds token hashes, so it
// must not be readable by group or others.
type Config struct {
	Listen        []string        `json:"listen"`
	AllowLAN      bool            `json:"allow_lan,omitempty"`
	DataDir       string          `json:"data_dir"`
	GogBin        string          `json:"gog_bin"`
	GogAccount    string          `json:"gog_account"`
	EmailTo       string          `json:"email_to"`
	ReminderHour  int             `json:"reminder_hour"`
	DigestWeekday string          `json:"digest_weekday"`
	CalendarName  string          `json:"calendar_name"`
	FXURL         string          `json:"fx_url"`
	Tokens        []httpapi.Token `json:"tokens"`
}

func Defaults() Config {
	return Config{
		Listen: []string{"127.0.0.1:7733"}, DataDir: "~/.local/share/bitsub", GogBin: "gog",
		ReminderHour: 9, DigestWeekday: "Monday", CalendarName: "Subscriptions", Tokens: []httpapi.Token{},
	}
}

func DefaultConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "bitsub", "bitsubd.json")
}

var (
	tokenName = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	// tailnet is Tailscale's CGNAT range.
	tailnet = netip.MustParsePrefix("100.64.0.0/10")
)

// LoadConfig reads path, refusing group- or world-readable files, and fills
// unset fields with Defaults.
func LoadConfig(path string) (Config, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Config{}, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("%s is readable by group or others (mode %v); chmod 600 it", path, fi.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := Defaults()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if len(c.Listen) == 0 {
		return errors.New("listen: at least one address")
	}
	for _, addr := range c.Listen {
		if err := checkListen(addr, c.AllowLAN); err != nil {
			return err
		}
	}
	if c.ReminderHour < 0 || c.ReminderHour > 23 {
		return errors.New("reminder_hour: 0–23")
	}
	if _, err := parseWeekday(c.DigestWeekday); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, t := range c.Tokens {
		if !tokenName.MatchString(t.Name) || seen[t.Name] {
			return fmt.Errorf("tokens: bad or repeated name %q", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

// checkListen allows loopback and Tailscale addresses; anything else needs
// allow_lan, because the LAN would carry bearer tokens in plain text.
func checkListen(addr string, allowLAN bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen %q: %w", addr, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("listen %q: use an IP address, not a host name", addr)
	}
	switch {
	case ip.IsLoopback(), tailnet.Contains(ip):
		return nil
	case allowLAN && !ip.IsUnspecified():
		return nil
	}
	return fmt.Errorf("listen %q: only loopback or Tailscale (100.64.0.0/10) addresses unless allow_lan is set; never 0.0.0.0", addr)
}

func parseWeekday(s string) (time.Weekday, error) {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if strings.EqualFold(d.String(), s) {
			return d, nil
		}
	}
	return 0, fmt.Errorf("digest_weekday: %q is not a weekday name", s)
}

// SaveConfig writes cfg atomically with mode 0600.
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bitsubd-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}
