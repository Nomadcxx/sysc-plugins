package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repoRootForTest returns the sysc-plugins checkout root. Package tests run
// with the working directory set to tools/catalog, so the root is two levels
// up; it is a real checkout with a real go.mod, never a synthetic fixture,
// because packaging a real (small) plugin is how this test proves the
// deterministic build and archive shape end to end.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %q has no go.mod: %v", root, err)
	}
	return root
}

func readTarEntries(t *testing.T, archivePath string) []*tar.Header {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("not a gzip stream: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var headers []*tar.Header
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		headers = append(headers, hdr)
	}
	return headers
}

// TestPackagePluginTimer builds the real, small timer plugin from this
// checkout and proves the archive shape, permissions, determinism and
// content the plan requires, all without ever writing into plugins/timer/bin.
func TestPackagePluginTimer(t *testing.T) {
	repoRoot := repoRootForTest(t)
	m, err := readManifest(filepath.Join(repoRoot, "plugins", "timer"))
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}

	binDir := filepath.Join(repoRoot, "plugins", "timer", "bin")
	binBefore := dirState(t, binDir)
	outDir := t.TempDir()
	res, err := packagePlugin(repoRoot, "timer", "amd64", outDir)
	if err != nil {
		t.Fatalf("packagePlugin: %v", err)
	}

	wantName := fmt.Sprintf("%s-%s-linux-amd64.tar.gz", m.ID, m.Version)
	if got := filepath.Base(res.Path); got != wantName {
		t.Fatalf("archive name = %q, want %q", got, wantName)
	}
	if filepath.Dir(res.Path) != outDir {
		t.Fatalf("archive written to %q, want under %q", res.Path, outDir)
	}
	// make install builds into plugins/timer/bin, so the directory may exist;
	// packaging must leave it exactly as it found it.
	if after := dirState(t, binDir); after != binBefore {
		t.Fatalf("packaging wrote into plugins/timer/bin: before %q, after %q", binBefore, after)
	}

	info, err := os.Stat(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != res.Size {
		t.Fatalf("reported size %d, actual %d", res.Size, info.Size())
	}
	if res.SHA256 == "" || len(res.SHA256) != 64 {
		t.Fatalf("sha256 %q is not 64 hex digits", res.SHA256)
	}

	headers := readTarEntries(t, res.Path)
	if len(headers) == 0 {
		t.Fatal("archive has no entries")
	}

	names := make([]string, len(headers))
	byName := make(map[string]*tar.Header, len(headers))
	for i, h := range headers {
		names[i] = h.Name
		byName[h.Name] = h
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("archive entries are not sorted: %v", names)
	}

	topDir := m.ID + "/"
	for _, name := range names {
		if !strings.HasPrefix(name, topDir) {
			t.Fatalf("entry %q is outside the top-level %q directory", name, topDir)
		}
		if strings.HasSuffix(strings.TrimSuffix(name, "/"), ".go") {
			t.Fatalf("entry %q is a Go source file; those must be excluded", name)
		}
		if strings.Contains(name, "/testdata/") {
			t.Fatalf("entry %q is under testdata/; that must be excluded", name)
		}
	}
	for _, h := range headers {
		if h.Uid != 0 || h.Gid != 0 {
			t.Fatalf("entry %q has uid/gid %d/%d, want 0/0", h.Name, h.Uid, h.Gid)
		}
	}

	manifestHdr, ok := byName[topDir+"manifest.json"]
	if !ok {
		t.Fatalf("archive has no %smanifest.json", topDir)
	}
	if manifestHdr.Mode != 0o644 {
		t.Fatalf("manifest.json mode = %o, want 0644", manifestHdr.Mode)
	}

	execName := topDir + m.Exec
	execHdr, ok := byName[execName]
	if !ok {
		t.Fatalf("archive has no exec entry %q", execName)
	}
	if execHdr.Mode != 0o755 {
		t.Fatalf("exec entry mode = %o, want 0755", execHdr.Mode)
	}
	if execHdr.Typeflag != tar.TypeReg {
		t.Fatalf("exec entry is not a regular file")
	}

	for _, h := range headers {
		if h.Typeflag == tar.TypeDir && h.Mode != 0o755 {
			t.Fatalf("directory %q mode = %o, want 0755", h.Name, h.Mode)
		}
		if h.Typeflag == tar.TypeReg && h.Name != execName && h.Mode != 0o644 {
			t.Fatalf("file %q mode = %o, want 0644", h.Name, h.Mode)
		}
	}

	// Packaging twice from the same commit must give the same bytes.
	res2, err := packagePlugin(repoRoot, "timer", "amd64", t.TempDir())
	if err != nil {
		t.Fatalf("second packagePlugin: %v", err)
	}
	if res2.SHA256 != res.SHA256 {
		t.Fatalf("packaging twice gave different sha256: %s vs %s", res.SHA256, res2.SHA256)
	}
}

