# sysc-games Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** A Go sysc-shell plugin (`org.sysc.games`) — Lutris game deck with real launch/running truth, process actions, session sparklines, and a cover-art chain; exceeds the reference QML lutrisLauncher.

**Architecture:** `plugins/games/` in the `~/sysc-plugins` monorepo. A `Source` interface (one impl: lutris, reading `pga.db` via modernc.org/sqlite, read-only). Pure functions build the `v1.Node` tree (panel + bar pill). `running/` scans `/proc` (injectable root). `covers/` resolves art: local → SteamGridDB → generated initials tile. Persistence via host `state.get/set` (`prefs`, `sessions`). Main loop copies `cmd/sysc-plugin-world-clock/main.go` structure verbatim.

**Tech Stack:** Go 1.26, `github.com/Nomadcxx/sysc-plugins`, `sysc-shell/plugin/v1` (replace-directive), `modernc.org/sqlite` (new dep), stdlib `image/draw`+`png`, `os/exec` (xdg-open), `/proc`.

**Verified protocol facts (do not re-litigate):**
- PGA schema has NO `hidden` column; columns incl. `id,name,slug,platform,runner,executable,directory,lastplayed,installed,year,configpath,playtime`.
- Panel shortcuts: `manifest.json` → `panels[].shortcuts: [{"key":"<single a-z/0-9>","node":"<node-id>"}]` (modifiers optional; Escape/arrows are host-owned — `/` search must map to letter keys, use `f` for favorite-focus or bind search node to key... host only sends alphanumerics; bind what we need). Events arrive as `v1.InputEvent{Event: v1.EventShortcut, Node: <declared id>, Key}`.
- `KindSegmented` children are buttons, panel-view-only. `KindImage` needs absolute `Path` + exactly one of `ImageSize` or `ImageW/ImageH`. `KindGraph` = column sparkline, `Values []float64`.
- Revision discipline: discard InputEvents whose `Revision != view.rev`.

**Test gate:** `cd ~/sysc-plugins && go test ./... && go vet ./...` green at every commit. Manifest validation: `go run ./tools/validate-manifests`.

---

### Task 1: Scaffold + dependency

**Files:**
- Modify: `~/sysc-plugins/go.mod` (via go get)
- Create: `~/sysc-plugins/plugins/games/manifest.json`
- Create: `~/sysc-plugins/cmd/sysc-plugin-games/main.go`

**Step 1:** `cd ~/sysc-plugins && go get modernc.org/sqlite@latest`

**Step 2:** Create `plugins/games/manifest.json` modeled on `cmd/sysc-plugin-world-clock/manifest.json`: id `org.sysc.games`, version 0.1.0, protocol {major 1, minor 7}, exec `bin/sysc-plugin-games`, capabilities `["panels","settings","state"]`, requires.commands `[]`, one widget `{id:"bar"}`, one panel `{id:"panel", width:720, height:560, placement:"attached", shortcuts:[{"key":"g","node":"search-input"}]}` (g focuses search; `/` is not a valid shortcut key — alphanumeric only), settings: `steamgriddb_key` (string, default ""), `source_lutris` (bool, default true), `poll_running` (select off/auto, default auto), `hide_unavailable` (bool, default false).

**Step 3:** Create `cmd/sysc-plugin-games/main.go`: copy world-clock main.go skeleton (env struct, `runPlugin(in,out,env)`, recv-goroutine → buffered channel, select loop handling ViewOpen/ViewClose/ViewResync/InputEvent/SettingsChanged, `v1.NewClient`+`Handshake(v1.Identity{...})`). Stub `snapshotAll()` returning nil until panel tasks land.

**Step 4:** `go build ./cmd/sysc-plugin-games` → passes.

**Step 5:** `go run ./tools/validate-manifests` → passes.

**Step 6:** Commit `git add -A && git commit -m "games: scaffold plugin binary + manifest"`

---

### Task 2: source.Game + Source interface

**Files:**
- Create: `~/sysc-plugins/plugins/games/source/source.go`
- Test: `~/sysc-plugins/plugins/games/source/source_test.go` (smoke only)

