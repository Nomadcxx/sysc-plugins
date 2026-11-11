package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeToken is the only token these tests use.
const fakeToken = "fake-bridge-token-9f3a"

const snapshotJSON = `{"bridge":{"status":"online","error":""},"hermes":{"status":"idle"},"session":{"id":"s1","running":false},"messages":[{"role":"user","text":"hi"}]}`

// testBridge points a Bridge at an httptest server.
func testBridge(t *testing.T, token string, h http.HandlerFunc) *Bridge {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	b, err := NewBridge(host, port, token)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestNewBridgeAcceptsAnyValidHost(t *testing.T) {
	for _, tc := range []struct{ host, url string }{
		{"127.0.0.1", "http://127.0.0.1:19777"},
		{"::1", "http://[::1]:19777"},
		{"localhost", "http://localhost:19777"},
		{"192.168.0.10", "http://192.168.0.10:19777"},
		{"example.com", "http://example.com:19777"},
	} {
		b, err := NewBridge(tc.host, 19777, "")
		if err != nil {
			t.Errorf("NewBridge(%q) = %v, want accepted", tc.host, err)
			continue
		}
		if b.BaseURL != tc.url {
			t.Errorf("NewBridge(%q).BaseURL = %q, want %q", tc.host, b.BaseURL, tc.url)
		}
		if want := tc.host == "127.0.0.1" || tc.host == "::1" || tc.host == "localhost"; IsLoopback(tc.host) != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tc.host, !want, want)
		}
	}
	for _, host := range []string{"http://x", "x/y", "x y", "x\r\n", "", "x?y", "x#y", "user@x", "x:80", "-x.com", "a..b"} {
		if _, err := NewBridge(host, 19777, ""); err == nil {
			t.Errorf("NewBridge(%q) accepted, want rejected", host)
		}
	}
	for _, port := range []int{0, -1, 65536} {
		if _, err := NewBridge("127.0.0.1", port, ""); err == nil {
			t.Errorf("NewBridge port %d accepted, want rejected", port)
		}
	}
	if _, err := NewBridge("127.0.0.1", 65535, ""); err != nil {
		t.Errorf("port 65535: %v", err)
	}
}

func TestBridgeTransportIgnoresProxies(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	b, err := NewBridge("192.168.0.10", 19777, "")
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := b.HTTP.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Fatalf("transport = %#v, want an *http.Transport with Proxy nil", b.HTTP.Transport)
	}
}

func TestHealthAndDetectSendNoTokenStateSendsItExactly(t *testing.T) {
	seen := map[string][]string{}
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Values("X-Bridge-Token")
		if r.Header.Get("Authorization") != "" {
			t.Errorf("%s sent an Authorization header", r.URL.Path)
		}
		switch r.URL.Path {
		case "/health":
			reply(w, 200, `{"bridge":{"status":"online","error":""}}`)
		case "/detect":
			reply(w, 200, `{"hermesHome":"/home/fixture/.hermes","hermesCommand":"hermes","hermesHomeExists":true,"configExists":true,"gateway":{"status":"running","pid":"42","platforms":{}},"model":{"name":"example-model","provider":"example"},"bridgeHost":"127.0.0.1","bridgePort":19777,"stateFile":"~/.cache/noctalia-hermes/state.json"}`)
		case "/state":
			reply(w, 200, snapshotJSON)
		}
	})
	ctx := context.Background()
	health, err := b.Health(ctx)
	if err != nil || health.Bridge.Status != "online" {
		t.Fatalf("Health = %+v, %v", health, err)
	}
	det, err := b.Detect(ctx)
	if err != nil || det.HermesHome != "/home/fixture/.hermes" || det.Model.Name != "example-model" || det.Model.Provider != "example" || det.Gateway.Status != "running" || det.BridgePort != 19777 {
		t.Fatalf("Detect = %+v, %v", det, err)
	}
	if _, err := b.State(ctx); err != nil {
		t.Fatal(err)
	}
	if len(seen["/health"]) != 0 || len(seen["/detect"]) != 0 {
		t.Fatalf("unauthenticated routes sent a token header: health %d detect %d values", len(seen["/health"]), len(seen["/detect"]))
	}
	if got := seen["/state"]; len(got) != 1 || got[0] != fakeToken {
		t.Fatalf("/state sent %d X-Bridge-Token values; want exactly the token", len(got))
	}
}

func TestForbiddenStateNeverEchoesTheToken(t *testing.T) {
	for _, token := range []string{fakeToken, ""} {
		b := testBridge(t, token, func(w http.ResponseWriter, r *http.Request) {
			reply(w, 403, `{"error":"forbidden"}`)
		})
		_, err := b.State(context.Background())
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("token %t: err = %v, want ErrAuth", token != "", err)
		}
		if strings.Contains(err.Error(), fakeToken) || strings.Contains(fmt.Sprintf("%+v %#v", err, err), fakeToken) {
			t.Fatal("403 error contains the token")
		}
	}
	b, _ := NewBridge("127.0.0.1", 19777, fakeToken)
	if s := fmt.Sprintf("%v %+v %#v %s", b, b, b, *b); strings.Contains(s, fakeToken) {
		t.Fatal("formatting a Bridge prints its token")
	}
}

