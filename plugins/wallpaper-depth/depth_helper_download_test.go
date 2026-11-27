package wallpaperdepth

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDownloadModelStopsAtTheSizeLimit feeds the helper more bytes than the
// expected model size and checks that it refuses before writing the excess.
func TestDownloadModelStopsAtTheSizeLimit(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not available")
	}
	script := `
import importlib.util
import pathlib
import sys
import tempfile

spec = importlib.util.spec_from_file_location("depth_helper", "depth_helper.py")
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)
helper.MODEL_SIZE = 10

class Response:
    def __init__(self):
        self.chunks = [b"12345678", b"abc"]
    def __enter__(self):
        return self
    def __exit__(self, *args):
        return False
    def read(self, _size):
        return self.chunks.pop(0) if self.chunks else b""

helper.urllib.request.urlopen = lambda *args, **kwargs: Response()

with tempfile.TemporaryDirectory() as directory:
    destination = pathlib.Path(directory) / "model.onnx"
    try:
        helper.download_model(destination)
    except RuntimeError as error:
        if "size mismatch" not in str(error):
            print(f"unexpected error: {error}", file=sys.stderr)
            sys.exit(1)
    else:
        print("download did not reject an oversized response", file=sys.stderr)
        sys.exit(1)
    if destination.exists():
        print("oversized model reached its destination", file=sys.stderr)
        sys.exit(1)
    if destination.with_suffix(".onnx.part").exists():
        print("temporary download was not removed", file=sys.stderr)
        sys.exit(1)
`
	cmd := exec.Command(python, "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("download size-limit check failed: %v\n%s", err, strings.TrimSpace(string(output)))
	}
}
