# Air Quality & Pollen

Air Quality & Pollen shows air quality and UV data for configured coordinates
worldwide, plus Australian pollen and thunderstorm asthma where the regional
AirHealth service returns a location. The default coordinates are in
Melbourne; set decimal latitude and longitude in settings for another location.

## Sources

Open-Meteo is the global model source. AirHealth and EPA Victoria are regional
sources and are used only where their coordinates are covered. All three
settings default to on; turn off a source in settings to exclude its readings.

**EPA Victoria (default, on).** The plugin reads air quality from a public
ArcGIS feature service that mirrors EPA Victoria's monitoring network. It needs
no key or registration. The plugin picks the nearest station with a reading and
prefers a Standard monitoring station over an indicative sensor within one
kilometre. EPA publishes this data under CC BY 4.0; the panel credits EPA.

The backend research documents EPA's official Environment Monitoring API. It
needs a free key. The shell has no protected plugin setting for credentials, so
the plugin uses the keyless mirror.

**Open-Meteo (default, on).** Supplies the global fallback air-quality index,
particulate and gaseous pollutant readings, and UV. Its CAMS ENSEMBLE model
needs no key for non-commercial use. Where AirHealth or a nearby EPA Victoria
station supplies a local air-quality category, that category takes precedence;
Open-Meteo still supplies UV and pollutant detail. Its particulate fields are
a 45 km model grid. Its fallback category uses the US AQI scale, shown with its
scale label.

The displayed UV value is today's forecast peak, and the panel and tooltip label
it as today's peak so it is not mistaken for the current UV level.

Open-Meteo exposes pollen variables only for Europe, and this plugin
does not currently request them. Outside locations returned by AirHealth,
pollen and thunderstorm-asthma data are unavailable; global air quality and UV
remain available through Open-Meteo.

**AirHealth pollen apps (on by default).** Grass pollen and allergen readings
come from the free app service behind the regional pollen sites. The plugin uses
the keyless app endpoints and their free detail route; it does not use premium
features, an API key, account cookies or a separate city scraper. Configured
latitude and longitude select the service location, which then routes to its
SAL dashboard. The plugin enables AirHealth by default for Australian
coordinates. Turn it off in settings to hide its pollen readings. Live checks
covered these sites:

