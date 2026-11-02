package lidguard

import (
	"context"
	"strings"
	"testing"

	shelllint "github.com/Nomadcxx/sysc-shell/plugin/lint"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func findNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func findIcon(root *v1.Node, icon string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Icon == icon {
		return root
	}
	for _, child := range root.Children {
		if found := findIcon(child, icon); found != nil {
			return found
		}
	}
	return nil
}

func findTextNode(root *v1.Node, text string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.Text == text {
		return root
	}
	for _, child := range root.Children {
		if found := findTextNode(child, text); found != nil {
			return found
		}
	}
	return nil
}

func flatten(root *v1.Node) string {
	if root == nil {
		return ""
	}
	parts := []string{root.Text, root.Name}
	for _, child := range root.Children {
		parts = append(parts, flatten(child))
	}
	return strings.Join(parts, " ")
}

func TestViewsValidateAndFitShellGeometry(t *testing.T) {
	states := []Snapshot{
		{State: v1.ServiceStateUnknown, Available: true},
		{State: v1.ServiceStateInactive, Available: true},
		{State: v1.ServiceStateActive, Available: true},
		{State: v1.ServiceStateUnavailable, Message: "systemd is unavailable"},
		{State: v1.ServiceStateError, Available: true, Message: "systemd returned an error"},
		{State: v1.ServiceStateActive, Available: true, Busy: true},
	}
	for _, state := range states {
		for _, width := range []int{shelllint.BarWidth, 32, 64, 120, 240} {
			bar := BarTreeAtWidth(state, width)
			for _, finding := range shelllint.Tree(bar, v1.ViewBar, width, shelllint.BarHeight) {
				t.Errorf("state %+v bar width %d: %s", state, width, finding)
			}
		}
		for _, check := range []struct {
			name string
			root *v1.Node
			kind v1.ViewKind
			w, h int
		}{
			{"tooltip", TooltipTree(state), v1.ViewTooltip, shelllint.TooltipWidth, shelllint.TooltipHeight},
			{"panel", PanelTree(state), v1.ViewPanel, PanelWidth, PanelHeight},
		} {
			for _, finding := range shelllint.Tree(check.root, check.kind, check.w, check.h) {
				t.Errorf("state %+v %s: %s", state, check.name, finding)
			}
		}
	}
}

func TestBarNamesTheNextActionAndCurrentState(t *testing.T) {
	for _, tc := range []struct {
		state v1.ServiceState
		busy  bool
		name  string
		text  string
	}{
		{v1.ServiceStateInactive, false, "Enable Lid Guard", "Off"},
		{v1.ServiceStateActive, false, "Disable Lid Guard", "On"},
		{v1.ServiceStateActive, true, "Lid Guard is changing", "Working"},
		{v1.ServiceStateUnavailable, false, "Lid Guard unavailable; open details", "Unavailable"},
		{v1.ServiceStateError, false, "Lid Guard needs attention; open details", "Error"},
	} {
		bar := BarTreeAtWidth(Snapshot{State: tc.state, Available: tc.state != v1.ServiceStateUnavailable, Busy: tc.busy}, 240)
		toggle := findNode(bar, nodeToggle)
		if toggle == nil || toggle.Name != tc.name || toggle.Text != tc.text {
			t.Errorf("state %s busy=%t toggle = %+v, want name %q and text %q", tc.state, tc.busy, toggle, tc.name, tc.text)
		}
		if tc.busy {
			if toggle == nil || !toggle.Disabled {
				t.Errorf("state %s busy=%t toggle should be disabled: %+v", tc.state, tc.busy, toggle)
			}
		} else if tc.state == v1.ServiceStateUnavailable && toggle != nil && toggle.Disabled {
			t.Errorf("unavailable bar button should stay available for details: %+v", toggle)
		}
	}
}

func TestBarKeepsConfirmedStateDuringRefresh(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case release <- struct{}{}:
		default:
		}
	})
	service := New(testOptions(func(context.Context, string, ...string) (string, string, error) {
		close(entered)
		<-release
		return "inactive\n", "", nil
	}))
	service.status.State = v1.ServiceStateInactive
	if err := service.Request(context.Background(), "refresh"); err != nil {
		t.Fatalf("Request(refresh): %v", err)
	}
	<-entered
	snapshot := service.Snapshot()
	if snapshot.Busy {
		t.Fatal("read-only refresh was exposed as a changing service action")
	}
	bar := BarTreeAtWidth(snapshot, 240)
	toggle := findNode(bar, nodeToggle)
	if toggle == nil {
		t.Fatal("bar toggle missing")
	}
	if toggle.Text != "Off" || toggle.Tone != v1.ToneSubtle || toggle.Disabled {
		t.Fatalf("refresh presented as %q with tone %q; want confirmed Off state", toggle.Text, toggle.Tone)
	}
	release <- struct{}{}
	waitSnapshot(t, service, func(got Snapshot) bool { return !got.Busy && !got.Refreshing })
}

