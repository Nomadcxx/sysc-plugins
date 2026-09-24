// Command sysc-plugin-wallpaper-depth follows shell wallpaper assignments and
// registers generated depth masks for each output.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	wallpaperdepth "github.com/Nomadcxx/sysc-plugins/plugins/wallpaper-depth"
	"github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const (
	pollInterval    = time.Second
	hostCallTimeout = 750 * time.Millisecond
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	const pluginID = "org.sysc.wallpaper-depth"
	return runWith(in, out, wallpaperdepth.Helper{
		ScriptPath: wallpaperdepth.ScriptPath(),
		DataDir:    wallpaperdepth.DataDir(pluginID),
	})
}

func runWith(in io.Reader, out io.Writer, runner wallpaperdepth.Runner) error {
	rawClient := v1.NewClient(in, out)
	hello, err := rawClient.Handshake(identity.FromManifest(v1.Identity{
		ID: "org.sysc.wallpaper-depth", Name: "Wallpaper Depth", Version: "1.0.0",
	}))
	if err != nil {
		return err
	}
	if !hasCapability(hello.Capabilities, "wallpaper") {
		return errors.New("host did not grant wallpaper capability")
	}
	client := &serializedClient{client: rawClient}

	ctx, cancel := context.WithCancel(context.Background())
	controller := wallpaperdepth.NewController(ctx, runner, wallpaperRegistrar{client: client})
	const eventBuffer = 32
	events := make(chan commandEvent, eventBuffer)
	var workers sync.WaitGroup
	workers.Add(3)
	go receiveMessages(ctx, cancel, rawClient, events, &workers)
	go pollWallpapers(ctx, client, events, &workers)
	go watchController(ctx, controller, events, &workers)
	controller.Check()
	defer func() {
		cancel()
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
		controller.Close()
		workers.Wait()
	}()

	views := make(map[string]openView)
	settings := wallpaperdepth.Settings{AutoGenerate: true, Threshold: 30, Feather: 8}
	pollError := ""
	publish := func() error {
		snapshot := controller.Snapshot()
		if pollError != "" && snapshot.Error == "" {
			snapshot.Error = pollError
		}
		for id, current := range views {
			current.revision++
			views[id] = current
			var root *v1.Node
			switch current.kind {
			case v1.ViewBar:
				root = wallpaperdepth.BarTree(snapshot)
			case v1.ViewTooltip:
				root = wallpaperdepth.TooltipTree(snapshot)
			default:
				root = wallpaperdepth.PanelTree(snapshot)
			}
			if err := client.Snapshot(id, current.revision, root); err != nil {
				return err
			}
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-events:
			switch event.kind {
			case eventControllerChanged:
				if err := publish(); err != nil {
					return err
				}
			case eventWallpaperSnapshot:
				changed := false
				if event.err != nil {
					if next := event.err.Error(); pollError != next {
						pollError, changed = next, true
					}
				} else {
					if pollError != "" {
						pollError, changed = "", true
					}
					controller.Poll(domainOutputs(event.snapshot.Outputs))
				}
				if changed {
					if err := publish(); err != nil {
						return err
					}
				}
			case eventHostMessage:
				switch message := event.message.(type) {
				case *v1.HostShutdown:
					return nil
				case *v1.ViewOpen:
					instance := message.Instance
					if instance == "" {
						instance = message.ViewID
					}
					views[message.ViewID] = openView{
						kind: message.View, output: message.Output, instance: instance,
					}
					if err := publish(); err != nil {
						return err
					}
				case *v1.ViewClose:
					delete(views, message.ViewID)
				case *v1.ViewResync:
					if current, ok := views[message.ViewID]; ok {
						current.revision = 0
						views[message.ViewID] = current
					}
					if err := publish(); err != nil {
						return err
					}
				case *v1.InputEvent:
					if err := handleInput(ctx, client, controller, views, message); err != nil {
						if pollError != err.Error() {
							pollError = err.Error()
							if publishErr := publish(); publishErr != nil {
								return publishErr
							}
						}
					}
				case *v1.SettingsChanged:
					var changed bool
					settings, changed = applySettings(settings, message.Values)
					if changed {
						controller.SetSettings(settings)
					}
				}
			}
		}
	}
}

type commandEventKind uint8

const (
	eventHostMessage commandEventKind = iota
	eventWallpaperSnapshot
	eventControllerChanged
)

type commandEvent struct {
	kind     commandEventKind
	message  v1.Message
	snapshot v1.WallpaperSnapshotResult
	err      error
}

func receiveMessages(ctx context.Context, cancel context.CancelFunc, client *v1.Client, events chan<- commandEvent, workers *sync.WaitGroup) {
	defer workers.Done()
	for {
		message, err := client.Recv()
		if err != nil {
			cancel()
			return
		}
		select {
		case events <- commandEvent{kind: eventHostMessage, message: message}:
		case <-ctx.Done():
			return
		}
	}
}

func pollWallpapers(ctx context.Context, client *serializedClient, events chan<- commandEvent, workers *sync.WaitGroup) {
	defer workers.Done()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	poll := func() bool {
		snapshot, err := fetchWallpapers(ctx, client)
		select {
		case events <- commandEvent{kind: eventWallpaperSnapshot, snapshot: snapshot, err: err}:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !poll() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A single poller performs calls synchronously. The ticker's one-slot
			// channel coalesces any tick received while a call was active.
			if !poll() {
				return
			}
		}
	}
}

