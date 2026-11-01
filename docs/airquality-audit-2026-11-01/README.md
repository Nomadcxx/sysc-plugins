# Air Quality & Pollen audit, 2026-11-01

This report covers Beads tasks `sysc-plugins-38` (UI/UX Pro audit) and
`sysc-plugins-39` (end-to-end plugin audit). It audits plugin 0.1.0 as merged
in PR #305 against `docs/airquality-handover.md` and
`docs/airquality-backend-research.md`. The fixes ship as 0.1.1.

## Method

- Code trace of `cmd/sysc-plugin-airquality` and `plugins/airquality`, plus
  the sysc-shell host paths it depends on (`internal/shell/pluginhost.go`,
  `internal/plugin/landlock_linux.go`, `plugin/v1`), at shell `105e8b11`.
- Live provider checks (2026-11-01) for Melbourne, Sydney, Canberra, Perth,
  Hobart, Darwin, Brisbane and Geelong, and for London, Tokyo, Auckland and
  Delhi.
- Regression tests that reproduce each fixed behavioural bug against the 0.1.0
  code before the fix.
- Renders through the shell's real layout and painter (`sysc-panel-preview`
  built from shell `105e8b11`) for nine states: normal, high pollen with a
  thunderstorm alert, poor AQI, Tokyo, Delhi, stale, partial failure, offline
  and loading. Each state was rendered as the panel, as tooltips at 280 and
  200 px, and as bars at 240, 120 and 32 px. Panels were rendered at 150% and
  125% scale and at protocol minors 10, 24 and 25. The renders went through two
  iterations.

## Findings

Severity: **C** critical, **H** high, **M** medium, **L** low.

| ID | Sev | Area | Finding | Status |
|----|-----|------|---------|--------|
| E1 | C | Data | The keyless EPA Victoria ArcGIS mirror has not updated since 2025-12-05 (`dataLastEditDate`; all 91 stations carry that reading). The plugin never checked a station's `until`, so whenever EPA was consulted (AirHealth off, failing, or without air quality) the panel showed an 11-month-old reading as current, ahead of Open-Meteo. | Fixed: readings older than 3 h are rejected (`coordinator.go` `fetchEPA`, `EPAMaxAge`). Open-Meteo fills the row and the panel names the cause: "EPA Victoria: no current station reading (latest 5 Dec 2025)". |
| E2 | H | Refresh | The shell sends `settings.changed` on every plugin start (`pushSettings`). The handler re-armed the timer unconditionally, which replaced the 250 ms first refresh with the full interval. With unchanged settings the first reading took 15 minutes. Any bar `view.open` carrying instance settings also postponed the refresh. | Fixed: the timer is re-armed only when the interval or the request changes; instance-scoped messages never touch it. Test: `TestStartupSettingsKeepImmediateRefresh`. |
| E3 | H | Cache | A partial failure returned the cached report and discarded the fresh one. The first partial report was cached and then served on every later partial refresh, so while one provider stayed down, UV and air quality from the working providers never updated. | Fixed: `Cache.Merge`. Providers that answered replace their own rows; a failed source's rows from the same place are carried for up to 12 h (`CarryLimit`, per-row `Updated`). The header age reports the oldest row shown. Tests: `TestPartialFailureStillUpdatesWorkingSources`, `TestCacheMergeCarriesRecentRowsFromFailedSource`. |
| E4 | H | Location | After a location change, the previous city's report stayed on screen as "stale", and a partial failure at the new place kept it there. The restored report has no recorded place. The change itself showed a red "Source update failed: refreshing with updated settings" card. | Fixed: reports record their `Origin` (persisted). `Cache.Relocate` drops any other place's report, including one with no recorded origin. Alert state resets on relocation. No failure card for a settings change. Tests: `TestLocationChangeDropsOldPlace`, `TestCacheNeverMixesPlaces`. |
| E5 | M | Bar | The `show` setting is per widget instance but was stored once per plugin, so the last bar configured set every bar. | Fixed: kept per instance. Test: `TestBarShowIsPerInstance`. |
| E6 | M | AirHealth | `selectSAL` returned an empty snapshot when an SA3 response did not list the suburb, contradicting its comment. The pollen readings disappeared with no error. | Fixed: falls back to the area's first suburb. Test: `TestSelectSALFallsBackToArea`. |
| E7 | L | Tests | The harness forwarder stopped reading the plugin's output after `stop`. A snapshot written during the last refresh then blocked the plugin before it read `host.shutdown`, a flake under `-race` (2 of 8 runs). | Fixed: the forwarder drains until the plugin exits (0 of 15 runs). |
| U1 | H | Panel | The dialled measure has no row, and the breakdown strip attached only to rows, so a Very Poor or US AQI reading never named its pollutant. | Fixed: the strip sits under the dial. [poor-aqi](poor-aqi.webp) |
| U2 | H | Bar | Truncation kept the category and dropped the number: "Air US AQI unhealthy for se" at 240 px and "Air US AQI u" at 120 px. | Fixed: the figure comes first and the cut falls at a word boundary ("Air AQI 101 unhealthy (sensitive)"; "Air AQI 101" at 120 px). |
| U3 | M | Panel | The alert title was pinned to the far edge, away from its bell icon. | Fixed. [high-pollen-storm](high-pollen-storm.webp) |
| U4 | M | Panel | Moderate icons used the brand accent (blue) while the dial painted Moderate amber. UV borrowed the air-quality glyph. | Fixed: warning tone; UV uses the sun glyph. |
| U5 | M | Errors | A partial outage used the full red error container, which outweighed the readings, and the copy was raw Go errors ("dial tcp: …", "503 Service Unavailable"). | Fixed: a warning card when readings are shown and the error container only when nothing is. Messages are plain ("service unavailable (HTTP 503)", "timed out", "can't connect; check the network"). [partial](partial.webp) |
| U6 | M | Location | Outside Australia the header said "35.68, 139.65" and the missing pollen rows read as a fault. | Fixed: "35.68°N, 139.65°E", plus a note that pollen and thunderstorm asthma cover Australian locations only. [global-tokyo](global-tokyo.webp) |
| U7 | M | A11y | The gauge's accessible name was only the measure title, so a screen reader heard "Grass pollen" with no reading. | Fixed: the name carries the reading ("Grass pollen: Moderate"). |
| U8 | L | Contrast | The stale alert's detail used subtle text on a card, a pair the theme does not validate. | Fixed: normal tone; the title already says "Last alert". |
| U9 | L | Copy | Ages above two days read "72h ago". The offline tooltip matched the loading tooltip ("No reading"). | Fixed: "3d ago"; "Sources unavailable". |
| U10 | L | Settings | The latitude and longitude settings did not say they default to Melbourne or that the shell's weather location is not used. | Fixed in the manifest descriptions and the README. |
| U11 | L | Panel | When the panel scrolls, the shell's 4 px scroll thumb touches the right-pinned forecast text. | Deferred: the scroll thumb is drawn by the shell, and the uniform-padding workaround misaligns the left edge. |
| U12 | L | Gauge | The gauge's centre value is very large relative to the panel title. | Deferred: the shell sizes the gauge text, not the plugin. |