**Step 1: Failing smoke test** — construct `source.Game{ID:"7", Name:"Hades", Runner:"wine"}`; assert `Game` compiles + `Source` interface satisfied by a fake:

```go
type fakeSource struct{ games []Game }
func (f *fakeSource) List(context.Context) ([]Game, error) { return f.games, nil }
func (f *fakeSource) Launch(_ context.Context, g Game) error { return nil }
func (f *fakeSource) Stop(_ context.Context, g Game) error { return nil }
func (f *fakeSource) Running(context.Context) (map[string]time.Time, error) { return nil, nil }
func (f *fakeSource) Sections(context.Context) ([]string, error) { return []string{"Action"}, nil }
```

**Step 2:** `go test ./plugins/games/source/ -run TestFakeSatisfiesInterface -v` → FAIL (undefined types).

**Step 3:** Implement `source.go`:

```go
package source

import (
	"context"
	"time"
)

type Game struct {
	ID          string
	Name        string
	Slug        string
	Runner      string
	Platform    string
	Year        string
	PlaytimeSec float64
	LastPlayed  time.Time
	Directory   string
	Executable  string
	ConfigPath  string
	CoverPath   string // resolved absolute art path, "" = none yet
	Source      string // "lutris"
}

type Source interface {
	Name() string
	List(ctx context.Context) ([]Game, error)
	Launch(ctx context.Context, g Game) error
	Stop(ctx context.Context, g Game) error
	Running(ctx context.Context) (map[string]time.Time, error) // keyed by Game.ID
	Sections(ctx context.Context) ([]string, error)
}
```

**Step 4:** run test → PASS. **Step 5:** commit `games: source interface + Game model`.

---

### Task 3: lutris source — List from pga.db

**Files:**
- Create: `~/sysc-plugins/plugins/games/source/lutris/lutris.go`
- Test: `~/sysc-plugins/plugins/games/source/lutris/lutris_test.go`

**Step 1: Failing test** with a fixture-builder helper (reuse in later tasks):

```go
func makePGA(t *testing.T) string { // real Lutris columns, temp dir
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pga.db")
	db, err := sql.Open("sqlite", dbPath) // modernc driver, imported by lutris pkg
	if err != nil { t.Fatal(err) }
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE games (id INTEGER PRIMARY KEY, name TEXT UNIQUE,
		sortname TEXT, slug TEXT, installer_slug TEXT, parent_slug TEXT, platform TEXT,
		runner TEXT, executable TEXT, directory TEXT, updated DATETIME, lastplayed DATETIME,
		installed BOOLEAN, installed_at DATETIME, year TEXT, configpath TEXT,
		has_custom_banner BOOLEAN DEFAULT 0, has_custom_icon BOOLEAN DEFAULT 0,
		has_custom_coverart_big BOOLEAN DEFAULT 0, playtime REAL DEFAULT 0,
		service TEXT, service_id TEXT, discord_id TEXT)`)
	if err != nil { t.Fatal(err) }
	_, _ = db.Exec(`INSERT INTO games (name,slug,runner,platform,playtime,lastplayed,installed,directory,year)
		VALUES ('Hades','hades','wine','Windows',3.5,'2026-09-20 18:00:00',1,'/Games/Hades','2020'),
		('Not Installed','ni','linux',NULL,0,NULL,0,NULL,NULL)`)
	return dbPath
}

func TestListReadsGames(t *testing.T) {
	src, err := lutris.New(lutris.Options{DBPath: makePGA(t)})
	if err != nil { t.Fatal(err) }
	games, err := src.List(context.Background())
	// expect len 2; Hades PlaytimeSec==3.5*3600? NO — decide unit below.
}
```

**Unit decision (verify first!):** before writing assertions, run
`sqlite3 ~/.local/share/lutris/pga.db "select name,playtime from games limit 5"` and compare against real library totals to determine if playtime is hours (Lutris UI shows hours). Write `const playtimeColumnIsHours = true/false` in lutris.go accordingly and normalize `PlaytimeSec` to **seconds** internally. Record the observed value in the test as a comment.

**Step 2:** run → FAIL (package missing).

**Step 3:** Implement `lutris.go`: `Options{DBPath string}` (default `filepath.Join(os.UserHomeDir(), ".local/share/lutris/pga.db")`), `New(Options)` opens via `sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(1000)")`, pings. `List` selects the needed columns, `installed=1`, maps rows to `source.Game{Source:"lutris", ID: strconv.FormatInt(id)}`; NULL lastplayed → zero time. Other interface methods return `source.ErrNotImplemented` for now (added to source.go in this task).

