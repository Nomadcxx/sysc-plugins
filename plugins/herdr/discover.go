package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrHerdrMissing reports that the herdr binary could not be found. It wraps
// exec.ErrNotFound so callers can match on either.
var ErrHerdrMissing = fmt.Errorf("herdr binary not found: %w", exec.ErrNotFound)

// SessionNameRe is the set of characters a herdr session name may contain.
var SessionNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

var errOutputTooLarge = errors.New("herdr output too large")

// SessionInfo is one entry from `herdr session list --json`.
type SessionInfo struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Running bool   `json:"running"`
	Dir     string `json:"session_dir"`
	Socket  string `json:"socket_path"`
}

// StoppedWorkspace is a project recovered from a stopped session's session.json.
type StoppedWorkspace struct {
	ID   string
	Name string
}

// DefaultHerdrBin returns the herdr binary on PATH, or "" if absent.
func DefaultHerdrBin() string {
	path, err := exec.LookPath("herdr")
	if err != nil {
		return ""
	}
	return path
}

// ValidSessionName reports whether name is a legal herdr session name.
func ValidSessionName(name string) bool {
	return SessionNameRe.MatchString(name) && name != "." && name != ".."
}

// ListSessions runs `<bin> session list --json` and parses the result.
func ListSessions(ctx context.Context, bin string) ([]SessionInfo, error) {
	if bin == "" {
		return nil, fmt.Errorf("%w: empty path", ErrHerdrMissing)
	}
	cmd := exec.CommandContext(ctx, bin, "session", "list", "--json")
	out := &limitWriter{limit: 2 << 20}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s: %w", ErrHerdrMissing, bin, err)
		}
		return nil, err
	}
	return parseSessionList(out.buf.Bytes())
}

type limitWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errOutputTooLarge
	}
	return w.buf.Write(p)
}

// parseSessionList is the pure parse seam for ListSessions output.
func parseSessionList(data []byte) ([]SessionInfo, error) {
	var payload struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Sessions, nil
}

// LoadStopped reads a stopped session's workspace list. A missing or corrupt
// file yields an empty slice plus the error so callers degrade to the name only.
func LoadStopped(dir string) ([]StoppedWorkspace, error) {
	f, err := os.Open(filepath.Join(dir, "session.json"))
	if err != nil {
		return []StoppedWorkspace{}, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return []StoppedWorkspace{}, err
	}
	var payload struct {
		Workspaces []struct {
			ID          string `json:"workspace_id"`
			CustomName  string `json:"custom_name"`
			IdentityCwd string `json:"identity_cwd"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return []StoppedWorkspace{}, err
	}

	home, _ := os.UserHomeDir()
	out := make([]StoppedWorkspace, 0, len(payload.Workspaces))
	for _, w := range payload.Workspaces {
		name := strings.TrimSpace(w.CustomName)
		if name == "" {
			cwd := strings.TrimSpace(w.IdentityCwd)
			switch {
			case cwd == "":
				continue
			case home != "" && cwd == home:
				name = "~"
			default:
				name = filepath.Base(cwd)
			}
		}
		out = append(out, StoppedWorkspace{ID: w.ID, Name: name})
	}
	return out, nil
}
