# Timer & World-Clock Noctalia Parity Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Bring the timer and world-clock plugins to noctalia visual/interaction parity by opening plugin/v1 minor 2 (fills, radius, bold, size tiers, disabled, alignment hints) and rebuilding both panels on it.

**Architecture:** Two repos. sysc-shell gains the protocol primitives (wire fields + validation + converter mapping onto existing ui/render tokens — zero new rendering). sysc-plugins rebuilds the two views on the new vocabulary and bumps manifests to minor 2. Old hosts ignore the new JSON fields, so a mismatched pair degrades instead of breaking.

**Tech Stack:** Go 1.26, plugin/v1 wire protocol (JSONL), sysc-shell internal/ui + internal/render (existing tokens only).

**Repos:** `~/sysc-shell` (protocol, Tasks 1–4) and `~/sysc-plugins` (views, Tasks 5–9). Design decisions locked in brainstorm: full-expressiveness wire scope; timer closes its panel on start (noctalia-exact); positional digit parsing for 3+ digit inputs (`300`→5:00, `10300`→1:03:00; `90` stays 90 seconds).

---

## Design summary (approved in brainstorm)

- **Wire minor 2, new optional Node fields** — each maps 1:1 onto an existing ui.Node/render token, so the shell needs plumbing, not rendering:
  - `fill` (containers + buttons): `surface` · `accent` · `container` · `error` · `soft` · `card` · `outline` · `chip` · `error-container` → the nine ui.Fill tokens. Scrim stays host-only.
  - `radius` int on containers → ui.Radius (host clamps).
  - `bold` on text + buttons → ui.Bold.
  - `size` on text: `body`(default) · `caption` · `label` · `title` · `headline` · `display` · `mono` → theme.TextRole.
  - `disabled` on interactive nodes → ui.AriaDisabled (stays in traversal, activation blocked by dropping the action route).
  - `center_x` / `pin_end` placement hints → ui.CenterX / ui.PinEnd.
- **Timer**: setup panel = display-size bold centered remaining time, progress meter, positional duration input (disabled while running), Start = accent chip, Pause = soft chip, Reset = error chip, hint line in caption. Start closes the panel (`panel.close`); the bar carries the running state (accent glyph + countdown). Fired → notification + bar click clears.
- **World clock**: header (`title` size) over an add row (input + accent Add chip); zone cards (`card` fill + radius): drag handle, bold city label over subtle zone id, bold tabular time over subtle offset, ghost trash with the existing two-click confirm. Empty state subtle.
- **Non-goals**: desktop widget (no wire view kind), animations, scrim.

---

### Task 1: wire fields + validation (sysc-shell)

**Files:**
- Modify: `plugin/v1/node.go`
- Test: `plugin/v1/node_test.go`

**Step 1: Write the failing validation tests**

Append to `plugin/v1/node_test.go`:

```go
func TestValidateAcceptsMinorTwoFields(t *testing.T) {
	t.Parallel()

	root := &Node{Kind: KindColumn, Fill: "card", Radius: 12, Children: []*Node{
		{Kind: KindText, Text: "title", Size: "title", Bold: true, CenterX: true},
		{Kind: KindButton, ID: "go", Text: "Go", Name: "Go", Role: "button",
			Fill: "accent", Disabled: true, Events: []EventKind{EventActivate}},
		{Kind: KindRow, Children: []*v1Placeholder{}},
	}}
	// Drop the placeholder row; keep the tree legal.
	root.Children = root.Children[:2]
	if err := Validate(root, ViewPanel); err != nil {
		t.Fatalf("minor-2 fields rejected: %v", err)
	}
}

func TestValidateRejectsBadMinorTwoValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		node *Node
	}{
		{"unknown fill", &Node{Kind: KindColumn, Fill: "neon"}},
		{"fill on text", &Node{Kind: KindText, Text: "x", Fill: "card"}},
		{"unknown size", &Node{Kind: KindText, Text: "x", Size: "giant"}},
		{"size on button", &Node{Kind: KindButton, ID: "b", Text: "x", Name: "b", Role: "button", Size: "title", Events: []EventKind{EventActivate}}},
		{"radius negative", &Node{Kind: KindColumn, Radius: -1}},
		{"radius over limit", &Node{Kind: KindColumn, Radius: MaxExtent + 1}},
		{"disabled on text", &Node{Kind: KindText, Text: "x", Disabled: true}},
	}
	for _, tc := range cases {
		if err := Validate(tc.node, ViewPanel); err == nil {
			t.Errorf("%s: Validate accepted", tc.name)
		}
	}
}
```

