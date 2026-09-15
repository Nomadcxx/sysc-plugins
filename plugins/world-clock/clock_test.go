package worldclock

import (
	"testing"
	"time"
)

func TestValidateZoneUsesIANA(t *testing.T) {
	t.Parallel()
	if err := ValidateZone("Australia/Sydney"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateZone("Not/AZone"); err == nil {
		t.Fatal("invalid zone accepted")
	}
}

func TestProposeAddRejectsDuplicates(t *testing.T) {
	t.Parallel()
	c := New()
	if err := c.ProposeAdd("UTC"); err == nil {
		t.Fatal("duplicate UTC accepted")
	}
	if err := c.ProposeAdd("Europe/Paris"); err != nil {
		t.Fatal(err)
	}
	if c.PendingAdd() != "Europe/Paris" {
		t.Fatal("add was not pending")
	}
	c.ConfirmAdd()
	if got := c.Zones(); len(got) != 2 || got[1] != "Europe/Paris" {
		t.Fatalf("zones = %v", got)
	}
}

func TestRemoveWaitsForConfirmation(t *testing.T) {
	t.Parallel()
	c := New()
	_ = c.ProposeAdd("Europe/Paris")
	c.ConfirmAdd()
	c.ProposeRemove("Europe/Paris")
	if len(c.Zones()) != 2 {
		t.Fatal("remove applied before confirm")
	}
	c.ConfirmRemove()
	if got := c.Zones(); len(got) != 1 || got[0] != "UTC" {
		t.Fatalf("zones = %v", got)
	}
}

func TestRestoreKeepsOrderAndDropsJunk(t *testing.T) {
	t.Parallel()
	c := New()
	c.Restore([]string{"Europe/Paris", "bad", "Europe/Paris", "Asia/Tokyo"})
	got := c.Zones()
	if len(got) != 2 || got[0] != "Europe/Paris" || got[1] != "Asia/Tokyo" {
		t.Fatalf("zones = %v", got)
	}
}

func TestReorderAtEveryInsertionIndex(t *testing.T) {
	t.Parallel()
	for insert := 0; insert <= 3; insert++ {
		c := New()
		c.Restore([]string{"UTC", "Europe/Paris", "Asia/Tokyo"})
		if err := c.Reorder(2, insert); err != nil {
			t.Fatalf("insert %d: %v", insert, err)
		}
		got := c.Zones()
		if len(got) != 3 {
			t.Fatalf("insert %d lost a zone: %v", insert, got)
		}
		seen := map[string]int{}
		for _, z := range got {
			seen[z]++
		}
		for _, z := range []string{"UTC", "Europe/Paris", "Asia/Tokyo"} {
			if seen[z] != 1 {
				t.Fatalf("insert %d: %v", insert, got)
			}
		}
	}
}

func TestOffsetAndClockFormats(t *testing.T) {
	t.Parallel()
	c := New()
	c.Restore([]string{"UTC"})
	now := time.Date(2026, 6, 1, 15, 4, 0, 0, time.UTC)
	r := c.Readings(now)
	if len(r) != 1 || r[0].Clock != "15:04" || r[0].Offset != "UTC+0" {
		t.Fatalf("24h = %+v", r)
	}
	c.SetHour24(false)
	r = c.Readings(now)
	if r[0].Clock != "3:04 PM" {
		t.Fatalf("12h = %q", r[0].Clock)
	}
}