**Step 4:** run → PASS. **Step 5:** commit `games: lutris List via modernc sqlite (real pga schema)`.

---

### Task 4: coverart path resolution in List

**Files:**
- Modify: `~/sysc-plugins/plugins/games/source/lutris/lutris.go`
- Test: `~/sysc-plugins/plugins/games/source/lutris/lutris_test.go`

**Step 1:** failing test: build fixture dir `<root>/pga.db` + `<root>/coverart/hades.jpg` (touch file); `New(Options{DBPath:..., LutrisRoot: root})`; assert `games[0].CoverPath == <root>/coverart/hades.jpg`, second game `""`.

**Step 2:** FAIL. **Step 3:** add `LutrisRoot` option (default `~/.local/share/lutris`); after List rows, stat `coverart/<slug>.jpg` then `.png`; set CoverPath. Also add `BannerPath()` unexported helper reading `banners/<slug>.png` (used by covers later — or skip until Task 12; **skip it, YAGNI**).

**Step 4:** PASS. **Step 5:** commit `games: resolve local coverart during List`.

---

### Task 5: Launch via xdg-open (injectable)

**Files:**
- Modify: `~/sysc-plugins/plugins/games/source/lutris/lutris.go`
- Test: `~/sysc-plugins/plugins/games/source/lutris/lutris_test.go`

**Step 1:** failing test with `Options{LookPath: ..., Command: func(name string, args ...string) *exec.Cmd {...}}` capture: assert `Launch(Game{ID:"7"})` runs `xdg-open ["lutris:rungameid/7"]`. Easiest real seam: `Options.Runner func(ctx context.Context, name string, args ...string) error` (default `exec.CommandContext(...).Run`); test injects recorder.

**Step 2:** FAIL. **Step 3:** implement `Launch` calling `s.run(ctx, "xdg-open", "lutris:rungameid/"+g.ID)`; error wraps. **Step 4:** PASS. **Step 5:** commit `games: lutris launch via xdg-open (injectable runner)`.

---

### Task 6: running detection (/proc scan)

**Files:**
- Create: `~/sysc-plugins/plugins/games/running/running.go`
- Test: `~/sysc-plugins/plugins/games/running/running_test.go`

**Step 1:** failing tests over a fake proc root:

```go
// helper writes <root>/<pid>/cmdline (NUL-separated) and <root>/<pid>/stat
// proc 1234: "/Games/Hades/start.sh\0" → matches game dir /Games/Hades
// proc 1235: "/usr/bin/foo\0" → no match
func TestScanMatchesByDirectory(t *testing.T) { ... }
func TestScanGroupLeaderIsProcPID(t *testing.T) { // Start() == pid for /proc hits
	got, err := Scan("/games-list", fakeRoot) // expect {"/games-list/Hades": {PID:1234, Start: <mtime of 1234 dir>}}
}
```

Also: unreadable dirs skipped; no match → empty map.

**Step 2:** FAIL. **Step 3:** implement `running.go`:

```go
type Match struct{ PID int; Start time.Time }
// Scan returns gameDir -> match for any live process whose cmdline (or its
// environment) contains gameDir. Group leader == pid (kill(-pid) works).
// ponytail: cmdline-substring match; per-runner probes if false negatives appear.
func Scan(gameDirs []string, procRoot string) (map[string]Match, error)
```

Implementation: read dir entries of procRoot (numeric only), read `<pid>/cmdline`, replace NULs with space, substring test against each gameDir (skip empty dirs). `Start` = os.Stat(`<procRoot>/<pid>`).ModTime() (procfs dir mtime = process start; on fake roots = mkdir time — fine for tests). Return first match per gameDir.

