package v1

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// timerPanel is the richest tree Milestone 6A has to carry: the Timer panel,
// with a themed column, a live countdown, a duration field, and three buttons.
func timerPanel() *Node {
	return &Node{
		Kind:    KindColumn,
		Padding: 12,
		Gap:     8,
		Children: []*Node{
			{Kind: KindText, Text: "05:00", Tabular: true},
			{Kind: KindProgress, Value: 0.4},
			{
				Kind: KindTextInput, ID: "duration", Key: "duration",
				Text: "5m", Name: "Duration", Role: "textbox",
				Events: []EventKind{EventChange, EventSubmit},
			},
			{
				Kind: KindRow, Gap: 8,
				Children: []*Node{
					{Kind: KindButton, ID: "start", Text: "Start", Name: "Start", Role: "button", Events: []EventKind{EventActivate}},
					{Kind: KindButton, ID: "pause", Text: "Pause", Name: "Pause", Role: "button", Events: []EventKind{EventActivate}},
					{Kind: KindButton, ID: "reset", Text: "Reset", Name: "Reset", Role: "button", Events: []EventKind{EventActivate}},
				},
			},
		},
	}
}

// chain returns a single-child tree exactly depth levels deep, root included.
func chain(depth int) *Node {
	root := &Node{Kind: KindColumn}
	n := root
	for i := 1; i < depth; i++ {
		child := &Node{Kind: KindColumn}
		n.Children = []*Node{child}
		n = child
	}
	n.Kind = KindText
	n.Text = "leaf"
	return root
}

// wide returns a column holding count text children, so the whole tree is
// count+1 nodes.
func wide(count int) *Node {
	root := &Node{Kind: KindColumn, Children: make([]*Node, count)}
	for i := range root.Children {
		root.Children[i] = &Node{Kind: KindText, Text: "x"}
	}
	return root
}

func TestValidateAcceptsTheTimerPanel(t *testing.T) {
	t.Parallel()

	if err := Validate(timerPanel(), ViewPanel); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsANilRoot(t *testing.T) {
	t.Parallel()

	if err := Validate(nil, ViewPanel); err == nil {
		t.Fatal("Validate accepted a nil root")
	}
}

func TestValidateRejectsAnUnknownKind(t *testing.T) {
	t.Parallel()

	err := Validate(&Node{Kind: "canvas"}, ViewPanel)
	if err == nil {
		t.Fatal("Validate accepted an unknown kind")
	}
	if !strings.Contains(err.Error(), "canvas") {
		t.Fatalf("err = %v, want it to name the kind", err)
	}
}

func TestValidateRejectsDuplicateNodeIDs(t *testing.T) {
	t.Parallel()

	root := &Node{Kind: KindRow, Children: []*Node{
		{Kind: KindButton, ID: "go", Text: "A", Name: "A", Role: "button", Events: []EventKind{EventActivate}},
		{Kind: KindButton, ID: "go", Text: "B", Name: "B", Role: "button", Events: []EventKind{EventActivate}},
	}}
	err := Validate(root, ViewPanel)
	if err == nil {
		t.Fatal("Validate accepted a duplicate node ID")
	}
	if !strings.Contains(err.Error(), "go") {
		t.Fatalf("err = %v, want it to name the duplicated ID", err)
	}
}

func TestValidateRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()

	// Keys carry retained editor identity across revisions; two nodes claiming
	// one key would make the retained buffer ambiguous.
	root := &Node{Kind: KindColumn, Children: []*Node{
		{Kind: KindText, Key: "row", Text: "A"},
		{Kind: KindText, Key: "row", Text: "B"},
	}}
	if err := Validate(root, ViewPanel); err == nil {
		t.Fatal("Validate accepted a duplicate key")
	}
}

func TestValidateBoundsTreeDepth(t *testing.T) {
	t.Parallel()

	if err := Validate(chain(MaxDepth), ViewPanel); err != nil {
		t.Fatalf("depth %d rejected: %v", MaxDepth, err)
	}
	if err := Validate(chain(MaxDepth+1), ViewPanel); err == nil {
		t.Fatalf("depth %d accepted, want rejection", MaxDepth+1)
	}
}