[Sizes and fallbacks](sizes-and-fallbacks.webp) shows the 125% panel, the
protocol minor 10 row fallback and the offline state.
[Tooltips](tooltips.webp) shows the tooltip at 280 and 200 px.

## Confirmed behaviour

- **Routing.** AirHealth resolved all eight Australian cities, including
  Darwin, Hobart and Brisbane, so the regional routes are not limited to the
  four named apps. London, Tokyo, Auckland and Delhi route to Open-Meteo only.
  EPA is queried only when AirHealth supplies no air quality.
- **Scales.** US AQI bands are 0–50, 51–100 and over 100 on the three-step
  axis. The UV peak comes from today's `uv_index_max` in the coordinate's
  timezone (`timezone=auto`). The PM2.5 dial uses EPA's published breakpoints.
  An unknown category fails safe to high.
- **Notifications.** A notification fires once per transition into high (or
  moderate for thunderstorm asthma) and re-arms on the drop. Carried rows never
  raise an alert, because alerts use only the rows the refresh returned. Alert
  state resets when the location changes.
- **Protocol.** The SDK negotiates up to minor 25. Hosts from minor 24 get the
  half gauge without band labels, and hosts below minor 24 get plain rows.
- **Sandbox.** The plugin's capabilities are `notifications`, `panels`,
  `settings` and `state`, and all four are used. Landlock restricts only the
  filesystem, and the plugin uses no files (state goes through host calls).
  Responses are capped at 4 MiB and each refresh at 20 s.
- **Lifecycle.** `host.shutdown` returns from the loop. The low-risk
  in-process stdin note in the handover still applies.
- **Packaging.** Catalog metadata exists, and `tools/catalog validate` passes.
  `catalog.json` gains the entry when the release workflow publishes.

## Deferred decisions

1. **Shared location (requirement gap).** The shell keeps a weather location
   (`config.Weather` latitude/longitude/city), but the plugin protocol has no
   host call or setting type that exposes it, so the plugin cannot inherit it.
   Closing the gap needs a shell protocol addition, either a `location.get`
   call or a location setting type. Until then the plugin defaults to Melbourne
   and the settings say so.
2. **EPA source.** The keyless mirror is frozen, so EPA now contributes
   nothing until it updates. EPA's official API needs a key, and the shell has
   no protected credential setting. The owner should decide whether to switch
   the EPA default off or wait for credential support. With the guard in place,
   leaving it on is accurate: users who turn AirHealth off see a warning naming
   the stale mirror.
3. **European pollen.** Open-Meteo publishes grass and other pollen for Europe.
   Adding it would be new scope (category thresholds per species), so it is
   not part of this audit.

## Verification

- `GOFLAGS=-mod=mod go test -race -count=1 ./plugins/airquality` and
  `./cmd/sysc-plugin-airquality` (the latter looped 15× with no failure)
- `GOMAXPROCS=4 go test -p 2 -count=1 ./...`, `go vet -p 2 ./...`, `staticcheck ./...`
- `go run ./tools/validate-manifests`, `go run ./tools/thumbnail -check`,
  `go run ./tools/catalog validate`
- `make capture PLUGIN=airquality` regenerated `screenshot.png` and
  `thumbnail.webp`; the capture lints clean at 460×600.
- A live fetch with AirHealth off for Melbourne returned Open-Meteo air quality
  and the dated EPA warning.
