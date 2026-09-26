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
- `VIA_ORIGIN` — the site's origin (`https://go-via.dev`). Also forces
  Secure cookies, so set it only behind HTTPS. Unset, actions accept any origin
  and via and the site each log that at startup; set it in production.
- `VIA_BASE` — path prefix this build serves under, empty for the latest. See
  [`DEPLOY.md`](./DEPLOY.md#versions).
- `VIA_VERSIONS` — `label=base` pairs for the version picker, latest first.
  See [`DEPLOY.md`](./DEPLOY.md#versions).

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
- `shell/` — chrome: `layout.go` (sidebar, version picker), `nav.go` (`Nav`,
  `NavFor`), `meta.go`.
- `search/` — full-text search: an index built from the rendered pages at
  startup, and the sidebar box that queries it.
- `content/` — one mounted page per file, plus `page.go` for the wiring they
  share.
- `demos/` — one demo per file, embedded verbatim for its card's Source.
  `shared_contract.go` holds what the demos use without declaring.
- `demo/` — the card, rate limits and resets.
- `snippet/` — code blocks and chroma highlighting. `snippet/src/` is the Go
  the pages show, compiled and tested. `snippet/gen` generates `static/chroma.css`,
  keeping chroma's styles out of the binary.
- `icon/` — the inline SVG icons.
- `static/` — `site.css`, `chroma.css`, `islands.css`, the inspector and
  island scripts, `brand/`, `fonts/`, `vendor/` (MapLibre) and `data/` (the
  island's GeoJSON), served from `embed`.

This is a nested module (`go-via.dev/site`, `replace ../..`) so chroma stays
out of via's `go.mod`; the root `go build ./...` does not see it. No Node, no
asset pipeline. `static/chroma.css` is generated: `go generate ./snippet`, and a
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

- `demos.ResetAll` runs every 15 minutes. It zeroes the shared counter on
  `/live`, the counter on `/start`, the front page's counter and the vote
  tallies. The first two are published, so open tabs redraw; the others have
  no topic, so a tab redraws them on its next click or reload.
- Per-tab lists cap at `keepRows` (50) and are never reset.
- Mutating actions spend a per-client token, keyed on the client IP. Dropping
  the session cookie buys no fresh budget.
  - 20/min: the feed, shared counter and pings on `/live`; the tutorial chat.
  - 30/min: the vote on `/actions`; the counter on `/start`.
  - 10/min: the upload on `/actions`.
  - None: the front page's counter.
