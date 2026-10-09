package capture

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// repoWith builds a minimal repository with one plugin, "demo", whose first
// panel is 300x200.
func repoWith(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(modulePath+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "plugins", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const demoManifest = `{"panels":[{"id":"panel","width":300,"height":200}]}`

// standIn writes a shell script that plays sysc-panel-preview. Its body runs
// after the argument loop, which sets $out to the -o value and logs the
// arguments and standard input under $CAPTURE_LOG.
func standIn(t *testing.T, body string) (command, logDir string) {
	t.Helper()
	logDir = t.TempDir()
	t.Setenv("CAPTURE_LOG", logDir)
	fixture := filepath.Join(logDir, "fixture.png")
	f, err := os.Create(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Setenv("CAPTURE_FIXTURE", fixture)

	script := `#!/bin/sh
printf '%s\n' "$@" > "$CAPTURE_LOG/args"
cat > "$CAPTURE_LOG/stdin"
while [ $# -gt 0 ]; do if [ "$1" = "-o" ]; then out="$2"; fi; shift; done
` + body + "\n"
	command = filepath.Join(t.TempDir(), "sysc-panel-preview")
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return command, logDir
}

func okTree() *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: 8, Children: []*v1.Node{{Kind: v1.KindText, Text: "hello"}}}
}

func TestRenderRunsTheCommandAtTheManifestSize(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, logs := standIn(t, `cp "$CAPTURE_FIXTURE" "$out"`)

	out, err := render(root, "demo", okTree(), command)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "plugins", "demo", "screenshot.png")
	if out != want {
		t.Fatalf("wrote %s, want %s", out, want)
	}
	args, _ := os.ReadFile(filepath.Join(logs, "args"))
	for _, w := range []string{"-width\n300", "-height\n200", "-o\n" + want} {
		if !strings.Contains(string(args), w) {
			t.Errorf("arguments %q lack %q", args, w)
		}
	}
	stdin, _ := os.ReadFile(filepath.Join(logs, "stdin"))
	var sent v1.Node
	if err := json.Unmarshal(stdin, &sent); err != nil || sent.Kind != v1.KindColumn || len(sent.Children) != 1 {
		t.Fatalf("the command was sent %q, want the panel tree as JSON (err %v)", stdin, err)
	}
}

func TestRenderRefusesATreeThatDoesNotLayOutBeforeRunningAnything(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, logs := standIn(t, `cp "$CAPTURE_FIXTURE" "$out"`)
	// An 8-padded row 28 tall leaves 12 px for a 20 px icon: the host refuses it.
	bad := &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
		{Kind: v1.KindRow, Height: 28, Padding: 8, Children: []*v1.Node{{Kind: v1.KindIcon, Icon: "battery_full", IconSize: 20}}},
	}}
	_, err := render(root, "demo", bad, command)
	if err == nil || !strings.Contains(err.Error(), "does not lay out at 300x200") || !strings.Contains(err.Error(), "root.children[0]") {
		t.Fatalf("err = %v, want a layout refusal naming the node", err)
	}
	if _, statErr := os.Stat(filepath.Join(logs, "args")); statErr == nil {
		t.Error("the command ran for a tree that cannot lay out")
	}
}

func TestRenderReportsTheCommandsFailureWithoutFontScanNoise(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, _ := standIn(t, `echo "fontscan 2026/10/09 using system font dirs" >&2; echo "sysc-panel-preview: boom" >&2; exit 1`)
	_, err := render(root, "demo", okTree(), command)
	if err == nil || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "fontscan") {
		t.Fatalf("err = %v, want the command's own message and none of the font-scan log", err)
	}
}

func TestRenderRejectsAnOutputThatIsNotAPNG(t *testing.T) {
	root := repoWith(t, demoManifest)
	command, _ := standIn(t, `echo hello > "$out"`)
	if _, err := render(root, "demo", okTree(), command); err == nil || !strings.Contains(err.Error(), "not a PNG") {
		t.Fatalf("err = %v, want a not-a-PNG failure", err)
	}
}

func TestRenderNeedsAPanelWithASize(t *testing.T) {
	for name, manifest := range map[string]string{
		"no panels":  `{"panels":[]}`,
		"zero width": `{"panels":[{"id":"p","width":0,"height":200}]}`,
		"bad json":   `{`,
	} {
		t.Run(name, func(t *testing.T) {
			root := repoWith(t, manifest)
			command, _ := standIn(t, `cp "$CAPTURE_FIXTURE" "$out"`)
			if _, err := render(root, "demo", okTree(), command); err == nil {
				t.Fatal("rendered without a usable panel size")
			}
		})
	}
}

func TestFindCommandPrefersTheEnvironmentThenPathThenExplainsHowToInstall(t *testing.T) {
	t.Setenv(envCommand, "/opt/custom/sysc-panel-preview")
	if got, err := findCommand(); err != nil || got != "/opt/custom/sysc-panel-preview" {
		t.Fatalf("findCommand = %q, %v; want the environment's path", got, err)
	}

	t.Setenv(envCommand, "")
	t.Setenv("PATH", t.TempDir())
	_, err := findCommand()
	if err == nil || !strings.Contains(err.Error(), installHint) || !strings.Contains(err.Error(), envCommand) {
		t.Fatalf("err = %v, want the install line and the override variable", err)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, commandName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got, err := findCommand(); err != nil || got != filepath.Join(bin, commandName) {
		t.Fatalf("findCommand = %q, %v; want the one on PATH", got, err)
	}
}

func TestRepoRootWalksUpToTheModule(t *testing.T) {
	root := repoWith(t, demoManifest)
	t.Chdir(filepath.Join(root, "plugins", "demo"))
	got, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(root); got != want && got != root {
		t.Fatalf("repoRoot = %s, want %s", got, root)
	}

	t.Chdir(t.TempDir())
	if _, err := repoRoot(); err == nil {
		t.Fatal("repoRoot found a module outside the repository")
	}
}

func TestCaptureIsOffUnlessAskedFor(t *testing.T) {
	t.Setenv(envCapture, "")
	if enabled() {
		t.Error("capture is on without CAPTURE=1")
	}
	t.Setenv(envCapture, "0")
	if enabled() {
		t.Error("capture is on for CAPTURE=0")
	}
	t.Setenv(envCapture, "1")
	if !enabled() {
		t.Error("capture is off for CAPTURE=1")
	}
}

func TestGradientWritesADecodablePNGOfTheAskedSize(t *testing.T) {
	path := Gradient(t, 40, 30, color.NRGBA{R: 200, A: 255}, color.NRGBA{B: 200, A: 255})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 30 {
		t.Fatalf("size = %v, want 40x30", b)
	}
	topLeft, bottomRight := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA), color.NRGBAModel.Convert(img.At(39, 29)).(color.NRGBA)
	if topLeft.R <= bottomRight.R || topLeft.B >= bottomRight.B {
		t.Errorf("corners %v and %v do not run from the first colour to the second", topLeft, bottomRight)
	}
	if !strings.HasPrefix(path, os.TempDir()) {
		t.Errorf("wrote %s outside the temp directory", path)
	}
}