// sized returns a tree of exactly count nodes that stays inside the per-node
// child limit, so a node-count test cannot be satisfied by the child check.
func sized(count int) *Node {
	root := &Node{Kind: KindColumn}
	remaining := count - 1
	for remaining > 0 {
		group := &Node{Kind: KindColumn}
		root.Children = append(root.Children, group)
		remaining--
		for n := 0; n < MaxChildren && remaining > 0; n++ {
			group.Children = append(group.Children, &Node{Kind: KindText, Text: "x"})
			remaining--
		}
	}
	return root
}

func TestValidateBoundsNodeCount(t *testing.T) {
	t.Parallel()

	if err := Validate(sized(MaxNodes), ViewPanel); err != nil {
		t.Fatalf("%d nodes rejected: %v", MaxNodes, err)
	}
	if err := Validate(sized(MaxNodes+1), ViewPanel); err == nil {
		t.Fatalf("%d nodes accepted, want rejection", MaxNodes+1)
	}
}

func TestValidateBoundsChildrenPerNode(t *testing.T) {
	t.Parallel()

	if err := Validate(wide(MaxChildren), ViewPanel); err != nil {
		t.Fatalf("%d children rejected: %v", MaxChildren, err)
	}
	if err := Validate(wide(MaxChildren+1), ViewPanel); err == nil {
		t.Fatalf("%d children accepted, want rejection", MaxChildren+1)
	}
}

func TestValidateBoundsTextPerNode(t *testing.T) {
	t.Parallel()

	at := &Node{Kind: KindText, Text: strings.Repeat("a", MaxTextBytes)}
	if err := Validate(at, ViewPanel); err != nil {
		t.Fatalf("text at the limit rejected: %v", err)
	}
	over := &Node{Kind: KindText, Text: strings.Repeat("a", MaxTextBytes+1)}
	if err := Validate(over, ViewPanel); err == nil {
		t.Fatal("text over the limit accepted")
	}
}

func TestValidateRequiresAccessibleIdentityOnInteractiveNodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		node *Node
	}{
		{"no id", &Node{Kind: KindButton, Text: "Go", Name: "Go", Role: "button", Events: []EventKind{EventActivate}}},
		{"no name", &Node{Kind: KindButton, ID: "go", Text: "Go", Role: "button", Events: []EventKind{EventActivate}}},
		{"no role", &Node{Kind: KindButton, ID: "go", Text: "Go", Name: "Go", Events: []EventKind{EventActivate}}},
		{"no events", &Node{Kind: KindButton, ID: "go", Text: "Go", Name: "Go", Role: "button"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := Validate(c.node, ViewPanel); err == nil {
				t.Fatalf("Validate accepted an interactive node with %s", c.name)
			}
		})
	}
}

func TestValidateRejectsEventsOnNodesThatCannotEmitThem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		node *Node
	}{
		{"text emits activate", &Node{Kind: KindText, Text: "x", Events: []EventKind{EventActivate}}},
		{"button emits change", &Node{Kind: KindButton, ID: "b", Text: "x", Name: "x", Role: "button", Events: []EventKind{EventChange}}},
		{"input emits pointer", &Node{Kind: KindTextInput, ID: "i", Name: "i", Role: "textbox", Events: []EventKind{EventPointer}}},
		{"unknown event", &Node{Kind: KindButton, ID: "b", Text: "x", Name: "x", Role: "button", Events: []EventKind{"levitate"}}},
		{"duplicate event", &Node{Kind: KindButton, ID: "b", Text: "x", Name: "x", Role: "button", Events: []EventKind{EventActivate, EventActivate}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := Validate(c.node, ViewPanel); err == nil {
				t.Fatalf("Validate accepted %s", c.name)
			}
		})
	}
}

func TestValidateRejectsChildrenOnLeafKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []NodeKind{KindText, KindIcon, KindProgress, KindButton, KindTextInput} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			n := &Node{Kind: kind, ID: "x", Name: "x", Role: "r", Icon: "clear-day", Text: "x",
				Events: []EventKind{EventActivate}, Children: []*Node{{Kind: KindText, Text: "child"}}}
			if kind == KindTextInput {
				n.Events = []EventKind{EventChange}
			}
			if err := Validate(n, ViewPanel); err == nil {
				t.Fatalf("Validate accepted children on %s", kind)
			}
		})
	}
}

