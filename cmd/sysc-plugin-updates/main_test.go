package main

import (
	"errors"
	"testing"
)

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
