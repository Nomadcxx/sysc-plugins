// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	privacy "github.com/Nomadcxx/sysc-plugins/plugins/privacy"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func validAuditOutput() []byte {
	return []byte(`{"t":"done","grade":"unknown","snapshot":{"schema":1,"available":true,"generated_at":"2026-10-20T12:00:00Z","overall":{"grade":"unknown"},"network":{"vpn":{"state":"unknown"}},"categories":[{"id":"browser","name":"Browser","grade":"unknown","installed":[],"installed_total":0}],"findings":[],"reduced":[]}}`)
}

func TestRunPrivacybitUsesSingleSnapshotCommand(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	binary := filepath.Join(dir, "privacybit")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PRIVACYBIT_ARGS\"\nprintf '%s\\n' '" + string(validAuditOutput()) + "'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PRIVACYBIT_ARGS", argsPath)
	data, err := runPrivacybit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "audit\n--snapshot\n" {
		t.Fatalf("privacybit args = %q, want audit --snapshot", args)
	}
	snapshot, err := privacy.ParseAuditOutput(data)
	if err != nil {
		t.Fatalf("privacybit output did not parse: %v", err)
	}
	if len(snapshot.Categories) != 1 {
		t.Fatalf("privacybit snapshot has %d categories, want a host-accepted category", len(snapshot.Categories))
	}
}

func TestRunPrivacybitFallsBackToUserLocalBin(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(home, "args")
	binary := filepath.Join(dir, "privacybit")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PRIVACYBIT_ARGS\"\nprintf '%s\\n' '" + string(validAuditOutput()) + "'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PRIVACYBIT_ARGS", argsPath)
	if _, err := runPrivacybit(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "audit\n--snapshot\n" {
		t.Fatalf("privacybit args = %q, want audit --snapshot", args)
	}
}

func TestRefreshSnapshotPublishesUnavailableWithoutMisleadingInstallHint(t *testing.T) {
	for name, test := range map[string]struct {
		run        auditRunner
		wantReason string
	}{
		"missing binary": {
			run:        func(context.Context) ([]byte, error) { return nil, errors.New("privacybit was not found on PATH") },
			wantReason: "privacybit was not found on PATH",
		},
		"command failure": {
			run: func(context.Context) ([]byte, error) {
				return nil, errors.New("privacybit audit --snapshot failed: exit status 1")
			},
			wantReason: "audit --snapshot failed",
		},
		"malformed output": {
			run:        func(context.Context) ([]byte, error) { return []byte("not json"), nil },
			wantReason: "decode privacybit event",
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := refreshSnapshot(context.Background(), test.run)
			if snapshot.Available || snapshot.Reason == "" || snapshot.Overall.Grade != "unknown" || !snapshot.Network.TelemetryConnectionsUnknown {
				t.Fatalf("invalid audit did not publish explicit unavailable state: %+v", snapshot)
			}
			if !strings.Contains(snapshot.Reason, test.wantReason) {
				t.Fatalf("unavailable reason = %q, want it to contain %q", snapshot.Reason, test.wantReason)
			}
			if snapshot.Hint != "" {
				t.Fatalf("unavailable audit showed generic installation hint: %q", snapshot.Hint)
			}
		})
	}
}

func TestRefreshSnapshotKeepsValidUnknownSnapshotAfterNoCoverageExit(t *testing.T) {
	snapshot := refreshSnapshot(context.Background(), func(context.Context) ([]byte, error) {
		return validAuditOutput(), errors.New("privacybit exit status 2: no detection source produced coverage")
	})
	if !snapshot.Available || snapshot.Overall.Grade != "unknown" || !snapshot.Network.TelemetryConnectionsUnknown {
		t.Fatalf("valid unknown audit became unavailable or green: %+v", snapshot)
	}
}

func TestPluginFallbackIdentityMatchesManifest(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "..", "plugins", "privacy", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var declared struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifest, &declared); err != nil {
		t.Fatal(err)
	}

	pluginInput, hostInput := io.Pipe()
	hostOutput, pluginOutput := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runPlugin(ctx, pluginInput, pluginOutput, func(context.Context) ([]byte, error) {
			return nil, nil
		})
	}()
	defer func() {
		cancel()
		_ = hostInput.Close()
		_ = pluginOutput.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("privacy plugin did not stop")
		}
	}()

	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 18}},
		"capabilities": []string{"panels", "state"},
	})
	var hello v1.PluginHello
	readPrivacyMessage(t, bufio.NewReader(hostOutput), &hello)
	if hello.Plugin.Version != declared.Version {
		t.Fatalf("fallback identity version = %q, manifest version = %q", hello.Plugin.Version, declared.Version)
	}
}

