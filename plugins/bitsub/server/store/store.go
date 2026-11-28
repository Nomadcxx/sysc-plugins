// Package store is bitsub's SQLite database. bitsubd is the only process
// that opens it; every mutation runs inside a Tx so a write and its audit
// row land together.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

type Store struct {
	db *sql.DB
}

// Tx is an open transaction. All reads and writes go through it.
type Tx struct {
	tx *sql.Tx
}

// Open creates the parent directory (0700) and the database file (0600) if
// needed, then applies pending migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// One connection serialises writers and keeps every Tx consistent.
	db.SetMaxOpenConns(1)
	st := &Store{db: db}
	if err := st.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return st, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Tx runs fn in a transaction, committing when it returns nil.
func (s *Store) Tx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Tx{tx: tx}); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// NormName lower-cases name, collapses whitespace and trims it; names that
// normalise equal are near-duplicates.
func NormName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t.UTC(), err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
