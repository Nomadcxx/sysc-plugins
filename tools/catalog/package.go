package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var validArch = map[string]bool{"amd64": true, "arm64": true}

// packageResult is the JSON line `catalog package` prints on success.
type packageResult struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func runPackage(args []string) error {
	fs := flag.NewFlagSet("package", flag.ContinueOnError)
	plugin := fs.String("plugin", "", "plugin directory under plugins/")
	arch := fs.String("arch", "", "amd64 or arm64")
	out := fs.String("out", "", "directory to write the archive into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *plugin == "" || *arch == "" || *out == "" {
		return fmt.Errorf("package: -plugin, -arch and -out are all required")
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	res, err := packagePlugin(repoRoot, *plugin, *arch, *out)
	if err != nil {
		return err
	}
	line, err := json.Marshal(res)
	if err != nil {
		return err
	}
	fmt.Println(string(line))
	return nil
}

// packagePlugin builds plugins/<pluginDir>'s binary for arch and writes a
// deterministic release tarball into outDir. repoRoot is the sysc-plugins
// checkout root: it is both where plugins/<pluginDir> and cmd/<cmd> are
// resolved from, and the `go build` working directory, so the build always
// uses that checkout's go.mod/go.sum.
func packagePlugin(repoRoot, pluginDir, arch, outDir string) (packageResult, error) {
	if !validArch[arch] {
		return packageResult{}, fmt.Errorf("package: arch must be amd64 or arm64, got %q", arch)
	}
	pluginRoot := filepath.Join(repoRoot, "plugins", pluginDir)
	m, err := readManifest(pluginRoot)
	if err != nil {
		return packageResult{}, fmt.Errorf("package: %w", err)
	}

	cmdName := "sysc-plugin-" + pluginDir
	cmdDir := filepath.Join(repoRoot, "cmd", cmdName)
	if info, err := os.Stat(cmdDir); err != nil || !info.IsDir() {
		return packageResult{}, fmt.Errorf("package: cmd/%s does not exist", cmdName)
	}
	wantExec := "bin/" + cmdName
	if m.Exec != wantExec {
		return packageResult{}, fmt.Errorf("package: manifest exec is %q, want %q", m.Exec, wantExec)
	}

	mtime, err := resolveMtime(repoRoot)
	if err != nil {
		return packageResult{}, fmt.Errorf("package: %w", err)
	}

	buildDir, err := os.MkdirTemp("", "sysc-catalog-build-*")
	if err != nil {
		return packageResult{}, err
	}
	defer os.RemoveAll(buildDir)
	binPath := filepath.Join(buildDir, cmdName)
	if err := buildBinary(repoRoot, "./cmd/"+cmdName, arch, binPath); err != nil {
		return packageResult{}, fmt.Errorf("package: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return packageResult{}, err
	}
	archiveName := fmt.Sprintf("%s-%s-linux-%s.tar.gz", m.ID, m.Version, arch)
	archivePath := filepath.Join(outDir, archiveName)
	size, sum, err := writeArchive(archivePath, pluginRoot, m, binPath, mtime)
	if err != nil {
		return packageResult{}, fmt.Errorf("package: %w", err)
	}
	return packageResult{Path: archivePath, Size: size, SHA256: sum}, nil
}

// buildBinary runs the pinned, reproducible build the release workflow
// relies on: no cgo, a fixed OS/arch pair, trimmed paths, and no build id (Go
// embeds one by default, and it varies run to run unless suppressed).
func buildBinary(repoRoot, pkg, arch, out string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-buildid=", "-o", out, pkg)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build %s: %w\n%s", pkg, err, output)
	}
	return nil
}

// resolveMtime is the single timestamp stamped onto every archive entry, so
// two packaging runs against the same commit produce byte-identical
// archives. SOURCE_DATE_EPOCH (https://reproducible-builds.org/specs/source-date-epoch/)
// takes precedence for testing and for a workflow that wants to pin a value
// other than the tagged commit's own time.
func resolveMtime(repoRoot string) (time.Time, error) {
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		sec, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH %q: %w", v, err)
		}
		return time.Unix(sec, 0).UTC(), nil
	}
	cmd := exec.Command("git", "-C", repoRoot, "log", "-1", "--format=%ct")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("git log -1 --format=%%ct: %w", err)
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("git log -1 --format=%%ct: %w", err)
	}
	return time.Unix(sec, 0).UTC(), nil
}

