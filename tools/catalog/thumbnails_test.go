package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeCommit = "0123456789abcdef0123456789abcdef01234567"

// releasedTimerRepo is a repository whose catalog already has a timer row.
func releasedTimerRepo(t *testing.T) string {
	t.Helper()
	root := newFixtureRepo(t, "timer", "org.sysc.timer", "Pomodoro Timer", "1.0.0")
	withReadmeServer(t, http.StatusNotFound, nil)
	dist := t.TempDir()
	writeDistArchive(t, dist, "org.sysc.timer", "1.0.0", "amd64", "v1")
	if err := updateCatalog(root, defaultRepo, "timer-v1.0.0", dist, time.Now().UTC()); err != nil {
		t.Fatalf("updateCatalog: %v", err)
	}
	return root
}

func TestThumbnailsPinsToTheCommit(t *testing.T) {
	root := releasedTimerRepo(t)
	var log bytes.Buffer
	if err := pinThumbnails(root, defaultRepo, fakeCommit, "", &log); err != nil {
		t.Fatalf("pinThumbnails: %v", err)
	}
	e := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if e.Screenshot == nil || !strings.Contains(e.Screenshot.URL, "/"+fakeCommit+"/plugins/timer/thumbnail.webp") {
		t.Fatalf("screenshot = %+v, want a URL at commit %s", e.Screenshot, fakeCommit)
	}
	if e.Version != "1.0.0" {
		t.Errorf("version changed to %q; only the screenshot may change", e.Version)
	}
}

func TestThumbnailsSkipsRowsWithNoThumbnailAtTheCommit(t *testing.T) {
	root := releasedTimerRepo(t)
	before := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	withThumbnailResponse(t, http.StatusNotFound, nil)
	var log bytes.Buffer
	if err := pinThumbnails(root, defaultRepo, fakeCommit, "", &log); err != nil {
		t.Fatalf("pinThumbnails: %v", err)
	}
	if !strings.Contains(log.String(), "skip org.sysc.timer") {
		t.Errorf("log = %q, want a skip line", log.String())
	}
	after := entryByID(t, readCatalogFile(t, filepath.Join(root, "catalog.json")), "org.sysc.timer")
	if before.Screenshot == nil || after.Screenshot == nil || *before.Screenshot != *after.Screenshot {
		t.Errorf("row changed: %+v vs %+v", after.Screenshot, before.Screenshot)
	}
}

func TestThumbnailsNamedPluginMustHaveOne(t *testing.T) {
	root := releasedTimerRepo(t)
	withThumbnailResponse(t, http.StatusNotFound, nil)
	err := pinThumbnails(root, defaultRepo, fakeCommit, "timer", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "plugins/timer/thumbnail.webp") {
		t.Fatalf("err = %v, want a missing-thumbnail failure naming the file", err)
	}
}

func TestThumbnailsRefMustBeAFullCommit(t *testing.T) {
	root := releasedTimerRepo(t)
	for _, ref := range []string{"main", "v1.0.0", "0123abc", ""} {
		err := pinThumbnails(root, defaultRepo, ref, "", &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "40-character commit") {
			t.Errorf("ref %q: err = %v, want a full-commit requirement", ref, err)
		}
	}
}
