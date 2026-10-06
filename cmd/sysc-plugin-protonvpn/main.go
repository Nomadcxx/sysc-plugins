package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	identity "github.com/Nomadcxx/sysc-plugins/internal/identity"
	"github.com/Nomadcxx/sysc-plugins/plugins/protonvpn"
	v1 "github.com/Nomadcxx/sysc-shell/plugin/v1"
)

func main() {
	home, _ := os.UserHomeDir()
	env := environment{
		now:         time.Now,
		callTimeout: 5 * time.Second,
		cliBin:      "protonvpn",
		lookPath:    exec.LookPath,
		serversPath: filepath.Join(home, ".cache", "Proton", "VPN", "serverlist.json"),
	}
	if err := runPlugin(os.Stdin, os.Stdout, env); err != nil {
		os.Exit(1)
	}
}

type environment struct {
	now func() time.Time
	// callTimeout bounds every host call. Calls run on the loop, and a reply
	// queued behind a burst of input would otherwise never be read.
	callTimeout time.Duration
	cliBin      string
	lookPath    func(string) (string, error)
	serversPath string
}

type settings struct {
	refreshSeconds    int
	trafficMonitoring bool
	notifyOnConnect   bool
	barMode           string
	quickConnect      string
}

func defaultSettings() settings {
	return settings{refreshSeconds: 5, trafficMonitoring: true, notifyOnConnect: true, barMode: "code", quickConnect: "fastest"}
}

// apply takes the values the host sent; a missing or mistyped key keeps its
// current value, and refresh_seconds is clamped to the manifest's 2–60.
func (s *settings) apply(values map[string]any) {
	if v, ok := values["refresh_seconds"].(float64); ok {
		s.refreshSeconds = int(min(max(v, 2), 60))
	}
	if v, ok := values["traffic_monitoring"].(bool); ok {
		s.trafficMonitoring = v
	}
	if v, ok := values["notify_on_connect"].(bool); ok {
		s.notifyOnConnect = v
	}
	if v, ok := values["bar_mode"].(string); ok {
		s.barMode = v
	}
	if v, ok := values["quick_connect"].(string); ok {
		s.quickConnect = v
	}
}

func (s settings) values() map[string]string {
	return map[string]string{
		"refresh_seconds":    strconv.Itoa(s.refreshSeconds),
		"traffic_monitoring": strconv.FormatBool(s.trafficMonitoring),
		"notify_on_connect":  strconv.FormatBool(s.notifyOnConnect),
		"bar_mode":           s.barMode,
		"quick_connect":      s.quickConnect,
	}
}

type view struct {
	kind  v1.ViewKind
	rev   uint64
	width int
}

type session struct {
	env      environment
	client   *v1.Client
	settings settings
	views    map[string]view

	machine *protonvpn.Machine
	cli     *protonvpn.CLI
	// async carries closures from background work back onto the loop, the
	// only place allowed to touch the machine.
	async chan func()

	countries   []protonvpn.Country
	tab         string
	expanded    string
	page        int
	query       string
	userDraft   string
	queryReseed uint64
	userReseed  uint64
	hasCLI      bool
	hasCopyTool bool
	notice      string
	signInErr   string
	fileErr     string

	lastPhase   protonvpn.Phase
	lastStatus  time.Time
	lastTraffic time.Time
	lastLink    time.Time
	lastNATMP   time.Time
}

// terminals are spawned in this order for the sign-in hand-off; prefix is
// what each expects before the command line.
var terminals = []struct {
	bin    string
	prefix []string
}{
	{"xdg-terminal-exec", nil},
	{"x-terminal-emulator", nil},
	{"kitty", []string{"-e"}},
	{"alacritty", []string{"-e"}},
	{"foot", []string{"-e"}},
	{"gnome-terminal", []string{"--"}},
	{"konsole", []string{"-e"}},
	{"xterm", []string{"-e"}},
}

