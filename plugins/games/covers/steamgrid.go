package covers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func slugFile(slug string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(slug)
	if safe == "" {
		return "game"
	}
	return safe
}

// Doer is the injectable HTTP seam (an *http.Client satisfies it).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// FetchGrid downloads a 600x900 SteamGridDB image for the slug into destDir
// and returns the local path, or "" on no key / no result / any error.
// Never retries, never logs: a missing cover is not an incident.
// ponytail: slug-as-search-term matching; IGDB-grade matching if wrong-game
// art ever shows up.
func FetchGrid(ctx context.Context, do Doer, apiKey, baseURL, slug, destDir string) string {
	if apiKey == "" || slug == "" {
		return ""
	}
	if baseURL == "" {
		baseURL = "https://www.steamgriddb.com/api/v2"
	}
	cached := filepath.Join(destDir, slugFile(slug)+"-grid.jpg")
	if _, err := os.Stat(cached); err == nil {
		return cached
	}
	data, ok := sgdbGet(ctx, do, apiKey, baseURL+"/search/autocomplete/"+slug+"?languages=en")
	if !ok {
		return ""
	}
	list, _ := data.([]any)
	if len(list) == 0 {
		return ""
	}
	first, _ := list[0].(map[string]any)
	gameID, _ := first["id"].(float64)
	if gameID == 0 {
		return ""
	}
	grids, ok := sgdbGet(ctx, do, apiKey, fmt.Sprintf("%s/grids/game/%d?dimensions=600x900", baseURL, int(gameID)))
	if !ok {
		return ""
	}
	items, _ := grids.([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		url, _ := m["url"].(string)
		if url == "" {
			continue
		}
		if err := download(ctx, do, url, cached); err == nil {
			return cached
		}
	}
	return ""
}

func sgdbGet(ctx context.Context, do Doer, apiKey, url string) (any, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	res, err := do.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false
	}
	val, ok := envelope["data"]
	return val, ok
}

func download(ctx context.Context, do Doer, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := do.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	// ponytail: grids are ~100KB; 8MB cap is paranoia, not policy.
	if _, err := io.Copy(f, io.LimitReader(res.Body, 8<<20)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
