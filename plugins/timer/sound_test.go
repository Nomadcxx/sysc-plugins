package timer

import "testing"

// The two transition sounds have to exist in the theme, or the phase change
// is silent and the setting that promises a sound is a lie.
func TestTransitionSoundsExist(t *testing.T) {
	for _, sound := range []string{SoundWorkDone, SoundBreakDone} {
		if got := SoundFile(sound); got == "" {
			t.Errorf("sound %q is not in the theme at %s", sound, soundDir)
		}
	}
}

func TestSoundFileRejectsAnUnknownName(t *testing.T) {
	if got := SoundFile("no-such-sound-here"); got != "" {
		t.Errorf("SoundFile = %q, want empty for a name the theme lacks", got)
	}
}

// Play must be safe when the desktop has no player and when the name is
// not in the theme: a session keeps time either way.
func TestPlayIsSafeWithoutASound(t *testing.T) {
	Play("no-such-sound-here")
}
