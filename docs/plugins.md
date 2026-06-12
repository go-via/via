---
title: Plugins
layout: default
parent: Guides
nav_order: 6
---

# Plugins

```go
app := via.New(via.WithPlugins(
    picocss.Plugin(picocss.WithThemes(picocss.AllPicoThemes)),
    echarts.Plugin(),
))
```

Plugins implement `Register(*via.App)` and call any of `AppendToHead`,
`AppendToFoot`, `AppendAttrToHTML`, `HandleFunc`, or `RegisterAppSignal`
during boot to inject document fragments, asset routes, and client-driven
signals.

{: .warning }
Call these only from `Register` — the document-mutation slices are not
lock-guarded against concurrent appends after the server starts.

Plugin packages expose `Plugin(...)` as the canonical constructor (never
`New(...)`) so `via.WithPlugins(...)` call sites stay uniform.

## Asset delivery

The bundled plugins **embed their pinned client builds** with `go:embed` and
serve them from a content-hashed, immutably-cached same-origin path. Plugin
registration does **zero network I/O** — there is no boot-time CDN fetch, no
third-party origin in the page, and an air-gapped or offline deploy works out
of the box. The embedded version is fixed; `WithVersion(v)` only guards the
pin (restating the embedded version is a no-op; any other version panics,
because there is no embedded asset or SRI hash to back it).

Two opt-outs, on the `echarts` and `maplibre` plugins (Pico ships only the
embedded build):

- **`WithSource(url)`** — serve the script from a **same-origin** path you
  host yourself (a custom build or internal mirror). Cross-origin URLs are
  rejected; use `WithCDN` for those.
- **`WithCDN(url, integrity)`** — load from a CDN. The `integrity` SRI hash
  (`sha256-`/`sha384-`/`sha512-` + base64 digest of that exact body) is
  **mandatory** — the emitted `<script>` carries it plus
  `crossorigin="anonymous"`, so a tampered response is refused by the
  browser. There is no way to opt out of SRI; a version bump means supplying
  the new build's hash. MapLibre also has `WithCDNStylesheet(url, integrity)`.

```go
echarts.Plugin(echarts.WithCDN(
    "https://cdn.jsdelivr.net/npm/echarts@6.0.0/dist/echarts.min.js",
    "sha384-…",
))
```

## Bundled plugins

### picocss

