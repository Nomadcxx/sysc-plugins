// Package source normalises game libraries behind one interface so the UI
// never learns what Lutris is.
package source

import (
	"context"
	"errors"
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
}

type Source interface {
	Name() string
	List(ctx context.Context) ([]Game, error)
	Launch(ctx context.Context, g Game) error
	Stop(ctx context.Context, g Game) error
	Running(ctx context.Context) (map[string]time.Time, error) // keyed by Game.ID
}