func runPlugin(in io.Reader, out io.Writer, env environment) error {
	c := v1.NewClient(in, out)
	if _, err := c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.protonvpn", Name: "ProtonVPN", Version: "1.0.1"})); err != nil {
		return err
	}
	s := &session{
		env: env, client: c, settings: defaultSettings(), views: map[string]view{},
		machine: &protonvpn.Machine{},
		cli:     &protonvpn.CLI{Bin: env.cliBin, Timeout: 10 * time.Second},
		async:   make(chan func(), 8),
		tab:     "connections",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := make(chan v1.Message, 8)
	go func() {
		for {
			msg, err := c.Recv()
			if err != nil {
				cancel()
				return
			}
			incoming <- msg
		}
	}()
	s.startup(ctx)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-incoming:
			switch m := msg.(type) {
			case *v1.HostShutdown:
				return nil
			case *v1.ViewOpen:
				s.views[m.ViewID] = view{kind: m.View, width: m.Width}
				s.snapshot(m.ViewID)
			case *v1.ViewClose:
				delete(s.views, m.ViewID)
			case *v1.ViewResync:
				if v, ok := s.views[m.ViewID]; ok {
					v.rev = 0
					s.views[m.ViewID] = v
					s.snapshot(m.ViewID)
				}
			case *v1.InputEvent:
				// Stale input — a view closed or repainted past the
				// revision it was sent for — must never reach the CLI.
				if v, ok := s.views[m.ViewID]; !ok || m.Revision != v.rev {
					continue
				}
				s.handle(ctx, m)
				// A press carries no state the tree renders; republishing
				// after it would make the matching release stale mid-gesture.
				if m.Event == v1.EventActivate {
					s.snapshotAll()
				}
			case *v1.SettingsChanged:
				s.settings.apply(m.Values)
				s.snapshotAll()
			}
		case f := <-s.async:
			f()
			s.notifyTransitions(ctx)
			s.snapshotAll()
		case now := <-ticker.C:
			s.tick(ctx, now)
		}
	}
}

// startup probes the environment, restores the tab, and takes the first
// readings. Without the CLI everything past the probe is skipped; the panel
// banner explains the rest.
func (s *session) startup(ctx context.Context) {
	if _, err := s.env.lookPath(s.env.cliBin); err == nil {
		s.hasCLI = true
		s.hasCopyTool = s.probeCopyTool()
	}
	s.restoreTab(ctx)
	s.loadServers()
	if s.hasCLI {
		s.pollStatus(ctx)
		s.pollInfo(ctx)
		s.pollConfig(ctx)
		s.lastPhase = s.machine.Snapshot().Phase
	}
}

func (s *session) probeCopyTool() bool {
	for _, tool := range []string{"wl-copy", "xclip"} {
		if _, err := s.env.lookPath(tool); err == nil {
			return true
		}
	}
	return false
}

func (s *session) restoreTab(ctx context.Context) {
	reply, err := s.call(ctx, v1.CallStateGet, v1.StateGetParams{Key: "ui"})
	if err != nil || !reply.OK {
		return
	}
	var res v1.StateGetResult
	if json.Unmarshal(reply.Result, &res) != nil || !res.Found {
		return
	}
	var ui struct {
		Tab string `json:"tab"`
	}
	if json.Unmarshal(res.Value, &ui) == nil && (ui.Tab == "connections" || ui.Tab == "protection" || ui.Tab == "account") {
		s.tab = ui.Tab
	}
}

func (s *session) persistTab(ctx context.Context) {
	value, err := json.Marshal(map[string]string{"tab": s.tab})
	if err != nil {
		return
	}
	_, _ = s.call(ctx, v1.CallStateSet, v1.StateSetParams{Key: "ui", Value: value})
}

func (s *session) loadServers() {
	if servers, err := protonvpn.LoadServers(s.env.serversPath); err == nil && len(servers) > 0 {
		names := map[string]string{}
		for _, c := range protonvpn.FallbackCountries() {
			names[c.Code] = c.Name
		}
		s.countries = protonvpn.Aggregate(servers, names, true)
		return
	}
	s.countries = protonvpn.FallbackCountries()
	s.notice = "Server list unavailable"
}