func TestEveryEnvelopeNormalizes(t *testing.T) {
	for name, body := range map[string]string{
		"bare":   snapshotJSON,
		"state":  `{"state":` + snapshotJSON + `}`,
		"result": `{"result":{"status":"completed"},"state":` + snapshotJSON + `}`,
	} {
		b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, body) })
		snap, err := b.Post(context.Background(), "/refresh", map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !snap.HasState || snap.State.Bridge.Status != "online" {
			t.Fatalf("%s: state = %+v", name, snap.State)
		}
		var want State
		if err := json.Unmarshal([]byte(snapshotJSON), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snap.State, want) {
			t.Fatalf("%s: state %+v differs from the bare snapshot %+v", name, snap.State, want)
		}
		if name == "result" && string(snap.Result) != `{"status":"completed"}` {
			t.Fatalf("result envelope lost its result: %s", snap.Result)
		}
		state, err := b.State(context.Background())
		if err != nil || state.Bridge.Status != "online" {
			t.Fatalf("%s via State: %+v, %v", name, state, err)
		}
	}
	// /gateway/start answers {result} with no state.
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, `{"result":{"status":"started"}}`) })
	snap, err := b.Post(context.Background(), "/gateway/start", nil)
	if err != nil || snap.HasState || string(snap.Result) != `{"status":"started"}` {
		t.Fatalf("result-only reply: %+v, %v", snap, err)
	}
}

func TestBadRepliesMapToTypedErrors(t *testing.T) {
	longMessage := "hermes: traceback " + strings.Repeat("secret-ish stderr ", 200)
	big := `{"bridge":{"status":"online"},"pad":"` + strings.Repeat("x", 4<<20) + `"}`
	cases := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"malformed", 200, `{"bridge":`, func(e error) bool { return errors.Is(e, ErrBadResponse) }},
		{"empty", 200, ``, func(e error) bool { return errors.Is(e, ErrBadResponse) }},
		{"not an object", 200, `[1,2]`, func(e error) bool { return errors.Is(e, ErrBadResponse) }},
		{"over the cap", 200, big, func(e error) bool { return errors.Is(e, ErrResponseTooLarge) }},
		{"413", 413, `{"error":"payload_too_large"}`, func(e error) bool { return errors.Is(e, ErrTooLarge) }},
		{"404", 404, `{"error":"not_found"}`, func(e error) bool { return errors.Is(e, ErrNotFound) }},
		{"403", 403, `{"error":"forbidden"}`, func(e error) bool { return errors.Is(e, ErrAuth) }},
		{"409 mismatch", 409, `{"error":"approval_mismatch"}`, func(e error) bool { return errors.Is(e, ErrApprovalMismatch) }},
		{"500", 500, `{"error":"oneshot_failed","message":` + strconv.Quote(longMessage) + `}`, func(e error) bool {
			var be *ErrBridge
			return errors.As(e, &be) && be.Code == "oneshot_failed" && be.Status == 500 && strings.Contains(be.DebugMessage(fakeToken), "traceback")
		}},
		{"500 no body", 500, ``, func(e error) bool {
			var be *ErrBridge
			return errors.As(e, &be) && be.Code == ""
		}},
	}
	for _, code := range []string{"empty_prompt", "missing_session_id", "empty_model", "invalid_json", "invalid_payload", "invalid_model", "something_new"} {
		code := code
		cases = append(cases, struct {
			name   string
			status int
			body   string
			check  func(error) bool
		}{"400 " + code, 400, `{"error":"` + code + `"}`, func(e error) bool {
			var br *ErrBadRequest
			return errors.As(e, &br) && br.Code == code
		}})
	}
	for _, tc := range cases {
		b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) { reply(w, tc.status, tc.body) })
		_, err := b.Post(context.Background(), "/prompt", map[string]string{"text": "hello"})
		if err == nil || !tc.check(err) {
			t.Errorf("%s: err = %v (%T), want the typed error", tc.name, err, err)
			continue
		}
		if strings.Contains(err.Error(), fakeToken) {
			t.Errorf("%s: error contains the token", tc.name)
		}
		if strings.Contains(err.Error(), "traceback") || strings.Contains(err.Error(), "stderr") {
			t.Errorf("%s: error %q contains the bridge message", tc.name, err)
		}
	}
}

func TestDebugMessageRedactsTheToken(t *testing.T) {
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 500, `{"error":"gateway_start_failed","message":"bad token `+fakeToken+` here"}`)
	})
	_, err := b.Post(context.Background(), "/gateway/start", nil)
	var be *ErrBridge
	if !errors.As(err, &be) {
		t.Fatalf("err = %v", err)
	}
	if msg := be.DebugMessage(fakeToken); strings.Contains(msg, fakeToken) || !strings.Contains(msg, "bad token") {
		t.Fatalf("DebugMessage = %q, want the message with the token redacted", msg)
	}
}

