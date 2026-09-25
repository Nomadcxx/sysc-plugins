# ProtonVPN Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the ProtonVPN plugin (`org.sysc.protonvpn`) so it matches or exceeds Noctalia's `noctaproton-vpn`, DMS's `DMS-Proton_VPN`, and the official GTK app's interaction design: bar + tooltip, a 460×580 three-tab panel, quick connect, country/server browsing, kill switch, NetShield, port forwarding with NAT-PMP, split tunneling, and account management — all on the official `protonvpn` CLI.

**Architecture:** One plugin process (it is the service). The package splits by responsibility: `cli.go` (the only file that spawns the CLI), `state.go` (phase machine + polling cadence), `servers.go` (serverlist aggregation), `apps.go` (`.desktop` scan), `natpmp.go` (RFC 6886), `bar.go` / `panel.go` / `connections.go` / `protection.go` / `account.go` (view trees), and `cmd/.../main.go` (event loop). sysc-shell gains fourteen material glyphs; the wire protocol is unchanged.

**Tech Stack:** Go 1.26, `github.com/Nomadcxx/sysc-shell/plugin/v1` (JSONL wire), `plugin/lint` (host layout pipeline), `protonvpn` CLI (external), `nmcli` (optional, link watch), Noto Color Emoji (optional, flag spike).

**Spec:** `docs/plans/2026-09-26-protonvpn-design.md`

## Global Constraints

- Manifest: schema 1, id `org.sysc.protonvpn`, version `1.0.0`, protocol `{"major": 1, "minor": 8}`, capabilities `["panels", "settings", "state", "notifications"]`, `requires.commands: ["protonvpn"]`, panel 460×580 `attached`, `include_settings` false.
- Bar lint box 240×32; tooltip 280×200; panel box 460×580.
- Settings: `refresh_seconds` int 5 (2–60); `traffic_monitoring` bool true; `notify_on_connect` bool true; `bar_mode` select `icon|code|status` default `code`; `quick_connect` select `fastest|random|p2p|tor` default `fastest`.
- State key `ui`, value `{"tab":"connections"}`. Never write state except in response to a user mutation; undecodable value → defaults in memory, no write.
- Every icon name must exist in the shell catalogue at the pin produced by Task 1; an unknown name gets the whole view refused.
- No new Go module dependencies.
- Commit messages carry no AI attribution (a repo hook rejects it).
- Copy is verbatim from the spec: status words `Unprotected`, `Connecting…`, `Protected`, `Disconnecting…`, `Connection error`; placeholder `Search country or server`; `Active port: {N}`; `{name} is under maintenance`; `Fastest country`; `Auto-selected on connect`; `protonvpn CLI not found`; `Server list unavailable`; `Remember to restart affected apps`.
- CLI parsers are governed by fixtures in `plugins/protonvpn/testdata/`, captured from a real CLI where possible (Task 5 step 1); never from assumption alone.
- Test command throughout (from `~/sysc-plugins`): `go test ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/`.

## Review Focus

1. **CLI output drift** between protonvpn-cli versions: parsers must fail loud (error, not zero-value) on unparseable status output, and the panel must show the raw first stderr line on command failure. Test in Task 5 (`TestParseStatusGarbageIsAnError`).
2. **Long names** (a 30-char server name, a country with a long name) in country/server rows and the bar: lint must pass, text clips, buttons never refuse the row. Test in Task 10 (`TestConnectionsLintLongNames`).
3. **Transition deadline**: a connect that never completes becomes `Connection error` after 20s, using an injectable clock. Test in Task 6 (`TestConnectDeadline`).
4. **Malformed `settings.json`**: split-tunnel writes must preserve unknown keys and never clobber the file on a read failure. Test in Task 11 (`TestSplitTunnelPreservesUnknownKeys`).
5. **NAT-PMP on a network without the Proton gateway**: failures surface as `—` in the port row, retried next cycle, never a crash or a blocked loop. Test in Task 14 (`TestNATPMPFailureIsNonFatal`).

---

## File map

| File | Status | Responsibility |
|---|---|---|
| `~/sysc-shell/internal/render/icons/material/build.py` | modify | add 14 names to `ICONS` |
| `~/sysc-shell/internal/render/icons/material/material-symbols-rounded.ttf` | regenerate | subset font |
| `~/sysc-shell/internal/render/icons/material/SOURCE.md` | modify | inventory, size, hash |
| `~/sysc-shell/internal/render/materialfont.go` | modify | accept the 14 names |
| `~/sysc-shell/internal/render/materialfont_test.go` | modify | inventory list |
| `go.mod`, `go.sum` | modify | shell pin bump; repairs go.sum |
| `plugins/protonvpn/manifest.json` | create | schema, capabilities, settings |
| `plugins/protonvpn/cli.go` (+`_test`) | create | CLI spawn + parsers |
| `plugins/protonvpn/state.go` (+`_test`) | create | phase machine, traffic sampler |
| `plugins/protonvpn/servers.go` (+`_test`) | create | serverlist parse + aggregate |
| `plugins/protonvpn/apps.go` (+`_test`) | create | `.desktop` scan |
| `plugins/protonvpn/natpmp.go` (+`_test`) | create | RFC 6886 client |
| `plugins/protonvpn/bar.go` (+`_test`) | create | bar pill + tooltip |
| `plugins/protonvpn/panel.go` (+`_test`) | create | panel skeleton + connection card |
| `plugins/protonvpn/connections.go` (+`_test`) | create | connections tab |
| `plugins/protonvpn/protection.go` (+`_test`) | create | protection tab |
| `plugins/protonvpn/account.go` (+`_test`) | create | account tab |
| `plugins/protonvpn/testdata/*` | create | CLI fixtures, serverlist fixture, desktop fixtures |
| `cmd/sysc-plugin-protonvpn/main.go` (+`main_test.go`) | create | event loop |
| `README.md`, `docs/plans/README.md` | modify | register the plugin and plan |

---

### Task 1: Shell glyphs (sysc-shell repo)

**Files:**
- Modify: `~/sysc-shell/internal/render/icons/material/build.py` (end of `ICONS`)
- Regenerate: `~/sysc-shell/internal/render/icons/material/material-symbols-rounded.ttf`
- Modify: `~/sysc-shell/internal/render/icons/material/SOURCE.md` (Result line, Inventory block, hash)
- Modify: `~/sysc-shell/internal/render/materialfont.go` (`materialIcons` map)
- Test: `~/sysc-shell/internal/render/materialfont_test.go` (`materialInventory`)

**Interfaces:**
- Produces: `render.ValidMaterialIcon(name) == true` for `shield`, `verified_user`, `vpn_key`, `vpn_key_off`, `bolt`, `dns`, `location_on`, `security`, `flag`, `download`, `upload`, `swap_vert`, `language`, `gpp_bad` at the commit Task 2 pins.

This task is the commission from the brainstorm, made executable. `public` and `edit` already exist (world clock). sysc-shell may carry unrelated uncommitted work — stage only the five files above.

- [ ] **Step 1: Write the failing test**

In `materialfont_test.go`, append to `materialInventory` after the `"public": …, "edit": …` entries (keep alphabetical position loose; the list is a set):

```go
	"shield", "verified_user", "vpn_key", "vpn_key_off", "bolt", "dns",
	"location_on", "security", "flag", "download", "upload", "swap_vert",
	"language", "gpp_bad",
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ~/sysc-shell && go test ./internal/render/ -run Material -v`
Expected: FAIL naming the fourteen names (not accepted / not covered by the font).

- [ ] **Step 3: Add the names to the builder and the map**

`build.py`, at the end of `ICONS`:

```python
    # The ProtonVPN plugin: state glyphs (shield/verified_user/vpn_key/
    # vpn_key_off/bolt/gpp_bad), detail rows (dns/location_on/security),
    # country and feature tags (flag/public), traffic (download/upload),
    # and sort/expand affordances (swap_vert/language).
    "shield",
    "verified_user",
    "vpn_key",
    "vpn_key_off",
    "bolt",
    "dns",
    "location_on",
    "security",
    "flag",
    "download",
    "upload",
    "swap_vert",
    "language",
    "gpp_bad",
```

`materialfont.go`, after the `"public": {}, "edit": {},` line:

```go
	// The ProtonVPN plugin's state, detail, and traffic glyphs.
	"shield": {}, "verified_user": {}, "vpn_key": {}, "vpn_key_off": {},
	"bolt": {}, "dns": {}, "location_on": {}, "security": {}, "flag": {},
	"download": {}, "upload": {}, "swap_vert": {}, "language": {}, "gpp_bad": {},
```