`picocss.Plugin()` wires the [Pico CSS](https://picocss.com) framework:
theme + dark-mode switching driven by client signals (no full reload),
served from a plugin asset route with ETag revalidation and gzip
negotiation. Options include `WithThemes(...)`, `WithDefaultTheme(...)`,
`WithClassless()`, `WithColorClasses()`, and `WithDarkMode()` /
`WithLightMode()`. `picocss.ThemeRef()` / `DarkModeRef()` return the Datastar
signal references for inline expressions.

```go
h.Button(h.Text("Blue"),
    h.DataOnClick("%s = %q", picocss.ThemeRef(), picocss.PicoThemeBlue))
```

See `internal/examples/picocss` for client-side theme switching.

### echarts

`echarts.Plugin()` integrates [Apache ECharts](https://echarts.apache.org).
Hold a `*echarts.Chart` on the page, build it in `OnInit`, mount it in
`View`, and update it from actions or a `via.Stream` ticker:

```go
type Page struct {
    Chart *echarts.Chart
}

func (p *Page) OnInit(ctx *via.Ctx) error {
    if p.Chart == nil {
        p.Chart = echarts.NewChart(
            echarts.WithElementID("cpu"),
            echarts.WithTitle("CPU"),
            echarts.WithDimensions("100%", "300px"),
        )
    }
    return nil
}

func (p *Page) Refresh(ctx *via.Ctx) error {
    return p.Chart.SetSeries(ctx, echarts.Line("CPU", [][]any{ {0, 12}, {1, 18} }))
}
```

See `internal/examples/sysmon` for a live system monitor streaming into
ECharts.

### maplibre

`maplibre.Plugin()` integrates [MapLibre GL JS](https://maplibre.org) —
interactive vector maps whose camera, markers, and data layers are all driven
from Go and pushed over SSE. Hold a `*maplibre.Map`, build it in `OnInit`,
mount it in `View`, then move it from actions or a `via.Stream` ticker:

```go
type Page struct {
    Map *maplibre.Map
}

func (p *Page) OnInit(ctx *via.Ctx) error {
    if p.Map == nil {
        p.Map = maplibre.NewMap(
            maplibre.WithCenter(maplibre.At(-122.42, 37.77)), // At(lng, lat)
            maplibre.WithZoom(11),
            maplibre.WithNavigationControl(),
        )
    }
    return nil
}

func (p *Page) View(ctx *via.CtxR) h.H { return p.Map.Mount() }

func (p *Page) GoToTokyo(ctx *via.Ctx) { p.Map.FlyTo(ctx, maplibre.At(139.69, 35.69), 10) }
```

Coordinates are `[lng, lat]` — longitude first — the inverse of the lat/lng most
map UIs print, and the single most common MapLibre mistake. The camera, marker,
and center APIs take a typed `LngLat` whose named fields defuse the swap: build
it with `maplibre.LngLat{Lng: …, Lat: …}` (order-independent) or the
`maplibre.At(lng, lat)` shorthand; box APIs take a `Bounds{West, South, East,
North}`. GeoJSON geometry (`Point`, `LineString`, `Polygon`) stays as raw
`[lng, lat]` arrays.

The runtime surface, all delivered over SSE:

- Camera — `FlyTo` (curved flight), `EaseTo`, `JumpTo`, `SetCenter` (all take a
  `LngLat`), `SetZoom`, `SetPitch`, `SetBearing`, and `FitBounds(ctx, Bounds)`.
- Markers — `AddMarker(ctx, id, At(lng, lat), opts…)` places a keyed pin;
  `WithMarker` declares a static one at construction; `MoveMarker` repositions
  it live (vehicle tracking), `RemoveMarker` / `ClearMarkers` tear down. Options:
  `Color`, `Draggable`, `Scale`, `PopupText` (XSS-safe, for user content),
  `PopupHTML` (an `h.H` body — `h.T` escapes, `h.Raw` is trusted markup only).
- Data — declare sources/layers with `WithGeoJSONSource` + `WithLayer`, or
  add them at runtime with `AddGeoJSONSource` / `AddLayer`; push live data
  with `SetGeoJSON(ctx, sourceID, fc)`. Build GeoJSON with `Point`,
  `LineString`, `Polygon`, `Feature`, `FeatureCollection`; build layers with
  `CircleLayer` / `LineLayer` / `FillLayer` / `SymbolLayer`.
- Events — drive Go from user gestures: `OnClick`, `OnMoveEnd`,
  `OnMarkerClick`, `OnMarkerDragEnd`, `OnFeatureClick`, and a generic
  `OnMapEvent` escape hatch (right-click, double-click, …). Each takes a bound
  method that reads a typed `MapEvent` — the clicked `LngLat`, the `MarkerID` /
  `FeatureID`, and the live camera — via `p.Map.Event(ctx)`. `WithFeatureHover`
  highlights the hovered feature client-side, with no round-trip.
- Styling — compose data-driven paint/layout values with typed expression
  builders instead of raw `[]any`: `Get`, `FeatureState`, `Zoom`, `Boolean`,
  `Case`, `Interpolate`, `Step`, plus `WhenHovered` / `WhenState` sugar (e.g.
  `Paint("fill-color", WhenHovered("#ffcc00", "#5856d6"))`).
- Dialogs — `ShowPopup(ctx, id, at, text)` / `ShowPopupHTML` (an `h.H` body) /
  `ClosePopup` show keyed, server-driven popups; the "open a popup at the
  clicked feature" pattern pairs naturally with `OnFeatureClick`.
- Lifecycle — `SetStyle`, `Resize`, `Dispose`, and the `Call(ctx, method,
  args…)` escape hatch for any Map method the typed API misses.

{: .warning }
The default style is MapLibre's no-key demo style, meant for demos and CI,
not a production SLA. Supply your own with `WithStyle(url)` (e.g. a MapTiler
or Stadia style) for real use. Pin a v5 release — v6 is ESM-only and drops the
`maplibregl` global the `<script>` include relies on.

Self-host or harden with `WithSource` / `WithStylesheet` (offline, air-gapped),
or `WithCSPBuild()` to use the inline-worker bundle under a strict `worker-src`
policy.

See `internal/examples/maps` for a server-driven world map: city buttons fly
the camera and a drone marker glides along a route, live, over SSE.
