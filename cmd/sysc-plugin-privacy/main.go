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

type openedView struct {
	kind       v1.ViewKind
	width      int
	instance   string
	output     string
	generation uint32
	revision   uint64
}

func runPlugin(ctx context.Context, in io.Reader, out io.Writer, runAudit auditRunner) error {
	client := v1.NewClient(in, out)
	if _, err := client.Handshake(v1.Identity{ID: "org.sysc.privacy", Name: "Privacy audit", Version: "0.1.0"}); err != nil {
		return err
	}
	pluginCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan v1.Message, 16)
	receiveError := make(chan error, 1)
	go func() {
		for {
			message, err := client.Recv()
			if err != nil {
				select {
				case receiveError <- err:
				default:
				}
				cancel()
				return
			}
			select {
			case incoming <- message:
			case <-pluginCtx.Done():
				return
			}
		}
	}()
	if runAudit == nil {
		runAudit = runPrivacybit
	}
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	snapshot := refreshSnapshot(pluginCtx, runAudit)
	if pluginCtx.Err() != nil {
		return nil
	}
	if err := publishSnapshot(pluginCtx, client, snapshot); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && pluginCtx.Err() == nil {
			log.Printf("privacybit state publish timed out: %v", err)
		} else if pluginCtx.Err() != nil {
			return nil
		} else {
			return err
		}
	}
	views := make(map[string]openedView)
	scanning := false
	scanMessage := ""
	for {
		select {
		case <-pluginCtx.Done():
			select {
			case err := <-receiveError:
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					return err
				}
			default:
			}
			return nil
		case err := <-receiveError:
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case message := <-incoming:
			switch message := message.(type) {
			case *v1.ViewOpen:
				view := openedView{kind: message.View, width: message.Width, instance: message.Instance,
					output: message.Output, generation: message.Generation}
				views[message.ViewID] = view
				if err := publishView(pluginCtx, client, views, message.ViewID, snapshot, scanning, scanMessage); err != nil {
					return err
				}
			case *v1.ViewClose:
				delete(views, message.ViewID)
			case *v1.ViewResync:
				if _, ok := views[message.ViewID]; ok {
					if err := publishView(pluginCtx, client, views, message.ViewID, snapshot, scanning, scanMessage); err != nil {
						return err
					}
				}
			case *v1.InputEvent:
				view, ok := views[message.ViewID]
				if !ok || message.Revision != view.revision || message.Event != v1.EventActivate {
					continue
				}
				if view.kind == v1.ViewBar && message.Node == "privacy-open" {
					callCtx, callCancel := context.WithTimeout(pluginCtx, callTimeout)
					reply, err := client.Call(callCtx, v1.CallPanelOpen, v1.PanelParams{
						Entry: "panel", Instance: view.instance, Output: view.output, Generation: view.generation,
					})
					callCancel()
					if err != nil {
						if pluginCtx.Err() != nil {
							return nil
						}
						log.Printf("open privacy panel failed: %s", cleanError(err.Error()))
					} else if !reply.OK {
						log.Printf("open privacy panel failed: %s", cleanError(reply.Error))
					}
					continue
				}
				if view.kind != v1.ViewPanel || message.Node != "privacy-scan" || scanning {
					continue
				}
				scanning, scanMessage = true, "Checking installed apps"
				if err := publishViews(pluginCtx, client, views, snapshot, scanning, scanMessage); err != nil {
					return err
				}
				snapshot = refreshSnapshot(pluginCtx, runAudit)
				if pluginCtx.Err() != nil {
					return nil
				}
				scanning, scanMessage = false, ""
				if err := publishSnapshot(pluginCtx, client, snapshot); err != nil {
					if errors.Is(err, context.DeadlineExceeded) && pluginCtx.Err() == nil {
						log.Printf("privacybit state publish timed out: %v", err)
					} else if pluginCtx.Err() != nil {
						return nil
					} else {
						return err
					}
				}
				if err := publishViews(pluginCtx, client, views, snapshot, scanning, scanMessage); err != nil {
					return err
				}
			}
		case <-ticker.C:
			snapshot = refreshSnapshot(pluginCtx, runAudit)
			if pluginCtx.Err() != nil {
				return nil
			}
			if err := publishSnapshot(pluginCtx, client, snapshot); err != nil {
				if errors.Is(err, context.DeadlineExceeded) && pluginCtx.Err() == nil {
					log.Printf("privacybit state publish timed out: %v", err)
				} else if pluginCtx.Err() != nil {
					return nil
				} else {
					return err
				}
			}
			if err := publishViews(pluginCtx, client, views, snapshot, scanning, scanMessage); err != nil {
				return err
			}
		}
	}
}

func publishSnapshot(ctx context.Context, client *v1.Client, snapshot privacy.Snapshot) error {
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

func publishViews(ctx context.Context, client *v1.Client, views map[string]openedView, snapshot privacy.Snapshot, scanning bool, scanMessage string) error {
	for id := range views {
		if err := publishView(ctx, client, views, id, snapshot, scanning, scanMessage); err != nil {
			return err
		}
	}
	return nil
}

func publishView(ctx context.Context, client *v1.Client, views map[string]openedView, id string, snapshot privacy.Snapshot, scanning bool, scanMessage string) error {
	view, ok := views[id]
	if !ok {
		return nil
	}
	now := time.Now()
	var root *v1.Node
	switch view.kind {
	case v1.ViewBar:
		root = privacy.BarTree(snapshot, now, view.width)
	case v1.ViewTooltip:
		root = privacy.TooltipTree(snapshot, now)
	case v1.ViewPanel:
		root = privacy.PanelTree(snapshot, now, scanning, scanMessage)
	default:
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	view.revision++
	if err := client.Snapshot(id, view.revision, root); err != nil {
		return fmt.Errorf("publish privacy view %q: %w", id, err)
	}
	views[id] = view
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
