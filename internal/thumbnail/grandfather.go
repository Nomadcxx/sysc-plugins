package thumbnail

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

var dirNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// LoadGrandfathered reads the list of plugin directories exempt from the
// thumbnail requirement until their backfill lands: one directory name per
// line, with blank lines and #-comments ignored. A missing file is an empty
// list, so deleting the file once the list is empty needs no other change.
func LoadGrandfathered(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if !dirNamePattern.MatchString(name) {
			return nil, fmt.Errorf("%s:%d: %q is not a plugin directory name", path, n, name)
		}
		if out[name] {
			return nil, fmt.Errorf("%s:%d: %q is listed twice", path, n, name)
		}
		out[name] = true
	}
	return out, sc.Err()
}