- [ ] **Step 4: Rebuild the subset**

Follow `SOURCE.md` for the pinned upstream URL and instanced axes, then:

```bash
cd ~/sysc-shell && python3 internal/render/icons/material/build.py <pinned-upstream.ttf>
sha256sum internal/render/icons/material/material-symbols-rounded.ttf
stat -c %s internal/render/icons/material/material-symbols-rounded.ttf
```

- [ ] **Step 5: Verify every name shapes to a visible glyph**

```bash
cd ~/sysc-shell && go test ./internal/render/ -run Material -v
```
Expected: PASS (the inventory test asserts both map and font coverage; a name in one but not the other fails).

- [ ] **Step 6: Update SOURCE.md and commit**

Record the new inventory count, byte size, and SHA-256 in `SOURCE.md`.

```bash
cd ~/sysc-shell && git add internal/render/icons/material/build.py \
  internal/render/icons/material/material-symbols-rounded.ttf \
  internal/render/icons/material/SOURCE.md \
  internal/render/materialfont.go internal/render/materialfont_test.go
git commit -m "feat(icons): add VPN glyph set for the protonvpn plugin"
```

---

### Task 2: Pin the shell, repair go.sum (sysc-plugins)

**Files:**
- Modify: `go.mod`, `go.sum`

**Context:** HEAD's go.sum has zero sysc-shell lines (dropped in `228113c`) and the pin (20260925112547) predates the shell commit that added `public`/`edit`, so world-clock tests fail at HEAD. This task fixes both and equips the new glyphs.

- [ ] **Step 1: Pin and tidy**

```bash
cd ~/sysc-plugins
SHELL_COMMIT=$(git -C ~/sysc-shell rev-parse HEAD)
go get github.com/Nomadcxx/sysc-shell@v0.0.0-$(git -C ~/sysc-shell log -1 --format=%cd --date=format:'%Y%m%d%H%M%S')
go mod tidy
```

- [ ] **Step 2: Verify the whole repo is green**

```bash
cd ~/sysc-plugins && go build ./... && go test ./... && make validate
```
Expected: world-clock tests PASS (they need `public`/`edit`); all other packages PASS; manifest validation PASSes.

- [ ] **Step 3: Commit**

```bash
cd ~/sysc-plugins && git add go.mod go.sum
git commit -m "build: pin shell with VPN glyphs, repair go.sum"
```

---

### Task 3: Flag emoji spike (acceptance gate)

**Files:**
- Create (temporary): `~/sysc-shell/internal/render/emoji_spike_test.go`
- Modify: `docs/plans/2026-09-26-protonvpn-design.md` (record the outcome)

**Context:** The design ships country rows with a flag glyph when the render pipeline paints CBDT emoji, else a code-badge capsule. `internal/render` is an internal package, so the spike runs inside the sysc-shell repo and is deleted after.

- [ ] **Step 1: Check an emoji font exists**

```bash
fc-list | grep -i emoji
```
Expected: at least one CBDT font (Noto Color Emoji). If none: install `noto-color-emoji` via the system package manager, or record `badge` and skip to Step 4.

- [ ] **Step 2: Write the spike test**

```go
package render

import (
	"image"
	"testing"
)

func TestSpikeRegionalIndicatorFlagPaints(t *testing.T) {
	r := NewTextRenderer() // match the constructor used by render tests
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	n := r.Paint(img, "\U0001F1FA\U0001F1F8", TextSpec{Size: 16}) // US flag
	if n == 0 {
		t.Fatal("flag painted nothing")
	}
	// Non-transparent pixels prove a colour blit, not two notdef boxes.
	found := false
	for x := 0; x < 64 && !found; x++ {
		for y := 0; y < 32; y++ {
			if img.At(x, y).(color.RGBA).A > 0 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("flag produced no pixels")
	}
}
```

Match the exact renderer constructor, `TextSpec` fields, and paint entry point to the neighbouring render tests (`fontmap_test.go`, `text_test.go`) — the assertion that matters is "more than zero painted pixels for the two-rune flag".

- [ ] **Step 3: Run the spike**

```bash
cd ~/sysc-shell && go test ./internal/render/ -run Spike -v
```
Expected: PASS (flags paint) or FAIL (badges ship). Either outcome is a result, not a bug.

- [ ] **Step 4: Record and clean up**

Delete `emoji_spike_test.go`. In the design doc's "Flag emoji spike" section, replace the last sentence with the outcome, e.g. `Outcome (2026-09-26): flags paint with Noto Color Emoji — country rows lead with a flag glyph.` or `Outcome (2026-09-26): no CBDT emoji font — code-badge capsule ships.`

```bash
cd ~/sysc-plugins && git add docs/plans/2026-09-26-protonvpn-design.md
git commit -m "docs(protonvpn): record flag emoji spike outcome"
```

---

### Task 4: Manifest and plugin skeleton

**Files:**
- Create: `plugins/protonvpn/manifest.json`
- Create: `cmd/sysc-plugin-protonvpn/main.go`
- Modify: `README.md` (plugin table row), `docs/plans/README.md` (plan row)

**Interfaces:**
- Produces: a plugin that handshakes, serves an empty panel/bar/tooltip, passes `make validate`, and installs.

- [ ] **Step 1: Write the manifest**

`plugins/protonvpn/manifest.json`:

```json
{
  "schema": 1,
  "id": "org.sysc.protonvpn",
  "name": "ProtonVPN",
  "version": "1.0.0",
  "protocol": { "major": 1, "minor": 8 },
  "exec": "sysc-plugin-protonvpn",
  "capabilities": ["panels", "settings", "state", "notifications"],
  "requires": { "commands": ["protonvpn"] },
  "services": [ { "id": "service" } ],
  "widgets": [ { "id": "bar", "settings": [] } ],
  "panels": [
    {
      "id": "panel",
      "width": 460,
      "height": 580,
      "placement": "attached",
      "include_settings": false
    }
  ],
  "settings": [
    { "key": "refresh_seconds", "type": "int", "default": 5, "min": 2, "max": 60 },
    { "key": "traffic_monitoring", "type": "bool", "default": true },
    { "key": "notify_on_connect", "type": "bool", "default": true },
    { "key": "bar_mode", "type": "select", "default": "code",
      "options": ["icon", "code", "status"] },
    { "key": "quick_connect", "type": "select", "default": "fastest",
      "options": ["fastest", "random", "p2p", "tor"] }
  ]
}
```

Match field spellings to `plugins/world-clock/manifest.json` and `plugins/github-notifications/manifest.json` (the `exec` key, settings option shape, and requires shape follow those files exactly); `make validate` is the arbiter.

- [ ] **Step 2: Write the skeleton loop**

`cmd/sysc-plugin-protonvpn/main.go` — copy the structure of `cmd/sysc-plugin-world-clock/main.go`: `environment` struct (now func, callTimeout), `settings` struct with `apply(map[string]any)` (missing/mistyped keeps current, ints clamped to 2–60), `session` struct (env, client, store, settings, `views map[string]view{kind,rev}`, snapshot/patch/snapshotAll helpers, `call(ctx, kind, params)` with timeout), `runPlugin` doing `v1.NewClient` → `c.Handshake(identity.FromManifest(v1.Identity{ID: "org.sysc.protonvpn", Name: "ProtonVPN", Version: "1.0.0"}))` → `c.Recv()` goroutine → select loop over ctx.Done / incoming (HostShutdown returns, ViewOpen stores + snapshots, ViewClose deletes, ViewResync re-snapshots, InputEvent dispatches to a stub `handle`, SettingsChanged applies + re-snapshots). Views render placeholder trees for now:

```go
func barTree(s *session) *v1.Node {
	return &v1.Node{Kind: v1.KindRow, Children: []*v1.Node{
		{Kind: v1.KindIcon, Icon: "vpn_key_off", Tone: v1.ToneSubtle},
	}}
}

func panelTree(s *session) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Padding: 12, Gap: 8, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "ProtonVPN", Size: "title", Bold: true},
	}}
}

func tooltipTree(s *session) *v1.Node {
	return &v1.Node{Kind: v1.KindColumn, Gap: 4, Children: []*v1.Node{
		{Kind: v1.KindText, Text: "Unprotected", Tone: v1.ToneSubtle},
	}}
}
```

- [ ] **Step 3: Validate and build**

