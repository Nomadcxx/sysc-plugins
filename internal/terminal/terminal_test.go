package terminal

import (
	"errors"
	"strings"
	"testing"
)

func only(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, want := range names {
			if name == want {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestResolvePreferredWinsWithDefaultPrefix(t *testing.T) {
	bin, prefix, err := Resolve("kitty", only("kitty", "xterm"))
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/kitty" {
		t.Fatalf("bin = %q, want /usr/bin/kitty", bin)
	}
	if len(prefix) != 1 || prefix[0] != "-e" {
		t.Fatalf("prefix = %v, want [-e]", prefix)
	}
}

func TestResolvePreferredNotADefaultHasNoPrefix(t *testing.T) {
	bin, prefix, err := Resolve("myterm", only("myterm"))
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/myterm" || len(prefix) != 0 {
		t.Fatalf("got %q %v, want /usr/bin/myterm with no prefix", bin, prefix)
	}
}

func TestResolveFallsBackInDefaultOrder(t *testing.T) {
	bin, prefix, err := Resolve("", only("foot", "xterm"))
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/foot" || len(prefix) != 1 || prefix[0] != "-e" {
		t.Fatalf("got %q %v, want /usr/bin/foot with [-e]", bin, prefix)
	}
}

func TestResolvePreferredMissingFallsBack(t *testing.T) {
	bin, _, err := Resolve("kitty", only("xterm"))
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/xterm" {
		t.Fatalf("bin = %q, want /usr/bin/xterm", bin)
	}
}

func TestResolveWeztermPrefix(t *testing.T) {
	bin, prefix, err := Resolve("wezterm", only("wezterm"))
	if err != nil {
		t.Fatal(err)
	}
	if bin != "/usr/bin/wezterm" || len(prefix) != 2 || prefix[0] != "start" || prefix[1] != "--" {
		t.Fatalf("got %q %v, want /usr/bin/wezterm with [start --]", bin, prefix)
	}
}

func TestResolveNoneFound(t *testing.T) {
	_, _, err := Resolve("kitty", only())
	if err == nil {
		t.Fatal("want error when no terminal exists")
	}
	if !strings.Contains(err.Error(), "kitty") {
		t.Fatalf("error %q should mention what was tried", err)
	}
}
