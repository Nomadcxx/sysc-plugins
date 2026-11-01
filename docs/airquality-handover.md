# Air Quality & Pollen handover

Updated 2026-11-02. This replaces the 2026-11-01 handover, which is in git
history. The audit it commissioned is in
[`airquality-audit-2026-11-01/README.md`](airquality-audit-2026-11-01/README.md).

## State at handover

| Item | Where | State |
| --- | --- | --- |
| Audit fixes, plugin 0.1.1 | sysc-plugins #307 | Merged (`d02fc57`). Beads `sysc-plugins-38` and `-39` closed. |
| Shell location protocol, minor 26 | sysc-shell [#595](https://github.com/Nomadcxx/sysc-shell/pull/595) | Merged (`28f2b13a`). The owner rebased it and added `994e4318` "order location updates" before merging. |
| Shell location, European and Google pollen, plugin 0.2.0 | sysc-plugins [#309](https://github.com/Nomadcxx/sysc-plugins/pull/309) | Merged (`11ba6e3`), with the owner's review fixes `fdd2ee0` (off-loop location poll, transient Google rows) and `bd1f626` (pollen source coverage). |
| Repin and this handover | branch `fix/airquality-repin-handover` | #309 merged with `go.mod` pinning the pre-rebase #595 commit `9a9bcb37`, which is no longer on any sysc-shell branch. The follow-up repins to the #595 merge commit, `v0.2.4-0.20261101121114-28f2b13a7997`, and lands this file. Between the two commits `plugin/v1` differs only in a doc comment. |

To repin again, resolve a commit's pseudo-version with
`GOFLAGS=-mod=mod GOPROXY=direct go list -m -json github.com/Nomadcxx/sysc-shell@<sha>`,
edit the `require` line, delete the old `go.sum` lines, and build with
`GOFLAGS=-mod=mod` to refresh `go.sum`. Do not run `go get` or `go mod tidy`
in this repository. Pin only commits reachable from sysc-shell `main` or a tag,
because branch commits disappear when a branch is rebased.

Merges were blocked for the session's agent by the auto-mode permission check
("merge without review"); the owner merges.

## What the merged PRs do

### sysc-shell #595: protocol minor 26

- `plugin/v1`: `CallLocationGet` (`location.get`), `LocationGetParams{}`,
  `Location{configured, pending, latitude, longitude, label}`,
  `LocationChanged` (`location.changed`, registered `ToPlugin` and in the
  encoder's type switch), `LocationMinor = 26`, `ProtocolMinor = 26`.
- `internal/plugin`: the new `CapLocation` capability (`"location"`), which a
  manifest may declare only at minor 26 or later. The dispatcher answers
  `location.get` only when the capability is granted; `CallEnv.Location`
  provides the answer.
- `internal/services/weather.go`: `Weather.Place(ctx)` returns the
  configured location. A configured city is geocoded on demand through a
  `geocode` helper that `fetch` now shares, so the cache is shared and no
  weather lease is needed. A failed geocode is reported as `Pending`.
- `internal/shell/pluginlocation.go`: `pluginHost.location` records what each
  plugin was last told. `locationMaybeChanged` runs off-lock after every
  config reload (`registry.go`, after `SetCity`). It pushes `location.changed`
  only to running plugins that are granted the capability, negotiated minor 26
  or later, and were last told a different place. The owner's `994e4318`
  numbers each reload (`nextRevision`) and sends through `sendIfCurrent`, so
  a slow lookup from an older reload can no longer overwrite a newer place.

### sysc-plugins #309: plugin 0.2.0

- **Shell location** (`cmd/sysc-plugin-airquality/main.go`):
  `session.queryLocation` runs off the event loop at start and on every
  refresh tick, because the host may geocode before answering and a city that
  resolves later triggers no push. `locationRevision` discards a poll answer
  that a newer `*v1.LocationChanged` push has superseded. The first refresh
  waits for the first answer, and later refreshes use the last known place. `session.sources()` uses the shell's place when
  `use_shell_location` is on (the default) and the place is configured and not
  pending; otherwise it uses the plugin's own coordinates. `sourcesChanged`
  centralises relocation, alert re-arming and refresh for settings and
  location changes. The shell's label names the place unless AirHealth gives a
  suburb.
- **European pollen** (`plugins/airquality/pollen.go`): Open-Meteo's hourly
  CAMS pollen comes in the existing air-quality request. Each day's value is
  the mean of its local hours, placed on MeteoSwiss's per-species load classes
  (None, Low, Moderate, High, Very High). Olive uses birch's classes, because
  MeteoSwiss publishes none for olive. Outside Europe the values are null and
  the plugin shows nothing from this source.
- **Google Pollen** (`plugins/airquality/google.go`): Universal Pollen Index
  0–5 from `forecast:lookup`, with `days=4` and `plantsDescription=false`. The
  key comes from the masked `google_pollen_key` setting (`"secret": true`,
  already supported by the shell, which stores config at 0600). It travels in
  the `X-Goog-Api-Key` header, never the URL. An HTTP 400 whose body says
  "not supported" is treated as no coverage. Google's terms prohibit caching or
  storage, so the session draws Google rows from a per-run `live` report. The
  `Cache` and persisted state never hold them: `WithoutGoogle` strips them in
  `EncodeReport`, `Cache.Keep` and `carryOver`, and keeps source-neutral
  forecast days. The panel shows the required credit, "Source: Includes pollen
  data from Google."
- **Pollen precedence** (`coordinator.go` `Fetch`): AirHealth in Australia,
  then Open-Meteo for Europe, then Google when earlier sources have no pollen
  and a key is set. `Report.PollenNote`
  explains a gap outside AirHealth's coverage. Without a key it reads "Pollen
  here needs a Google Pollen API key; add one in this plugin's settings."
- **EPA Victoria:** on by default, but queried only inside a coarse Victoria
  outline (`victoria` polygon, `EPAVictoriaMayCoverCoordinates`). The outline
  errs outside the border, so Albury still gets Wodonga's station.
- **Manifest:** protocol minor 26, the `location` capability, the two new
  settings and version 0.2.0. `tools/validate-manifests` learned `location`
  (minor 26), `CallLocationGet` and `secret` settings.
- **Docs:** the plugin README, a "The shell's location" section in
  `docs/writing-plugins.md`, and a resolution note in the audit report.

## Verification done

- **Shell:** `go test -count=1 -p 2 ./internal/shell ./internal/plugin
  ./internal/services ./plugin/... ./internal/config` passed, plus all five
  CI checks.
- **Plugins:** race tests passed for both AirQuality packages (`cmd` looped
  3×), along with `GOMAXPROCS=4 go test -p 2 ./...`, `go vet`, `staticcheck`,
  manifest, thumbnail and catalog validation, and all three CI checks.
- **Live fetches (2026-11-01/02):**
  - Melbourne and Sydney use AirHealth; Sydney no longer queries EPA.
  - London gets Open-Meteo pollen: all None, the real November value.
  - Madrid gets Open-Meteo pollen: mugwort Moderate.
  - New York shows air quality and UV, plus the key note.
- **Renders** through the real shell painter (`sysc-panel-preview`): the
  Europe, US-with-Google (fixture) and US-without-key panels and their
  tooltips fit.

## Outstanding work, in order

1. **Merge the repin follow-up** (`fix/airquality-repin-handover`). Until it
   lands, `main` builds only while the Go module proxy still serves the
   orphaned `9a9bcb37`, and `GOPROXY=direct` builds fail.
2. **Deploy and test in a live shell.** Follow the deploy rule: build the
   shell only from an `origin/main` worktree with `scripts/deploy --host
   desktop|laptop`. Install the plugin binary as in the plugin deploy notes.
   Then check:
   - The plugin's declared capabilities changed (`location` added). Expect
     the shell to withhold the grants as "changed" until the user approves
     the plugin again in Settings > Plugins. Approve it, and check that the
     approval screen lists `location`. No capability has its own consent
     wording in the shell, so the screen shows the capability name.
   - With Settings > Weather set to a city, the panel names that city and
     reads there. Change the city: the panel should move within seconds,
     driven by `location.changed`, without a restart.
   - With the weather location cleared, or "Use the shell's weather location"
     off, the plugin falls back to its own latitude and longitude.
   - The "Google Pollen API key" field draws masked.
3. **Test Google Pollen with a real key.** It is untested live, because no key
   was available. The parser follows Google's documented response, but two
   things are unconfirmed: the HTTP 400 "not supported" wording for uncovered
   regions, and how an invalid key is reported (expected HTTP 403 → "request
   refused (HTTP 403)"). Check the US (e.g. 40.71, −74.01), a covered
   non-US country, an uncovered region, and a bad key. The key must never
   appear in the panel or logs.
4. **Optional: tag the shell** (v0.2.4) and repin
   `github.com/Nomadcxx/sysc-shell` to the tag in a small follow-up. The
   pseudo-version pin of the #595 merge commit is valid without it.
5. **Optional: switch EPA to its official API.** The keyless ArcGIS mirror
   has been frozen since 2025-12-05. Its readings are rejected after three
   hours, so Open-Meteo supplies Victorian air quality whenever AirHealth has
   none. Secret settings now make the official EPA Environment Monitoring API
   (free key) practical; see `docs/airquality-backend-research.md`.
6. **Release.** The next sysc-plugins release adds AirQuality to
   `catalog.json`. Because the manifest declares minor 26, shells older than
   #595 are offered an older release or refuse 0.2.0.

## Known limits

- `location.changed` fires after a config reload. A city that resolves later
  is picked up at the plugin's next refresh tick (default 15 minutes) or
  Retry.
- European pollen thresholds are MeteoSwiss's. Other national schemes differ.
  Off-season values are 0 and show as "None".
- Google rows are live-only by Google's terms, so a failed refresh removes
  them. The panel then shows the error card and any keyless data.
- Notifications are sysc-shell desktop notifications while the plugin runs,
  not remote push.

## Workspace notes

- **Worktrees from this work** (remove once the repin follow-up merges):
  - sysc-plugins `.worktrees/airquality-audit-20261101` (#307, merged)
  - `.worktrees/airquality-location-pollen` (#309, merged)
  - `.worktrees/airquality-handover-20261101` (old handover)
  - `.worktrees/airquality-repin` (the repin follow-up)
  - sysc-shell `~/worktrees/sysc-shell-aq-audit` (#595, merged; it still
    has the pre-rebase `9a9bcb37` checked out)
- **Commit messages:** the commit-msg hooks in both repositories reject `bot`,
  `agent`, `llm` and `claude` as substrings (watch "both"). They also reject
  Co-Authored-By trailers.
- **Beads hook:** fresh worktrees have no `.beads/beads.db`, so commit with
  `BEADS_DB` pointing at a copy whose `dirty_issues` is emptied.
- **Beads truncation:** in this repository, `bd update` and `bd close` truncate
  `.beads/issues.jsonl` (bd 0.17.7). After any bd write, check that `wc -l`
  equals the `bd stats` total. If not, run
  `sqlite3 .beads/beads.db "DELETE FROM export_hashes;" && bd export -o .beads/issues.jsonl`.
  The primary checkout's tracker file also carries other sessions'
  uncommitted changes; leave them alone.
- **Go commands:** a hook blocks uncapped `./...` and `-race` with `./...`.
  Use `-p 2` and race-test one package at a time.
- **Renders:** build `sysc-panel-preview` from a current shell worktree with
  `GOWORK=off go build ./cmd/sysc-panel-preview`. It accepts only column roots,
  so wrap bar rows in a column to preview them.
- **Temp space:** `/tmp` has a per-user quota, and other sessions' files
  fill it. When a build fails with "disk quota exceeded", set
  `GOTMPDIR=~/.cache/go-tmp`. Keep large binaries and renders out of `/tmp`.
