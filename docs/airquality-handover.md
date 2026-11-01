# Air Quality & Pollen handover

Updated 2026-11-01.

Status: shell PR [#588](https://github.com/Nomadcxx/sysc-shell/pull/588) merged
to `main` at `4914480184c4a285b94940da1336d02b8df500be`. GitHub requires a PR and
five checks for `main`, so the direct push was rejected. All five checks passed;
local `main` is synced to `origin/main`. No tagged release was requested.

## User requirements and decisions

- Show air quality, UV, grass pollen, other allergens and thunderstorm asthma
  for the configured location.
- Use coordinate lookup for regional Australian pollen feeds. Live checks
  covered Melbourne, Canberra, Sydney and Perth; the route resolves other
  supported locations too.
- The AirHealth integration uses free app data and the free detail route. It
  needs no premium feature, API key, account cookie or city-specific scraper.
  Coordinates select the service location. The plugin enables AirHealth by
  default for Australian coordinates, and users can turn it off in settings.
- The shell belongs to the user. The requested half gauge uses green, amber and
  red severity zones; shell limitations are not design constraints.

## Delivered

The plugin combines AirHealth and EPA Victoria regional data with Open-Meteo's
global air-quality and UV model. World-range coordinates route regional feeds
only where they apply; Open-Meteo uses the coordinate's local timezone. The
plugin includes forecasts, breakdowns, alerts, caching, bar and panel views,
and attribution. AirHealth reports grass as a category, so protocol 25 places
the needle at the reported category's midpoint and labels it “category
position.” The gauge adds no pollen count.

AirHealth coordinates resolve to Melbourne, Canberra, Sydney, Perth and other
returned Australian locations. The panel labels UV as today's peak, shows
allergen detail and source alert text, and offers Retry after a source failure.
Non-Australian coordinates use Open-Meteo for air quality and UV; AirHealth
pollen and EPA Victoria station data stay limited to their regional coverage.
Google's higher-resolution Air Quality API was researched, but it requires a
key and billing and is not integrated; see the backend research notes.

The shell change adds half gauges and severity bands in protocol minor 24, then
bounded band labels in minor 25. Plain gauges retain the full-ring renderer.
Older hosts receive the numeric dial fallback or rows, according to their
negotiated protocol.

The review fixes also keep the last complete report stale after a partial source
failure, capture immutable settings for refreshes and ignore results from older
settings, persist dial bands and breakdowns, fail safely on an unknown category,
and preserve the AirHealth opt-out request check.

## Verification

- Shell: `go test ./plugin/... ./internal/render ./internal/theme ./internal/plugin ./internal/ui -count=1` passed after rebasing on the fetched `origin/main`.
- All five required GitHub checks passed before merge: `test`, `coverage`,
  `govulncheck`, `landlock-abi` and `verify / verify`.
- Plugin: `GOFLAGS=-mod=mod go test ./plugins/airquality ./cmd/sysc-plugin-airquality -race -count=1` and `go vet` passed.
- Manifest validation passed for all 19 plugin manifests.
- A protocol 25 live pipe check returned a half gauge for Melbourne grass, four
  labelled bands and needle position `0.625` for “High.” The panel included the
  thunderstorm alert, forecast and full attribution.
- Scoped provider tests cover Australian source routing and worldwide model
  coordinates; a notification test covers a fresh high reading during partial
  source failure.
- The UI/UX review used the weather plugin as a reference and inspected the
  rendered gauge. The earlier grass dial and threshold-label gaps are resolved.

## Plugin merge and screenshot review

Plugin PR [#305](https://github.com/Nomadcxx/sysc-plugins/pull/305) merged to
`sysc-plugins/main` at `39ed91d20ad5533621e38c17719bf74e80dbbfbd`. The actual
panel screenshot and catalog thumbnail were reviewed beside KDE Connect,
ThinkPad, Moonbit, Weather and Control Centre captures.

That pass found low contrast in the blue reading text on measure cards. The
panel, tooltip and breakdown now use the normal foreground for readings, while
the severity icons and half gauge keep their accent colors. The source credits
wrap by provider and keep their full text. The regenerated panel capture is
690×900; its reading foreground/card contrast is 8.28:1 and caption/panel
background contrast is 6.19:1. The screenshot and thumbnail are
`plugins/airquality/screenshot.png` and `plugins/airquality/thumbnail.webp`.

The plugin CI run initially failed staticcheck on a redundant wire-to-domain
struct literal and an unused UV helper. Both findings were fixed. Final PR
checks `test`, `verify / verify` and `govulncheck` passed. Repository-wide
staticcheck, AirQuality race tests, `go vet` and thumbnail validation also
passed locally.

## Commissioned audits

Work status lives in Beads. Continue from these open tasks:

- `sysc-plugins-38`: UI/UX Pro audit of the panel, bar, tooltip and settings
  using actual shell renders, representative data/error states, accessibility
  checks and comparisons with the shell Control Centre and existing plugins.
- `sysc-plugins-39`: end-to-end plugin audit covering provider routing, data
  parsing and scales, caching, refresh concurrency, notifications, protocol
  compatibility, packaging, sandboxing and lifecycle behavior.

The full scopes and acceptance criteria are in `bd show sysc-plugins-38` and
`bd show sysc-plugins-39`.

## Product limits to carry forward

- The plugin uses configured coordinates and defaults to Melbourne. It does
  not inherit a system or Control Centre location automatically. The full
  audit should confirm whether the shell exposes a supported shared location
  source.
- Open-Meteo supplies global air-quality and UV data. Google Air Quality was
  researched but needs a billed API key; the shell has no protected plugin
  setting for that key, so the plugin does not call Google.
- Warnings use sysc-shell desktop notifications while the plugin service is
  running. They are not remote push notifications.

## Remaining work and workspace notes

- Shell integration is complete. No release tag was created.
- Keep any local shell `replace` directive out of committed `go.mod` changes.
- Do not run `go mod tidy` or `go get` in the plugin workspace. Use
  `GOFLAGS=-mod=mod` for checks.
- The shell commit hook rejects commit messages containing `bot`, `agent`,
  `llm` or `claude` as substrings.
- The EPA official gateway remains deferred because plugin settings have no
  protected credential field. Keep the keyless ArcGIS mirror as the EPA source.
- A low-risk review note remains: an in-process host that leaves stdin open
  after `HostShutdown` can leave the reader goroutine blocked. The production
  plugin process exits on shutdown.