// tick runs every second and does the periodic work: status, the transition
// deadline, link liveness, traffic deltas and NAT-PMP refreshes.
// ponytail: polling loop with stamps; an event-driven tunnel watcher needs
// netlink and only pays off if the 1s cadence measurably hurts.
func (s *session) tick(ctx context.Context, now time.Time) {
	if !s.hasCLI {
		return
	}
	before := s.machine.Snapshot()
	transitioning := before.Phase == protonvpn.PhaseConnecting || before.Phase == protonvpn.PhaseDisconnecting

	interval := time.Duration(s.settings.refreshSeconds) * time.Second
	if transitioning {
		interval = time.Second
	}
	if now.Sub(s.lastStatus) >= interval {
		s.lastStatus = now
		s.pollStatus(ctx)
		if s.machine.TransitionExpired() {
			s.machine.Fail("Timeout")
		}
	}
	snap := s.machine.Snapshot()

	if snap.Phase == protonvpn.PhaseConnected {
		if s.settings.trafficMonitoring && now.Sub(s.lastTraffic) >= time.Second {
			s.lastTraffic = now
			s.sampleTraffic()
		}
		if now.Sub(s.lastLink) >= 2*time.Second {
			s.lastLink = now
			s.checkLink(ctx)
		}
		if snap.Config.PortForwarding && now.Sub(s.lastNATMP) >= 45*time.Second {
			s.lastNATMP = now
			s.requestPortMapping()
		}
	}
	snap = s.machine.Snapshot()
	if snap != before {
		s.notifyTransitions(ctx)
		s.snapshotAll()
	}
}

func (s *session) sampleTraffic() {
	read := func(metric string) (uint64, bool) {
		b, err := os.ReadFile(filepath.Join("/sys/class/net", "proton0", "statistics", metric))
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		return n, err == nil
	}
	rx, rxOK := read("rx_bytes")
	tx, txOK := read("tx_bytes")
	if !rxOK && !txOK {
		s.machine.SampleTraffic(0, 0, false)
		return
	}
	s.machine.SampleTraffic(rx, tx, true)
}

// checkLink drops the interface claim when NetworkManager no longer reports
// the tunnel, then forces an immediate status poll to reconcile.
func (s *session) checkLink(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "nmcli", "-t", "-f", "NAME,TYPE,DEVICE,STATE",
		"connection", "show", "--active").Output()
	if err != nil {
		return // no NetworkManager or it is busy: say nothing
	}
	if strings.Contains(string(out), "proton0") {
		return
	}
	if s.machine.Snapshot().Interface != "" {
		s.machine.SetInterface("")
		s.lastStatus = time.Time{}
	}
}

func (s *session) requestPortMapping() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		n := protonvpn.NATPMP{}
		port, err := n.RequestPort(ctx)
		s.async <- func() {
			if err == nil && port != 0 {
				s.machine.SetPort(port)
			}
		}
	}()
}

func (s *session) pollStatus(ctx context.Context) {
	st, err := s.cli.Status(ctx)
	if err != nil {
		return
	}
	s.machine.SetStatus(st)
}

func (s *session) pollInfo(ctx context.Context) {
	info, err := s.cli.Info(ctx)
	if err != nil {
		return
	}
	s.machine.SetInfo(info)
}

func (s *session) pollConfig(ctx context.Context) {
	cfg, err := s.cli.Config(ctx)
	if err != nil {
		return
	}
	s.machine.SetConfig(cfg)
}

// notifyTransitions fires the desktop notifications the settings ask for
// whenever the phase settles into connected or drops out of it.
func (s *session) notifyTransitions(ctx context.Context) {
	snap := s.machine.Snapshot()
	if snap.Phase == s.lastPhase {
		return
	}
	switch snap.Phase {
	case protonvpn.PhaseConnected:
		if s.settings.notifyOnConnect {
			s.notify(ctx, "Connected to "+snap.Status.Server)
		}
	case protonvpn.PhaseDisconnected:
		if s.lastPhase == protonvpn.PhaseConnected {
			s.notify(ctx, "Disconnected")
		}
	}
	s.lastPhase = snap.Phase
}

func (s *session) notify(ctx context.Context, summary string) {
	_, _ = s.call(ctx, v1.CallNotify, v1.NotifyParams{Summary: summary})
}