func TestRefreshDoesNotBlockOrRepeatToggleFromViews(t *testing.T) {
	snapshot := Snapshot{State: v1.ServiceStateInactive, Available: true, Refreshing: true}
	for _, node := range []string{nodeToggle, nodePanelAction} {
		action, open := HandleInput(&v1.InputEvent{Node: node, Event: v1.EventActivate}, snapshot)
		if action != "toggle" || open {
			t.Errorf("activation of %s during refresh = (%q,%t), want queued toggle", node, action, open)
		}
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeRefresh, Event: v1.EventActivate}, snapshot); action != "" || open {
		t.Errorf("panel refresh during refresh = (%q,%t), want no duplicate request", action, open)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventPointer, Button: v1.ButtonSecondary}, snapshot); action != "" || open {
		t.Errorf("bar refresh during refresh = (%q,%t), want no duplicate request", action, open)
	}
	refresh := findNode(PanelTree(snapshot), nodeRefresh)
	if refresh == nil || !refresh.Disabled {
		t.Fatalf("panel refresh control = %+v, want disabled while a refresh runs", refresh)
	}
}

func TestHandleInputPreservesBarParityAndOffersDetails(t *testing.T) {
	available := Snapshot{State: v1.ServiceStateInactive, Available: true}
	for _, tc := range []struct {
		event  v1.EventKind
		button v1.PointerButton
		want   string
		open   bool
	}{
		{v1.EventActivate, "", "enable", false},
		{v1.EventPointer, v1.ButtonSecondary, "refresh", false},
		{v1.EventPointer, v1.ButtonMiddle, "", true},
	} {
		action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: tc.event, Button: tc.button}, available)
		if action != tc.want || open != tc.open {
			t.Errorf("HandleInput(%s,%s) = (%q,%t), want (%q,%t)", tc.event, tc.button, action, open, tc.want, tc.open)
		}
	}
	for _, state := range []Snapshot{
		{State: v1.ServiceStateActive, Available: true, Busy: true},
	} {
		action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, state)
		if action != "" || open {
			t.Errorf("disabled state %+v produced action %q, open=%t", state, action, open)
		}
	}
}

func TestUnavailableBarOpensRecoveryPanelAndOffersRefresh(t *testing.T) {
	snapshot := Snapshot{
		State:     v1.ServiceStateUnavailable,
		Message:   "Missing required commands: systemd-run",
		Available: false,
	}
	toggle := findNode(BarTreeAtWidth(snapshot, 240), nodeToggle)
	if toggle == nil || toggle.Disabled {
		t.Fatalf("unavailable bar button = %+v, want an enabled details control", toggle)
	}
	if toggle.Name != "Lid Guard unavailable; open details" {
		t.Fatalf("unavailable bar name = %q", toggle.Name)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, snapshot); action != "" || !open {
		t.Fatalf("primary activation = (%q,%t), want details panel", action, open)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventPointer, Button: v1.ButtonSecondary}, snapshot); action != "refresh" || open {
		t.Fatalf("secondary click = (%q,%t), want refresh", action, open)
	}
	tooltip := flatten(TooltipTree(snapshot))
	for _, want := range []string{"Unavailable", "Click: details", "Right-click: refresh", "Middle-click: details", "Missing required commands: systemd-run"} {
		if !strings.Contains(tooltip, want) {
			t.Errorf("unavailable tooltip missing %q: %s", want, tooltip)
		}
	}
}

