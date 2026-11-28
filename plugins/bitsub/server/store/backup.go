package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// BackupDaily writes dir/bitsub-YYYYMMDD.db with VACUUM INTO unless today's
// copy exists, then removes the oldest copies beyond keep.
func (s *Store) BackupDaily(dir string, today subs.Date, keep int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("bitsub-%04d%02d%02d.db", today.Year, today.Month, today.Day)
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "bitsub-????????.db"))
	if err != nil {
		return err
	}
	sort.Strings(matches)
	for len(matches) > keep {
		if err := os.Remove(matches[0]); err != nil && !os.IsNotExist(err) {
			return err
		}
		os.Remove(strings.TrimSuffix(matches[0], ".db") + ".db-wal")
		matches = matches[1:]
	}
	return nil
}
