package hermes

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-shell/plugin/icons"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// panelSizes are the declared chat panel and the fitted-down size.
var panelSizes = [][2]int{{780, 720}, {640, 520}}

var allSections = []Section{SectionNone, SectionSummary, SectionSessions, SectionModel}

func longText(i int) string {
	return fmt.Sprintf("Message %d: ", i) + strings.Repeat("a fairly long sentence about the release notes and the test suite. ", 12)
}

func basePanel(s Status) PanelState {
	sum := richSummary(s)
	sum.ApprovalPending = false
	p := PanelState{
		Summary:   sum,
		SessionID: "20261111_115900_abcd",
		Messages: []Message{
			{ID: "m1", Role: "user", Text: "Tidy the release notes and run the test suite."},
			{ID: "m2", Role: "assistant", Text: "The notes are tidied.\nThe tests need a shell command."},
		},
		Sessions: SessionsSnapshot{Loaded: true, Sessions: tenSessions()},
		Models:   modelOptions(5),
		Avatar:   "",
		Now:      sessionNow,
	}
	return p
}

func heavyPanel(s Status, section Section, approval bool) PanelState {
	p := basePanel(s)
	p.Messages = nil
	for i := 0; i < 30; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		p.Messages = append(p.Messages, Message{ID: fmt.Sprintf("m%d", i), Role: role, Text: longText(i)})
	}
	for i := 0; i < 8; i++ {
		p.Events = append(p.Events, ToolEvent{ID: fmt.Sprintf("e%d", i), Type: "tool.complete", Name: "terminal", Text: "go test ./... " + strings.Repeat("x", 80)})
	}
	p.ShowToolActivity = true
	p.LastTool = "terminal"
	p.Models = modelOptions(20)
	p.Section = section
	if approval {
		p.Summary.ApprovalPending = true
		p.Summary.ApprovalMessage = "Run go test ./... in the repository"
		p.ApprovalTool = "terminal"
	}
	return p
}

func countNodes(n *v1.Node) int {
	c := 0
	walk(n, func(*v1.Node) { c++ })
	return c
}

func findKey(root *v1.Node, key string) *v1.Node {
	var found *v1.Node
	walk(root, func(n *v1.Node) {
		if found == nil && n.Key == key {
			found = n
		}
	})
	return found
}

func TestPanelLaysOutEverySectionAndApproval(t *testing.T) {
	avatar := repoAvatar(t)
	if len(Statuses) != 7 {
		t.Fatalf("Statuses has %d entries", len(Statuses))
	}
	for _, s := range Statuses {
		for _, sec := range allSections {
			for _, approval := range []bool{false, true} {
				for _, size := range panelSizes {
					name := fmt.Sprintf("%s section=%q approval=%v %dx%d", s, sec, approval, size[0], size[1])
					p := heavyPanel(s, sec, approval)
					p.Avatar = avatar
					tree := PanelTree(p, size[0], size[1])
					lintView(t, name, tree, v1.ViewPanel, size[0], size[1])
					checkBudget(t, name, tree, size[0], size[1])
					if tr := findKey(tree, "transcript"); tr == nil || tr.Height < 160 {
						t.Errorf("%s: transcript = %+v, want at least 160 tall", name, tr)
					}
					if n := countNodes(tree); n >= v1.MaxNodes {
						t.Errorf("%s: %d nodes", name, n)
					}
				}
			}
		}
	}
}

func TestPanelNodeBudgetWithEveryCap(t *testing.T) {
	for _, sec := range allSections {
		p := heavyPanel(StatusBusy, sec, true)
		p.Summary.ApprovalPending = sec == SectionNone
		tree := PanelTree(p, 780, 720)
		if n := countNodes(tree); n >= v1.MaxNodes {
			t.Errorf("section %q: %d nodes, want under %d", sec, n, v1.MaxNodes)
		}
		bubbles := 0
		if tr := findKey(tree, "transcript"); tr != nil {
			for _, c := range tr.Children {
				if strings.HasPrefix(c.Key, "msg:") {
					bubbles++
				}
			}
		}
		if bubbles != 24 {
			t.Errorf("section %q: %d message bubbles counted, want the 24 the budget is for", sec, bubbles)
		}
		if sec != SectionNone && findKey(tree, "section-body:"+string(sec)) == nil {
			t.Errorf("section %q: body missing from the budgeted tree", sec)
		}
		if err := v1.Validate(tree, v1.ViewPanel); err != nil {
			t.Errorf("section %q: %v", sec, err)
		}
	}
}

