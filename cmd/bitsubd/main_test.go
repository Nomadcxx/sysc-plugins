package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/httpapi"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

func writeConfig(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bitsubd.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, mode)
	return path
}

func TestConfigRejectsWorldReadable(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604} {
		if _, err := LoadConfig(writeConfig(t, mode, `{}`)); err == nil || !strings.Contains(err.Error(), "readable") {
			t.Errorf("mode %v: %v", mode, err)
		}
	}
	cfg, err := LoadConfig(writeConfig(t, 0o600, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Listen) != 1 || cfg.Listen[0] != "127.0.0.1:7733" || cfg.ReminderHour != 9 || cfg.DigestWeekday != "Monday" ||
		cfg.CalendarName != "Subscriptions" || cfg.GogBin != "gog" || !strings.HasSuffix(cfg.DataDir, ".local/share/bitsub") {
		t.Fatalf("defaults %+v", cfg)
	}
}

func TestConfigRejectsLANWithoutOptIn(t *testing.T) {
	cases := []struct {
		body string
		ok   bool
	}{
		{`{"listen":["192.168.0.10:7733"]}`, false},
		{`{"listen":["192.168.0.10:7733"],"allow_lan":true}`, true},
		{`{"listen":["100.101.1.2:7733"]}`, true},
		{`{"listen":["127.0.0.1:7733","100.64.0.1:7733"]}`, true},
		{`{"listen":["0.0.0.0:7733"]}`, false},
		{`{"listen":[":7733"]}`, false},
		{`{"listen":["archpc:7733"]}`, false},
		{`{"listen":["[::1]:7733"]}`, true},
		{`{"reminder_hour":24}`, false},
		{`{"digest_weekday":"Funday"}`, false},
		{`{"tokens":[{"name":"Bad Name","sha256":"x"}]}`, false},
		{`{"unknown_field":1}`, false},
	}
	for _, c := range cases {
		_, err := LoadConfig(writeConfig(t, 0o600, c.body))
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", c.body, err)
		}
	}
}

func TestTokenAddListRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "bitsubd.json") // absent: token add creates it
	var out, errOut bytes.Buffer
	if code := run([]string{"-config", path, "token", "add", "laptop-plugin"}, &out, &errOut); code != 0 {
		t.Fatalf("add: %d %s", code, errOut.String())
	}
	plain := strings.TrimSpace(out.String())
	if !strings.HasPrefix(plain, "bsk_") {
		t.Fatalf("printed %q", out.String())
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), plain) {
		t.Fatal("plaintext token stored")
	}
	cfg, err := LoadConfig(path)
	if err != nil || len(cfg.Tokens) != 1 || httpapi.NewAuth(cfg.Tokens).Actor(http.Header{"Authorization": {"Bearer " + plain}}) != "laptop-plugin" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if code := run([]string{"-config", path, "token", "add", "laptop-plugin"}, &out, &errOut); code == 0 {
		t.Fatal("duplicate name accepted")
	}
	if code := run([]string{"-config", path, "token", "add", "Bad Name"}, &out, &errOut); code == 0 {
		t.Fatal("bad name accepted")
	}
	run([]string{"-config", path, "token", "add", "codex-laptop"}, &out, &errOut)
	out.Reset()
	if code := run([]string{"-config", path, "token", "list"}, &out, &errOut); code != 0 || out.String() != "laptop-plugin\ncodex-laptop\n" {
		t.Fatalf("list %d %q", code, out.String())
	}
	if code := run([]string{"-config", path, "token", "revoke", "laptop-plugin"}, &out, &errOut); code != 0 {
		t.Fatal("revoke failed")
	}
	if code := run([]string{"-config", path, "token", "revoke", "laptop-plugin"}, &out, &errOut); code == 0 {
		t.Fatal("revoking a missing token succeeded")
	}
	cfg, _ = LoadConfig(path)
	if len(cfg.Tokens) != 1 || cfg.Tokens[0].Name != "codex-laptop" {
		t.Fatalf("%+v", cfg.Tokens)
	}
}

