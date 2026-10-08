package updates

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runnerWith(stdout, stderr []byte, exit int, err error) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		return stdout, stderr, exit, err
	}
}

func TestCheckRepoParsesArrowLines(t *testing.T) {
	run := runnerWith(sample(t, "checkupdates.txt"), nil, 0, nil)
	updates, err := CheckRepo(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 3 {
		t.Fatalf("got %d updates, want 3: %+v", len(updates), updates)
	}
	first := updates[0]
	if first.Name != "linux" || first.Old != "6.17.1.arch1-1" || first.New != "6.17.2.arch1-1" {
		t.Fatalf("first update = %+v", first)
	}
	if first.Source != SourceRepo || !first.Core {
		t.Fatalf("linux should be a repo core update: %+v", first)
	}
	if !updates[1].Core {
		t.Fatalf("mesa should be core: %+v", updates[1])
	}
	if updates[2].Core {
		t.Fatalf("firefox should not be core: %+v", updates[2])
	}
}

func TestCheckRepoExitTwoMeansNoUpdates(t *testing.T) {
	run := runnerWith(nil, nil, 2, nil)
	updates, err := CheckRepo(context.Background(), run)
	if err != nil || updates != nil {
		t.Fatalf("got %v, %v; want no updates and no error", updates, err)
	}
}

func TestCheckRepoReportsStderrReason(t *testing.T) {
	run := runnerWith(nil, sample(t, "checkupdates-error.txt"), 1, nil)
	_, err := CheckRepo(context.Background(), run)
	if err == nil {
		t.Fatal("want error on exit 1")
	}
	if !strings.Contains(err.Error(), "Cannot fetch updates") {
		t.Fatalf("error %q should carry the stderr reason", err)
	}
}

func TestCheckRepoReportsRunnerFailure(t *testing.T) {
	run := runnerWith(nil, nil, -1, os.ErrNotExist)
	_, err := CheckRepo(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "checkupdates") {
		t.Fatalf("got %v, want an error naming checkupdates", err)
	}
}

func TestCheckAURStripsIgnoredTags(t *testing.T) {
	run := runnerWith(sample(t, "paru.txt"), nil, 0, nil)
	updates, err := CheckAUR(context.Background(), run, "paru")
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 3 {
		t.Fatalf("got %d updates, want 3: %+v", len(updates), updates)
	}
	if updates[0].New != "2.0.5-1" {
		t.Fatalf("ignored tag should be stripped: %+v", updates[0])
	}
	if updates[0].Source != SourceAUR {
		t.Fatalf("source = %q, want aur", updates[0].Source)
	}
	if !updates[2].Core {
		t.Fatalf("nvidia-utils should be core: %+v", updates[2])
	}
}

func TestCheckAURYaySample(t *testing.T) {
	run := runnerWith(sample(t, "yay.txt"), nil, 0, nil)
	updates, err := CheckAUR(context.Background(), run, "yay")
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].Name != "world-clock-git" {
		t.Fatalf("got %+v", updates)
	}
}

func TestCheckAURNonZeroEmptyIsNoUpdates(t *testing.T) {
	run := runnerWith(nil, nil, 1, nil)
	updates, err := CheckAUR(context.Background(), run, "paru")
	if err != nil || updates != nil {
		t.Fatalf("got %v, %v; want no updates and no error", updates, err)
	}
}

func TestCheckFlatpak(t *testing.T) {
	run := runnerWith(sample(t, "flatpak.txt"), nil, 0, nil)
	updates, err := CheckFlatpak(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2: %+v", len(updates), updates)
	}
	if updates[0].Name != "org.mozilla.firefox" || updates[0].New != "143.0.2" || updates[0].Old != "" {
		t.Fatalf("first flatpak update = %+v", updates[0])
	}
	if updates[0].Source != SourceFlatpak {
		t.Fatalf("source = %q, want flatpak", updates[0].Source)
	}
}

func TestParseArrowLinesSkipsMalformed(t *testing.T) {
	updates := parseArrowLines([]byte("garbage\nfoo 1.0-1 -> 2.0-1\nnot an arrow\n"), SourceRepo)
	if len(updates) != 1 || updates[0].Name != "foo" || updates[0].New != "2.0-1" {
		t.Fatalf("got %+v, want just foo", updates)
	}
}

func TestIsCore(t *testing.T) {
	for name, want := range map[string]bool{
		"linux":       true,
		"linux-lts":   true,
		"nvidia-dkms": true,
		"systemd":     true,
		"glibc":       true,
		"mesa":        true,
		"amd-ucode":   true,
		"intel-ucode": true,
		"firefox":     false,
		"bash":        false,
		"vim":         false,
	} {
		if got := isCore(name); got != want {
			t.Errorf("isCore(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCounts(t *testing.T) {
	updates := []Update{
		{Name: "linux", Source: SourceRepo, Core: true},
		{Name: "firefox", Source: SourceRepo},
		{Name: "paru-bin", Source: SourceAUR},
		{Name: "org.mozilla.firefox", Source: SourceFlatpak},
	}
	repo, aur, flatpak := Counts(updates)
	if repo != 2 || aur != 1 || flatpak != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", repo, aur, flatpak)
	}
	if got := CoreCount(updates); got != 1 {
		t.Fatalf("CoreCount = %d, want 1", got)
	}
}
