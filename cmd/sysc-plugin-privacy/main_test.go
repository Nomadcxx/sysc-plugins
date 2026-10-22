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
	"strings"
	"testing"

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

func TestRefreshSnapshotPublishesUnavailableForMissingOrInvalidOutput(t *testing.T) {
	for name, run := range map[string]auditRunner{
		"missing binary":   func(context.Context) ([]byte, error) { return nil, errors.New("not found") },
		"malformed output": func(context.Context) ([]byte, error) { return []byte("not json"), nil },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := refreshSnapshot(context.Background(), run)
			if snapshot.Available || snapshot.Reason == "" || snapshot.Overall.Grade != "unknown" || !snapshot.Network.TelemetryConnectionsUnknown {
				t.Fatalf("invalid audit did not publish explicit unavailable state: %+v", snapshot)
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

func TestLimitedBufferCapsMemory(t *testing.T) {
	buffer := &limitedBuffer{limit: 8}
	if n, err := buffer.Write([]byte(strings.Repeat("x", 1024))); n != 1024 || err != nil {
		t.Fatalf("limited write = %d, %v", n, err)
	}
	if buffer.Len() != 8 || !buffer.exceeded {
		t.Fatalf("buffer size=%d exceeded=%v, want 8 and true", buffer.Len(), buffer.exceeded)
	}
}