// tarEntry is one file or directory to write into the archive, keyed by its
// full path inside the tarball (including the leading "<id>/").
type tarEntry struct {
	archivePath string
	isDir       bool
	source      string // disk path for a regular file; unused for a directory
	mode        int64  // 0 means the writer's default for that entry kind
}

// writeArchive builds the deterministic tarball D3 and the controller
// ruling describe: the plugin directory minus *.go, testdata/ and bin/, plus
// the built binary at the manifest's exec path, under a top-level directory
// named for the plugin id. Entries are written in sorted order with uid/gid
// 0 and a single mtime, so packaging twice yields the same bytes.
func writeArchive(archivePath, pluginRoot string, m pluginManifest, binPath string, mtime time.Time) (int64, string, error) {
	entries, err := collectEntries(pluginRoot, m.ID)
	if err != nil {
		return 0, "", err
	}
	execArchivePath := path.Join(m.ID, filepath.ToSlash(m.Exec))
	addParentDirs(entries, execArchivePath, m.ID)
	entries[execArchivePath] = tarEntry{archivePath: execArchivePath, source: binPath, mode: 0o755}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	f, err := os.Create(archivePath)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	sum := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, sum))
	tw := tar.NewWriter(gz)

	for _, name := range names {
		e := entries[name]
		hdr := &tar.Header{
			Name:    e.archivePath,
			ModTime: mtime,
			Format:  tar.FormatUSTAR,
		}
		if e.isDir {
			hdr.Typeflag = tar.TypeDir
			hdr.Name += "/"
			hdr.Mode = 0o755
			if err := tw.WriteHeader(hdr); err != nil {
				return 0, "", err
			}
			continue
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		info, err := os.Stat(e.source)
		if err != nil {
			return 0, "", err
		}
		hdr.Typeflag = tar.TypeReg
		hdr.Mode = mode
		hdr.Size = info.Size()
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, "", err
		}
		if err := copyFile(tw, e.source); err != nil {
			return 0, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, "", err
	}
	if err := gz.Close(); err != nil {
		return 0, "", err
	}
	if err := f.Sync(); err != nil {
		return 0, "", err
	}
	info, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(sum.Sum(nil)), nil
}

func copyFile(w io.Writer, source string) error {
	rf, err := os.Open(source)
	if err != nil {
		return err
	}
	defer rf.Close()
	_, err = io.Copy(w, rf)
	return err
}

// collectEntries walks pluginRoot and returns every entry the archive keeps,
// keyed by archive path under archiveRoot (the plugin id): directories and
// regular files, minus *.go source, testdata/ and bin/ (the build's own
// output directory, which the caller replaces with the freshly built
// binary).
func collectEntries(pluginRoot, archiveRoot string) (map[string]tarEntry, error) {
	entries := map[string]tarEntry{archiveRoot: {archivePath: archiveRoot, isDir: true}}
	err := filepath.WalkDir(pluginRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(pluginRoot, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		archivePath := path.Join(archiveRoot, filepath.ToSlash(rel))
		if d.IsDir() {
			if name == "testdata" || name == "bin" {
				return filepath.SkipDir
			}
			entries[archivePath] = tarEntry{archivePath: archivePath, isDir: true}
			return nil
		}
		if filepath.Ext(name) == ".go" {
			return nil
		}
		entries[archivePath] = tarEntry{archivePath: archivePath, source: p}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// addParentDirs ensures every directory between archiveRoot and archivePath
// (exclusive of both) has a directory entry, for paths (such as bin/) that
// collectEntries skipped from the source walk but the binary still needs.
func addParentDirs(entries map[string]tarEntry, archivePath, archiveRoot string) {
	dir := path.Dir(archivePath)
	for dir != archiveRoot && dir != "." && dir != "/" {
		if _, ok := entries[dir]; !ok {
			entries[dir] = tarEntry{archivePath: dir, isDir: true}
		}
		dir = path.Dir(dir)
	}
}
