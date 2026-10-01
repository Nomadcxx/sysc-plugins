package notes

import (
	"os"
	"path/filepath"
	"sync"
)

// EmptyArtPath finds the shipped illustration the way AI Usage finds its
// logos: beside the installed binary, under ../assets. The host decodes it and
// reserves the box either way, so a missing file only blanks the art.
var EmptyArtPath = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(exe), "..", "assets", "empty-notes.png"))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	return abs
})
