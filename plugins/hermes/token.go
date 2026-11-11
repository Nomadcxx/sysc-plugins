package hermes

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// maxTokenBytes caps a token file; a bridge token is far smaller.
const maxTokenBytes = 4096

var (
	// ErrNoToken means no token is configured: the file is missing or empty,
	// or nothing was pasted. Every authenticated route then answers 403.
	ErrNoToken = errors.New("no bridge token")
	// ErrInvalidToken means a token was found but cannot be sent as a header.
	ErrInvalidToken = errors.New("bridge token contains a line break")
)

// ReadToken reads a bridge token from a regular file of at most 4 KiB and
// trims surrounding whitespace. A missing or empty file is ErrNoToken. No
// error it returns contains the file's contents.
func ReadToken(path string) (string, error) {
	path = ExpandPath(path)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	// O_NONBLOCK keeps a file swapped for a FIFO after the Stat from hanging.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("token file %s could not be read", path)
	}
	if len(data) > maxTokenBytes {
		return "", fmt.Errorf("token file %s is larger than %d bytes", path, maxTokenBytes)
	}
	token, err := cleanToken(string(data))
	if err != nil {
		return "", fmt.Errorf("token file %s: %w", path, err)
	}
	return token, nil
}

// cleanToken trims a token and refuses one that would split a header.
func cleanToken(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrNoToken
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", ErrInvalidToken
	}
	return s, nil
}

// TokenKind names where a token came from, for the settings panel.
type TokenKind string

const (
	TokenPasted TokenKind = "pasted"
	TokenFile   TokenKind = "file"
	TokenLocal  TokenKind = "local"
	TokenNone   TokenKind = "none"
)

// Token is a resolved bridge token. Its value is unexported and every
// formatting verb prints only the kind and path, so a log line or a rendered
// view cannot leak it by accident.
type Token struct {
	Kind  TokenKind
	Path  string // the file it was read from; empty for pasted or none
	value string
}

// Value is the token to send as X-Bridge-Token.
func (t Token) Value() string { return t.value }

func (t Token) String() string {
	if t.Path == "" {
		return "token(" + string(t.Kind) + ")"
	}
	return "token(" + string(t.Kind) + " " + t.Path + ")"
}

func (t Token) GoString() string { return t.String() }

// Format makes every verb, including %#v and %+v, print String.
func (t Token) Format(f fmt.State, verb rune) {
	if verb == 'q' {
		fmt.Fprintf(f, "%q", t.String())
		return
	}
	io.WriteString(f, t.String())
}

// TokenSource holds the settings that choose a token.
type TokenSource struct {
	ClientOnly bool
	Pasted     string // bridge_token_manual
	File       string // bridge_token_file
	StateFile  string // state_file, Local mode
}

// LocalCandidates are the Local-mode token files in the order they are
// tried: beside the state file, then the sysc-hermes and Noctalia cache
// directories, so a bridge either plugin started can be adopted.
func (s TokenSource) LocalCandidates() []string {
	stateFile := s.StateFile
	if stateFile == "" {
		stateFile = DefaultStateFile
	}
	cache := CacheHome()
	var out []string
	seen := map[string]bool{}
	for _, p := range []string{
		filepath.Join(filepath.Dir(ExpandPath(stateFile)), "bridge.token"),
		filepath.Join(cache, "sysc-hermes", "bridge.token"),
		filepath.Join(cache, "noctalia-hermes", "bridge.token"),
	} {
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// Resolve picks the token to use.
//
// Client-only: the pasted token when it is not blank, else the token file,
// else none. Local: each candidate in order; the first one accept approves
// is kept (accept is the service's /state check; nil accepts the first
// readable token). A Local miss returns none and, when a candidate could not
// be read, that reason.
func (s TokenSource) Resolve(accept func(token string) bool) (Token, error) {
	if s.ClientOnly {
		if strings.TrimSpace(s.Pasted) != "" {
			value, err := cleanToken(s.Pasted)
			if err != nil {
				return Token{Kind: TokenNone}, fmt.Errorf("pasted token: %w", err)
			}
			return Token{Kind: TokenPasted, value: value}, nil
		}
		if s.File == "" {
			return Token{Kind: TokenNone}, nil
		}
		value, err := ReadToken(s.File)
		if errors.Is(err, ErrNoToken) {
			return Token{Kind: TokenNone}, nil
		}
		if err != nil {
			return Token{Kind: TokenNone}, err
		}
		return Token{Kind: TokenFile, Path: ExpandPath(s.File), value: value}, nil
	}
	var firstErr error
	for _, path := range s.LocalCandidates() {
		value, err := ReadToken(path)
		if err != nil {
			if !errors.Is(err, ErrNoToken) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if accept == nil || accept(value) {
			return Token{Kind: TokenLocal, Path: path, value: value}, nil
		}
	}
	return Token{Kind: TokenNone}, firstErr
}
