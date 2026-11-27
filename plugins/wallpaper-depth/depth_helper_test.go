package wallpaperdepth

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestDepthHelperCapsSourcePixels keeps the helper's check aligned with the
// host's 64 Mi pixel mask limit. The limit is enforced by Python before the
// wallpaper is converted or any full-size buffer is allocated.
func TestDepthHelperCapsSourcePixels(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not available")
	}

	const loadModule = `import importlib.util
spec = importlib.util.spec_from_file_location("depth_helper", "depth_helper.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
`

	tests := []struct {
		name     string
		width    int
		height   int
		wantErr  bool
		wantText string
	}{
		{name: "exact limit is accepted", width: 8192, height: 8192},
		{name: "one row over the limit is refused", width: 8192, height: 8193, wantErr: true, wantText: "maximum supported size"},
		{name: "invalid dimensions are refused", width: 0, height: 10, wantErr: true, wantText: "invalid dimensions"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := loadModule + "module.validate_source_size(" + strconv.Itoa(test.width) + ", " + strconv.Itoa(test.height) + ")\n"
			cmd := exec.Command(python, "-c", call)
			output, err := cmd.CombinedOutput()
			if test.wantErr {
				if err == nil {
					t.Fatalf("validate_source_size(%d, %d) accepted an oversized source", test.width, test.height)
				}
				if !strings.Contains(string(output), test.wantText) {
					t.Fatalf("error %q does not mention %q", output, test.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate_source_size(%d, %d) failed: %v\n%s", test.width, test.height, err, output)
			}
		})
	}
}
