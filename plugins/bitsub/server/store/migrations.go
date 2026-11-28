package store

import (
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrate applies each numbered migration above schema_version, one
// transaction per file.
func (s *Store) migrate() error {
	var hasMeta int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&hasMeta); err != nil {
		return err
	}
	current := 0
	if hasMeta == 1 {
		var v string
		err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
		if err == nil {
			current, _ = strconv.Atoi(v)
		}
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		num, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: bad number", e.Name())
		}
		if num <= current {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(num)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