(Drop the `v1Placeholder` line — final test body has two children.)

**Step 2: Run to verify failure**

Run: `go test ./plugin/v1/ -run MinorTwo`
Expected: FAIL — `Fill`, `Radius`, `Bold`, `Size`, `Disabled`, `CenterX`, `PinEnd` undefined / fields ignored.

**Step 3: Implement the fields**

In `plugin/v1/node.go`, extend `Node` (after `Tone`):

```go
	// Fill names the semantic background of a container or button. The host
	// maps it onto its own theme tokens; an unknown name is a diagnosable
	// validation error, not a fallback.
	Fill string `json:"fill,omitempty"`
	// Radius overrides the corner radius of a container in logical pixels.
	Radius int `json:"radius,omitempty"`
	// Bold marks an emphasized text run. Shaping resolves a real bold face.
	Bold bool `json:"bold,omitempty"`
	// Size names a type-ladder rung: body, caption, label, title, headline,
	// display, mono. The theme decides the point size and weight.
	Size string `json:"size,omitempty"`
	// Disabled greys an interactive node out: it stays in keyboard traversal
	// with its accessible name explaining why, and activation is blocked.
	Disabled bool `json:"disabled,omitempty"`
	// CenterX centres a child in its column track.
	CenterX bool `json:"center_x,omitempty"`
	// PinEnd right-pins the last child of a two-child row.
	PinEnd bool `json:"pin_end,omitempty"`
```

Enums + validation:

```go
var knownFills = map[string]bool{
	"surface": true, "accent": true, "container": true, "error": true,
	"soft": true, "card": true, "outline": true, "chip": true,
	"error-container": true,
}

var knownSizes = map[string]bool{
	"body": true, "caption": true, "label": true, "title": true,
	"headline": true, "display": true, "mono": true,
}

func fillAllowed(k NodeKind) bool {
	return k.container() || k == KindButton
}
```

In `Validate`'s node walk (after `events`):

```go
	if err := v.minorTwo(n, path); err != nil {
		return err
	}
```

```go
func (v *validator) minorTwo(n *Node, path string) error {
	if n.Fill != "" {
		if !knownFills[n.Fill] {
			return fmt.Errorf("%s: unknown fill %q", path, n.Fill)
		}
		if !fillAllowed(n.Kind) {
			return fmt.Errorf("%s: %s cannot carry a fill", path, n.Kind)
		}
	}
	if n.Radius < 0 || n.Radius > 256 {
		return fmt.Errorf("%s: radius %d outside 0..256", path, n.Radius)
	}
	if n.Size != "" {
		if !knownSizes[n.Size] {
			return fmt.Errorf("%s: unknown size %q", path, n.Size)
		}
		if n.Kind != KindText {
			return fmt.Errorf("%s: %s cannot carry a size", path, n.Kind)
		}
	}
	if n.Disabled && !n.Kind.interactive() {
		return fmt.Errorf("%s: %s cannot be disabled", path, n.Kind)
	}
	return nil
}
```

**Step 4: Run to verify pass**

Run: `go test ./plugin/v1/`
Expected: PASS (all existing tests too).

**Step 5: Commit**

```bash
git add plugin/v1/
git commit -m "feat(plugin): minor-2 wire fields for noctalia-parity views"
```

---

### Task 2: converter mapping (sysc-shell)

**Files:**
- Modify: `internal/plugin/view.go` (convertNode + a role map)
- Test: `internal/plugin/view_test.go`

**Step 1: Write the failing converter tests**

Append to `internal/plugin/view_test.go`:

```go
func TestConvertMapsMinorTwoFields(t *testing.T) {
	t.Parallel()

	root := &v1.Node{Kind: v1.KindColumn, Fill: "card", Radius: 12, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "big", Size: "display", Bold: true, CenterX: true},
		{Kind: v1.KindTextInput, ID: "dur", Text: "5m", Name: "Duration", Role: "textbox",
			Disabled: true, Events: []v1.EventKind{v1.EventChange}},
	}}
	got, err := Convert(root, v1.ViewPanel)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Fill != ui.FillContainerHigh || got.Radius != 12 {
		t.Fatalf("card = fill %v radius %d", got.Fill, got.Radius)
	}
	title := got.Children[0]
	if !title.Bold || title.TextRole != theme.RoleDisplay || !title.CenterX {
		t.Fatalf("title = %+v", title)
	}
	input := got.Children[1]
	if !input.AriaDisabled || input.Action != "" {
		t.Fatalf("disabled input = %+v", input)
	}
}

func TestConvertRejectsAnUnknownFill(t *testing.T) {
	t.Parallel()
	root := &v1.Node{Kind: v1.KindColumn, Fill: "neon"}
	if _, err := Convert(root, v1.ViewPanel); err == nil {
		t.Fatal("unknown fill accepted")
	}
}
```

