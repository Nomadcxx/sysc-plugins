package mcptools

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/httpapi"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/service"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/server/store"
	"github.com/Nomadcxx/sysc-plugins/plugins/bitsub/subs"
)

var ctx = context.Background()

type rates struct{}

func (rates) Current() (subs.Rates, bool) {
	return subs.Rates{Date: subs.MustDate("2026-11-29"), PerAUD: map[subs.Currency]float64{"USD": 0.645}}, true
}

func connect(t *testing.T, auth *httpapi.Auth) (*mcp.ClientSession, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bitsub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 29, 10, 0, 0, 0, time.UTC)
	svc := service.New(st, func() time.Time { return now }, time.UTC, rates{}, nil, "archPC")
	ct, srvT := mcp.NewInMemoryTransports()
	if _, err := NewServer(svc, "test", auth).Connect(ctx, srvT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, st
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	return strings.Join(text, "\n"), res.IsError
}

func decodeInto(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
}

var synthetic = map[string]any{"name": "Synthetic", "amount": "20", "currency": "USD", "cycle": "month", "anchor_date": "2026-12-05"}

func TestToolsListed(t *testing.T) {
	cs, _ := connect(t, nil)
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"add_subscription", "confirm_cancelled", "delete_subscription", "get_subscription", "list_subscriptions",
		"record_charge", "set_intent", "totals", "upcoming", "update_subscription"}
	if !slices.Equal(names, want) {
		t.Fatalf("%v", names)
	}
}

func TestAddThenListShowsAUDAndOriginal(t *testing.T) {
	cs, st := connect(t, nil)
	text, isErr := call(t, cs, "add_subscription", synthetic)
	if isErr {
		t.Fatal(text)
	}
	var added subscriptionOut
	decodeInto(t, text, &added)
	if added.ID == "" || added.PriceDisplay != "A$31.01/mo (US$20.00)" || added.Amount != "20.00" || added.Currency != "USD" ||
		added.Intent != "undecided" || added.NextDate != "2026-12-05" {
		t.Fatalf("%+v", added)
	}
	text, _ = call(t, cs, "list_subscriptions", map[string]any{})
	var list listOut
	decodeInto(t, text, &list)
	if list.Count != 1 || list.Subscriptions[0].PriceDisplay != "A$31.01/mo (US$20.00)" || list.Subscriptions[0].PriceAUD != "A$31.01" ||
		list.Subscriptions[0].PriceOriginal != "US$20.00" {
		t.Fatalf("%+v", list)
	}
	var rows []store.AuditRow
	st.Tx(ctx, func(tx *store.Tx) error { rows, _ = tx.ListAudit(added.ID); return nil })
	if len(rows) != 1 || rows[0].Actor != "mcp" {
		t.Fatalf("actor fallback: %+v", rows)
	}
}

func TestDuplicateReturnsToolErrorNamingExisting(t *testing.T) {
	cs, _ := connect(t, nil)
	text, _ := call(t, cs, "add_subscription", synthetic)
	var first subscriptionOut
	decodeInto(t, text, &first)
	dup := map[string]any{"name": " SYNTHETIC ", "amount": "20", "currency": "USD", "cycle": "month", "anchor_date": "2026-12-05"}
	text, isErr := call(t, cs, "add_subscription", dup)
	if !isErr || !strings.Contains(text, first.ID) || !strings.Contains(text, "Synthetic") || !strings.Contains(text, "allow_duplicate") {
		t.Fatalf("%v %s", isErr, text)
	}
	dup["allow_duplicate"] = true
	if text, isErr := call(t, cs, "add_subscription", dup); isErr {
		t.Fatal(text)
	}
}

func TestValidationErrorNamesField(t *testing.T) {
	cs, _ := connect(t, nil)
	bad := map[string]any{"name": "X", "amount": "0", "currency": "USD", "cycle": "month", "anchor_date": "2026-12-05"}
	text, isErr := call(t, cs, "add_subscription", bad)
	if !isErr || !strings.Contains(text, "amount") {
		t.Fatalf("%v %s", isErr, text)
	}
	text, isErr = call(t, cs, "set_intent", map[string]any{"id": "missing", "intent": "keep"})
	if !isErr || !strings.Contains(text, "list_subscriptions") {
		t.Fatalf("not found: %v %s", isErr, text)
	}
}