func TestPackagePluginRefusesMissingCmd(t *testing.T) {
	repoRoot := t.TempDir()
	writeFile(t, filepath.Join(repoRoot, "plugins", "ghost", "manifest.json"), `{
		"schema": 1, "id": "org.sysc.ghost", "name": "Ghost", "version": "1.0.0",
		"exec": "bin/sysc-plugin-ghost", "protocol": {"major": 1, "minor": 0}
	}`)
	_, err := packagePlugin(repoRoot, "ghost", "amd64", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when cmd/sysc-plugin-ghost does not exist")
	}
}

func TestPackagePluginRefusesExecMismatch(t *testing.T) {
	repoRoot := t.TempDir()
	writeFile(t, filepath.Join(repoRoot, "plugins", "odd", "manifest.json"), `{
		"schema": 1, "id": "org.sysc.odd", "name": "Odd", "version": "1.0.0",
		"exec": "bin/wrong-name", "protocol": {"major": 1, "minor": 0}
	}`)
	if err := os.MkdirAll(filepath.Join(repoRoot, "cmd", "sysc-plugin-odd"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := packagePlugin(repoRoot, "odd", "amd64", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when manifest exec does not match bin/sysc-plugin-<dir>")
	}
}

func TestPackagePluginRefusesBadArch(t *testing.T) {
	repoRoot := repoRootForTest(t)
	if _, err := packagePlugin(repoRoot, "timer", "riscv64", t.TempDir()); err == nil {
		t.Fatal("expected an error for an unsupported arch")
	}
}

// TestCollectEntries proves the inclusion and exclusion rules against
// synthetic fixtures shaped like the two plugins the plan calls out by name:
// kdeconnect (assets/*.png must survive) and wallpaper-depth (depth_helper.py
// and README.md must survive), both alongside *.go, testdata/ and bin/
// content that must not.
func TestCollectEntries(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		files   []string // relative paths to create, non-empty content
		wantIn  []string // archive paths (under id/) that must be present
		wantOut []string // archive paths that must be absent
	}{
		{
			name: "kdeconnect-like",
			id:   "org.sysc.kdeconnect",
			files: []string{
				"manifest.json",
				"service.go",
				"view.go",
				"service_test.go",
				"assets/desktop.png",
				"assets/phone.png",
				"bin/sysc-plugin-kdeconnect",
				"testdata/fixture.json",
			},
			wantIn: []string{
				"org.sysc.kdeconnect/manifest.json",
				"org.sysc.kdeconnect/assets/desktop.png",
				"org.sysc.kdeconnect/assets/phone.png",
			},
			wantOut: []string{
				"org.sysc.kdeconnect/service.go",
				"org.sysc.kdeconnect/view.go",
				"org.sysc.kdeconnect/service_test.go",
				"org.sysc.kdeconnect/bin/sysc-plugin-kdeconnect",
				"org.sysc.kdeconnect/testdata/fixture.json",
			},
		},
		{
			name: "wallpaper-depth-like",
			id:   "org.sysc.wallpaper-depth",
			files: []string{
				"manifest.json",
				"service.go",
				"service_test.go",
				"depth_helper.py",
				"README.md",
				"bin/sysc-plugin-wallpaper-depth",
			},
			wantIn: []string{
				"org.sysc.wallpaper-depth/manifest.json",
				"org.sysc.wallpaper-depth/depth_helper.py",
				"org.sysc.wallpaper-depth/README.md",
			},
			wantOut: []string{
				"org.sysc.wallpaper-depth/service.go",
				"org.sysc.wallpaper-depth/service_test.go",
				"org.sysc.wallpaper-depth/bin/sysc-plugin-wallpaper-depth",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, rel := range tc.files {
				writeFile(t, filepath.Join(root, rel), "x")
			}
			entries, err := collectEntries(root, tc.id)
			if err != nil {
				t.Fatalf("collectEntries: %v", err)
			}
			for _, want := range tc.wantIn {
				if _, ok := entries[want]; !ok {
					t.Errorf("missing entry %q", want)
				}
			}
			for _, unwanted := range tc.wantOut {
				if _, ok := entries[unwanted]; ok {
					t.Errorf("entry %q must be excluded", unwanted)
				}
			}
		})
	}
}

func TestResolveMtimeSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	got, err := resolveMtime(t.TempDir())
	if err != nil {
		t.Fatalf("resolveMtime: %v", err)
	}
	if got.Unix() != 1700000000 {
		t.Fatalf("mtime = %v, want unix 1700000000", got)
	}
}

func TestResolveMtimeFallsBackToGit(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	got, err := resolveMtime(repoRootForTest(t))
	if err != nil {
		t.Fatalf("resolveMtime: %v", err)
	}
	if got.IsZero() {
		t.Fatal("resolveMtime returned the zero time")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirState summarises a directory's entries, sizes, and modification times,
// or reports that it does not exist.
func dirState(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s:%d:%d;", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}
