package main

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForTerminalChecksAfterExitFollowingLogChange(t *testing.T) {
	done := make(chan struct{})
	ticks := make(chan time.Time, 1)
	exited := make(chan struct{})
	var reads atomic.Int32
	var updates atomic.Int32
	mtime := func() time.Time {
		if reads.Add(1) == 1 {
			return time.Unix(1, 0)
		}
		return time.Unix(2, 0)
	}
	wait := func(time.Duration) {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	go waitForTerminalWith(done, mtime, ticks, wait, func() { updates.Add(1) }, func() { close(exited) })
	ticks <- time.Now()
	<-exited
	if got := updates.Load(); got != 2 {
		t.Fatalf("refreshes = %d, want a post-exit refresh after the earlier log-triggered check", got)
	}
}

func TestQueueOneCheckRunsOneFollowUpAfterAnActiveCheck(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	followUp := make(chan struct{})
	var calls atomic.Int32
	request := queueOneCheck(func() {
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
		case 2:
			close(followUp)
		}
	})
	request()
	<-started
	request()
	close(release)
	select {
	case <-followUp:
	case <-time.After(time.Second):
		t.Fatal("a refresh requested during an active check was lost")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("check calls = %d, want 2", got)
	}
}

func TestUpdateCommandChoice(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()

	both := func(name string) (string, error) {
		if name == "paru" || name == "yay" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	none := func(string) (string, error) { return "", errors.New("not found") }

	cases := []struct {
		name string
		cfg  settings
		look func(string) (string, error)
		want string
	}{
		{"override wins", settings{updateCommand: "topgrade"}, both, "topgrade"},
		{"helper preferred", settings{aurHelper: "auto"}, both, "paru -Syu"},
		{"fallback to pacman", settings{aurHelper: "off"}, both, "sudo pacman -Syu"},
		{"no helper installed", settings{aurHelper: "auto"}, none, "sudo pacman -Syu"},
		{"explicit missing helper", settings{aurHelper: "yay"}, none, "sudo pacman -Syu"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lookPath = c.look
			if got := updateCommand(c.cfg); got != c.want {
				t.Fatalf("updateCommand = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDefaultSettings(t *testing.T) {
	cfg := defaultSettings()
	if cfg.intervalHours != 3 || cfg.aurHelper != "auto" || !cfg.includeFlatpak || !cfg.hideWhenZero || cfg.notify {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.updateMethod != "terminal" {
		t.Fatalf("updateMethod = %q, want terminal", cfg.updateMethod)
	}
}