func TestSetIntentDescriptionForbidsCancelling(t *testing.T) {
	cs, _ := connect(t, nil)
	res, _ := cs.ListTools(ctx, nil)
	for _, tool := range res.Tools {
		if tool.Name == "set_intent" || tool.Name == "confirm_cancelled" {
			if !strings.Contains(tool.Description, "Never attempt to cancel") {
				t.Errorf("%s: %q", tool.Name, tool.Description)
			}
		}
	}
}

func TestLifecycleThroughTools(t *testing.T) {
	cs, _ := connect(t, nil)
	text, _ := call(t, cs, "add_subscription", synthetic)
	var s subscriptionOut
	decodeInto(t, text, &s)

	if text, isErr := call(t, cs, "get_subscription", map[string]any{"id_or_name": "synthetic"}); isErr || !strings.Contains(text, s.ID) {
		t.Fatalf("get: %s", text)
	}
	text, isErr := call(t, cs, "update_subscription", map[string]any{"id": s.ID, "amount": "25", "notes": "price rise"})
	var upd subscriptionOut
	decodeInto(t, text, &upd)
	if isErr || upd.Amount != "25.00" || upd.Notes != "price rise" || upd.Name != "Synthetic" {
		t.Fatalf("update: %s", text)
	}
	if text, isErr := call(t, cs, "upcoming", map[string]any{"days": 7}); isErr || !strings.Contains(text, s.ID) {
		t.Fatalf("upcoming: %s", text)
	}
	text, isErr = call(t, cs, "totals", map[string]any{})
	var tot totalsOut
	decodeInto(t, text, &tot)
	if isErr || tot.PerMonth != "A$38.76" || tot.Next30Count != 1 {
		t.Fatalf("totals: %s", text)
	}
	if text, isErr := call(t, cs, "record_charge", map[string]any{"id": s.ID, "date": "2026-11-05"}); isErr || !strings.Contains(text, "recorded") {
		t.Fatalf("record_charge: %s", text)
	}
	text, isErr = call(t, cs, "set_intent", map[string]any{"id": s.ID, "intent": "cancel"})
	if isErr || !strings.Contains(text, `"intent":"cancel"`) || !strings.Contains(text, "owner") {
		t.Fatalf("set_intent: %s", text)
	}
	text, isErr = call(t, cs, "confirm_cancelled", map[string]any{"id": s.ID, "access_until": "2026-12-05"})
	if isErr || !strings.Contains(text, `"status":"cancelled"`) {
		t.Fatalf("confirm: %s", text)
	}
	if text, _ := call(t, cs, "list_subscriptions", map[string]any{"status": "cancelled"}); !strings.Contains(text, s.ID) {
		t.Fatalf("cancelled filter: %s", text)
	}
	if text, isErr := call(t, cs, "delete_subscription", map[string]any{"id": s.ID}); isErr || !strings.Contains(text, s.ID) {
		t.Fatalf("delete: %s", text)
	}
}

func TestActorFromBearerHeader(t *testing.T) {
	plain, sha, _ := httpapi.GenerateToken()
	auth := httpapi.NewAuth([]httpapi.Token{{Name: "codex-laptop", SHA256: sha}})
	h := http.Header{}
	h.Set("Authorization", "Bearer "+plain)
	if got := actorOf(auth, &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: h}}); got != "codex-laptop" {
		t.Fatalf("%q", got)
	}
	if got := actorOf(auth, &mcp.CallToolRequest{}); got != "mcp" {
		t.Fatalf("no extra: %q", got)
	}
	if got := actorOf(nil, &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: h}}); got != "mcp" {
		t.Fatalf("no auth: %q", got)
	}
}
