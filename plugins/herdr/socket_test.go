package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCallOnReturnsResult(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	reqs := make(chan request, 1)
	go func() {
		defer server.Close()
		line, err := bufio.NewReader(server).ReadString('\n')
		if err != nil {
			return
		}
		var req request
		_ = json.Unmarshal([]byte(line), &req)
		reqs <- req
		_, _ = server.Write([]byte(`{"id":"c1","result":{"type":"ok","answer":7}}` + "\n"))
	}()

	var got struct {
		Type   string `json:"type"`
		Answer int    `json:"answer"`
	}
	if err := CallOn(context.Background(), client, "session.snapshot", map[string]any{"x": 1}, &got); err != nil {
		t.Fatalf("CallOn: %v", err)
	}
	req := <-reqs
	if req.ID != "c1" {
		t.Errorf("id = %q, want c1", req.ID)
	}
	if req.Method != "session.snapshot" {
		t.Errorf("method = %q", req.Method)
	}
	params, ok := req.Params.(map[string]any)
	if !ok || params["x"] != float64(1) {
		t.Errorf("params = %#v", req.Params)
	}
	if got.Type != "ok" || got.Answer != 7 {
		t.Errorf("result = %+v, want type=ok answer=7", got)
	}
}

func TestCallOnSurfacesAPIError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		defer server.Close()
		_, _ = bufio.NewReader(server).ReadString('\n')
		_, _ = server.Write([]byte(`{"id":"c1","error":{"code":"not_found","message":"no such pane"}}` + "\n"))
	}()

	err := CallOn(context.Background(), client, "agent.focus", map[string]any{"pane_id": "w1:p1"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != "not_found" {
		t.Errorf("code = %q, want not_found", apiErr.Code)
	}
	if apiErr.Error() != "herdr not_found: no such pane" {
		t.Errorf("Error() = %q", apiErr.Error())
	}
}

func TestSubscribeNormalizesGlobalEventNames(t *testing.T) {
	// The real server delivers global events underscore-named (verified live
	// against herdr v0.9.1) while pane-scoped frames already use dots.
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_, _ = bufio.NewReader(server).ReadString('\n')
		_, _ = server.Write([]byte(`{"id":"c1","result":{"type":"subscription_started"}}` + "\n"))
		_, _ = server.Write([]byte(`{"event":"pane_created","data":{"pane":{"pane_id":"w1:p2"}}}` + "\n"))
		_, _ = server.Write([]byte(`{"event":"layout_updated","data":{}}` + "\n"))
		_, _ = server.Write([]byte(`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1"}}` + "\n"))
	}()
	sub, err := subscribeConn(context.Background(), client, []SubSpec{{Type: "pane.created"}})
	if err != nil {
		t.Fatalf("subscribeConn: %v", err)
	}
	defer sub.Close()
	want := []string{"pane.created", "layout.updated", "pane.agent_status_changed"}
	for _, w := range want {
		ev, ok := sub.Next()
		if !ok || ev.Type != w {
			t.Fatalf("event = %+v ok=%v, want type %q", ev, ok, w)
		}
	}
}

func TestSubscribeStreamsEvents(t *testing.T) {
	client, server := net.Pipe()

	go func() {
		defer server.Close()
		_, _ = bufio.NewReader(server).ReadString('\n')
		_, _ = server.Write([]byte(`{"id":"c1","result":{"type":"subscription_started"}}` + "\n"))
		_, _ = server.Write([]byte(`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","agent_status":"working"}}` + "\n"))
		_, _ = server.Write([]byte(`{"event":"pane.output_changed","data":{"pane_id":"w1:p1"}}` + "\n"))
	}()

	sub, err := subscribeConn(context.Background(), client, []SubSpec{{Type: "pane.agent_status_changed", PaneID: "w1:p1"}})
	if err != nil {
		t.Fatalf("subscribeConn: %v", err)
	}
	defer sub.Close()

	ev, ok := sub.Next()
	if !ok || ev.Type != "pane.agent_status_changed" {
		t.Fatalf("first event = %+v ok=%v", ev, ok)
	}
	if !json.Valid(ev.Data) || !strings.Contains(string(ev.Data), "w1:p1") {
		t.Errorf("first data = %s", ev.Data)
	}
	ev2, ok := sub.Next()
	if !ok || ev2.Type != "pane.output_changed" {
		t.Fatalf("second event = %+v ok=%v", ev2, ok)
	}
	if _, ok := sub.Next(); ok {
		t.Fatal("Next returned ok after server closed; want false")
	}
}

func TestSubscribeSendsExactlyOneRequest(t *testing.T) {
	client, server := net.Pipe()

	second := make(chan string, 1)
	go func() {
		defer server.Close()
		br := bufio.NewReader(server)
		if _, err := br.ReadString('\n'); err != nil {
			second <- "first-read-error"
			return
		}
		_, _ = server.Write([]byte(`{"id":"c1","result":{"type":"subscription_started"}}` + "\n"))
		_ = server.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		line, err := br.ReadString('\n')
		if err != nil {
			second <- ""
			return
		}
		second <- line
	}()

	sub, err := subscribeConn(context.Background(), client, []SubSpec{{Type: "workspace.created"}})
	if err != nil {
		t.Fatalf("subscribeConn: %v", err)
	}
	_ = sub.Close()

	select {
	case line := <-second:
		if line != "" {
			t.Fatalf("unexpected second request on subscription conn: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed a second read/timeout")
	}
}

func TestSubscribeSurvivesContextDeadline(t *testing.T) {
	client, server := net.Pipe()

	go func() {
		defer server.Close()
		_, _ = bufio.NewReader(server).ReadString('\n')
		_, _ = server.Write([]byte(`{"id":"c1","result":{"type":"subscription_started"}}` + "\n"))
		time.Sleep(200 * time.Millisecond)
		_, _ = server.Write([]byte(`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1"}}` + "\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	sub, err := subscribeConn(ctx, client, []SubSpec{{Type: "pane.agent_status_changed", PaneID: "w1:p1"}})
	if err != nil {
		t.Fatalf("subscribeConn: %v", err)
	}
	defer sub.Close()

	if ev, ok := sub.Next(); !ok || ev.Type != "pane.agent_status_changed" {
		t.Fatalf("event after ctx deadline: %+v ok=%v; stream should outlive the ack-phase deadline", ev, ok)
	}
}