func TestValidateBoundsProgressValue(t *testing.T) {
	t.Parallel()

	if err := Validate(&Node{Kind: KindProgress, Value: 1}, ViewPanel); err != nil {
		t.Fatalf("progress 1 rejected: %v", err)
	}
	for _, v := range []float64{-0.1, 1.1} {
		if err := Validate(&Node{Kind: KindProgress, Value: v}, ViewPanel); err == nil {
			t.Fatalf("progress %v accepted", v)
		}
	}
}

func TestValidateRejectsNonFiniteProgress(t *testing.T) {
	t.Parallel()

	// JSON has no NaN literal, but a malformed float can still reach layout
	// through arithmetic on the host side; reject it at the boundary.
	nan := &Node{Kind: KindProgress, Value: math.NaN()}
	if err := Validate(nan, ViewPanel); err == nil {
		t.Fatal("Validate accepted a non-finite progress value")
	}
}

func TestValidateRequiresAnIconName(t *testing.T) {
	t.Parallel()

	if err := Validate(&Node{Kind: KindIcon}, ViewPanel); err == nil {
		t.Fatal("Validate accepted an icon with no name")
	}
	if err := Validate(&Node{Kind: KindIcon, Icon: "../../etc/passwd"}, ViewPanel); err == nil {
		t.Fatal("Validate accepted an icon name that is not an identifier")
	}
	if err := Validate(&Node{Kind: KindIcon, Icon: "clear-day"}, ViewPanel); err != nil {
		t.Fatalf("Validate rejected a plain icon name: %v", err)
	}
}

func TestValidateButtonIcon(t *testing.T) {
	t.Parallel()

	ok := &Node{Kind: KindButton, ID: "camera", Icon: "camera",
		Name: "Open screen recorder", Role: "button", Events: []EventKind{EventActivate}}
	if err := Validate(ok, ViewBar); err != nil {
		t.Fatalf("Validate rejected a button with a catalogue icon: %v", err)
	}
	if err := Validate(&Node{Kind: KindButton, ID: "b", Icon: "../../etc/passwd",
		Name: "x", Role: "button", Events: []EventKind{EventActivate}}, ViewPanel); err == nil {
		t.Fatal("Validate accepted an icon name that is not an identifier")
	}
}

func TestValidateRejectsNegativeGeometry(t *testing.T) {
	t.Parallel()

	for _, n := range []*Node{
		{Kind: KindText, Text: "x", Width: -1},
		{Kind: KindText, Text: "x", MaxWidth: -1},
		{Kind: KindRow, Padding: -1},
		{Kind: KindRow, Gap: -1},
	} {
		if err := Validate(n, ViewPanel); err == nil {
			t.Fatalf("Validate accepted negative geometry on %+v", n)
		}
	}
}

func TestValidateBoundsGeometry(t *testing.T) {
	t.Parallel()

	if err := Validate(&Node{Kind: KindText, Text: "x", Width: MaxExtent + 1}, ViewPanel); err == nil {
		t.Fatal("Validate accepted a width past the extent ceiling")
	}
}

func TestBarViewsRejectKeyboardFields(t *testing.T) {
	t.Parallel()

	// A bar view has no keyboard focus of its own, so a field placed there
	// could never be typed into.
	root := &Node{Kind: KindRow, Children: []*Node{
		{Kind: KindTextInput, ID: "i", Name: "i", Role: "textbox", Events: []EventKind{EventChange}},
	}}
	if err := Validate(root, ViewBar); err == nil {
		t.Fatal("bar view accepted a text input")
	}
	if err := Validate(root, ViewPanel); err != nil {
		t.Fatalf("panel view rejected a text input: %v", err)
	}
}

func TestBarViewsAcceptPointerControls(t *testing.T) {
	t.Parallel()

	root := &Node{Kind: KindRow, Children: []*Node{
		{Kind: KindText, Text: "05:00", Tabular: true},
		{Kind: KindButton, ID: "open", Text: "Timer", Name: "Open timer", Role: "button",
			Events: []EventKind{EventActivate, EventPointer}},
	}}
	if err := Validate(root, ViewBar); err != nil {
		t.Fatalf("bar view rejected a pointer control: %v", err)
	}
}