func TestOneSectionOpenAtATime(t *testing.T) {
	if ToggleSection(SectionNone, SectionModel) != SectionModel ||
		ToggleSection(SectionModel, SectionModel) != SectionNone ||
		ToggleSection(SectionModel, SectionSessions) != SectionSessions {
		t.Error("ToggleSection must open one and close the rest")
	}
	for _, id := range []string{"section:summary", "section:sessions", "section:model"} {
		if _, ok := SectionForNode(id); !ok {
			t.Errorf("SectionForNode(%q) does not resolve", id)
		}
	}
	for _, sec := range allSections {
		for _, approval := range []bool{false, true} {
			p := heavyPanel(StatusIdle, sec, approval)
			tree := PanelTree(p, 780, 720)
			open := 0
			for _, other := range allSections[1:] {
				if findKey(tree, "section-body:"+string(other)) != nil {
					open++
					if other != sec {
						t.Errorf("section %q open but %q rendered", sec, other)
					}
				}
			}
			if sec != SectionNone && open != 1 {
				t.Errorf("section %q approval=%v: %d bodies rendered, want 1", sec, approval, open)
			}
			if sec == SectionNone && open != 0 {
				t.Errorf("no section open but %d bodies rendered", open)
			}
			for _, size := range panelSizes {
				tree := PanelTree(p, size[0], size[1])
				card := findKey(tree, "approval")
				if approval && (card == nil || card.Height != 112) {
					t.Errorf("section %q %dx%d: approval card = %+v, want 112 tall", sec, size[0], size[1], card)
				}
				if !approval && card != nil {
					t.Errorf("section %q: approval card without a pending approval", sec)
				}
			}
		}
	}
}

func TestGatewayButtonsOnlyInLocalMode(t *testing.T) {
	for _, gw := range []string{"running", "stopped", ""} {
		for _, clientOnly := range []bool{false, true} {
			p := basePanel(StatusIdle)
			p.Summary.GatewayStatus = gw
			p.ClientOnly = clientOnly
			tree := PanelTree(p, 780, 720)
			start, stop := findID(tree, "start-gateway"), findID(tree, "stop-gateway")
			switch {
			case clientOnly:
				if start != nil || stop != nil {
					t.Errorf("client-only gateway=%q offers start=%v stop=%v", gw, start != nil, stop != nil)
				}
			case gw == "running":
				if start != nil || stop == nil || stop.Text != "Stop gateway" {
					t.Errorf("local running: start=%v stop=%+v", start != nil, stop)
				}
			default:
				if stop != nil || start == nil || start.Text != "Start gateway" {
					t.Errorf("local gateway=%q: start=%+v stop=%v", gw, start, stop != nil)
				}
			}
		}
	}
}

func TestInterruptOnlyWhileRunning(t *testing.T) {
	for _, running := range []bool{false, true} {
		p := basePanel(StatusIdle)
		p.Summary.SessionRunning = running
		tree := PanelTree(p, 780, 720)
		if got := findID(tree, "interrupt") != nil; got != running {
			t.Errorf("running=%v: action-row interrupt present=%v", running, got)
		}
		send, stop := findID(tree, "send"), findID(tree, "composer-interrupt")
		if running && (send != nil || stop == nil || stop.Text != "Interrupt") {
			t.Errorf("running: send=%v interrupt=%+v", send != nil, stop)
		}
		if !running && (stop != nil || send == nil || send.Text != "Send") {
			t.Errorf("idle: send=%+v interrupt=%v", send, stop != nil)
		}
	}
	want := []string{"new-session", "refresh", "start-gateway", "settings"}
	p := basePanel(StatusOffline)
	p.Summary.GatewayStatus = "stopped"
	var row []string
	tree := PanelTree(p, 780, 720)
	if ar := findKey(tree, "actions"); ar != nil {
		for _, c := range ar.Children {
			row = append(row, c.ID)
		}
	}
	if strings.Join(row, ",") != strings.Join(want, ",") {
		t.Errorf("action row = %v, want %v", row, want)
	}
}