func watchController(ctx context.Context, controller *wallpaperdepth.Controller, events chan<- commandEvent, workers *sync.WaitGroup) {
	defer workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-controller.Changed():
			select {
			case events <- commandEvent{kind: eventControllerChanged}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func fetchWallpapers(ctx context.Context, client *serializedClient) (v1.WallpaperSnapshotResult, error) {
	reply, err := callHost(ctx, client, v1.CallWallpaperSnapshot, nil)
	if err != nil {
		return v1.WallpaperSnapshotResult{}, fmt.Errorf("wallpaper.snapshot: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(reply.Result, &fields); err != nil {
		return v1.WallpaperSnapshotResult{}, fmt.Errorf("wallpaper.snapshot result: %w", err)
	}
	for _, key := range []string{"revision", "scale", "outputs"} {
		if _, ok := fields[key]; !ok {
			return v1.WallpaperSnapshotResult{}, fmt.Errorf("wallpaper.snapshot result is missing %q", key)
		}
	}
	var snapshot v1.WallpaperSnapshotResult
	if err := decodeStrict(reply.Result, &snapshot); err != nil {
		return v1.WallpaperSnapshotResult{}, fmt.Errorf("wallpaper.snapshot result: %w", err)
	}
	return snapshot, nil
}

func setWallpaperMask(ctx context.Context, client *serializedClient, params v1.WallpaperMaskSetParams) error {
	if _, err := callHost(ctx, client, v1.CallWallpaperMaskSet, params); err != nil {
		return fmt.Errorf("wallpaper.mask.set: %w", err)
	}
	return nil
}

type wallpaperRegistrar struct{ client *serializedClient }

func (r wallpaperRegistrar) SetMask(ctx context.Context, output, wallpaperPath, maskPath string) error {
	return setWallpaperMask(ctx, r.client, v1.WallpaperMaskSetParams{
		Output: output, WallpaperPath: wallpaperPath, MaskPath: maskPath,
	})
}

func callHost(ctx context.Context, client *serializedClient, kind v1.CallKind, params any) (v1.HostReply, error) {
	callCtx, cancel := context.WithTimeout(ctx, hostCallTimeout)
	defer cancel()
	reply, err := client.Call(callCtx, kind, params)
	if err != nil {
		return reply, err
	}
	if !reply.OK {
		if reply.Error == "" {
			return reply, fmt.Errorf("host rejected %s", kind)
		}
		return reply, fmt.Errorf("host rejected %s: %s", kind, reply.Error)
	}
	return reply, nil
}

type openView struct {
	kind     v1.ViewKind
	revision uint64
	output   string
	instance string
}

// v1.Client does not serialize sends. One lock covers each call through its
// reply and each view snapshot, keeping concurrent frames intact on stdout.
// ponytail: allow one outstanding host call; upgrade to a send-only protocol
// lock if parallel host calls become necessary.
type serializedClient struct {
	client *v1.Client
	mu     sync.Mutex
}

func (c *serializedClient) Call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.Call(ctx, kind, params)
}

func (c *serializedClient) Snapshot(viewID string, revision uint64, root *v1.Node) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.Snapshot(viewID, revision, root)
}

func decodeStrict(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON results")
		}
		return err
	}
	return nil
}

func domainOutputs(outputs []v1.WallpaperOutput) []wallpaperdepth.Output {
	converted := make([]wallpaperdepth.Output, 0, len(outputs))
	for _, output := range outputs {
		converted = append(converted, wallpaperdepth.Output{
			Name: output.Output, State: string(output.State), WallpaperPath: output.Path,
		})
	}
	return converted
}

func applySettings(current wallpaperdepth.Settings, values map[string]any) (wallpaperdepth.Settings, bool) {
	next := current
	changed := false
	if value, ok := values["auto_generate"].(bool); ok && next.AutoGenerate != value {
		next.AutoGenerate, changed = value, true
	}
	if value, ok := settingInt(values["threshold"], 0, 100); ok && next.Threshold != value {
		next.Threshold, changed = value, true
	}
	if value, ok := settingInt(values["feather"], 0, 50); ok && next.Feather != value {
		next.Feather, changed = value, true
	}
	return next, changed
}

func settingInt(raw any, minValue, maxValue int) (int, bool) {
	switch value := raw.(type) {
	case float64:
		if value < float64(minValue) || value > float64(maxValue) || math.Trunc(value) != value {
			return 0, false
		}
		return int(value), true
	case int:
		if value < minValue || value > maxValue {
			return 0, false
		}
		return value, true
	default:
		return 0, false
	}
}

func handleInput(ctx context.Context, client *serializedClient, controller *wallpaperdepth.Controller, views map[string]openView, input *v1.InputEvent) error {
	if input.Event != v1.EventActivate {
		return nil
	}
	if input.Node == "open" {
		output, instance := input.Output, input.ViewID
		if current, ok := views[input.ViewID]; ok {
			if output == "" {
				output = current.output
			}
			if current.instance != "" {
				instance = current.instance
			}
		}
		_, err := callHost(ctx, client, v1.CallPanelOpen, v1.PanelParams{
			Entry: "panel", Output: output, Instance: instance,
		})
		return err
	}
	switch input.Node {
	case "check":
		controller.Check()
	case "setup":
		controller.Setup()
	case "generate-all":
		controller.GenerateAll()
	case "clear-cache":
		controller.ClearCache()
	default:
		if strings.HasPrefix(input.Node, "generate-") {
			controller.Generate(strings.TrimPrefix(input.Node, "generate-"))
		}
	}
	return nil
}

func hasCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}