func TestRunPluginPublishesBoundedStateSnapshot(t *testing.T) {
	pluginInput, hostInput := io.Pipe()
	hostOutput, pluginOutput := io.Pipe()
	defer hostInput.Close()
	defer hostOutput.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runPlugin(ctx, pluginInput, pluginOutput, func(context.Context) ([]byte, error) {
			return validAuditOutput(), nil
		})
	}()
	if _, err := io.WriteString(hostInput, `{"type":"host.hello","supported":[{"major":1,"minor":8}],"capabilities":["state"]}`+"\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(hostOutput)
	hello, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var pluginHello map[string]any
	if err := json.Unmarshal(hello, &pluginHello); err != nil {
		t.Fatal(err)
	}
	if pluginHello["type"] != "plugin.hello" {
		t.Fatalf("first plugin message = %s", hello)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var call v1.HostCall
	if err := json.Unmarshal(line, &call); err != nil {
		t.Fatal(err)
	}
	if call.Call != v1.CallStateSet {
		t.Fatalf("plugin call = %q, want state.set", call.Call)
	}
	var params v1.StateSetParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Key != "control_center" {
		t.Fatalf("state key = %q, want control_center", params.Key)
	}
	var snapshot privacy.Snapshot
	if err := json.Unmarshal(params.Value, &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Available || !snapshot.Network.TelemetryConnectionsUnknown || len(snapshot.Categories) != 1 {
		t.Fatalf("published state lost availability or telemetry unknown: %+v", snapshot)
	}
	reply, err := json.Marshal(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostInput.Write(append(reply, '\n')); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = hostInput.Close()
	_ = pluginOutput.Close()
}

// A host that closes the plugin's pipe stops the plugin. The plugin must
// leave cleanly, or the shell reads the non-zero exit as a crash and
// restarts a plugin that was stopped on purpose.
func TestRunPluginExitsCleanlyWhenTheHostClosesThePipe(t *testing.T) {
	pluginInput, hostInput := io.Pipe()
	hostOutput, pluginOutput := io.Pipe()
	defer hostInput.Close()
	defer hostOutput.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runPlugin(ctx, pluginInput, pluginOutput, func(context.Context) ([]byte, error) {
			return validAuditOutput(), nil
		})
	}()
	if _, err := io.WriteString(hostInput, `{"type":"host.hello","supported":[{"major":1,"minor":8}],"capabilities":["state"]}`+"\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(hostOutput)
	if _, err := reader.ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var call v1.HostCall
	if err := json.Unmarshal(line, &call); err != nil {
		t.Fatal(err)
	}
	reply, err := json.Marshal(v1.HostReply{Type: "host.reply", ID: call.ID, OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostInput.Write(append(reply, '\n')); err != nil {
		t.Fatal(err)
	}

	// The host closes the pipe under the plugin. The plugin reads the
	// closed pipe as io.ErrClosedPipe rather than io.EOF.
	if err := pluginInput.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the plugin reported the closed host pipe as a failure: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the plugin did not stop after the host closed the pipe")
	}
	_ = hostInput.Close()
	_ = pluginOutput.Close()
}

func TestLimitedBufferCapsMemory(t *testing.T) {
	buffer := &limitedBuffer{limit: 8}
	if n, err := buffer.Write([]byte(strings.Repeat("x", 1024))); n != 1024 || err != nil {
		t.Fatalf("limited write = %d, %v", n, err)
	}
	if buffer.Len() != 8 || !buffer.exceeded {
		t.Fatalf("buffer size=%d exceeded=%v, want 8 and true", buffer.Len(), buffer.exceeded)
	}
}

func TestPrivacyManifestDeclaresBarWidgetAndAttachedPanel(t *testing.T) {
	raw, err := os.ReadFile("../../plugins/privacy/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID           string   `json:"id"`
		Capabilities []string `json:"capabilities"`
		Widgets      []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"widgets"`
		Panels []struct {
			ID        string `json:"id"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Placement string `json:"placement"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "org.sysc.privacy" || !slices.Contains(manifest.Capabilities, "state") || !slices.Contains(manifest.Capabilities, "panels") {
		t.Fatalf("manifest identity/capabilities = %q %v", manifest.ID, manifest.Capabilities)
	}
	if len(manifest.Widgets) != 1 || manifest.Widgets[0].ID != "bar" || manifest.Widgets[0].Label == "" {
		t.Fatalf("manifest widgets = %+v, want one labelled bar widget", manifest.Widgets)
	}
	if len(manifest.Panels) != 1 || manifest.Panels[0].ID != "panel" || manifest.Panels[0].Width != 620 || manifest.Panels[0].Height != 720 || manifest.Panels[0].Placement != "attached" {
		t.Fatalf("manifest panels = %+v, want one 620x720 attached panel", manifest.Panels)
	}
}

func TestRunPluginBarActivationOpensDeclaredPanel(t *testing.T) {
	hostInput, reader, stop := startPrivacyPlugin(t, func(context.Context) ([]byte, error) {
		return currentAuditOutput(), nil
	})
	defer stop()
	ackStateCall(t, hostInput, reader)

	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeViewOpen, "view_id": "bar-1", "view": v1.ViewBar,
		"entry": "bar", "instance": "privacy-placement", "output": "DP-1", "generation": 7, "width": 240,
	})
	var view v1.ViewSnapshot
	readPrivacyMessage(t, reader, &view)
	if view.Type != v1.TypeViewSnapshot || view.ViewID != "bar-1" || findPrivacyNode(view.Root, "privacy-open") == nil {
		t.Fatalf("bar view = %+v", view)
	}
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeInputEvent, "view_id": "bar-1", "revision": view.Revision,
		"node": "privacy-open", "event": v1.EventActivate, "output": "DP-1", "generation": 7,
	})
	var call v1.HostCall
	readPrivacyMessage(t, reader, &call)
	if call.Call != v1.CallPanelOpen {
		t.Fatalf("bar activation call = %q, want panel.open", call.Call)
	}
	var params v1.PanelParams
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Entry != "panel" || params.Instance != "privacy-placement" || params.Output != "DP-1" || params.Generation != 7 {
		t.Fatalf("panel.open params = %+v", params)
	}
	replyPrivacyCall(t, hostInput, call.ID)
}

func TestRunPluginPanelRefreshPublishesOneFreshScanAndDropsStaleClicks(t *testing.T) {
	var runs atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	hostInput, reader, stop := startPrivacyPlugin(t, func(ctx context.Context) ([]byte, error) {
		if runs.Add(1) == 2 {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return currentAuditOutput(), nil
	})
	defer stop()
	ackStateCall(t, hostInput, reader)

	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeViewOpen, "view_id": "panel-1", "view": v1.ViewPanel,
		"entry": "panel", "instance": "privacy-placement", "output": "DP-1", "generation": 7,
	})
	var initial v1.ViewSnapshot
	readPrivacyMessage(t, reader, &initial)
	if initial.Type != v1.TypeViewSnapshot || findPrivacyNode(initial.Root, "privacy-scan") == nil {
		t.Fatalf("initial panel view = %+v", initial)
	}
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeInputEvent, "view_id": "panel-1", "revision": initial.Revision,
		"node": "privacy-scan", "event": v1.EventActivate, "output": "DP-1", "generation": 7,
	})
	var busy v1.ViewSnapshot
	readPrivacyMessage(t, reader, &busy)
	button := findPrivacyNode(busy.Root, "privacy-scan")
	if busy.Type != v1.TypeViewSnapshot || busy.Revision <= initial.Revision || button == nil || !button.Disabled || button.Text != "Scanning…" {
		t.Fatalf("busy view = %+v; button = %+v", busy, button)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("manual refresh did not start a second audit")
	}
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeInputEvent, "view_id": "panel-1", "revision": initial.Revision,
		"node": "privacy-scan", "event": v1.EventActivate, "output": "DP-1", "generation": 7,
	})
	close(release)
	var stateCall v1.HostCall
	readPrivacyMessage(t, reader, &stateCall)
	if stateCall.Call != v1.CallStateSet {
		t.Fatalf("after scan call = %q, want state.set", stateCall.Call)
	}
	var state v1.StateSetParams
	if err := json.Unmarshal(stateCall.Params, &state); err != nil {
		t.Fatal(err)
	}
	var snapshot privacy.Snapshot
	if err := json.Unmarshal(state.Value, &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Available || snapshot.GeneratedAt.IsZero() || !snapshot.GeneratedAt.After(time.Now().Add(-time.Minute)) {
		t.Fatalf("manual scan did not publish a fresh snapshot: %+v", snapshot)
	}
	replyPrivacyCall(t, hostInput, stateCall.ID)
	var refreshed v1.ViewSnapshot
	readPrivacyMessage(t, reader, &refreshed)
	if refreshed.Type != v1.TypeViewSnapshot || refreshed.Revision <= busy.Revision {
		t.Fatalf("refreshed panel = %+v", refreshed)
	}
	sendPrivacyMessage(t, hostInput, map[string]any{"type": v1.TypeViewResync, "view_id": "panel-1"})
	var barrier v1.ViewSnapshot
	readPrivacyMessage(t, reader, &barrier)
	if barrier.Type != v1.TypeViewSnapshot || barrier.Revision <= refreshed.Revision {
		t.Fatalf("resync barrier = %+v", barrier)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("stale queued click started %d audits, want exactly 2 including startup", got)
	}
}

func TestRunPluginShowsFirstAuditProgressUntilItCompletes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		runAudit      auditRunner
		wantAvailable bool
		wantText      string
	}{
		{
			name: "success",
			runAudit: func(context.Context) ([]byte, error) {
				return currentAuditOutput(), nil
			},
			wantAvailable: true,
			wantText:      "Browser",
		},
		{
			name: "error",
			runAudit: func(context.Context) ([]byte, error) {
				return nil, errors.New("privacybit permission scan failed")
			},
			wantText: "privacybit permission scan failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			hostInput, reader, stop := startPrivacyPlugin(t, func(ctx context.Context) ([]byte, error) {
				close(started)
				select {
				case <-release:
					return tc.runAudit(ctx)
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			defer stop()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("the initial audit did not start")
			}

			sendPrivacyMessage(t, hostInput, map[string]any{
				"type": v1.TypeViewOpen, "view_id": "panel-1", "view": v1.ViewPanel,
				"entry": "panel", "instance": "privacy-placement", "output": "DP-1", "generation": 7,
			})
			var pending v1.ViewSnapshot
			readPrivacyMessage(t, reader, &pending)
			button := findPrivacyNode(pending.Root, "privacy-scan")
			if pending.Type != v1.TypeViewSnapshot || !privacyTreeHasText(pending.Root, "First audit in progress") {
				t.Fatalf("initial panel did not show pending audit progress: %+v", pending.Root)
			}
			if privacyTreeHasText(pending.Root, "Audit unavailable") {
				t.Fatal("initial panel showed an empty unavailable card while the audit was running")
			}
			if button == nil || !button.Disabled || button.Text != "Scanning…" {
				t.Fatalf("initial scan button = %+v, want disabled Scanning…", button)
			}

			close(release)
			var stateCall v1.HostCall
			readPrivacyMessage(t, reader, &stateCall)
			if stateCall.Call != v1.CallStateSet {
				t.Fatalf("completion call = %q, want state.set", stateCall.Call)
			}
			var state v1.StateSetParams
			if err := json.Unmarshal(stateCall.Params, &state); err != nil {
				t.Fatal(err)
			}
			var snapshot privacy.Snapshot
			if err := json.Unmarshal(state.Value, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Available != tc.wantAvailable {
				t.Fatalf("completion availability = %t, want %t (%+v)", snapshot.Available, tc.wantAvailable, snapshot)
			}
			replyPrivacyCall(t, hostInput, stateCall.ID)

			var refreshed v1.ViewSnapshot
			readPrivacyMessage(t, reader, &refreshed)
			button = findPrivacyNode(refreshed.Root, "privacy-scan")
			if !privacyTreeHasText(refreshed.Root, tc.wantText) {
				t.Fatalf("completed panel omitted %q: %+v", tc.wantText, refreshed.Root)
			}
			if button == nil || button.Disabled || button.Text != "Scan now" {
				t.Fatalf("completed scan button = %+v, want enabled Scan now", button)
			}
		})
	}
}

func TestRunPluginTimerRefreshShowsProgressInOpenPanel(t *testing.T) {
	initialRelease := make(chan struct{})
	refreshRelease := make(chan struct{})
	initialStarted := make(chan struct{})
	refreshStarted := make(chan struct{})
	var runs atomic.Int32
	hostInput, reader, stop := startPrivacyPluginWithInterval(t, func(ctx context.Context) ([]byte, error) {
		switch runs.Add(1) {
		case 1:
			close(initialStarted)
			select {
			case <-initialRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case 2:
			close(refreshStarted)
			select {
			case <-refreshRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return currentAuditOutput(), nil
	}, 20*time.Millisecond)
	defer stop()
	select {
	case <-initialStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the initial audit did not start")
	}

	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeViewOpen, "view_id": "panel-1", "view": v1.ViewPanel,
		"entry": "panel", "instance": "privacy-placement", "output": "DP-1", "generation": 7,
	})
	var pending v1.ViewSnapshot
	readPrivacyMessage(t, reader, &pending)
	close(initialRelease)
	ackStateCall(t, hostInput, reader)
	var current v1.ViewSnapshot
	readPrivacyMessage(t, reader, &current)
	button := findPrivacyNode(current.Root, "privacy-scan")
	if button == nil || button.Disabled || button.Text != "Scan now" {
		t.Fatalf("completed initial scan button = %+v, want enabled Scan now", button)
	}

	select {
	case <-refreshStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the timer did not start a refresh")
	}
	var busy v1.ViewSnapshot
	readPrivacyMessage(t, reader, &busy)
	button = findPrivacyNode(busy.Root, "privacy-scan")
	if button == nil || !button.Disabled || button.Text != "Scanning…" || !privacyTreeHasText(busy.Root, "Checking installed apps") {
		t.Fatalf("timer refresh view = %+v; button = %+v", busy.Root, button)
	}

	close(refreshRelease)
	ackStateCall(t, hostInput, reader)
	var refreshed v1.ViewSnapshot
	readPrivacyMessage(t, reader, &refreshed)
	button = findPrivacyNode(refreshed.Root, "privacy-scan")
	if button == nil || button.Disabled || button.Text != "Scan now" {
		t.Fatalf("completed timer refresh button = %+v, want enabled Scan now", button)
	}
}

// TestPrivacyExitsCleanlyWhenTheHostClosesThePipe proves a closed host pipe
// ends the plugin instead of failing it.
//
// The host closes a plugin's pipes when it stops the plugin, and a plugin
// reading a pipe that was closed under it sees io.ErrClosedPipe rather than
// io.EOF. A plugin that reports that as an error exits non-zero, and the
// shell reads a non-zero exit as a crash and restarts a plugin that was
// stopped on purpose. The plugin has to leave the way the other plugins in
// this repository do: quietly, on the end of the stream.
func TestPrivacyExitsCleanlyWhenTheHostClosesThePipe(t *testing.T) {
	pluginInput, hostInput := io.Pipe()
	hostOutput, pluginOutput := io.Pipe()
	t.Cleanup(func() {
		_ = hostInput.Close()
		_ = pluginOutput.Close()
	})
	finished := make(chan error, 1)
	go func() {
		finished <- runPlugin(context.Background(), pluginInput, pluginOutput, func(context.Context) ([]byte, error) {
			return currentAuditOutput(), nil
		})
	}()
	reader := bufio.NewReader(hostOutput)
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 18}},
		"capabilities": []string{"panels", "state"},
	})
	var hello map[string]any
	readPrivacyMessage(t, reader, &hello)
	if hello["type"] != "plugin.hello" {
		t.Fatalf("first plugin message = %#v", hello)
	}
	// The startup snapshot is published with a state.set call, and the
	// plugin waits for the answer before it serves anything.
	ackStateCall(t, hostInput, reader)
	// The host stops the plugin by closing the pipe the plugin reads from.
	_ = pluginInput.Close()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("plugin reported the closed host pipe as a failure: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("privacy plugin did not stop after the host closed the pipe")
	}
}

// A long audit must not hold the event loop.
//
// The audit is an external command that can take a minute. When it ran on
// the loop, the loop answered nothing while it ran: the incoming buffer
// filled and the host's calls timed out. A view the host asks for has to
// come back while the audit is still running.
func TestALongAuditDoesNotHoldTheEventLoop(t *testing.T) {
	pluginInput, hostInput := io.Pipe()
	hostOutput, pluginOutput := io.Pipe()
	defer hostInput.Close()
	defer hostOutput.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- runPlugin(ctx, pluginInput, pluginOutput, func(ctx context.Context) ([]byte, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return validAuditOutput(), nil
		})
	}()
	reader := bufio.NewReader(hostOutput)
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 18}},
		"capabilities": []string{"panels", "state"},
	})
	var hello map[string]any
	readPrivacyMessage(t, reader, &hello)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the audit never started")
	}

	// The host opens a view while the audit runs: the view must come back.
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": v1.TypeViewOpen, "view_id": "bar-1", "view": v1.ViewBar,
		"entry": "bar", "instance": "privacy-placement", "output": "DP-1", "generation": 7, "width": 240,
	})
	var view v1.ViewSnapshot
	readPrivacyMessage(t, reader, &view)
	if view.Type != v1.TypeViewSnapshot || view.ViewID != "bar-1" {
		t.Fatalf("view while the audit ran = %+v", view)
	}

	// The audit finishes and its snapshot reaches the host.
	close(release)
	var call v1.HostCall
	readPrivacyMessage(t, reader, &call)
	if call.Call != v1.CallStateSet {
		t.Fatalf("after the audit call = %q, want state.set", call.Call)
	}
	replyPrivacyCall(t, hostInput, call.ID)
	// The refreshed view follows the state.
	var refreshed v1.ViewSnapshot
	readPrivacyMessage(t, reader, &refreshed)
	if refreshed.Type != v1.TypeViewSnapshot || refreshed.ViewID != "bar-1" {
		t.Fatalf("refreshed view = %+v", refreshed)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("plugin error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the plugin did not stop")
	}
	_ = pluginOutput.Close()
}

func startPrivacyPlugin(t *testing.T, runAudit auditRunner) (*io.PipeWriter, *bufio.Reader, func()) {
	return startPrivacyPluginWithInterval(t, runAudit, refreshInterval)
}

func startPrivacyPluginWithInterval(t *testing.T, runAudit auditRunner, interval time.Duration) (*io.PipeWriter, *bufio.Reader, func()) {
	t.Helper()
	pluginInput, hostInput := io.Pipe()
	hostOutput, pluginOutput := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runPluginWithInterval(ctx, pluginInput, pluginOutput, runAudit, interval) }()
	reader := bufio.NewReader(hostOutput)
	sendPrivacyMessage(t, hostInput, map[string]any{
		"type": "host.hello", "supported": []v1.Version{{Major: 1, Minor: 18}},
		"capabilities": []string{"panels", "state"},
	})
	var hello map[string]any
	readPrivacyMessage(t, reader, &hello)
	if hello["type"] != "plugin.hello" {
		t.Fatalf("first plugin message = %#v", hello)
	}
	return hostInput, reader, func() {
		cancel()
		_ = hostInput.Close()
		_ = pluginOutput.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("privacy plugin did not stop")
		}
	}
}

func sendPrivacyMessage(t *testing.T, w *io.PipeWriter, message any) {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
}

func readPrivacyMessage(t *testing.T, reader *bufio.Reader, target any) {
	t.Helper()
	line := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		data, err := reader.ReadBytes('\n')
		line <- struct {
			data []byte
			err  error
		}{data: data, err: err}
	}()
	select {
	case result := <-line:
		if result.err != nil {
			t.Fatalf("read plugin message: %v", result.err)
		}
		if err := json.Unmarshal(result.data, target); err != nil {
			t.Fatalf("decode plugin message %s: %v", result.data, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for plugin message")
	}
}

func ackStateCall(t *testing.T, hostInput *io.PipeWriter, reader *bufio.Reader) {
	t.Helper()
	var call v1.HostCall
	readPrivacyMessage(t, reader, &call)
	if call.Call != v1.CallStateSet {
		t.Fatalf("startup call = %q, want state.set", call.Call)
	}
	replyPrivacyCall(t, hostInput, call.ID)
}

func replyPrivacyCall(t *testing.T, hostInput *io.PipeWriter, id string) {
	t.Helper()
	if id == "" {
		t.Fatal("plugin host call has no id")
	}
	sendPrivacyMessage(t, hostInput, v1.HostReply{Type: v1.TypeHostReply, ID: id, OK: true})
}

func currentAuditOutput() []byte {
	return []byte(strings.Replace(string(validAuditOutput()), "2026-10-20T12:00:00Z", time.Now().UTC().Format(time.RFC3339Nano), 1))
}

func findPrivacyNode(root *v1.Node, id string) *v1.Node {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findPrivacyNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func privacyTreeHasText(root *v1.Node, want string) bool {
	if root == nil {
		return false
	}
	if strings.Contains(root.Text, want) {
		return true
	}
	for _, child := range root.Children {
		if privacyTreeHasText(child, want) {
			return true
		}
	}
	return false
}
