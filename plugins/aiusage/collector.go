package aiusage

import (
	"context"
	"net/http"
	"os"
	"time"
)

// Collector is one provider's data source. Fetch returns a normalized report
// or an error: *ErrSetup for a missing credential, anything else for a fault
// the loop retains last-good data over.
type Collector interface {
	ID() string
	Fetch(ctx context.Context) (ProviderReport, error)
}

// Env carries the seams every collector needs. Tests inject all four; the
// zero-value accessors fall back to the process defaults.
type Env struct {
	// Client is the HTTP client for API collectors. Nil means the default
	// client; tests inject one pointed at httptest.
	Client *http.Client
	// Home overrides the user's home directory. Nil means os.UserHomeDir.
	Home string
	// Now overrides the clock. Nil means time.Now.
	Now func() time.Time
	// Env overrides environment lookups. Nil means os.Getenv.
	Env func(string) string
	// Keys are the pasted per-provider keys from settings, keyed by
	// provider id. A pasted key wins over the environment and over
	// well-known files.
	Keys map[string]string
}

// key returns the pasted key for a provider id, if any.
func (e Env) key(id string) string {
	if e.Keys == nil {
		return ""
	}
	return e.Keys[id]
}

func (e Env) httpClient() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

func (e Env) home() string {
	if e.Home != "" {
		return e.Home
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Env) getenv(key string) string {
	if e.Env != nil {
		return e.Env(key)
	}
	return os.Getenv(key)
}

// Registry builds collectors by id. A new provider is one constructor entry,
// one file, one fixture; nothing else in the plugin changes.
type Registry map[string]func(Env) Collector

// Build constructs the collector registered under id.
func (r Registry) Build(id string, env Env) (Collector, bool) {
	new, ok := r[id]
	if !ok {
		return nil, false
	}
	return new(env), true
}