// recordingRunner is a fake gog that remembers argv.
type recordingRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingRunner) reply(args []string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, args)
	r.mu.Unlock()
	switch strings.Join(args[:2], " ") {
	case "calendar create-calendar":
		return []byte(`{"calendar":{"id":"cal1"}}`), nil
	case "calendar events":
		return []byte(`{"events":[],"nextPageToken":""}`), nil
	case "calendar create":
		return []byte(`{"event":{"id":"ev1"}}`), nil
	}
	return []byte(`{}`), nil
}

func (r *recordingRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	return r.reply(args)
}
func (r *recordingRunner) RunStdin(_ context.Context, _ string, args ...string) ([]byte, error) {
	return r.reply(args)
}

func (r *recordingRunner) creates() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, c := range r.calls {
		if len(c) > 1 && c[0] == "calendar" && c[1] == "create" {
			out = append(out, c)
		}
	}
	return out
}

type noFX struct{}

func (noFX) Do(*http.Request) (*http.Response, error) { return nil, os.ErrDeadlineExceeded }

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func TestServeEndToEnd(t *testing.T) {
	plain, sha, _ := httpapi.GenerateToken()
	cfg := Defaults()
	cfg.DataDir = t.TempDir()
	cfg.GogAccount = "me@example.com"
	cfg.Tokens = []httpapi.Token{{Name: "claude-archpc", SHA256: sha}}
	runner := &recordingRunner{}
	now := time.Date(2026, 11, 29, 10, 0, 0, 0, time.Local)
	d, err := newDaemon(cfg, deps{now: func() time.Time { return now }, runner: runner, fxDoer: noFX{}, debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.serve(ctx, []net.Listener{ln}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	base := "http://" + ln.Addr().String()
	client := &http.Client{Transport: bearer{plain, http.DefaultTransport}, Timeout: 5 * time.Second}

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "add_subscription", Arguments: map[string]any{
		"name": "Netflix", "amount": "18.99", "currency": "AUD", "cycle": "month", "anchor_date": "2026-12-14"}})
	if err != nil || res.IsError {
		t.Fatalf("add: %v %+v", err, res)
	}

	resp, err := client.Get(base + "/api/v1/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	var snap subs.Snapshot
	json.NewDecoder(resp.Body).Decode(&snap)
	resp.Body.Close()
	if len(snap.Items) != 1 || snap.Items[0].Name != "Netflix" || snap.Channels.FXError == "" {
		t.Fatalf("snapshot %+v", snap)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(runner.creates()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	creates := runner.creates()
	if len(creates) != 1 || !strings.Contains(strings.Join(creates[0], " "), "Netflix renews") {
		t.Fatalf("calendar creates %v", creates)
	}

	var rows int
	d.st.Tx(ctx, func(tx *store.Tx) error {
		audit, _ := tx.ListAudit(snap.Items[0].ID)
		for _, a := range audit {
			if a.Actor == "claude-archpc" {
				rows++
			}
		}
		return nil
	})
	if rows != 1 {
		t.Fatalf("audit actor not the token name (%d rows)", rows)
	}

	noAuth, _ := http.Get(base + "/api/v1/snapshot")
	if noAuth.StatusCode != 401 {
		t.Fatalf("unauthenticated: %d", noAuth.StatusCode)
	}
}

func TestChannelsWithoutGoogle(t *testing.T) {
	cfg := Defaults()
	cfg.DataDir = t.TempDir()
	now := time.Date(2026, 11, 30, 10, 0, 0, 0, time.Local) // a Monday
	d, err := newDaemon(cfg, deps{now: func() time.Time { return now }, fxDoer: noFX{}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.st.Close()
	ch := d.Channels()
	if ch.CalendarError != "Google not configured" || ch.EmailError != "Google not configured" {
		t.Fatalf("%+v", ch)
	}
	// Monday 10:00 with no digest sent yet: due now (today at 09:00).
	if want := time.Date(2026, 11, 30, 9, 0, 0, 0, time.Local); !ch.NextDigest.Equal(want) {
		t.Fatalf("next digest %v want %v", ch.NextDigest, want)
	}
}
