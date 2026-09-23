// Command sysc-plugin-mini-docker manages Docker containers from the shell
// bar, ported from the Noctalia community plugin of the same name.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	minidocker "github.com/Nomadcxx/sysc-plugins/plugins/mini-docker"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

// fallbackVersion backs the handshake when the manifest is unreadable (go
// run, tests); TestHandshakeFallbackMatchesManifest pins it to the manifest.
const fallbackVersion = "0.4.0"

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

// newSession is the seam tests replace to keep the real docker CLI out of
// the harness.
var newSession = func() *minidocker.Session { return minidocker.NewSession(minidocker.CLI{}) }

type view struct {
	kind     v1.ViewKind
	rev      uint64
	instance string
}

type refreshRequest struct {
	containers bool
	active     bool
	staleOnly  bool
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.mini-docker", Name: "Mini Docker", Version: fallbackVersion})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session := newSession()
	views := map[string]view{}
	lastTree := map[string][sha256.Size]byte{}
	settings := struct {
		interval   time.Duration
		statusMode string
	}{interval: 5 * time.Second, statusMode: "always"}

	// mu guards views, rendered-tree digests, and settings. sendMu serializes
	// every encoder write, including host.call, across the main loop and poller.
	var mu sync.Mutex
	var sendMu sync.Mutex

	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			select {
			case incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	interval := func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return settings.interval
	}

	publish := func(forceViewID string) {
		mu.Lock()
		defer mu.Unlock()
		state := session.State()
		mode := settings.statusMode
		running := 0
		for _, container := range state.Containers {
			if container.Running() {
				running++
			}
		}
		for id, current := range views {
			root := viewTree(current.kind, state, mode, running)
			encoded, err := json.Marshal(root)
			if err != nil {
				continue
			}
			digest := sha256.Sum256(encoded)
			if previous, exists := lastTree[id]; id != forceViewID && exists && previous == digest {
				continue
			}
			revision := current.rev + 1
			sendMu.Lock()
			err = c.Snapshot(id, revision, root)
			sendMu.Unlock()
			if err == nil {
				current.rev = revision
				views[id] = current
				lastTree[id] = digest
			}
		}
	}

	// ponytail: one queued request coalesces bursts; widen only if users need
	// distinct refreshes to survive a Docker operation already in progress.
	refresh := make(chan refreshRequest, 1)
	queueRefresh := func(request refreshRequest) {
		select {
		case refresh <- request:
			return
		default:
		}
		select {
		case pending := <-refresh:
			forceActive := pending.active && !pending.staleOnly || request.active && !request.staleOnly
			request = refreshRequest{
				containers: pending.containers || request.containers,
				active:     pending.active || request.active,
				staleOnly:  (pending.active || request.active) && !forceActive,
			}
		default:
		}
		select {
		case refresh <- request:
		default:
		}
	}

	refreshNow := func(request refreshRequest) {
		scope := session.State().Scope
		if request.containers || request.active && scope == minidocker.ScopeContainers {
			session.RefreshWithStart(ctx, func() { publish("") })
		}
		scope = session.State().Scope
		if request.active && scope != minidocker.ScopeContainers {
			if !request.staleOnly || tabNeedsRefresh(session.State(), scope, interval(), time.Now()) {
				session.RefreshTabWithStart(ctx, scope, func() { publish("") })
			}
		}
		publish("")
	}

	intervalChanged := make(chan struct{}, 1)
	go func() {
		refreshNow(refreshRequest{containers: true, active: true})
		timer := time.NewTimer(interval())
		defer timer.Stop()
		resetTimer := func(delay time.Duration) {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-intervalChanged:
				resetTimer(interval())
			case request := <-refresh:
				refreshNow(request)
				resetTimer(interval())
			case <-timer.C:
				refreshNow(refreshRequest{containers: true, active: true})
				timer.Reset(interval())
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch msg := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				mu.Lock()
				views[msg.ViewID] = view{kind: msg.View, instance: msg.Instance}
				delete(lastTree, msg.ViewID)
				mu.Unlock()
				publish(msg.ViewID)
				if msg.View == v1.ViewPanel {
					queueRefresh(refreshRequest{active: true})
				}
			case *v1.ViewClose:
				mu.Lock()
				delete(views, msg.ViewID)
				delete(lastTree, msg.ViewID)
				mu.Unlock()
			case *v1.ViewResync:
				mu.Lock()
				if current, ok := views[msg.ViewID]; ok {
					current.rev = 0
					views[msg.ViewID] = current
					delete(lastTree, msg.ViewID)
				}
				mu.Unlock()
				publish(msg.ViewID)
			case *v1.InputEvent:
				mu.Lock()
				current, exists := views[msg.ViewID]
				validRevision := exists && current.rev == msg.Revision
				mu.Unlock()
				if !validRevision {
					continue
				}
				if current.kind == v1.ViewPanel && (msg.Event == v1.EventChange || msg.Event == v1.EventSubmit) {
					switch msg.Node {
					case "name", "port", "env":
						session.UpdateRunField(msg.Node, msg.Text)
						publish("")
					}
					continue
				}
				if msg.Event != v1.EventActivate {
					continue
				}
				switch {
				case msg.Node == "open" && current.kind == v1.ViewBar:
					sendMu.Lock()
					_, _ = c.Call(ctx, v1.CallPanelOpen, v1.PanelParams{
						Entry: "panel", Output: msg.Output, Generation: msg.Generation, Instance: current.instance,
					})
					sendMu.Unlock()
				case msg.Node == "refresh" && current.kind == v1.ViewPanel:
					queueRefresh(refreshRequest{containers: true, active: true})
				case msg.Node == "form:publish" && current.kind == v1.ViewPanel:
					session.ToggleRunPublish()
					publish("")
				case msg.Node == "form:network" && current.kind == v1.ViewPanel:
					session.CycleRunNetwork()
					publish("")
				case msg.Node == "form:cancel" && current.kind == v1.ViewPanel:
					session.CancelRunForm()
					publish("")
				case msg.Node == "run-submit" && current.kind == v1.ViewPanel:
					go func() {
						session.SubmitRunWithStart(ctx, func() { publish("") })
						publish("")
					}()
				case msg.Node == "confirm" && current.kind == v1.ViewPanel:
					go func() {
						session.ConfirmRemovalWithStart(ctx, func() { publish("") })
						publish("")
					}()
				case msg.Node == "cancel" && current.kind == v1.ViewPanel:
					session.CancelRemoval()
					publish("")
				case current.kind == v1.ViewPanel && strings.HasPrefix(msg.Node, "tab:"):
					if scope, ok := scopeFromNode(msg.Node); ok && session.SetScope(scope) {
						publish("")
						if tabNeedsRefresh(session.State(), scope, interval(), time.Now()) {
							queueRefresh(refreshRequest{active: true, staleOnly: true})
						}
					}
				case current.kind == v1.ViewPanel && strings.HasPrefix(msg.Node, "select:"):
					if id := strings.TrimPrefix(msg.Node, "select:"); id != "" {
						session.Select(id)
						publish("")
					}
				case current.kind == v1.ViewPanel && strings.HasPrefix(msg.Node, "run:"):
					id := strings.TrimPrefix(msg.Node, "run:")
					go func(id string) {
						session.OpenRunForm(ctx, id)
						publish("")
					}(id)
				default:
					if current.kind != v1.ViewPanel {
						continue
					}
					if action, id, ok := minidocker.ParseAction(msg.Node); ok {
						switch action {
						case "start", "stop", "restart":
							go func(action, id string) {
								session.ActWithStart(ctx, action, id, func() { publish("") })
								publish("")
							}(action, id)
						case "remove", "rmi", "volrm", "netrm":
							session.ArmRemoval(id)
							publish("")
						}
					}
				}
			case *v1.SettingsChanged:
				changed := false
				intervalWasChanged := false
				defaultNetwork := ""
				defaultNetworkChanged := false
				mu.Lock()
				if raw, ok := msg.Values["refresh_interval_seconds"]; ok {
					if value, ok := raw.(float64); ok && value >= 1 && value <= 30 && value == math.Trunc(value) {
						next := time.Duration(value) * time.Second
						intervalWasChanged = next != settings.interval
						settings.interval = next
					}
				}
				if raw, ok := msg.Values["status_mode"]; ok {
					if mode, ok := raw.(string); ok && (mode == "always" || mode == "running_only" || mode == "hidden") && mode != settings.statusMode {
						settings.statusMode = mode
						changed = true
					}
				}
				if raw, ok := msg.Values["default_network"]; ok {
					if network, ok := raw.(string); ok && len(network) <= v1.MaxTextBytes {
						defaultNetwork = network
						defaultNetworkChanged = true
					}
				}
				mu.Unlock()
				if defaultNetworkChanged {
					session.SetDefaultNetwork(defaultNetwork)
				}
				if intervalWasChanged {
					select {
					case intervalChanged <- struct{}{}:
					default:
					}
				}
				if changed {
					publish("")
				}
			}
		}
	}
}

