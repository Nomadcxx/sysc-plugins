// Package hermes is the sysc-shell Hermes Agent plugin: a client for the
// Hermes bridge, in Local mode (a bridge the plugin starts or adopts on this
// machine) or Client-only mode (a bridge somewhere else).
package hermes

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Local-mode bridge address and the default paths from the manifest.
const (
	LocalHost        = "127.0.0.1"
	LocalPort        = 19777
	DefaultStateFile = "~/.cache/sysc-hermes/state.json"
	DefaultTokenFile = "~/.config/sysc-shell/hermes.token"
)

// MaxRequestBytes is the bridge's own request cap (MAX_BODY_BYTES). The
// client refuses a larger encoded body before any network I/O.
const MaxRequestBytes = 1_048_576

// Response caps are the plugin's own; the bridge does not cap responses.
const (
	stateResponseCap    = 4 << 20
	sessionsResponseCap = 1 << 20
	errorResponseCap    = 64 << 10
)

// Per-call timeouts, from the design's bridge contract.
const (
	stateTimeout    = 10 * time.Second
	actionTimeout   = 30 * time.Second
	sessionsTimeout = 15 * time.Second
	gatewayTimeout  = 40 * time.Second
	oneShotTimeout  = 11 * time.Minute
)

// timeoutFor is the deadline one request to path gets.
func timeoutFor(path string) time.Duration {
	switch path {
	case "/state", "/health", "/detect":
		return stateTimeout
	case "/sessions":
		return sessionsTimeout
	case "/gateway/start", "/gateway/stop":
		return gatewayTimeout
	case "/oneshot":
		return oneShotTimeout
	default:
		return actionTimeout
	}
}

// responseCapFor is the most bytes the client reads from a reply to path.
func responseCapFor(path string) int64 {
	if path == "/sessions" {
		return sessionsResponseCap
	}
	return stateResponseCap
}

// ExpandPath expands a leading "~" or "~/" to the user's home directory. It is
// shared by every path setting. Other paths are returned unchanged.
func ExpandPath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// CacheHome is $XDG_CACHE_HOME when it is an absolute path, else ~/.cache.
func CacheHome() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	return ExpandPath("~/.cache")
}

// IsLoopback reports whether host names this machine. It only decides
// whether to warn that bridge traffic is cleartext; it never refuses a host.
func IsLoopback(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidModelName reports whether s may be sent as a model or provider name:
// non-empty, no whitespace, and not starting with "-", so it cannot inject a
// flag into the bridge's config.set value.
func ValidModelName(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
