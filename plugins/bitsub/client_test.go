package bitsub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

func writeClientConfig(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, mode)
	return path
}

func TestLoadClientConfigRejectsLoosePerms(t *testing.T) {
	good := `{"url":"http://archpc.tail1234.ts.net:7733","token":"bsk_x"}`
	if _, err := LoadClientConfig(writeClientConfig(t, 0o644, good)); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("0644: %v", err)
	}
	cfg, err := LoadClientConfig(writeClientConfig(t, 0o600, good))
	if err != nil || cfg.URL != "http://archpc.tail1234.ts.net:7733" || cfg.Token != "bsk_x" {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, bad := range []string{`{"url":"ftp://x","token":"t"}`, `{"url":"http://x","token":""}`, `{"url":"","token":"t"}`, `not json`} {
		if _, err := LoadClientConfig(writeClientConfig(t, 0o600, bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := LoadClientConfig(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("missing file accepted")
	}
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/.config/bitsub/client.json"); got != filepath.Join(home, ".config/bitsub/client.json") {
		t.Errorf("ExpandHome %q", got)
	}
}

func TestClientUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{URL: srv.URL, Token: "bad"})
	if _, err := c.Snapshot(context.Background(), 0); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("snapshot: %v", err)
	}
	if err := c.SetIntent(context.Background(), "x", subs.Keep); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("intent: %v", err)
	}
}

func TestClientRequests(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+string(body))
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/snapshot"):
			json.NewEncoder(w).Encode(subs.Snapshot{LastSeq: 9, ServedBy: "archPC"})
		case strings.HasSuffix(r.URL.Path, "/intent") && strings.Contains(string(body), "nuke"):
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"must be keep, cancel or undecided","field":"intent"}`)
		default:
			io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer srv.Close()
	c := NewClient(ClientConfig{URL: srv.URL + "/", Token: "bsk_t"})
	ctx := context.Background()
	snap, err := c.Snapshot(ctx, 4)
	if err != nil || snap.LastSeq != 9 {
		t.Fatalf("%+v %v", snap, err)
	}
	if err := c.SetIntent(ctx, "a b", subs.Cancel); err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmCancelled(ctx, "id1"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetIntent(ctx, "x", "nuke"); err == nil || !strings.Contains(err.Error(), "must be keep") {
		t.Fatalf("400: %v", err)
	}
	want := []string{
		"GET /api/v1/snapshot?since=4 Bearer bsk_t ",
		`POST /api/v1/subscriptions/a%20b/intent Bearer bsk_t {"intent":"cancel"}`,
		`POST /api/v1/subscriptions/id1/confirm-cancelled Bearer bsk_t {}`,
	}
	for i, w := range want {
		if strings.TrimSpace(got[i]) != strings.TrimSpace(w) {
			t.Errorf("request %d: %q want %q", i, got[i], w)
		}
	}
}

func TestClientServerDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := NewClient(ClientConfig{URL: url, Token: "t"}).Snapshot(context.Background(), 0); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("%v", err)
	}
}
