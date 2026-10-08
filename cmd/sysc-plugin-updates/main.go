// Command sysc-plugin-updates reports pending repo, AUR and Flatpak updates
// and starts updates in a terminal the user can watch.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Nomadcxx/sysc-plugins/internal/hostcall"
	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/internal/terminal"
	updates "github.com/Nomadcxx/sysc-plugins/plugins/updates"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

const fallbackVersion = "0.1.0"

// Seams tests replace.
var (
	newRunner   = updates.ExecRunner
	lookPath    = exec.LookPath
	rebootProbe = osReboot
)

type settings struct {
	interval       time.Duration
	intervalHours  int
	aurHelper      string
	includeFlatpak bool
	notify         bool
	hideWhenZero   bool
	terminal       string
	updateCommand  string
	updateMethod   string
}

func defaultSettings() settings {
	return settings{
		interval:       3 * time.Hour,
		intervalHours:  3,
		aurHelper:      "auto",
		includeFlatpak: true,
		hideWhenZero:   true,
		updateMethod:   "terminal",
	}
}

type view struct {
	kind     v1.ViewKind
	rev      uint64
	instance string
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}

func run(in *os.File, out *os.File) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.updates", Name: "System Updates", Version: fallbackVersion})); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var sendMu sync.Mutex

	cfg := defaultSettings()
	svc := updates.NewService(updates.Config{AURHelper: cfg.aurHelper, IncludeFlatpak: cfg.includeFlatpak}, newRunner, lookPath, time.Now, rebootProbe)

	views := map[string]view{}
	lastTree := map[string][sha256.Size]byte{}
	runHint := ""

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

	publish := func(forceViewID string) {
		mu.Lock()
		defer mu.Unlock()
		state := svc.State()
		now := time.Now()
		for viewID, current := range views {
			var root *v1.Node
			switch current.kind {
			case v1.ViewBar:
				root = updates.BarTree(state, cfg.hideWhenZero)
			case v1.ViewTooltip:
				root = updates.TooltipTree(state, now)
			case v1.ViewPanel:
				root = updates.PanelTree(state, now, runHint)
			default:
				continue
			}
			data, err := json.Marshal(root)
			if err != nil {
				continue
			}
			digest := sha256.Sum256(data)
			if forceViewID != viewID && lastTree[viewID] == digest {
				continue
			}
			revision := current.rev + 1
			sendMu.Lock()
			err = c.Snapshot(viewID, revision, root)
			sendMu.Unlock()
			if err != nil {
				continue
			}
			current.rev = revision
			views[viewID] = current
			lastTree[viewID] = digest
		}
	}

	setRunHint := func(hint string) {
		mu.Lock()
		runHint = hint
		mu.Unlock()
	}

	persist := func() {
		data := svc.PersistedState()
		if data == nil {
			return
		}
		sendMu.Lock()
		_, _ = hostcall.Call(ctx, c, v1.CallStateSet, v1.StateSetParams{Key: "last", Value: data})
		sendMu.Unlock()
	}

	notifyIfDue := func() {
		mu.Lock()
		enabled := cfg.notify
		mu.Unlock()
		if !enabled {
			return
		}
		body, ok := svc.TakeNotification(time.Now())
		if !ok {
			return
		}
		sendMu.Lock()
		_, _ = hostcall.Call(ctx, c, v1.CallNotify, v1.NotifyParams{Summary: "System Updates", Body: body})
		sendMu.Unlock()
	}

	checkNow := func() {
		go func() {
			svc.Check(ctx)
			persist()
			publish("")
			notifyIfDue()
		}()
	}

	runUpdate := func() {
		mu.Lock()
		cfgSnap := cfg
		mu.Unlock()

		if cfgSnap.updateMethod == "pkexec" {
			if _, err := lookPath("pkexec"); err != nil {
				setRunHint("pkexec not found. Use the terminal method, or run a polkit agent.")
				publish("")
				return
			}
			setRunHint("")
			cmd := exec.Command("pkexec", "pacman", "-Syu", "--noconfirm")
			if err := cmd.Start(); err != nil {
				setRunHint("Couldn't start pkexec: " + err.Error())
				publish("")
				return
			}
			go func() {
				err := cmd.Wait()
				body := "Update finished."
				if err != nil {
					body = "Update failed: " + err.Error()
				}
				sendMu.Lock()
				_, _ = hostcall.Call(ctx, c, v1.CallNotify, v1.NotifyParams{Summary: "System Updates", Body: body})
				sendMu.Unlock()
				time.Sleep(5 * time.Second)
				checkNow()
			}()
			return
		}

		command := updateCommand(cfgSnap)
		bin, prefix, err := terminal.Resolve(cfgSnap.terminal, lookPath)
		if err != nil {
			setRunHint("No terminal found. Set one in the plugin's settings, or run: " + command)
			publish("")
			return
		}
		setRunHint("")
		script := command + `; status=$?; echo; read -r -p "Done (exit $status). Press Enter to close. " _`
		args := append(append([]string{}, prefix...), "sh", "-c", script)
		cmd := exec.Command(bin, args...)
		if err := cmd.Start(); err != nil {
			setRunHint("Couldn't start " + bin + ": " + err.Error())
			publish("")
			return
		}
		publish("")
		go waitForTerminal(cmd, checkNow)
	}

	sendMu.Lock()
	reply, err := hostcall.Call(ctx, c, v1.CallStateGet, v1.StateGetParams{Key: "last"})
	sendMu.Unlock()
	if err == nil && reply.OK {
		var result v1.StateGetResult
		if json.Unmarshal(reply.Result, &result) == nil && result.Found {
			svc.RestoreState(result.Value)
		}
	}

	go svc.Loop(ctx, func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return cfg.interval
	}, func() {
		persist()
		publish("")
		notifyIfDue()
	})

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch event := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				mu.Lock()
				views[event.ViewID] = view{kind: event.View, instance: event.Instance}
				delete(lastTree, event.ViewID)
				mu.Unlock()
				publish(event.ViewID)
			case *v1.ViewClose:
				mu.Lock()
				delete(views, event.ViewID)
				delete(lastTree, event.ViewID)
				mu.Unlock()
			case *v1.ViewResync:
				mu.Lock()
				current, exists := views[event.ViewID]
				if exists {
					current.rev = 0
					views[event.ViewID] = current
					delete(lastTree, event.ViewID)
				}
				mu.Unlock()
				publish(event.ViewID)
			case *v1.InputEvent:
				mu.Lock()
				current, exists := views[event.ViewID]
				mu.Unlock()
				if !exists || current.rev != event.Revision || event.Event != v1.EventActivate {
					continue
				}
				switch event.Node {
				case "open":
					if current.kind != v1.ViewBar {
						continue
					}
					sendMu.Lock()
					_, _ = hostcall.Call(ctx, c, v1.CallPanelOpen, v1.PanelParams{Entry: "panel", Output: event.Output, Generation: event.Generation, Instance: current.instance})
					sendMu.Unlock()
				case "updates-refresh":
					checkNow()
				case "updates-news":
					sendMu.Lock()
					_, _ = hostcall.Call(ctx, c, v1.CallOpenURL, v1.OpenURLParams{URL: "https://archlinux.org/news/"})
					sendMu.Unlock()
				case "updates-run":
					runUpdate()
				}
			case *v1.SettingsChanged:
				mu.Lock()
				changed, recheck := false, false
				for key, value := range event.Values {
					switch key {
					case "interval_hours":
						if hours, ok := value.(float64); ok && hours >= 1 && hours <= 24 && hours == math.Trunc(hours) {
							cfg.intervalHours = int(hours)
							cfg.interval = time.Duration(hours) * time.Hour
							changed = true
						}
					case "aur_helper":
						if helper, ok := value.(string); ok && (helper == "auto" || helper == "paru" || helper == "yay" || helper == "off") {
							cfg.aurHelper = helper
							changed, recheck = true, true
						}
					case "include_flatpak":
						if enabled, ok := value.(bool); ok {
							cfg.includeFlatpak = enabled
							changed, recheck = true, true
						}
					case "notify":
						if enabled, ok := value.(bool); ok {
							cfg.notify = enabled
							changed = true
						}
					case "hide_when_zero":
						if hidden, ok := value.(bool); ok {
							cfg.hideWhenZero = hidden
							changed = true
						}
					case "terminal":
						if name, ok := value.(string); ok && len(name) <= v1.MaxTextBytes {
							cfg.terminal = name
							changed = true
						}
					case "update_command":
						if command, ok := value.(string); ok && len(command) <= v1.MaxTextBytes {
							cfg.updateCommand = command
							changed = true
						}
					case "update_method":
						if method, ok := value.(string); ok && (method == "terminal" || method == "pkexec") {
							cfg.updateMethod = method
							changed = true
						}
					}
				}
				cfgSnap := cfg
				mu.Unlock()
				if !changed {
					continue
				}
				svc.SetConfig(updates.Config{AURHelper: cfgSnap.aurHelper, IncludeFlatpak: cfgSnap.includeFlatpak})
				publish("")
				if recheck {
					checkNow()
				}
			}
		}
	}
}

