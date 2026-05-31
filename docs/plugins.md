---
title: Plugins
layout: default
parent: Guides
nav_order: 5
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