```bash
cd ~/sysc-plugins && make validate && go build ./... && go vet ./plugins/protonvpn/ ./cmd/sysc-plugin-protonvpn/
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
cd ~/sysc-plugins && git add plugins/protonvpn cmd/sysc-plugin-protonvpn README.md docs/plans/README.md
git commit -m "feat(protonvpn): manifest and plugin skeleton"
```

---

### Task 5: CLI parsers (`cli.go`)

**Files:**
- Create: `plugins/protonvpn/cli.go`, `plugins/protonvpn/cli_test.go`
- Create: `plugins/protonvpn/testdata/status-connected.txt`, `status-disconnected.txt`, `info.txt`, `config-list.txt`, `connect-error.txt`

**Interfaces:**

```go
type Phase int
const (
	PhaseDisconnected Phase = iota
	PhaseConnecting
	PhaseConnected
	PhaseDisconnecting
	PhaseError
)

type Status struct {
	Phase    Phase
	Server   string
	Country  string
	City     string
	IP       string
	Protocol string
}

type Info struct {
	Username string
	Plan     string
	Version  string
	Interface string
}

type Config struct {
	KillSwitch     string // "standard" | "off"
	NetShield      string // "off" | "malware-only" | "malware-ads-trackers"
	PortForwarding bool
}

type CLI struct {
	Bin     string
	Timeout time.Duration
	Now     func() time.Time
}

func (c *CLI) Run(ctx context.Context, args ...string) (stdout, stderr string, err error)
func (c *CLI) Status(ctx context.Context) (Status, error)
func (c *CLI) Info(ctx context.Context) (Info, error)
func (c *CLI) Config(ctx context.Context) (Config, error)
func (c *CLI) Connect(ctx context.Context, target string) (string, error) // target "" = fastest; returns stderr for error detail
func (c *CLI) Disconnect(ctx context.Context) (string, error)
func ParseStatus(stdout string) (Status, error)
func ParseInfo(stdout string) (Info, error)
func ParseConfig(stdout string) (Config, error)
func ErrorDetail(stderr string) string // first non-empty line, trimmed
```

- [ ] **Step 1: Capture real CLI output if available**

```bash
command -v protonvpn && { protonvpn status; protonvpn info; protonvpn config list; } | tee /tmp/proton-capture.txt
```
If the CLI is installed, paste the real output into the fixtures (Step 2) instead of the shapes below, and note the CLI version in the fixture header comment. If not installed, use the fixture shapes as-is and flag the fixture files with a leading comment `# captured-from: noctalia service.luau parsing; verify against a live CLI`.

- [ ] **Step 2: Write fixtures**

`testdata/status-connected.txt`:

```
Status:       Connected
Server:       US-NY#1
Country:      United States
City:         New York
IP:           198.51.100.7
Protocol:     WireGuard
```

`testdata/status-disconnected.txt`:

```
Status:       Disconnected
```

`testdata/info.txt`:

```
User:         jane@example.com
Plan:         Proton VPN Plus
CLI Version:  3.13.0
Interface:    proton0
```

`testdata/config-list.txt`:

```
Kill Switch:  standard
Netshield:    malware-only
Port Forwarding: on
```

`testdata/connect-error.txt` (used as a stderr fixture):

```
[!] Authentication denied. Please check your credentials.
```

- [ ] **Step 3: Write the failing tests**

`cli_test.go` — table-driven:

```go
func TestParseStatus(t *testing.T) {
	connected, _ := os.ReadFile("testdata/status-connected.txt")
	s, err := ParseStatus(string(connected))
	if err != nil { t.Fatal(err) }
	if s.Phase != PhaseConnected || s.Server != "US-NY#1" || s.Country != "United States" ||
		s.City != "New York" || s.IP != "198.51.100.7" || s.Protocol != "WireGuard" {
		t.Fatalf("got %+v", s)
	}
	disc, _ := os.ReadFile("testdata/status-disconnected.txt")
	s, err = ParseStatus(string(disc))
	if err != nil || s.Phase != PhaseDisconnected { t.Fatalf("got %+v err %v", s, err) }
}

func TestParseStatusGarbageIsAnError(t *testing.T) {
	if _, err := ParseStatus("hello world\nno keys here"); err == nil {
		t.Fatal("expected error on unparseable status")
	}
}

func TestParseInfoAndConfig(t *testing.T) { /* same shape: read fixtures, assert fields */ }

func TestErrorDetail(t *testing.T) {
	b, _ := os.ReadFile("testdata/connect-error.txt")
	if got := ErrorDetail(string(b)); got != "[!] Authentication denied. Please check your credentials." {
		t.Fatalf("got %q", got)
	}
	if got := ErrorDetail(""); got != "" { t.Fatalf("got %q", got) }
}
```

Parser rules: split lines, split on the first `:`, trim spaces, match keys case-insensitively (`Status`, `Server`, `Country`, `City`, `IP`, `Protocol`; `User`, `Plan`, `CLI Version`, `Interface`; `Kill Switch`, `Netshield`, `Port Forwarding`). `Status` maps `Connected`/`Connecting`/`Disconnected`/`Disconnecting` to phases (case-insensitive); any other value or a missing `Status` key is an error. `Port Forwarding` maps `on`/`off` case-insensitively.

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./plugins/protonvpn/ -run 'TestParse|TestErrorDetail' -v`
Expected: FAIL (functions undefined).

- [ ] **Step 5: Implement `cli.go`**

Parsers as above. `Run` uses `exec.CommandContext` with a per-command timeout (`c.Timeout`, default 10s; `Connect` uses 15s), captures `CombinedOutput`-style separate stdout/stderr buffers, and returns both with the error. `Connect(ctx, target)` builds args: `connect` plus `--random`/`--p2p`/`--tor` for those targets, `--country CC` for 2-letter codes, else the raw server name; empty target = bare `connect`. `Status` runs `status`, `Info` runs `info`, `Config` runs `config list`.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./plugins/protonvpn/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd ~/sysc-plugins && git add plugins/protonvpn
git commit -m "feat(protonvpn): CLI parsers and fixtures"
```

---

### Task 6: State machine (`state.go`)

**Files:**
- Create: `plugins/protonvpn/state.go`, `plugins/protonvpn/state_test.go`

**Interfaces:**

```go
type Snapshot struct {
	Phase   Phase
	Err     string // error detail line; cleared on the next state change
	Status  Status
	Info    Info
	Config  Config
	RxRate, TxRate float64 // bytes/s
	RxTotal, TxTotal float64
	Port    int    // NAT-PMP forwarded port; 0 = none
}

type Machine struct {
	Now func() time.Time // injectable; tests drive it
	// fields: snap Snapshot, transitionStarted time.Time, lastRx, lastTx uint64, lastSample time.Time
}

func (m *Machine) Snapshot() Snapshot
func (m *Machine) SetStatus(s Status)      // from the status poll; applies the 20s deadline
func (m *Machine) SetInfo(i Info)
func (m *Machine) SetConfig(c Config)
func (m *Machine) SetPort(p int)
func (m *Machine) StartConnect()           // PhaseConnecting, clears Err, stamps the deadline
func (m *Machine) StartDisconnect()        // PhaseDisconnecting, stamps the deadline
func (m *Machine) Fail(detail string)      // PhaseError + detail
func (m *Machine) SampleTraffic(rx, tx uint64, ifaceExists bool) // 1s cadence; rates + totals
func (m *Machine) TransitionExpired() bool // connecting/disconnecting older than 20s
```

- [ ] **Step 1: Write the failing tests**

`state_test.go`:

