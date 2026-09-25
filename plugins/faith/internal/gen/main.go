// Command gen builds the Faith plugin's bundled data from pinned public-domain
// and CC BY sources. It is run by hand when a source changes, never by the
// build:
//
//	go run ./plugins/faith/internal/gen \
//	    -bible-api <HelloAOLab/bible-api checkout> \
//	    -scrollmapper <scrollmapper/bible_databases checkout> \
//	    -out plugins/faith/data
//
// The output is deterministic: sorted, canonical order, and gzip with no
// name or timestamp, so a rerun on the same sources is byte-identical.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/plugins/faith"
)

func main() {
	api := flag.String("bible-api", "", "HelloAOLab/bible-api checkout")
	sm := flag.String("scrollmapper", "", "scrollmapper/bible_databases checkout")
	out := flag.String("out", "plugins/faith/data", "output directory")
	flag.Parse()
	if *api == "" || *sm == "" {
		fmt.Fprintln(os.Stderr, "gen: -bible-api and -scrollmapper are required")
		os.Exit(2)
	}
	if err := run(*api, *sm, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(api, sm, out string) error {
	outputs := map[string][]byte{}
	counts := map[string]int{}
	omitted := map[string][]string{}
	for _, tr := range []struct{ id, dir, suffix string }{
		{"BSB", "bible/bsb", "BSB.usfm"},
		{"WEB", "bible/engwebp", "engwebp.usfm"},
	} {
		books := make([][]Verse, len(faith.Books))
		for i, b := range faith.Books {
			matches, _ := filepath.Glob(filepath.Join(api, tr.dir, "*"+b.Code+tr.suffix))
			if len(matches) != 1 {
				return fmt.Errorf("%s %s: %d source files", tr.id, b.Code, len(matches))
			}
			f, err := os.Open(matches[0])
			if err != nil {
				return err
			}
			vs, err := ParseUSFM(f)
			f.Close()
			if err != nil {
				return fmt.Errorf("%s %s: %w", tr.id, b.Code, err)
			}
			books[i] = vs
		}
		data, n, om, err := encodeText(books)
		if err != nil {
			return fmt.Errorf("%s: %w", tr.id, err)
		}
		outputs[tr.id+".tsv.gz"], counts[tr.id], omitted[tr.id] = data, n, om
	}
	f, err := os.Open(filepath.Join(sm, "formats/json/KJV.json"))
	if err != nil {
		return err
	}
	kjv, err := ParseKJV(f)
	f.Close()
	if err != nil {
		return err
	}
	data, n, om, err := encodeText(kjv)
	if err != nil {
		return fmt.Errorf("KJV: %w", err)
	}
	outputs["KJV.tsv.gz"], counts["KJV"], omitted["KJV"] = data, n, om

	f, err = os.Open(filepath.Join(sm, "sources/extras/cross_references.txt"))
	if err != nil {
		return err
	}
	xr, err := ParseXrefs(f)
	f.Close()
	if err != nil {
		return err
	}
	data, n, err = encodeXrefs(xr)
	if err != nil {
		return err
	}
	outputs["xrefs.tsv.gz"], counts["xrefs"] = data, n

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(outputs))
	for name, data := range outputs {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return os.WriteFile(filepath.Join(out, "SOURCES.md"), sources(api, sm, names, outputs, counts, omitted), 0o644)
}

// encodeText writes BOOK\tC\tV\tTEXT lines in canonical order and checks
// that every verse carries clean text.
func encodeText(books [][]Verse) ([]byte, int, []string, error) {
	var (
		b       strings.Builder
		omitted []string
	)
	n := 0
	for i, vs := range books {
		if len(vs) == 0 {
			return nil, 0, nil, fmt.Errorf("%s has no verses", faith.Books[i].Code)
		}
		for _, v := range vs {
			if v.Text == "" {
				// Modern critical texts omit a few verses (Luke 17:36 in
				// the WEB); the store steps over the gap.
				omitted = append(omitted, fmt.Sprintf("%s %d:%d", faith.Books[i].Code, v.Chapter, v.Verse))
				continue
			}
			if strings.ContainsAny(v.Text, "\\\t\n|") || strings.Contains(v.Text, "strong=") {
				return nil, 0, nil, fmt.Errorf("%s %d:%d: unclean text %q", faith.Books[i].Code, v.Chapter, v.Verse, v.Text)
			}
			fmt.Fprintf(&b, "%s\t%d\t%d\t%s\n", faith.Books[i].Code, v.Chapter, v.Verse, v.Text)
			n++
		}
	}
	data, err := gz([]byte(b.String()))
	return data, n, omitted, err
}

func encodeXrefs(xr map[string][]string) ([]byte, int, error) {
	type row struct {
		ref  faith.Ref
		refs []string
	}
	rows := make([]row, 0, len(xr))
	for key, refs := range xr {
		r, err := faith.ParseRef(key)
		if err != nil {
			return nil, 0, err
		}
		rows = append(rows, row{r, refs})
	}
	sort.Slice(rows, func(i, j int) bool { return less(rows[i].ref, rows[j].ref) })
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%d\t%d\t%s\n", faith.Books[r.ref.Book].Code, r.ref.Chapter, r.ref.Verse, strings.Join(r.refs, ";"))
	}
	data, err := gz([]byte(b.String()))
	return data, len(rows), err
}

func gz(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func head(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func sources(api, sm string, names []string, outputs map[string][]byte, counts map[string]int, omitted map[string][]string) []byte {
	var b strings.Builder
	b.WriteString(`# Faith bundled data

Generated by ` + "`plugins/faith/internal/gen`" + `. Do not edit these files by hand; rerun the generator.

## Sources

| Output | Source | Commit | Licence |
|---|---|---|---|
`)
	fmt.Fprintf(&b, "| `BSB.tsv.gz` | [HelloAOLab/bible-api](https://github.com/HelloAOLab/bible-api) `bible/bsb/*.usfm` (Berean Standard Bible) | `%s` | Public domain (dedicated 2023-04-30) |\n", head(api))
	fmt.Fprintf(&b, "| `WEB.tsv.gz` | [HelloAOLab/bible-api](https://github.com/HelloAOLab/bible-api) `bible/engwebp/*.usfm` (World English Bible) | `%s` | Public domain |\n", head(api))
	fmt.Fprintf(&b, "| `KJV.tsv.gz` | [scrollmapper/bible_databases](https://github.com/scrollmapper/bible_databases) `formats/json/KJV.json` (King James Version, 1769) | `%s` | Public domain outside the United Kingdom, where Crown letters patent apply |\n", head(sm))
	fmt.Fprintf(&b, "| `xrefs.tsv.gz` | [scrollmapper/bible_databases](https://github.com/scrollmapper/bible_databases) `sources/extras/cross_references.txt`, from [OpenBible.info](https://www.openbible.info/labs/cross-references/) (2024-11-04) | `%s` | CC BY, OpenBible.info. Changed: the five highest-voted positive references per verse are kept, and a range that crosses a chapter keeps its first verse. |\n", head(sm))
	b.WriteString("\n## Outputs\n\n| File | Rows | Bytes | SHA-256 |\n|---|---|---|---|\n")
	for _, name := range names {
		sum := sha256.Sum256(outputs[name])
		fmt.Fprintf(&b, "| `%s` | %d | %d | `%x` |\n", name, counts[strings.TrimSuffix(name, ".tsv.gz")], len(outputs[name]), sum)
	}
	b.WriteString("\n## Omitted verses\n\nVerses a source leaves empty are not written; the store steps over them.\n\n")
	for _, id := range []string{"BSB", "WEB", "KJV"} {
		list := "none"
		if len(omitted[id]) > 0 {
			list = strings.Join(omitted[id], ", ")
		}
		fmt.Fprintf(&b, "- %s: %s\n", id, list)
	}
	return []byte(b.String())
}