// handle dispatches one input event by node id. Snapshots are the loop's
// job after this returns; here we only mutate state and start work.
func (s *session) handle(ctx context.Context, m *v1.InputEvent) {
	node := m.Node
	switch {
	case node == "bar":
		// A primary click arrives twice: EventPointer on press, EventActivate
		// on release. Opening on both would toggle the panel straight closed.
		switch {
		case m.Event == v1.EventPointer && m.Button == v1.ButtonSecondary:
			s.quickAction(ctx)
		case m.Event == v1.EventActivate:
			_, _ = s.call(ctx, v1.CallPanelOpen, v1.PanelParams{
				Entry: "panel", Output: m.Output, Generation: m.Generation, Instance: m.ViewID,
			})
		}
	case node == "action":
		s.actionButton(ctx)
	case strings.HasPrefix(node, "qc:"):
		s.connect(ctx, quickTarget(strings.TrimPrefix(node, "qc:")))
	case node == "search" && (m.Event == v1.EventChange || m.Event == v1.EventSubmit):
		s.query, s.page = m.Text, 0
	case node == "clear-search":
		s.query, s.queryReseed, s.page = "", s.queryReseed+1, 0
	case node == "page:prev":
		if s.page > 0 {
			s.page--
		}
	case node == "page:next":
		if s.page+1 < protonvpn.CountryPages(s.connsState()) {
			s.page++
		}
	case strings.HasPrefix(node, "expand:"):
		cc := strings.TrimPrefix(node, "expand:")
		if s.expanded == cc {
			s.expanded = ""
		} else {
			s.expanded = cc
		}
	case strings.HasPrefix(node, "connect:"):
		s.connect(ctx, strings.TrimPrefix(node, "connect:"))
	case strings.HasPrefix(node, "server-connect:"):
		s.connect(ctx, strings.TrimPrefix(node, "server-connect:"))
	case node == "ks":
		s.setConfig(ctx, "kill-switch", map[bool]string{true: "off", false: "standard"}[s.machine.Snapshot().Config.KillSwitch != "off"])
	case strings.HasPrefix(node, "ns:"):
		if level := strings.TrimPrefix(node, "ns:"); protonvpn.ValidNetShield(level) {
			s.setConfig(ctx, "netshield", level)
		}
	case node == "pf":
		s.setConfig(ctx, "port-forwarding", map[bool]string{true: "off", false: "on"}[s.machine.Snapshot().Config.PortForwarding])
	case node == "copy-port":
		if port := s.machine.Snapshot().Port; port != 0 {
			_, _ = s.call(ctx, v1.CallClipboardWrite, v1.ClipboardWriteParams{Text: strconv.Itoa(port)})
		}
	case node == "signin-user" && m.Event == v1.EventChange:
		s.userDraft = m.Text
	case node == "signin":
		s.spawnSignIn()
	case node == "signout":
		s.signOut(ctx)
	case node == "refresh":
		s.pollStatus(ctx)
		s.pollInfo(ctx)
		s.pollConfig(ctx)
		s.notifyTransitions(ctx)
	case strings.HasPrefix(node, "tab:"):
		s.tab = strings.TrimPrefix(node, "tab:")
		s.expanded, s.query, s.page = "", "", 0
		s.persistTab(ctx)
	}
}

func quickTarget(name string) string {
	if name == "fastest" {
		return ""
	}
	return name
}

func (s *session) actionButton(ctx context.Context) {
	switch s.machine.Snapshot().Phase {
	case protonvpn.PhaseDisconnected, protonvpn.PhaseError:
		s.connect(ctx, "")
	case protonvpn.PhaseConnecting:
		s.disconnect(ctx)
	case protonvpn.PhaseConnected:
		s.disconnect(ctx)
	}
}

func (s *session) quickAction(ctx context.Context) {
	switch s.machine.Snapshot().Phase {
	case protonvpn.PhaseDisconnected, protonvpn.PhaseError:
		s.connect(ctx, quickTarget(s.settings.quickConnect))
	case protonvpn.PhaseConnected:
		s.disconnect(ctx)
	}
}

// connect marks the transition, then runs the blocking CLI call off-loop;
// the result closure lands back on the loop, the only machine writer.
func (s *session) connect(_ context.Context, target string) {
	if !s.hasCLI {
		return
	}
	s.machine.StartConnect()
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		out, errOut, err := s.cli.Connect(runCtx, target)
		s.async <- func() {
			if err != nil {
				s.machine.Fail(protonvpn.ErrorDetail(errOut))
				return
			}
			if ip := protonvpn.ParseConnectIP(out); ip != "" {
				s.machine.SetIP(ip)
			}
			s.pollStatus(context.Background())
		}
	}()
}