- [Melbourne Pollen](https://www.melbournepollen.com.au/)
- [Canberra Pollen](https://www.canberrapollen.com.au/)
- [Sydney Pollen](https://www.sydneypollen.com.au/)
- [Perth Pollen](https://www.perthpollen.com.au/)

The panel credits the providers with: "Data provided in collaboration with the
University of Tasmania and University of Melbourne".

## Coverage

| Measure | Source | Where |
| --- | --- | --- |
| Air quality category | AirHealth, EPA Victoria, Open-Meteo | AirHealth returned Australian locations; EPA near Victorian stations; Open-Meteo global model fallback |
| Pollutant readings | Open-Meteo | Global model coverage |
| UV index | Open-Meteo | Global model coverage |
| Grass pollen | AirHealth regional pollen apps | Melbourne, Canberra, Sydney, Perth and other returned Australian locations |
| Other allergens | AirHealth regional pollen apps | Returned Australian locations |
| Thunderstorm asthma | AirHealth | Seasonal Australian forecast, when published for the location |

Thunderstorm asthma is seasonal. Out of season the measure is absent rather
than "Low", and the panel hides it instead of implying monitoring is running.

Melbourne is the default location. Set latitude and longitude in settings to
select the AirHealth location and the global model grid. The `Station` setting
only overrides the nearest EPA Victoria site.

## Breakdowns

The headline row for a measure is a summary; the useful detail is underneath it.
Where a source publishes constituent readings, the panel shows them as a strip
under the worst measure only:

```
Air quality
Fair · 19.9 µg/m³
PM2.5 19.9 · PM10 7.6 · O3 53 · NO2 4.4
```

The panel expands the worst measure. Showing every breakdown would push the
forecast and attribution below the 600px panel. The expanded detail belongs to
the reading most likely to need attention.

The headline figure leads the strip. When EPA supplies the headline and
Open-Meteo supplies neighbouring pollutants, the row puts EPA's value first.
A live test caught a case where the panel showed 19.9 µg/m³ above a PM2.5
reading of 4.8.

For allergens the same mechanism lists the species: grass, birch, cypress,
eucalypts, olive, plane and plantain, plus Alternaria spores.

## Severity

Every measure is reduced to one of three severities: good, moderate or high.
This exists because the scales are not comparable. Grass pollen runs Low to
Extreme, air quality runs Very Good to Extremely Poor and is inverted, and
thunderstorm asthma runs Low to High. The mapping comes from the published
breakpoints in the service's own metric tree, and from each breakpoint's
`warn_level` rather than its position, so air quality does not render backwards.

## Notifications

Two notifications are available and both default to on:

- **Air quality, pollen or UV high:** Fires when any measure reaches high
  severity, once per transition.
- **Thunderstorm asthma:** Fires at moderate or above and independently of the
  other measures, because it must
  not be suppressed by grass pollen ranking higher.

These use sysc-shell's desktop notification service while the plugin service is
running; they are not remote push notifications. A persistent reading fires
once per state transition, rather than on every refresh. A fresh warning from
one source can still notify when another source fails during the same refresh.

## Panel and bar

The bar shows the worst current measure by default. The `Bar shows` setting can
pin it to grass, air quality or thunderstorm asthma, or show the icon only. The
widget drops its text on a narrow bar.

The panel leads with a half radial dial for the worst measure that has a numeric
scale, then lists each measure with its level and any numeric reading, then up
to three forecast days. UV readings are identified as today's peak. While a
fetch is failing the last known reading is kept and marked stale, the error
names which source failed, and a Retry button runs the existing refresh action.
The thunderstorm asthma alert card includes the source's explanatory text.

The dial paints four to six severity zones in green, amber and red, with a
needle at the reading and the figure and category written under the arc. The
current renderer does not print each zone's label or threshold value. The number
and current severity remain visible in text, but the thresholds are conveyed by
colour alone. Labels on the arc need shell rendering support; a separate legend
could use plugin text but would take panel space.

Zones are equal slices of the arc, not a linear map of the concentration. A
linear PM2.5 dial would spend five percent of the arc on "Very Good" and two
thirds on "Very Poor", so an unremarkable reading would sit inside a dial that
looked almost entirely red. The zones follow the published categories and the
needle interpolates within the zone it earned, so the angle and the number agree.

Thunderstorm asthma never gets a dial. Its forecast is ordinal rather than a
concentration, so an arc would imply a precision the source does not have; it
appears as the alert card instead. UV gets a dial whose zones are uneven in
purpose: the step from 2 to 3 changes what a person should do, the step from 9
to 10 does not.

AirHealth reports grass pollen as a category without a concentration. On
protocol minor 25 or newer, the dial places the needle at the midpoint of that
category band and says "category position" under the dial. This represents the
reported category, not an invented pollen count. A minor 24 host keeps the
numeric-measure dial and shows grass in the rows.

Half dials and severity bands need sysc-shell protocol minor 24; band labels
and category-only grass positioning need minor 25. Against an older shell the
panel falls back to the row list, and a narrow bar drops to its icon.

Until a shell release carries minor 25, this plugin needs a `replace` in the
repository `go.mod` pointing at a shell checkout that includes it:

```
replace github.com/Nomadcxx/sysc-shell => /path/to/sysc-shell
```

The half gauge is on branch `feat/gauge-half-severity-bands`; its band labels
extend that work at protocol minor 25. Drop the replace once that is released
and the dependency pin is bumped.

## Settings

| Setting | Default | Notes |
| --- | --- | --- |
| AirHealth pollen apps | on | Australian coverage; skipped elsewhere; opt-out in settings |
| EPA Victoria | on | Near Victorian stations; skipped elsewhere |
| Open-Meteo | on | Global model AQI, UV and pollutants |
| Latitude | -37.8136 | Melbourne; accepts -90 to 90 |
| Longitude | 144.9631 | Melbourne; accepts -180 to 180 |
| Station | empty | Site name override; empty means nearest |
| Refresh | 15 min | 5–180 |
| Search radius | 15 km | 1–100 |
| Notify on high | on | Air quality, pollen and UV |
| Notify thunderstorm asthma | on | |

## Development

```sh
go test ./plugins/airquality/ ./cmd/sysc-plugin-airquality/
make build PLUGINS=airquality
```

The backend research, including the exact endpoints, payload shapes and the
lat/lon ordering trap in the location endpoint, is recorded in
[`docs/airquality-backend-research.md`](../../docs/airquality-backend-research.md).
