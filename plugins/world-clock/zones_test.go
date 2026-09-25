package worldclock

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ids(zones []Zone) []string {
	out := make([]string, len(zones))
	for i, z := range zones {
		out[i] = z.ID
	}
	return out
}

func TestDecodeLegacyStrings(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`["Europe/Paris","bad","Europe/Paris","Asia/Tokyo"]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Zone{{ID: "Europe/Paris", OnBar: true}, {ID: "Asia/Tokyo", OnBar: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zones = %+v", got)
	}
}

func TestDecodeObjectsDefaultsOnBarAndClampsLabel(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`[{"id":"Asia/Tokyo","label":"  Office  "},{"id":"UTC","on_bar":false},{"id":"Local"},{"id":""},{"nope":1},{"id":"Europe/Berlin","label":"` + strings.Repeat("x", 30) + `"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Zone{
		{ID: "Asia/Tokyo", Label: "Office", OnBar: true},
		{ID: "UTC", OnBar: false},
		{ID: "Europe/Berlin", Label: strings.Repeat("x", MaxLabelRunes), OnBar: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zones = %+v", got)
	}
}

func TestDecodeKeepsZonesWithMalformedOptionalFields(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`[{"id":"Asia/Tokyo","label":5,"on_bar":false},{"id":"Europe/Paris","label":"Office","on_bar":"yes"},{"id":"Australia/Sydney","label":null,"on_bar":null},{"id":5,"label":"bad id"}]`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Zone{
		{ID: "Asia/Tokyo", OnBar: false},
		{ID: "Europe/Paris", Label: "Office", OnBar: true},
		{ID: "Australia/Sydney", OnBar: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zones = %+v, want %+v", got, want)
	}
}

func TestDecodeEmptyListStaysEmpty(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(`[]`))
	if err != nil || len(got) != 0 {
		t.Fatalf("zones = %+v err = %v", got, err)
	}
}

func TestDecodeRejectsNonList(t *testing.T) {
	t.Parallel()
	if _, err := Decode([]byte(`{"not":"a list"}`)); err == nil {
		t.Fatal("object accepted as a zone list")
	}
}

func TestEncodeRoundTrips(t *testing.T) {
	t.Parallel()
	in := []Zone{{ID: "Asia/Tokyo", Label: "Office", OnBar: false}}
	raw, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(raw)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip = %+v err = %v", out, err)
	}
}

func TestNewStoreSeedsDefaults(t *testing.T) {
	t.Parallel()
	if got := ids(NewStore().Zones()); !reflect.DeepEqual(got, DefaultZones) {
		t.Fatalf("defaults = %v", got)
	}
}

func TestAddRejectsDuplicateAndInvalid(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := s.Add("UTC", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate err = %v", err)
	}
	if err := s.Add("Not/AZone", ""); !errors.Is(err, ErrInvalidZone) {
		t.Fatalf("invalid err = %v", err)
	}
	if err := s.Add("Australia/Sydney", "Home"); err != nil {
		t.Fatal(err)
	}
	last := s.Zones()[len(s.Zones())-1]
	if last != (Zone{ID: "Australia/Sydney", Label: "Home", OnBar: true}) {
		t.Fatalf("added = %+v", last)
	}
}

func TestRenameAndToggleBar(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.StartRename("UTC")
	if !s.Rename("UTC", "  Server  ") || s.Label("UTC") != "Server" || s.Renaming() != "" {
		t.Fatalf("rename: label=%q renaming=%q", s.Label("UTC"), s.Renaming())
	}
	if !s.Rename("UTC", "") || s.Label("UTC") != "" {
		t.Fatal("empty rename did not reset the label")
	}
	if !s.ToggleBar("UTC") || s.Zones()[0].OnBar {
		t.Fatal("toggle did not hide UTC")
	}
	if s.Rename("Nope/Zone", "x") || s.ToggleBar("Nope/Zone") {
		t.Fatal("mutated a missing zone")
	}
}

func TestStoreReorderAtEveryInsertionIndex(t *testing.T) {
	t.Parallel()
	want := map[int][]string{
		0: {"Asia/Tokyo", "UTC", "Europe/Paris"},
		1: {"UTC", "Asia/Tokyo", "Europe/Paris"},
		2: {"UTC", "Europe/Paris", "Asia/Tokyo"},
		3: {"UTC", "Europe/Paris", "Asia/Tokyo"},
	}
	for insert, w := range want {
		s := NewStore()
		s.Load([]Zone{{ID: "UTC", OnBar: true}, {ID: "Europe/Paris", OnBar: true}, {ID: "Asia/Tokyo", OnBar: true}})
		changed := s.Reorder("Asia/Tokyo", insert)
		if got := ids(s.Zones()); !reflect.DeepEqual(got, w) {
			t.Fatalf("insert %d: %v", insert, got)
		}
		if changed != (insert < 2) {
			t.Fatalf("insert %d: changed = %v", insert, changed)
		}
	}
	s := NewStore()
	if s.Reorder("Nope/Zone", 0) || s.Reorder("UTC", -1) || s.Reorder("UTC", 99) {
		t.Fatal("bad reorder reported a change")
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.ProposeDelete("UTC")
	if s.PendingDelete() != "UTC" || len(s.Zones()) != 4 {
		t.Fatal("delete applied before confirm")
	}
	if !s.ConfirmDelete() || s.Has("UTC") || s.PendingDelete() != "" {
		t.Fatal("confirm did not delete")
	}
	if s.ConfirmDelete() {
		t.Fatal("confirm without a pending delete reported a change")
	}
}

func TestEditStateClearsWhenZoneGoes(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.StartRename("UTC")
	s.ProposeDelete("Asia/Tokyo")
	if s.Renaming() != "" || s.PendingDelete() != "Asia/Tokyo" {
		t.Fatal("rename and delete confirm must be exclusive")
	}
	s.StartRename("UTC")
	s.Load([]Zone{{ID: "Asia/Tokyo", OnBar: true}})
	if s.Renaming() != "" || s.PendingDelete() != "" {
		t.Fatal("reload kept edit state for a missing zone")
	}
	s.StartRename("Nope/Zone")
	if s.Renaming() != "" {
		t.Fatal("started renaming a missing zone")
	}
}
