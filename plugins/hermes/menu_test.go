package hermes

import (
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-shell/plugin/icons"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// menuSize is the manifest's declared menu panel.
var menuSize = [2]int{260, 300}

func TestMenuItemsFollowNoctaliaOrder(t *testing.T) {
	full := []MenuItem{
		{"open", "Open Hermes", menuIconOpen},
		{"new-session", "New session", menuIconNewSession},
		{"interrupt", "Interrupt", menuIconInterrupt},
		{"refresh", "Refresh", menuIconRefresh},
		{"start-gateway", "Start gateway", menuIconStartGateway},
		{"settings", "Hermes Settings", menuIconSettings},
	}
	withoutGateway := []MenuItem{full[0], full[1], full[2], full[3], full[5]}
	if len(Statuses) != 7 {
		t.Fatalf("Statuses has %d entries, want seven", len(Statuses))
	}
	for _, s := range Statuses {
		for _, clientOnly := range []bool{false, true} {
			want := withoutGateway
			if CanStartGateway(s, clientOnly) {
				want = full
			}
			got := MenuItems(s, clientOnly)
			if len(got) != len(want) {
				t.Errorf("%s clientOnly=%v: items = %v, want %v", s, clientOnly, got, want)
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("%s clientOnly=%v: item %d = %v, want %v", s, clientOnly, i, got[i], want[i])
				}
			}
		}
	}
	if len(MenuItems(StatusOffline, false)) != 6 {
		t.Error("local offline menu should offer Start gateway")
	}
	if len(MenuItems(StatusOffline, true)) != 5 {
		t.Error("client-only menu must not offer Start gateway")
	}
}

func TestMenuNeverOffersStop(t *testing.T) {
	for _, s := range Statuses {
		for _, clientOnly := range []bool{false, true} {
			items := MenuItems(s, clientOnly)
			if len(items) == 0 {
				t.Errorf("%s clientOnly=%v: menu is empty", s, clientOnly)
			}
			for _, it := range items {
				if strings.Contains(strings.ToLower(it.ID+" "+it.Label), "stop") {
					t.Errorf("%s clientOnly=%v offers %v", s, clientOnly, it)
				}
			}
		}
	}
}

func TestMenuLaysOutAndMatchesItems(t *testing.T) {
	actions := manifestActions(t)
	for _, s := range Statuses {
		for _, clientOnly := range []bool{false, true} {
			name := string(s)
			if clientOnly {
				name += " client-only"
			}
			tree := MenuTree(s, clientOnly, menuSize[0], menuSize[1])
			lintView(t, name+" menu", tree, v1.ViewPanel, menuSize[0], menuSize[1])
			checkBudget(t, name+" menu", tree, menuSize[0], menuSize[1])
			checkGlyphs(t, name+" menu", tree)
			items := MenuItems(s, clientOnly)
			if len(tree.Children) != len(items) {
				t.Fatalf("%s: menu has %d rows, want %d", name, len(tree.Children), len(items))
			}
			for i, it := range items {
				row := tree.Children[i]
				if row.Kind != v1.KindButton || row.ID != it.ID || row.Text != it.Label || row.Icon != it.Icon {
					t.Errorf("%s: row %d = %s %q %q %q, want %v", name, i, row.Kind, row.ID, row.Text, row.Icon, it)
				}
				if row.Role != "button" || row.Name == "" || len(row.Events) != 1 || row.Events[0] != v1.EventActivate {
					t.Errorf("%s: row %d is not an accessible activate button: %+v", name, i, row)
				}
				if !actions[it.ID] {
					t.Errorf("%s: menu action %q is not a manifest service action", name, it.ID)
				}
				if !icons.ValidName(it.Icon) {
					t.Errorf("%s: menu icon %q is not in the pinned catalogue", name, it.Icon)
				}
			}
		}
	}
}