func TestPanelStatusIconsFollowDesign(t *testing.T) {
	want := map[Status]string{
		StatusOffline: "power_settings_new", StatusIdle: "check", StatusBusy: "refresh",
		StatusAttention: "notifications-active", StatusDegraded: "gpp_maybe",
		StatusError: "cancel", StatusUnknown: "ai-usage",
	}
	for _, s := range Statuses {
		if got := panelStatusIcon(s); got != want[s] {
			t.Errorf("panelStatusIcon(%s) = %q, want %q", s, got, want[s])
		}
		tree := PanelTree(basePanel(s), 780, 720)
		icon := findKey(tree, "header-status-icon")
		if icon == nil || icon.Icon != want[s] || icon.Tone != s.Tone() {
			t.Errorf("%s: header status icon = %+v", s, icon)
		}
	}
}

func TestHeaderMatchesDesign(t *testing.T) {
	avatar := repoAvatar(t)
	p := basePanel(StatusIdle)
	p.Avatar = avatar
	tree := PanelTree(p, 780, 720)
	header := findKey(tree, "header")
	if header == nil || header.Height != 56 {
		t.Fatalf("header = %+v", header)
	}
	var img *v1.Node
	walk(header, func(n *v1.Node) {
		if n.Kind == v1.KindImage {
			img = n
		}
	})
	if img == nil || img.Path != avatar {
		t.Error("header has no avatar")
	}
	joined := strings.Join(texts(header), "|")
	for _, want := range []string{"Hermes", "Online", "openrouter / example-model"} {
		if !strings.Contains(joined, want) {
			t.Errorf("header is missing %q in %q", want, joined)
		}
	}
	for id, text := range map[string]string{"section:sessions": "Sessions", "pin": "Pin", "close": ""} {
		n := findID(header, id)
		if n == nil || n.Text != text {
			t.Errorf("header button %s = %+v, want text %q", id, n, text)
		}
	}
	p.Pinned = true
	if n := findID(PanelTree(p, 780, 720), "pin"); n == nil || n.Text != "Unpin" {
		t.Errorf("pinned header pin button = %+v", n)
	}
	for _, c := range []struct {
		mutate func(*PanelState)
		want   string
	}{
		{func(p *PanelState) { p.Summary.Provider, p.Summary.ModelName, p.Summary.Model = "", "", "" }, "No model selected"},
		{func(p *PanelState) {
			p.Summary.Provider, p.Summary.ModelName, p.Summary.Model = "", "", ""
			p.DefaultProvider, p.DefaultModel = "nous", "default-model"
		}, "nous / default-model"},
		{func(p *PanelState) { p.Summary.Provider, p.Summary.ModelName = "", "solo-model" }, "solo-model"},
	} {
		q := basePanel(StatusIdle)
		c.mutate(&q)
		if got := panelModelLabel(q); got != c.want {
			t.Errorf("panelModelLabel = %q, want %q", got, c.want)
		}
	}
}

