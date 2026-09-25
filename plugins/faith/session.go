package faith

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// Effects are what an event asks the process to do; the session itself does
// no I/O.
type (
	// NotifyEffect sends a prayer.
	NotifyEffect struct{ Params v1.NotifyParams }
	// OpenPanelEffect opens the reading panel beside the bar widget.
	OpenPanelEffect struct{ Output, Instance string }
	// OpenURLEffect opens a page in the browser.
	OpenURLEffect struct{ URL string }
	// FetchCommentaryEffect asks for a verse's note; Gen identifies the
	// request so a late answer for a verse the panel has left is dropped.
	FetchCommentaryEffect struct {
		Ref Ref
		Gen uint64
	}
	// SaveStateEffect asks for Persisted to be written to host state.
	SaveStateEffect struct{}
)

// Effect is one of the effect types above.
type Effect any

// Persisted is what survives a restart.
type Persisted struct {
	Ref *Ref `json:"ref,omitempty"`
	Bag *Bag `json:"bag,omitempty"`
}

// Session is the plugin's state. It is used from one goroutine.
type Session struct {
	settings Settings
	bible    *Bible
	pool     Pool
	xrefs    *Xrefs
	rng      *rand.Rand
	canRead  bool

	ref         Ref
	bag         Bag
	panels      int
	lastRefresh time.Time
	lastDay     string

	commentary CommentaryStatus
	note       string
	noteGen    uint64
}

// NewSession starts on a verse chosen by the settings' verse mode.
func NewSession(s Settings, rng *rand.Rand, now time.Time, canRead bool) (*Session, error) {
	pool, err := LoadPool()
	if err != nil {
		return nil, err
	}
	xrefs, err := LoadXrefs()
	if err != nil {
		return nil, err
	}
	bible, err := LoadBible(s.Translation)
	if err != nil {
		return nil, err
	}
	ss := &Session{settings: s, bible: bible, pool: pool, xrefs: xrefs, rng: rng, canRead: canRead}
	ss.pick(now)
	return ss, nil
}

// Ref is the current reference.
func (s *Session) Ref() Ref { return s.ref }

// Settings are the settings in force.
func (s *Session) Settings() Settings { return s.settings }

// Restore adopts a persisted reference and bag. A reference the current
// translation cannot show is ignored; in daily mode the date decides.
func (s *Session) Restore(p Persisted) {
	if p.Bag != nil {
		s.bag = *p.Bag
	}
	if p.Ref != nil && s.settings.VerseMode != ModeDaily {
		if r, ok := s.resolve(*p.Ref); ok {
			s.ref = r
		}
	}
}

// Persisted is the state to save.
func (s *Session) Persisted() Persisted {
	ref, bag := s.ref, s.bag
	bag.Order = append([]string(nil), bag.Order...)
	return Persisted{Ref: &ref, Bag: &bag}
}

func (s *Session) resolve(r Ref) (Ref, bool) {
	if r.Book < 0 || r.Book >= len(Books) {
		return Ref{}, false
	}
	if s.bible.Has(r.Book, r.Chapter, r.Verse) {
		return r, true
	}
	return s.bible.Resolve(r)
}

// pick chooses a verse by the verse mode.
func (s *Session) pick(now time.Time) {
	s.lastRefresh = now
	s.lastDay = now.Format("2006-01-02")
	var r Ref
	switch s.settings.VerseMode {
	case ModeDaily:
		r = s.pool.Daily(now)
	case ModeBible:
		r = s.bible.Random(s.rng)
	default:
		r = s.pool.Random(s.rng, s.ref)
	}
	if rr, ok := s.resolve(r); ok {
		s.ref = rr
	} else {
		s.ref = s.pool.Random(s.rng, s.ref)
	}
}

// moved finishes any change of verse.
func (s *Session) moved(now time.Time) []Effect {
	s.lastRefresh = now
	return append([]Effect{SaveStateEffect{}}, s.commentaryRequest()...)
}

func (s *Session) commentaryRequest() []Effect {
	s.noteGen++
	s.note = ""
	if !s.settings.ShowCommentary {
		s.commentary = CommentaryOff
		return nil
	}
	if s.panels == 0 {
		s.commentary = CommentaryLoading
		return nil
	}
	s.commentary = CommentaryLoading
	return []Effect{FetchCommentaryEffect{Ref: s.ref, Gen: s.noteGen}}
}

// Input handles an event from any view.
func (s *Session) Input(ev *v1.InputEvent, now time.Time) []Effect {
	switch {
	case ev.Node == NodeCross:
		switch {
		case ev.Event == v1.EventActivate:
			return s.pray()
		case ev.Event == v1.EventPointer && ev.Button == v1.ButtonSecondary:
			return s.pray()
		case ev.Event == v1.EventPointer && ev.Button == v1.ButtonMiddle:
			return []Effect{OpenPanelEffect{Output: ev.Output, Instance: ev.ViewID}}
		}
		// A primary press arrives as a pointer event before the release's
		// activate; acting on both would pray twice per click.
		return nil
	case ev.Event != v1.EventActivate:
		return nil
	case ev.Node == NodePrev:
		if r, ok := s.bible.Prev(s.ref); ok {
			s.ref = r
			return s.moved(now)
		}
	case ev.Node == NodeNext:
		if r, ok := s.bible.Next(s.ref); ok {
			s.ref = r
			return s.moved(now)
		}
	case ev.Node == NodeNew:
		s.pick(now)
		return s.moved(now)
	case ev.Node == NodeRead:
		if s.canRead {
			return []Effect{OpenURLEffect{URL: ReaderURL(s.bible.ID, s.ref)}}
		}
	case strings.HasPrefix(ev.Node, nodeXrefPrefix):
		var i int
		if _, err := fmt.Sscanf(ev.Node, nodeXrefPrefix+"%d", &i); err != nil {
			return nil
		}
		xs := s.crossRefs()
		if i >= 0 && i < len(xs) {
			s.ref = xs[i]
			return s.moved(now)
		}
	}
	return nil
}

