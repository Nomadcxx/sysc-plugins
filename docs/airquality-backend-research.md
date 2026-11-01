# Air quality & pollen plugin: backend research

Status: backend runs; live AirHealth checks covered regional locations on 2026-10-31;
Google Maps Platform global coverage reviewed on 2026-11-01.
Target: a sysc-shell plugin for worldwide air quality and UV, with Australian
pollen and thunderstorm-asthma data where regional feeds cover the location.

## Current implementation

**Air quality:** AirHealth's regional category where returned, EPA Victoria
near its monitoring network, then Open-Meteo's global CAMS model. Open-Meteo
supplies UV and gaseous pollutants worldwide.
The official Environment Monitoring API is documented below but is not
implemented because the shell has no protected plugin setting for its key.

**Pollen and thunderstorm asthma:** the keyless app service behind the AirHealth
regional pollen sites supplies the free app data and detail route. Coordinate
lookup selects the regional location. The plugin enables this source by default
for Australian coordinates; users can turn it off in settings. Coordinates
outside the AirHealth and EPA regional coverage use the global Open-Meteo model.

## Global source and Google Maps Platform assessment

Open-Meteo is the default global source. Its [air-quality endpoint](https://open-meteo.com/en/docs/air-quality-api)
supplies the CAMS model AQI and pollutant concentrations, plus UV data, without
an API key.
The plugin requests location-aware daily timing with `timezone=auto`. Its
particulate reading is a model estimate rather than a nearby monitor reading.

Google's [Air Quality API](https://developers.google.com/maps/documentation/air-quality/overview)
was not included in the original comparison. Current Google documentation
describes current conditions, up to 30 days of hourly history, and forecasts up
to 96 hours for over 100 countries, at roughly 500 m grid resolution. Country
coverage and available AQI scales vary. It returns AQIs, pollutant data and
health recommendations, but does not replace the pollen or thunderstorm-asthma
feeds.

Google requires a Google Cloud project, API key and enabled billing, and
[charges per request](https://developers.google.com/maps/documentation/air-quality/usage-and-billing).
Its [display rules](https://developers.google.com/maps/documentation/air-quality/policies)
require Google Maps attribution. The
shell settings do not currently provide protected storage for a provider key,
so this plugin does not call it. Open-Meteo already gives configured locations
a keyless global air-quality fallback; consider Google if the project adds
secure key storage and explicitly accepts its usage billing.

Google has a separate [Pollen API](https://developers.google.com/maps/documentation/pollen/overview)
for pollen types, plant species, indexes and health recommendations. It
documents coverage in over 65 countries, 1 km resolution and daily forecasts
up to five days. It also requires an API key and billing, [charges per request](https://developers.google.com/maps/documentation/pollen/usage-and-billing),
and has [additional attribution and public terms/privacy-policy requirements](https://developers.google.com/maps/documentation/pollen/policies).
It could extend pollen coverage beyond AirHealth, but is not integrated without
protected key storage and an explicit billing decision.

Open-Meteo's pollen variables are limited to Europe and are not requested by
the current plugin. Outside AirHealth's returned Australian locations, pollen
and thunderstorm-asthma readings are not currently available.

The AirHealth app feed provides:

- The regional apps display this **free app data**. The plugin uses the free
  detail route and sends no API key, account cookie or registration.
- Coordinate lookup selects the matching regional service for one request path.
- CloudFront serves the responses (`x-cache: Hit`, `age` header).
- One request returns every measure we care about for a location.
- Already resolves postcode → suburb → SA3/SAL, so we never need a location database.
- Already contains the **thunderstorm asthma risk forecast**, which removes the
  need to scrape VicEmergency.
- Publishes the **numeric category breakpoints** at `/dashboard/metrictree`, so we
  can categorise concentrations exactly as the official app does.
- Contains a category vocabulary and colour palette we can reuse for the UI.

The shared app service covers regional locations through coordinate lookup, so
the plugin does not need city-specific website scrapers.

## Endpoints

Base: `https://app-api.airhealthservices.au` (HTTP/2, nginx front, CloudFront CDN).
No `Authorization`, `X-API-Key` or other credential is required for the endpoints below.
`VITE_USE_MOCK_API` / `?test=mock` exists in the app but is not needed by us.

### 1. Resolve coordinates → location

```
GET /locations/?coordinate=<lat>,<lon>
```

**Order is latitude,longitude.** Passing lon,lat silently returns a wrong
location (Melbourne's lon,lat returned Cocos (Keeling) Islands). This is the
single easiest bug to ship, so the client must build the query as
`fmt.Sprintf("%.4f,%.4f", lat, lon)`.

Response:

```json
{"locations":[{
  "id":863,
  "postcode":"3000",
  "Postcode Name":"Melbourne",
  "suburb":"Melbourne",
  "state":"VIC",
  "SAL":"21640",   "SAL Name":"Melbourne",
  "SA2":"206041503","SA2 Name":"Melbourne CBD - East",
  "SA3":"20604",   "SA3 Name":"Melbourne City",
  "IAR":"20101201", "IAR Name":"Melbourne - Central",
  "District":"VIC_PW007", "District Name":"Central",
  "latitude":-37.825023,"longitude":144.97158,
  "use_sal":false
}]}
```

`use_sal` selects which key the app queries with: `true` → `/SAL/<SAL>/`,
`false` → `/SA3/<SA3>/`. For Melbourne it is `false`, so SA3 `20604` is used.
Both forms work and return the same measures; SAL is finer-grained (suburb).

Also present, unverified against a real postcode: a postcode → SA3 lookup used by
the `/enter-postcode` flow. We do not need it if we geolocate or let the user
enter coordinates.

### 2. Dashboard — the one request the plugin should actually use

```
GET /dashboard/metricdash/SA3/<SA3>/
GET /dashboard/metricdash/SAL/<SAL>/
```

No `free`/`premium` segment here (that segment only exists on the `/details/`
and `/history/` routes). Returns ~144 KB for an SA3, ~9 KB for an SAL.
The SAL form is what we want: it is per-suburb and far smaller.

Response shape:

```json
{
  "sals":["21640"],
  "locations":[{
    "location":{"id":27593,"sal":"21640","suburb":"Melbourne","state":"VIC",
                "sa3":["21640"],"sa3_name":[""],"district":[""],"district_name":[""]},
    "current_conditions":{
      "date":"2026-10-31","day_title":"Later today",
      "text":"Take care",
      "colour":"#ffffff","background_colour":"#b5090a",
      "image":"https://app-api.airhealthservices.au/media/resourceimg/Scene_2010.svg",
      "weather":{"rain_range":"14.3 mm","temperature_min":16.0,
                 "temperature_max":23.0,"rain_probability":null}
    },
    "measures":[
      {"name":"Grass","text":"High","value":2,
       "colour":"#ffffff","background_colour":"#fe9a1f",
       "image":".../Allergens_3.svg"},
      {"name":"Other Allergens","text":"High","value":2,...},
      {"name":"Air Quality","text":"Good","value":1,...,
       "show_metric":{"subcat":"PM2.5","value":0.0,
                      "monitor_is_close":true,"units":"ug/m3"}},
      {"name":"Thunderstorm Asthma","text":"Moderate","value":1,...}
    ],
    "alert":{"title":"Thunderstorm Asthma",
             "text":"The Thunderstorm Asthma forecast for this location is currently: Moderate. Take care.",
             "icon_image_url":".../esta_icon_alert.svg",
             "colour":"#ffffff","background_colour":"#fbc503"},
    "forecast":[
      {"date":"2026-10-31","day_title":"Later today",
       "temperature_max":23.0,"temperature_min":16.0,
       "measures":[{"name":"Grass","text":"High","value":2,...},
                   {"name":"Air Quality","text":"Very Good","value":0,...},
                   {"name":"Thunderstorm Asthma Alert","text":"Moderate","value":1,...}]}
    ]
  }],
  "ads":{},"news":[],"factsheets":[],"podcasts":[],
  "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"
}
```

Notes:

- `measures` is the current state; `forecast[]` is day-by-day.
- Measure names inside `forecast[].measures` are **not** identical to the
  top-level `measures` names: the forecast uses `"Thunderstorm Asthma Alert"`,
  the current block uses `"Thunderstorm Asthma"`. Match on prefix, not equality.
- Forecast measures are **sparse**. Some days omit Air Quality or Thunderstorm
  Asthma entirely (see 2026-11-02, which has only Grass and Other Allergens).
  The renderer must treat a missing measure as "no forecast", not as Low.
- `alert` is `null` or absent when there is no active alert.
- `show_metric` is present only on Air Quality and carries the real PM2.5 number
  in `value` with `units`. `monitor_is_close` appears to mean the nearest
  monitoring station is nearby; `value: 0.0` with `monitor_is_close: true` is a
  genuine reading, not a placeholder.
- `current_conditions.text` is an overall verdict string ("Take care"), separate
  from any individual measure.
- `weather` fields can be `null` (`rain_probability` was).

### 3. Per-measure detail (optional, richer)

```
GET /dashboard/metricdash/free/SA3/<SA3>/details/<Measure_Type>/
GET /dashboard/metricdash/{free|premium}/SAL/<SAL>/details/<Measure_Type>/
```

`free`/`premium` is required here. `premium` behaves the same without a token for
the measures we tested; treat it as cosmetic.

`Measure_Type` takes the exact strings from the app enum. Note the URL slugs are
**inconsistent** with the enum values, so hardcode a slug map:

| app enum value | URL slug | works |
|---|---|---|
| `"Air Quality"` | `Air_Quality` | yes |
| `"Allergens"` | `Allergens` | yes |
| `"Grass"` | `Grass` | yes |
| `"Woodsmoke"` | `Woodsmoke` | yes |
| `"Thunderstorm Asthma"` | `Thunderstorm_Asthma` | yes |
| (not in enum) | `Grass_Pollen` | yes, identical to `Grass` |
| (not in enum) | `Wood_Smoke` | partial — `current` populated, `data` empty |

Response:

```json
{"sals":[...],
 "locations":[
   {"location":{"id":27991,"sal":"22038","suburb":"Parkville","state":"VIC",...},
    "current":{"name":"Grass","text":"High","value":2,"colour":"#ffffff",
               "background_colour":"#fe9a1f","image":".../Allergens_3.svg"},
    "data":{"combined":[{"name":"Grass","forecast_date":"2026-10-31",
                          "day_title":"Later today","text":"High","value":2,...},
                        {"...":"..."}],
            "Alternaria":[...],"Birch":[...],"Cypress":[...],
            "Eucalypts":[...],"Olive":[...],"Plane":[...],"Plantain":[...]},
    "map":{"has_map":false,"map_image":null,"map_desc":null}}
 ],
 "attribution":"Data provided in collaboration with the University of Tasmania and University of Melbourne"}
```

Measure-specific `data` keys:

- **Air Quality** → `combined`, `PM2.5`, `PM10`, `O3`. (No NO2/SO2/CO at all.)
  Each element paints a *slot*, not a timestamp: `day_title` is `"Now"` or
  `"Later"` and `num_boxes` says how many slots to paint. The numeric
  concentration is **only** in `current.show_metric.value`, not in `data`.
- **Allergens** → `combined`, plus one series each for `Alternaria`, `Birch`,
  `Cypress`, `Eucalypts`, `Olive`, `Plane`, `Plantain`. This is the fungal spore
  and tree-pollen breakdown; genuinely useful for a sufferer.
- **Grass** → `combined` plus a `Grass` series. Verified against SAL `21640`:
  `combined` has 6 entries with `day_title` "Later today" for today; the `Grass`
  series has 6 entries with `day_title` "Later" and carries `num_boxes`. Same
  levels, slightly different labelling — use `combined` for values and only
  fall back to the per-series key if `combined` is absent.
- **Air Quality** `combined` returned only 2 entries (today + tomorrow) while
  `PM2.5` had 3 and `PM10` had 1 — series lengths differ per pollutant, so never
  zip them by index; always key on `forecast_date`.
- **Woodsmoke** → `current` only, `data` empty.
- **Thunderstorm Asthma** → `data` is a **flat array**, not an object with
  `combined`. The client transform detects this case explicitly
  (`MEASURE_TYPE.THUNDERSTORM_ASTHMA || MEASURE_TYPE.WOOD_SMOKE`).
  Getting this wrong is a decode failure.

So any Go struct must accept `data` as either an object or an array. Simplest is
`json.RawMessage` plus a type switch.

### 4. History (optional, for a sparkline later)

```
GET /dashboard/metricdash/free/SAL/<SAL>/history/<Measure_Type>/
```

Returns `{"sals":[...],"locations":[{"location":...,"data":[{...}]}]}` with one
entry per past day, same paint-slot shape as above. Verified: Grass history for
SAL `21640` returned **91 entries**, newest first (2026-10-31 → older), so this
is a full season of grass levels — enough for a season sparkline. Air Quality
history returned `{}` (empty object, HTTP 200) — handle that as "no history"
rather than an error. Note the ordering is **newest first** in the raw response;
the app re-sorts ascending.

### 4b. Current vs forecast are different series

For the same day, `current` and `forecast[0]` can disagree. Observed on
2026-10-31 for SAL `21640`: current Air Quality = "Good" (`value: 1`) but
`forecast[0]` Air Quality = "Very Good" (`value: 0`). Do not treat them as
duplicates to be merged — current is the live observation, `forecast[0]` is the
day's modelled outlook. Show current prominently and the forecast below it.

### 5. Endpoints that do NOT work

- `/dashboard/metricdash/SA3/` (no id) → 404 plain nginx HTML.
- `/dashboard/metrictree/` **with a trailing slash** → 404. Without the slash it
  works: see "Numeric breakpoints" below. Trailing-slash sensitivity is real on
  this host — always build paths deliberately.
- `/dashboard/metricdash/District/<District>/` → **200 but empty** (`sals: []`,
  `locations: []`). District-level is not populated; do not rely on it.
- `/dashboard/free/SA3/<id>/` → 404.
- `api.epa.vic.gov.au` → does not resolve (the real EPA host is the gateway below).
- `/maps/values/AUS/<Measure>/` and `/maps/geojson/...` → **401** without a
  `device_jwt_token`, i.e. the map layers are account-gated. Do not use them.
  `/maps/maptree` and `/locations/sals/` are keyless (the latter returns `{}`
  with a `coordinate` param; it wants `?keyword=<text>` for suburb search).

## Category taxonomy

Extracted from the bundle's category definitions and confirmed against live data.
Colours are the app's own, and map cleanly onto sysc-shell tones.

| Measure | Ordered levels | Colours |
|---|---|---|
| Grass | Low, Moderate, High, Extreme | `#4bd964`, `#fbc503`, `#fe9a1f`, `#b5090a` |
| Allergens | Low, Moderate, High, Extreme | same |
| Air Quality | Very Good, Good, Fair, Poor, Very Poor, Extremely Poor | `#56e0ff`, `#4bd964`, `#fbc503`, `#fe9a1f`, `#b5090a`, … |
| Thunderstorm Asthma | Low, Moderate, High | `#4bd964`, `#fbc503`, `#fe9a1f` |
| Woodsmoke | Low, Moderate, High | `#4bd964`, `#fbc503`, `#b5090a` |

`value` is the zero-based index into each list. **Air Quality is inverted
relative to the others** — `0` is *best* ("Very Good"), not worst. Any severity
ranking, threshold comparison or dial fill must special-case Air Quality or it
will render backwards. This is the second easy bug to ship.

Category display text exists for each level and is worth reusing verbatim in the
panel; examples:

- Grass High — "Airborne grass allergens are elevated."
- Thunderstorm Asthma High — "There is more than one of the conditions necessary
  for an epidemic thunderstorm asthma event."
- Air Quality Poor — "There is increased pollution in the air. If you are at
  higher risk consider taking steps to reduce your exposure."

## Numeric breakpoints (`GET /dashboard/metrictree`)

**Do not hardcode the categories from the table above.** The API publishes the
authoritative breakpoints that the official app itself uses to turn a
concentration into a category. Fetch them at startup and cache for the day.

```
GET https://app-api.airhealthservices.au/dashboard/metrictree
```

No trailing slash. Keyless. ~5 KB. Response is a nested map:
`measure → group → metric → {total_boxes, classes[]}`, where each class is
`{name, lower_bound, upper_bound, warn_level}`.

Note the quirk the app itself applies: it adds 1 to every `lower_bound` and
subtracts 1 from every `upper_bound` before use (see `fetchMetricTrees` in the
bundle). That is a legacy off-by-one workaround. Our implementation should
ignore `lower_bound`/`upper_bound` as given and use the **class order plus the
`upper_bound` of each class as an exclusive ceiling**, which matches the raw
data:

| Measure / metric | Classes and ceilings (exclusive upper) |
|---|---|
| Air Quality / PM2.5 (µg/m³) | Very Good <5, Good <12.5, Fair <25, Poor <50, Very Poor <150, Extremely Poor ≥150 |
| Air Quality / PM10 (µg/m³) | Very Good <20, Good <40, Fair <80, Poor <120, Very Poor <300, Extremely Poor ≥300 |
| Air Quality / O3 (pphm) | Very Good <25, Good <50, Fair <100, Poor <150, Very Poor <300, Extremely Poor ≥300 |
| Air Quality / NO2 (pphm) | Very Good ≤0, Good <30, Fair <120, Poor <180, Very Poor <360, Extremely Poor ≥360 |
| Grass (grains/m³) | Low <20, Moderate <50, High <100, Extreme ≥100 |
| Allergens / Alternaria, Birch, Cypress, Eucalypts, Olive, Plane, Plantain | Low <20, Moderate <50, High <100 (Cypress High <500), Extreme above that |
| Thunderstorm Asthma | Low ≤0.5, Moderate 1.0–1.5, High ≥2.0 (ordinal, not a concentration) |
| Woodsmoke | Low <350, Moderate <450, High ≥450 (units not published) |

Worth noting: `warn_level` (0/1/2) is supplied per class and is the cleanest
severity signal the API gives us — it is already normalised across measures,
including Air Quality, so it sidesteps the inversion problem entirely. Use
`warn_level` for tone/colour decisions and the class order for text.

Also from `/maps/maptree` (keyless, ~19 KB): the canonical class names, colours
and units for every layer. It confirms the palette used above, gives
`"grains/m³"` as the grass/allergen unit, `µg/m³` for particulates and `pphm`
(parts per hundred million, i.e. 10 ppb) for O3/NO2, and supplies the six-day
`timestep_values` label list ("Later today", then "Sunday, 1 Nov 2026" format).
`/maps/maptree` also marks which layers are free vs premium *in the mobile app*
— irrelevant to us, since the dashboard routes are keyless.

## Attribution and endpoint notes

### AirHealth regional pollen apps

The plugin uses the free app route and detail endpoints. It sends no API key or
account cookie. Coordinate lookup selects the dashboard for the configured
location. The panel credits the providers: "Data provided in collaboration with
the University of Tasmania and University of Melbourne".

### Other source attribution

- **EPA Victoria** — CC BY 4.0 via DataVic, attribution to EPA Victoria. The
  Environment Monitoring API is the official channel (free key). See the
  keyless fallback below.
- **Open-Meteo** — free for non-commercial use, attribution to CAMS ENSEMBLE and
  Open-Meteo. Air quality only for Australia; pollen layers are Europe-only.
- The API key `VITE_REPORT_API_KEY` found in the bundle is for symptom reporting
  only and must not be used.

### Keyless EPA Victoria fallback (no registration)

If we want EPA data with no API key at all, there is a public ArcGIS feature
service that mirrors EPA's readings:

```
GET https://services1.arcgis.com/vHnIGBHHqDR6y0CR/arcgis/rest/services/Air_Quality_Latest_Readings/FeatureServer/0/query
    ?where=1=1&outFields=*&f=json
```

Keyless, public, layer `air_quality_index`. Verified 2026-10-31. Fields:
`siteID, siteName, siteType, healthParameter, healthAdvice, averageValue, since,
until` (epoch ms). Real rows:

```json
{"siteName":"Yinnar","siteType":"Sensor","healthParameter":"Particles",
 "healthAdvice":"Good","averageValue":6.58,"since":1764910800000,"until":1764914400000}
{"siteName":"Alphington","siteType":"Standard","healthParameter":"PM2.5",
 "healthAdvice":"Fair","averageValue":20.75,"since":1764910800000,"until":1764914400000}
```

This is genuinely useful: it gives `healthAdvice` (the EPA category) and
`averageValue` together, so no breakpoint table is needed, and `siteType` lets us
prefer `Standard` stations over indicative `Sensor` ones. Caveats: it is an
unofficial third-party mirror (ArcGIS owner `jakc`, public access, licenceInfo
points back to the EPA portal), it returns one row per site per health parameter
so we must pick the PM2.5 row, and there is no distance sort — we compute the
nearest `Standard` site ourselves from site coordinates, which this layer does
**not** include. So: good fallback for "what is the current category", not a
complete replacement.

ArcGIS supports spatial filtering (`geometry`, `geometryType`,
`inSR`/`outSR`, `distance`, `units`) on the query, so **nearest-site lookup by
coordinates is possible** without a site list: pass `geometry={"x":144.9631,"y":-37.8136}`,
`geometryType=esriGeometryPoint`, `inSR=4326`, `distance=25000`, `units=esriSRUnit_Meter`,
`spatialRel=esriSpatialRelIntersects`. That makes the keyless path viable as a
real primary option for air quality.

## Coverage

The AirHealth app service resolved and returned live dashboard measures for
Melbourne, Canberra, Sydney and Perth using the configured coordinates. The
location lookup returned these routes:

| App/site | SAL | State | Dashboard measures returned |
| --- | --- | --- | --- |
| [Melbourne Pollen](https://www.melbournepollen.com.au/) | `21640` | VIC | Grass, Other Allergens, Air Quality, Thunderstorm Asthma |
| [Canberra Pollen](https://www.canberrapollen.com.au/) | `80038` | ACT | Grass, Other Allergens, Air Quality |
| [Sydney Pollen](https://www.sydneypollen.com.au/) | `13730` | NSW | Grass, Other Allergens, Air Quality |
| [Perth Pollen](https://www.perthpollen.com.au/) | `51230` | WA | Grass, Other Allergens, Air Quality |

The same `/locations/?coordinate=lat,lon` lookup selects the SAL; the plugin
then requests that SAL's dashboard. It does not scrape each website or need
browser cookies. These checks confirm the listed metro areas, not every address
in Australia; an absent measure remains absent rather than being shown as Low.
Thunderstorm asthma is seasonal and is published only when the service returns
it.

EPA Victoria is Victorian by definition. For other states the Open-Meteo provider
is the only option (NSW has its own keyed API; not worth the complexity now).

### Open-Meteo: what is actually available for Melbourne

Verified live 2026-10-31 for `-37.81,144.96`:

| variable | result |
|---|---|
| `pm10`, `pm2_5`, `carbon_monoxide`, `nitrogen_dioxide`, `sulphur_dioxide`, `ozone` | real values (7.0, 4.3, 105.0, 3.1, 1.0, 57.0) |
| `uv_index` | real value, `0.1` at the time of the request |
| `us_aqi`, `european_aqi` | real values (28, 19) |
| `uv_index_max` (daily) | real values, `[4.9, 4.55, 3.2, 7.4]` |
| `grass_pollen`, `birch_pollen`, `alder_pollen`, … | **`null`** — Europe-only |

**Daily variables are mostly rejected.** `us_aqi`, `european_aqi`, `pm2_5_max`
and `pm10_max` all fail with

```json
{"error":true,"reason":"Invalid value: Cannot initialize ForecastVariableDaily…"}
```

`uv_index_max` is the one daily variable that works. So a multi-day air quality
forecast from this source is not available; only current values plus a UV daily
peak. Requesting an unsupported daily variable fails the whole response, not just
that field — do not add them speculatively.

**Errors are a boolean, not an object.** The failure shape is
`{"error": true, "reason": "..."}`. A Go struct declaring `Error *struct{…}`
fails to unmarshal on every success response. Declare `Failed bool` and
`Reason string` separately.

**Absent must stay distinct from zero.** Every numeric field in the response is
nullable. UV legitimately reads 0.0 overnight, and a `null` is a different
statement ("this variable is not modelled here") from a zero ("the sun is down").
Decode into `*float64` and drop the row when the pointer is nil.

**UV peak beats UV now.** The instantaneous `uv_index` is largely a statement
about the clock: it is 0.1 at breakfast and 0.1 after dinner regardless of how
severe the day was. The plugin reports `uv_index_max` for today as the headline
and shows the current value alongside it.

## Provider architecture

Four providers, each independently toggleable, behind one small interface:

```go
type AirSnapshot struct {
    Site     string     // "Melbourne CBD"
    Type     string     // "Standard" | "Sensor"
    PM25     float64
    Category string     // "Fair"
    Until    time.Time
    Source   string
    Stale    bool
}

type PollenSnapshot struct {
    Grass    Measure
    Other    Measure
    Combined Measure
    TS       Measure
    Alert    *Alert
    Forecast []Day
    Attribution string
    Stale    bool
}

type Provider[T any] interface {
    ID() string
    Enabled(Settings) bool
    Fetch(ctx context.Context) (T, error)
}
```

| Provider | Covers | Key | Licence | Default |
|---|---|---|---|---|
| `epa-arcgis` | PM2.5, PM10, O3, NO2, category | none | CC BY 4.0 (EPA data) | **on** |
| `epa-portal` | same, plus full time series | EPA key | CC BY 4.0 | deferred |
| `airhealth` | grass, allergens, TS asthma, AQ | none | provider attribution shown | **on, opt-out** |
| `open-meteo` | air quality, gases and UV | none | CAMS attribution shown | **on** |

The bar can work with just `epa-arcgis`. Pollen and thunderstorm asthma require
AirHealth, so those measures are absent when the user turns that source off.

## Proposed backend architecture

```
plugins/airquality/
  manifest.json
  client.go        shared HTTP client, timeouts, backoff, user agent
  epa.go           EPA ArcGIS mirror + EPA portal client; nearest-Standard-site
  airhealth.go     AirHealth client: locations, metricdash, details
  metrictree.go    fetch + parse breakpoints; classify(concentration) → class
  openmeteo.go     optional AQ forecast fallback
  measure.go       Measure type, ordering, warn_level severity, tone mapping
  cache.go         TTL cache + stale-on-error + on-disk last-good snapshot
  view.go          bar/tooltip/panel rendering (phase 2)
cmd/sysc-plugin-airquality/main.go
```

Domain types:

```go
type Class struct {
    Name      string  // "High"
    Upper     float64 // exclusive ceiling; 0 for the top class
    WarnLevel int     // 0/1/2, already normalised across measures
    Colour    string  // "#fe9a1f"
}

type Tree struct {                        // from /dashboard/metrictree
    Measures map[string]map[string]map[string]MetricTree
}
type MetricTree struct {
    TotalBoxes int
    Classes    []Class
}

type Measure struct {
    Name     string  // "Grass", "Air Quality", ...
    Value    int     // raw API value (index into the measure's class list)
    Text     string  // "High"
    BG       string  // "#fe9a1f"
    Warn     int     // retained from Class where derivable
    Metric   *Metric // PM2.5 value/units when present
}

type Metric struct{ Subcat string; Value float64; Units string }

type Day struct {
    Date     time.Time
    Title    string
    Max, Min *float64
    Measures map[string]Measure // absent key = no forecast, never zero-filled
}

type Dashboard struct {
    Location    Location
    Overall     string   // current_conditions.text
    Weather     Weather
    Current     []Measure
    Forecast    []Day
    Alert       *Alert
    Attribution string
    FetchedAt   time.Time
    Stale       bool
}
```

Normalisation rules, all of which need a test:

1. Build the coordinate query as `lat,lon`; assert on the serialised URL.
2. `data` decodes as object **or** array.
3. Forecast measure name `"Thunderstorm Asthma Alert"` matches current
   `"Thunderstorm Asthma"`.
4. Missing forecast measure stays missing; never default to zero/Low.
5. Air Quality value ordering is reversed for severity purposes — prefer
   `Class.WarnLevel` and only fall back to raw `value` when the tree is absent.
6. Unknown measure names or unknown levels are preserved, not dropped — the API
   can add measures and we should degrade gracefully.
7. `alert == null`, `weather == null`, `{}` history body and 404s all decode
   without panic.
8. `classify()` honours exclusive upper bounds and returns the top class for
   anything above the last ceiling.
9. Metrictree fetch failure degrades to the static fallback table rather than
   disabling the plugin.

Settings:

- `latitude`, `longitude` (default `-37.8136`, `144.9631` — Melbourne CBD)
- `station` — `auto` (nearest Standard site) or a named site
- `refresh_seconds` — default 600 (EPA publishes hourly averages; each row has a
  `until` timestamp so we can align instead of guessing)
- `bar_measures` — which measures to surface in the bar
- `source_airhealth` — on by default for the plugin's Australian coordinates;
  users can turn it off in settings
- EPA portal key — deferred until a protected credential setting is available
- `alert_level` — notify at or above this severity

Refresh strategy:

- EPA ArcGIS: 10 minutes. Rows carry `since`/`until` epoch-ms so we can compute
  freshness exactly and avoid polling before the next hourly window flips.
- AirHealth: 15 minutes (`VITE_POOLING_INTERVAL` is 15).
- Location resolution (AirHealth only): once per coordinate, cache indefinitely,
  re-resolve if coordinates move more than ~1 km.
- On-disk state: cache the last good snapshot so the bar shows something
  immediately after a shell restart, with an explicit stale marker.
- Single in-flight request per provider; cancel on shutdown; exponential backoff
  capped at the refresh interval on repeated failures.
- Each provider fails independently — one dead source must not blank the bar.

## EPA Victoria official API (optional upgrade)

It returns full time series and confidence rather than a single latest value.
It needs a free key. The shell currently has no protected plugin setting for
that credential, so the keyless ArcGIS mirror remains in use until such a path
is available.

- Base `https://gateway.api.epa.vic.gov.au/environmentMonitoring/v1/sites/`
- `X-API-Key` header, free registration at <https://portal.api.epa.vic.gov.au/>,
  product "Environment Monitoring", 5 req/s.
- Without a key the gateway returns **401** with an Azure APIM
  `WWW-Authenticate: AzureApiManagementKey` header. Verified 2026-10-31.
- `?environmentalSegment=air&location=[lat,lon]` → nearest site.
- `<siteID>/parameters` → `parameters[].timeSeries[].readings[]` with
  `averageValue`, `confidence`, `totalSample`, `until`, `healthAdvice`.
- Pollutant names `PM2.5`, `PM10`, `NO2`, `O3`, `SO2`, `CO`.
- Site types `Standard` (19 accurate sites) vs `Sensor` (indicative).
- Same data underlies the CC-licensed DataVic datasets, so attribution to EPA
  Victoria covers both the portal and the ArcGIS mirror.

Open-Meteo remains a reasonable keyless backup for forecast PM2.5 and AQI, but its
pollen layers are Europe-only and useless here.

## Current source decisions

The plugin reads the free, keyless AirHealth app feed for configured Australian
coordinates. Coordinate lookup selects the regional pollen service, and the
panel credits its providers. Users can turn the source off in settings. The panel
shows allergen detail, labels UV as today's peak, includes source alert text and
offers Retry after a source failure.

EPA's keyless ArcGIS mirror remains the particulate source. The official EPA
gateway remains deferred until the shell supports protected credential storage.
4. Phase 2 UI: half radial dials are viable — we have an ordered level, a real
   concentration, published breakpoints and a `warn_level` severity. Note Air
   Quality's inverted scale before wiring a dial, and prefer `warn_level` over raw
   `value` so every measure shares one severity axis.
5. Thunderstorm asthma is seasonal (1 Oct – 31 Dec). Out of season the provider
   returns Low or omits the measure entirely; the panel should hide the section
   rather than show a permanent "Low" that implies monitoring is active.

## Appendix: how to re-verify these claims

Everything above was checked against live endpoints on 2026-10-31. To re-verify
the regional location and dashboard routes:

```sh
# AirHealth: location resolution (note lat,lon order)
curl -s 'https://app-api.airhealthservices.au/locations/?coordinate=-37.8136,144.9631'
curl -s 'https://app-api.airhealthservices.au/locations/?coordinate=-35.2809,149.1300'
curl -s 'https://app-api.airhealthservices.au/locations/?coordinate=-33.8688,151.2093'
curl -s 'https://app-api.airhealthservices.au/locations/?coordinate=-31.9505,115.8605'

# AirHealth: dashboard for Melbourne (SAL 21640)
curl -s 'https://app-api.airhealthservices.au/dashboard/metricdash/SAL/21640/' | head -c 400
curl -s 'https://app-api.airhealthservices.au/dashboard/metricdash/SAL/80038/' | head -c 400
curl -s 'https://app-api.airhealthservices.au/dashboard/metricdash/SAL/13730/' | head -c 400
curl -s 'https://app-api.airhealthservices.au/dashboard/metricdash/SAL/51230/' | head -c 400

# AirHealth: canonical breakpoints and class colours
curl -s 'https://app-api.airhealthservices.au/dashboard/metrictree' | head -c 400

# EPA Victoria: keyless ArcGIS mirror, nearest Standard site to the CBD
curl -s -G 'https://services1.arcgis.com/vHnIGBHHqDR6y0CR/arcgis/rest/services/Air_Quality_Latest_Readings/FeatureServer/0/query' \
  --data-urlencode 'where=1=1' \
  --data-urlencode 'outFields=siteName,siteType,healthParameter,healthAdvice,averageValue,until' \
  --data-urlencode 'geometry={"x":144.9631,"y":-37.8136}' \
  --data-urlencode 'geometryType=esriGeometryPoint' \
  --data-urlencode 'inSR=4326' --data-urlencode 'returnGeometry=true' \
  --data-urlencode 'distance=8000' --data-urlencode 'units=esriSRUnit_Meter' \
  --data-urlencode 'spatialRel=esriSpatialRelIntersects' --data-urlencode 'f=json'

# EPA Victoria: official gateway, confirms 401 without a key
curl -s -o /dev/null -w '%{http_code}\n' \
  'https://gateway.api.epa.vic.gov.au/environmentMonitoring/v1/sites/?environmentalSegment=air'

# Open-Meteo: keyless air quality for Melbourne
curl -s 'https://air-quality-api.open-meteo.com/v1/air-quality?latitude=-37.81&longitude=144.96&current=pm2_5,us_aqi'
```

If `/dashboard/metricdash/SAL/21640/` starts returning 403 or a login redirect,
the cached report is kept stale and the source error is shown in the panel; the
plugin does not retry in a tight loop.