func TestComposerKeySurvivesRepublishAndReseedOnlyOnSend(t *testing.T) {
	p := basePanel(StatusIdle)
	first := findID(PanelTree(p, 780, 720), "composer")
	p.Messages = append(p.Messages, Message{ID: "m3", Role: "assistant", Text: "More"})
	second := findID(PanelTree(p, 780, 720), "composer")
	if first == nil || second == nil {
		t.Fatal("no composer")
	}
	if first.Key == "" || first.Key != second.Key || first.Reseed != second.Reseed {
		t.Errorf("republish changed the composer: %q/%d -> %q/%d", first.Key, first.Reseed, second.Key, second.Reseed)
	}
	if first.Kind != v1.KindTextInput || first.Placeholder != "Ask Hermes..." || !first.SubmitOnEnter {
		t.Errorf("composer = %+v", first)
	}
	p.Composer = AfterSend(p.Composer)
	third := findID(PanelTree(p, 780, 720), "composer")
	if third.Reseed != first.Reseed+1 || third.Key != first.Key {
		t.Errorf("after send: key %q reseed %d, want %q %d", third.Key, third.Reseed, first.Key, first.Reseed+1)
	}
	for _, id := range []string{"composer-new-session", "composer-reset", "one-shot"} {
		if findID(PanelTree(p, 780, 720), id) == nil {
			t.Errorf("composer has no %s", id)
		}
	}
}

func TestSendPlanCreatesASessionFirst(t *testing.T) {
	paths := func(calls []BridgeCall) string {
		var out []string
		for _, c := range calls {
			out = append(out, c.Path)
		}
		return strings.Join(out, ",")
	}
	calls, err := SendPlan("", "hello", false)
	if err != nil || paths(calls) != "/session/create,/prompt" {
		t.Errorf("no session: %v %v", paths(calls), err)
	}
	calls, err = SendPlan("s1", "  hello  ", false)
	if err != nil || paths(calls) != "/prompt" || calls[0].Body.(PromptRequest).Text != "hello" {
		t.Errorf("with session: %+v %v", calls, err)
	}
	calls, err = SendPlan("", "hello", true)
	if err != nil || paths(calls) != "/oneshot" {
		t.Errorf("one-shot: %v %v", paths(calls), err)
	}
	if _, err := SendPlan("s1", " \n\t", false); err == nil || err.Error() != "Prompt is empty" {
		t.Errorf("blank prompt err = %v", err)
	}
	if got := paths(ResetPlan()); got != "/session/create" {
		t.Errorf("reset = %v, want a new session", got)
	}
}

func TestEmptyStatesNameTheProblem(t *testing.T) {
	off := basePanel(StatusOffline)
	off.Messages = nil
	joined := strings.Join(texts(findKey(PanelTree(off, 780, 720), "transcript")), "|")
	if !strings.Contains(joined, "Bridge offline") || !strings.Contains(joined, "Connection failed") {
		t.Errorf("offline transcript = %q", joined)
	}
	none := basePanel(StatusIdle)
	none.Messages, none.SessionID = nil, ""
	joined = strings.Join(texts(findKey(PanelTree(none, 780, 720), "transcript")), "|")
	if !strings.Contains(joined, "No active session") {
		t.Errorf("no-session transcript = %q", joined)
	}
}

func TestInteractiveNodesHaveUniqueIDsNamesAndRoles(t *testing.T) {
	for _, sec := range allSections {
		for _, approval := range []bool{false, true} {
			p := heavyPanel(StatusBusy, sec, approval)
			p.Models = modelOptions(45)
			tree := PanelTree(p, 780, 720)
			seen := map[string]bool{}
			walk(tree, func(n *v1.Node) {
				switch n.Kind {
				case v1.KindButton, v1.KindTextInput, v1.KindSlider, v1.KindDragSource:
				default:
					return
				}
				if n.ID == "" || n.Name == "" || n.Role == "" {
					t.Errorf("section %q: %s %q lacks id, name or role: %+v", sec, n.Kind, n.ID, n)
				}
				if seen[n.ID] {
					t.Errorf("section %q: duplicate id %q", sec, n.ID)
				}
				seen[n.ID] = true
			})
			for _, id := range []string{"composer", "composer-interrupt", "close", "pin", "section:sessions", "new-session", "settings"} {
				if !seen[id] {
					t.Errorf("section %q: interactive node %q missing", sec, id)
				}
			}
		}
	}
}