```go
func TestConnectDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Machine{Now: func() time.Time { return now }}
	m.StartConnect()
	if m.Snapshot().Phase != PhaseConnecting { t.Fatal("want connecting") }
	now = now.Add(19 * time.Second)
	if m.TransitionExpired() { t.Fatal("expired early") }
	now = now.Add(2 * time.Second)
	if !m.TransitionExpired() { t.Fatal("want expired after 20s") }
	m.SetStatus(Status{Phase: PhaseError})
	if m.Snapshot().Phase != PhaseError { t.Fatal("want error") }
}

func TestTrafficRates(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Machine{Now: func() time.Time { return now }}
	m.SampleTraffic(1000, 500, true) // baseline
	now = now.Add(time.Second)
	m.SampleTraffic(2000, 1500, true)
	s := m.Snapshot()
	if s.RxRate != 1000 || s.TxRate != 1000 { t.Fatalf("got %v/%v", s.RxRate, s.TxRate) }
	if s.RxTotal != 2000 || s.TxTotal != 1500 { t.Fatalf("totals %v/%v", s.RxTotal, s.TxTotal) }
}

func TestTrafficStopsWhenInterfaceVanishes(t *testing.T) {
	now := time.Unix(1000, 0)
	m := &Machine{Now: func() time.Time { return now }}
	m.SampleTraffic(1000, 500, true)
	now = now.Add(time.Second)
	m.SampleTraffic(0, 0, false) // tunnel gone: rates zero, totals frozen
	s := m.Snapshot()
	if s.RxRate != 0 || s.TxRate != 0 { t.Fatal("want zero rates") }
	if s.RxTotal != 1000 { t.Fatalf("totals frozen at %v", s.RxTotal) }
}

func TestErrorClearsOnNextTransition(t *testing.T) {
	m := &Machine{Now: func() time.Time { return time.Unix(1000, 0) }}
	m.Fail("Tunnel setup failed")
	if m.Snapshot().Err == "" { t.Fatal("want detail") }
	m.StartConnect()
	if m.Snapshot().Err != "" { t.Fatal("want detail cleared") }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./plugins/protonvpn/ -run 'TestConnect|TestTraffic|TestError' -v`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement `state.go`**

Per the interfaces. `SetStatus` maps a `PhaseConnected` result to clear any transition deadline; a phase change while connecting/disconnecting updates the stamp. `SampleTraffic` computes rates from the delta over the elapsed time (guard divide-by-zero on the first sample); a vanished interface zeroes rates and freezes totals. `TransitionExpired` is true only in `PhaseConnecting`/`PhaseDisconnecting` past 20s.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./plugins/protonvpn/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/sysc-plugins && git add plugins/protonvpn/state.go plugins/protonvpn/state_test.go
git commit -m "feat(protonvpn): phase machine and traffic sampler"
```

---

### Task 7: Server list (`servers.go`)

**Files:**
- Create: `plugins/protonvpn/servers.go`, `plugins/protonvpn/servers_test.go`
- Create: `plugins/protonvpn/testdata/serverlist.json` (a trimmed fixture: 3 countries × 2–3 servers exercising features, tiers, loads, maintenance)

**Interfaces:**

```go
const (
	FeatSecureCore = 1 << iota
	FeatTor
	FeatP2P
	FeatStreaming
)

type Server struct {
	Name    string
	City    string
	Country string // ISO code
	Load    int    // 0..100
	Tier    int    // 0 = free
	Features int
	Up      bool
	Score   float64
}

type Country struct {
	Code     string
	Name     string
	Servers  []Server
	Load     int // rounded mean of up servers
	Maintenance bool
	Features int // union
}

func LoadServers(path string) ([]Server, error)
func Aggregate(servers []Server, countryNames map[string]string, freeTier bool) []Country
func FallbackCountries() []Country // the built-in 12, all Load 0, Maintenance false
```

- [ ] **Step 1: Write the fixture**

`testdata/serverlist.json` — mirror the real file's shape (`LogicalServers` array; fields `Name`, `City`, `ExitCountry`, `Load`, `Tier`, `Features`, `Status`, `Score`). Verify the field names against noctalia's `servers.py` (which parses the same file) and adjust the struct tags to match. Include: one US server (P2P, load 30, up), one US server (load 95, up), one NL server (Tor+P2P, down), one SE server (Secure Core, up, tier 2), one free-tier server (tier 0).

- [ ] **Step 2: Write the failing tests**

```go
func TestLoadServers(t *testing.T) {
	servers, err := LoadServers("testdata/serverlist.json")
	if err != nil { t.Fatal(err) }
	if len(servers) != 5 { t.Fatalf("got %d", len(servers)) }
}

func TestAggregate(t *testing.T) {
	servers, _ := LoadServers("testdata/serverlist.json")
	countries := Aggregate(servers, map[string]string{"US": "United States", "NL": "Netherlands", "SE": "Sweden"}, false)
	if len(countries) != 3 { t.Fatalf("got %d", len(countries)) }
	us := countries[0]
	if us.Code != "US" || len(us.Servers) != 2 || us.Maintenance { t.Fatalf("US: %+v", us) }
	if us.Load != 63 { t.Fatalf("US load %d", us.Load) } // round((30+95)/2)
	nl := findCountry(t, countries, "NL")
	if !nl.Maintenance { t.Fatal("NL all down → maintenance") }
	if nl.Features&FeatTor == 0 { t.Fatal("NL carries Tor") }
}

func TestAggregateFreeTierSortsFreeFirst(t *testing.T) {
	servers, _ := LoadServers("testdata/serverlist.json")
	countries := Aggregate(servers, nil, true)
	if countries[0].Servers[0].Tier != 0 { t.Fatal("free locations first") }
}

func TestFallbackCountries(t *testing.T) {
	fb := FallbackCountries()
	if len(fb) != 12 { t.Fatalf("got %d", len(fb)) }
	for _, c := range fb { if c.Name == "" || len(c.Code) != 2 { t.Fatalf("bad fallback %+v", c) } }
}
```

- [ ] **Step 3: Run to verify they fail, then implement**

`LoadServers` decodes the JSON array (tolerate both a bare array and an object wrapping it). `Aggregate` groups by exit country, computes the mean load of up servers (0 when all down → `Maintenance`), unions features, sorts by name; `freeTier` sorts countries whose best server is tier 0 first. `FallbackCountries` embeds the same 12 codes noctalia ships (US, GB, DE, FR, NL, CA, JP, AU, IT, ES, SE, CH).

- [ ] **Step 4: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestLoad|TestAggregate|TestFallback' -v
git add plugins/protonvpn/servers.go plugins/protonvpn/servers_test.go plugins/protonvpn/testdata/serverlist.json
git commit -m "feat(protonvpn): serverlist aggregation"
```

---

### Task 8: Bar and tooltip (`bar.go`)

**Files:**
- Create: `plugins/protonvpn/bar.go`, `plugins/protonvpn/bar_test.go`

**Interfaces:**

```go
type BarState struct {
	Snap     Snapshot
	Mode     string // icon | code | status
	Quick    string // quick_connect setting, for the tooltip hint
}

func Bar(s BarState) *v1.Node        // root row for the 240×32 bar
func Tooltip(s BarState) *v1.Node    // root column for 280×200
```

- [ ] **Step 1: Write the failing tests**

```go
func TestBarStates(t *testing.T) {
	cases := []struct {
		phase Phase; wantIcon string; wantTone v1.Tone
	}{
		{PhaseDisconnected, "vpn_key_off", v1.ToneSubtle},
		{PhaseConnecting, "bolt", v1.ToneAccent},
		{PhaseConnected, "shield", v1.ToneAccent},
		{PhaseError, "gpp_bad", v1.ToneError},
	}
	for _, tc := range cases {
		n := Bar(BarState{Snap: Snapshot{Phase: tc.phase, Status: Status{Country: "US"}}, Mode: "code"})
		icon, text := barContent(t, n)
		if icon != tc.wantIcon { t.Errorf("phase %v icon %q", tc.phase, icon) }
		_ = text
	}
}

func TestBarModes(t *testing.T) {
	s := BarState{Snap: Snapshot{Phase: PhaseConnected, Status: Status{Country: "US"}}, Mode: "icon"}
	if text := barText(t, Bar(s)); text != "" { t.Fatalf("icon mode shows %q", text) }
	s.Mode = "code"
	if text := barText(t, Bar(s)); text != "US" { t.Fatalf("code mode shows %q", text) }
	s.Mode = "status"
	if text := barText(t, Bar(s)); text != "Protected" { t.Fatalf("status mode shows %q", text) }
}

func TestBarConnectingShowsEllipsis(t *testing.T) {
	s := BarState{Snap: Snapshot{Phase: PhaseConnecting}, Mode: "code"}
	if text := barText(t, Bar(s)); text != "…" { t.Fatalf("got %q", text) }
}

func TestBarLintEveryState(t *testing.T) {
	for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
		for _, mode := range []string{"icon", "code", "status"} {
			root := Bar(BarState{Snap: Snapshot{Phase: phase, Status: Status{Country: "US", Server: "US-NY#1"}}, Mode: mode})
			if findings := lint.Tree(root, "bar", lint.BarWidth, lint.BarHeight); len(findings) > 0 {
				t.Fatalf("phase %v mode %s: %v", phase, mode, findings)
			}
		}
	}
}

func TestTooltipLint(t *testing.T) {
	root := Tooltip(BarState{Snap: Snapshot{Phase: PhaseConnected, Status: Status{Server: "US-NY#1", Country: "United States", City: "New York", IP: "198.51.100.7", Protocol: "WireGuard"}, RxRate: 1024, TxRate: 512}})
	if findings := lint.Tree(root, "tooltip", 280, 200); len(findings) > 0 { t.Fatalf("%v", findings) }
}
```

