// Copyright (C) 2026 Nomadcxx
// SPDX-License-Identifier: GPL-3.0-or-later

// Command sysc-plugin-privacy publishes a bounded privacybit audit summary.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	privacy "github.com/Nomadcxx/sysc-plugins/plugins/privacy"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	refreshInterval = 5 * time.Minute
	auditTimeout    = 90 * time.Second
	callTimeout     = 5 * time.Second
	maxStderrBytes  = 8 << 10
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runPlugin(ctx, os.Stdin, os.Stdout, runPrivacybit); err != nil {
		log.Printf("privacy plugin stopped: %v", err)
		os.Exit(1)
	}
}

type auditRunner func(context.Context) ([]byte, error)

func runPlugin(ctx context.Context, in io.Reader, out io.Writer, runAudit auditRunner) error {
	client := v1.NewClient(in, out)
	if _, err := client.Handshake(v1.Identity{ID: "org.sysc.privacy", Name: "Privacy audit", Version: "0.1.0"}); err != nil {
		return err
	}
	pluginCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	receiveError := make(chan error, 1)
	go func() {
		for {
			if _, err := client.Recv(); err != nil {
				receiveError <- err
				cancel()
				return
			}
		}
	}()
	if runAudit == nil {
		runAudit = runPrivacybit
	}
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		if err := publishAudit(pluginCtx, client, runAudit); err != nil {
			if errors.Is(err, context.DeadlineExceeded) && pluginCtx.Err() == nil {
				log.Printf("privacybit state publish timed out: %v", err)
			} else if pluginCtx.Err() != nil {
				return nil
			} else {
				return err
			}
		}
		select {
		case <-pluginCtx.Done():
			return nil
		case err := <-receiveError:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-ticker.C:
		}
	}
}

func publishAudit(ctx context.Context, client *v1.Client, runAudit auditRunner) error {
	snapshot := refreshSnapshot(ctx, runAudit)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode privacy snapshot: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	reply, err := client.Call(callCtx, v1.CallStateSet, v1.StateSetParams{Key: "control_center", Value: raw})
	if err != nil {
		return fmt.Errorf("publish privacy snapshot: %w", err)
	}
	if !reply.OK {
		log.Printf("privacybit state publish failed: %s", cleanError(reply.Error))
	}
	return nil
}

func refreshSnapshot(ctx context.Context, runAudit auditRunner) privacy.Snapshot {
	started := time.Now().UTC()
	data, runErr := runAudit(ctx)
	snapshot, parseErr := privacy.ParseAuditOutput(data)
	if parseErr == nil {
		return snapshot
	}
	reason := parseErr.Error()
	if runErr != nil {
		reason = runErr.Error()
	}
	return privacy.UnavailableSnapshot(reason, started)
}

func runPrivacybit(ctx context.Context) ([]byte, error) {
	binary, err := privacybitExecutable()
	if err != nil {
		return nil, err
	}
	auditCtx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	command := exec.CommandContext(auditCtx, binary, "audit", "--snapshot")
	stdout := &limitedBuffer{limit: privacy.MaxAuditOutputBytes}
	stderr := &limitedBuffer{limit: maxStderrBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("privacybit audit --snapshot failed: %w: %s", err, cleanError(stderr.String()))
	}
	if stdout.exceeded {
		return stdout.Bytes(), errors.New("privacybit audit output exceeds 4 MiB")
	}
	return stdout.Bytes(), nil
}

func privacybitExecutable() (string, error) {
	path, pathErr := exec.LookPath("privacybit")
	if pathErr == nil {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("privacybit was not found on PATH or in ~/.local/bin: %w", pathErr)
	}
	localPath := filepath.Join(home, ".local", "bin", "privacybit")
	info, err := os.Stat(localPath)
	if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
		return localPath, nil
	}
	return "", fmt.Errorf("privacybit was not found on PATH or as an executable at %s: %w", localPath, pathErr)
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.exceeded = true
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

func cleanError(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}
