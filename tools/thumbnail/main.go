// Command thumbnail writes, or checks, the catalog thumbnail every plugin
// ships.
//
//	thumbnail -plugin plugins/<dir>   write plugins/<dir>/thumbnail.webp
//	thumbnail                         write it for every plugin not grandfathered
//	thumbnail -check [-plugin ...]    fail if a committed thumbnail differs
//
// It renders from plugins/<dir>/manifest.json, the plugin's category in
// catalog-meta.json and plugins/<dir>/screenshot.png, so the same inputs
// always produce the same bytes. It runs from the repository root.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/internal/thumbnail"
)

const (
	grandfatheredFile = "tools/thumbnail/grandfathered.txt"
	minShotWidth      = 320
	maxShotBytes      = 4 << 20
)

func main() {
	if err := run(".", os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "thumbnail:", err)
		os.Exit(1)
	}
}

func run(root string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("thumbnail", flag.ContinueOnError)
	flags.SetOutput(stderr)
	plugin := flags.String("plugin", "", "plugin directory, plugins/<dir> (default: every plugin not in "+grandfatheredFile+")")
	check := flags.Bool("check", false, "compare with the committed thumbnail.webp instead of writing it")
	if err := flags.Parse(args); err != nil {
		return err
	}

	meta, err := readCategories(filepath.Join(root, "catalog-meta.json"))
	if err != nil {
		return err
	}
	dirs, err := selectDirs(root, *plugin)
	if err != nil {
		return err
	}

	var failures []string
	for _, dir := range dirs {
		data, warnings, err := build(root, dir, meta)
		for _, w := range warnings {
			fmt.Fprintln(stderr, "warning:", w)
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("plugins/%s: %v", dir, err))
			continue
		}
		path := filepath.Join(root, "plugins", dir, "thumbnail.webp")
		regenerate := "go run ./tools/thumbnail -plugin plugins/" + dir
		if *check {
			have, err := os.ReadFile(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				failures = append(failures, fmt.Sprintf("plugins/%s: missing thumbnail.webp; run: %s", dir, regenerate))
			case err != nil:
				failures = append(failures, fmt.Sprintf("plugins/%s: %v", dir, err))
			case !bytes.Equal(have, data):
				failures = append(failures, fmt.Sprintf("plugins/%s: thumbnail.webp is out of date; regenerate with: %s", dir, regenerate))
			}
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			failures = append(failures, fmt.Sprintf("plugins/%s: %v", dir, err))
			continue
		}
		fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", path, len(data))
	}
	if len(failures) > 0 {
		return errors.New("\n" + strings.Join(failures, "\n"))
	}
	return nil
}

// selectDirs returns the plugin directory names to process: the one named by
// -plugin, or every directory under plugins/ not on the grandfathered list.
func selectDirs(root, plugin string) ([]string, error) {
	if plugin != "" {
		dir := filepath.Base(filepath.Clean(plugin))
		if _, err := os.Stat(filepath.Join(root, "plugins", dir, "manifest.json")); err != nil {
			return nil, fmt.Errorf("-plugin %q: no manifest.json under plugins/%s", plugin, dir)
		}
		return []string{dir}, nil
	}
	grand, err := thumbnail.LoadGrandfathered(filepath.Join(root, grandfatheredFile))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !grand[e.Name()] {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func build(root, dir string, meta map[string]string) ([]byte, []string, error) {
	in, err := loadInput(root, dir, meta)
	if err != nil {
		return nil, nil, err
	}
	if in.Screenshot, err = loadScreenshot(filepath.Join(root, "plugins", dir, "screenshot.png")); err != nil {
		return nil, nil, err
	}
	img, warnings, err := thumbnail.Render(in)
	if err != nil {
		return nil, warnings, err
	}
	data, err := thumbnail.Encode(img)
	if err != nil {
		return nil, warnings, err
	}
	if err := thumbnail.Validate(data); err != nil {
		return nil, warnings, fmt.Errorf("rendered thumbnail %w", err)
	}
	return data, warnings, nil
}

type manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Widgets     []json.RawMessage `json:"widgets"`
	Panels      []json.RawMessage `json:"panels"`
}

// loadInput gathers everything a thumbnail shows except the screenshot.
func loadInput(root, dir string, meta map[string]string) (thumbnail.Input, error) {
	data, err := os.ReadFile(filepath.Join(root, "plugins", dir, "manifest.json"))
	if err != nil {
		return thumbnail.Input{}, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return thumbnail.Input{}, fmt.Errorf("manifest.json: %w", err)
	}
	category, ok := meta[m.ID]
	if !ok || category == "" {
		return thumbnail.Input{}, fmt.Errorf("catalog-meta.json has no category for %q", m.ID)
	}
	return thumbnail.Input{
		Dir: dir, ID: m.ID, Name: m.Name, Description: m.Description,
		Category: category, HasPanel: len(m.Panels) > 0, HasBar: len(m.Widgets) > 0,
	}, nil
}

// readCategories maps plugin id to category from catalog-meta.json.
func readCategories(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make(map[string]string, len(raw))
	for id, row := range raw {
		out[id] = row.Category
	}
	return out, nil
}

// loadScreenshot reads a plugin's capture, which must be a PNG at least
// minShotWidth wide and at most maxShotBytes.
func loadScreenshot(path string) (image.Image, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("missing screenshot.png; capture the plugin's panel (docs/publishing.md, \"Thumbnails\")")
	}
	if err != nil {
		return nil, err
	}
	if info.Size() > maxShotBytes {
		return nil, fmt.Errorf("screenshot.png is %d bytes; keep it under 4 MiB", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("screenshot.png cannot be read: %w", err)
	}
	if format != "png" {
		return nil, fmt.Errorf("screenshot.png is a %s image; it must be a PNG", format)
	}
	if cfg.Width < minShotWidth {
		return nil, fmt.Errorf("screenshot.png is %d px wide; it must be at least %d", cfg.Width, minShotWidth)
	}
	return png.Decode(bytes.NewReader(data))
}