**Step 4:** PASS. **Step 5:** commit `games: proc-based running detection`.

---

### Task 7: Stop (SIGTERM → SIGKILL)

**Files:**
- Create: `~/sysc-plugins/plugins/games/running/stop.go`
- Test: `~/sysc-plugins/plugins/games/running/stop_test.go`

**Step 1:** failing test: spawn `exec.Command("sleep", "60")` with `SysProcAttr: &syscall.SysProcAttr{Setpgid: true}`; `StopWith(pid, killer)` where killer=syscall.Kill wrapper; after call, `syscall.Kill(-pid, 0)` → ESRCH (process gone, SIGTERM handled by sleep). Second test: killer records calls; force path invoked with SIGKILL after grace when process refuses to die (use `trap '' TERM; sleep 60` via sh -c to ignore TERM; assert SIGKILL arrives). Keep grace param `stopGrace time.Duration` (tests pass 50ms).

**Step 2:** FAIL. **Step 3:**

```go
func Stop(pid int, grace time.Duration) error {
	// group kill so wine proton prefixes die too
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil { return err }
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pid, 0) != nil { return nil } // graceful
		time.Sleep(100 * time.Millisecond)
	}
	return syscall.Kill(-pid, syscall.SIGKILL) // forced
}
```

**Step 4:** PASS. **Step 5:** commit `games: group-stop with SIGTERM then SIGKILL`.

---

### Task 8: lutris Stop + Running wiring

**Files:**
- Modify: `~/sysc-plugins/plugins/games/source/lutris/lutris.go`
- Test: `~/sysc-plugins/plugins/games/source/lutris/lutris_test.go`

**Step 1:** failing test: `Options{ProcRoot: fake}`; fixture game with Directory `/Games/Hades`; fake proc dir matching → `Running()` returns `{"7": startTime}`; `Stop(Game{ID:"7"})` calls injectable `StopFn func(pid, grace)` (default `running.Stop`) with the scanned pid.

**Step 2:** FAIL. **Step 3:** implement: `Running` = List(installed) → dirs → `running.Scan(dirs, s.procRoot)` → key by game ID. `Stop` = scan single game dir → pid → `s.stopFn(pid, 5*time.Second)`; unknown game / not running → error.

**Step 4:** PASS. **Step 5:** commit `games: lutris Running/Stop wired to proc scan`.

---

### Task 9: Sections (platforms)

**Files:** Modify `lutris.go` + test. **Step 1:** failing test asserts `Sections` returns distinct non-null `platform` values sorted. **Step 2:** FAIL. **Step 3:** implement `select distinct platform ... where platform is not null order by 1`. **Step 4:** PASS. **Step 5:** commit.

---

### Task 10: store (prefs + sessions JSON)

**Files:**
- Create: `~/sysc-plugins/plugins/games/store/store.go`
- Test: `~/sysc-plugins/plugins/games/store/store_test.go`

**Step 1:** failing tests: `Prefs{SortMode string; View string; Favorites map[string]bool; Hidden map[string]bool}` + `Sessions map[string][]Session{gameID: {{Start,End time.Time}}}`; `MarshalPrefs/UnmarshalPrefs` round-trip; `NormalizeSessions` caps per-game history to 30 entries and drops in-progress ones on finalization; `DailyMinutes(sessions, now)` returns last 14 days []float64 (minutes played per day) for KindGraph.

**Step 2:** FAIL. **Step 3:** implement with `encoding/json` only (no schema machinery). Session open/close helpers: `Start(log, id, t)`, `End(log, id, t)`.

**Step 4:** PASS. **Step 5:** commit `games: prefs + session log store`.

---

### Task 11: covers — generated initials tile (blank cards impossible)

**Files:**
- Create: `~/sysc-plugins/plugins/games/covers/covers.go`
- Test: `~/sysc-plugins/plugins/games/covers/covers_test.go`

**Step 1:** failing test: `GenerateTile("Hades", outPath)` → file exists, decodes via `png.Decode` to 300x400-ish dims, hash-stable for same name+seed (deterministic: color from FNV hash of name).