func TestUnknownStateKeepsDetailsAndRefreshWithoutOfferingToggle(t *testing.T) {
	snapshot := Snapshot{
		State: v1.ServiceStateUnknown, Available: true,
		Message: "Lid Guard is changing state; wait for systemd to settle before changing it.",
	}
	bar := findNode(BarTreeAtWidth(snapshot, 240), nodeToggle)
	if bar == nil || bar.Disabled || !strings.Contains(bar.Name, "open details") {
		t.Fatalf("unknown-state bar button = %+v, want enabled details route", bar)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, snapshot); action != "" || !open {
		t.Fatalf("unknown-state primary activation = (%q,%t), want details panel", action, open)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventPointer, Button: v1.ButtonSecondary}, snapshot); action != "refresh" || open {
		t.Fatalf("unknown-state secondary click = (%q,%t), want refresh", action, open)
	}
	tooltip := flatten(TooltipTree(snapshot))
	for _, want := range []string{"Checking", "Click: details", "Right-click: refresh", snapshot.Message} {
		if !strings.Contains(tooltip, want) {
			t.Errorf("unknown-state tooltip missing %q: %s", want, tooltip)
		}
	}
	panel := PanelTree(snapshot)
	toggle, refresh := findNode(panel, "panel-action"), findNode(panel, nodeRefresh)
	if toggle == nil || !toggle.Disabled || toggle.Text != "Checking" || toggle.Name != "Lid Guard status is unknown" || refresh == nil || refresh.Disabled {
		t.Fatalf("unknown-state panel actions = toggle %+v, refresh %+v; want disabled Checking and enabled Refresh", toggle, refresh)
	}
}

func TestPanelActionsMatchDisplayedStateAndErrorRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    v1.ServiceState
		message  string
		text     string
		nameText string
		action   string
	}{
		{name: "inactive", state: v1.ServiceStateInactive, text: "Enable", nameText: "Enable Lid Guard", action: "enable"},
		{name: "active", state: v1.ServiceStateActive, text: "Disable", nameText: "Disable Lid Guard", action: "disable"},
		{name: "failed unit", state: v1.ServiceStateError, message: "The Lid Guard service failed; enable it to recover.", text: "Enable", nameText: "Enable Lid Guard to recover", action: "enable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{State: tc.state, Available: true, Message: tc.message}
			panel := PanelTree(snapshot)
			toggle, refresh := findNode(panel, "panel-action"), findNode(panel, nodeRefresh)
			if toggle == nil || toggle.Disabled || toggle.Text != tc.text || toggle.Name != tc.nameText {
				t.Errorf("panel action = %+v, want enabled %q (%q)", toggle, tc.text, tc.nameText)
			}
			if tc.state == v1.ServiceStateError && (refresh == nil || refresh.Disabled) {
				t.Errorf("error-state refresh = %+v, want enabled", refresh)
			}
			if action, open := HandleInput(&v1.InputEvent{Node: "panel-action", Event: v1.EventActivate}, snapshot); action != tc.action || open {
				t.Errorf("panel action for %s = (%q,%t), want (%q,false)", tc.name, action, open, tc.action)
			}
		})
	}

	for _, message := range []string{"systemd returned an unknown Lid Guard service state.", "The Lid Guard service failed; enable it to recover."} {
		snapshot := Snapshot{State: v1.ServiceStateError, Available: true, Message: message}
		bar := findNode(BarTree(snapshot), nodeToggle)
		if bar == nil || bar.Disabled || !strings.Contains(bar.Name, "open details") {
			t.Errorf("bar error control = %+v, want enabled details route", bar)
		}
		if action, open := HandleInput(&v1.InputEvent{Node: nodeToggle, Event: v1.EventActivate}, snapshot); action != "" || !open {
			t.Errorf("bar error activation = (%q,%t), want details panel", action, open)
		}
	}
}

