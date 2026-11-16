package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Typed bridge errors. None of their strings contains the token or the
// bridge's raw message text.
var (
	ErrAuth             = errors.New("bridge refused the token")
	ErrNotFound         = errors.New("bridge has no such route")
	ErrApprovalMismatch = errors.New("approval no longer matches the pending request")
	ErrTooLarge         = fmt.Errorf("request is larger than the bridge's %d-byte limit", MaxRequestBytes)
	ErrUnreachable      = errors.New("bridge unreachable")
	ErrBadResponse      = errors.New("bridge sent a malformed reply")
	ErrResponseTooLarge = errors.New("bridge reply is larger than the plugin accepts")
)

// ErrBadRequest is a 400 reply. Code is the bridge's short error identifier,
// such as empty_prompt or invalid_model.
type ErrBadRequest struct{ Code string }

func (e *ErrBadRequest) Error() string {
	if e.Code == "" {
		return "bridge rejected the request"
	}
	return "bridge rejected the request: " + e.Code
}

// ErrBridge is any other failure status, usually 500. The bridge's message
// may be raw hermes output; it is kept out of Error and only DebugMessage
// returns it, with the token redacted, for the debug log.
type ErrBridge struct {
	Status  int
	Code    string
	message string
}

func (e *ErrBridge) Error() string {
	s := "bridge error (HTTP " + strconv.Itoa(e.Status) + ")"
	if e.Code != "" {
		s += ": " + e.Code
	}
	return s
}

// DebugMessage is the bridge's message text with token redacted.
func (e *ErrBridge) DebugMessage(token string) string { return Redact(e.message, token) }

// Redact replaces every occurrence of token in s.
func Redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[redacted]")
}

// unreachableError wraps a transport failure so errors.Is matches both
// ErrUnreachable and the cause.
type unreachableError struct{ err error }

func (e *unreachableError) Error() string        { return "bridge unreachable: " + e.err.Error() }
func (e *unreachableError) Unwrap() error        { return e.err }
func (e *unreachableError) Is(target error) bool { return target == ErrUnreachable }

// Health is the /health reply.
type Health struct {
	Bridge BridgeInfo `json:"bridge"`
}

// Detect is the /detect reply: the bridge host's view of its Hermes install.
type Detect struct {
	HermesHome       string        `json:"hermesHome"`
	HermesCommand    string        `json:"hermesCommand"`
	HermesHomeExists bool          `json:"hermesHomeExists"`
	ConfigExists     bool          `json:"configExists"`
	Gateway          DetectGateway `json:"gateway"`
	Model            DetectModel   `json:"model"`
	BridgeHost       string        `json:"bridgeHost"`
	BridgePort       int           `json:"bridgePort"`
	StateFile        string        `json:"stateFile"`
}

// DetectGateway is /detect's gateway object.
type DetectGateway struct {
	Status    string            `json:"status"`
	PID       string            `json:"pid"`
	Platforms map[string]string `json:"platforms"`
}

// DetectModel is /detect's default model.
type DetectModel struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// Snapshot is a normalized reply: the state when the reply carried one, and
// the result object of a {result,state} or {result} envelope.
type Snapshot struct {
	State    State
	HasState bool
	Result   json.RawMessage
}

// SessionSummary is one /sessions row.
type SessionSummary struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Preview   string          `json:"preview"`
	StartedAt json.RawMessage `json:"started_at"`
}

// Bridge is an HTTP client for one Hermes bridge.
type Bridge struct {
	BaseURL string
	token   string
	HTTP    *http.Client
}

// String never includes the token.
func (b Bridge) String() string   { return "Bridge(" + b.BaseURL + ")" }
func (b Bridge) GoString() string { return b.String() }

// NewBridge builds a client for http://host:port. host is an IP literal or a
// DNS name; any valid host is accepted, loopback or not (see IsLoopback for
// the cleartext warning).
func NewBridge(host string, port int, token string) (*Bridge, error) {
	if err := validHost(host); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("bridge port %d is outside 1-65535", port)
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return &Bridge{
		BaseURL: "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		token:   token,
		HTTP:    &http.Client{Transport: newTransport()},
	}, nil
}

// newTransport never consults proxy environment variables: the bridge is
// reached directly or through the user's own SSH forward.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0, // per-call contexts bound every request
	}
}

// validHost accepts an IPv4 or IPv6 literal (bare or bracketed) or a DNS
// name, and nothing that could carry a scheme, path, query, credentials,
// port or whitespace.
func validHost(host string) error {
	if host == "" {
		return errors.New("bridge host is empty")
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if ip := net.ParseIP(host[1 : len(host)-1]); ip != nil && strings.Contains(host, ":") {
			return nil
		}
		return fmt.Errorf("bridge host %q is not a valid address", host)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > 253 {
		return errors.New("bridge host is longer than 253 characters")
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("bridge host %q is not a valid name", host)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return fmt.Errorf("bridge host %q is not a host name or address; enter only the host, without a scheme, path or port", host)
			}
		}
	}
	return nil
}

// validPath accepts a bridge route such as /prompt or /gateway/start.
func validPath(path string) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return fmt.Errorf("bridge path %q must start with a single /", path)
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '/' || c == '-' || c == '_' || c == '.') {
			return fmt.Errorf("bridge path %q contains a character routes never use", path)
		}
	}
	return nil
}

