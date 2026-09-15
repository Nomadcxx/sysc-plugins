package v1

import (
	"strings"
	"testing"
)

func TestValidPluginID(t *testing.T) {
	t.Parallel()

	good := []string{"org.sysc.timer", "a.b", "org.example.world-clock", "n0.p1"}
	for _, s := range good {
		if !ValidPluginID(s) {
			t.Errorf("ValidPluginID(%q) = false, want true", s)
		}
	}

	bad := []string{
		"", "timer", "Org.Sysc.Timer", "org..sysc", ".org.sysc", "org.sysc.",
		"org sysc.timer", "org/sysc", "../escape", "org.sysc.timer/../x",
		"org." + strings.Repeat("a", 128),
	}
	for _, s := range bad {
		if ValidPluginID(s) {
			t.Errorf("ValidPluginID(%q) = true, want false", s)
		}
	}
}

func TestValidEntryID(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"bar", "panel", "timer-1", "warn_at", "a0"} {
		if !ValidEntryID(s) {
			t.Errorf("ValidEntryID(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "Bar", "-lead", "has space", "a.b", "a/b", "..", strings.Repeat("a", 129)} {
		if ValidEntryID(s) {
			t.Errorf("ValidEntryID(%q) = true, want false", s)
		}
	}
}
