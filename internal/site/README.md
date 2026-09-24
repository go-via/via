# go-via.dev

The documentation site, written with via. Every demo is a via child actually
running, and each shows its own source.

## Run

```sh
cd internal/site
go run .
```

## Environment

- `VIA_ADDR` — listen address, default `:8080`.
- `VIA_SESSION_KEY` — cookie signing key, read by via. Under 16 bytes panics.
  The default session store is in-memory, so a restart forgets every session
  either way; a fixed key only matters once a persistent store is configured.
- `VIA_ORIGIN` — the site's origin (`https://go-via.dev`). Also turns on
  Secure cookies, so set it only behind HTTPS. Unset, actions accept any origin
  and via logs a warning at startup; set it in production.

`/healthz` answers `ok <version>`, where version is stamped at build time
with `-ldflags "-X main.version=..."`. `/robots.txt` and `/favicon.ico` are
served next to it.

## Deploy

See [`DEPLOY.md`](./DEPLOY.md): a static binary behind Caddy, on any Linux
host.

## Layout

- `main.go` — the server: configuration from the environment, signals,
  shutdown.
- `site/` — router options, mounts, static serving. `site.New` is what
  `main.go` and the tests both build.
- `shell/` — chrome: `layout.go`, `nav.go` (`Nav`, `NavFor`), `meta.go`.
- `content/` — one mounted page per file, plus `page.go` for the wiring they
  share.
- `demos/` — one demo per file, embedded verbatim for its card's Source.
  `shared_contract.go` holds what the demos use without declaring.
- `demo/` — the card, chroma highlighting, rate limits and resets. `demo/gen`
  generates `static/chroma.css`, keeping chroma's styles out of the binary.
- `static/` — `site.css`, `chroma.css`, `islands.css`, the inspector and
  island scripts, `brand/`, `fonts/`, `vendor/` (MapLibre) and `data/` (the
  island's GeoJSON), served from `embed`.

This is a nested module (`go-via.dev/site`, `replace ../..`) so chroma stays
out of via's `go.mod`; the root `go build ./...` does not see it. No Node, no
asset pipeline. `static/chroma.css` is generated: `go generate ./demo`, and a
test pins it.

## Adding a demo

1. `demos/<x>.go`: one type with `View`, actions as methods. It is shown whole,
   so it has to read on its own.
2. A field on the page's root struct in `content/`.
3. `demo.Card(title, prose, via.Child(p.X), "<x>.go")`.

A demo that mutates shared state takes a `Limiter` field, set by the demo's
own `New*` constructor from a limiter the page owns, and checks
`Lim.Allow(ctx)` first.

## Public-state bounds

- `demos.ResetAll` runs every 15 minutes. The shared counter's reset is
  published, so open tabs redraw; the vote tallies have no topic, so a tab
  redraws them on its next vote or reload.
- Per-tab lists cap at `keepRows` (50) and are never reset.
- Mutating actions spend a per-client token, keyed on the client IP: 20/min
  each for the feed, the shared counter and the pings on `/live`, 30/min for
  the vote on `/actions`. Dropping the session cookie buys no fresh budget.