**Step 2:** FAIL. **Step 3:** implement: stdlib `image`, `image/draw`, `golang.org/x/image/font/basicfont` (already in go.mod x/image — if not, this task adds it), `hash/fnv` picks bg color from small palette; draws 1-2 letter initials centered (basicfont is tiny — scale via drawing at 1/4 size then NearestNeighbor upscale, `// ponytail: crude but zero-font-file`). Cache path: `os.UserCacheDir()/sysc-games/<slug>.png`. `Resolve(g Game, cacheDir string) string`: CoverPath if file exists, else cached tile, else generate tile, return path (never "").

**Step 4:** PASS. **Step 5:** commit `games: generated initials cover tiles + resolve chain`.

---

### Task 12: SteamGridDB fetch (settings-gated)

**Files:** Create `covers/steamgrid.go`; test `covers/steamgrid_test.go`.

**Step 1:** failing test with `httptest.Server`: fake returns JSON `{"data":[{"image":"http://.../grid.jpg"}]}`; second handler serves image bytes; `Fetch(slug, apiKey, destDir, httpDoer)` writes dest and returns path; wrong api key (401) → "" no error spam; empty data → "".

**Step 2:** FAIL. **Step 3:** implement minimal: search endpoint `https://www.steamgriddb.com/api/v2/search/autocomplete/<slug>` → first game id → `/grids` → first `url` → download. All through injected `*http.Client`-like doer func. Cache file check first (already downloaded → skip network).

**Step 4:** PASS. **Step 5:** commit `games: SteamGridDB covers when key set`.

---

### Task 13: panel tree builder — pure function + snapshot test

**Files:**
- Create: `~/sysc-plugins/plugins/games/panel/view.go`
- Test: `~/sysc-plugins/plugins/games/panel/view_test.go`

Pattern: copy `~/sysc-shell/plugins/reference/weather/view_test.go` idiom — build a `panel.State` struct, call pure `BuildTree(state) *v1.Node`, assert on node IDs/kinds/text/events walking children.

**Step 1:** failing tests covering: (a) root KindColumn with header row (KindSegmented 4 options, Selected reflects state.Section), search KindTextInput `ID:"search-input"` Events [change,submit], KindList `ID:"game-list"` with 2-cards-per-row rows of `card-<id>` (KindImage cover via resolved path, star if favorite, runner chip text), (b) filter: query "had" keeps Hades only, (c) section favorites/hidden/playing subsets, (d) selected card `Selected:true` + detail column `ID:"detail"` with hero image, name+year text, playtime line, KindGraph `ID:"session-graph"` Values from DailyMinutes, buttons `launch-<id>`, `more-<id>`, (e) empty library → subtle text node, never ToneError.

**Step 2:** FAIL. **Step 3:** implement `panel.State{Games []source.Game; Section, Query string; Sel string; Prefs store.Prefs; Running map[string]time.Time; Sessions ...; Err error}` + `BuildTree`. Use `Key:` fields per design for replacement identity. Set `v1.Node` fields only through literal constructors — no helpers until duplication hurts.

**Step 4:** PASS. **Step 5:** commit `games: pure panel tree builder`.

---

### Task 14: bar pill tree builder

**Files:** Create `bar/view.go`, `bar/view_test.go`.

**Step 1:** failing tests: idle → single node keyed `"bar-pill"` KindButton icon "🎮" (or KindIcon+button row), Tone Subtle when no library; one running → cover KindImage ImageSize 18 + name + elapsed `42m`/`1h02m` from start time at 60s granularity (`Elapsed(start, now)` pure helper); 2+ → first name + " +N". Test with fixed `now` param (pure).

**Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS. **Step 5:** commit `games: bar pill states`.

---

### Task 15: main loop wiring — open panel, render, events

**Files:** Modify `cmd/sysc-plugin-games/main.go`.