(`barContent`/`barText` are small test helpers walking the tree.)

- [ ] **Step 2: Run to verify they fail, then implement**

`Bar` returns a row containing one button: ID `bar`, Name `ProtonVPN`, Role `button`, Events `activate` + `pointer` (right click handled by the loop via `Button`), children = icon + optional text per mode. Status words: `Unprotected` / `Connecting…` / `Protected` / `Disconnecting…` / `Connection error`. `Tooltip` renders: status word (bold), `server`, `city, country`, `IP`, `↓ rx · ↑ tx` as `download`/`upload` icons with tabular text, `protocol`; disconnected shows `Unprotected` + `Right-click to quick connect`.

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestBar|TestTooltip' -v
git add plugins/protonvpn/bar.go plugins/protonvpn/bar_test.go
git commit -m "feat(protonvpn): bar and tooltip"
```

---

### Task 9: Panel skeleton and connection card (`panel.go`)

**Files:**
- Create: `plugins/protonvpn/panel.go`, `plugins/protonvpn/panel_test.go`

**Interfaces:**

```go
type PanelState struct {
	Snap     Snapshot
	Tab      string // connections | protection | account
	HasCLI   bool
	Traffic  bool   // traffic_monitoring setting
}

func Panel(s PanelState) *v1.Node // root column, 460×580 budget
```

- [ ] **Step 1: Write the failing tests**

```go
func TestPanelSkeleton(t *testing.T) {
	s := PanelState{Snap: Snapshot{Phase: PhaseConnected, Status: Status{Server: "US-NY#1", Country: "United States", City: "New York", IP: "198.51.100.7", Protocol: "WireGuard"}}, Tab: "connections", HasCLI: true, Traffic: true}
	root := Panel(s)
	// Connection card: status word, server, city/country, IP·protocol, action button.
	assertTextContains(t, root, "Protected")
	assertTextContains(t, root, "US-NY#1")
	assertTextContains(t, root, "New York, United States")
	assertTextContains(t, root, "198.51.100.7 · WireGuard")
	if btn := findNode(t, root, "action"); btn.Text != "Disconnect" { t.Fatalf("action %q", btn.Text) }
}

func TestActionButtonMorphs(t *testing.T) {
	cases := []struct{ phase Phase; want string; wantDisabled bool }{
		{PhaseDisconnected, "Connect", false},
		{PhaseConnecting, "Cancel", false},
		{PhaseConnected, "Disconnect", false},
		{PhaseDisconnecting, "Disconnect", true},
		{PhaseError, "Connect", false},
	}
	for _, tc := range cases {
		btn := findNode(t, Panel(PanelState{Snap: Snapshot{Phase: tc.phase}, Tab: "connections", HasCLI: true}), "action")
		if btn.Text != tc.want || btn.Disabled != tc.wantDisabled { t.Fatalf("phase %v: %+v", tc.phase, btn) }
	}
}

func TestPanelDisconnectedCopy(t *testing.T) {
	root := Panel(PanelState{Snap: Snapshot{Phase: PhaseDisconnected}, Tab: "connections", HasCLI: true})
	assertTextContains(t, root, "Unprotected")
	assertTextContains(t, root, "Fastest country")
	assertTextContains(t, root, "Auto-selected on connect")
}

func TestPanelErrorDetailLine(t *testing.T) {
	root := Panel(PanelState{Snap: Snapshot{Phase: PhaseError, Err: "Tunnel setup failed"}, Tab: "connections", HasCLI: true})
	assertTextContains(t, root, "Connection error")
	assertTextContains(t, root, "Tunnel setup failed")
}

func TestPanelCLIMissingBanner(t *testing.T) {
	root := Panel(PanelState{Tab: "connections"})
	assertTextContains(t, root, "protonvpn CLI not found")
}

func TestPanelTabNav(t *testing.T) {
	root := Panel(PanelState{Tab: "protection", HasCLI: true})
	nav := findNode(t, root, "tab:protection")
	if nav.Fill != "accent" { t.Fatalf("active tab fill %q", nav.Fill) }
	if other := findNode(t, root, "tab:connections"); other.Fill != "soft" { t.Fatalf("inactive fill %q", other.Fill) }
}

func TestPanelLintEveryState(t *testing.T) {
	for _, tab := range []string{"connections", "protection", "account"} {
		for _, phase := range []Phase{PhaseDisconnected, PhaseConnecting, PhaseConnected, PhaseDisconnecting, PhaseError} {
			root := Panel(PanelState{Snap: Snapshot{Phase: phase, Status: Status{Server: "US-NY#1", Country: "United States", City: "New York", IP: "198.51.100.7", Protocol: "WireGuard"}, RxRate: 1024, TxRate: 512, Port: 51820}, Tab: tab, HasCLI: true, Traffic: true})
			if findings := lint.Tree(root, "panel", 460, 580); len(findings) > 0 {
				t.Fatalf("tab %s phase %v: %v", tab, phase, findings)
			}
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail, then implement**

Root column, Padding 12, Gap 8. Children:

1. Optional banner (when `!HasCLI`): error-tone text `protonvpn CLI not found`.
2. Connection card: row, Fill `card`, Radius 10, Padding 8, Height 96, Gap 8 —
   - leading column (clips): row 1 = status icon (`vpn_key_off` subtle / `bolt` accent / `shield` accent / `gpp_bad` error) + status word (bold); row 2 = server name (bold) + country-code badge (chip capsule) — disconnected shows `Fastest country` over `Auto-selected on connect`; row 3 = `IP · protocol` (subtle) or, when connected and `Traffic`, the keyed rx/tx line (key `traffic`: `download` icon + tabular rate + `upload` icon + tabular rate).
   - PinEnd action button: ID `action`, 92×40, text per the morph table, Fill accent (`Connect`) / soft (`Cancel`) / error (`Disconnect`), Disabled while disconnecting.
3. Error detail (when `Snap.Err != ""`): error-tone text, key `err`.
4. Tab nav row: three buttons `tab:connections` / `tab:protection` / `tab:account`, equal share, Height 36, active Fill `accent`, inactive `soft`.
5. Tab content: dispatch to `connectionsTree` / `protectionTree` / `accountTree` — stubs in this task (a single subtle text naming the tab), filled by Tasks 10–12.

Rate formatting: `formatRate(bps float64) string` → `1.2 MB/s`, `340 KB/s`, `12 B/s` (one decimal below 10).

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestPanel|TestAction' -v
git add plugins/protonvpn/panel.go plugins/protonvpn/panel_test.go
git commit -m "feat(protonvpn): panel skeleton and connection card"
```

---

### Task 10: Connections tab (`connections.go`)

**Files:**
- Create: `plugins/protonvpn/connections.go`, `plugins/protonvpn/connections_test.go`

**Interfaces:**

```go
type ConnectionsState struct {
	Snap        Snapshot
	Query       string
	QueryReseed uint64
	Countries   []Country
	Expanded    string // country code currently expanded
	Flags       bool   // flag emoji spike outcome
	Notice      string // "Server list unavailable"
}

func ConnectionsTree(s ConnectionsState) *v1.Node
```

- [ ] **Step 1: Write the failing tests**

```go
func TestConnectionsQuickConnectRow(t *testing.T) {
	root := ConnectionsTree(ConnectionsState{Snap: Snapshot{Phase: PhaseDisconnected}})
	for _, id := range []string{"qc:fastest", "qc:random", "qc:p2p", "qc:tor"} {
		if findNode(t, root, id) == nil { t.Fatalf("missing %s", id) }
	}
}

func TestConnectionsCountryRows(t *testing.T) {
	countries := []Country{
		{Code: "US", Name: "United States", Load: 63, Servers: []Server{{Name: "US-NY#1", City: "New York", Load: 30, Up: true}}},
		{Code: "NL", Name: "Netherlands", Load: 0, Maintenance: true, Servers: []Server{{Name: "NL#1", Up: false}}},
	}
	root := ConnectionsTree(ConnectionsState{Countries: countries})
	assertTextContains(t, root, "United States")
	assertTextContains(t, root, "1 servers · 63%")
	if findNode(t, root, "country:US") == nil { t.Fatal("missing country row") }
	nl := findNode(t, root, "country:NL")
	if nl.Tone != v1.ToneSubtle { t.Fatal("maintenance row dimmed") }
	if btn := findNode(t, root, "connect:NL"); btn == nil || btn.Disabled != true { t.Fatal("maintenance connect disabled") }
}

func TestConnectionsExpandShowsServers(t *testing.T) {
	countries := []Country{{Code: "US", Name: "United States", Servers: []Server{
		{Name: "US-NY#1", City: "New York", Load: 30, Up: true, Features: FeatP2P},
		{Name: "US-CA#1", City: "Los Angeles", Load: 95, Up: true},
	}}}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Expanded: "US"})
	assertTextContains(t, root, "US-NY#1")
	assertTextContains(t, root, "US-CA#1")
	if findNode(t, root, "server:US-NY#1") == nil { t.Fatal("missing server row") }
}

func TestConnectionsLoadTone(t *testing.T) {
	// >90 error, >75 accent, else normal — on the load progress node.
}

func TestConnectionsSearchFilters(t *testing.T) {
	countries := []Country{
		{Code: "US", Name: "United States", Servers: []Server{{Name: "US-NY#1", Up: true}}},
		{Code: "DE", Name: "Germany", Servers: []Server{{Name: "DE-FRA#1", Up: true}}},
	}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Query: "germ"})
	assertTextContains(t, root, "Germany")
	if findNode(t, root, "country:US") != nil { t.Fatal("US should be filtered out") }
	// Server-name search surfaces matches grouped under their country, auto-expanded.
	root = ConnectionsTree(ConnectionsState{Countries: countries, Query: "NY#1"})
	assertTextContains(t, root, "US-NY#1")
}