func TestGenericErrorOffersRefreshWithoutDuplicatePrimaryRetry(t *testing.T) {
	snapshot := Snapshot{
		State: v1.ServiceStateError, Available: true,
		Message: "systemd returned an unknown Lid Guard service state.",
	}
	panel := PanelTree(snapshot)
	primary, refresh := findNode(panel, nodePanelAction), findNode(panel, nodeRefresh)
	if primary == nil || !primary.Disabled || primary.Fill != "surface" || primary.Text != "Needs attention" || primary.Name != "Lid Guard needs attention" {
		t.Fatalf("generic-error primary action = %+v, want subdued disabled Needs attention", primary)
	}
	if refresh == nil || refresh.Disabled {
		t.Fatalf("generic-error refresh = %+v, want the single enabled retry action", refresh)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodePanelAction, Event: v1.EventActivate}, snapshot); action != "" || open {
		t.Fatalf("generic-error primary activation = (%q,%t), want no action", action, open)
	}
	if action, open := HandleInput(&v1.InputEvent{Node: nodeRefresh, Event: v1.EventActivate}, snapshot); action != "refresh" || open {
		t.Fatalf("generic-error refresh activation = (%q,%t), want refresh", action, open)
	}
}

func TestErrorStatusCopyUsesContrastSafeSurfaceAndExplicitState(t *testing.T) {
	for _, tc := range []struct {
		name, stateText, tooltipText, message string
		state                                 v1.ServiceState
	}{
		{
			name: "error", state: v1.ServiceStateError,
			stateText: "Service error", tooltipText: "Error", message: "systemd returned an unknown Lid Guard service state.",
		},
		{
			name: "unavailable", state: v1.ServiceStateUnavailable,
			stateText: "Unavailable", tooltipText: "Unavailable", message: "The user service manager is unavailable.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{State: tc.state, Available: tc.state != v1.ServiceStateUnavailable, Message: tc.message}
			panel := PanelTree(snapshot)
			if len(panel.Children) < 2 || panel.Children[1].Fill != "error-container" {
				t.Fatalf("error status card = %+v, want error-container surface", panel.Children[1])
			}
			for _, text := range []string{tc.stateText, tc.message} {
				node := findTextNode(panel, text)
				if node == nil || node.Tone != v1.ToneNormal {
					t.Errorf("panel text %q node = %+v, want inherited error-container foreground", text, node)
				}
			}
			tooltip := TooltipTree(snapshot)
			for _, text := range []string{tc.tooltipText, tc.message} {
				node := findTextNode(tooltip, text)
				if node == nil || node.Tone != v1.ToneSubtle {
					t.Errorf("tooltip text %q node = %+v, want readable subtle tone", text, node)
				}
			}
		})
	}
}

func TestUnavailableDependencyListWrapsWithoutHidingCommands(t *testing.T) {
	message := "Missing required commands: systemctl, systemd-run, systemd-inhibit"
	if len(message) <= 64 {
		t.Fatalf("test message length = %d, want to exceed the old single-line limit", len(message))
	}
	panel := PanelTree(Snapshot{State: v1.ServiceStateUnavailable, Message: message})
	if len(panel.Children) < 2 || len(panel.Children[1].Children) < 2 {
		t.Fatalf("status card shape changed: %+v", panel)
	}
	statusCard := panel.Children[1]
	if len(statusCard.Children) != 3 || statusCard.Children[1].Text != "Missing required commands:" || statusCard.Children[2].Text != "systemctl, systemd-run, systemd-inhibit" {
		t.Fatalf("unavailable detail lines = %+v, want heading and complete command list", statusCard.Children)
	}
	for _, detail := range statusCard.Children[1:] {
		if detail.Tooltip != message {
			t.Errorf("unavailable detail line tooltip = %q, want full message %q", detail.Tooltip, message)
		}
	}
}

