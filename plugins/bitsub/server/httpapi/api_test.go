package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ctx = context.Background()

type api struct {
	srv    *httptest.Server
	st     *store.Store
	svc    *service.Service
	tokens map[string]string // name → plaintext
}

func newAPI(t *testing.T) *api {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bitsub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 29, 10, 0, 0, 0, time.UTC)
	svc := service.New(st, func() time.Time { return now }, time.UTC, nil, nil, "archPC")
	a := &api{st: st, svc: svc, tokens: map[string]string{}}
	var list []Token
	for _, name := range []string{"laptop-plugin", "claude-archpc"} {
		plain, sha, err := GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		a.tokens[name] = plain
		list = append(list, Token{Name: name, SHA256: sha})
	}
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "mcp:"+ActorFrom(r.Context())) })
	a.srv = httptest.NewServer(NewHandler(svc, NewAuth(list), mcp))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *api) do(t *testing.T, method, path, token, body string) (int, map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

func (a *api) add(t *testing.T, name string) subs.Subscription {
	t.Helper()
	s, err := a.svc.Add(ctx, "test", service.AddInput{Name: name, Amount: "10", Currency: "AUD", Cycle: subs.Month, AnchorDate: "2026-12-10"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGenerateToken(t *testing.T) {
	plain, sha, err := GenerateToken()
	if err != nil || !strings.HasPrefix(plain, "bsk_") || len(plain) < 40 || len(sha) != 64 {
		t.Fatalf("%q %q %v", plain, sha, err)
	}
	other, _, _ := GenerateToken()
	if other == plain {
		t.Fatal("tokens repeat")
	}
}

func TestNoTokenIs401(t *testing.T) {
	a := newAPI(t)
	for _, path := range []string{"/api/v1/snapshot", "/mcp"} {
		code, m, _ := a.do(t, "GET", path, "", "")
		if code != 401 || m["error"] != "unauthorized" {
			t.Errorf("%s: %d %v", path, code, m)
		}
	}
	if code, _, _ := a.do(t, "GET", "/api/v1/snapshot", "Basic abc", ""); code != 401 {
		t.Errorf("malformed header: %d", code)
	}
}

func TestRevokedTokenIs401(t *testing.T) {
	a := newAPI(t)
	revoked, _, _ := GenerateToken()
	if code, _, _ := a.do(t, "GET", "/api/v1/snapshot", revoked, ""); code != 401 {
		t.Fatalf("%d", code)
	}
}

func TestMCPMountedBehindAuthWithActor(t *testing.T) {
	a := newAPI(t)
	code, _, body := a.do(t, "POST", "/mcp", a.tokens["claude-archpc"], "{}")
	if code != 200 || body != "mcp:claude-archpc" {
		t.Fatalf("%d %q", code, body)
	}
}

func TestSnapshotSince(t *testing.T) {
	a := newAPI(t)
	s := a.add(t, "Netflix")
	a.st.Tx(ctx, func(tx *store.Tx) error {
		for _, k := range []string{"a", "b", "c"} {
			tx.InsertNotification(k, subs.Notification{SubscriptionID: s.ID, Summary: k, Urgency: "normal", IssuedAt: time.Now()})
		}
		return nil
	})
	code, _, raw := a.do(t, "GET", "/api/v1/snapshot?since=2", a.tokens["laptop-plugin"], "")
	var snap subs.Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil || code != 200 {
		t.Fatalf("%d %v %s", code, err, raw)
	}
	if len(snap.Notifications) != 1 || snap.Notifications[0].Seq != 3 || snap.LastSeq != 3 || len(snap.Items) != 1 || snap.ServedBy != "archPC" {
		t.Fatalf("%+v", snap)
	}
	if code, m, _ := a.do(t, "GET", "/api/v1/snapshot?since=abc", a.tokens["laptop-plugin"], ""); code != 400 || m["field"] != "since" {
		t.Fatalf("bad since: %d %v", code, m)
	}
}

func TestSetIntentRecordsActor(t *testing.T) {
	a := newAPI(t)
	s := a.add(t, "Netflix")
	code, _, raw := a.do(t, "POST", "/api/v1/subscriptions/"+s.ID+"/intent", a.tokens["laptop-plugin"], `{"intent":"keep"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	var rows []store.AuditRow
	a.st.Tx(ctx, func(tx *store.Tx) error { rows, _ = tx.ListAudit(s.ID); return nil })
	if len(rows) != 2 || rows[1].Actor != "laptop-plugin" || rows[1].Action != "set_intent" {
		t.Fatalf("%+v", rows)
	}
}

func TestConfirmCancelled(t *testing.T) {
	a := newAPI(t)
	s := a.add(t, "Stan")
	tok := a.tokens["laptop-plugin"]
	if code, _, raw := a.do(t, "POST", "/api/v1/subscriptions/"+s.ID+"/confirm-cancelled", tok, `{"access_until":"2026-12-31"}`); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	got, _ := a.svc.Get(ctx, s.ID)
	if got.Status != subs.Cancelled || got.AccessUntil.String() != "2026-12-31" {
		t.Fatalf("%+v", got)
	}
	other := a.add(t, "Spotify")
	if code, _, raw := a.do(t, "POST", "/api/v1/subscriptions/"+other.ID+"/confirm-cancelled", tok, ``); code != 200 {
		t.Fatalf("empty body: %d %s", code, raw)
	}
}

func TestBadIntentIs400WithField(t *testing.T) {
	a := newAPI(t)
	s := a.add(t, "Netflix")
	code, m, _ := a.do(t, "POST", "/api/v1/subscriptions/"+s.ID+"/intent", a.tokens["laptop-plugin"], `{"intent":"nuke"}`)
	if code != 400 || m["field"] != "intent" || m["error"] == "" {
		t.Fatalf("%d %v", code, m)
	}
	code, _, _ = a.do(t, "POST", "/api/v1/subscriptions/"+s.ID+"/intent", a.tokens["laptop-plugin"], `{"intent":"keep","extra":1}`)
	if code != 400 {
		t.Fatalf("unknown field accepted: %d", code)
	}
	code, _, _ = a.do(t, "POST", "/api/v1/subscriptions/"+s.ID+"/intent", a.tokens["laptop-plugin"], `{"intent":"keep"}{"intent":"cancel"}`)
	if code != 400 {
		t.Fatalf("trailing data accepted: %d", code)
	}
}

func TestUnknownIDIs404(t *testing.T) {
	a := newAPI(t)
	code, m, _ := a.do(t, "POST", "/api/v1/subscriptions/nope/intent", a.tokens["laptop-plugin"], `{"intent":"keep"}`)
	if code != 404 || m["error"] == "" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	a := newAPI(t)
	s := a.add(t, "Netflix")
	big := `{"intent":"keep","pad":"` + strings.Repeat("x", 17<<10) + `"}`
	code, _, _ := a.do(t, "POST", "/api/v1/subscriptions/"+s.ID+"/intent", a.tokens["laptop-plugin"], big)
	if code != 413 {
		t.Fatalf("%d", code)
	}
}

func TestWrongMethodRejected(t *testing.T) {
	a := newAPI(t)
	if code, _, _ := a.do(t, "POST", "/api/v1/snapshot", a.tokens["laptop-plugin"], ""); code != 405 {
		t.Fatalf("%d", code)
	}
}