Add `"github.com/Nomadcxx/sysc-shell/internal/theme"` to the test imports.

**Step 2: Run to verify failure**

Run: `go test ./internal/plugin/ -run MinorTwo`
Expected: FAIL.

**Step 3: Implement the mapping**

In `internal/plugin/view.go`:

```go
var wireFills = map[string]ui.Fill{
	"surface":          ui.FillNone,
	"accent":           ui.FillAccent,
	"container":        ui.FillContainer,
	"error":            ui.FillError,
	"soft":             ui.FillSoft,
	"card":             ui.FillContainerHigh,
	"outline":          ui.FillOutline,
	"chip":             ui.FillContainerHighest,
	"error-container":  ui.FillErrorContainer,
}

var wireSizes = map[string]theme.TextRole{
	"body":      theme.RoleBody,
	"caption":   theme.RoleCaption,
	"label":     theme.RoleLabel,
	"title":     theme.RoleTitle,
	"headline":  theme.RoleHeadline,
	"display":   theme.RoleDisplay,
	"mono":      theme.RoleMono,
}
```

In `convertNode`, extend the copy block and add the switch:

```go
	out := &ui.Node{
		Padding:  n.Padding,
		Gap:      n.Gap,
		Width:    n.Width,
		Height:   n.Height,
		MaxWidth: n.MaxWidth,
		Tabular:  n.Tabular,
		Name:     n.Name,
		Role:     n.Role,
		Bold:     n.Bold,
		CenterX:  n.CenterX,
		PinEnd:   n.PinEnd,
	}
	if n.Fill != "" {
		fill, ok := wireFills[n.Fill]
		if !ok {
			return nil, fmt.Errorf("plugin: %s: unknown fill %q", path, n.Fill)
		}
		if !n.Kind.container() && n.Kind != v1.KindButton {
			return nil, fmt.Errorf("plugin: %s: %s cannot carry a fill", path, n.Kind)
		}
		out.Fill = fill
	}
	if n.Radius != 0 {
		out.Radius = n.Radius
	}
	switch n.Size {
	case "":
	case "body":
		out.TextRole = theme.RoleBody
	case "caption":
		out.TextRole = theme.RoleCaption
	case "label":
		out.TextRole = theme.RoleLabel
	case "title":
		out.TextRole = theme.RoleTitle
	case "headline":
		out.TextRole = theme.RoleHeadline
	case "display":
		out.TextRole = theme.RoleDisplay
	case "mono":
		out.TextRole = theme.RoleMono
	default:
		return nil, fmt.Errorf("plugin: %s: unknown size %q", path, n.Size)
	}
```

And in the interactive branch (after `out.Focusable = true` for button and text input):

```go
		if n.Disabled {
			out.AriaDisabled = true
			out.Action = "" // no action route: activation is blocked
		}
```

Add `"github.com/Nomadcxx/sysc-shell/internal/theme"` to view.go imports.

**Step 4: Run to verify pass**

