package thinkpad

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "sysc-plugins", "thinkpad.json")
}

func TestStarterProfilesAreSeededOnFirstRun(t *testing.T) {
	path := tempStorePath(t)
	s := OpenStore(path)

	if issue := s.LoadIssue(); issue != "" {
		t.Fatalf("LoadIssue = %q, want none for a missing file", issue)
	}
	names := profileNames(s)
	want := []string{"default", "night", "bright"} // seed order, as written
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("seeded %v, want %v", names, want)
	}
	if s.LastUsed() != "default" {
		t.Errorf("LastUsed = %q, want default", s.LastUsed())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("OpenStore wrote the file before anything asked it to")
	}

	// NewController seeds on first run; that is the only thing that writes.
	if err := s.seedSeeded(); err != nil {
		t.Fatalf("seedSeeded: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the seeded file was not written: %v", err)
	}
	var f storeFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("seeded file is not valid JSON: %v", err)
	}
	if f.Version != storeVersion {
		t.Errorf("Version = %d, want %d", f.Version, storeVersion)
	}
	// A second call must not rewrite, so a user's edits are never clobbered.
	if err := s.seedSeeded(); err != nil {
		t.Fatalf("seedSeeded again: %v", err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(raw) {
		t.Error("seedSeeded rewrote the file on a second call")
	}
}

// The whole point of the store is surviving a reboot, so a save has to be
// readable by the next process.
func TestSaveRoundTripsThroughDisk(t *testing.T) {
	path := tempStorePath(t)
	s := OpenStore(path)
	if err := s.Save(Profile{Name: "gaming", Settings: Settings{
		FanLevel: "4", KeyboardOn: true, KeyboardLevel: 3, LidOn: true,
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reopened := OpenStore(path)
	if issue := reopened.LoadIssue(); issue != "" {
		t.Fatalf("LoadIssue = %q after a clean save", issue)
	}
	got, ok := reopened.Profile("gaming")
	if !ok {
		t.Fatal("the saved profile did not come back")
	}
	want := Settings{FanLevel: "4", KeyboardOn: true, KeyboardLevel: 3, LidOn: true}
	if !got.Settings.Equal(want) {
		t.Errorf("round-tripped %+v, want %+v", got.Settings, want)
	}
	if len(reopened.Profiles()) != 4 {
		t.Errorf("len(Profiles()) = %d, want 4", len(reopened.Profiles()))
	}
}

func TestSaveReplacesByNameAndIsCaseInsensitive(t *testing.T) {
	s := OpenStore(tempStorePath(t))
	if err := s.Save(Profile{Name: "night", Settings: Settings{FanLevel: "1"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(Profile{Name: "NIGHT", Settings: Settings{FanLevel: "2"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n := len(s.Profiles()); n != 3 {
		t.Fatalf("len(Profiles()) = %d, want 3; Save must replace, not append", n)
	}
	got, _ := s.Profile("night")
	if got.Settings.FanLevel != "2" {
		t.Errorf("FanLevel = %q, want the second save to win", got.Settings.FanLevel)
	}
}

// A hand-edited file that no longer parses must never be silently replaced:
// the user may have kept notes in it, and a plugin that eats them on first run
// is worse than one that starts from defaults.
func TestCorruptFileIsReportedAndLeftAlone(t *testing.T) {
	path := tempStorePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "{ this is not json, and it is mine\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	s := OpenStore(path)
	if issue := s.LoadIssue(); issue == "" {
		t.Fatal("LoadIssue is empty for an unparsable file")
	}
	if !strings.Contains(s.LoadIssue(), path) {
		t.Errorf("LoadIssue = %q, want it to name the path", s.LoadIssue())
	}
	// The plugin still needs something to show.
	if len(s.Profiles()) != 0 {
		t.Errorf("Profiles() = %v, want empty for a corrupt file", profileNames(s))
	}
	if err := s.seedSeeded(); err != nil {
		t.Fatalf("seedSeeded: %v", err)
	}
	if got := readFile(t, path); got != original {
		t.Errorf("the corrupt file was rewritten:\n%q", got)
	}

	// A deliberate save is the user saying it is fine to overwrite.
	if err := s.Save(Profile{Name: "fresh", Settings: Settings{FanLevel: FanLevelAuto}}); err != nil {
		t.Fatalf("Save over a corrupt file: %v", err)
	}
	if got := readFile(t, path); strings.Contains(got, "this is not json") {
		t.Error("an explicit save did not take effect")
	}
}

func TestSaveRejectsNamesThatWouldBreakThePanel(t *testing.T) {
	s := OpenStore(tempStorePath(t))
	for _, name := range []string{"", "   ", strings.Repeat("x", 41), "bad\x00name", "bad\x07name"} {
		if err := s.Save(Profile{Name: name}); err == nil {
			t.Errorf("Save(%q) = nil, want an error", name)
		}
	}
	if n := len(s.Profiles()); n != 3 {
		t.Errorf("len(Profiles()) = %d, want the 3 starters and no rejects", n)
	}
	if err := s.Save(Profile{Name: strings.Repeat("x", 40)}); err != nil {
		t.Errorf("a 40-character name should be allowed: %v", err)
	}
}

// Anything read off disk goes through the same validation as the whitelist the
// hardware layer uses, so a stale file cannot aim a bad value at the kernel.
func TestSanitizeKeepsADiskFileFromPushingBadValuesAtTheHardware(t *testing.T) {
	path := tempStorePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"profiles":[
		{"name":"  DAILY  ","settings":{"fan_level":"  AUTO  ","keyboard_on":true,"keyboard_level":99,"lid_on":true}},
		{"name":"broken","settings":{"fan_level":"9; rm -rf /","keyboard_on":false,"keyboard_level":-5,"lid_on":false}},
		{"name":"blank","settings":{}}
	],"last_used":"daily"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	s := OpenStore(path)
	if issue := s.LoadIssue(); issue != "" {
		t.Fatalf("LoadIssue = %q, want none", issue)
	}
	daily, ok := s.Profile("DAILY")
	if !ok {
		t.Fatal("the profile was not found after trimming its name")
	}
	if daily.Settings.FanLevel != FanLevelAuto {
		t.Errorf("FanLevel = %q, want the trimmed lowercased auto", daily.Settings.FanLevel)
	}
	if daily.Settings.KeyboardLevel != MaxKeyboardLevel {
		t.Errorf("KeyboardLevel = %d, want it clamped to %d", daily.Settings.KeyboardLevel, MaxKeyboardLevel)
	}
	broken, _ := s.Profile("broken")
	if broken.Settings.FanLevel != "" {
		t.Errorf("FanLevel = %q, want an unsupported level dropped, not written", broken.Settings.FanLevel)
	}
	if broken.Settings.KeyboardLevel != 0 {
		t.Errorf("KeyboardLevel = %d, want a negative clamped to 0", broken.Settings.KeyboardLevel)
	}
	if blank, _ := s.Profile("blank"); blank.Settings.FanLevel != "" {
		t.Errorf("an empty profile came back with fan level %q", blank.Settings.FanLevel)
	}
}

func TestDuplicateNamesOnDiskCollapseToOne(t *testing.T) {
	path := tempStorePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"profiles":[
		{"name":"work","settings":{"fan_level":"1"}},
		{"name":"WORK","settings":{"fan_level":"2"}},
		{"name":"","settings":{}}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := OpenStore(path)
	if n := len(s.Profiles()); n != 1 {
		t.Fatalf("len(Profiles()) = %d, want 1 after case-folding and dropping the blank name", n)
	}
	if got := s.Profiles()[0].Settings.FanLevel; got != "1" {
		t.Errorf("FanLevel = %q, want the first entry to win", got)
	}
}

func TestDeleteRefusesTheLastProfile(t *testing.T) {
	s := OpenStore(tempStorePath(t))
	for _, name := range []string{"bright", "default"} {
		if err := s.Delete(name); err != nil {
			t.Fatalf("Delete(%q): %v", name, err)
		}
	}
	if n := len(s.Profiles()); n != 1 {
		t.Fatalf("len(Profiles()) = %d, want 1", n)
	}
	if err := s.Delete("night"); err == nil {
		t.Error("deleting the last profile = nil, want an error")
	}
	if n := len(s.Profiles()); n != 1 {
		t.Errorf("len(Profiles()) = %d after the refused delete, want 1", n)
	}
	if err := s.Delete("nonexistent"); err == nil {
		t.Error("deleting an unknown profile = nil, want an error")
	}
}

func TestDeleteOfTheActiveProfileClearsLastUsed(t *testing.T) {
	s := OpenStore(tempStorePath(t))
	if err := s.Remember("night"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("night"); err != nil {
		t.Fatal(err)
	}
	if s.LastUsed() != "" {
		t.Errorf("LastUsed = %q, want it cleared rather than pointing at a deleted profile", s.LastUsed())
	}
}

func TestRenameMovesTheProfileAndItsLastUsedPointer(t *testing.T) {
	s := OpenStore(tempStorePath(t))
	before, ok := s.Profile("night")
	if !ok {
		t.Fatal("the night profile vanished")
	}
	if err := s.Rename("night", "dark room"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok := s.Profile("dark room"); !ok {
		t.Error("the renamed profile is not findable")
	}
	if _, ok := s.Profile("night"); ok {
		t.Error("the old name still resolves")
	}
	after, ok := s.Profile("dark room")
	if !ok {
		t.Fatal("the renamed profile is not findable")
	}
	if !before.Settings.Equal(after.Settings) {
		t.Errorf("Rename changed the settings: %+v -> %+v", before.Settings, after.Settings)
	}

	if err := s.Rename("default", "bright"); err == nil {
		t.Error("renaming onto an existing name = nil, want an error")
	}
	if err := s.Rename("nothing", "x"); err == nil {
		t.Error("renaming an unknown profile = nil, want an error")
	}
	if err := s.Rename("default", "  "); err == nil {
		t.Error("renaming to a blank name = nil, want an error")
	}
}

func TestRememberFollowsARename(t *testing.T) {
	s := OpenStore(tempStorePath(t))
	if err := s.Remember("bright"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("bright", "loud"); err != nil {
		t.Fatal(err)
	}
	if s.LastUsed() != "loud" {
		t.Errorf("LastUsed = %q, want loud", s.LastUsed())
	}
}

func TestStoreFileIsWrittenPrivate(t *testing.T) {
	path := tempStorePath(t)
	s := OpenStore(path)
	if err := s.seedSeeded(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestWritesAreAtomicSoNoTempFilesAreLeftBehind(t *testing.T) {
	path := tempStorePath(t)
	s := OpenStore(path)
	for i := 0; i < 5; i++ {
		if err := s.Save(Profile{Name: "p", Settings: Settings{FanLevel: "0"}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only the store file", names)
	}
}

func TestDefaultStorePathHonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	want := filepath.Join("/tmp/xdg-state", "sysc-plugins", "thinkpad.json")
	if got := DefaultStorePath(); got != want {
		t.Errorf("DefaultStorePath() = %q, want %q", got, want)
	}
}

func TestSettingsSummaryDescribesAProfileForTheListRow(t *testing.T) {
	s := Settings{FanLevel: FanLevelAuto, KeyboardOn: true, KeyboardLevel: 2, LidOn: false}
	if got := s.Summary(); got != "fan auto · backlight 2 · lid off" {
		t.Errorf("Summary() = %q", got)
	}
	if got := (Settings{KeyboardOn: false}).Summary(); !strings.Contains(got, "backlight off") {
		t.Errorf("Summary() = %q, want it to say the backlight is off", got)
	}
	if got := (Settings{FanLevel: FanLevelDisengaged, LidOn: true}).Summary(); !strings.Contains(got, "fan full") {
		t.Errorf("Summary() = %q, want disengaged shown as full speed", got)
	}
}

func profileNames(s *Store) []string {
	out := []string{}
	for _, p := range s.Profiles() {
		out = append(out, p.Name)
	}
	return out
}
