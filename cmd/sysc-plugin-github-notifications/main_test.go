package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	githubnotifications "github.com/Nomadcxx/sysc-plugins/plugins/github-notifications"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func TestBarClicksOpenGitHubPanel(t *testing.T) {
	type clickEvent struct {
		event  v1.EventKind
		button v1.PointerButton
	}
	cases := []struct {
		name   string
		events []clickEvent
	}{
		{name: "left", events: []clickEvent{
			{event: v1.EventPointer, button: v1.ButtonPrimary},
			{event: v1.EventActivate},
		}},
		{name: "right", events: []clickEvent{
			{event: v1.EventPointer, button: v1.ButtonSecondary},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pluginIn, hostWriter := io.Pipe()
			hostReader, pluginOut := io.Pipe()
			hostEncoder := v1.NewEncoder(hostWriter)
			hostDecoder := v1.NewDecoder(hostReader, v1.ToHost)
			var writeMu sync.Mutex
			send := func(msg v1.Message) error {
				writeMu.Lock()
				defer writeMu.Unlock()
				return hostEncoder.Encode(msg)
			}

			done := make(chan error, 1)
			go func() { done <- runPlugin(pluginIn, pluginOut, barTestGH{}, nil) }()

			pluginHello := make(chan *v1.PluginHello, 1)
			panelCalls := make(chan *v1.HostCall, 2)
			snapshots := make(chan *v1.ViewSnapshot, 4)
			refreshSaved := make(chan struct{}, 1)
			go func() {
				for {
					msg, err := hostDecoder.Decode()
					if err != nil {
						return
					}
					switch msg := msg.(type) {
					case *v1.PluginHello:
						pluginHello <- msg
					case *v1.ViewSnapshot:
						snapshots <- msg
					case *v1.HostCall:
						if msg.Call == v1.CallPanelOpen {
							panelCalls <- msg
							continue
						}
						result := json.RawMessage(`null`)
						if msg.Call == v1.CallStateGet {
							result = json.RawMessage(`{"found":false}`)
						}
						if err := send(&v1.HostReply{ID: msg.ID, OK: true, Result: result}); err != nil {
							return
						}
						if msg.Call == v1.CallStateSet {
							select {
							case refreshSaved <- struct{}{}:
							default:
							}
						}
					}
				}
			}()

			cleanup := func() {
				_ = send(&v1.HostShutdown{})
				_ = hostWriter.Close()
				_ = pluginIn.Close()
				_ = hostReader.Close()
				_ = pluginOut.Close()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("plugin did not stop")
				}
			}
			t.Cleanup(cleanup)

			if err := send(&v1.HostHello{Supported: []v1.Version{{Major: v1.ProtocolMajor, Minor: v1.ProtocolMinor}}}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-pluginHello:
			case <-time.After(2 * time.Second):
				t.Fatal("plugin handshake timed out")
			}
			select {
			case <-refreshSaved:
			case <-time.After(3 * time.Second):
				t.Fatal("initial refresh did not finish")
			}

			const viewID = "github-bar"
			const instance = "github-notifications-smoke"
			if err := send(&v1.ViewOpen{ViewID: viewID, View: v1.ViewBar, Entry: "bar", Instance: instance, Output: "DP-1", Generation: 7}); err != nil {
				t.Fatal(err)
			}
			var snapshot *v1.ViewSnapshot
			select {
			case snapshot = <-snapshots:
			case <-time.After(2 * time.Second):
				t.Fatal("bar snapshot timed out")
			}
			for _, event := range tc.events {
				if err := send(&v1.InputEvent{ViewID: viewID, Revision: snapshot.Revision, Node: "open", Event: event.event, Button: event.button, Output: "DP-1", Generation: 7}); err != nil {
					t.Fatal(err)
				}
			}
			var calls []*v1.HostCall
			deadline := time.NewTimer(300 * time.Millisecond)
			defer deadline.Stop()
		collect:
			for {
				select {
				case call := <-panelCalls:
					calls = append(calls, call)
					if err := send(&v1.HostReply{ID: call.ID, OK: true, Result: json.RawMessage(`null`)}); err != nil {
						t.Fatal(err)
					}
				case <-deadline.C:
					break collect
				}
			}
			if len(calls) != 1 {
				t.Fatalf("bar click made %d panel.open calls, want exactly one", len(calls))
			}
			var got v1.PanelParams
			if err := json.Unmarshal(calls[0].Params, &got); err != nil {
				t.Fatal(err)
			}
			want := v1.PanelParams{Entry: "panel", Output: "DP-1", Generation: 7, Instance: instance}
			if got != want {
				t.Fatalf("panel.open params = %+v, want %+v", got, want)
			}
		})
	}
}

type barTestGH struct{}

func (barTestGH) Notifications(context.Context, int, int) ([]githubnotifications.RawItem, error) {
	return nil, nil
}

func (barTestGH) Search(context.Context, githubnotifications.WorkKind, int, int) (githubnotifications.RawWorkPage, error) {
	return githubnotifications.RawWorkPage{}, nil
}

func (barTestGH) Activity(context.Context, time.Time, time.Time) (githubnotifications.RawActivity, error) {
	return githubnotifications.RawActivity{}, nil
}

func (barTestGH) MarkRead(context.Context, string) error { return nil }
func (barTestGH) MarkAll(context.Context) error          { return nil }

// fakeOpener replaces xdg-open with a script for openURL tests.
func fakeOpener(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestOpenURLDoesNotWaitForBrowser(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeOpener(t, "#!/bin/sh\necho $$ > "+pidFile+"\nsleep 30\n")

	start := time.Now()
	err := openURL(context.Background(), "https://github.com/Nomadcxx/sysc-plugins/issues/1")
	if err != nil {
		t.Fatalf("opening with a browser that stays running: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("openURL waited %v for the opener", elapsed)
	}

	var pid int
	deadline := time.Now().Add(time.Second)
	for {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("opener never ran: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer syscall.Kill(-pid, syscall.SIGKILL)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("opener was killed instead of detached: %v", err)
	}
}

func TestOpenURLReportsImmediateOpenerFailure(t *testing.T) {
	fakeOpener(t, "#!/bin/sh\nexit 3\n")
	if err := openURL(context.Background(), "https://github.com/Nomadcxx/sysc-plugins/issues/1"); err == nil {
		t.Fatal("an opener that fails immediately was reported as a successful open")
	}
}
