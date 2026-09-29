package integration

import (
	"os"
	"os/exec"
	"path/filepath"
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