func TestConnectionsConnectedCountryTint(t *testing.T) {
	countries := []Country{{Code: "US", Name: "United States"}}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Snap: Snapshot{Phase: PhaseConnected, Status: Status{Country: "US"}}})
	if row := findNode(t, root, "country:US"); row.Fill != "container" { t.Fatalf("fill %q", row.Fill) }
}

func TestConnectionsLintLongNames(t *testing.T) {
	long := strings.Repeat("X", 30)
	countries := []Country{{Code: "US", Name: long, Servers: []Server{{Name: long, City: long, Load: 50, Up: true}}}}
	root := ConnectionsTree(ConnectionsState{Countries: countries, Expanded: "US"})
	if findings := lint.Tree(root, "panel", 460, 580); len(findings) > 0 { t.Fatalf("%v", findings) }
}

func TestConnectionsLintEveryState(t *testing.T) {
	// disconnected/connecting/connected × empty countries, fallback notice, expanded, search.
}
```

- [ ] **Step 2: Run to verify they fail, then implement**

Content column (inside the 404 budget): quick-connect row (four buttons `qc:fastest` / `qc:random` / `qc:p2p` / `qc:tor`, equal share, Height 36, Fill `soft`, disabled while transitioning); search row (text_input ID `search`, Width = 460−24−gap−40, Height 40, Placeholder `Search country or server`, `Reseed: QueryReseed`, Events change+submit; clear button ID `clear-search` 40×40, Disabled when query empty); notice line (subtle, when set); `KindList` Height ~316, Gap 4.

Country row (ID `country:<CC>`, row, Fill `card` or `container` when connected-country, Radius 10, Padding 8): flag glyph or code-badge capsule (fixed Width 36, chip fill, bold 2-letter code); name (bold, clips); `N servers · L%` (subtle) + load `progress` (Width 48, Height 6, Value load/100, Tone by threshold) — hidden on maintenance rows; expand button `expand:<CC>` (28×28, icon `expand_more`); connect button `connect:<CC>` PinEnd (text `Connect`, 84×32, Fill accent, Disabled on maintenance).

Expanded server rows (indented column under the country row): per server, row ID `server:<name>`: `dns` icon, name (clips), city (subtle, clips), load % (tabular), feature tags (`lan` P2P / `visibility_off` Tor / `security` Secure Core / `play_arrow` streaming, subtle), connect button `server-connect:<name>` PinEnd. Maintenance servers: subtle tone, tooltip `{name} is under maintenance`, disabled connect.

Search: case-insensitive substring on country name/code; server-name matches render their country auto-expanded with only matching servers. Empty result: subtle `No matching location`.

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestConnections' -v
git add plugins/protonvpn/connections.go plugins/protonvpn/connections_test.go
git commit -m "feat(protonvpn): connections tab"
```

---

### Task 11: Protection tab (`protection.go`)

**Files:**
- Create: `plugins/protonvpn/protection.go`, `plugins/protonvpn/protection_test.go`
- Create: `plugins/protonvpn/splittunnel.go` (settings.json read/write; small enough to live beside protection.go — merge into `protection.go` if it stays under ~80 lines)

**Interfaces:**

```go
type ProtectionState struct {
	Snap       Snapshot
	Apps       []string // excluded app paths
	Candidates []App    // scan results for the picker
	AppQuery   string
	AppReseed  uint64
	Port       int
	HasCopyTool bool
	Err        string
}

type App struct{ Value, Label string }

func ProtectionTree(s ProtectionState) *v1.Node
func ReadSplitTunnel(path string) (enabled bool, apps []string, err error)
func WriteSplitTunnel(path string, enabled bool, apps []string) error // preserves unknown keys
```

- [ ] **Step 1: Write the failing tests**

```go
func TestProtectionRows(t *testing.T) {
	root := ProtectionTree(ProtectionState{Snap: Snapshot{Phase: PhaseConnected, Config: Config{KillSwitch: "standard", NetShield: "malware-only", PortForwarding: true}}, Port: 51820, HasCopyTool: true})
	if btn := findNode(t, root, "ks"); btn.Text != "On" { t.Fatalf("ks %q", btn.Text) }
	if findNode(t, root, "ns:malware-only").Fill != "accent" { t.Fatal("netshield active segment") }
	if findNode(t, root, "pf").Text != "On" { t.Fatal("pf toggle") }
	assertTextContains(t, root, "Active port: 51820")
	if findNode(t, root, "copy-port") == nil { t.Fatal("copy button") }
}

func TestProtectionPortRowHiddenWhenDisconnected(t *testing.T) {
	root := ProtectionTree(ProtectionState{Snap: Snapshot{Phase: Disconnected, Config: Config{PortForwarding: true}}, Port: 51820})
	if findNode(t, root, "copy-port") != nil { t.Fatal("port row only when connected") }
}

func TestProtectionCopyHiddenWithoutTool(t *testing.T) {
	root := ProtectionTree(ProtectionState{Snap: Snapshot{Phase: PhaseConnected, Config: Config{PortForwarding: true}}, Port: 51820})
	if findNode(t, root, "copy-port") != nil { t.Fatal("copy hidden without wl-copy/xclip") }
}

func TestProtectionSplitTunnelBlockedByKillSwitch(t *testing.T) {
	root := ProtectionTree(ProtectionState{Snap: Snapshot{Config: Config{KillSwitch: "standard"}}})
	if btn := findNode(t, root, "st"); btn.Disabled != true { t.Fatal("st disabled under KS") }
	assertTextContains(t, root, "Disable kill switch to use split tunneling")
}

func TestProtectionAppRowsAndSuggestions(t *testing.T) {
	root := ProtectionTree(ProtectionState{
		Apps: []string{"/usr/bin/firefox"},
		Candidates: []App{{Value: "/usr/bin/firefox", Label: "Firefox"}, {Value: "/usr/bin/chromium", Label: "Chromium"}},
		AppQuery: "fire",
	})
	assertTextContains(t, root, "Firefox")
	if findNode(t, root, "del-app:/usr/bin/firefox") == nil { t.Fatal("delete button") }
	if findNode(t, root, "app-suggest:/usr/bin/firefox") == nil { t.Fatal("suggestion chip") }
}

func TestSplitTunnelPreservesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(`{"features":{"split_tunneling":{"enabled":false,"apps":[]}},"other":{"keep":true}}`), 0o600)
	if err := WriteSplitTunnel(path, true, []string{"/usr/bin/firefox"}); err != nil { t.Fatal(err) }
	var doc map[string]any
	raw, _ := os.ReadFile(path)
	json.Unmarshal(raw, &doc)
	other := doc["other"].(map[string]any)
	if other["keep"] != true { t.Fatal("unknown keys clobbered") }
	st := doc["features"].(map[string]any)["split_tunneling"].(map[string]any)
	if st["enabled"] != true || !reflect.DeepEqual(st["apps"], []any{"/usr/bin/firefox"}) { t.Fatalf("st: %v", st) }
}

func TestSplitTunnelMalformedFileFailsLoud(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte("not json"), 0o600)
	if err := WriteSplitTunnel(path, true, nil); err == nil { t.Fatal("want error, not clobber") }
}

func TestProtectionLint(t *testing.T) {
	// KS on/off × PF on/off × ST on/off × app rows, at 460×580.
}
```