func (s *Session) pray() []Effect {
	for range len(Prayers) {
		p := s.bag.Draw(s.rng, s.settings.Tradition)
		text, source, err := p.Body(s.bible)
		if err != nil {
			continue
		}
		return []Effect{NotifyEffect{Params: NotifyFor(p.Title, text, source, s.settings.PrayerSeconds)}, SaveStateEffect{}}
	}
	return nil
}

// ReaderURL is the chapter on biblehub.com in the given translation.
func ReaderURL(translation string, r Ref) string {
	return fmt.Sprintf("https://biblehub.com/%s/%s/%d.htm", strings.ToLower(translation), Books[r.Book].Slug, r.Chapter)
}

// crossRefs are the current verse's references that this translation carries.
func (s *Session) crossRefs() []Ref {
	var out []Ref
	for _, r := range s.xrefs.For(s.ref) {
		if rr, ok := s.resolve(r); ok && rr.Verse == r.Verse {
			out = append(out, r)
		}
	}
	return out
}

// PanelOpened counts an open panel and fetches commentary for it.
func (s *Session) PanelOpened() []Effect {
	s.panels++
	if s.panels == 1 && s.settings.ShowCommentary && s.commentary != CommentaryReady && s.commentary != CommentaryNone {
		return s.commentaryRequest()
	}
	return nil
}

// PanelClosed forgets a closed panel.
func (s *Session) PanelClosed() {
	if s.panels > 0 {
		s.panels--
	}
}

// CommentaryResult applies a fetched note. It reports false for a stale
// answer, which changes nothing.
func (s *Session) CommentaryResult(gen uint64, text string, found bool, err error) bool {
	if gen != s.noteGen || !s.settings.ShowCommentary {
		return false
	}
	switch {
	case err != nil:
		s.commentary = CommentaryUnavailable
	case !found:
		s.commentary = CommentaryNone
	default:
		s.commentary, s.note = CommentaryReady, text
	}
	return true
}

// Tick moves the verse on when its time comes: a new day in daily mode, or
// the refresh interval in the random modes while no panel is open. It
// reports whether anything changed.
func (s *Session) Tick(now time.Time) (bool, []Effect) {
	switch s.settings.VerseMode {
	case ModeDaily:
		if day := now.Format("2006-01-02"); day != s.lastDay {
			s.pick(now)
			return true, s.moved(now)
		}
	default:
		every := time.Duration(s.settings.RefreshMinutes) * time.Minute
		if every > 0 && s.panels == 0 && now.Sub(s.lastRefresh) >= every {
			s.pick(now)
			return true, s.moved(now)
		}
	}
	return false, nil
}

// ApplySettings merges new settings and returns the effects of the change.
func (s *Session) ApplySettings(values map[string]any, now time.Time) []Effect {
	old, oldRef := s.settings, s.ref
	s.settings.Apply(values)
	var effects []Effect
	if s.settings.Translation != old.Translation {
		if b, err := LoadBible(s.settings.Translation); err == nil {
			s.bible = b
			if r, ok := s.resolve(s.ref); ok {
				s.ref = r
			} else {
				s.pick(now)
			}
		} else {
			s.settings.Translation = old.Translation
		}
	}
	if s.settings.VerseMode != old.VerseMode {
		s.pick(now)
		effects = append(effects, s.moved(now)...)
	} else if s.ref != oldRef {
		effects = append(effects, s.moved(now)...)
	} else if s.settings.ShowCommentary != old.ShowCommentary {
		effects = append(effects, s.commentaryRequest()...)
	}
	s.lastRefresh = now
	return effects
}

// Views.

// BarTree is the bar view.
func (s *Session) BarTree() *v1.Node { return BarTree(s.ref, s.settings.ShowReference) }

// TooltipTree is the tooltip view.
func (s *Session) TooltipTree() *v1.Node {
	text, _ := s.bible.Text(s.ref)
	return TooltipTree(s.ref, text)
}

// PanelTree is the panel view.
func (s *Session) PanelTree() *v1.Node {
	text, _ := s.bible.Text(s.ref)
	_, canPrev := s.bible.Prev(s.ref)
	_, canNext := s.bible.Next(s.ref)
	return PanelTree(PanelModel{
		Ref: s.ref, Translation: s.bible.ID, Verse: text,
		CanPrev: canPrev, CanNext: canNext, CanRead: s.canRead,
		Commentary: s.commentary, Note: s.note, Xrefs: s.crossRefs(),
	})
}