func TestTooltipViewsAreReadOnly(t *testing.T) {
	t.Parallel()

	for _, n := range []*Node{
		{Kind: KindButton, ID: "b", Text: "x", Name: "x", Role: "button", Events: []EventKind{EventActivate}},
		{Kind: KindTextInput, ID: "i", Name: "i", Role: "textbox", Events: []EventKind{EventChange}},
	} {
		root := &Node{Kind: KindColumn, Children: []*Node{n}}
		if err := Validate(root, ViewTooltip); err == nil {
			t.Fatalf("tooltip accepted an interactive %s", n.Kind)
		}
	}

	ok := &Node{Kind: KindColumn, Children: []*Node{
		{Kind: KindText, Text: "Timer"},
		{Kind: KindText, Text: "04:12 remaining", Tabular: true},
		{Kind: KindRow, Children: []*Node{
			{Kind: KindText, Text: "Humidity"},
			{Kind: KindText, Text: "40%", Tabular: true},
		}},
	}}
	if err := Validate(ok, ViewTooltip); err != nil {
		t.Fatalf("tooltip rejected read-only content: %v", err)
	}

	for _, n := range []*Node{
		{Kind: KindList, Height: 40},
		{Kind: KindDragSource, ID: "d", Name: "d", Role: "button", Events: []EventKind{EventPointer}},
		{Kind: KindDropZone, ID: "z", Accept: []string{"zone"}, Events: []EventKind{EventDrop}},
	} {
		root := &Node{Kind: KindColumn, Children: []*Node{n}}
		if err := Validate(root, ViewTooltip); err == nil {
			t.Fatalf("tooltip accepted a %s", n.Kind)
		}
	}
}

func TestValidateRejectsAnUnknownViewKind(t *testing.T) {
	t.Parallel()

	if err := Validate(&Node{Kind: KindText, Text: "x"}, ViewKind("wallpaper")); err == nil {
		t.Fatal("Validate accepted an unknown view kind")
	}
}

func TestValidateRejectsAnUnknownTone(t *testing.T) {
	t.Parallel()

	if err := Validate(&Node{Kind: KindText, Text: "x", Tone: "chartreuse"}, ViewPanel); err == nil {
		t.Fatal("Validate accepted an unknown tone")
	}
}

func TestValidateRejectsAnOversizedNodeID(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", MaxIdentBytes+1)
	n := &Node{Kind: KindButton, ID: long, Text: "x", Name: "x", Role: "button", Events: []EventKind{EventActivate}}
	if err := Validate(n, ViewPanel); err == nil {
		t.Fatal("Validate accepted an oversized node ID")
	}
}

func TestValidateRejectsListAndDragOutsideAPanel(t *testing.T) {
	t.Parallel()
	list := &Node{Kind: KindRow, Children: []*Node{{Kind: KindList, Height: 80, Children: []*Node{
		{Kind: KindText, Text: "x"},
	}}}}
	if err := Validate(list, ViewBar); err == nil {
		t.Fatal("a bar accepted a list")
	}
	drag := &Node{Kind: KindColumn, Children: []*Node{{
		Kind: KindDragSource, ID: "h", Text: "=", Name: "Reorder", Role: "button",
		Events: []EventKind{EventPointer},
	}}}
	if err := Validate(drag, ViewBar); err == nil {
		t.Fatal("a bar accepted a drag handle")
	}
	unnamed := &Node{Kind: KindColumn, Children: []*Node{{
		Kind: KindDragSource, ID: "h", Text: "=", Role: "button",
		Events: []EventKind{EventPointer},
	}}}
	if err := Validate(unnamed, ViewPanel); err == nil {
		t.Fatal("a nameless drag handle was accepted")
	}
}

func TestValidateKeepsEditorFlagsOnTextInput(t *testing.T) {
	t.Parallel()
	ok := &Node{Kind: KindColumn, Children: []*Node{{
		Kind: KindTextInput, ID: "n", Key: "note", Name: "Note", Role: "textbox",
		Multiline: true, SubmitOnEnter: true, Reseed: 2,
		Events: []EventKind{EventChange},
	}}}
	if err := Validate(ok, ViewPanel); err != nil {
		t.Fatalf("text input rejected editor flags: %v", err)
	}
	bad := &Node{Kind: KindColumn, Children: []*Node{{
		Kind: KindButton, ID: "b", Name: "Go", Role: "button",
		Multiline: true, Events: []EventKind{EventActivate},
	}}}
	if err := Validate(bad, ViewPanel); err == nil {
		t.Fatal("a button accepted multiline")
	}
}

func TestInputEventJSONOmitsIME(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(&InputEvent{Node: "n", Event: EventChange, Text: "typed"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "preedit") {
		t.Fatalf("IME leaked onto the wire: %s", raw)
	}
}