- [ ] **Step 2: Run to verify they fail, then implement**

A `KindList` (Height 404, Gap 4) of rows:

- Kill switch: label `Kill Switch` (bold) + description `Block traffic if the tunnel drops` (subtle) + button ID `ks` PinEnd (text `On`/`Off`, Fill accent/soft).
- NetShield: label + description + segmented row of three buttons `ns:off` / `ns:malware-only` / `ns:malware-ads-trackers` labelled `Off` / `Malware` / `Malware+Ads`, active Fill accent.
- Port forwarding: label + description + button `pf`; when on and connected and `Port > 0`: row `Active port: {N}` (tabular) + copy button `copy-port` (icon `content_copy`, only when `HasCopyTool`); when on and `Port == 0`: subtle `Negotiating port…`.
- Split tunneling: label + description + button `st`; under KS: Disabled + error-tone `Disable kill switch to use split tunneling`; when enabled: excluded-app rows (label = app name, subtle path, delete button `del-app:<path>` icon `delete`), add row (text_input ID `app-query`, Placeholder `Add an app`, Reseed, change+submit) and up to 5 suggestion chips `app-suggest:<path>` (label + path, from `Candidates` filtered by `AppQuery`, excluding already-added); enabling fires the host `notify` call with `Split tunneling enabled. Remember to restart affected apps.` (loop-side, Task 15).
- `Err` renders as an error-tone line at the foot.

`ReadSplitTunnel`/`WriteSplitTunnel`: decode into `map[string]any` (preserve everything), mutate `features.split_tunneling.{enabled,apps}`, re-encode with two-space indent; a malformed file or missing parent objects is an error — never clobber. Default path `~/.config/Proton/VPN/settings.json` (caller supplies it; tests use temp dirs).

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestProtection|TestSplitTunnel' -v
git add plugins/protonvpn/protection.go plugins/protonvpn/protection_test.go plugins/protonvpn/splittunnel.go
git commit -m "feat(protonvpn): protection tab and split tunneling"
```

---

### Task 12: Account tab (`account.go`)

**Files:**
- Create: `plugins/protonvpn/account.go`, `plugins/protonvpn/account_test.go`

**Interfaces:**

```go
type AccountState struct {
	Snap        Snapshot
	SignedIn    bool
	UserDraft   string
	UserReseed  uint64
	Err         string
}

func AccountTree(s AccountState) *v1.Node
```

- [ ] **Step 1: Write the failing tests**

```go
func TestAccountSignedIn(t *testing.T) {
	root := AccountTree(AccountState{SignedIn: true, Snap: Snapshot{Info: Info{Username: "jane@example.com", Plan: "Proton VPN Plus", Version: "3.13.0", Interface: "proton0"}, Status: Status{Protocol: "WireGuard"}}})
	assertTextContains(t, root, "jane@example.com")
	assertTextContains(t, root, "Proton VPN Plus")
	assertTextContains(t, root, "3.13.0")
	assertTextContains(t, root, "proton0")
	if findNode(t, root, "signout") == nil { t.Fatal("signout") }
	if findNode(t, root, "refresh") == nil { t.Fatal("refresh") }
}

func TestAccountSignedOut(t *testing.T) {
	root := AccountTree(AccountState{UserReseed: 1})
	if findNode(t, root, "signin-user") == nil { t.Fatal("username input") }
	if findNode(t, root, "signin") == nil { t.Fatal("signin button") }
	assertTextContains(t, root, "Complete sign-in in the terminal (password + 2FA)")
}

func TestAccountOptionsSection(t *testing.T) {
	root := AccountTree(AccountState{SignedIn: true})
	assertTextContains(t, root, "Options")
	assertTextContains(t, root, "Change in shell settings")
}

func TestAccountLint(t *testing.T) {
	// signed-in / signed-out / error, at 460×580.
}
```

- [ ] **Step 2: Run to verify they fail, then implement**

A `KindList` (Height 404, Gap 4):

- Signed-in card: row Fill `card` Radius 10 Padding 8 — `person` icon, username (bold), plan (subtle).
- Buttons row: `signout` (`Sign out`, Fill soft) + `refresh` (icon `refresh`, 40×40).
- Info rows (label subtle + value tabular PinEnd): `CLI version`, `Protocol`, `Tunnel interface`.
- Signed-out instead renders: hint card (`Complete sign-in in the terminal (password + 2FA)`, subtle), sign-in row (text_input ID `signin-user`, Placeholder `Username`, Reseed, change+submit; button `signin` `Sign in`, Fill accent, Disabled when draft empty), and the terminal-handoff hint line.
- Options section: title `Options` + read-only rows for the five settings values + subtle `Change in shell settings`.
- `Err` as an error-tone line at the foot.

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestAccount' -v
git add plugins/protonvpn/account.go plugins/protonvpn/account_test.go
git commit -m "feat(protonvpn): account tab"
```

---

### Task 13: App scanner (`apps.go`)

**Files:**
- Create: `plugins/protonvpn/apps.go`, `plugins/protonvpn/apps_test.go`
- Create: `plugins/protonvpn/testdata/applications/*.desktop` (fixture tree)

**Interfaces:**

```go
func ScanApps(dataDirs []string, pathEnv []string) []App
```

- [ ] **Step 1: Write fixtures and failing tests**

Fixtures under `testdata/applications/`: `firefox.desktop` (`Exec=firefox %u`), `code.desktop` (`Exec=/usr/share/code/code --no-sandbox`), `flatpak-app.desktop` (`Exec=flatpak run org.example.App`), `snap-app.desktop` (`Exec=snap run example`), `terminal.desktop` (`Exec=gnome-terminal`), `setuid.desktop` pointing at a fixture binary mode 4755.

```go
func TestScanApps(t *testing.T) {
	dirs := []string{"testdata/applications"}
	paths := []string{"/usr/bin", "/usr/local/bin"} // fixture binaries live here (created by the test)
	apps := ScanApps(dirs, paths)
	got := map[string]string{}
	for _, a := range apps { got[a.Label] = a.Value }
	if got["Firefox"] != "firefox" { t.Fatalf("firefox: %v", got) }
	if got["Code"] != "/usr/share/code/code" { t.Fatalf("code: %v", got) }
	for _, banned := range []string{"Flatpak App", "Snap App", "Terminal"} {
		if _, ok := got[banned]; ok { t.Fatalf("%s must be excluded", banned) }
	}
}

func TestScanAppsSortedByLabel(t *testing.T) { /* assert ascending labels */ }
```

- [ ] **Step 2: Run to verify they fail, then implement**

Port of noctalia's `apps.py`: parse `Exec=` (strip field codes `%u` etc.), take the first token, resolve against `pathEnv` (absolute paths kept as-is), keep only executable regular files; exclude flatpak/snap runners, shells (`sh bash zsh fish dash ksh tcsh env gtk-launch xdg-open`), dispatchers (`hyprctl uwsm xdg-terminal-exec dbus-launch`, `omarchy-*` prefixes), setuid/setgid binaries. Sort by label. The loop calls it with `xdg.DataDirs` + `~/.local/share` and `os.Getenv("PATH")` split.

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestScan' -v
git add plugins/protonvpn/apps.go plugins/protonvpn/apps_test.go plugins/protonvpn/testdata/applications
git commit -m "feat(protonvpn): split-tunnel app scanner"
```

---

### Task 14: NAT-PMP client (`natpmp.go`)

**Files:**
- Create: `plugins/protonvpn/natpmp.go`, `plugins/protonvpn/natpmp_test.go`

**Interfaces:**

```go
type NATPMP struct {
	Gateway string // default "10.2.0.1:5351"
	Now     func() time.Time
}

