package faith

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultAPIBase is the Free Use Bible API. Only commentary comes from it.
const DefaultAPIBase = "https://bible.helloao.org/api"

const (
	// maxCommentaryBytes bounds one chapter response. Adam Clarke's longest
	// chapters run to a few hundred kilobytes.
	maxCommentaryBytes = 4 << 20
	commentaryChapters = 16
)

// ErrTooLarge reports a response over maxCommentaryBytes.
var ErrTooLarge = errors.New("faith: commentary response is too large")

// StatusError reports a non-200 response.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("faith: commentary: HTTP %d", e.Code) }

// Commentary fetches Adam Clarke's commentary a chapter at a time and keeps
// the most recently used chapters in memory.
type Commentary struct {
	Base string
	HTTP *http.Client

	mu    sync.Mutex
	cache map[string]map[int]string
	order []string
}

// NewCommentary returns a client for base, or DefaultAPIBase when empty.
func NewCommentary(base string) *Commentary {
	if base == "" {
		base = DefaultAPIBase
	}
	return &Commentary{
		Base:  strings.TrimRight(base, "/"),
		HTTP:  &http.Client{Timeout: 10 * time.Second},
		cache: map[string]map[int]string{},
	}
}

// Entry returns the note on a reference's first verse. found is false when
// the commentary has nothing on that verse, which is not an error.
func (c *Commentary) Entry(ctx context.Context, r Ref) (text string, found bool, err error) {
	key := fmt.Sprintf("%s/%d", Books[r.Book].Code, r.Chapter)
	c.mu.Lock()
	ch, ok := c.cache[key]
	if ok {
		c.touch(key)
	}
	c.mu.Unlock()
	if !ok {
		ch, err = c.fetch(ctx, r)
		if err != nil {
			return "", false, err
		}
		c.store(key, ch)
	}
	text, found = ch[r.Verse]
	return text, found, nil
}

// touch moves key to the most recently used end. The caller holds c.mu.
func (c *Commentary) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(append(c.order[:i:i], c.order[i+1:]...), key)
			return
		}
	}
}

func (c *Commentary) store(key string, ch map[int]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.cache[key]; ok {
		c.touch(key)
	} else {
		c.order = append(c.order, key)
	}
	c.cache[key] = ch
	for len(c.order) > commentaryChapters {
		delete(c.cache, c.order[0])
		c.order = c.order[1:]
	}
}

// Cached reports how many chapters are held, for tests.
func (c *Commentary) Cached() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}

func (c *Commentary) fetch(ctx context.Context, r Ref) (map[int]string, error) {
	url := fmt.Sprintf("%s/c/adam-clarke/%s/%d.json", c.Base, Books[r.Book].Code, r.Chapter)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCommentaryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxCommentaryBytes {
		return nil, ErrTooLarge
	}
	return parseCommentary(body)
}

func parseCommentary(body []byte) (map[int]string, error) {
	var doc struct {
		Chapter struct {
			Content []struct {
				Type    string            `json:"type"`
				Number  int               `json:"number"`
				Content []json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"chapter"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("faith: commentary: %w", err)
	}
	out := map[int]string{}
	for _, item := range doc.Chapter.Content {
		if item.Type != "verse" {
			continue
		}
		var b strings.Builder
		for _, raw := range item.Content {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				b.WriteString(s)
				continue
			}
			var part struct {
				Text      string `json:"text"`
				Heading   string `json:"heading"`
				LineBreak bool   `json:"lineBreak"`
			}
			if json.Unmarshal(raw, &part) != nil {
				continue
			}
			switch {
			case part.Text != "":
				b.WriteString(part.Text)
			case part.Heading != "":
				b.WriteString("\n" + part.Heading + "\n")
			case part.LineBreak:
				b.WriteString("\n")
			}
		}
		if text := strings.TrimSpace(b.String()); text != "" {
			out[item.Number] = text
		}
	}
	return out, nil
}
