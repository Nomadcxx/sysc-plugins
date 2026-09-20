package timer

import (
	"os"
	"os/exec"
	"sync"
)

// Sound theme names for the two transitions worth hearing.
const (
	SoundWorkDone  = "alarm-clock-elapsed"
	SoundBreakDone = "complete"
)

// soundDir holds the freedesktop sound theme every desktop here ships.
const soundDir = "/usr/share/sounds/freedesktop/stereo/"

// player resolves once to a command that plays one file, or to nothing.
//
// These are the plain pipewire and pulse players rather than the canberra
// one that names theme sounds. canberra refuses with "Sound disabled" when
// the desktop turns event sounds off, and that setting is about interface
// clicks, not about an alarm the user asked this plugin to raise -- the
// switch for this is the plugin's own sound setting.
//
// Nothing is a real answer: a desktop with no player still keeps time, and
// a missing beep is not worth refusing to run a session over, which is why
// no player appears in the manifest's required commands.
var player = sync.OnceValue(func() func(string) *exec.Cmd {
	for _, name := range []string{"pw-play", "paplay"} {
		if bin, err := exec.LookPath(name); err == nil {
			return func(file string) *exec.Cmd { return exec.Command(bin, file) }
		}
	}
	return nil
})

// SoundFile is the theme file for a transition, or "" when it is missing.
func SoundFile(sound string) string {
	path := soundDir + sound + ".oga"
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// Play sounds one transition without blocking the countdown.
//
// The caller is the tick handler, which owes the next frame to the bar, so
// this starts the process and reaps it on its own goroutine rather than
// waiting on it. Failures are silent on purpose: there is nothing the user
// could do about one mid-session, and the notification still arrives.
func Play(sound string) {
	build := player()
	if build == nil {
		return
	}
	file := SoundFile(sound)
	if file == "" {
		return
	}
	cmd := build(file)
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}
