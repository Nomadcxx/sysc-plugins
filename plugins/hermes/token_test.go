package hermes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Every token in these tests is a fake.
const (
	fakePasted = "fake-pasted-token-0001"
	fakeFile   = "fake-file-token-0002"
)

func writeToken(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadTokenTrimsAValidFile(t *testing.T) {
	path := writeToken(t, filepath.Join(t.TempDir(), "bridge.token"), "  "+fakeFile+"\n")
	got, err := ReadToken(path)
	if err != nil || got != fakeFile {
		t.Fatalf("ReadToken = %q, %v; want the trimmed token", got, err)
	}
}

func TestReadTokenMissingOrEmptyMeansNoToken(t *testing.T) {
	dir := t.TempDir()
	for name, path := range map[string]string{
		"missing":    filepath.Join(dir, "absent.token"),
		"empty":      writeToken(t, filepath.Join(dir, "empty.token"), ""),
		"whitespace": writeToken(t, filepath.Join(dir, "blank.token"), " \n\t\n"),
	} {
		got, err := ReadToken(path)
		if !errors.Is(err, ErrNoToken) || got != "" {
			t.Errorf("%s: ReadToken = %q, %v; want ErrNoToken", name, got, err)
		}
	}
}

func TestReadTokenRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.token")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.token")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	big := writeToken(t, filepath.Join(dir, "big.token"), strings.Repeat("a", 4097))
	embedded := writeToken(t, filepath.Join(dir, "nl.token"), "fake-first\nfake-second\n")
	carriage := writeToken(t, filepath.Join(dir, "cr.token"), "fake-first\rfake-second")
	for name, path := range map[string]string{
		"directory": sub, "symlink to directory": link, "fifo": fifo,
		"4 KiB+": big, "embedded newline": embedded, "embedded CR": carriage,
	} {
		got, err := ReadToken(path)
		if err == nil || errors.Is(err, ErrNoToken) || got != "" {
			t.Errorf("%s: ReadToken = %q, %v; want a rejection", name, got, err)
			continue
		}
		if strings.Contains(err.Error(), "fake-") {
			t.Errorf("%s: error %q contains the file's value", name, err)
		}
	}
	exact := writeToken(t, filepath.Join(dir, "exact.token"), strings.Repeat("b", 4096))
	if got, err := ReadToken(exact); err != nil || len(got) != 4096 {
		t.Errorf("a 4096-byte token: len %d, %v; want accepted", len(got), err)
	}
}

func TestClientOnlyPastedBeatsFile(t *testing.T) {
	file := writeToken(t, filepath.Join(t.TempDir(), "hermes.token"), fakeFile+"\n")
	tok, err := TokenSource{ClientOnly: true, Pasted: "  " + fakePasted + " ", File: file}.Resolve(nil)
	if err != nil || tok.Kind != TokenPasted || tok.Value() != fakePasted {
		t.Fatalf("pasted and file: kind %s, %v; want the pasted token", tok.Kind, err)
	}
	tok, err = TokenSource{ClientOnly: true, Pasted: "   ", File: file}.Resolve(nil)
	if err != nil || tok.Kind != TokenFile || tok.Value() != fakeFile || tok.Path != file {
		t.Fatalf("blank paste: kind %s path %q, %v; want the file token", tok.Kind, tok.Path, err)
	}
	tok, err = TokenSource{ClientOnly: true, File: filepath.Join(t.TempDir(), "absent")}.Resolve(nil)
	if err != nil || tok.Kind != TokenNone || tok.Value() != "" {
		t.Fatalf("nothing set: kind %s, %v; want none", tok.Kind, err)
	}
	tok, err = TokenSource{ClientOnly: true, Pasted: "fake-a\nfake-b", File: file}.Resolve(nil)
	if err == nil || tok.Kind != TokenNone || strings.Contains(err.Error(), "fake-") {
		t.Fatalf("pasted CR/LF: kind %s, %v; want a rejection without the value", tok.Kind, err)
	}
}

func TestClientOnlyFileExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeToken(t, filepath.Join(home, ".config/sysc-shell/hermes.token"), fakeFile)
	tok, err := TokenSource{ClientOnly: true, File: DefaultTokenFile}.Resolve(nil)
	if err != nil || tok.Kind != TokenFile || tok.Value() != fakeFile {
		t.Fatalf("default token file: kind %s, %v", tok.Kind, err)
	}
}

func TestLocalCandidatesInOrderFirstAcceptedKept(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	stateDir := t.TempDir()
	src := TokenSource{StateFile: filepath.Join(stateDir, "state.json"), Pasted: fakePasted}
	want := []string{
		filepath.Join(stateDir, "bridge.token"),
		filepath.Join(cache, "sysc-hermes", "bridge.token"),
		filepath.Join(cache, "noctalia-hermes", "bridge.token"),
	}
	got := src.LocalCandidates()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("LocalCandidates = %q, want %q", got, want)
	}
	for i, path := range want {
		writeToken(t, path, fmt.Sprintf("fake-local-%d\n", i))
	}
	var tried []string
	tok, err := src.Resolve(func(token string) bool {
		tried = append(tried, token)
		return token == "fake-local-1"
	})
	if err != nil || tok.Kind != TokenLocal || tok.Value() != "fake-local-1" || tok.Path != want[1] {
		t.Fatalf("Resolve = kind %s path %q, %v; want the second candidate", tok.Kind, tok.Path, err)
	}
	if strings.Join(tried, ",") != "fake-local-0,fake-local-1" {
		t.Fatalf("tried %q, want the first two in order and no more", tried)
	}

	tok, _ = src.Resolve(func(string) bool { return false })
	if tok.Kind != TokenNone || tok.Value() != "" {
		t.Fatalf("nothing accepted: kind %s, want none", tok.Kind)
	}
}

func TestLocalSkipsMissingAndDeduplicates(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	// The default state file lives in the sysc-hermes cache dir, so its
	// sibling token and the cache candidate are the same file.
	src := TokenSource{StateFile: filepath.Join(cache, "sysc-hermes", "state.json")}
	if got := src.LocalCandidates(); len(got) != 2 {
		t.Fatalf("LocalCandidates = %q, want two after de-duplication", got)
	}
	noctalia := writeToken(t, filepath.Join(cache, "noctalia-hermes", "bridge.token"), "fake-noctalia")
	calls := 0
	tok, err := src.Resolve(func(string) bool { calls++; return true })
	if err != nil || tok.Kind != TokenLocal || tok.Path != noctalia || calls != 1 {
		t.Fatalf("Resolve = kind %s path %q calls %d, %v; want the Noctalia token after skipping a missing file", tok.Kind, tok.Path, calls, err)
	}
}

func TestTokenNeverFormatsItsValue(t *testing.T) {
	tok := Token{Kind: TokenPasted, value: fakePasted}
	for _, s := range []string{fmt.Sprint(tok), fmt.Sprintf("%v %+v %#v %s %q", tok, tok, tok, tok, tok), fmt.Sprintf("%v", &tok)} {
		if strings.Contains(s, fakePasted) {
			t.Fatalf("formatted token %q contains the value", s)
		}
	}
}