func viewTree(kind v1.ViewKind, state minidocker.SessionSnapshot, statusMode string, running int) *v1.Node {
	switch kind {
	case v1.ViewBar:
		return minidocker.BarTree(minidocker.BarLabel(statusMode, running, state.ContainerTab.Available), !state.ContainerTab.Available)
	case v1.ViewTooltip:
		return minidocker.TooltipTreeForSession(state, running)
	default:
		return minidocker.PanelTreeForSession(state)
	}
}

func scopeFromNode(node string) (minidocker.Scope, bool) {
	switch node {
	case "tab:containers":
		return minidocker.ScopeContainers, true
	case "tab:images":
		return minidocker.ScopeImages, true
	case "tab:volumes":
		return minidocker.ScopeVolumes, true
	case "tab:networks":
		return minidocker.ScopeNetworks, true
	default:
		return "", false
	}
}

func tabNeedsRefresh(state minidocker.SessionSnapshot, scope minidocker.Scope, interval time.Duration, now time.Time) bool {
	var status minidocker.TabStatus
	switch scope {
	case minidocker.ScopeContainers:
		status = state.ContainerTab
	case minidocker.ScopeImages:
		status = state.ImageTab
	case minidocker.ScopeVolumes:
		status = state.VolumeTab
	case minidocker.ScopeNetworks:
		status = state.NetworkTab
	default:
		return false
	}
	return !status.Available || status.RefreshedAt.IsZero() || now.Sub(status.RefreshedAt) >= interval
}
