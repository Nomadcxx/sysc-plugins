package source

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Set merges several libraries behind one Source-shaped surface: ids come out
// namespaced and deduplicated, and Launch/Stop route back to the owning
// library. Sources are iterated in the order given — the session builds the
// set with the user's default launcher first, so a title that exists in more
// than one library is served by the preferred one.
type Set struct {
	sources []Source
}

func NewSet(sources ...Source) *Set { return &Set{sources: sources} }

// Names lists the library names in preference order.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.sources))
	for _, src := range s.sources {
		out = append(out, src.Name())
	}
	return out
}

// List fans out over the set, namespaces ids, and collapses copies of the
// same game: store identity first, normalized title as the fallback for
// copies no library can name (a Lutris import without service_id, a
// sideloaded Heroic game). A library that fails is skipped; the error only
// surfaces when every library failed.
func (s *Set) List(ctx context.Context) ([]Game, error) {
	if len(s.sources) == 0 {
		return nil, errors.New("no game libraries enabled")
	}
	var out []Game
	byIdentity := map[string]int{}
	byTitle := map[string]int{}
	var errs []error
	for _, src := range s.sources {
		games, err := src.List(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.Name(), err))
			continue
		}
		for _, g := range games {
			if g.Source == "" {
				g.Source = src.Name()
			}
			g.ID = Key(g.Source, g.ID)

			idx := -1
			if g.Identity != "" {
				if i, ok := byIdentity[g.Identity]; ok {
					idx = i
				}
			}
			if idx < 0 {
				if i, ok := byTitle[titleKey(g.Name)]; ok {
					// Title fallback only folds copies from *other* libraries,
					// and never merges two games whose store ids disagree.
					if out[i].Source != g.Source &&
						!(out[i].Identity != "" && g.Identity != "" && out[i].Identity != g.Identity) {
						idx = i
					}
				}
			}
			if idx >= 0 {
				if out[idx].Source != g.Source && !contains(out[idx].OtherSources, g.Source) {
					out[idx].OtherSources = append(out[idx].OtherSources, g.Source)
				}
				continue
			}
			out = append(out, g)
			i := len(out) - 1
			if g.Identity != "" {
				byIdentity[g.Identity] = i
			}
			if _, ok := byTitle[titleKey(g.Name)]; !ok {
				byTitle[titleKey(g.Name)] = i
			}
		}
	}
	if len(out) == 0 && len(errs) > 0 && len(errs) == len(s.sources) {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Launch routes to the owning library with the namespace stripped, so every
// backend only ever sees its own local ids.
func (s *Set) Launch(ctx context.Context, g Game) error {
	src, err := s.owner(g.Source)
	if err != nil {
		return err
	}
	_, local := Split(g.ID)
	g.ID = local
	return src.Launch(ctx, g)
}

// Stop routes like Launch.
func (s *Set) Stop(ctx context.Context, g Game) error {
	src, err := s.owner(g.Source)
	if err != nil {
		return err
	}
	_, local := Split(g.ID)
	g.ID = local
	return src.Stop(ctx, g)
}

// Running merges every library's running map into namespaced keys. Keys match
// the ids List hands out, so the launch machine and the session log agree
// with the running set no matter which library owns the game. Each library
// only ever sees its own games, with the namespace stripped.
func (s *Set) Running(ctx context.Context, games []Game) (map[string]time.Time, error) {
	perSource := map[string][]Game{}
	for _, g := range games {
		_, local := Split(g.ID)
		g.ID = local
		perSource[g.Source] = append(perSource[g.Source], g)
	}
	out := map[string]time.Time{}
	var errs []error
	for _, src := range s.sources {
		m, err := src.Running(ctx, perSource[src.Name()])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.Name(), err))
			continue
		}
		for id, start := range m {
			out[Key(src.Name(), id)] = start
		}
	}
	if len(errs) > 0 && len(errs) == len(s.sources) {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func (s *Set) owner(name string) (Source, error) {
	for _, src := range s.sources {
		if src.Name() == name {
			return src, nil
		}
	}
	if name == "" {
		return nil, errors.New("game has no owning library")
	}
	return nil, fmt.Errorf("library %q is not enabled", name)
}

// titleKey normalizes a title for the identity-less fallback match.
func titleKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