// updateCommand picks the command the update run should execute: the user's
// override, else the AUR helper's -Syu, else pacman through sudo.
func updateCommand(cfg settings) string {
	if cfg.updateCommand != "" {
		return cfg.updateCommand
	}
	if helper := updates.ResolveAURHelper(cfg.aurHelper, lookPath); helper != "" {
		return helper + " -Syu"
	}
	return "sudo pacman -Syu"
}

// waitForTerminal re-checks the system after the terminal exits or after
// pacman.log moves on, whichever comes first, with a settle delay so the
// database lock is gone.
func waitForTerminal(cmd *exec.Cmd, onDone func()) {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	startMtime := pacmanLogMtime()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			time.Sleep(5 * time.Second)
			onDone()
			return
		case <-ticker.C:
			if mtime := pacmanLogMtime(); !mtime.IsZero() && mtime != startMtime {
				time.Sleep(5 * time.Second)
				onDone()
				return
			}
		}
	}
}

func pacmanLogMtime() time.Time {
	info, err := os.Stat("/var/log/pacman.log")
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// osReboot reads the running kernel and boot time from the real system.
func osReboot() (bool, string) {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false, ""
	}
	uname := strings.TrimSpace(string(data))
	var bootTime time.Time
	if stat, err := os.Open("/proc/stat"); err == nil {
		scanner := bufio.NewScanner(stat)
		for scanner.Scan() {
			if value, ok := strings.CutPrefix(scanner.Text(), "btime "); ok {
				if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
					bootTime = time.Unix(seconds, 0)
				}
				break
			}
		}
		_ = stat.Close()
	}
	return updates.RebootNeeded(os.DirFS("/"), uname, bootTime)
}