func (s *session) disconnect(ctx context.Context) {
	if !s.hasCLI {
		return
	}
	s.machine.StartDisconnect()
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, errOut, err := s.cli.Disconnect(runCtx)
		s.async <- func() {
			if err != nil {
				s.machine.Fail(protonvpn.ErrorDetail(errOut))
				return
			}
			s.pollStatus(context.Background())
		}
	}()
}

func (s *session) setConfig(ctx context.Context, key, value string) {
	if _, _, err := s.cli.Run(ctx, "config", "set", key, value); err != nil {
		s.fileErr = err.Error()
		return
	}
	s.pollConfig(ctx)
}

func (s *session) spawnSignIn() {
	for _, t := range terminals {
		bin, err := s.env.lookPath(t.bin)
		if err != nil {
			continue
		}
		args := append(append([]string{}, t.prefix...), s.env.cliBin, "signin", s.userDraft)
		if err := exec.Command(bin, args...).Start(); err != nil {
			continue
		}
		s.signInErr = ""
		return
	}
	s.signInErr = fmt.Sprintf("No terminal found; run: %s signin %s", s.env.cliBin, s.userDraft)
}

func (s *session) signOut(ctx context.Context) {
	if _, _, err := s.cli.Run(ctx, "signout"); err != nil {
		s.signInErr = err.Error()
		return
	}
	s.machine.SetInfo(protonvpn.Info{})
	s.machine.StartDisconnect()
	go func() {
		s.async <- func() {
			s.pollStatus(context.Background())
			s.pollInfo(context.Background())
		}
	}()
}

func (s *session) tree(v view) *v1.Node {
	switch v.kind {
	case v1.ViewBar:
		return barTreeAtWidth(s, v.width)
	case v1.ViewTooltip:
		return tooltipTree(s)
	}
	return panelTree(s)
}

func (s *session) snapshot(id string) {
	v := s.views[id]
	v.rev++
	s.views[id] = v
	_ = s.client.Snapshot(id, v.rev, s.tree(v))
}

func (s *session) snapshotAll() {
	for id := range s.views {
		s.snapshot(id)
	}
}

func (s *session) call(ctx context.Context, kind v1.CallKind, params any) (v1.HostReply, error) {
	if s.env.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.env.callTimeout)
		defer cancel()
	}
	return s.client.Call(ctx, kind, params)
}

func barTreeAtWidth(s *session, width int) *v1.Node {
	snap := s.machine.Snapshot()
	return protonvpn.BarAtWidth(protonvpn.BarState{Snap: snap, Mode: s.settings.barMode, Quick: s.settings.quickConnect}, width)
}

func tooltipTree(s *session) *v1.Node {
	snap := s.machine.Snapshot()
	return protonvpn.Tooltip(protonvpn.BarState{Snap: snap, Mode: s.settings.barMode, Quick: s.settings.quickConnect, Traffic: s.settings.trafficMonitoring})
}

func panelTree(s *session) *v1.Node {
	return protonvpn.Panel(s.panelState())
}

// connsState is the Connections tab's render state, also used to clamp the
// page before stepping it.
func (s *session) connsState() protonvpn.ConnectionsState {
	return protonvpn.ConnectionsState{
		Query: s.query, QueryReseed: s.queryReseed,
		Countries: s.countries, Expanded: s.expanded, Page: s.page, Notice: s.notice,
	}
}

func (s *session) panelState() protonvpn.PanelState {
	snap := s.machine.Snapshot()
	return protonvpn.PanelState{
		Snap:    snap,
		Tab:     s.tab,
		HasCLI:  s.hasCLI,
		Traffic: s.settings.trafficMonitoring,
		Conns:   s.connsState(),
		Prot: protonvpn.ProtectionState{
			Port: snap.Port, HasCopyTool: s.hasCopyTool, Err: s.fileErr,
		},
		Acct: protonvpn.AccountState{
			SignedIn: snap.Info.Username != "", UserDraft: s.userDraft,
			Settings: s.settings.values(), UserReseed: s.userReseed, Err: s.signInErr,
		},
	}
}
