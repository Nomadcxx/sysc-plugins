package hermes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func modelOptions(n int) []ModelOption {
	out := make([]ModelOption, 0, n)
	providers := []string{"openrouter", "anthropic", "nous"}
	for i := 0; i < n; i++ {
		p := providers[i%len(providers)]
		out = append(out, ModelOption{Provider: p, Model: fmt.Sprintf("model-%02d", i), Name: fmt.Sprintf("Model %02d (%s)", i, p)})
	}
	return out
}

func modelRowIDs(root *v1.Node) []string {
	var ids []string
	walk(root, func(n *v1.Node) {
		if strings.HasPrefix(n.ID, "model:") {
			ids = append(ids, n.ID)
		}
	})
	return ids
}

func TestModelNodeIDsHashProviderAndModel(t *testing.T) {
	re := regexp.MustCompile(`^model:[0-9a-f]{12}$`)
	a := ModelNodeID(ModelOption{Provider: "openrouter", Model: "m"})
	b := ModelNodeID(ModelOption{Provider: "nous", Model: "m"})
	if !re.MatchString(a) || !re.MatchString(b) || a == b {
		t.Errorf("model ids %q %q", a, b)
	}
	if ModelSig("p", "m") == ModelSig("p:", ":m") {
		t.Error("sig must separate provider and model")
	}
	opts := modelOptions(5)
	for _, o := range opts {
		got, ok := ModelForNode(ModelNodeID(o), opts, "", "")
		if !ok || got != o {
			t.Errorf("ModelForNode(%s) = %+v %v", ModelNodeID(o), got, ok)
		}
	}
}

func TestModelSectionRowsFilterAndDefault(t *testing.T) {
	few := ModelSection(modelOptions(5), ModelPicker{}, "", "", 612, 400)
	if findID(few, "model-filter") != nil {
		t.Error("filter input should appear only above 20 models")
	}
	if len(modelRowIDs(few)) != 5 {
		t.Errorf("rows = %v", modelRowIDs(few))
	}
	many := modelOptions(45)
	tree := ModelSection(many, ModelPicker{}, "", "", 612, 400)
	if f := findID(tree, "model-filter"); f == nil || f.Kind != v1.KindTextInput {
		t.Error("filter input missing above 20 models")
	}
	if got := len(modelRowIDs(tree)); got != 20 {
		t.Errorf("rows = %d, want 20", got)
	}
	filtered := ModelSection(many, ModelPicker{Filter: "model-4"}, "", "", 612, 400)
	if got := len(modelRowIDs(filtered)); got != 5 {
		t.Errorf("filter model-4 rows = %d, want 5 (40..44)", got)
	}
	byProvider := ModelSection(many, ModelPicker{Provider: "nous"}, "", "", 612, 400)
	if got := len(modelRowIDs(byProvider)); got != 15 {
		t.Errorf("provider nous rows = %d, want 15", got)
	}
	// Provider choices: Current/default, then each provider.
	var seg *v1.Node
	walk(tree, func(n *v1.Node) {
		if n.Kind == v1.KindSegmented && seg == nil {
			seg = n
		}
	})
	if seg == nil || len(seg.Children) < 2 || seg.Children[0].Text != "Current/default" || !seg.Children[0].Selected {
		t.Fatalf("provider choices = %+v", seg)
	}
	// A configured default not in the list is shown first.
	withDefault := ModelSection(modelOptions(3), ModelPicker{}, "custom", "my-default", 612, 400)
	ids := modelRowIDs(withDefault)
	want := ModelNodeID(ModelOption{Provider: "custom", Model: "my-default"})
	if len(ids) != 4 || ids[0] != want {
		t.Errorf("default first: ids = %v, want %s first", ids, want)
	}
	if o, ok := ModelForNode(want, modelOptions(3), "custom", "my-default"); !ok || o.Model != "my-default" {
		t.Errorf("default model does not map back: %+v %v", o, ok)
	}
	empty := ModelSection(nil, ModelPicker{}, "", "", 612, 400)
	if !strings.Contains(strings.Join(texts(empty), "|"), "No models reported by the bridge") {
		t.Errorf("empty models = %v", texts(empty))
	}
	for _, id := range []string{"model-persist", "model-apply"} {
		if n := findID(tree, id); n == nil || n.Kind != v1.KindButton {
			t.Errorf("model section has no %s button", id)
		}
	}
	lintView(t, "model section", &v1.Node{Kind: v1.KindColumn, Width: 640, Height: 520, Padding: 14, Children: []*v1.Node{tree}}, v1.ViewPanel, 640, 520)
}

func TestApplyModelSendsProviderModelPersist(t *testing.T) {
	req, err := ApplyModel(ModelPicker{Provider: "nous", Model: "model-01", Persist: true}, "openrouter")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(req)
	if string(raw) != `{"provider":"nous","model":"model-01","persist":true}` {
		t.Errorf("body = %s", raw)
	}
	req, err = ApplyModel(ModelPicker{Model: "model-02"}, "openrouter")
	if err != nil || req.Provider != "openrouter" || req.Persist {
		t.Errorf("Current/default should use the live provider: %+v %v", req, err)
	}
	for _, p := range []ModelPicker{
		{Provider: "nous", Model: "  "},
		{Provider: "nous", Model: "a b"},
		{Provider: "nous", Model: "--global"},
		{Provider: "-x", Model: "m"},
		{Model: "m"},
	} {
		if _, err := ApplyModel(p, ""); err == nil || err.Error() != "Invalid model or provider name" {
			t.Errorf("ApplyModel(%+v) err = %v, want local refusal", p, err)
		}
	}
}

func TestPickerSeedsUntilTouched(t *testing.T) {
	seeded := SeedPicker(ModelPicker{}, "openrouter", "live-model")
	if seeded.Provider != "openrouter" || seeded.Model != "live-model" {
		t.Errorf("untouched picker = %+v", seeded)
	}
	touched := SeedPicker(ModelPicker{Provider: "nous", Model: "mine", Dirty: true}, "openrouter", "live-model")
	if touched.Provider != "nous" || touched.Model != "mine" {
		t.Errorf("touched picker reseeded: %+v", touched)
	}
}
