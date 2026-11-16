package hermes

import (
	"errors"
	"strings"
	"unicode"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	maxModelRows      = 20
	maxProviderChoice = 4
	modelRowHeight    = 28
	modelMinHeight    = 3*sectionButtonH + 2*sectionGap

	modelIconSelected = "check"
	modelIconRow      = "chevron_right"
	modelIconPersist  = "verified_user"

	currentDefault = "Current/default"
)

// errInvalidModel is the local refusal for a name the bridge would reject.
var errInvalidModel = errors.New("Invalid model or provider name")

// ModelPicker is the panel's local choice. It seeds from the live model
// until the user touches it; Provider "" means Current/default.
type ModelPicker struct {
	Provider string
	Model    string
	Filter   string
	Persist  bool
	Dirty    bool
}

// ModelRequest is the body of POST /model.
type ModelRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Persist  bool   `json:"persist"`
}

// validModelNameUI refuses what the bridge would: empty names, any
// whitespace, and a leading dash that would read as a flag.
func validModelNameUI(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ModelSig hashes provider and model together; the separator keeps
// ("p:", ":m") apart from ("p", "m").
func ModelSig(provider, model string) string {
	return shortHash(provider + "::" + model)
}

// ModelNodeID is a model row's button id.
func ModelNodeID(o ModelOption) string {
	return "model:" + ModelSig(o.Provider, o.Model)
}

// modelChoices puts a configured default the bridge did not report first.
func modelChoices(opts []ModelOption, defProvider, defModel string) []ModelOption {
	if strings.TrimSpace(defModel) == "" {
		return opts
	}
	for _, o := range opts {
		if o.Model == defModel && (defProvider == "" || o.Provider == defProvider) {
			return opts
		}
	}
	name := defModel + " (default)"
	if defProvider != "" {
		name = defModel + " (" + defProvider + ", default)"
	}
	return append([]ModelOption{{Provider: defProvider, Model: defModel, Name: name}}, opts...)
}

// ModelForNode maps a pressed model row back to its option.
func ModelForNode(id string, opts []ModelOption, defProvider, defModel string) (ModelOption, bool) {
	for _, o := range modelChoices(opts, defProvider, defModel) {
		if ModelNodeID(o) == id {
			return o, true
		}
	}
	return ModelOption{}, false
}

func modelProviders(opts []ModelOption) []string {
	var out []string
	seen := map[string]bool{}
	for _, o := range opts {
		if o.Provider != "" && !seen[o.Provider] {
			seen[o.Provider] = true
			out = append(out, o.Provider)
		}
	}
	return out
}

// ProviderNodeID is a provider choice's button id; "" is Current/default.
func ProviderNodeID(provider string) string {
	if provider == "" {
		return "provider:default"
	}
	return "provider:" + shortHash(provider)
}

// ProviderForNode maps a provider choice back to its provider.
func ProviderForNode(id string, opts []ModelOption) (string, bool) {
	if id == ProviderNodeID("") {
		return "", true
	}
	for _, p := range modelProviders(opts) {
		if ProviderNodeID(p) == id {
			return p, true
		}
	}
	return "", false
}

// SeedPicker follows the live model until the user has touched the picker.
func SeedPicker(p ModelPicker, liveProvider, liveModel string) ModelPicker {
	if p.Dirty {
		return p
	}
	p.Provider, p.Model = liveProvider, liveModel
	return p
}

// ApplyModel builds the /model body, refusing a bad name locally.
// Current/default applies to the live provider.
func ApplyModel(p ModelPicker, liveProvider string) (ModelRequest, error) {
	provider := p.Provider
	if provider == "" {
		provider = liveProvider
	}
	if !validModelNameUI(provider) || !validModelNameUI(p.Model) {
		return ModelRequest{}, errInvalidModel
	}
	return ModelRequest{Provider: provider, Model: p.Model, Persist: p.Persist}, nil
}

func visibleModels(opts []ModelOption, p ModelPicker, defProvider, defModel string) []ModelOption {
	filter := strings.ToLower(strings.TrimSpace(p.Filter))
	var out []ModelOption
	for _, o := range modelChoices(opts, defProvider, defModel) {
		if p.Provider != "" && o.Provider != p.Provider {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(o.Model+" "+o.Name), filter) {
			continue
		}
		out = append(out, o)
		if len(out) == maxModelRows {
			break
		}
	}
	return out
}

func modelNaturalHeight(opts []ModelOption, p ModelPicker, defProvider, defModel string) int {
	h := 2*sectionButtonH + sectionGap
	if len(modelProviders(opts)) > 0 {
		h += sectionButtonH + sectionGap
	}
	if len(opts) > maxModelRows {
		h += sectionButtonH + sectionGap
	}
	if rows := len(visibleModels(opts, p, defProvider, defModel)); rows > 0 {
		h += rows*modelRowHeight - sectionButtonH
		if h < modelMinHeight {
			h = modelMinHeight
		}
	}
	return h
}

// ModelSection is the model picker: provider choices, a filter above
// twenty models, up to twenty rows, Persist and Apply.
func ModelSection(opts []ModelOption, p ModelPicker, defProvider, defModel string, width, height int) *v1.Node {
	col := &v1.Node{Kind: v1.KindColumn, Width: width, Height: height, Gap: sectionGap}
	used := 0
	add := func(n *v1.Node) {
		if len(col.Children) > 0 {
			used += sectionGap
		}
		used += n.Height
		col.Children = append(col.Children, n)
	}
	providers := modelProviders(opts)
	if len(providers) > maxProviderChoice {
		providers = providers[:maxProviderChoice]
	}
	if len(providers) > 0 {
		// Current/default keeps its full label; providers share what is left.
		choices := append([]string{""}, providers...)
		first := buttonWidth(currentDefault, "")
		rest := (width - first - len(providers)*4) / len(providers)
		if natural := buttonWidth(strings.Repeat("m", 12), ""); rest > natural {
			rest = natural
		}
		seg := &v1.Node{Kind: v1.KindSegmented, Width: width, Height: sectionButtonH, Gap: 4}
		for _, c := range choices {
			text, w := c, rest
			if c == "" {
				text, w = currentDefault, first
			}
			b := button(ProviderNodeID(c), clip(text, labelBytes(w, false)), "Provider "+text, "", w, sectionButtonH)
			b.Selected = c == p.Provider
			b.Fill = "soft"
			if b.Selected {
				b.Fill = "accent"
			}
			seg.Children = append(seg.Children, b)
		}
		add(seg)
	}
	if len(opts) > maxModelRows {
		add(&v1.Node{
			Kind: v1.KindTextInput, ID: "model-filter", Key: "model-filter", Name: "Filter models", Role: "textbox",
			Text: p.Filter, Placeholder: "Filter models", Width: width, Height: sectionButtonH, Padding: 6,
			Events: []v1.EventKind{v1.EventChange},
		})
	}
	rows := visibleModels(opts, p, defProvider, defModel)
	footer := &v1.Node{
		Kind: v1.KindRow, Width: width, Height: sectionButtonH, Gap: 8, PinEnd: true,
		Children: []*v1.Node{
			toggle("model-persist", "Persist", "Persist the model choice", modelIconPersist, p.Persist, buttonWidth("Persist", modelIconPersist)),
			button("model-apply", "Apply", "Apply the model", modelIconSelected, buttonWidth("Apply", modelIconSelected), sectionButtonH),
		},
	}
	if len(rows) == 0 {
		text := "No models reported by the bridge"
		if len(opts) > 0 {
			text = "No models match"
		}
		add(&v1.Node{Kind: v1.KindText, Text: text, Size: "caption", Tone: v1.ToneSubtle, Height: lineHeight})
	} else {
		listH := height - used - sectionGap - footer.Height
		if len(col.Children) > 0 {
			listH -= sectionGap
		}
		if listH < modelRowHeight {
			listH = modelRowHeight
		}
		list := &v1.Node{Kind: v1.KindList, Key: "model-list", Width: width, Height: listH}
		for _, o := range rows {
			name := o.Name
			if name == "" {
				name = o.Model
			}
			icon, fill := modelIconRow, "soft"
			if o.Model == p.Model && (p.Provider == "" || o.Provider == p.Provider) {
				icon, fill = modelIconSelected, "accent"
			}
			b := button(ModelNodeID(o), clip(name, labelBytes(width-scrollbarInset, true)), "Model "+name, icon, width-scrollbarInset, modelRowHeight)
			b.Fill = fill
			list.Children = append(list.Children, b)
		}
		add(list)
	}
	add(footer)
	return col
}

// toggle stands in for a checkbox: Selected is legal only inside a
// segmented control, so an on toggle is an accent-filled button.
func toggle(id, text, name, icon string, on bool, width int) *v1.Node {
	b := button(id, text, name, icon, width, sectionButtonH)
	if on {
		b.Fill = "accent"
		b.Name = name + " (on)"
	} else {
		b.Fill = "soft"
		b.Name = name + " (off)"
	}
	return b
}
