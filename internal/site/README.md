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
  Secure cookies, so set it only behind HTTPS. Unset accepts action POSTs from
  any origin and warns at boot.

`/healthz` answers `ok <version>`; the playbook stamps the short commit hash
via `-ldflags "-X main.version=..."`, suffixed `-dirty` when the tree is not
clean. `/robots.txt` and `/favicon.ico` are served next to it.

## Deploy

`deploy/playbook.yml` cross-builds locally, installs under systemd behind
Caddy, mints the session key once, and ends by probing `/healthz` on the
service and then on the public origin. Needs a Debian-family host reachable as
root, DNS for `domain` and `www.domain` already pointing at it, and
ansible-core 2.15+.

```sh
cd internal/site/deploy
ansible-playbook -i 203.0.113.10, playbook.yml
```

Override `domain` or `bind` with `-e`; `-e check_public=false` skips the public
probe, for a host DNS does not point at yet.

## Layout

- `main.go` — the server: configuration from the environment, signals,
  shutdown.
- `site/` — router options, mounts, static serving. `site.New` is what
  `main.go` and the tests both build.
- `shell/` — chrome: `layout.go`, `nav.go` (`Nav`, `NavFor`), `meta.go`.
- `content/` — one mounted page per file, plus `page.go` for the wiring they
  share.
- `demos/` — one demo per file, embedded verbatim for its Source tab.
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
