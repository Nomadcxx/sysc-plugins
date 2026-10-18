// Package source normalises game libraries behind one interface so the UI
// never learns what Lutris, Steam, Heroic or Faugus is.
package source

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrNotImplemented marks Source methods an implementation does not offer yet.
var ErrNotImplemented = errors.New("not implemented")

type Game struct {
	ID          string
	Name        string
	Slug        string
	Runner      string
	Platform    string
	Year        string
	Installed   bool
	PlaytimeSec float64
	LastPlayed  time.Time
	Directory   string
	Executable  string
	ConfigPath  string
	CoverPath   string // absolute path to resolved cover art, "" when none
	Source      string // owning implementation, e.g. "lutris"

	// Identity is the strong cross-library key ("steam:730", "epic:Sugar"),
	// "" when the backend cannot name the store game. The set merges copies
	// that share an identity even when their titles differ.
	Identity string
	// OtherSources lists the libraries a merged card also exists in, in set
	// order; the detail pane reads it as "Also in …".
	OtherSources []string
}

type Source interface {
	Name() string
	// List returns the library's games with source-local ids; the set
	// namespaces them on the way out.
	List(ctx context.Context) ([]Game, error)
	// Launch and Stop receive a Game whose id is source-local again: the set
	// strips its namespace before routing by Game.Source.
	Launch(ctx context.Context, g Game) error
	Stop(ctx context.Context, g Game) error
	// Running uses the List snapshot it is handed (never a fresh listing) and
	// maps source-local ids to start times; the set namespaces them.
	Running(ctx context.Context, games []Game) (map[string]time.Time, error)
}

// Key is the session-wide identity of a game: "<source>:<local id>", with the
// local id escaped so keys are always safe to embed in node IDs and map keys.
// Backends never see keys — the set namespaces their ids on the way out and
// strips the namespace before routing Launch/Stop back to the owner.
func Key(src, id string) string {
	return src + ":" + escape(id)
}

// Split reverses Key. A key without a separator yields an empty source.
func Split(key string) (src, id string) {
	src, rest, ok := strings.Cut(key, ":")
	if !ok {
		return "", key
	}
	return src, unescape(rest)
}

func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if isSafe(s[i]) {
			b.WriteByte(s[i])
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[s[i]>>4])
		b.WriteByte(hex[s[i]&0x0f])
	}
	return b.String()
}

func unescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := hexVal(s[i+1])
			lo, ok2 := hexVal(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isSafe(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '.' || c == '_' || c == '-':
		return true
	}
	return false
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