**Step 1:** unit-test the handler function in isolation (`session.handleEvent(v) (actions []string)` style seam like world-clock tests if any; else keep manual smoke in Step 4). Handlers: ViewOpen panel → Snapshot full tree; ViewOpen bar → pill; InputEvent: stale revision ignored; segmented node IDs `section-<name>` → switch section; `search-input` change/submit → set Query; `card-<id>` activate → select (+detail patch); `launch-<id>` activate → Task 16 machine; pointer secondary on card → action list (M3 adds actions, now flip stub); bar pill activate → open panel.

**Step 2:** FAIL (if test seam built) else compile. **Step 3:** implement event dispatch + `snapshotAll` using BuildTree; poll goroutine: `time.Ticker` 2s while panel open, 5s when closed & any launching/running, stopped otherwise (world-clock wallCheck pattern); settings applied on SettingsChanged.

**Step 4:** manual smoke: build binary, run under `script` or with host if available; at minimum `go test ./... && go vet ./...` + a fake-host integration test: spawn our runPlugin with io.Pipes, send HostHello/ViewOpen, assert a ViewSnapshot arrives with root KindColumn (test the full client path — one such test, cheap, decisive).

**Step 5:** commit `games: render panel + bar in plugin loop`.

---

### Task 16: launch state machine

**Files:** Create `panel/launch.go` + test; wire in main.

**Step 1:** failing tests (pure): `Machine.Transition(now)`: `Idle + LaunchRequested → Launching` (starts t0); `Launching + RunningSeen → Running`; `Launching + t0>15s + not seen → Failed`; `Running + Gone → Idle + CompletedSession(start,end)`. No wall-clock in functions — `now`/`t0` params.

**Step 2:** FAIL. **Step 3:** implement map[string]*LaunchState in main loop, driven by poll ticks: while Launching poll /proc at 1s; Failed → card/detail Tone Error + notify (host `notify` call) once; Running→Idle transition finalizes session log (store.Sessions End) and persists via state.set.

**Step 4:** PASS. **Step 5:** commit `games: launch state machine + session finalization`.

---

### Task 17: process actions + switcher

**Files:** panel/view.go (More/flip column: Stop when running, Configure `lutris:showconfig/<id>` via open-url?? — use `xdg-open` same seam, Open folder → `open-url` file:// dir or xdg-open dir, Favorite toggle, Hide toggle, Remove → `xdg-open lutris:uninstall/<id>`), main.go handlers; bar switcher (secondary pointer on pill while running → list of running games each with stop button node `bar-stop-<id>`).

**Step 1:** tests first per existing pattern (tree contains `stop-<id>` iff running; prefs flip nodes selected state; switcher list from Running map). **Step 2:** FAIL. **Step 3:** implement handlers mutating store + calling source.Stop/Launch; persist prefs via state.set after mutation (loaded-guard!). **Step 4:** PASS. **Step 5:** commit `games: process actions and bar switcher`.

---

### Task 18: state persistence round-trip

**Files:** main.go (boot: `state.get prefs|sessions` before first snapshot; `loaded` flag; save on change; restore sessions on panel open per world-clock's "ensureLoaded before save" idiom).

**Step 1:** fake-host pipe test: after ViewOpen, plugin issues state.get calls (assert HostCall kinds seen), answers arrive → snapshot reflects restored favorites. **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS. **Step 5:** commit `games: host-state persistence`.

---

### Task 19: real-library smoke + settings gating + polish

**Step 1:** run binary against real `~/.local/share/lutris/pga.db` manually (host or fake stdio script): verify 20 games list, playtime matches Lutris UI, covers resolve, `poll_running off` disables scan (unit-testable: ticker count). **Step 2:** fix whatever breaks (likely: playtime unit, dates parsing `2026-09-20 18:00:00` layout in sqlite driver — scan as string, parse `time.Parse("2006-01-02 15:04:05", s)`). **Step 3:** validate manifest, run full suite. **Step 4:** commit `games: real-library fixes`.

---

## Done-when

`go test ./...` green; plugin boots, bar shows running pill within 5s of launching a game via Lutris UI itself (detection works without our launch), panel matches approved design, reference-plugin roadmap items (badges, now-playing, kill, switcher, SteamGridDB, blank-proof art) all shipped.