// RequestPort sends UDP+TCP mapping requests (lifetime 60s) and returns the
// assigned port, or an error. Retries back off 250ms→2s.
func (n *NATPMP) RequestPort(ctx context.Context) (int, error)
func BuildMapRequest(op uint8, internalPort, externalPort, lifetime uint32) []byte
func ParseMapResponse(b []byte) (port int, epoch uint32, err error)
```

- [ ] **Step 1: Write the failing tests (golden bytes)**

```go
func TestBuildMapRequest(t *testing.T) {
	got := BuildMapRequest(2, 0, 0, 60) // op 2 = TCP map per RFC 6886
	want := []byte{0, 2, 0, 0, 0, 0, 0, 0, 0, 60, 0, 0}
	if !bytes.Equal(got, want) { t.Fatalf("got %v want %v", got, want) }
}

func TestParseMapResponse(t *testing.T) {
	resp := []byte{0, 2, 0, 0, 0, 0, 20, 60, 0, 0, 0, 100, 0, 0, 0, 1}
	port, epoch, err := ParseMapResponse(resp)
	if err != nil || port != 5244 || epoch != 100 { t.Fatalf("port %d epoch %d err %v", port, epoch, err) }
	if _, _, err := ParseMapResponse([]byte{0, 2, 0, 6}); err == nil { t.Fatal("error result code must fail") }
}

func TestNATPMPFailureIsNonFatal(t *testing.T) {
	n := &NATPMP{Gateway: "127.0.0.1:1", Now: time.Now} // nothing listens
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := n.RequestPort(ctx); err == nil { t.Fatal("want error") } // and it must return, not hang
}
```

- [ ] **Step 2: Run to verify they fail, then implement**

RFC 6886: 12-byte map request (version 0, op 1=UDP/2=TCP, reserved, internal port 0, requested external port 0, lifetime 60s), send to the gateway, parse the 16-byte response (version, op, result code, epoch, port, lifetime); result code ≠ 0 is an error. Retry with 250ms→2s backoff until the context ends. The loop calls it every 45s while connected with PF on and feeds `Machine.SetPort`.

- [ ] **Step 3: Run to verify they pass, then commit**

```bash
go test ./plugins/protonvpn/ -run 'TestBuild|TestParse|TestNATPMP' -v
git add plugins/protonvpn/natpmp.go plugins/protonvpn/natpmp_test.go
git commit -m "feat(protonvpn): NAT-PMP client"
```

---

### Task 15: Event loop (`main.go`)

**Files:**
- Modify: `cmd/sysc-plugin-protonvpn/main.go`
- Create: `cmd/sysc-plugin-protonvpn/main_test.go`

**Interfaces (session additions):**

```go
type session struct {
	// ... skeleton fields from Task 4 ...
	machine    state.Machine   // or local equivalent
	cli        *protonvpn.CLI
	countries  []protonvpn.Country
	apps       []protonvpn.App
	tab        string
	expanded   string
	query, appQuery string
	queryReseed, appReseed uint64
	hasCLI, hasCopyTool, flags bool
}
```

- [ ] **Step 1: Write the failing loop tests**

Fake-client tests in the style of `cmd/sysc-plugin-world-clock/main_test.go`:

```go
func TestTabSwitchPersists(t *testing.T) {
	// InputEvent activate on tab:protection → next state.set payload contains {"tab":"protection"}.
}
func TestActionButtonWiresCommands(t *testing.T) {
	// action on disconnected → Connect ran with no target; on connected → Disconnect.
}
func TestRightClickQuickConnect(t *testing.T) {
	// bar pointer event Button right, disconnected, quick_connect=p2p → Connect --p2p.
}
func TestSettingsChangeRearms(t *testing.T) {
	// settings.changed refresh_seconds=2 → poll ticker re-armed; bad value keeps current.
}
func TestNotifyOnConnect(t *testing.T) {
	// phase transitions to connected with notify_on_connect → one notify call; setting off → none.
}
func TestSplitTunnelEnableNotifies(t *testing.T) {
	// st toggle on → notify "Split tunneling enabled. Remember to restart affected apps."
}
```

- [ ] **Step 2: Run to verify they fail, then implement**

Loop responsibilities (mirror the world-clock main.go structure):

- **Startup**: probe `exec.LookPath("protonvpn")` → `hasCLI`; probe `wl-copy`/`xclip` → `hasCopyTool`; restore `ui` state (tab); initial `Status`/`Info`/`Config` fetch; load serverlist (`LoadServers` on `~/.cache/Proton/VPN/serverlist.json`, fallback `FallbackCountries` + notice); scan apps.
- **Timers**: status poll every `refresh_seconds` (1s while connecting/disconnecting; `TransitionExpired` → `Fail("Timeout")`); nmcli link watch every 2s while connected (vanished tunnel → immediate status refresh); traffic sample every 1s while connected (`/sys/class/net/proton0/statistics/rx_bytes` + `tx_bytes`); NAT-PMP every 45s while connected + PF on.
- **InputEvent dispatch** by node ID: `bar` (activate → `panel.open`; pointer Button 3 → quick connect/disconnect per phase + `quick_connect` setting), `action`, `qc:*`, `search`/`clear-search`, `expand:<CC>`, `connect:<CC>` → `Connect --country CC`, `server-connect:<name>` → `Connect <name>`, `ks`, `ns:*`, `pf`, `copy-port` (shell `wl-copy`/`xclip`, then a transient "Copied" affordance), `st`, `del-app:<path>`, `app-query`/`app-suggest:<path>`, `signin`/`signin-user`, `signout`, `refresh`, `tab:*`.
- **Sign-in handoff**: spawn `xdg-terminal-exec` → `x-terminal-emulator` → `kitty alacritty foot gnome-terminal konsole xterm`, first found, running `protonvpn signin <user>`; on spawn failure set `Err` with the manual command.
- **Notifications**: on phase → connected (and `notify_on_connect`) notify `Connected to <server>`; → disconnected notify `Disconnected`; split-tunnel enable notify per spec.
- **Patching**: keyed patches (`traffic`, `err`, bar) when shape is stable; snapshots on tab switch, expand, search, phase transitions, settings changes.

- [ ] **Step 3: Run to verify they pass**

```bash
go test ./cmd/sysc-plugin-protonvpn/ -v && go test ./plugins/protonvpn/ -v
```

- [ ] **Step 4: Commit**

```bash
cd ~/sysc-plugins && git add cmd/sysc-plugin-protonvpn
git commit -m "feat(protonvpn): event loop, polling, and notifications"
```

---

### Task 16: Full lint sweep, docs, live acceptance

**Files:**
- Modify: `README.md`, `docs/plans/README.md` (if not done in Task 4)
- Create: `docs/plans/2026-09-26-protonvpn-handover.md`

- [ ] **Step 1: Whole-repo gates**

```bash
cd ~/sysc-plugins && make build && make test && make validate
```
Expected: all PASS, including world-clock and every other plugin.

- [ ] **Step 2: Lint sweep across every view state**

Confirm the test suites cover: panel 460×580 × {3 tabs} × {5 phases} × {search, expanded, app-picker, maintenance, CLI-missing}; bar 240×32 × {3 modes} × {5 phases}; tooltip 280×200 × {connected, disconnected}. Add any missing combination the sweep reveals.

- [ ] **Step 3: Live acceptance**

```bash
make install
```
Reload the shell. With the `protonvpn` CLI installed and signed in:

1. Bar shows `vpn_key_off` + no text; right-click quick-connects; the pill morphs to `shield` + country code.
2. Panel opens at 460×580; connection card shows `Unprotected` / `Fastest country`; Connect morphs the button through `Cancel` → `Disconnect`.
3. Connections tab: search filters, country expands to servers, load colours match thresholds, connected country is tinted.
4. Protection tab: toggles reflect `config list`; `Active port: N` appears when PF is on and connected; copy works (or is hidden).
5. Account tab: plan and info rows correct; sign-out/sign-in handoff opens a terminal.
6. Screenshots: panel (all tabs, connected + disconnected), bar in each mode, tooltip. Save under `docs/plans/` or the handover doc.
7. Kill the tunnel externally (`nmcli` down) → bar returns to `Unprotected` within ~2s (link watch) or ≤ `refresh_seconds` without nmcli.

- [ ] **Step 4: Handover doc and final commit**

Write `docs/plans/2026-09-26-protonvpn-handover.md`: what shipped, the flag-spike outcome, fixture provenance (which CLI version produced them), known gaps (protocol override, pinned servers), and the verification transcript.

```bash
cd ~/sysc-plugins && git add -A && git status --short
git commit -m "docs(protonvpn): handover and acceptance record"
```