Run: `go test ./internal/plugin/`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/plugin/view.go internal/plugin/view_test.go
git commit -m "feat(plugin): map minor-2 wire fields onto the shell tree"
```

---

### Task 3: host handshake version + gates (sysc-shell)

**Files:**
- Modify: `internal/plugin/supervisor.go:201` — `Supported: []v1.Version{{Major: 1, Minor: 2}}`
- Modify: `plugin/v1/node.go` doc comment — minor 2 notes

**Step 1:** Bump the advertised minor to 2.
**Step 2:** Run `go test -race ./internal/plugin/ ./tests/integration/` — the handshake test asserts the PLUGIN's declared version, so it stays green.
**Step 3:** Commit `feat(plugin): advertise protocol minor 2` and push.

---

### Task 4: positional duration parsing (sysc-plugins, timer)

**Files:**
- Modify: `plugins/timer/timer.go` (`ParseDuration`)
- Test: `plugins/timer/timer_test.go` (add cases)

**Step 1: Write the failing tests**

```go
func TestParseDurationPositionalDigits(t *testing.T) {
	t.Parallel()
	// Noctalia's digit-block rule: 3-4 digits are mmss, 5-6 are hhmmss.
	cases := map[string]time.Duration{
		"130":    90 * time.Second,
		"1030":   10*time.Minute + 30*time.Second,
		"10300":  time.Hour + 3*time.Minute,
		"10000":  time.Hour,
	}
	for in, want := range cases {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	// 7+ digits stay rejected.
	if _, err := ParseDuration("1000000"); err == nil {
		t.Error("7-digit input accepted")
	}
}
```

**Step 2: Run — expect FAIL** (`"10300"` parses as 10300 seconds today).

**Step 3: Implement** — in `ParseDuration`, inside the bare-digits branch (`if n, err := strconv.Atoi(s); err == nil`):

```go
		if len(s) >= 3 {
			// Noctalia's digit-block rule: the last two digits are seconds,
			// the pair before them minutes, the rest hours.
			sec := n % 100
			min := (n / 100) % 100
			hrs := n / 10000
			if sec > 59 || min > 59 {
				return 0, fmt.Errorf("duration %q", s)
			}
			return time.Duration(hrs)*time.Hour + time.Duration(min)*time.Minute + time.Duration(sec)*time.Second, nil
		}
		return time.Duration(n) * time.Second, nil
```

(Note: `n` was parsed before the length check; clamp `hrs` sanity at 99. One- and two-digit inputs stay bare seconds, matching noctalia.)

**Step 4: Run — PASS. Step 5: Commit** `feat(timer): positional digit durations`.

---

### Task 5: timer panel rebuild + close-on-start (sysc-plugins)

**Files:**
- Modify: `plugins/timer/view.go` (PanelTree), `plugins/timer/manifest.json` (minor 2), `cmd/sysc-plugin-timer/main.go` (close-on-start, disabled passthrough)
- Test: `plugins/timer/view_test.go`

**Step 1: Write the failing tests** — PanelTree(Snapshot{Mode: Idle, ...}, progress, duration) must contain: remaining text with `Size: "display"`, `Bold: true`, `CenterX: true`; progress; duration input with `Disabled: false`; Start button `Fill: "accent"`; Reset `Tone: error` + `Fill: "error"`. Running state: input `Disabled: true`, Start label "Pause" with `Fill: "soft"`. Validate all.

**Step 2: Run — FAIL.**

**Step 3: Implement PanelTree**

```go
func PanelTree(remaining string, state State, progress float64, duration string) *v1.Node {
	tone := v1.ToneNormal
	switch state {
	case StateRunning, StatePaused:
		tone = v1.ToneAccent
	case StateNotify:
		tone = v1.ToneError
	}

	col := &v1.Node{Kind: v1.KindColumn, Gap: 10, Children: []*v1.Node{
		{Kind: v1.KindText, Text: remaining, Tabular: true, Tone: tone,
			Size: "display", Bold: true, CenterX: true, Height: 48},
		{Kind: v1.KindProgress, Value: progress},
	}}

	toggle := v1.Node{Kind: v1.KindButton, ID: "start", Name: "Start timer", Role: "button",
		Fill: "accent", Events: []v1.EventKind{v1.EventActivate}}
	reset := v1.Node{Kind: v1.KindButton, ID: "reset", Text: "Reset", Name: "Reset timer", Role: "button",
		Fill: "error", Events: []v1.EventKind{v1.EventActivate}}
	input := v1.Node{Kind: v1.KindTextInput, ID: "duration", Text: duration, Name: "Duration", Role: "textbox",
		Events: []v1.EventKind{v1.EventChange, v1.EventSubmit}}

	switch state {
	case StateRunning:
		toggle.ID, toggle.Text, toggle.Name, toggle.Fill = "pause", "Pause", "Pause timer", "soft"
		input.Disabled = true
		col.Children = append(col.Children, &input, &v1.Node{Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{&reset, &toggle}})
	case StatePaused:
		toggle.Text, toggle.Name, toggle.Fill = "Resume", "Resume timer", "soft"
		input.Disabled = true
		col.Children = append(col.Children, &input, &v1.Node{Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{&reset, &toggle}})
	default:
		toggle.Text, toggle.Name = "Start", "Start timer"
		col.Children = append(col.Children, &input, &v1.Node{Kind: v1.KindRow, Gap: 8,
			Children: []*v1.Node{&reset, &toggle}})
	}

	if state == StateIdle {
		col.Children = append(col.Children, &v1.Node{Kind: v1.KindText,
			Text: "90 · 5m · 1030 = 10:30 · 1h30m", Tone: v1.ToneSubtle, Size: "caption", CenterX: true})
	}
	return col
}
```

In `cmd/sysc-plugin-timer/main.go`'s input handler, after `rec`/start fires:

```go
		case "start":
			tm.Start()
			save(ctx, c, tm)
			// Noctalia closes the panel on start; the bar carries the count.
			_, _ = c.Call(ctx, v1.CallPanelClose, v1.PanelParams{Entry: "panel", Output: m.Output, Instance: m.ViewID})
```

(`v1.CallPanelClose` exists in the wire; keep the paused-state Resume without closing.)

**Step 4: Run — PASS. Step 5: Commit** `feat(timer): noctalia panel on minor-2 vocabulary`.

---

### Task 6: world-clock card rebuild (sysc-plugins)

**Files:**
- Modify: `plugins/world-clock/view.go` (PanelTree rows), `plugins/world-clock/manifest.json` (minor 2)
- Test: `plugins/world-clock/view_test.go`

**Step 1: Write the failing test** — PanelTree rows: each zone row carries `Fill: "card"` + `Radius: 10`; the city label is `Bold`; the zone id is `ToneSubtle`; the time is `Bold` + `Tabular`; the offset is `ToneSubtle`; the header title is `Size: "title"` + `Bold`; add button `Fill: "accent"`; empty state `ToneSubtle`. Validate.

**Step 2: Run — FAIL.**

**Step 3: Implement** — restructure each zone row as a two-column card:

```go
	rows = append(rows, &v1.Node{Kind: v1.KindRow, Gap: 8, Key: "row:" + r.Zone,
		Fill: "card", Radius: 10, Padding: 8, Children: []*v1.Node{
			{Kind: v1.KindDragSource, ID: "drag:" + r.Zone, Key: "drag:" + r.Zone, Text: "=",
				Name: "Reorder " + r.Zone, Role: "button", DragType: "zone", Payload: r.Zone,
				Events: []v1.EventKind{v1.EventPointer}},
			{Kind: v1.KindColumn, Gap: 2, Children: []*v1.Node{
				{Kind: v1.KindText, Text: r.Label, Bold: true},
				{Kind: v1.KindText, Text: r.Zone, Tone: v1.ToneSubtle},
			}},
			{Kind: v1.KindColumn, Gap: 2, PinEnd: true, Children: []*v1.Node{
				{Kind: v1.KindText, Key: "time:" + r.Zone, Text: r.Clock, Tabular: true, Bold: true},
				{Kind: v1.KindText, Text: r.Offset, Tone: v1.ToneSubtle},
			}},
			{Kind: v1.KindButton, ID: "rm:" + r.Zone, Text: "Remove", Name: "Remove " + r.Zone, Role: "button",
				Events: []v1.EventKind{v1.EventActivate}},
			{Kind: v1.KindDropZone, ID: "drop:" + strconv.Itoa(i), Accept: []string{"zone"},
				Events: []v1.EventKind{v1.EventDrop}},
		}})
```

Header + add row: title `Size: "title", Bold: true`; Add button `Fill: "accent"`. Empty state: `ToneSubtle` "No zones — add a city".

**Step 4: Run — PASS. Step 5: Commit** `feat(world-clock): noctalia zone cards on minor-2 vocabulary`.

---

### Task 7: manifests, full gates, push

**Step 1:** Bump `plugins/timer/manifest.json` and `plugins/world-clock/manifest.json` to `"protocol": {"major": 1, "minor": 2}` and versions `1.2.0`.
**Step 2:** `gofmt -l .` empty; `go vet ./...`; `go run ./tools/validate-manifests`; `go test -race ./...` — all green.
**Step 3:** Commit `chore(plugins): declare protocol minor 2 for timer and world clock`, push both repos (sysc-shell first).

---

### Task 8: deploy + live verify (laptop)

**Step 1:** On the laptop: pull both repos, `go build -o /tmp/sysc-shell-new ./cmd/sysc-shell` (sysc-shell), `make build && make install` (sysc-plugins).
**Step 2:** Restart the shell (same `/tmp/shell-env.txt` flow), confirm all four plugin processes + no `plugin start failed` lines.
**Step 3:** Screenshots: timer setup panel (accent Start, error Reset, display-size time), running state (panel closed, accent bar countdown), world-clock zone cards. Pull PNGs and view.
**Step 4:** User judgement call on parity; iterate from feedback.
