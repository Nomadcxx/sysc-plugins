// Command sysc-thinkpad-screenshot paints every panel state and one bar sample
// from the ThinkPad plugin to PNGs, using sysc-shell's own renderer.
//
// The view tests prove the panel's geometry lints clean, which is not the same
// as proving it looks right: nothing clips, nothing is cramped, nothing sits in
// a place a person would not choose. This tool closes that gap by rendering the
// exact trees the host would receive, so the result can be looked at.
//
//	go run ./cmd/sysc-thinkpad-screenshot -preview /path/to/sysc-panel-preview
//
// It writes one <state>.json - the wire tree, exactly as it goes to the host -
// and, when -preview names a sysc-panel-preview binary, one <state>.png per
// state. Without -preview it still writes the trees, so they can be rendered
// later or diffed in review.
//
// The preview binary lives in the sysc-shell repository, not this one, so it is
// never vendored or required to build.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/Nomadcxx/sysc-plugins/plugins/thinkpad"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-thinkpad-screenshot:", err)
		os.Exit(1)
	}
}

func run() error {
	outDir := flag.String("outdir", filepath.Join("plugins", "thinkpad", "docs", "screenshots"), "directory for the generated trees and images")
	preview := flag.String("preview", "", "path to a sysc-panel-preview binary; without it only the JSON trees are written")
	root := flag.String("root", ".", "repository root, used to find the plugin's logo asset")
	only := flag.String("state", "", "render one panel state or bar (default: all of them)")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	// Resolve the logo the way the installed binary would, so the hero in the
	// picture carries the real wordmark rather than the fallback glyph.
	exeDir := filepath.Join(*root, "plugins", "thinkpad", "bin")
	cfg := thinkpad.DefaultConfig()
	logo := thinkpad.LogoPath(cfg, exeDir)
	if logo == "" {
		fmt.Fprintln(os.Stderr, "note: no logo asset found, the hero will use the fallback glyph")
	}

	states := thinkpad.ExampleStates()
	names := make([]string, 0, len(states))
	for name := range states {
		if *only == "" || name == *only {
			names = append(names, name)
		}
	}
	if len(names) == 0 && *only != "bar" {
		return fmt.Errorf("no such state %q", *only)
	}
	sort.Strings(names)

	for _, name := range names {
		snap := states[name]
		height := thinkpad.PanelPreferredHeight(snap, "")
		tree := thinkpad.PanelTree(snap, cfg, logo, "")

		blob, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		jsonPath := filepath.Join(*outDir, name+".json")
		if err := os.WriteFile(jsonPath, append(blob, '\n'), 0o644); err != nil {
			return err
		}

		if *preview == "" {
			fmt.Printf("%-12s tree -> %s\n", name, jsonPath)
			continue
		}
		pngPath := filepath.Join(*outDir, name+".png")
		if err := paint(*preview, pngPath, blob, thinkpad.PanelWidth, height); err != nil {
			// One bad state must not hide the others.
			fmt.Fprintf(os.Stderr, "%-12s FAILED: %v\n", name, err)
			continue
		}
		fmt.Printf("%-12s png  -> %s  (%dx%d)\n", name, pngPath, thinkpad.PanelWidth, height)
	}

	if *only == "" || *only == "bar" {
		// BarTree resolves its asset next to the running executable. Point it at
		// the same bundled wordmark used by the panel screenshots.
		cfg.LogoPath = logo
		bar := &v1.Node{Kind: v1.KindColumn, Children: []*v1.Node{
			thinkpad.BarTree(cfg),
		}}
		blob, err := json.MarshalIndent(bar, "", "  ")
		if err != nil {
			return fmt.Errorf("bar: %w", err)
		}
		jsonPath := filepath.Join(*outDir, "bar.json")
		if err := os.WriteFile(jsonPath, append(blob, '\n'), 0o644); err != nil {
			return err
		}
		if *preview == "" {
			fmt.Printf("%-12s tree -> %s\n", "bar", jsonPath)
			return nil
		}
		pngPath := filepath.Join(*outDir, "bar.png")
		if err := paint(*preview, pngPath, blob, 240, 32); err != nil {
			return fmt.Errorf("bar: %w", err)
		}
		fmt.Printf("%-12s png  -> %s  (%dx%d)\n", "bar", pngPath, 240, 32)
	}
	return nil
}

// paint runs the shell's renderer over one tree. The preview tool reads the
// tree on standard input, so nothing has to be written to a temporary file.
func paint(preview, outPath string, blob []byte, width, height int) error {
	cmd := exec.Command(preview,
		"-width", fmt.Sprint(width),
		"-height", fmt.Sprint(height),
		"-o", outPath,
	)
	cmd.Stdin = bytes.NewReader(blob)
	// The renderer is chatty about fontconfig on a bare box; that noise is not
	// the caller's problem unless the render actually fails.
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(string(out)))
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