func TestActivityLineWordingAndVisibility(t *testing.T) {
	p := basePanel(StatusIdle)
	p.Summary.ToolEvents, p.Summary.RunningTools = 1, 1
	p.ShowToolActivity = true
	if got := activityLine(p); got != "1 background action running" {
		t.Errorf("running = %q", got)
	}
	p.Summary.RunningTools, p.Summary.ToolEvents, p.LastTool = 0, 4, "terminal"
	if got := activityLine(p); got != "4 background actions, last: terminal" {
		t.Errorf("idle = %q", got)
	}
	p.LastTool = ""
	if got := activityLine(p); got != "4 background actions" {
		t.Errorf("no last tool = %q", got)
	}
	p.ShowToolActivity = false
	if findKey(PanelTree(p, 780, 720), "activity") != nil {
		t.Error("activity line shown with show_tool_activity off and no turn running")
	}
	p.Summary.SessionRunning = true
	if a := findKey(PanelTree(p, 780, 720), "activity"); a == nil || a.Height != 28 {
		t.Errorf("activity line while running = %+v", a)
	}
	p.Summary.ToolEvents = 0
	if findKey(PanelTree(p, 780, 720), "activity") != nil {
		t.Error("activity line without activity")
	}
}

func TestTranscriptBubblesAndEvents(t *testing.T) {
	p := basePanel(StatusIdle)
	p.Messages = append(p.Messages, Message{ID: "m3", Role: "tool-ish", Text: "unknown role"})
	tree := PanelTree(p, 780, 720)
	tr := findKey(tree, "transcript")
	if tr == nil || tr.Kind != v1.KindList {
		t.Fatalf("transcript = %+v", tr)
	}
	wantFill := []string{"accent", "card", "card"}
	wantIcon := []string{"person", "ai-usage", "ai-usage"}
	wantWho := []string{"You", "Hermes", "Hermes"}
	if len(tr.Children) != 3 {
		t.Fatalf("transcript has %d children", len(tr.Children))
	}
	for i, b := range tr.Children {
		if b.Fill != wantFill[i] || b.Key != "msg:"+p.Messages[i].ID {
			t.Errorf("bubble %d fill %q key %q", i, b.Fill, b.Key)
		}
		var icon *v1.Node
		walk(b, func(n *v1.Node) {
			if n.Kind == v1.KindIcon && icon == nil {
				icon = n
			}
		})
		if icon == nil || icon.Icon != wantIcon[i] {
			t.Errorf("bubble %d icon = %+v", i, icon)
		}
		if got := texts(b); len(got) == 0 || got[0] != wantWho[i] {
			t.Errorf("bubble %d texts = %v", i, got)
		}
	}
	if got := strings.Join(texts(tr.Children[1]), "|"); got != "Hermes|The notes are tidied.|The tests need a shell command." {
		t.Errorf("newlines should split lines: %q", got)
	}
	heavy := heavyPanel(StatusIdle, SectionNone, false)
	htree := PanelTree(heavy, 640, 520)
	htr := findKey(htree, "transcript")
	bubbles, events := 0, 0
	for _, c := range htr.Children {
		if strings.HasPrefix(c.Key, "msg:") {
			bubbles++
		}
		if strings.HasPrefix(c.Key, "event:") {
			events++
			var icon *v1.Node
			walk(c, func(n *v1.Node) {
				if n.Kind == v1.KindIcon {
					icon = n
				}
			})
			if icon == nil || icon.Icon != "terminal" {
				t.Errorf("event row icon = %+v", icon)
			}
		}
	}
	if bubbles != 24 || events != 8 {
		t.Errorf("heavy transcript: %d bubbles %d events, want 24 and 8", bubbles, events)
	}
	if !strings.Contains(strings.Join(texts(htr), "|"), "Showing the last 24 messages") {
		t.Error("missing the last-24 caption")
	}
	if findKey(htr, "msg:m5") != nil || findKey(htr, "msg:m6") == nil {
		t.Error("transcript should keep the newest 24 messages")
	}
	limit := (640 - 2*14) / 8
	walk(htr, func(n *v1.Node) {
		if n.Kind == v1.KindText && len(n.Text) > limit {
			t.Errorf("line %q is %d bytes, past %d", n.Text, len(n.Text), limit)
		}
	})
	heavy.ShowToolActivity = false
	for _, c := range findKey(PanelTree(heavy, 640, 520), "transcript").Children {
		if strings.HasPrefix(c.Key, "event:") {
			t.Fatal("events rendered with show_tool_activity off")
		}
	}
}

