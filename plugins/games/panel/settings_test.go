package panel

import (
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// The settings header is the plugin half of the host's settings panel (the
// manifest declares include_settings); the host wraps it in a card inside a
// scroll, so it is linted at the declared box and at a much narrower one.
func TestSettingsHeaderFits(t *testing.T) {
	root := SettingsHeader()
	if err := v1.Validate(root, v1.ViewPanel); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, box := range [][2]int{{720, 720}, {420, 220}} {
		if f := shelllint.Tree(root, v1.ViewPanel, box[0], box[1]); len(f) != 0 {
			t.Errorf("settings header at %dx%d: %v", box[0], box[1], f)
		}
	}
	if find(t, root, "back") == nil {
		t.Fatal("the settings header needs the back button")
	}
	if find(t, root, "settings") != nil {
		t.Fatal("the header is not the settings form; the host renders that")
	}
}

// The deck labels each card with its library only when libraries are merged:
// with one library the runner/platform are the whole story.
func TestLibraryBadgeOnlyWhenMerged(t *testing.T) {
	g := games()[0]
	g.Source = "lutris"
	s := baseState()
	if got := badges(s, g); contains(got, "Lutris") {
		t.Fatalf("single library deck labels the card: %v", got)
	}
	s.Libraries = []string{"lutris", "faugus"}
	if got := badges(s, g); !contains(got, "Lutris") {
		t.Fatalf("merged deck must label the card with its library: %v", got)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