func TestUnreachableBridge(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	b, _ := NewBridge("127.0.0.1", port, fakeToken)
	_, err = b.State(context.Background())
	if !errors.Is(err, ErrUnreachable) || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("closed port: err = %v, want ErrUnreachable without the token", err)
	}
}

func TestSessionsNotFoundIsNotAnEmptyList(t *testing.T) {
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) { reply(w, 404, `{"error":"not_found"}`) })
	if list, err := b.Sessions(context.Background()); !errors.Is(err, ErrNotFound) || list != nil {
		t.Fatalf("404: %v, %v; want ErrNotFound and no list", list, err)
	}
	b = testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, `{"sessions":[]}`) })
	list, err := b.Sessions(context.Background())
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty: %#v, %v; want a non-nil empty list", list, err)
	}
	b = testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Bridge-Token") != fakeToken {
			reply(w, 403, `{"error":"forbidden"}`)
			return
		}
		reply(w, 200, `{"sessions":[{"id":"abc123","title":"Release notes","preview":"tidy","started_at":1762860000}]}`)
	})
	list, err = b.Sessions(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != "abc123" || list[0].Title != "Release notes" {
		t.Fatalf("one session: %+v, %v", list, err)
	}
	big := `{"sessions":[],"pad":"` + strings.Repeat("x", 1<<20) + `"}`
	b = testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, big) })
	if _, err := b.Sessions(context.Background()); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("1 MiB+ sessions: %v, want ErrResponseTooLarge", err)
	}
}

func TestOversizedPromptNeverReachesTheServer(t *testing.T) {
	var hits atomic.Int32
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		reply(w, 200, snapshotJSON)
	})
	huge := strings.Repeat("p", MaxRequestBytes)
	if _, err := b.Post(context.Background(), "/prompt", map[string]string{"text": huge}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Post 1 MB+ = %v, want ErrTooLarge", err)
	}
	if _, err := b.OneShot(context.Background(), huge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("OneShot 1 MB+ = %v, want ErrTooLarge", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server saw %d requests, want none", hits.Load())
	}
	if _, err := b.Post(context.Background(), "/prompt", map[string]string{"text": "small"}); err != nil || hits.Load() != 1 {
		t.Fatalf("small prompt: %v, hits %d", err, hits.Load())
	}
}

func TestPostRejectsBadPaths(t *testing.T) {
	b, _ := NewBridge("127.0.0.1", 19777, fakeToken)
	for _, p := range []string{"", "prompt", "//evil.example/x", "/x?y=1", "/x#y", "/a b", "/x\r\n"} {
		if _, err := b.Post(context.Background(), p, nil); err == nil || errors.Is(err, ErrUnreachable) {
			t.Errorf("Post(%q) = %v, want a path error before any I/O", p, err)
		}
	}
}

// deadlineTransport records each request's deadline and body.
type deadlineTransport struct {
	next      http.RoundTripper
	deadlines map[string]time.Duration
	bodies    map[string]string
}

func (d *deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if dl, ok := r.Context().Deadline(); ok {
		d.deadlines[r.URL.Path] = time.Until(dl)
	}
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		d.bodies[r.URL.Path] = string(data)
		r.Body = io.NopCloser(strings.NewReader(string(data)))
	}
	return d.next.RoundTrip(r)
}

func TestEachCallGetsItsTimeout(t *testing.T) {
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions" {
			reply(w, 200, `{"sessions":[]}`)
			return
		}
		reply(w, 200, snapshotJSON)
	})
	rec := &deadlineTransport{next: b.HTTP.Transport, deadlines: map[string]time.Duration{}, bodies: map[string]string{}}
	b.HTTP.Transport = rec
	ctx := context.Background()
	_, _ = b.State(ctx)
	_, _ = b.Sessions(ctx)
	_, _ = b.Post(ctx, "/interrupt", nil)
	_, _ = b.Post(ctx, "/gateway/start", nil)
	_, _ = b.OneShot(ctx, "summarize")
	for path, want := range map[string]time.Duration{
		"/state": 10 * time.Second, "/sessions": 15 * time.Second, "/interrupt": 30 * time.Second,
		"/gateway/start": 40 * time.Second, "/oneshot": 11 * time.Minute,
	} {
		got, ok := rec.deadlines[path]
		if !ok || got > want || got < want-5*time.Second {
			t.Errorf("%s deadline in %v, want about %v", path, got, want)
		}
	}
	if rec.bodies["/oneshot"] != `{"text":"summarize"}` {
		t.Errorf("one-shot body = %q", rec.bodies["/oneshot"])
	}
}

func TestOneShotEndsOnCancel(t *testing.T) {
	release := make(chan struct{})
	b := testBridge(t, fakeToken, func(w http.ResponseWriter, r *http.Request) {
		// Reading the body lets the server notice the client hang up.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	// Registered after testBridge, so it runs before the server's Close.
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := b.OneShot(ctx, "a slow question")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled one-shot: %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
}