func TestApprovalFixtureMatchesDesign(t *testing.T) {
	p := heavyPanel(StatusAttention, SectionNone, true)
	card := findKey(PanelTree(p, 780, 720), "approval")
	if card == nil {
		t.Fatal("no approval card")
	}
	var icon *v1.Node
	walk(card, func(n *v1.Node) {
		if n.Kind == v1.KindIcon && icon == nil {
			icon = n
		}
	})
	if icon == nil || icon.Icon != "gpp_maybe" {
		t.Errorf("approval icon = %+v", icon)
	}
	joined := strings.Join(texts(card), "|")
	if !strings.Contains(joined, "terminal") || !strings.Contains(joined, "Run go test ./... in the repository") {
		t.Errorf("approval texts = %q", joined)
	}
	var labels []string
	walk(card, func(n *v1.Node) {
		if n.Kind == v1.KindButton {
			labels = append(labels, n.Text)
		}
	})
	if strings.Join(labels, ",") != "Deny,Allow,Allow Session" {
		t.Errorf("approval buttons = %v", labels)
	}
	p.ApprovalTool = ""
	if !strings.Contains(strings.Join(texts(findKey(PanelTree(p, 780, 720), "approval")), "|"), "Approval required") {
		t.Error("approval without a tool name should read Approval required")
	}
}

func TestPanelGlyphsAreInTheCatalogue(t *testing.T) {
	avatar := repoAvatar(t)
	for _, s := range Statuses {
		for _, sec := range allSections {
			for _, clientOnly := range []bool{false, true} {
				p := heavyPanel(s, sec, true)
				p.Avatar = avatar
				p.ClientOnly = clientOnly
				p.Summary.SessionRunning = s == StatusBusy
				for _, av := range []string{avatar, ""} {
					p.Avatar = av
					walk(PanelTree(p, 780, 720), func(n *v1.Node) {
						if n.Icon == "" {
							return
						}
						if !icons.ValidName(n.Icon) {
							t.Errorf("%s/%s: %q is not in the pinned catalogue", s, sec, n.Icon)
						}
						for _, bad := range []string{"warning", "history", "bolt", "link_off"} {
							if n.Icon == bad {
								t.Errorf("%s/%s: forbidden glyph %q", s, sec, n.Icon)
							}
						}
					})
				}
			}
		}
	}
	_ = time.Second
	seen := map[string]bool{}
	// A busy panel shows Interrupt and an idle one Send, so take both.
	for _, s := range []Status{StatusBusy, StatusIdle} {
		p := heavyPanel(s, SectionModel, true)
		walk(PanelTree(p, 780, 720), func(n *v1.Node) {
			if n.Icon != "" {
				seen[n.Icon] = true
			}
		})
	}
	for _, want := range []string{"sms", "add", "stop", "settings", "replay", "push_pin", "close", "send", "person", "terminal", "gpp_maybe", "refresh", "ai-usage"} {
		if !seen[want] {
			t.Errorf("panel never uses the design glyph %q (have %v)", want, seen)
		}
	}
}

func TestWrapLinesSplitsAndCaps(t *testing.T) {
	got := wrapLines("one two three\nfour", 9, 12)
	if strings.Join(got, "|") != "one two|three|four" {
		t.Errorf("wrap = %q", got)
	}
	if got := wrapLines(strings.Repeat("x", 20), 8, 12); strings.Join(got, "|") != "xxxxxxxx|xxxxxxxx|xxxx" {
		t.Errorf("long word = %q", got)
	}
	capped := wrapLines(strings.Repeat("word ", 200), 20, 12)
	if len(capped) != 12 || !strings.HasSuffix(capped[11], "…") {
		t.Errorf("capped = %d lines, last %q", len(capped), capped[len(capped)-1])
	}
	for _, l := range capped {
		if len(l) > 20 {
			t.Errorf("line %q past 20 bytes", l)
		}
	}
}
