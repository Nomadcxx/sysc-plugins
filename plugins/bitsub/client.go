package bitsub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

// ErrUnauthorized means bitsubd rejected the token (HTTP 401).
var ErrUnauthorized = errors.New("token rejected")

const (
	clientTimeout = 10 * time.Second
	maxSnapshot   = 4 << 20
)

// ClientConfig is ~/.config/bitsub/client.json. It holds the token, so the
// plugin reads it itself and the token never lands in the shell's settings.
type ClientConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// ExpandHome resolves a leading "~/".
func ExpandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}

// LoadClientConfig refuses a file anyone but the owner can read.
func LoadClientConfig(path string) (ClientConfig, error) {
	path = ExpandHome(path)
	fi, err := os.Stat(path)
	if err != nil {
		return ClientConfig{}, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return ClientConfig{}, fmt.Errorf("%s is readable by others; chmod 0600 it", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ClientConfig{}, err
	}
	var cfg ClientConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ClientConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ClientConfig{}, fmt.Errorf("%s: url must be http(s)://host:port", path)
	}
	if cfg.Token == "" {
		return ClientConfig{}, fmt.Errorf("%s: token is empty", path)
	}
	return cfg, nil
}

// Client talks to bitsubd's /api/v1.
type Client struct {
	cfg  ClientConfig
	http *http.Client
}

func NewClient(cfg ClientConfig) *Client {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &Client{cfg: cfg, http: &http.Client{Timeout: clientTimeout}}
}

func (c *Client) Snapshot(ctx context.Context, since int64) (subs.Snapshot, error) {
	var snap subs.Snapshot
	err := c.do(ctx, http.MethodGet, "/api/v1/snapshot?since="+strconv.FormatInt(since, 10), nil, &snap)
	return snap, err
}

func (c *Client) SetIntent(ctx context.Context, id string, intent subs.Intent) error {
	return c.do(ctx, http.MethodPost, "/api/v1/subscriptions/"+url.PathEscape(id)+"/intent", map[string]subs.Intent{"intent": intent}, nil)
}

func (c *Client) ConfirmCancelled(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/subscriptions/"+url.PathEscape(id)+"/confirm-cancelled", struct{}{}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshot))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode != http.StatusOK:
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("bitsubd: %s", e.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
