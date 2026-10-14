// Package terminal resolves the program a plugin should use to show an
// interactive command. The shell does not hand plugins a terminal of its
// own, so a plugin that wants the user to watch a command run picks from
// this list.
package terminal

import (
	"fmt"
	"strings"
)

// Candidate is one terminal to try: the program and the arguments it wants
// before the command line.
type Candidate struct {
	Bin    string
	Prefix []string
}

// Defaults are tried in order when the user has not named a terminal.
var Defaults = []Candidate{
	{Bin: "xdg-terminal-exec"},
	{Bin: "kitty", Prefix: []string{"-e"}},
	{Bin: "foot", Prefix: []string{"-e"}},
	{Bin: "alacritty", Prefix: []string{"-e"}},
	{Bin: "wezterm", Prefix: []string{"start", "--"}},
	{Bin: "ghostty", Prefix: []string{"-e"}},
	{Bin: "gnome-terminal", Prefix: []string{"--"}},
	{Bin: "konsole", Prefix: []string{"-e"}},
	{Bin: "xterm", Prefix: []string{"-e"}},
}

// Resolve picks the terminal to run a command in. A non-empty preferred
// name wins when it exists on PATH; when it names one of the defaults, that
// default's prefix comes with it. Otherwise the first default present wins.
// lookPath is injected so tests do not need a PATH.
func Resolve(preferred string, lookPath func(string) (string, error)) (bin string, prefix []string, err error) {
	if preferred != "" {
		if found, err := lookPath(preferred); err == nil {
			return found, prefixFor(preferred), nil
		}
	}
	for _, candidate := range Defaults {
		if found, err := lookPath(candidate.Bin); err == nil {
			return found, append([]string(nil), candidate.Prefix...), nil
		}
	}
	return "", nil, fmt.Errorf("no terminal found (tried %s)", strings.Join(names(), ", "))
}

func prefixFor(bin string) []string {
	for _, candidate := range Defaults {
		if candidate.Bin == bin {
			return append([]string(nil), candidate.Prefix...)
		}
	}
	return nil
}

func names() []string {
	out := make([]string, 0, len(Defaults))
	for _, candidate := range Defaults {
		out = append(out, candidate.Bin)
	}
	return out
}
