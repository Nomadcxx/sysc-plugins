package protonvpn

import (
	"testing"

	lint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestAccountSignedIn(t *testing.T) {
	root := AccountTree(AccountState{SignedIn: true, Snap: Snapshot{
		Info:      Info{Username: "jane@example.com"},
		Status:    Status{Protocol: "wireguard"},
		Interface: "proton0",
	}})
	assertTextContains(t, root, "jane@example.com")
	assertTextContains(t, root, "wireguard")
	assertTextContains(t, root, "proton0")
	findNode(t, root, "signout")
	findNode(t, root, "refresh")
	if lookupNode(root, "signin-user") != nil {
		t.Fatal("signed-in view must not carry the sign-in controls")
	}
}

func TestAccountSignedOut(t *testing.T) {
	root := AccountTree(AccountState{UserReseed: 1})
	user := findNode(t, root, "signin-user")
	if user.Reseed != 1 {
		t.Fatalf("reseed %d", user.Reseed)
	}
	btn := findNode(t, root, "signin")
	if !btn.Disabled {
		t.Fatal("sign in must be disabled with an empty draft")
	}
	assertTextContains(t, root, "Complete sign-in in the terminal (password + 2FA)")
}

func TestAccountSignInDraftEnables(t *testing.T) {
	root := AccountTree(AccountState{UserDraft: "jane"})
	if btn := findNode(t, root, "signin"); btn.Disabled {
		t.Fatal("sign in must enable once a username is drafted")
	}
}

func TestAccountOptionsSection(t *testing.T) {
	root := AccountTree(AccountState{SignedIn: true})
	assertTextContains(t, root, "Options")
	assertTextContains(t, root, "Change in shell settings")
}

func TestAccountSettingsRows(t *testing.T) {
	root := AccountTree(AccountState{SignedIn: true, Settings: map[string]string{
		"refresh_seconds": "5",
		"bar_mode":        "code",
	}})
	assertTextContains(t, root, "Status refresh (seconds)")
	assertTextContains(t, root, "5")
	assertTextContains(t, root, "Bar shows")
	assertTextContains(t, root, "code")
	// Settings the map omits still render, with the em-dash placeholder.
	assertTextContains(t, root, "Show live traffic")
}

func TestAccountErrorLine(t *testing.T) {
	root := AccountTree(AccountState{Err: "sign-in failed"})
	assertTextContains(t, root, "sign-in failed")
}

func TestAccountLint(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		for _, phase := range []Phase{PhaseDisconnected, PhaseConnected, PhaseError} {
			root := AccountTree(AccountState{SignedIn: signedIn, Snap: Snapshot{
				Phase:     phase,
				Info:      Info{Username: "jane@example.com"},
				Status:    Status{Protocol: "wireguard"},
				Interface: "proton0",
			}})
			if findings := lint.Tree(root, v1.ViewPanel, 460, 404); len(findings) > 0 {
				t.Fatalf("signedIn %v phase %v: %v", signedIn, phase, findings)
			}
		}
	}
}