// Health asks /health, which needs no token.
func (b *Bridge) Health(ctx context.Context) (Health, error) {
	var h Health
	data, err := b.do(ctx, http.MethodGet, "/health", nil, false)
	if err != nil {
		return h, err
	}
	if err := decodeObject(data, &h); err != nil {
		return Health{}, err
	}
	return h, nil
}

// Detect asks /detect, which needs no token.
func (b *Bridge) Detect(ctx context.Context) (Detect, error) {
	var d Detect
	data, err := b.do(ctx, http.MethodGet, "/detect", nil, false)
	if err != nil {
		return d, err
	}
	if err := decodeObject(data, &d); err != nil {
		return Detect{}, err
	}
	return d, nil
}

// State fetches /state.
func (b *Bridge) State(ctx context.Context) (State, error) {
	data, err := b.do(ctx, http.MethodGet, "/state", nil, true)
	if err != nil {
		return State{}, err
	}
	snap, err := snapshotOf(data)
	if err != nil {
		return State{}, err
	}
	if !snap.HasState {
		return State{}, ErrBadResponse
	}
	return snap.State, nil
}

// Sessions lists recent sessions. A bridge without the route answers
// ErrNotFound and a nil list; an empty history is a non-nil empty list.
func (b *Bridge) Sessions(ctx context.Context) ([]SessionSummary, error) {
	data, err := b.do(ctx, http.MethodGet, "/sessions", nil, true)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Sessions []SessionSummary `json:"sessions"`
	}
	if err := decodeObject(data, &reply); err != nil {
		return nil, err
	}
	if reply.Sessions == nil {
		reply.Sessions = []SessionSummary{}
	}
	return reply.Sessions, nil
}

// Post sends body as JSON to an authenticated route and normalizes the
// reply. A nil body sends no body, which the bridge reads as {}.
func (b *Bridge) Post(ctx context.Context, path string, body any) (Snapshot, error) {
	if err := validPath(path); err != nil {
		return Snapshot{}, err
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return Snapshot{}, fmt.Errorf("encode %s request: %w", path, err)
		}
		if len(payload) > MaxRequestBytes {
			return Snapshot{}, ErrTooLarge
		}
	}
	data, err := b.do(ctx, http.MethodPost, path, payload, true)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotOf(data)
}

// OneShot runs one prompt through /oneshot, with the long one-shot timeout.
// Cancelling ctx ends the request; the bridge may keep running the prompt.
func (b *Bridge) OneShot(ctx context.Context, text string) (Snapshot, error) {
	return b.Post(ctx, "/oneshot", struct {
		Text string `json:"text"`
	}{text})
}

// do performs one request with the path's timeout and response cap.
func (b *Bridge) do(ctx context.Context, method, path string, body []byte, auth bool) ([]byte, error) {
	if err := validPath(path); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, timeoutFor(path))
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(callCtx, method, b.BaseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("bridge request %s: %w", path, ErrBadResponse)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if auth && b.token != "" {
		req.Header.Set("X-Bridge-Token", b.token)
	}
	client := b.HTTP
	if client == nil {
		client = &http.Client{Transport: newTransport()}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &unreachableError{err: errors.Unwrap(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	limit := responseCapFor(path)
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &unreachableError{err: err}
	}
	if int64(len(data)) > limit {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

// statusError maps a failure status and its {error,message} body.
func statusError(resp *http.Response) error {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, errorResponseCap))
	_ = json.Unmarshal(data, &body)
	code := errorCode(body.Error)
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return &ErrBadRequest{Code: code}
	case http.StatusForbidden:
		return ErrAuth
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusRequestEntityTooLarge:
		return ErrTooLarge
	case http.StatusConflict:
		if code == "approval_mismatch" {
			return ErrApprovalMismatch
		}
	}
	return &ErrBridge{Status: resp.StatusCode, Code: code, message: body.Message}
}

// errorCode keeps a bridge error code only if it looks like an identifier,
// so a hostile reply cannot put arbitrary text into an error string.
func errorCode(s string) string {
	if len(s) > 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return ""
		}
	}
	return s
}

// decodeObject decodes a JSON object reply.
func decodeObject(data []byte, v any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return ErrBadResponse
	}
	if err := json.Unmarshal(data, v); err != nil {
		return ErrBadResponse
	}
	return nil
}

// snapshotOf normalizes a bare snapshot, a {state} envelope, a
// {result,state} envelope and a result-only {result} reply.
func snapshotOf(data []byte) (Snapshot, error) {
	var top map[string]json.RawMessage
	if err := decodeObject(data, &top); err != nil {
		return Snapshot{}, err
	}
	stateRaw, hasState := top["state"]
	result, hasResult := top["result"]
	_, hasBridge := top["bridge"]
	var snap Snapshot
	switch {
	case hasState:
		snap.Result = result
		if bytes.Equal(bytes.TrimSpace(stateRaw), []byte("null")) {
			return snap, nil
		}
		state, err := stateOf(stateRaw)
		if err != nil {
			return Snapshot{}, err
		}
		snap.State, snap.HasState = state, true
	case hasResult && !hasBridge:
		snap.Result = result
	default:
		state, err := stateOf(bytes.TrimSpace(data))
		if err != nil {
			return Snapshot{}, err
		}
		snap.State, snap.HasState = state, true
	}
	return snap, nil
}

// stateOf decodes one snapshot object into the State mirror. A field of the
// wrong shape is left zero for Normalize rather than failing the reply.
func stateOf(raw json.RawMessage) (State, error) {
	var state State
	if err := decodeObject(raw, &state); err != nil {
		return State{}, err
	}
	return state, nil
}
