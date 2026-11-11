package hermes

import (
	"os"
	"os/exec"
	"testing"
)

// TestBundledBridgeUnitTests runs the bundled bridge's stdlib unittest suite
// (scripts/test_hermes_bridge.py). It skips when python3 is absent.
func TestBundledBridgeUnitTests(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(python, "-B", "-m", "unittest", "-v", "test_hermes_bridge")
	cmd.Dir = "scripts"
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bundled bridge tests failed: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
