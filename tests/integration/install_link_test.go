package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #20: GNU ln -sfn nests the checkout inside a real directory
// instead of replacing it, so `make link` must refuse to link over one.
func TestMakeLinkRefusesRealDirectory(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	plug := t.TempDir()
	dest := filepath.Join(plug, "org.sysc.timer")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-C", root, "link", "PLUGINS=sysc-plugin-timer:timer", "USER_PLUGIN_ROOT=" + plug}
	if out, err := exec.Command("make", args...).CombinedOutput(); err == nil {
		t.Fatalf("make link succeeded over a real directory:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(dest, "timer")); err == nil {
		t.Fatal("make link nested the checkout inside the catalog directory")
	}

	// Once the directory is gone the same link succeeds.
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("make", args...).CombinedOutput(); err != nil {
		t.Fatalf("make link into a clean root failed:\n%s", out)
	}
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is %v, want symlink", dest, fi.Mode())
	}
}

// Issue #124: one cgo plugin's missing headers must not block the others.
func TestMakeLinkSkipsCalendarWithoutLibecal(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	plug := t.TempDir()
	args := []string{"-C", root, "link", "USER_PLUGIN_ROOT=" + plug, "PKG_CONFIG=false"}
	out, err := exec.Command("make", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("make link failed without libecal:\n%s", out)
	}
	if !strings.Contains(string(out), "skipping calendar") {
		t.Fatalf("make link did not mention the calendar skip:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(plug, "org.sysc.calendar")); err == nil {
		t.Fatal("make link linked calendar without libecal")
	}
	entries, err := os.ReadDir(plug)
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := filepath.Glob(filepath.Join(root, "plugins", "*", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := len(manifests) - 1; len(entries) != want {
		t.Fatalf("linked %d plugins, want %d (all but calendar)", len(entries), want)
	}
	if out, err := exec.Command("make", "-C", root, "-n", "build", "PKG_CONFIG=false").CombinedOutput(); err != nil {
		t.Fatalf("make -n build failed:\n%s", out)
	} else if strings.Contains(string(out), "sysc-plugin-calendar") {
		t.Fatalf("dry-run build without libecal still builds calendar:\n%s", out)
	}
}

func TestMakeBuildStrictKeepsCalendarWithoutLibecal(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("make", "-C", repoRoot(t), "-n", "build", "PKG_CONFIG=false", "STRICT=1").CombinedOutput()
	if err != nil {
		t.Fatalf("make -n build STRICT=1 failed:\n%s", out)
	}
	if !strings.Contains(string(out), "sysc-plugin-calendar") {
		t.Fatalf("STRICT=1 did not keep calendar in the build list:\n%s", out)
	}
}
