package hermes

import (
	"path/filepath"
	"testing"
	"time"
)

func TestExpandPathResolvesHome(t *testing.T) {
	t.Setenv("HOME", "/home/fixture")
	for _, tc := range []struct{ in, want string }{
		{"~/.cache/sysc-hermes/state.json", "/home/fixture/.cache/sysc-hermes/state.json"},
		{"~", "/home/fixture"},
		{"/etc/hermes", "/etc/hermes"},
		{"relative/x", "relative/x"},
		{"~other/x", "~other/x"},
		{"", ""},
	} {
		if got := ExpandPath(tc.in); got != tc.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsLoopbackOnlyForLocalHosts(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true}, {"::1", true}, {"localhost", true}, {"LOCALHOST", true}, {"127.1.2.3", true},
		{"192.168.0.10", false}, {"example.com", false}, {"10.0.0.1", false}, {"localhost.example.com", false},
	} {
		if got := IsLoopback(tc.host); got != tc.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestValidModelName(t *testing.T) {
	for _, bad := range []string{
		"gpt 4", "-x", "a\tb", "", "--provider", "a\nb", "a\rb",
		" ", "a b", "-rm", "--global", "tab\tname", "new\nline",
	} {
		if ValidModelName(bad) {
			t.Errorf("ValidModelName(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"openai/gpt-5.1", "claude-sonnet-4", "x", "gpt-x", "anthropic/claude-x", "ok-"} {
		if !ValidModelName(good) {
			t.Errorf("ValidModelName(%q) = false, want true", good)
		}
	}
}

func TestCallTimeoutsAndCaps(t *testing.T) {
	for _, tc := range []struct {
		path string
		want time.Duration
	}{
		{"/state", 10 * time.Second}, {"/health", 10 * time.Second}, {"/detect", 10 * time.Second},
		{"/sessions", 15 * time.Second},
		{"/prompt", 30 * time.Second}, {"/approval", 30 * time.Second}, {"/refresh", 30 * time.Second},
		{"/gateway/start", 40 * time.Second}, {"/gateway/stop", 40 * time.Second},
		{"/oneshot", 11 * time.Minute},
	} {
		if got := timeoutFor(tc.path); got != tc.want {
			t.Errorf("timeoutFor(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	if responseCapFor("/state") != 4<<20 || responseCapFor("/health") != 4<<20 || responseCapFor("/prompt") != 4<<20 {
		t.Error("state, health and action responses must cap at 4 MiB")
	}
	if responseCapFor("/sessions") != 1<<20 {
		t.Error("sessions responses must cap at 1 MiB")
	}
	if MaxRequestBytes != 1_048_576 {
		t.Errorf("MaxRequestBytes = %d, want the bridge's 1048576", MaxRequestBytes)
	}
}

func TestCacheHomeFollowsXDG(t *testing.T) {
	t.Setenv("HOME", "/home/fixture")
	t.Setenv("XDG_CACHE_HOME", "")
	if got := CacheHome(); got != filepath.Join("/home/fixture", ".cache") {
		t.Errorf("CacheHome() without XDG = %q", got)
	}
	t.Setenv("XDG_CACHE_HOME", "/var/cache/fixture")
	if got := CacheHome(); got != "/var/cache/fixture" {
		t.Errorf("CacheHome() with XDG = %q", got)
	}
	t.Setenv("XDG_CACHE_HOME", "relative")
	if got := CacheHome(); got != filepath.Join("/home/fixture", ".cache") {
		t.Errorf("CacheHome() with relative XDG = %q, want the spec fallback", got)
	}
}