func TestViewsUsePlannedLaptopAndRefreshIcons(t *testing.T) {
	snapshot := Snapshot{State: v1.ServiceStateInactive, Available: true}
	if bar := findNode(BarTree(snapshot), nodeToggle); bar == nil || bar.Icon != "laptop" {
		t.Fatalf("bar icon = %+v, want laptop", bar)
	}
	panel := PanelTree(snapshot)
	if findIcon(panel, "laptop") == nil {
		t.Fatal("panel header has no laptop icon")
	}
	if refresh := findNode(panel, nodeRefresh); refresh == nil || refresh.Icon != "restart_alt" {
		t.Fatalf("refresh button = %+v, want restart_alt icon", refresh)
	}
}

func TestPanelHasExplicitStatusRecoveryAndBoundedActions(t *testing.T) {
	state := Snapshot{State: v1.ServiceStateUnavailable, Message: "systemd user manager is unavailable"}
	root := PanelTree(state)
	text := flatten(root)
	for _, want := range []string{"Unavailable", "systemd user manager is unavailable", "lid-close", "manual suspend"} {
		if !strings.Contains(text, want) {
			t.Errorf("panel missing %q: %s", want, text)
		}
	}
	toggle, refresh := findNode(root, "panel-action"), findNode(root, nodeRefresh)
	if toggle == nil || !toggle.Disabled || refresh == nil || refresh.Disabled {
		t.Fatalf("unavailable panel controls = toggle %+v, refresh %+v", toggle, refresh)
	}
	active := PanelTree(Snapshot{State: v1.ServiceStateActive, Available: true})
	toggle = findNode(active, "panel-action")
	if toggle == nil || toggle.Text != "Disable" || toggle.Disabled {
		t.Fatalf("active panel toggle = %+v, want enabled Disable", toggle)
	}
}

func TestDisabledPanelPrimaryActionShowsStateWithSubduedTreatment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
		text     string
		nameText string
	}{
		{
			name:     "busy",
			snapshot: Snapshot{State: v1.ServiceStateInactive, Available: true, Busy: true},
			text:     "Working…",
			nameText: "Lid Guard is changing",
		},
		{
			name:     "unavailable",
			snapshot: Snapshot{State: v1.ServiceStateUnavailable, Message: "systemd is unavailable"},
			text:     "Unavailable",
			nameText: "Lid Guard is unavailable",
		},
		{
			name:     "unknown",
			snapshot: Snapshot{State: v1.ServiceStateUnknown, Available: true},
			text:     "Checking",
			nameText: "Lid Guard status is unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := findNode(PanelTree(tc.snapshot), nodePanelAction)
			if action == nil || !action.Disabled || action.Fill != "surface" || action.Text != tc.text || action.Name != tc.nameText {
				t.Fatalf("disabled panel action = %+v, want disabled subdued %q (%q)", action, tc.text, tc.nameText)
			}
			if tc.name == "busy" {
				refresh := findNode(PanelTree(tc.snapshot), nodeRefresh)
				if refresh == nil || !refresh.Disabled || refresh.Fill != "surface" {
					t.Fatalf("busy refresh = %+v, want disabled surface treatment", refresh)
				}
			}
		})
	}
}

func TestBusyPanelShowsPendingFeedbackInsteadOfStaleServiceDetail(t *testing.T) {
	snapshot := Snapshot{
		State:     v1.ServiceStateInactive,
		Available: true,
		Busy:      true,
		Message:   "Previous command failed",
	}
	text := flatten(PanelTree(snapshot))
	if !strings.Contains(text, "Changing state") {
		t.Fatalf("busy panel status missing: %s", text)
	}
	if !strings.Contains(text, "Waiting for systemd to confirm the change.") {
		t.Fatalf("busy panel explanation missing: %s", text)
	}
	for _, stale := range []string{"Previous command failed", "normal lid-close policy is in effect"} {
		if strings.Contains(text, stale) {
			t.Errorf("busy panel shows stale detail %q: %s", stale, text)
		}
	}

	tooltip := flatten(TooltipTree(snapshot))
	if !strings.Contains(tooltip, "Waiting for systemd to confirm the change.") {
		t.Fatalf("busy tooltip explanation missing: %s", tooltip)
	}
	if strings.Contains(tooltip, "Previous command failed") {
		t.Fatalf("busy tooltip shows stale detail: %s", tooltip)
	}
}
